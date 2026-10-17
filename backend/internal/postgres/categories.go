package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

type Category struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Icon      *string `json:"icon"`
	Color     *string `json:"color"`
	SortOrder int     `json:"sort_order"`
	Archived  bool    `json:"is_archived"`
}

type SaveCategoryInput struct {
	Name      string
	Icon      *string
	Color     *string
	SortOrder int
}

func (s *Store) ListCategories(ctx context.Context, session auth.Session) ([]Category, error) {
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	profileID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, name, icon, color, sort_order, is_archived
		FROM public.categories WHERE user_id=$1 ORDER BY sort_order, lower(name), id`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	categories := make([]Category, 0)
	for rows.Next() {
		var category Category
		var icon, color sql.NullString
		if err := rows.Scan(&category.ID, &category.Name, &icon, &color, &category.SortOrder, &category.Archived); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		if icon.Valid {
			category.Icon = &icon.String
		}
		if color.Valid {
			category.Color = &color.String
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read categories: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit category read: %w", err)
	}
	return categories, nil
}

func (s *Store) CreateCategory(ctx context.Context, session auth.Session, in SaveCategoryInput) (Category, error) {
	if err := ValidateCategoryInput(in); err != nil {
		return Category{}, err
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	profileID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return Category{}, err
	}
	category, err := scanCategory(tx.QueryRowContext(ctx, `
		INSERT INTO public.categories (user_id, name, icon, color, sort_order)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, name, icon, color, sort_order, is_archived`,
		profileID, in.Name, in.Icon, in.Color, in.SortOrder))
	if err != nil {
		return Category{}, classifyCategoryError(err)
	}
	if err := tx.Commit(); err != nil {
		return Category{}, classifyCategoryError(err)
	}
	return category, nil
}

func (s *Store) UpdateCategory(ctx context.Context, session auth.Session, id string, in SaveCategoryInput) (Category, error) {
	if !idPattern.MatchString(id) {
		return Category{}, ErrInvalidCategory
	}
	if err := ValidateCategoryInput(in); err != nil {
		return Category{}, err
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return Category{}, err
	}
	defer tx.Rollback()
	profileID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return Category{}, err
	}
	category, err := scanCategory(tx.QueryRowContext(ctx, `
		UPDATE public.categories SET name=$3, icon=$4, color=$5, sort_order=$6
		WHERE id=$1 AND user_id=$2
		RETURNING id, name, icon, color, sort_order, is_archived`,
		id, profileID, in.Name, in.Icon, in.Color, in.SortOrder))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrCategoryUnavailable
	}
	if err != nil {
		return Category{}, classifyCategoryError(err)
	}
	if err := tx.Commit(); err != nil {
		return Category{}, classifyCategoryError(err)
	}
	return category, nil
}

func (s *Store) ArchiveCategory(ctx context.Context, session auth.Session, id string, archived bool) error {
	if !idPattern.MatchString(id) {
		return ErrInvalidCategory
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	profileID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE public.categories SET is_archived=$3 WHERE id=$1 AND user_id=$2`, id, profileID, archived)
	if err != nil {
		return classifyCategoryError(err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check category update: %w", err)
	}
	if count == 0 {
		return ErrCategoryUnavailable
	}
	return tx.Commit()
}

func (s *Store) beginCallerTx(ctx context.Context, session auth.Session) (*sql.Tx, error) {
	if session.AccessToken == "" || !idPattern.MatchString(session.Principal.UserID) {
		return nil, ErrCategoryUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin caller transaction: %w", err)
	}
	claims, err := json.Marshal(map[string]string{"sub": session.Principal.UserID, "role": "authenticated", "aud": "authenticated"})
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("encode caller claims: %w", err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"SET LOCAL ROLE app_api", nil},
		{"SELECT set_config('request.jwt.claims', $1, true)", []any{string(claims)}},
		{"SELECT set_config('request.jwt.claim.sub', $1, true)", []any{session.Principal.UserID}},
		{"SELECT set_config('request.jwt.claim.role', 'authenticated', true)", nil},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("install verified caller context: %w", err)
		}
	}
	return tx, nil
}

func callerProfileID(ctx context.Context, tx *sql.Tx, session auth.Session) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM public.profiles WHERE auth_user_id=$1 AND deleted_at IS NULL`, session.Principal.UserID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrCategoryUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("load caller profile: %w", err)
	}
	return id, nil
}

func scanCategory(row *sql.Row) (Category, error) {
	var category Category
	var icon, color sql.NullString
	err := row.Scan(&category.ID, &category.Name, &icon, &color, &category.SortOrder, &category.Archived)
	if icon.Valid {
		category.Icon = &icon.String
	}
	if color.Valid {
		category.Color = &color.String
	}
	return category, err
}

var (
	ErrInvalidCategory     = errors.New("invalid category")
	ErrCategoryUnavailable = errors.New("category is not available")
)

func ValidateCategoryInput(in SaveCategoryInput) error {
	if len([]rune(in.Name)) == 0 || len([]rune(in.Name)) > 80 || in.Name != strings.TrimSpace(in.Name) || in.SortOrder < 0 || in.SortOrder > 100000 {
		return ErrInvalidCategory
	}
	for _, value := range []*string{in.Icon, in.Color} {
		if value != nil && len(*value) > 64 {
			return ErrInvalidCategory
		}
	}
	return nil
}

func classifyCategoryError(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		if state.SQLState() == "23505" {
			return ErrCategoryConflict
		}
		if state.SQLState() == "23514" || state.SQLState() == "23502" {
			return ErrInvalidCategory
		}
	}
	return fmt.Errorf("category operation: %w", err)
}

var ErrCategoryConflict = errors.New("category name already exists")
