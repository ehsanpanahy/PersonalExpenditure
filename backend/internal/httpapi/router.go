package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/supabase"
)

func NewRouter(verifier auth.Verifier, supabaseClient *supabase.Client, logger *slog.Logger) http.Handler {
	return newRouter(verifier, supabaseClient, nil, logger)
}

func NewRouterWithPostgres(verifier auth.Verifier, supabaseClient *supabase.Client, store *postgres.Store, logger *slog.Logger) http.Handler {
	return newRouter(verifier, supabaseClient, store, logger)
}

func newRouter(verifier auth.Verifier, supabaseClient *supabase.Client, store *postgres.Store, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("GET /v1/me", requireAuth(verifier, logger)(http.HandlerFunc(meHandler)))
	mux.Handle("GET /v1/expenses", requireAuth(verifier, logger)(http.HandlerFunc(listPersonalExpenses(supabaseClient, logger))))
	createHandler := createExpense(supabaseClient, logger)
	if store != nil {
		createHandler = createExpenseWithPostgres(store, logger)
	}
	mux.Handle("POST /v1/expenses", requireAuth(verifier, logger)(http.HandlerFunc(createHandler)))
	return mux
}

type meResponse struct {
	UserID string `json:"user_id"`
}

func meHandler(w http.ResponseWriter, r *http.Request) {
	session, ok := auth.SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(meResponse{UserID: session.Principal.UserID})
}

func requireAuth(verifier auth.Verifier, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accessToken, ok := auth.ParseBearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
				return
			}

			principal, err := verifier.Verify(r.Context(), accessToken)
			if err != nil {
				if errors.Is(err, auth.ErrInvalidToken) {
					writeError(w, http.StatusUnauthorized, "unauthorized", "The access token is invalid or expired.")
					return
				}
				logger.Error("identity provider verification failed", "error", err)
				writeError(w, http.StatusServiceUnavailable, "identity_provider_unavailable", "Authentication is temporarily unavailable.")
				return
			}

			session := auth.Session{Principal: principal, AccessToken: accessToken}
			next.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), session)))
		})
	}
}
