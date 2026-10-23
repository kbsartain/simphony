package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testOperatorToken = "test-operator-token-32-characters-long"

func TestSecurityMiddlewareRequiresAuthentication(t *testing.T) {
	options := securityOptions{operatorToken: testOperatorToken, trustedOrigins: map[string]struct{}{}, limiter: newAuthFailureLimiter()}
	handler := options.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/state", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/state", nil)
	request.Header.Set("Authorization", "Bearer "+testOperatorToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want 204", response.Code)
	}
}

func TestSecurityMiddlewareRejectsCrossOriginMutation(t *testing.T) {
	options := securityOptions{operatorToken: testOperatorToken, trustedOrigins: map[string]struct{}{}, limiter: newAuthFailureLimiter()}
	handler := options.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/refresh", nil)
	request.Header.Set("Authorization", "Bearer "+testOperatorToken)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestSecurityMiddlewareRejectsFormMutation(t *testing.T) {
	options := securityOptions{operatorToken: testOperatorToken, trustedOrigins: map[string]struct{}{}, limiter: newAuthFailureLimiter()}
	handler := options.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/settings", strings.NewReader("x=y"))
	request.Header.Set("Authorization", "Bearer "+testOperatorToken)
	request.Header.Set("Origin", "http://127.0.0.1")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", response.Code)
	}
}

func TestDecodeJSONRejectsUnknownAndTrailingValues(t *testing.T) {
	for _, body := range []string{`{"known":true,"unknown":1}`, `{"known":true}{"known":false}`} {
		request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
		response := httptest.NewRecorder()
		var target struct {
			Known bool `json:"known"`
		}
		if err := decodeJSON(response, request, &target); err == nil {
			t.Fatalf("decodeJSON(%q) succeeded, want error", body)
		}
	}
}

func TestDecodeJSONReportsOversizedBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"value":"`+strings.Repeat("x", maxRequestBodySize)+`"}`))
	response := httptest.NewRecorder()
	var target struct {
		Value string `json:"value"`
	}
	err := decodeJSON(response, request, &target)
	if err == nil {
		t.Fatal("oversized request succeeded")
	}
	status, apiErr := requestDecodeError(err)
	if status != http.StatusRequestEntityTooLarge || apiErr.Code != "request_too_large" {
		t.Fatalf("decode error mapped to %d/%s, want 413/request_too_large", status, apiErr.Code)
	}
}

func TestSecurityValidationRequiresTokenAndLoopback(t *testing.T) {
	if err := (securityOptions{}).validate("127.0.0.1"); err == nil {
		t.Fatal("validation succeeded without token")
	}
	options := securityOptions{operatorToken: testOperatorToken}
	if err := options.validate("127.0.0.1"); err != nil {
		t.Fatalf("loopback validation failed: %v", err)
	}
	if err := options.validate("0.0.0.0"); err == nil {
		t.Fatal("remote bind validation succeeded")
	}
}

func TestAuthFailureLimiterIsGloballyAndMemoryBounded(t *testing.T) {
	limiter := newAuthFailureLimiter()
	now := time.Now()
	for i := 0; i < maxAuthFailureClients+50; i++ {
		limiter.recordFailure(fmt.Sprintf("client-%d", i), now.Add(time.Duration(i)*time.Nanosecond))
	}
	if got := len(limiter.clients); got != maxAuthFailureClients {
		t.Fatalf("tracked clients = %d, want %d", got, maxAuthFailureClients)
	}
	if limiter.allow("new-client", now.Add(time.Second)) {
		t.Fatal("global limiter allowed requests after the global failure threshold")
	}
	if !limiter.allow("new-client", now.Add(2*time.Minute)) {
		t.Fatal("global limiter did not reset after its window")
	}
}

func TestMergeMaskedSecretsDoesNotCrossOrigins(t *testing.T) {
	current := map[string]interface{}{
		"tracker": map[string]interface{}{
			"endpoint": "https://api.linear.app/graphql",
			"api_key":  "$LINEAR_API_KEY",
		},
	}
	changedOrigin := map[string]interface{}{
		"tracker": map[string]interface{}{
			"endpoint": "https://attacker.example/graphql",
			"api_key":  settingsSecretMask,
		},
	}
	merged := mergeMaskedSecrets(changedOrigin, current)
	if got := nestedString(merged, []string{"tracker", "api_key"}); got != settingsSecretMask {
		t.Fatalf("changed-origin credential = %q, want mask retained for validation failure", got)
	}

	sameOrigin := map[string]interface{}{
		"tracker": map[string]interface{}{
			"endpoint": "https://api.linear.app/v2/graphql",
			"api_key":  settingsSecretMask,
		},
	}
	merged = mergeMaskedSecrets(sameOrigin, current)
	if got := nestedString(merged, []string{"tracker", "api_key"}); got != "$LINEAR_API_KEY" {
		t.Fatalf("same-origin credential = %q, want preserved reference", got)
	}
}
