package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateCredentialURLRejectsUnsafeDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/v1",
		"https://127.0.0.1/v1",
		"https://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
		"https://user:password@example.com/v1",
	} {
		if _, _, err := ValidateCredentialURL(endpoint); err == nil {
			t.Fatalf("ValidateCredentialURL(%q) succeeded, want error", endpoint)
		}
	}
}

func TestValidateCredentialURLAllowsExactDevelopmentOrigin(t *testing.T) {
	t.Setenv(trustedOutboundOriginsEnv, "http://127.0.0.1:9090")
	if _, allowPrivate, err := ValidateCredentialURL("http://127.0.0.1:9090/v1/models"); err != nil || !allowPrivate {
		t.Fatalf("validation = allowPrivate %t, err %v", allowPrivate, err)
	}
	if _, _, err := ValidateCredentialURL("http://127.0.0.1:9091/v1/models"); err == nil {
		t.Fatal("different port unexpectedly trusted")
	}
}

func TestSameOriginUsesEffectivePorts(t *testing.T) {
	if !SameOrigin("https://example.com/v1", "https://EXAMPLE.com:443/v2") {
		t.Fatal("same HTTPS origin was not recognized")
	}
	if SameOrigin("https://example.com", "https://example.com:444") {
		t.Fatal("different ports were treated as the same origin")
	}
}

func TestReadAllLimited(t *testing.T) {
	if _, err := ReadAllLimited(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("oversized response was accepted")
	}
	data, err := ReadAllLimited(strings.NewReader("1234"), 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("bounded response = %q, %v", data, err)
	}
}

func TestSecureHTTPClientRejectsCrossOriginRedirectWithoutForwardingCredential(t *testing.T) {
	receivedCredential := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCredential = r.Header.Get("Authorization") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/models", http.StatusFound)
	}))
	defer source.Close()
	t.Setenv(trustedOutboundOriginsEnv, source.URL+","+target.URL)

	client, err := NewSecureHTTPClient(source.URL, 2*time.Second)
	if err != nil {
		t.Fatalf("NewSecureHTTPClient: %v", err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, source.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer sentinel")
	if _, err := client.Do(request); err == nil {
		t.Fatal("cross-origin redirect succeeded")
	}
	if receivedCredential {
		t.Fatal("redirect target received the credential")
	}
}
