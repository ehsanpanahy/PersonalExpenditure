package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ehsanpanahy/PersonalExpenditure/backend/internal/auth"
)

const testManagerID = "33333333-3333-3333-3333-333333333333"

type periodArrayConverter struct{}

func (periodArrayConverter) ConvertValue(value any) (driver.Value, error) {
	if values, ok := value.([]string); ok {
		return values, nil
	}
	return driver.DefaultParameterConverter.ConvertValue(value)
}

func newPeriodMockStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.ValueConverterOption(periodArrayConverter{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db), mock
}

func expectPeriodCallerContext(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL ROLE app_api").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claims'").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.sub'").WithArgs(testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT set_config\\('request.jwt.claim.role'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM public.profiles WHERE auth_user_id=$1 AND deleted_at IS NULL")).WithArgs(testCallerID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testCallerID))
}

func TestValidateCreatePeriodInput(t *testing.T) {
	end := "2026-03-10"
	valid := CreatePeriodInput{
		Name: "Spring trip", StartsOn: "2026-03-01", EndsOn: &end, Currency: "USD",
		MemberEmails: []string{"a@example.com", "b@example.com"}, ManagerEmail: "b@example.com",
	}
	if err := ValidateCreatePeriodInput(valid); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	// An ongoing period may omit the caller from the requested roster; since the
	// creator is always added, they may also be selected as manager.
	valid.EndsOn = nil
	valid.MemberEmails = nil
	valid.ManagerEmail = "creator@example.com"
	if err := ValidateCreatePeriodInput(valid); err != nil {
		t.Fatalf("ongoing period without caller in requested roster rejected: %v", err)
	}
}

func TestValidateCreatePeriodInputRejectsBusinessRuleViolations(t *testing.T) {
	endBefore := "2026-02-28"
	invalidDate := "2026-02-30"
	tooLongName := make([]rune, 121)
	for i := range tooLongName {
		tooLongName[i] = 'x'
	}
	cases := []struct {
		name   string
		mutate func(*CreatePeriodInput)
	}{
		{"blank name", func(in *CreatePeriodInput) { in.Name = " " }},
		{"name has outer whitespace", func(in *CreatePeriodInput) { in.Name = " Trip" }},
		{"name too long", func(in *CreatePeriodInput) { in.Name = string(tooLongName) }},
		{"invalid start date", func(in *CreatePeriodInput) { in.StartsOn = invalidDate }},
		{"end before start", func(in *CreatePeriodInput) { in.EndsOn = &endBefore }},
		{"lowercase currency", func(in *CreatePeriodInput) { in.Currency = "usd" }},
		{"currency wrong length", func(in *CreatePeriodInput) { in.Currency = "US" }},
		{"malformed member email", func(in *CreatePeriodInput) { in.MemberEmails = []string{"not-email"} }},
		{"duplicate case-insensitive member", func(in *CreatePeriodInput) { in.MemberEmails = []string{"a@example.com", "A@example.com"} }},
		{"malformed manager email", func(in *CreatePeriodInput) { in.ManagerEmail = "manager" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := CreatePeriodInput{Name: "Trip", StartsOn: "2026-03-01", Currency: "USD", MemberEmails: []string{"a@example.com"}, ManagerEmail: "a@example.com"}
			tc.mutate(&in)
			if err := ValidateCreatePeriodInput(in); !errors.Is(err, ErrInvalidPeriod) {
				t.Fatalf("ValidateCreatePeriodInput() error = %v, want ErrInvalidPeriod", err)
			}
		})
	}
}

func TestCreatePeriodWritesRosterAndSelectedManagerInOneCallerTransaction(t *testing.T) {
	store, mock := newPeriodMockStore(t)
	session := auth.Session{Principal: auth.Principal{UserID: testCallerID}, AccessToken: "verified-access-token"}
	in := CreatePeriodInput{
		Name: "Spring trip", StartsOn: "2026-03-01", Currency: "USD",
		MemberEmails: []string{"member@example.com", "manager@example.com"}, ManagerEmail: "manager@example.com",
	}
	expectPeriodCallerContext(mock)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT email, profile_id FROM public.resolve_period_member_emails($1::text[])")).WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"email", "profile_id"}).AddRow("member@example.com", testMemberID).AddRow("manager@example.com", testManagerID))
	mock.ExpectQuery("INSERT INTO public\\.periods").WithArgs(in.Name, testCallerID, in.StartsOn, nil, in.Currency).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPeriodID))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.period_members (period_id,user_id,role) VALUES ($1,$2,$3)")).WithArgs(testPeriodID, testCallerID, "manager").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.period_members (period_id,user_id,role) VALUES ($1,$2,$3)")).WithArgs(testPeriodID, testMemberID, "member").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO public.period_members (period_id,user_id,role) VALUES ($1,$2,$3)")).WithArgs(testPeriodID, testManagerID, "member").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.periods SET roster_finalized=true, updated_at=now() WHERE id=$1")).WithArgs(testPeriodID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.period_members SET role='member' WHERE period_id=$1 AND user_id=$2")).WithArgs(testPeriodID, testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.period_members SET role='manager' WHERE period_id=$1 AND user_id=$2")).WithArgs(testPeriodID, testManagerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.periods SET manager_id=$2, updated_at=now() WHERE id=$1")).WithArgs(testPeriodID, testManagerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,name,manager_id,starts_on::text,ends_on::text,currency::text FROM public.periods WHERE id=$1")).WithArgs(testPeriodID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "manager_id", "starts_on", "ends_on", "currency"}).AddRow(testPeriodID, in.Name, testManagerID, in.StartsOn, nil, in.Currency))
	mock.ExpectCommit()

	period, err := store.CreatePeriod(context.Background(), session, in)
	if err != nil {
		t.Fatalf("CreatePeriod() error = %v", err)
	}
	if period.ID != testPeriodID || period.ManagerID != testManagerID || period.Name != in.Name {
		t.Fatalf("CreatePeriod() = %#v; want created period with selected manager", period)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTransferPeriodManagerUsesVerifiedCallerAndDirectScopedUpdates(t *testing.T) {
	store, mock := newPeriodMockStore(t)
	session := auth.Session{Principal: auth.Principal{UserID: testCallerID}, AccessToken: "verified-access-token"}
	expectPeriodCallerContext(mock)
	mock.ExpectQuery("SELECT p\\.id FROM public\\.periods AS p").WithArgs(testPeriodID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPeriodID))
	mock.ExpectQuery("SELECT EXISTS\\(SELECT 1 FROM public\\.period_members").WithArgs(testPeriodID, testManagerID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.period_members SET role='member' WHERE period_id=$1 AND user_id=$2 AND role='manager'")).WithArgs(testPeriodID, testCallerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.period_members SET role='manager' WHERE period_id=$1 AND user_id=$2 AND role='member'")).WithArgs(testPeriodID, testManagerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.periods SET manager_id=$2, updated_at=now() WHERE id=$1")).WithArgs(testPeriodID, testManagerID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.TransferPeriodManager(context.Background(), session, testPeriodID, testManagerID); err != nil {
		t.Fatalf("TransferPeriodManager() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSoftDeletePeriodUsesManagerScopedUpdateInCallerTransaction(t *testing.T) {
	store, mock := newPeriodMockStore(t)
	session := auth.Session{Principal: auth.Principal{UserID: testCallerID}, AccessToken: "verified-access-token"}
	expectPeriodCallerContext(mock)
	mock.ExpectQuery("SELECT p\\.id FROM public\\.periods AS p").WithArgs(testPeriodID, testCallerID).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPeriodID))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE public.periods SET deleted_at=now(),updated_at=now() WHERE id=$1")).WithArgs(testPeriodID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.SoftDeletePeriod(context.Background(), session, testPeriodID); err != nil {
		t.Fatalf("SoftDeletePeriod() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
