package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/supabase"
)

const (
	defaultExpensePageSize = 50
	maxExpensePageSize     = 100
	maxExpenseOffset       = 10000
	maxExpenseResponseSize = 8 << 20
	// Transport bounds for expense creation. The Postgres store performs the
	// authoritative application validation inside the caller-scoped transaction.
	maxCreateExpenseBody   = 64 << 10
	maxCreateExpenseSplits = 200
)

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	amountPattern   = regexp.MustCompile(`^[0-9]+$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

func listPersonalExpenses(client *supabase.Client, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from, to, limit, offset, err := parseExpenseListQuery(r.URL.Query())
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
			return
		}
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}

		query := url.Values{
			"select":     {"id,amount_minor,currency,occurred_on,category_id,note,created_at"},
			"period_id":  {"is.null"},
			"deleted_at": {"is.null"},
			"and":        {"(occurred_on.gte." + from + ",occurred_on.lte." + to + ")"},
			"order":      {"occurred_on.desc,id.desc"},
			"limit":      {strconv.Itoa(limit)},
			"offset":     {strconv.Itoa(offset)},
		}
		response, err := client.Do(r.Context(), http.MethodGet, "expenses?"+query.Encode(), session, nil)
		if err != nil {
			logger.Error("Supabase expense query failed", "error", err)
			writeError(w, http.StatusBadGateway, "data_provider_unavailable", "Expenses are temporarily unavailable.")
			return
		}
		defer response.Body.Close()

		switch response.StatusCode {
		case http.StatusOK:
			// Forward the PostgREST JSON array; SQL and RLS define its contents.
		case http.StatusUnauthorized:
			writeError(w, http.StatusUnauthorized, "unauthorized", "The access token is no longer valid.")
			return
		case http.StatusForbidden:
			writeError(w, http.StatusForbidden, "forbidden", "The request is not permitted.")
			return
		default:
			logger.Error("Supabase rejected expense query", "status", response.StatusCode)
			writeError(w, http.StatusBadGateway, "data_provider_error", "Expenses are temporarily unavailable.")
			return
		}

		body, err := io.ReadAll(io.LimitReader(response.Body, maxExpenseResponseSize+1))
		if err != nil || len(body) > maxExpenseResponseSize {
			logger.Error("failed to read Supabase expense response", "error", err)
			writeError(w, http.StatusBadGateway, "data_provider_error", "Expenses are temporarily unavailable.")
			return
		}
		var records []struct {
			ID          string      `json:"id"`
			AmountMinor json.Number `json:"amount_minor"`
			Currency    string      `json:"currency"`
			OccurredOn  string      `json:"occurred_on"`
			CategoryID  *string     `json:"category_id"`
			Note        *string     `json:"note"`
			CreatedAt   string      `json:"created_at"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.UseNumber()
		if err := decoder.Decode(&records); err != nil {
			logger.Error("invalid Supabase expense response", "error", err)
			writeError(w, http.StatusBadGateway, "data_provider_error", "Expenses are temporarily unavailable.")
			return
		}
		expenses := make([]personalExpense, 0, len(records))
		for _, record := range records {
			if _, err := record.AmountMinor.Int64(); err != nil || record.ID == "" {
				logger.Error("invalid Supabase expense record")
				writeError(w, http.StatusBadGateway, "data_provider_error", "Expenses are temporarily unavailable.")
				return
			}
			expenses = append(expenses, personalExpense{
				ID: record.ID, AmountMinor: record.AmountMinor.String(), Currency: record.Currency,
				OccurredOn: record.OccurredOn, CategoryID: record.CategoryID, Note: record.Note,
				CreatedAt: record.CreatedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(expenses)
	}
}

type personalExpense struct {
	ID          string  `json:"id"`
	AmountMinor string  `json:"amount_minor"`
	Currency    string  `json:"currency"`
	OccurredOn  string  `json:"occurred_on"`
	CategoryID  *string `json:"category_id"`
	Note        *string `json:"note"`
	CreatedAt   string  `json:"created_at"`
}

// requestError is a request-shape validation failure; application rules are
// checked by the Go store before it writes the transaction.
type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) Error() string { return e.message }

// createExpenseInput is the validated form of POST /v1/expenses. Amounts are
// int64 (Postgres bigint range); the decimal-string form only exists at the
// mobile JSON boundary to preserve precision in JavaScript.
type createExpenseInput struct {
	ID         string
	PeriodID   *string
	CategoryID *string
	PaidBy     string
	Amount     int64
	Currency   string
	OccurredOn string
	Note       *string
	Splits     []expenseSplitInput
}

type expenseSplitInput struct {
	UserID string
	Amount int64
}

// createExpenseWithPostgres records a personal or shared expense after Go
// validates the request and the store writes through a caller-scoped transaction.
func createExpenseWithPostgres(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		if r.Body == nil {
			writeError(w, http.StatusBadRequest, "invalid_expense", "Request body is required.")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxCreateExpenseBody+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_expense", "Request body could not be read.")
			return
		}
		if len(raw) > maxCreateExpenseBody {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large.")
			return
		}
		input, reqErr := parseCreateExpenseBody(raw)
		if reqErr != nil {
			writeError(w, reqErr.status, reqErr.code, reqErr.message)
			return
		}
		storeInput := postgres.CreateExpenseInput{
			ID: input.ID, PeriodID: input.PeriodID, CategoryID: input.CategoryID,
			PaidBy: input.PaidBy, Amount: input.Amount, Currency: input.Currency,
			OccurredOn: input.OccurredOn, Note: input.Note,
			Splits: make([]postgres.Split, 0, len(input.Splits)),
		}
		for _, split := range input.Splits {
			storeInput.Splits = append(storeInput.Splits, postgres.Split{UserID: split.UserID, Amount: split.Amount})
		}
		id, err := store.CreateExpense(r.Context(), session, storeInput)
		switch {
		case errors.Is(err, postgres.ErrInvalidExpense):
			writeError(w, http.StatusBadRequest, "invalid_expense", err.Error())
			return
		case errors.Is(err, postgres.ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden", "The request is not permitted.")
			return
		case errors.Is(err, postgres.ErrConflict):
			writeError(w, http.StatusConflict, "expense_id_conflict", "An expense with this id already exists with different data.")
			return
		case err != nil:
			logger.Error("PostgreSQL expense transaction failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "data_provider_unavailable", "Expenses are temporarily unavailable.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	}
}

func updatePersonalExpense(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		var body struct {
			Amount     *string         `json:"amount_minor"`
			OccurredOn *string         `json:"occurred_on"`
			CategoryID *string         `json:"category_id"`
			Note       json.RawMessage `json:"note"`
		}
		if err := decodeStrictJSON(r, &body); err != nil || body.Amount == nil || body.OccurredOn == nil || body.CategoryID == nil || body.Note == nil {
			writeError(w, http.StatusBadRequest, "invalid_expense", "amount_minor, occurred_on, category_id, and note are required.")
			return
		}
		amount, err := parsePositiveBigint(*body.Amount)
		if err != nil || !uuidPattern.MatchString(*body.CategoryID) || !isValidDate(*body.OccurredOn) {
			writeError(w, http.StatusBadRequest, "invalid_expense", "Expense values are invalid.")
			return
		}
		var note *string
		if string(body.Note) != "null" {
			var value string
			if err := json.Unmarshal(body.Note, &value); err != nil || len(value) > 2000 {
				writeError(w, http.StatusBadRequest, "invalid_expense", "note must be a string or null, up to 2000 characters.")
				return
			}
			note = &value
		}
		if err := store.UpdatePersonalExpense(r.Context(), session, r.PathValue("id"), amount, *body.OccurredOn, *body.CategoryID, note); err != nil {
			writeExpenseStoreError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func softDeleteExpense(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		if err := store.SoftDeleteExpense(r.Context(), session, r.PathValue("id")); err != nil {
			writeExpenseStoreError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeExpenseStoreError(w http.ResponseWriter, err error, logger *slog.Logger) {
	switch {
	case errors.Is(err, postgres.ErrInvalidExpense):
		writeError(w, http.StatusBadRequest, "invalid_expense", err.Error())
	case errors.Is(err, postgres.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "The expense is not available to this account.")
	case errors.Is(err, postgres.ErrConflict):
		writeError(w, http.StatusConflict, "expense_conflict", "The expense could not be updated because it changed.")
	default:
		logger.Error("expense mutation failed", "error", err)
		writeError(w, http.StatusBadGateway, "data_provider_error", "Expenses are temporarily unavailable.")
	}
}

// parseCreateExpenseBody validates the JSON contract for POST /v1/expenses.
// Unknown fields are rejected per the contract (additionalProperties: false).
// Contextual application rules are validated by the Go Postgres store.
func parseCreateExpenseBody(raw []byte) (*createExpenseInput, *requestError) {
	invalid := func(message string) (*createExpenseInput, *requestError) {
		return nil, &requestError{status: http.StatusBadRequest, code: "invalid_expense", message: message}
	}
	var body struct {
		ID         *string `json:"id"`
		PeriodID   *string `json:"period_id"`
		CategoryID *string `json:"category_id"`
		PaidBy     *string `json:"paid_by"`
		Amount     *string `json:"amount_minor"`
		Currency   *string `json:"currency"`
		OccurredOn *string `json:"occurred_on"`
		Note       *string `json:"note"`
		Splits     *[]struct {
			UserID *string `json:"user_id"`
			Amount *string `json:"amount_minor"`
		} `json:"splits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return invalid("Request body must be a JSON expense object.")
	}
	if decoder.More() {
		return invalid("Request body must contain a single JSON object.")
	}

	if body.ID == nil || !uuidPattern.MatchString(*body.ID) {
		return invalid("id must be a client-generated UUID.")
	}
	if body.PaidBy == nil || !uuidPattern.MatchString(*body.PaidBy) {
		return invalid("paid_by must be a UUID.")
	}
	periodID, reqErr := parseOptionalUUID(body.PeriodID, "period_id")
	if reqErr != nil {
		return nil, reqErr
	}
	categoryID, reqErr := parseOptionalUUID(body.CategoryID, "category_id")
	if reqErr != nil {
		return nil, reqErr
	}
	if body.Amount == nil {
		return invalid("amount_minor is required.")
	}
	amount, err := parsePositiveBigint(*body.Amount)
	if err != nil {
		return invalid("amount_minor must be a positive integer within bigint range.")
	}
	if body.Currency == nil || !currencyPattern.MatchString(*body.Currency) {
		return invalid("currency must be ISO-4217 (e.g. USD).")
	}
	if body.OccurredOn == nil || !isValidDate(*body.OccurredOn) {
		return invalid("occurred_on must be a valid YYYY-MM-DD date.")
	}
	if body.Splits == nil {
		return invalid("splits is required.")
	}
	splits := *body.Splits
	if len(splits) < 1 || len(splits) > maxCreateExpenseSplits {
		return invalid(fmt.Sprintf("splits must contain between 1 and %d entries.", maxCreateExpenseSplits))
	}
	parsed := make([]expenseSplitInput, 0, len(splits))
	seen := make(map[string]struct{}, len(splits))
	// Overflow-safe exact-total check: the running sum never exceeds the
	// total, so int64 cannot overflow before a mismatch is reported.
	var sum int64
	for _, split := range splits {
		if split.UserID == nil || !uuidPattern.MatchString(*split.UserID) {
			return invalid("every split needs a user_id UUID.")
		}
		if split.Amount == nil {
			return invalid("every split needs an amount_minor.")
		}
		share, err := parsePositiveBigint(*split.Amount)
		if err != nil {
			return invalid("every split amount_minor must be a positive integer within bigint range.")
		}
		if _, dup := seen[*split.UserID]; dup {
			return invalid("splits must not repeat a participant.")
		}
		seen[*split.UserID] = struct{}{}
		if share > amount-sum {
			return invalid(fmt.Sprintf("splits total more than the expense total of %d.", amount))
		}
		sum += share
		parsed = append(parsed, expenseSplitInput{UserID: *split.UserID, Amount: share})
	}
	if sum != amount {
		return invalid(fmt.Sprintf("splits total %d but the expense total is %d: must match exactly", sum, amount))
	}

	return &createExpenseInput{
		ID: *body.ID, PeriodID: periodID, CategoryID: categoryID, PaidBy: *body.PaidBy,
		Amount: amount, Currency: *body.Currency, OccurredOn: *body.OccurredOn,
		Note: body.Note, Splits: parsed,
	}, nil
}

func parseOptionalUUID(value *string, name string) (*string, *requestError) {
	if value == nil {
		return nil, nil
	}
	if !uuidPattern.MatchString(*value) {
		return nil, &requestError{status: http.StatusBadRequest, code: "invalid_expense", message: name + " must be a UUID."}
	}
	return value, nil
}

// parsePositiveBigint parses a digits-only decimal string into an int64.
// It rejects empty input, non-digits, zero, and values beyond bigint range.
func parsePositiveBigint(value string) (int64, error) {
	if !amountPattern.MatchString(value) {
		return 0, fmt.Errorf("not a digits-only amount")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("amount out of range")
	}
	return parsed, nil
}

func isValidDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func parseExpenseListQuery(query url.Values) (from, to string, limit, offset int, err error) {
	for _, name := range []string{"from", "to", "limit", "offset"} {
		if len(query[name]) > 1 {
			return "", "", 0, 0, fmt.Errorf("%s must be provided at most once", name)
		}
	}
	from, to = query.Get("from"), query.Get("to")
	fromDate, fromErr := time.Parse("2006-01-02", from)
	toDate, toErr := time.Parse("2006-01-02", to)
	if fromErr != nil || fromDate.Format("2006-01-02") != from {
		return "", "", 0, 0, fmt.Errorf("from must be a valid YYYY-MM-DD date")
	}
	if toErr != nil || toDate.Format("2006-01-02") != to {
		return "", "", 0, 0, fmt.Errorf("to must be a valid YYYY-MM-DD date")
	}
	if fromDate.After(toDate) {
		return "", "", 0, 0, fmt.Errorf("from must be on or before to")
	}

	limit = defaultExpensePageSize
	if raw, exists := query["limit"]; exists {
		if len(raw) != 1 || raw[0] == "" {
			return "", "", 0, 0, fmt.Errorf("limit must be between 1 and %d", maxExpensePageSize)
		}
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > maxExpensePageSize {
			return "", "", 0, 0, fmt.Errorf("limit must be between 1 and %d", maxExpensePageSize)
		}
	}
	if raw, exists := query["offset"]; exists {
		if len(raw) != 1 || raw[0] == "" {
			return "", "", 0, 0, fmt.Errorf("offset must be between 0 and %d", maxExpenseOffset)
		}
		offset, err = strconv.Atoi(raw[0])
		if err != nil || offset < 0 || offset > maxExpenseOffset {
			return "", "", 0, 0, fmt.Errorf("offset must be between 0 and %d", maxExpenseOffset)
		}
	}
	return from, to, limit, offset, nil
}
