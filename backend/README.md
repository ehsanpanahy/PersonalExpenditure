# Go API

The v1 Go API is a stateless HTTP boundary in front of Supabase. Supabase Auth
owns passwords and sessions. The app sends its Supabase access token as a bearer
token; the API validates it with Supabase Auth. Reads forward that token to
PostgREST so Postgres can apply RLS. Go-owned writes use a dedicated Postgres
login that can assume the restricted `app_api` role. Each write opens a
transaction, installs claims from the verified caller, switches to `app_api`,
and only then reads or writes rows.

The API must never accept email/password as authorization for data requests, use
a service-role key for user-scoped work, or run a user-scoped query outside the
caller-scoped database transaction. Go owns application workflows and
authoritative request validation. Postgres RLS, constraints, and transactions
remain the data-security and integrity boundary. Balances, budgets, and reports
remain SQL aggregates.

## Current scaffold

- `GET /healthz` is an unauthenticated liveness check.
- `GET /v1/me` is a protected identity check returning the user ID verified by
  Supabase Auth.
- `GET /v1/expenses?from=YYYY-MM-DD&to=YYYY-MM-DD` lists non-deleted personal
  expenses for an explicit inclusive date range. Optional pagination uses
  `limit` (default 50, max 100) and `offset` (max 10000). Shared expenses are
  excluded by the query and tenant visibility is enforced by RLS.
- `POST /v1/expenses` validates and records a personal (`period_id` null, owned
  `category_id`, one owner split) or shared (`period_id` set, `category_id`
  null, one positive split per participant) expense in Go. Expense and split
  rows are inserted in one caller-scoped database transaction. A deferred
  database trigger enforces the exact split-total invariant. Amounts cross as
  decimal strings. Client-generated `id` values are idempotent (same
  id + same payload returns the id with 201); a conflicting payload is 409.
- Expense amounts are returned as decimal strings to preserve Postgres `bigint`
  precision in JavaScript clients.
- `supabase/migrations/20261014000000_expense_read_schema.sql` creates the
  profile/category/period/expense tables and RLS policies needed for the read
  path. `supabase/migrations/20261015000000_expense_writes.sql` adds the
  `expense_splits` table and computed `period_balances`.
- `supabase/migrations/20261016000000_go_expense_writes.sql` adds the restricted
  API roles, write-specific RLS policies, and deferred split-total checks. The
  old SQL write RPCs are executable only by `app_api` for the existing pgTAP
  conformance fixtures; the Go API does not call them, and mobile-authenticated
  clients cannot use them as an alternate write path.
- `openapi.yaml` is the API contract; generate mobile API types from it.

This is the backend foundation plus personal expense reads and writes.
Expense editing/deletion, periods, budgets, reports, and exports remain to be
designed and implemented. Add each feature as a Go workflow backed by
caller-scoped transactions, RLS, and database integrity constraints.

## Run locally

1. Start the local Supabase stack with the repository's Supabase configuration.
2. Copy `.env.example` values into your shell or a local ignored `.env` file.
   Set `SUPABASE_PUBLISHABLE_KEY` to the local publishable key (or legacy anon
   key), and configure `DATABASE_URL` for `app_api_login`.
   After `supabase db reset`, enable the restricted login and set a local-only
   password using a database-owner connection:

   ```sql
   ALTER ROLE app_api_login LOGIN PASSWORD '<local-only-secret>';
   ```

   Use a unique secret in production and provide it through the deployment
   secret manager, and use `sslmode=require` (or stronger) for non-local hosts.
   The API rejects a database URL that does not use `app_api_login` or does not
   require TLS remotely. Never connect the API as `postgres`, `service_role`, or
   a role with `BYPASSRLS`.
3. From `backend/`, run `go run ./cmd/api`.
4. Check `GET http://localhost:8080/healthz`.

Production deployments must expose the API only over HTTPS, either directly or
through a TLS-terminating reverse proxy. Never send bearer tokens over plaintext
HTTP outside local development.

For `/v1/me`, sign in through Supabase Auth and send the resulting access token:

```http
GET /v1/me HTTP/1.1
Authorization: Bearer <supabase-access-token>
```

The current verifier makes an Auth user-endpoint request for each protected
request. This favors a small, easy-to-audit initial implementation; its latency
and availability impact should be measured before production rollout. The
`auth.Verifier` interface allows a vetted local JWKS verifier later without
changing the HTTP or downstream RLS contract.
