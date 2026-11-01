package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
)

const maxPeriodBody = 32 << 10

func listPeriods(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		periods, err := store.ListPeriods(r.Context(), session)
		if err != nil {
			logger.Error("list periods failed", "error", err)
			writeError(w, http.StatusBadGateway, "data_provider_error", "Periods are temporarily unavailable.")
			return
		}
		writeJSON(w, http.StatusOK, periods)
	}
}

func getPeriod(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		period, err := store.GetPeriod(r.Context(), session, r.PathValue("id"))
		if err != nil {
			writePeriodError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, period)
	}
}

func createPeriod(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		input, err := decodeCreatePeriod(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_period", err.Error())
			return
		}
		period, err := store.CreatePeriod(r.Context(), session, input)
		if err != nil {
			writePeriodError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusCreated, period)
	}
}

func transferPeriodManager(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		var body struct {
			ManagerID *string `json:"manager_id"`
		}
		if err := decodePeriodJSON(r, &body); err != nil || body.ManagerID == nil || !uuidPattern.MatchString(*body.ManagerID) {
			writeError(w, http.StatusBadRequest, "invalid_period", "manager_id must be a member UUID.")
			return
		}
		if err := store.TransferPeriodManager(r.Context(), session, r.PathValue("id"), *body.ManagerID); err != nil {
			writePeriodError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func softDeletePeriod(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		if err := store.SoftDeletePeriod(r.Context(), session, r.PathValue("id")); err != nil {
			writePeriodError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeCreatePeriod(r *http.Request) (postgres.CreatePeriodInput, error) {
	var body struct {
		Name         *string         `json:"name"`
		StartsOn     *string         `json:"starts_on"`
		EndsOn       json.RawMessage `json:"ends_on"`
		Currency     *string         `json:"currency"`
		MemberEmails *[]string       `json:"member_emails"`
		ManagerEmail *string         `json:"manager_email"`
	}
	if err := decodePeriodJSON(r, &body); err != nil {
		return postgres.CreatePeriodInput{}, errors.New("request body must be a valid period object")
	}
	if body.Name == nil || body.StartsOn == nil || body.Currency == nil || body.MemberEmails == nil || body.ManagerEmail == nil {
		return postgres.CreatePeriodInput{}, errors.New("name, starts_on, currency, member_emails, and manager_email are required")
	}
	var endsOn *string
	if len(body.EndsOn) > 0 && string(body.EndsOn) != "null" {
		var value string
		if err := json.Unmarshal(body.EndsOn, &value); err != nil {
			return postgres.CreatePeriodInput{}, errors.New("ends_on must be a date or null")
		}
		endsOn = &value
	}
	input := postgres.CreatePeriodInput{Name: *body.Name, StartsOn: *body.StartsOn, EndsOn: endsOn, Currency: *body.Currency,
		MemberEmails: *body.MemberEmails, ManagerEmail: *body.ManagerEmail}
	if err := postgres.ValidateCreatePeriodInput(input); err != nil {
		return postgres.CreatePeriodInput{}, errors.New("period values are invalid; dates must be YYYY-MM-DD, end date cannot precede start date, and currency must be three uppercase letters")
	}
	return input, nil
}

func decodePeriodJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxPeriodBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writePeriodError(w http.ResponseWriter, err error, logger *slog.Logger) {
	switch {
	case errors.Is(err, postgres.ErrInvalidPeriod):
		writeError(w, http.StatusBadRequest, "invalid_period", "Period values are invalid or include an email without an existing account.")
	case errors.Is(err, postgres.ErrPeriodUnavailable):
		writeError(w, http.StatusNotFound, "period_not_found", "Period is not available.")
	default:
		logger.Error("period operation failed", "error", err)
		writeError(w, http.StatusBadGateway, "data_provider_error", "Periods are temporarily unavailable.")
	}
}
