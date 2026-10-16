package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

var (
	ErrInvalidExpense = errors.New("invalid expense")
	ErrForbidden      = errors.New("expense is not available")
	ErrConflict       = errors.New("expense id already exists with different data")
	idPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Split struct {
	UserID string
	Amount int64
}

type CreateExpenseInput struct {
	ID         string
	PeriodID   *string
	CategoryID *string
	PaidBy     string
	Amount     int64
	Currency   string
	OccurredOn string
	Note       *string
	Splits     []Split
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreateExpense validates the application rules and writes the expense and
// its frozen split rows atomically. The transaction runs as app_api with the
// verified Supabase identity installed for auth.uid() and RLS.
func (s *Store) CreateExpense(ctx context.Context, session auth.Session, in CreateExpenseInput) (string, error) {
	if session.AccessToken == "" || !idPattern.MatchString(session.Principal.UserID) {
		return "", ErrForbidden
	}
	if err := validateShape(in); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin caller transaction: %w", err)
	}
	defer tx.Rollback()

	claims, err := json.Marshal(map[string]string{
		"sub": session.Principal.UserID, "role": "authenticated", "aud": "authenticated",
	})
	if err != nil {
		return "", fmt.Errorf("encode caller claims: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "SET LOCAL ROLE app_api"); err != nil {
		return "", fmt.Errorf("set caller database role: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "SELECT set_config('request.jwt.claims', $1, true)", string(claims)); err != nil {
		return "", fmt.Errorf("set caller database claims: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "SELECT set_config('request.jwt.claim.sub', $1, true)", session.Principal.UserID); err != nil {
		return "", fmt.Errorf("set caller subject: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "SELECT set_config('request.jwt.claim.role', 'authenticated', true)"); err != nil {
		return "", fmt.Errorf("set caller role claim: %w", err)
	}

	var profileID, timezone string
	var baseCurrency sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, timezone, base_currency
		FROM public.profiles
		WHERE auth_user_id = $1 AND deleted_at IS NULL`, session.Principal.UserID,
	).Scan(&profileID, &timezone, &baseCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("load caller profile: %w", err)
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		return "", fmt.Errorf("profile has invalid timezone: %w", err)
	}
	date, _ := time.Parse("2006-01-02", in.OccurredOn)
	if in.OccurredOn > time.Now().In(location).Format("2006-01-02") {
		return "", fmt.Errorf("%w: occurred_on cannot be in the future", ErrInvalidExpense)
	}

	if in.PeriodID == nil {
		if in.PaidBy != profileID || in.CategoryID == nil || len(in.Splits) != 1 ||
			in.Splits[0].UserID != profileID || in.Splits[0].Amount != in.Amount {
			return "", fmt.Errorf("%w: personal expenses must belong to the caller and have one full owner split", ErrInvalidExpense)
		}
		if !baseCurrency.Valid || in.Currency != baseCurrency.String {
			return "", fmt.Errorf("%w: personal expense currency must match the caller's base currency", ErrInvalidExpense)
		}
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)`, *in.CategoryID, profileID).Scan(&exists)
		if err != nil {
			return "", fmt.Errorf("check expense category: %w", err)
		}
		if !exists {
			return "", ErrForbidden
		}
	} else {
		var currency string
		var startsOn time.Time
		var endsOn sql.NullTime
		err = tx.QueryRowContext(ctx, `
			SELECT currency, starts_on, ends_on
			FROM public.periods WHERE id=$1 AND deleted_at IS NULL`, *in.PeriodID,
		).Scan(&currency, &startsOn, &endsOn)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrForbidden
		}
		if err != nil {
			return "", fmt.Errorf("load period: %w", err)
		}
		if in.CategoryID != nil || in.Currency != currency || date.Before(startsOn) ||
			(endsOn.Valid && date.After(endsOn.Time)) {
			return "", fmt.Errorf("%w: shared expense category, currency, or date does not match its period", ErrInvalidExpense)
		}
		members, err := periodMembers(ctx, tx, *in.PeriodID)
		if err != nil {
			return "", err
		}
		if _, ok := members[profileID]; !ok {
			return "", ErrForbidden
		}
		if _, ok := members[in.PaidBy]; !ok {
			return "", fmt.Errorf("%w: paid_by must be a period member", ErrInvalidExpense)
		}
		for _, split := range in.Splits {
			if _, ok := members[split.UserID]; !ok {
				return "", fmt.Errorf("%w: every split participant must be a period member", ErrInvalidExpense)
			}
		}
	}

	if same, err := s.existingMatches(ctx, tx, profileID, in); err != nil {
		return "", err
	} else if same {
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit idempotent create: %w", err)
		}
		return in.ID, nil
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO public.expenses
			(id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		in.ID, profileID, in.PeriodID, in.CategoryID, in.PaidBy, in.Amount, in.Currency, date, in.Note)
	if err != nil {
		return "", classifyWriteError(err)
	}
	for _, split := range in.Splits {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
			VALUES ($1,$2,$3)`, in.ID, split.UserID, split.Amount); err != nil {
			return "", classifyWriteError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", classifyWriteError(err)
	}
	return in.ID, nil
}

func validateShape(in CreateExpenseInput) error {
	if !idPattern.MatchString(in.ID) || !idPattern.MatchString(in.PaidBy) || in.Amount <= 0 ||
		len(in.Currency) != 3 || in.Currency != upper(in.Currency) || len(in.Splits) == 0 || len(in.Splits) > 200 {
		return ErrInvalidExpense
	}
	date, err := time.Parse("2006-01-02", in.OccurredOn)
	if err != nil || date.Format("2006-01-02") != in.OccurredOn {
		return ErrInvalidExpense
	}
	if in.PeriodID != nil && !idPattern.MatchString(*in.PeriodID) {
		return ErrInvalidExpense
	}
	if in.CategoryID != nil && !idPattern.MatchString(*in.CategoryID) {
		return ErrInvalidExpense
	}
	seen := make(map[string]struct{}, len(in.Splits))
	var total int64
	for _, split := range in.Splits {
		if !idPattern.MatchString(split.UserID) || split.Amount <= 0 {
			return ErrInvalidExpense
		}
		if _, exists := seen[split.UserID]; exists {
			return ErrInvalidExpense
		}
		seen[split.UserID] = struct{}{}
		if split.Amount > in.Amount-total {
			return fmt.Errorf("%w: split total exceeds expense amount", ErrInvalidExpense)
		}
		total += split.Amount
	}
	if total != in.Amount {
		return fmt.Errorf("%w: split total must equal expense amount", ErrInvalidExpense)
	}
	return nil
}

func periodMembers(ctx context.Context, tx *sql.Tx, periodID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM public.period_members WHERE period_id=$1`, periodID)
	if err != nil {
		return nil, fmt.Errorf("load period members: %w", err)
	}
	defer rows.Close()
	members := make(map[string]struct{})
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("scan period member: %w", err)
		}
		members[userID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read period members: %w", err)
	}
	return members, nil
}

func (s *Store) existingMatches(ctx context.Context, tx *sql.Tx, caller string, in CreateExpenseInput) (bool, error) {
	var userID, paidBy, currency, occurredOn string
	var periodID, categoryID, note sql.NullString
	var amount int64
	err := tx.QueryRowContext(ctx, `
		SELECT user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on::text, note
		FROM public.expenses WHERE id=$1 AND deleted_at IS NULL`, in.ID,
	).Scan(&userID, &periodID, &categoryID, &paidBy, &amount, &currency, &occurredOn, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check idempotent expense: %w", err)
	}
	if userID != caller || !nullStringMatches(periodID, in.PeriodID) ||
		!nullStringMatches(categoryID, in.CategoryID) || paidBy != in.PaidBy ||
		amount != in.Amount || currency != in.Currency || occurredOn != in.OccurredOn ||
		!nullStringMatches(note, in.Note) {
		return false, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id, amount_minor FROM public.expense_splits WHERE expense_id=$1`, in.ID)
	if err != nil {
		return false, fmt.Errorf("check idempotent splits: %w", err)
	}
	type pair struct {
		user   string
		amount int64
	}
	existing := make([]pair, 0, len(in.Splits))
	for rows.Next() {
		var item pair
		if err := rows.Scan(&item.user, &item.amount); err != nil {
			rows.Close()
			return false, fmt.Errorf("scan idempotent split: %w", err)
		}
		existing = append(existing, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("read idempotent splits: %w", err)
	}
	rows.Close()
	wanted := make([]pair, 0, len(in.Splits))
	for _, split := range in.Splits {
		wanted = append(wanted, pair{split.UserID, split.Amount})
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].user < existing[j].user })
	sort.Slice(wanted, func(i, j int) bool { return wanted[i].user < wanted[j].user })
	if len(existing) != len(wanted) {
		return false, ErrConflict
	}
	for i := range existing {
		if existing[i] != wanted[i] {
			return false, ErrConflict
		}
	}
	return true, nil
}

func classifyWriteError(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "42501":
			return ErrForbidden
		case "23505":
			return ErrConflict
		case "23514", "23503", "23502":
			return fmt.Errorf("%w: database integrity check failed", ErrInvalidExpense)
		}
	}
	return fmt.Errorf("write expense transaction: %w", err)
}

func nullStringMatches(value sql.NullString, expected *string) bool {
	if expected == nil {
		return !value.Valid
	}
	return value.Valid && value.String == *expected
}
func upper(value string) string {
	b := []byte(value)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}
