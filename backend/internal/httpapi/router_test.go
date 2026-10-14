package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

type verifierFunc func(context.Context, string) (auth.Principal, error)

func (f verifierFunc) Verify(ctx context.Context, token string) (auth.Principal, error) {
	return f(ctx, token)
}

func TestRouterHealthzIsPublic(t *testing.T) {
	verifier := verifierFunc(func(context.Context, string) (auth.Principal, error) {
		t.Fatal("verifier called for public health route")
		return auth.Principal{}, nil
	})
	handler := NewRouter(verifier, quietLogger())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestRouterMeVerifiesTokenAndReturnsVerifiedIdentity(t *testing.T) {
	verifier := verifierFunc(func(_ context.Context, token string) (auth.Principal, error) {
		if token != "access-token" {
			t.Errorf("token = %q, want access-token", token)
		}
		return auth.Principal{UserID: "verified-user"}, nil
	})
	handler := NewRouter(verifier, quietLogger())
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var result meResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.UserID != "verified-user" {
		t.Fatalf("user_id = %q, want verified-user", result.UserID)
	}
}

func TestRouterMeRejectsMissingOrInvalidToken(t *testing.T) {
	verifier := verifierFunc(func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{}, auth.ErrInvalidToken
	})
	handler := NewRouter(verifier, quietLogger())
	for _, tt := range []struct {
		name   string
		header string
	}{
		{name: "missing"},
		{name: "invalid", header: "Bearer bad-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			request.Header.Set("Authorization", tt.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
		})
	}
}

func TestRouterMeReturnsUnavailableWhenVerifierFails(t *testing.T) {
	verifier := verifierFunc(func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{}, auth.ErrVerifierUnavailable
	})
	handler := NewRouter(verifier, quietLogger())
	request := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestRouterMeDoesNotTrustCallerSuppliedIdentity(t *testing.T) {
	verifier := verifierFunc(func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{UserID: "verified-user"}, nil
	})
	handler := NewRouter(verifier, quietLogger())
	request := httptest.NewRequest(http.MethodGet, "/v1/me?user_id=attacker", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result meResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.UserID != "verified-user" {
		t.Fatalf("user_id = %q, want verified-user", result.UserID)
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
