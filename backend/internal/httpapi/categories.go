package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/postgres"
)

const maxCategoryBody = 16 << 10

func listCategories(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		categories, err := store.ListCategories(r.Context(), session)
		if err != nil {
			logger.Error("list categories failed", "error", err)
			writeError(w, http.StatusBadGateway, "data_provider_error", "Categories are temporarily unavailable.")
			return
		}
		writeJSON(w, http.StatusOK, categories)
	}
}

func createCategory(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		input, err := decodeCategory(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
			return
		}
		category, err := store.CreateCategory(r.Context(), session, input)
		if err != nil {
			writeCategoryError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusCreated, category)
	}
}

func updateCategory(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		input, err := decodeCategory(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_category", err.Error())
			return
		}
		category, err := store.UpdateCategory(r.Context(), session, r.PathValue("id"), input)
		if err != nil {
			writeCategoryError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, category)
	}
}

func setCategoryArchived(store *postgres.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid access token is required.")
			return
		}
		var body struct {
			Archived *bool `json:"is_archived"`
		}
		if err := decodeStrictJSON(r, &body); err != nil || body.Archived == nil {
			writeError(w, http.StatusBadRequest, "invalid_category", "is_archived must be a boolean.")
			return
		}
		if err := store.ArchiveCategory(r.Context(), session, r.PathValue("id"), *body.Archived); err != nil {
			writeCategoryError(w, err, logger)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeCategory(r *http.Request) (postgres.SaveCategoryInput, error) {
	var body struct {
		Name      *string `json:"name"`
		Icon      *string `json:"icon"`
		Color     *string `json:"color"`
		SortOrder *int    `json:"sort_order"`
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return postgres.SaveCategoryInput{}, errors.New("request body must be a valid category object")
	}
	if body.Name == nil || body.SortOrder == nil {
		return postgres.SaveCategoryInput{}, errors.New("name and sort_order are required")
	}
	input := postgres.SaveCategoryInput{Name: strings.TrimSpace(*body.Name), Icon: body.Icon, Color: body.Color, SortOrder: *body.SortOrder}
	if err := postgres.ValidateCategoryInput(input); err != nil {
		return postgres.SaveCategoryInput{}, errors.New("name must be 1–80 characters and sort_order must be non-negative")
	}
	return input, nil
}

func decodeStrictJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return errors.New("missing body")
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxCategoryBody)
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

func writeCategoryError(w http.ResponseWriter, err error, logger *slog.Logger) {
	switch {
	case errors.Is(err, postgres.ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "invalid_category", "Category values are invalid.")
	case errors.Is(err, postgres.ErrCategoryConflict):
		writeError(w, http.StatusConflict, "category_name_conflict", "A category with that name already exists.")
	case errors.Is(err, postgres.ErrCategoryUnavailable):
		writeError(w, http.StatusNotFound, "category_not_found", "Category is not available.")
	default:
		logger.Error("category operation failed", "error", err)
		writeError(w, http.StatusBadGateway, "data_provider_error", "Categories are temporarily unavailable.")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
