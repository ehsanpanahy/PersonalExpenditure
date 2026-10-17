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

func expectCategoryCaller(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL ROLE app_api").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claims'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.sub'").WithArgs(testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.role'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM public.profiles WHERE auth_user_id=$1 AND deleted_at IS NULL")).
		WithArgs(testCallerID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testCallerID))
}

func categoryResultRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "name", "icon", "color", "sort_order", "is_archived"}).
		AddRow(testCategoryID, "Food", "fork", "blue", 3, false)
}

func TestCreateCategoryUsesVerifiedOwnerAndReturnsCategory(t *testing.T) {
	store, mock := newMockStore(t)
	expectCategoryCaller(mock)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO public.categories (user_id, name, icon, color, sort_order)")).
		WithArgs(testCallerID, "Food", "fork", "blue", 3).WillReturnRows(categoryResultRows())
	mock.ExpectCommit()
	category, err := store.CreateCategory(context.Background(), callerSession(), SaveCategoryInput{
		Name: "Food", Icon: stringPtr("fork"), Color: stringPtr("blue"), SortOrder: 3,
	})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}
	if category.ID != testCategoryID || category.Name != "Food" || category.SortOrder != 3 || category.Archived {
		t.Fatalf("CreateCategory() = %#v", category)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCategoryScopesByVerifiedOwnerAndHidesForeignCategory(t *testing.T) {
	t.Run("owned category updates", func(t *testing.T) {
		store, mock := newMockStore(t)
		expectCategoryCaller(mock)
		mock.ExpectQuery(regexp.QuoteMeta("UPDATE public.categories SET name=$3, icon=$4, color=$5, sort_order=$6")).
			WithArgs(testCategoryID, testCallerID, "Dining", nil, nil, 4).WillReturnRows(categoryResultRows())
		mock.ExpectCommit()
		if _, err := store.UpdateCategory(context.Background(), callerSession(), testCategoryID, SaveCategoryInput{Name: "Dining", SortOrder: 4}); err != nil {
			t.Fatalf("UpdateCategory() error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unavailable category", func(t *testing.T) {
		store, mock := newMockStore(t)
		expectCategoryCaller(mock)
		mock.ExpectQuery(regexp.QuoteMeta("UPDATE public.categories SET name=$3, icon=$4, color=$5, sort_order=$6")).
			WithArgs(testCategoryID, testCallerID, "Dining", nil, nil, 4).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "icon", "color", "sort_order", "is_archived"}))
		mock.ExpectRollback()
		_, err := store.UpdateCategory(context.Background(), callerSession(), testCategoryID, SaveCategoryInput{Name: "Dining", SortOrder: 4})
		if !errors.Is(err, ErrCategoryUnavailable) {
			t.Fatalf("error = %v, want unavailable", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestArchiveCategoryIsReversibleAndOwnerScoped(t *testing.T) {
	for _, archived := range []bool{true, false} {
		t.Run(map[bool]string{true: "archive", false: "restore"}[archived], func(t *testing.T) {
			store, mock := newMockStore(t)
			expectCategoryCaller(mock)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE public.categories SET is_archived=$3 WHERE id=$1 AND user_id=$2")).
				WithArgs(testCategoryID, testCallerID, archived).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := store.ArchiveCategory(context.Background(), callerSession(), testCategoryID, archived); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCategoryInputValidationAndPostgresConflictMapping(t *testing.T) {
	for _, in := range []SaveCategoryInput{{Name: "", SortOrder: 0}, {Name: " padded ", SortOrder: 0}, {Name: "valid", SortOrder: -1}, {Name: "valid", SortOrder: 100001}} {
		if err := ValidateCategoryInput(in); !errors.Is(err, ErrInvalidCategory) {
			t.Errorf("ValidateCategoryInput(%#v) = %v, want invalid", in, err)
		}
	}
	if err := ValidateCategoryInput(SaveCategoryInput{Name: "valid", SortOrder: 100000}); err != nil {
		t.Fatalf("valid boundary rejected: %v", err)
	}
	if got := classifyCategoryError(&postgresStateError{state: "23505"}); !errors.Is(got, ErrCategoryConflict) {
		t.Fatalf("duplicate mapping = %v", got)
	}
}

func TestUpdatePersonalExpenseCallsTransactionalDatabaseWorkflow(t *testing.T) {
	store, mock := newMockStore(t)
	expectMutationCallerContext(mock)
	mock.ExpectExec(regexp.QuoteMeta("SELECT public.update_expense($1,$2,$3,$4,$5,true)")).
		WithArgs(testExpenseID, int64(987), "2026-03-14", testCategoryID, "revised note").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	note := "revised note"
	if err := store.UpdatePersonalExpense(context.Background(), callerSession(), testExpenseID, 987, "2026-03-14", testCategoryID, &note); err != nil {
		t.Fatalf("UpdatePersonalExpense() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdatePersonalExpenseRejectsInvalidInputBeforeDatabaseAccess(t *testing.T) {
	cases := []struct {
		name, id, category, date string
		amount                   int64
	}{
		{"bad expense id", "bad", testCategoryID, "2026-03-14", 1},
		{"bad category id", testExpenseID, "bad", "2026-03-14", 1},
		{"zero amount", testExpenseID, testCategoryID, "2026-03-14", 0},
		{"negative amount", testExpenseID, testCategoryID, "2026-03-14", -5},
		{"invalid date", testExpenseID, testCategoryID, "2026-02-30", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newMockStore(t)
			if err := store.UpdatePersonalExpense(context.Background(), callerSession(), tc.id, tc.amount, tc.date, tc.category, nil); !errors.Is(err, ErrInvalidExpense) {
				t.Fatalf("error = %v, want ErrInvalidExpense", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSoftDeleteExpenseCallsSoftDeleteFunctionAndCommits(t *testing.T) {
	store, mock := newMockStore(t)
	expectMutationCallerContext(mock)
	mock.ExpectExec(regexp.QuoteMeta("SELECT public.soft_delete_expense($1)")).WithArgs(testExpenseID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.SoftDeleteExpense(context.Background(), callerSession(), testExpenseID); err != nil {
		t.Fatalf("SoftDeleteExpense() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalExpenseMutationsMapDatabaseAuthorizationFailures(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		store, mock := newMockStore(t)
		expectMutationCallerContext(mock)
		mock.ExpectExec(regexp.QuoteMeta("SELECT public.update_expense($1,$2,$3,$4,$5,true)")).WithArgs(testExpenseID, int64(1), "2026-03-14", testCategoryID, nil).WillReturnError(&postgresStateError{state: "42501"})
		mock.ExpectRollback()
		if err := store.UpdatePersonalExpense(context.Background(), callerSession(), testExpenseID, 1, "2026-03-14", testCategoryID, nil); !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v, want ErrForbidden", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		store, mock := newMockStore(t)
		expectMutationCallerContext(mock)
		mock.ExpectExec(regexp.QuoteMeta("SELECT public.soft_delete_expense($1)")).WithArgs(testExpenseID).WillReturnError(&postgresStateError{state: "42501"})
		mock.ExpectRollback()
		if err := store.SoftDeleteExpense(context.Background(), callerSession(), testExpenseID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v, want ErrForbidden", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func expectMutationCallerContext(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL ROLE app_api").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claims'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.sub'").WithArgs(testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.role'").WillReturnResult(sqlmock.NewResult(0, 1))
}

func stringPtr(value string) *string { return &value }
