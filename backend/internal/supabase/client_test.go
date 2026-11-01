package supabase

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

func TestClientForwardsCallerJWTAndPublishableKeyToAggregateRPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/v1/rpc/period_balances" {
			t.Errorf("request = %s %s, want POST RPC path", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("source") != "mobile" {
			t.Errorf("source query = %q, want mobile", r.URL.Query().Get("source"))
		}
		if got := r.Header.Get("apikey"); got != "publishable-key" {
			t.Errorf("apikey = %q, want publishable-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer caller-jwt" {
			t.Errorf("Authorization = %q, want caller JWT", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"p_period_id":"period-id"}` {
			t.Errorf("body = %q, want JSON payload", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "publishable-key", server.Client())
	session := auth.Session{Principal: auth.Principal{UserID: "user-1"}, AccessToken: "caller-jwt"}
	response, err := client.Do(context.Background(), http.MethodPost, "rpc/period_balances?source=mobile", session, strings.NewReader(`{"p_period_id":"period-id"}`))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
}

func TestClientRequiresVerifiedSession(t *testing.T) {
	client := NewClient("https://example.invalid", "key", http.DefaultClient)
	for _, session := range []auth.Session{
		{},
		{Principal: auth.Principal{UserID: "user"}},
		{AccessToken: "token"},
	} {
		if _, err := client.Do(context.Background(), http.MethodGet, "expenses", session, nil); err == nil {
			t.Fatal("Do() error = nil, want session error")
		}
	}
}

func TestClientRejectsUnsafeResourcePaths(t *testing.T) {
	client := NewClient("https://example.invalid", "key", http.DefaultClient)
	session := auth.Session{Principal: auth.Principal{UserID: "user"}, AccessToken: "token"}
	for _, resource := range []string{"", "/expenses", "https://attacker.invalid", "../admin", "rpc/../../admin", `expenses\\admin`} {
		if _, err := client.Do(context.Background(), http.MethodGet, resource, session, nil); err == nil {
			t.Errorf("Do(resource=%q) error = nil, want invalid resource", resource)
		}
	}
}
