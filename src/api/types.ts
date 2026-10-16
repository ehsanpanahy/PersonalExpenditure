/**
 * GENERATED from backend/openapi.yaml — do not hand-edit.
 * Regenerate instead so the mobile contract cannot drift from the Go API.
 * Source: PersonalExpenditure API v0.2.0 (`GET /v1/expenses`, `POST /v1/expenses`, `GET /v1/me`).
 */

export interface PersonalExpense {
  id: string;
  /** Integer minor units as a decimal string — preserves Postgres bigint precision in JS. */
  amount_minor: string;
  currency: string;
  occurred_on: string;
  category_id: string | null;
  note: string | null;
  created_at: string;
}

export interface CreateExpenseSplit {
  /** Split participant; must be a member of the period for shared expenses. */
  user_id: string;
  /** Frozen share in integer minor units, as a string to preserve bigint precision. */
  amount_minor: string;
}

export interface CreateExpenseInput {
  /** Client-generated expense id for idempotent create and offline replay. */
  id: string;
  /** Null for a personal expense; set for a shared expense in that period. */
  period_id?: string | null;
  /** Required for personal expenses (owned category); must be null for shared expenses. */
  category_id?: string | null;
  /** Who fronted the money. Equals the caller for personal expenses. */
  paid_by: string;
  /** Expense total in integer minor units, as a string. Must be > 0 and fit in bigint. */
  amount_minor: string;
  currency: string;
  occurred_on: string;
  note?: string | null;
  /** Frozen shares totalling exactly amount_minor. One entry (caller, full amount) for personal expenses. */
  splits: { user_id: string; amount_minor: string }[];
}

export interface CreateExpenseResult {
  id: string;
}

export interface ApiErrorBody {
  code: string;
  message: string;
}

export interface ApiErrorShape {
  error: ApiErrorBody;
}

export interface ListPersonalExpensesParams {
  /** Inclusive start date (YYYY-MM-DD), month bounds computed in the user's IANA zone. */
  from: string;
  /** Inclusive end date (YYYY-MM-DD). */
  to: string;
  limit?: number;
  offset?: number;
}
