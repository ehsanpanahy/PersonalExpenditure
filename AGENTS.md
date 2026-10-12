# AGENTS.md

Project instructions for **PersonalExpendiure** — a multi-tenant expense tracker
with shared-cost "Periods".

## Read this first

- `docs/REQUIREMENTS.md` — what the product does and why. Source of truth for intent.
- `docs/ARCHITECTURE.md` — schema, RLS policies, business logic, trade-offs.

Both were produced by a design interview. Do not silently contradict them. If you
need to change a decision, change the document too, in the same commit.

## Current status

**The project is not scaffolded yet.** It contains only documentation. Stack and
conventions below describe the agreed target; the commands section applies once
`create-expo-app` and `supabase init` have been run.

## Stack (already decided — do not substitute)

- React Native + Expo, TypeScript
- Expo Router (file-based navigation)
- TanStack Query — the **only** owner of server data
- Supabase: managed Postgres + Auth + Row Level Security
- Business logic in SQL (functions + views), not TypeScript
- Jest + jest-expo, pgTAP against the local Supabase stack
- GitHub Actions + EAS Build + EAS Update

Deliberately excluded from v1: web, desktop, Flutter, Redux, NestJS/Fastify.
See the out-of-scope list in `docs/REQUIREMENTS.md` §7.

## Rules that must never be violated

These encode real, hard-won correctness constraints. Breaking one produces bugs
that are **silent** — wrong numbers, or leaked data — rather than crashes.

1. **Money is always an integer count of minor units** (`amount_minor bigint`),
   paired with an ISO-4217 currency code. Never `float`, never a decimal
   fraction. All parsing and formatting goes through `src/shared/money/`.
2. **Postgres is the single source of truth.** Balance, budget, and report
   aggregation lives in SQL (`src/db` calls RPCs/views). Do not compute balances
   in TypeScript. If a second client ever needs these numbers, add SQL — never a
   second implementation.
3. **Row Level Security is enabled on every table**, and is the only thing
   preventing one customer from reading another's financial data. Do not disable,
   weaken, or add `service_role`-key code paths to client-side modules. The
   service-role key belongs in server-only code and must never ship in the app.
4. **Split shares are frozen at creation.** Store the computed `share_minor` per
   participant, never a rule ("equal 3-way"). Invariant:
   `SUM(splits.amount_minor) == expenses.amount_minor` exactly, enforced by
   `create_expense()` and asserted in tests.
5. **Soft delete everywhere** (`deleted_at`). Only account deletion erases data.
   A hard-deleted row can resurrect in a client's cache because a refetching
   client that already holds it never learns it is gone.
6. **Personal and shared money never merge.** "My spending this month" counts
   only personal expenses. A user's share of shared costs is displayed
   separately. See `docs/REQUIREMENTS.md` BR-6.
7. **Never derive month boundaries in UTC.** Compute them in the user's IANA
   time zone and pass explicit `(from, to)` date ranges — never a `'2026-03'`
   string.
8. **Balances are computed, never maintained.** No incrementally-updated
   `balances` table. A stored total drifts; a computed one cannot.
9. **The server validates everything** (`BR-10`). Client-side validation is for
   user experience only and is never a substitute for a server check.

## Conventions

- `src/shared/` is **pure TypeScript**: no `react`, no `expo`, no
  `@supabase/supabase-js`. This is what makes it unit-testable. Keep it that way.
- `src/db/types.ts` is **generated** from the live schema and **is committed**.
  Never hand-edit it; regenerate instead.
- `app/` routes stay thin — no business logic. Logic goes in `src/features/*`;
  pure logic in `src/shared`.
- TanStack Query owns server data. **Do not mirror query results into Zustand,
  Context, or any local store.** Local stores hold UI state only (selected month,
  dismissed onboarding). Duplicated caches are how the same expense ends up
  displayed two different ways.
- Feature folders are `src/features/<feature>/` and own their hooks, components,
  and screens together.

## Testing requirements

- **Golden dataset test** — a fixed, hand-computed set of expenses with exact
  expected balances asserted as integers. Money bugs produce wrong numbers, not
  exceptions, so "no errors" is not a passing test.
- **RLS suite** — seed two users sharing a Period and one outsider; assert the
  outsider reads zero rows. Test direct table access, not just app queries, or
  you are only proving the app filters politely.
- **Split-rounding conformance** — the rounding rule exists in both SQL and
  TypeScript. A shared fixture set must drive assertions in both the Jest and
  pgTAP suites so they cannot drift.

Must-cover cases: an amount that does not divide evenly, a one-cent expense, a
member who joined partway through, a custom exact split, a soft-deleted
expense, an expense with no category.

## Commands (once scaffolded)

```bash
npm run typecheck      # tsc --noEmit
npm test               # jest
npm run lint
npx supabase start     # local Postgres with RLS enabled
npx supabase db reset  # apply migrations + seed
npx supabase test db   # pgTAP: RLS + golden dataset
npx supabase gen types typescript --local
```

Every migration must be applied and tested in CI against an ephemeral Supabase
branch. A migration that has only run on one laptop will break production.

## Secrets

`.env`, `.env.local`, `*.keystore`, `*.jks`, `*.p8`, `*.p12`,
`GoogleService-Info.plist`, and `google-services.json` are gitignored and must
never be committed or pasted into a commit message, issue, or log.

## Open decisions

Do not implement these without an explicit answer — they are genuinely
unsettled, not merely undocumented:

- **Split-rounding duplication.** The rounding rule lives in SQL (authoritative)
  and TypeScript (optimistic preview). Whether to keep both with a conformance
  test, or drop the TypeScript copy and preview from the server, is undecided.
- **Backend language.** v1 has no application backend. If an edge function is
  ever needed, TypeScript (Deno or Node runtime) is the preference; PL/pgSQL for
  anything data-shaped. Decide when there is a concrete need.
- Refunds / negative amounts, period-member removal, and the "Period" product
  name. See `docs/REQUIREMENTS.md` §9 and `docs/ARCHITECTURE.md` §9.
