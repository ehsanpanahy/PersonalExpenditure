package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/supabase"
)

const (
	testExpenseID  = "40000000-0000-0000-0000-000000000001"
	testCategoryID = "20000000-0000-0000-0000-000000000001"
	testPeriodID   = "30000000-0000-0000-0000-000000000001"
	testPayerID    = "11111111-1111-1111-1111-111111111111"
	testMemberID   = "22222222-2222-2222-2222-222222222222"
)

func testVerifier() verifierFunc {
	return verifierFunc(func(_ context.Context, token string) (auth.Principal, error) {
		if token != "access-token" {
			return auth.Principal{}, auth.ErrInvalidToken
		}
		return auth.Principal{UserID: "verified-user"}, nil
	})
}

func decodeRPCBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/rest/v1/rpc/create_expense" {
		t.Fatalf("request = %s %s, want POST rpc/create_expense", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
		t.Fatalf("Authorization = %q, want caller JWT", got)
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode RPC body: %v", err)
	}
	return body
}

func TestCreateExpenseForwardsPersonalCreateUnderCallerJWT(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRPCBody(t, r)
		if body["p_id"] != testExpenseID {
			t.Errorf("p_id = %v", body["p_id"])
		}
		if body["p_period_id"] != nil {
			t.Errorf("p_period_id = %v, want null for personal", body["p_period_id"])
		}
		if body["p_category_id"] != testCategoryID {
			t.Errorf("p_category_id = %v", body["p_category_id"])
		}
		if body["p_paid_by"] != testPayerID {
			t.Errorf("p_paid_by = %v", body["p_paid_by"])
		}
		// bigint crosses to Postgres as a JSON number, never a float string.
		if body["p_amount_minor"] != float64(1250) {
			t.Errorf("p_amount_minor = %#v, want 1250", body["p_amount_minor"])
		}
		splits, ok := body["p_splits"].([]any)
		if !ok || len(splits) != 1 {
			t.Fatalf("p_splits = %#v, want one split", body["p_splits"])
		}
		split := splits[0].(map[string]any)
		if split["user_id"] != testPayerID || split["amount_minor"] != float64(1250) {
			t.Errorf("p_splits[0] = %#v", split)
		}
		for _, forbidden := range []string{"user_id", "p_user_id", "author", "amount_minor"} {
			if _, exists := body[forbidden]; exists {
				t.Errorf("RPC body carries %q: identity/amounts use p_ params only", forbidden)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"` + testExpenseID + `"`))
	}))
	defer upstream.Close()

	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())
	request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(`{
		"id": "`+testExpenseID+`",
		"period_id": null,
		"category_id": "`+testCategoryID+`",
		"paid_by": "`+testPayerID+`",
		"amount_minor": "1250",
		"currency": "USD",
		"occurred_on": "2026-03-14",
		"note": "groceries",
		"splits": [{"user_id": "`+testPayerID+`", "amount_minor": "1250"}]
	}`))
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result["id"] != testExpenseID {
		t.Fatalf("id = %q, want expense id", result["id"])
	}
}

func TestCreateExpenseForwardsSharedSplitsExactly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRPCBody(t, r)
		if body["p_period_id"] != testPeriodID {
			t.Errorf("p_period_id = %v", body["p_period_id"])
		}
		if body["p_category_id"] != nil {
			t.Errorf("p_category_id = %v, want null for shared", body["p_category_id"])
		}
		splits := body["p_splits"].([]any)
		if len(splits) != 3 {
			t.Fatalf("splits = %d, want 3", len(splits))
		}
		var sum float64
		for _, item := range splits {
			sum += item.(map[string]any)["amount_minor"].(float64)
		}
		if sum != 1000 || body["p_amount_minor"] != float64(1000) {
			t.Errorf("split sum = %v, total = %v, want 1000", sum, body["p_amount_minor"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"` + testExpenseID + `"`))
	}))
	defer upstream.Close()

	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())
	request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(`{
		"id": "`+testExpenseID+`",
		"period_id": "`+testPeriodID+`",
		"category_id": null,
		"paid_by": "`+testPayerID+`",
		"amount_minor": "1000",
		"currency": "USD",
		"occurred_on": "2026-03-10",
		"note": null,
		"splits": [
			{"user_id": "`+testPayerID+`", "amount_minor": "334"},
			{"user_id": "`+testMemberID+`", "amount_minor": "333"},
			{"user_id": "`+testCategoryID+`", "amount_minor": "333"}
		]
	}`))
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
}

func TestCreateExpenseRejectsMalformedBodiesWithoutUpstreamCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream called for a malformed body")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer upstream.Close()
	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())

	valid := `{"id": "` + testExpenseID + `", "paid_by": "` + testPayerID + `", "amount_minor": "1250", "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "` + testPayerID + `", "amount_minor": "1250"}]}`
	mutate := func(body string, fn func(map[string]any) map[string]any) string {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(fn(parsed))
		return string(raw)
	}
	cases := []struct {
		name string
		body string
	}{
		{"empty", ``},
		{"not JSON", `not json`},
		{"missing splits", mutate(valid, func(m map[string]any) map[string]any { delete(m, "splits"); return m })},
		{"empty splits", mutate(valid, func(m map[string]any) map[string]any { m["splits"] = []any{}; return m })},
		{"fractional amount", mutate(valid, func(m map[string]any) map[string]any { m["amount_minor"] = "12.50"; return m })},
		{"zero amount", mutate(valid, func(m map[string]any) map[string]any { m["amount_minor"] = "0"; return m })},
		{"negative amount", mutate(valid, func(m map[string]any) map[string]any { m["amount_minor"] = "-5"; return m })},
		{"bigint overflow", mutate(valid, func(m map[string]any) map[string]any { m["amount_minor"] = "9223372036854775808"; return m })},
		{"numeric amount loses precision", `{"id": "` + testExpenseID + `", "paid_by": "` + testPayerID + `", "amount_minor": 1250, "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "` + testPayerID + `", "amount_minor": "1250"}]}`},
		{"short split total", mutate(valid, func(m map[string]any) map[string]any {
			m["splits"] = []any{map[string]any{"user_id": testPayerID, "amount_minor": "1249"}}; return m
		})},
		{"zero share", mutate(valid, func(m map[string]any) map[string]any {
			m["amount_minor"] = "1250"
			m["splits"] = []any{map[string]any{"user_id": testPayerID, "amount_minor": "1250"}, map[string]any{"user_id": testMemberID, "amount_minor": "0"}}; return m
		})},
		{"duplicate participant", mutate(valid, func(m map[string]any) map[string]any {
			m["amount_minor"] = "2500"
			m["splits"] = []any{map[string]any{"user_id": testPayerID, "amount_minor": "1250"}, map[string]any{"user_id": testPayerID, "amount_minor": "1250"}}; return m
		})},
		{"bad id", mutate(valid, func(m map[string]any) map[string]any { m["id"] = "not-a-uuid"; return m })},
		{"bad paid_by", mutate(valid, func(m map[string]any) map[string]any { m["paid_by"] = "x"; return m })},
		{"bad currency", mutate(valid, func(m map[string]any) map[string]any { m["currency"] = "usd"; return m })},
		{"bad date", mutate(valid, func(m map[string]any) map[string]any { m["occurred_on"] = "2026-02-30"; return m })},
		{"body-supplied identity is not a field", mutate(valid, func(m map[string]any) map[string]any { m["user_id"] = "attacker"; return m })},
		{"trailing data", valid + ` {}`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(tt.body))
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if envelope.Error.Code != "invalid_expense" {
				t.Fatalf("error code = %q, want invalid_expense", envelope.Error.Code)
			}
		})
	}
}

func TestCreateExpenseMapsRPCErrors(t *testing.T) {
	cases := []struct {
		name       string
		rpcStatus  int
		rpcBody    string
		wantStatus int
		wantCode   string
	}{
		{"validation failure", http.StatusBadRequest, `{"code":"23514","message":"splits total 999 but expense total is 1000: must match exactly"}`, http.StatusBadRequest, "invalid_expense"},
		{"not a member", http.StatusBadRequest, `{"code":"42501","message":"caller is not a member of this period"}`, http.StatusForbidden, "forbidden"},
		{"id conflict", http.StatusBadRequest, `{"code":"23505","message":"expense id x already exists with a different payload"}`, http.StatusConflict, "expense_id_conflict"},
		{"unknown pg code", http.StatusBadRequest, `{"code":"XX000","message":"internal error"}`, http.StatusBadGateway, "data_provider_error"},
		{"non-JSON error", http.StatusBadRequest, `not json`, http.StatusBadGateway, "data_provider_error"},
		{"postgrest unauthorized", http.StatusUnauthorized, `{"code":"PGRST303","message":"expired token"}`, http.StatusUnauthorized, "unauthorized"},
		{"postgrest forbidden", http.StatusForbidden, `{"message":"denied"}`, http.StatusForbidden, "forbidden"},
		{"postgrest outage", http.StatusInternalServerError, `{"message":"down"}`, http.StatusBadGateway, "data_provider_error"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.rpcStatus)
				_, _ = w.Write([]byte(tt.rpcBody))
			}))
			defer upstream.Close()
			handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())
			request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(
				`{"id": "`+testExpenseID+`", "paid_by": "`+testPayerID+`", "amount_minor": "1250", "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "`+testPayerID+`", "amount_minor": "1250"}]}`))
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, tt.wantStatus, response.Body.String())
			}
			var envelope errorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if envelope.Error.Code != tt.wantCode {
				t.Fatalf("error code = %q, want %q", envelope.Error.Code, tt.wantCode)
			}
		})
	}
}

func TestCreateExpensePassesThroughRPCValidationMessage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"23514","message":"occurred_on 2026-04-01 is outside the period [2026-01-01 .. ongoing]","details":null,"hint":null}`))
	}))
	defer upstream.Close()
	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())
	request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(
		`{"id": "`+testExpenseID+`", "period_id": "`+testPeriodID+`", "paid_by": "`+testPayerID+`", "amount_minor": "500", "currency": "USD", "occurred_on": "2026-04-01", "splits": [{"user_id": "`+testPayerID+`", "amount_minor": "500"}]}`))
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "outside the period") {
		t.Fatalf("RPC message not passed through: %s", body)
	}
}

func TestCreateExpenseRejectsInvalidSuccessPayload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testExpenseID + `"}`))
	}))
	defer upstream.Close()
	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())
	request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(
		`{"id": "`+testExpenseID+`", "paid_by": "`+testPayerID+`", "amount_minor": "1250", "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "`+testPayerID+`", "amount_minor": "1250"}]}`))
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for a non-uuid RPC result", response.Code)
	}
}

func TestCreateExpenseRequiresAuthAndBoundsBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream called without auth")
	}))
	defer upstream.Close()
	handler := NewRouter(testVerifier(), supabase.NewClient(upstream.URL, "public-key", upstream.Client()), quietLogger())

	request := httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", response.Code)
	}

	huge := `{"id": "` + testExpenseID + `", "note": "` + strings.Repeat("n", maxCreateExpenseBody) + `"}` 
	request = httptest.NewRequest(http.MethodPost, "/v1/expenses", strings.NewReader(huge))
	request.Header.Set("Authorization", "Bearer access-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", response.Code)
	}
}

func TestParseCreateExpenseBodyAcceptsBigintBoundary(t *testing.T) {
	raw := `{"id": "` + testExpenseID + `", "paid_by": "` + testPayerID + `", "amount_minor": "9223372036854775807", "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "` + testPayerID + `", "amount_minor": "9223372036854775807"}]}`
	input, reqErr := parseCreateExpenseBody([]byte(raw))
	if reqErr != nil {
		t.Fatalf("max bigint rejected: %v", reqErr)
	}
	if input.Amount != 9223372036854775807 {
		t.Fatalf("amount = %d", input.Amount)
	}
	// Two max-bigint shares against a max-bigint total: a mismatch, never an overflow.
	raw = `{"id": "` + testExpenseID + `", "paid_by": "` + testPayerID + `", "amount_minor": "9223372036854775807", "currency": "USD", "occurred_on": "2026-03-14", "splits": [{"user_id": "` + testPayerID + `", "amount_minor": "9223372036854775807"}, {"user_id": "` + testMemberID + `", "amount_minor": "9223372036854775807"}]}`
	if _, reqErr := parseCreateExpenseBody([]byte(raw)); reqErr == nil || reqErr.code != "invalid_expense" {
		t.Fatalf("overflowing split sum reminder = %+v, want invalid_expense", reqErr)
	}
}
