package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kbsartain/simphony/pkg/api"
)

const (
	operatorTokenEnv      = "SIMPHONY_OPERATOR_TOKEN"
	trustedOriginsEnv     = "SIMPHONY_TRUSTED_ORIGINS"
	maxRequestBodySize    = 1 << 20 // 1 MiB
	maxAuthFailureClients = 1024
	maxAuthFailuresGlobal = 100
)

var errOperatorTokenRequired = errors.New("SIMPHONY_OPERATOR_TOKEN must contain at least 32 characters when the HTTP server is enabled")

type securityOptions struct {
	operatorToken  string
	trustedOrigins map[string]struct{}
	limiter        *authFailureLimiter
}

func securityOptionsFromEnv() securityOptions {
	options := securityOptions{
		operatorToken:  strings.TrimSpace(os.Getenv(operatorTokenEnv)),
		trustedOrigins: make(map[string]struct{}),
		limiter:        newAuthFailureLimiter(),
	}
	for _, raw := range strings.Split(os.Getenv(trustedOriginsEnv), ",") {
		origin, err := normalizeOrigin(raw)
		if err == nil && origin != "" {
			options.trustedOrigins[origin] = struct{}{}
		}
	}
	return options
}

// ValidateHTTPServerSecurity verifies the process-level requirements before
// any project runtime is started.
func ValidateHTTPServerSecurity(bind string) error {
	return securityOptionsFromEnv().validate(bind)
}

func (o securityOptions) validate(bind string) error {
	if len(o.operatorToken) < 32 {
		return errOperatorTokenRequired
	}
	if !isLoopbackBind(bind) {
		return fmt.Errorf("HTTP server bind %q is not loopback; bind Simphony to 127.0.0.1 or ::1 and place an authenticated TLS reverse proxy in front of it", bind)
	}
	return nil
}

func isLoopbackBind(bind string) bool {
	bind = strings.TrimSpace(bind)
	if strings.EqualFold(bind, "localhost") {
		return true
	}
	bind = strings.TrimPrefix(strings.TrimSuffix(bind, "]"), "[")
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

func (o securityOptions) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		originAllowed := o.requestOriginAllowed(r)
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && originAllowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if !originAllowed {
				writeSecurityError(w, http.StatusForbidden, "origin_denied", "Request origin is not trusted")
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Empty tokens are allowed only for handler-level unit fixtures. Start()
		// validates the real listener and refuses to run in this state.
		if o.operatorToken != "" {
			client := requestClientKey(r)
			if o.limiter != nil && !o.limiter.allow(client, time.Now()) {
				w.Header().Set("Retry-After", "60")
				writeSecurityError(w, http.StatusTooManyRequests, "rate_limited", "Too many authentication failures")
				return
			}
			if !o.authenticated(r) {
				if o.limiter != nil {
					o.limiter.recordFailure(client, time.Now())
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="Simphony", charset="UTF-8"`)
				log.Printf("security_audit action=authenticate outcome=denied source=%q path=%q", client, r.URL.Path)
				writeSecurityError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required")
				return
			}
			if o.limiter != nil {
				o.limiter.recordSuccess(client)
			}
		}

		if isUnsafeMethod(r.Method) {
			if !originAllowed {
				log.Printf("security_audit action=mutation outcome=denied reason=origin source=%q path=%q", requestClientKey(r), r.URL.Path)
				writeSecurityError(w, http.StatusForbidden, "origin_denied", "Request origin is not trusted")
				return
			}
			if r.ContentLength > 0 {
				contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
				if contentType != "application/json" {
					writeSecurityError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Mutation bodies must use application/json")
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
			auditWriter := &auditResponseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(auditWriter, r)
			log.Printf("security_audit principal=operator action=mutation outcome=completed source=%q method=%s path=%q status=%d", requestClientKey(r), r.Method, r.URL.Path, auditWriter.status)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type auditResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (o securityOptions) authenticated(r *http.Request) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return false
	}
	want := []byte(o.operatorToken)
	if scheme, value, ok := strings.Cut(header, " "); ok && strings.EqualFold(strings.TrimSpace(scheme), "Bearer") {
		return constantTimeEqual([]byte(strings.TrimSpace(value)), want)
	}
	if username, password, ok := r.BasicAuth(); ok {
		return strings.EqualFold(strings.TrimSpace(username), "simphony") && constantTimeEqual([]byte(password), want)
	}
	return false
}

func constantTimeEqual(got []byte, want []byte) bool {
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func (o securityOptions) requestOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
			parsed, err := url.Parse(referer)
			if err != nil {
				return false
			}
			origin = parsed.Scheme + "://" + parsed.Host
		} else {
			// Non-browser clients authenticate with an Authorization header and do
			// not ordinarily send Origin or Referer.
			return true
		}
	}
	normalized, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	if _, ok := o.trustedOrigins[normalized]; ok {
		return true
	}
	requestScheme := "http"
	if r.TLS != nil {
		requestScheme = "https"
	}
	want, err := normalizeOrigin(requestScheme + "://" + r.Host)
	return err == nil && normalized == want
}

func normalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("invalid origin %q", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin must not contain a path, query, or fragment")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin scheme must be http or https")
	}
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func writeSecurityError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.APIErrorResponse{Error: api.APIError{Code: code, Message: message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func requestDecodeError(err error) (int, api.APIError) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge, api.APIError{Code: "request_too_large", Message: fmt.Sprintf("Request body exceeds %d bytes", maxBytesErr.Limit)}
	}
	return http.StatusBadRequest, api.APIError{Code: "bad_request", Message: "Request body must contain one valid JSON object"}
}

type authFailureLimiter struct {
	mu      sync.Mutex
	clients map[string]authFailureWindow
	global  authFailureWindow
}

type authFailureWindow struct {
	started time.Time
	count   int
}

func newAuthFailureLimiter() *authFailureLimiter {
	return &authFailureLimiter{clients: make(map[string]authFailureWindow)}
}

func (l *authFailureLimiter) allow(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.global.started) < time.Minute && l.global.count >= maxAuthFailuresGlobal {
		return false
	}
	window, ok := l.clients[client]
	if !ok || now.Sub(window.started) >= time.Minute {
		return true
	}
	return window.count < 10
}

func (l *authFailureLimiter) recordFailure(client string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.global.started.IsZero() || now.Sub(l.global.started) >= time.Minute {
		l.global = authFailureWindow{started: now}
	}
	l.global.count++
	window, ok := l.clients[client]
	if !ok || now.Sub(window.started) >= time.Minute {
		window = authFailureWindow{started: now}
	}
	window.count++
	if !ok && len(l.clients) >= maxAuthFailureClients {
		oldestKey := ""
		oldestTime := now
		for key, candidate := range l.clients {
			if oldestKey == "" || candidate.started.Before(oldestTime) {
				oldestKey = key
				oldestTime = candidate.started
			}
		}
		delete(l.clients, oldestKey)
	}
	l.clients[client] = window
}

func (l *authFailureLimiter) recordSuccess(client string) {
	l.mu.Lock()
	delete(l.clients, client)
	l.mu.Unlock()
}

func requestClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr == "" {
		return "local"
	}
	return r.RemoteAddr
}
