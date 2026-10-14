package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/supabase"
)

const (
	defaultExpensePageSize = 50
	maxExpensePageSize     = 100
	maxExpenseOffset       = 10000
	maxExpenseResponseSize = 8 << 20
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
