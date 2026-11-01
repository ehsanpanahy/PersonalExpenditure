package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
)

func TestDecodeCreatePeriodAcceptsOngoingPeriodAndExactMemberEmails(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/periods", strings.NewReader(`{"name":"Spring trip","starts_on":"2026-03-01","ends_on":null,"currency":"USD","member_emails":["a@example.com","b@example.com"],"manager_email":"b@example.com"}`))
	in, err := decodeCreatePeriod(request)
	if err != nil {
		t.Fatalf("decodeCreatePeriod() error = %v", err)
	}
	if in.Name != "Spring trip" || in.StartsOn != "2026-03-01" || in.EndsOn != nil || in.Currency != "USD" {
		t.Fatalf("decoded period fields = %#v", in)
	}
	if len(in.MemberEmails) != 2 || in.MemberEmails[0] != "a@example.com" || in.ManagerEmail != "b@example.com" {
		t.Fatalf("decoded roster = %#v, manager = %q", in.MemberEmails, in.ManagerEmail)
	}
}

func TestDecodeCreatePeriodRejectsInvalidOrAmbiguousPayloads(t *testing.T) {
	valid := `{"name":"Trip","starts_on":"2026-03-01","ends_on":"2026-03-02","currency":"USD","member_emails":["a@example.com"],"manager_email":"a@example.com"}`
	cases := []struct{ name, body string }{
		{"end before start", strings.Replace(valid, `2026-03-02`, `2026-02-28`, 1)},
		{"invalid calendar date", strings.Replace(valid, `2026-03-01`, `2026-02-30`, 1)},
		{"duplicate exact-email roster", strings.Replace(valid, `["a@example.com"]`, `["a@example.com","A@example.com"]`, 1)},
		{"malformed email", strings.Replace(valid, `a@example.com`, `not-an-email`, 1)},
		{"unknown identity field", strings.TrimSuffix(valid, `}`) + `,"user_id":"attacker"}`},
		{"manager missing", strings.Replace(valid, `,"manager_email":"a@example.com"`, ``, 1)},
		{"non-date end", strings.Replace(valid, `2026-03-02`, `soon`, 1)},
		{"trailing JSON", valid + ` {}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/periods", strings.NewReader(tc.body))
			if _, err := decodeCreatePeriod(request); err == nil {
				t.Fatalf("decodeCreatePeriod(%s) unexpectedly succeeded", tc.body)
			}
		})
	}
}

func TestPeriodEndpointsRequireAuthenticationBeforeStoreAccess(t *testing.T) {
	handler := NewRouterWithPostgres(testVerifier(), nil, postgres.NewStore(nil), quietLogger())
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/periods", ""},
		{http.MethodPost, "/v1/periods", `{}`},
		{http.MethodGet, "/v1/periods/30000000-0000-0000-0000-000000000001", ""},
		{http.MethodPatch, "/v1/periods/30000000-0000-0000-0000-000000000001/manager", `{}`},
		{http.MethodDelete, "/v1/periods/30000000-0000-0000-0000-000000000001", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestWritePeriodErrorHidesUnavailablePeriodAndMapsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{postgres.ErrInvalidPeriod, http.StatusBadRequest, "invalid_period"},
		{postgres.ErrPeriodUnavailable, http.StatusNotFound, "period_not_found"},
	} {
		response := httptest.NewRecorder()
		writePeriodError(response, tc.err, quietLogger())
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.code) {
			t.Errorf("response = %d %q, want %d with %q", response.Code, response.Body.String(), tc.status, tc.code)
		}
	}
}
