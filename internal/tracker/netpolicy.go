package tracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const trustedOutboundOriginsEnv = "SIMPHONY_TRUSTED_OUTBOUND_ORIGINS"

// NewSecureHTTPClient constructs a bounded client pinned to the supplied
// credential-bearing origin. Redirects and resolved addresses are revalidated.
func NewSecureHTTPClient(rawURL string, timeout time.Duration) (*http.Client, error) {
	parsed, allowPrivate, err := ValidateCredentialURL(rawURL)
	if err != nil {
		return nil, err
	}
	origin := URLOrigin(parsed)
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
	}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("split outbound address: %w", err)
		}
		addresses, err := resolveHost(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range addresses {
			if !allowPrivate && isUnsafeIP(ip) {
				return nil, fmt.Errorf("outbound destination %s resolves to a private or special-use address", host)
			}
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("outbound destination %s did not resolve", host)
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("outbound redirect limit exceeded")
			}
			redirectURL, redirectAllowsPrivate, err := ValidateCredentialURL(req.URL.String())
			if err != nil {
				return err
			}
			if URLOrigin(redirectURL) != origin {
				return errors.New("credential-bearing redirect to a different origin is not allowed")
			}
			if redirectAllowsPrivate != allowPrivate {
				return errors.New("redirect changed outbound address policy")
			}
			return nil
		},
	}, nil
}

// ValidateCredentialURL validates the URL shape and returns whether an exact
// operator trust entry permits HTTP or private/special-use addressing.
func ValidateCredentialURL(rawURL string) (*url.URL, bool, error) {
	parsed, err := validateCredentialURLShape(rawURL)
	if err != nil {
		return nil, false, err
	}
	origin := URLOrigin(parsed)
	allowPrivate := trustedOrigins()[origin]
	if parsed.Scheme != "https" && !allowPrivate {
		return nil, false, errors.New("credential-bearing endpoints must use HTTPS unless their exact development origin is explicitly trusted")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && isUnsafeIP(ip) && !allowPrivate {
		return nil, false, errors.New("private or special-use outbound endpoints are not allowed")
	}
	return parsed, allowPrivate, nil
}

// SameOrigin reports whether two URLs share scheme, hostname, and effective port.
func SameOrigin(left string, right string) bool {
	leftURL, leftErr := validateCredentialURLShape(left)
	rightURL, rightErr := validateCredentialURLShape(right)
	return leftErr == nil && rightErr == nil && URLOrigin(leftURL) == URLOrigin(rightURL)
}

// URLOrigin returns an exact normalized scheme/host/effective-port origin.
func URLOrigin(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	port := parsed.Port()
	if port == "" {
		if scheme == "http" {
			port = "80"
		} else if scheme == "https" {
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(parsed.Hostname()), port)
}

// ReadAllLimited reads at most max bytes and errors if the response is larger.
func ReadAllLimited(body io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return nil, errors.New("response limit must be positive")
	}
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("outbound response exceeds %d bytes", max)
	}
	return data, nil
}

func validateCredentialURLShape(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid outbound endpoint %q", rawURL)
	}
	if parsed.User != nil {
		return nil, errors.New("outbound endpoint must not contain userinfo")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("outbound endpoint must not contain a fragment")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" {
		return nil, fmt.Errorf("outbound endpoint scheme %q is not allowed", parsed.Scheme)
	}
	parsed.Scheme = scheme
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed, nil
}

func trustedOrigins() map[string]bool {
	result := make(map[string]bool)
	for _, raw := range strings.Split(os.Getenv(trustedOutboundOriginsEnv), ",") {
		parsed, err := validateCredentialURLShape(raw)
		if err == nil {
			result[URLOrigin(parsed)] = true
		}
	}
	return result
}

func resolveHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve outbound destination %s: %w", host, err)
	}
	result := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, address.IP)
	}
	return result, nil
}

func isUnsafeIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}
