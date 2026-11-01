package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

type Period struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ManagerID string  `json:"manager_id"`
	StartsOn  string  `json:"starts_on"`
	EndsOn    *string `json:"ends_on"`
	Currency  string  `json:"currency"`
}

type CreatePeriodInput struct {
	Name         string
	StartsOn     string
	EndsOn       *string
	Currency     string
	MemberEmails []string
	ManagerEmail string
}

var (
	ErrInvalidPeriod     = errors.New("invalid period")
	ErrPeriodUnavailable = errors.New("period is not available")
)

func ValidateCreatePeriodInput(in CreatePeriodInput) error {
	if strings.TrimSpace(in.Name) == "" || in.Name != strings.TrimSpace(in.Name) || len([]rune(in.Name)) > 120 ||
		!isPeriodDate(in.StartsOn) || len(in.Currency) != 3 || in.Currency != upper(in.Currency) || len(in.MemberEmails) > 199 {
		return ErrInvalidPeriod
	}
	if in.EndsOn != nil && (!isPeriodDate(*in.EndsOn) || *in.EndsOn < in.StartsOn) {
		return ErrInvalidPeriod
	}
	seen := make(map[string]struct{}, len(in.MemberEmails))
	for _, email := range in.MemberEmails {
		if !validPeriodEmail(email) {
			return ErrInvalidPeriod
		}
		key := strings.ToLower(email)
		if _, ok := seen[key]; ok {
			return ErrInvalidPeriod
		}
		seen[key] = struct{}{}
	}
	if !validPeriodEmail(in.ManagerEmail) {
		return ErrInvalidPeriod
	}
	return nil
}

func (s *Store) ListPeriods(ctx context.Context, session auth.Session) ([]Period, error) {
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT p.id, p.name, p.manager_id, p.starts_on::text, p.ends_on::text, p.currency::text
		FROM public.periods AS p
		JOIN public.period_members AS pm ON pm.period_id=p.id
		JOIN public.profiles AS caller ON caller.id=pm.user_id
		WHERE caller.auth_user_id=$1 AND caller.deleted_at IS NULL AND p.deleted_at IS NULL
		ORDER BY p.starts_on DESC, lower(p.name), p.id`, session.Principal.UserID)
	if err != nil {
		return nil, fmt.Errorf("list periods: %w", err)
	}
	defer rows.Close()
	periods := make([]Period, 0)
	for rows.Next() {
		period, err := scanPeriod(rows)
		if err != nil {
			return nil, fmt.Errorf("scan period: %w", err)
		}
		periods = append(periods, period)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read periods: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit period read: %w", err)
	}
	return periods, nil
}

func (s *Store) GetPeriod(ctx context.Context, session auth.Session, id string) (Period, error) {
	if !idPattern.MatchString(id) {
		return Period{}, ErrInvalidPeriod
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return Period{}, err
	}
	defer tx.Rollback()
	period, err := scanPeriod(tx.QueryRowContext(ctx, `
		SELECT id, name, manager_id, starts_on::text, ends_on::text, currency::text
		FROM public.periods WHERE id=$1 AND deleted_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Period{}, ErrPeriodUnavailable
	}
	if err != nil {
		return Period{}, fmt.Errorf("get period: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Period{}, fmt.Errorf("commit period read: %w", err)
	}
	return period, nil
}

func (s *Store) CreatePeriod(ctx context.Context, session auth.Session, in CreatePeriodInput) (Period, error) {
	if err := ValidateCreatePeriodInput(in); err != nil {
		return Period{}, err
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return Period{}, err
	}
	defer tx.Rollback()
	callerID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return Period{}, classifyPeriodError(err)
	}
	lookupEmails := append(append([]string(nil), in.MemberEmails...), in.ManagerEmail)
	resolved, err := resolvePeriodMemberEmails(ctx, tx, lookupEmails)
	if err != nil {
		return Period{}, classifyPeriodError(err)
	}
	byEmail := make(map[string]string, len(resolved))
	for email, profileID := range resolved {
		byEmail[email] = profileID
	}
	memberIDs := make(map[string]struct{}, len(in.MemberEmails)+1)
	memberIDs[callerID] = struct{}{}
	for _, email := range in.MemberEmails {
		profileID, ok := byEmail[strings.ToLower(email)]
		if !ok {
			return Period{}, ErrInvalidPeriod
		}
		memberIDs[profileID] = struct{}{}
	}
	managerID, ok := byEmail[strings.ToLower(in.ManagerEmail)]
	if !ok {
		return Period{}, ErrInvalidPeriod
	}
	if _, ok := memberIDs[managerID]; !ok {
		return Period{}, ErrInvalidPeriod
	}
	members := make([]string, 0, len(memberIDs))
	for id := range memberIDs {
		members = append(members, id)
	}
	sort.Strings(members)

	// The creator starts as manager so RLS can authorize the roster inserts.
	// The deferred database invariant checks the final manager and role at commit.
	var id string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO public.periods (name, manager_id, starts_on, ends_on, currency, roster_finalized)
		VALUES ($1,$2,$3::date,$4::date,$5::char(3),false)
		RETURNING id`, in.Name, callerID, in.StartsOn, in.EndsOn, in.Currency).Scan(&id)
	if err != nil {
		return Period{}, classifyPeriodError(err)
	}
	for _, memberID := range members {
		role := "member"
		if memberID == callerID {
			role = "manager"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO public.period_members (period_id,user_id,role) VALUES ($1,$2,$3)`, id, memberID, role); err != nil {
			return Period{}, classifyPeriodError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.periods SET roster_finalized=true, updated_at=now() WHERE id=$1`, id); err != nil {
		return Period{}, classifyPeriodError(err)
	}
	if managerID != callerID {
		if _, err := tx.ExecContext(ctx, `UPDATE public.period_members SET role='member' WHERE period_id=$1 AND user_id=$2`, id, callerID); err != nil {
			return Period{}, classifyPeriodError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.period_members SET role='manager' WHERE period_id=$1 AND user_id=$2`, id, managerID); err != nil {
			return Period{}, classifyPeriodError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE public.periods SET manager_id=$2, updated_at=now() WHERE id=$1`, id, managerID); err != nil {
			return Period{}, classifyPeriodError(err)
		}
	}
	period, err := scanPeriod(tx.QueryRowContext(ctx, `SELECT id,name,manager_id,starts_on::text,ends_on::text,currency::text FROM public.periods WHERE id=$1`, id))
	if err != nil {
		return Period{}, classifyPeriodError(err)
	}
	if err := tx.Commit(); err != nil {
		return Period{}, classifyPeriodError(err)
	}
	return period, nil
}

func (s *Store) TransferPeriodManager(ctx context.Context, session auth.Session, periodID, managerID string) error {
	if !idPattern.MatchString(periodID) || !idPattern.MatchString(managerID) {
		return ErrInvalidPeriod
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	callerID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return classifyPeriodError(err)
	}
	var lockedPeriodID string
	err = tx.QueryRowContext(ctx, `
		SELECT p.id FROM public.periods AS p
		JOIN public.period_members AS pm ON pm.period_id=p.id
		WHERE p.id=$1 AND p.deleted_at IS NULL AND p.manager_id=$2
		  AND pm.user_id=$2 AND pm.role='manager'
		FOR UPDATE OF p`, periodID, callerID).Scan(&lockedPeriodID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPeriodUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock period for manager transfer: %w", err)
	}
	var isLive bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.period_members WHERE period_id=$1 AND user_id=$2) AND public.period_profile_is_live($2)`, periodID, managerID).Scan(&isLive)
	if err != nil {
		return fmt.Errorf("validate new period manager: %w", err)
	}
	if !isLive {
		return ErrInvalidPeriod
	}
	if managerID != callerID {
		if _, err = tx.ExecContext(ctx, `UPDATE public.period_members SET role='member' WHERE period_id=$1 AND user_id=$2 AND role='manager'`, periodID, callerID); err != nil {
			return classifyPeriodError(err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.period_members SET role='manager' WHERE period_id=$1 AND user_id=$2 AND role='member'`, periodID, managerID); err != nil {
			return classifyPeriodError(err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE public.periods SET manager_id=$2, updated_at=now() WHERE id=$1`, periodID, managerID); err != nil {
			return classifyPeriodError(err)
		}
	}
	return classifyPeriodError(tx.Commit())
}

func (s *Store) SoftDeletePeriod(ctx context.Context, session auth.Session, periodID string) error {
	if !idPattern.MatchString(periodID) {
		return ErrInvalidPeriod
	}
	tx, err := s.beginCallerTx(ctx, session)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	callerID, err := callerProfileID(ctx, tx, session)
	if err != nil {
		return classifyPeriodError(err)
	}
	var lockedPeriodID string
	err = tx.QueryRowContext(ctx, `
		SELECT p.id FROM public.periods AS p
		JOIN public.period_members AS pm ON pm.period_id=p.id
		WHERE p.id=$1 AND p.deleted_at IS NULL AND p.manager_id=$2
		  AND pm.user_id=$2 AND pm.role='manager'
		FOR UPDATE OF p`, periodID, callerID).Scan(&lockedPeriodID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPeriodUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock period for deletion: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE public.periods SET deleted_at=now(),updated_at=now() WHERE id=$1`, periodID); err != nil {
		return classifyPeriodError(err)
	}
	return classifyPeriodError(tx.Commit())
}

func resolvePeriodMemberEmails(ctx context.Context, tx *sql.Tx, emails []string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT email, profile_id FROM public.resolve_period_member_emails($1::text[])`, emails)
	if err != nil {
		return nil, fmt.Errorf("resolve period member emails: %w", err)
	}
	defer rows.Close()
	resolved := make(map[string]string, len(emails))
	for rows.Next() {
		var email, profileID string
		if err := rows.Scan(&email, &profileID); err != nil {
			return nil, fmt.Errorf("read resolved period member: %w", err)
		}
		resolved[strings.ToLower(email)] = profileID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read resolved period members: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close resolved period members: %w", err)
	}
	return resolved, nil
}

type rowScanner interface{ Scan(...any) error }

func scanPeriod(row rowScanner) (Period, error) {
	var p Period
	var ends sql.NullString
	err := row.Scan(&p.ID, &p.Name, &p.ManagerID, &p.StartsOn, &ends, &p.Currency)
	if ends.Valid {
		p.EndsOn = &ends.String
	}
	return p, err
}

func isPeriodDate(value string) bool {
	d, err := time.Parse("2006-01-02", value)
	return err == nil && d.Format("2006-01-02") == value
}

func validPeriodEmail(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 254 {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value && strings.Contains(value, "@")
}

func classifyPeriodError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCategoryUnavailable) {
		return ErrPeriodUnavailable
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "42501":
			return ErrPeriodUnavailable
		case "22023", "23514", "23503", "23502":
			return ErrInvalidPeriod
		}
	}
	return fmt.Errorf("period operation: %w", err)
}
