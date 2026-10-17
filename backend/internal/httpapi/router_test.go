package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/supabase"
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
	handler := NewRouter(verifier, nil, quietLogger())
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
	handler := NewRouter(verifier, nil, quietLogger())
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
	handler := NewRouter(verifier, nil, quietLogger())
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
	handler := NewRouter(verifier, nil, quietLogger())
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
	handler := NewRouter(verifier, nil, quietLogger())
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

func TestListPersonalExpensesUsesCallerJWTAndPreservesBigint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/v1/expenses" {
			t.Errorf("path = %q, want /rest/v1/expenses", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("Authorization = %q, want caller token", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("period_id") != "is.null" || r.URL.Query().Get("deleted_at") != "is.null" {
			t.Errorf("query must exclude shared and deleted expenses: %s", r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("and"); got != "(occurred_on.gte.2026-03-01,occurred_on.lte.2026-03-31)" {
			t.Errorf("date filter = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"expense-id","amount_minor":9007199254740993,"currency":"USD","occurred_on":"2026-03-14","category_id":null,"note":null,"created_at":"2026-03-14T10:00:00Z"}]`))
	}))
	defer upstream.Close()

	verifier := verifierFunc(func(_ context.Context, token string) (auth.Principal, error) {
		if token != "access-token" {
			t.Errorf("token = %q, want access-token", token)
		}
		return auth.Principal{UserID: "verified-user"}, nil
	})
	client := supabase.NewClient(upstream.URL, "public-key", upstream.Client())
	handler := NewRouter(verifier, client, quietLogger())
	request := httptest.NewRequest(http.MethodGet, "/v1/expenses?from=2026-03-01&to=2026-03-31", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var result []map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := result[0]["amount_minor"]; got != "9007199254740993" {
		t.Fatalf("amount_minor = %#v, want exact decimal string", got)
	}
}

func TestListPersonalExpensesRejectsInvalidDateRange(t *testing.T) {
	verifier := verifierFunc(func(context.Context, string) (auth.Principal, error) {
		return auth.Principal{UserID: "verified-user"}, nil
	})
	client := supabase.NewClient("http://127.0.0.1:1", "key", http.DefaultClient)
	handler := NewRouter(verifier, client, quietLogger())
	request := httptest.NewRequest(http.MethodGet, "/v1/expenses?from=2026-03-31&to=2026-03-01", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != "invalid_query" {
		t.Fatalf("error code = %q, want invalid_query", envelope.Error.Code)
	}
}

func TestParseExpenseListQueryBoundsPagination(t *testing.T) {
	_, _, _, _, err := parseExpenseListQuery(url.Values{
		"from": {"2026-03-01"}, "to": {"2026-03-31"}, "limit": {"101"},
	})
	if err == nil {
		t.Fatal("expected out-of-range limit to be rejected")
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDecodeCategoryTrimsNameAndRequiresStrictValidPayload(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/categories", strings.NewReader(`{"name":"  Food  ","sort_order":2,"icon":"fork","color":"blue"}`))
	input, err := decodeCategory(r)
	if err != nil {
		t.Fatalf("decodeCategory() error = %v", err)
	}
	if input.Name != "Food" || input.SortOrder != 2 || input.Icon == nil || *input.Icon != "fork" {
		t.Fatalf("decoded input = %#v", input)
	}
	for _, body := range []string{
		`{"name":"Food"}`,
		`{"name":"Food","sort_order":-1}`,
		`{"name":"Food","sort_order":0,"user_id":"attacker"}`,
		`{"name":"Food","sort_order":0} {"name":"other"}`,
		`{"name":"   ","sort_order":0}`,
	} {
		t.Run(body, func(t *testing.T) {
			if _, err := decodeCategory(httptest.NewRequest(http.MethodPost, "/v1/categories", strings.NewReader(body))); err == nil {
				t.Fatalf("decodeCategory(%s) unexpectedly succeeded", body)
			}
		})
	}
}

func TestCategoryHandlersRejectUnauthenticatedRequestsBeforeStore(t *testing.T) {
	logger := quietLogger()
	response := httptest.NewRecorder()
	listCategories(nil, logger)(response, httptest.NewRequest(http.MethodGet, "/v1/categories", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("list status = %d, want 401", response.Code)
	}
	response = httptest.NewRecorder()
	createCategory(nil, logger)(response, httptest.NewRequest(http.MethodPost, "/v1/categories", strings.NewReader(`{"name":"Food","sort_order":0,"owner":"forged"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("create status = %d, want 401", response.Code)
	}
}

func TestWriteCategoryErrorMapsConflictAndMissingCategory(t *testing.T) {
	for _, test := range []struct {
		err    error
		code   string
		status int
	}{
		{postgres.ErrCategoryConflict, "category_name_conflict", http.StatusConflict},
		{postgres.ErrCategoryUnavailable, "category_not_found", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		writeCategoryError(response, test.err, quietLogger())
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
			t.Errorf("response = %d %q, want %d with %q", response.Code, response.Body.String(), test.status, test.code)
		}
	}
}
