package postgres

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

const (
	testCallerID   = "11111111-1111-1111-1111-111111111111"
	testExpenseID  = "40000000-0000-0000-0000-000000000001"
	testCategoryID = "20000000-0000-0000-0000-000000000001"
	testPeriodID   = "30000000-0000-0000-0000-000000000001"
	testMemberID   = "22222222-2222-2222-2222-222222222222"
)

func newMockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func personalExpense() CreateExpenseInput {
	category := testCategoryID
	return CreateExpenseInput{
		ID: testExpenseID, CategoryID: &category, PaidBy: testCallerID,
		Amount: 1250, Currency: "USD", OccurredOn: "2026-03-14",
		Splits: []Split{{UserID: testCallerID, Amount: 1250}},
	}
}

func callerSession() auth.Session {
	return auth.Session{Principal: auth.Principal{UserID: testCallerID}, AccessToken: "verified-access-token"}
}

func expectCallerContext(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL ROLE app_api").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claims'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.sub'").WithArgs(testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.role'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, timezone, base_currency\n\t\tFROM public.profiles\n\t\tWHERE auth_user_id = $1 AND deleted_at IS NULL")).
		WithArgs(testCallerID).WillReturnRows(sqlmock.NewRows([]string{"id", "timezone", "base_currency"}).AddRow(testCallerID, "UTC", "USD"))
}

func expectNoExistingExpense(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on::text, note\n\t\tFROM public.expenses WHERE id=$1 AND deleted_at IS NULL")).
		WithArgs(testExpenseID).WillReturnRows(sqlmock.NewRows([]string{"user_id", "period_id", "category_id", "paid_by", "amount_minor", "currency", "occurred_on", "note"}))
}

func TestCreateExpenseWritesPersonalExpenseAndFrozenSplitInOneTransaction(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	expectNoExistingExpense(mock)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expenses")).
		WithArgs(in.ID, testCallerID, nil, in.CategoryID, in.PaidBy, in.Amount, in.Currency, time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)")).
		WithArgs(in.ID, testCallerID, int64(1250)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	id, err := store.CreateExpense(context.Background(), callerSession(), in)
	if err != nil || id != testExpenseID {
		t.Fatalf("CreateExpense() = (%q, %v), want (%q, nil)", id, err, testExpenseID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseUsesVerifiedCallerForRLSAndOwner(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	expectNoExistingExpense(mock)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expenses")).
		WithArgs(in.ID, testCallerID, nil, in.CategoryID, in.PaidBy, in.Amount, in.Currency, sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)")).
		WithArgs(in.ID, testCallerID, int64(1250)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// The expense input contains no caller identity. Ownership and RLS claims
	// must therefore come only from the verified auth.Session.
	if _, err := store.CreateExpense(context.Background(), callerSession(), in); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseWritesSharedExpenseForPeriodMembers(t *testing.T) {
	store, mock := newMockStore(t)
	period := testPeriodID
	in := CreateExpenseInput{
		ID: testExpenseID, PeriodID: &period, PaidBy: testCallerID,
		Amount: 1000, Currency: "USD", OccurredOn: "2026-03-14",
		Splits: []Split{{UserID: testCallerID, Amount: 334}, {UserID: testMemberID, Amount: 666}},
	}
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT currency, starts_on, ends_on\n\t\t\tFROM public.periods WHERE id=$1 AND deleted_at IS NULL")).
		WithArgs(testPeriodID).WillReturnRows(sqlmock.NewRows([]string{"currency", "starts_on", "ends_on"}).
		AddRow("USD", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id FROM public.period_members WHERE period_id=$1")).
		WithArgs(testPeriodID).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(testCallerID).AddRow(testMemberID))
	expectNoExistingExpense(mock)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expenses")).
		WithArgs(in.ID, testCallerID, &period, nil, in.PaidBy, in.Amount, in.Currency, time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)")).
		WithArgs(in.ID, testCallerID, int64(334)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)")).
		WithArgs(in.ID, testMemberID, int64(666)).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	if _, err := store.CreateExpense(context.Background(), callerSession(), in); err != nil {
		t.Fatalf("CreateExpense() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseActiveMatchingRetryIsIdempotent(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on::text, note\n\t\tFROM public.expenses WHERE id=$1 AND deleted_at IS NULL")).
		WithArgs(testExpenseID).WillReturnRows(sqlmock.NewRows([]string{"user_id", "period_id", "category_id", "paid_by", "amount_minor", "currency", "occurred_on", "note"}).
		AddRow(testCallerID, nil, testCategoryID, testCallerID, int64(1250), "USD", "2026-03-14", nil))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id, amount_minor FROM public.expense_splits WHERE expense_id=$1")).
		WithArgs(testExpenseID).WillReturnRows(sqlmock.NewRows([]string{"user_id", "amount_minor"}).AddRow(testCallerID, int64(1250)))
	mock.ExpectCommit()

	id, err := store.CreateExpense(context.Background(), callerSession(), in)
	if err != nil || id != testExpenseID {
		t.Fatalf("CreateExpense() = (%q, %v), want idempotent success", id, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseActiveRetryWithDifferentPayloadConflicts(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	in.Amount = 1300
	in.Splits[0].Amount = 1300
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on::text, note\n\t\tFROM public.expenses WHERE id=$1 AND deleted_at IS NULL")).
		WithArgs(testExpenseID).WillReturnRows(sqlmock.NewRows([]string{"user_id", "period_id", "category_id", "paid_by", "amount_minor", "currency", "occurred_on", "note"}).
		AddRow(testCallerID, nil, testCategoryID, testCallerID, int64(1250), "USD", "2026-03-14", nil))
	mock.ExpectRollback()

	if _, err := store.CreateExpense(context.Background(), callerSession(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateExpense() error = %v, want ErrConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseRollsBackWhenSplitInsertFails(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	expectNoExistingExpense(mock)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expenses")).
		WithArgs(in.ID, testCallerID, nil, in.CategoryID, in.PaidBy, in.Amount, in.Currency, sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)")).
		WithArgs(in.ID, testCallerID, int64(1250)).WillReturnError(errors.New("split write failed"))
	mock.ExpectRollback()

	if _, err := store.CreateExpense(context.Background(), callerSession(), in); err == nil {
		t.Fatal("CreateExpense() error = nil, want split insert failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseTreatsSoftDeletedIDAsConflict(t *testing.T) {
	store, mock := newMockStore(t)
	in := personalExpense()
	expectCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS(SELECT 1 FROM public.categories WHERE id=$1 AND user_id=$2)")).
		WithArgs(testCategoryID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	// Deleted rows are not idempotent successes. The insert reaches the unique
	// ID constraint and is surfaced as a conflict, preserving soft deletion.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on::text, note\n\t\tFROM public.expenses WHERE id=$1 AND deleted_at IS NULL")).
		WithArgs(testExpenseID).WillReturnRows(sqlmock.NewRows([]string{"user_id", "period_id", "category_id", "paid_by", "amount_minor", "currency", "occurred_on", "note"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.expenses")).
		WithArgs(in.ID, testCallerID, nil, in.CategoryID, in.PaidBy, in.Amount, in.Currency, sqlmock.AnyArg(), nil).
		WillReturnError(&postgresStateError{state: "23505"})
	mock.ExpectRollback()

	if _, err := store.CreateExpense(context.Background(), callerSession(), in); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateExpense() error = %v, want ErrConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateExpenseRejectsMissingVerifiedSessionBeforeDatabaseAccess(t *testing.T) {
	store, mock := newMockStore(t)
	if _, err := store.CreateExpense(context.Background(), auth.Session{}, personalExpense()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateExpense() error = %v, want ErrForbidden", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateShapeDocumentsMoneyAndSplitInvariants(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CreateExpenseInput)
		want bool
	}{
		{name: "exact integer shares", want: true},
		{name: "share total too small", edit: func(in *CreateExpenseInput) { in.Splits[0].Amount-- }},
		{name: "zero share", edit: func(in *CreateExpenseInput) { in.Splits[0].Amount = 0 }},
		{name: "duplicate participant", edit: func(in *CreateExpenseInput) { in.Splits = append(in.Splits, in.Splits[0]); in.Amount *= 2 }},
		{name: "overflow safe sum", edit: func(in *CreateExpenseInput) {
			in.Amount = 1<<63 - 1
			in.Splits = []Split{{UserID: testCallerID, Amount: 1<<63 - 1}, {UserID: "22222222-2222-2222-2222-222222222222", Amount: 1<<63 - 1}}
		}},
		{name: "invalid calendar date", edit: func(in *CreateExpenseInput) { in.OccurredOn = "2026-02-30" }},
		{name: "currency must be uppercase", edit: func(in *CreateExpenseInput) { in.Currency = "usd" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := personalExpense()
			if tt.edit != nil {
				tt.edit(&in)
			}
			err := validateShape(in)
			if got := err == nil; got != tt.want {
				t.Fatalf("validateShape() error = %v, want valid=%v", err, tt.want)
			}
		})
	}
}

type postgresStateError struct{ state string }

func (e *postgresStateError) Error() string    { return "postgres error " + e.state }
func (e *postgresStateError) SQLState() string { return e.state }
