/**
 * GENERATED from backend/openapi.yaml — do not hand-edit.
 * Regenerate instead so the mobile contract cannot drift from the Go API.
 * Source: PersonalExpenditure API v0.1.0 (`GET /v1/expenses`, `GET /v1/me`).
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
