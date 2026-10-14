package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSupabaseVerifierUsesAuthResponseIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/auth/v1/user" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("apikey"); got != "public-key" {
			t.Errorf("apikey = %q, want public-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer signed-token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"verified-user","sub":"untrusted-subject"}`))
	}))
	defer server.Close()

	verifier := NewSupabaseVerifier(server.URL, "public-key", server.Client())
	principal, err := verifier.Verify(context.Background(), "signed-token")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.UserID != "verified-user" {
		t.Fatalf("UserID = %q, want verified-user", principal.UserID)
	}
}

func TestSupabaseVerifierClassifiesResponses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantError error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantError: ErrInvalidToken},
		{name: "forbidden", status: http.StatusForbidden, wantError: ErrInvalidToken},
		{name: "server failure", status: http.StatusBadGateway, wantError: ErrVerifierUnavailable},
		{name: "empty identity", status: http.StatusOK, body: `{"id":" "}`, wantError: ErrInvalidToken},
		{name: "bad json", status: http.StatusOK, body: `{`, wantError: ErrVerifierUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			verifier := NewSupabaseVerifier(server.URL, "key", server.Client())
			_, err := verifier.Verify(context.Background(), "token")
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Verify() error = %v, want errors.Is(_, %v)", err, tt.wantError)
			}
		})
	}
}

func TestSupabaseVerifierRejectsBlankTokenWithoutRequest(t *testing.T) {
	verifier := NewSupabaseVerifier("http://127.0.0.1:1", "key", http.DefaultClient)
	_, err := verifier.Verify(context.Background(), "  ")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestParseBearerToken(t *testing.T) {
	for _, tt := range []struct {
		header string
		want   string
		ok     bool
	}{
		{header: "Bearer abc", want: "abc", ok: true},
		{header: "bearer abc", want: "abc", ok: true},
		{header: "Bearer", ok: false},
		{header: "Basic abc", ok: false},
		{header: "Bearer abc extra", ok: false},
		{header: "Bearer " + strings.Repeat("x", 0), ok: false},
	} {
		got, ok := ParseBearerToken(tt.header)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseBearerToken(%q) = (%q, %v), want (%q, %v)", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}
