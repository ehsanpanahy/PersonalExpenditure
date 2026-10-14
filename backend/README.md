# Go API

The v1 Go API is a stateless HTTP boundary in front of Supabase. Supabase Auth
owns passwords and sessions. The app sends its Supabase access token as a bearer
token; the API validates it with Supabase Auth and forwards that same token on
user-scoped PostgREST/RPC requests so Postgres can apply RLS.

The API must never accept email/password as authorization for data requests, use
a service-role key for user-scoped work, or fall back to privileged credentials
when token forwarding fails. Data validation, split allocation, balances,
budgets, and reports remain in Postgres functions and views.

## Current scaffold

- `GET /healthz` is an unauthenticated liveness check.
- `GET /v1/me` is a protected identity check returning the user ID verified by
  Supabase Auth.
- `internal/supabase` contains the request-scoped PostgREST/RPC transport for
  feature routes that will be added with their API contracts.
- `openapi.yaml` is the contract for the currently implemented routes. Extend it
  before adding app-facing endpoints; generate mobile API types from it.

This is the backend foundation, not the complete expense API. Feature endpoints
for expenses, periods, budgets, reports, and exports remain to be designed and
implemented.

## Run locally

1. Start the local Supabase stack with the repository's Supabase configuration.
2. Copy `.env.example` values into your shell or a local ignored `.env` file and
   set `SUPABASE_PUBLISHABLE_KEY` to the local publishable key (or legacy anon
   key).
3. From `backend/`, run `go run ./cmd/api`.
4. Check `GET http://localhost:8080/healthz`.

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
