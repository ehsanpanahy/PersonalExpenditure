# Module View — Developer Guide

Status: evolving implementation guide. Companion to
[`REQUIREMENTS.md`](./REQUIREMENTS.md) and [`ARCHITECTURE.md`](./ARCHITECTURE.md).

**Purpose.** This document is the enforcement layer. `REQUIREMENTS.md` says what
the app must do; `ARCHITECTURE.md` says how the data is shaped; this document says
**where every piece of code lives, and what it is allowed to touch.**

A developer should be able to answer two questions from this document alone:
*"where do I put this?"* and *"why is that not allowed here?"*

> **Status note.** The project is partially scaffolded. Paths below describe the
> intended boundaries; check the current repository before assuming a module
> exists.

---

## 1. The layering model

Four layers. Each one may only use the layer below it.

```mermaid
graph TD
    L0["L0 — Routes (app/)<br/>screen composition only"]
    L1["L1 — Features (src/features/&lt;feature&gt;/)<br/>screens, hooks, orchestration"]
    L2["L2 — API client (src/api/)<br/>typed client for Go"]
    L3["L3 — Pure domain (src/shared/)<br/>no framework, no database, no React"]
    L4["L4 — Postgres (supabase/)<br/>RLS, persisted integrity, aggregates"]

    L0 --> L1
    L1 --> L2
    L2 --> Go["Go application API<br/>workflows and authoritative request validation"]
    Go --> L4
    L1 --> L3
    L3 -.- L4

    style L4 fill:#f9d5d3,stroke:#c0392b
    style L3 fill:#d5e8d4,stroke:#27ae60
```

**Rules, in one place:**

| Rule | Statement |
| --- | --- |
| R1 | Arrows point down only. Nothing imports upward. |
| R2 | `src/shared/` imports **nothing** from React, Expo, or Supabase — only itself and the standard library. |
| R3 | Application data goes through the typed `src/api/` Go API client. Supabase Auth is used only for identity and session management. |
| R4 | `app/` routes import only `src/features/*` and `src/ui/*`. No `src/api`, no `src/shared`. |
| R5 | A feature may not import another feature's internals. Only its public `index.ts`. |
| R6 | Go owns application rules and request validation. Postgres owns RLS, constraints, triggers, and aggregates. Client checks only improve UX. |

### Why these rules exist

The expensive bugs in this product are not crashes. They are **silently wrong
numbers** and **data visible to the wrong person**. Every rule above exists to
prevent one of those:

- **R2** is what makes the money and date logic unit-testable without a phone, a
  simulator, or a database.
- **R3** keeps the mobile app away from database tables and prevents a privileged
  database key from entering client code.
- **R6** keeps workflow decisions in a testable application layer while database
  safeguards still protect persisted data if a caller makes a mistake.

---

## 2. Dependency matrix

Read as *row may import column*.

| | L0 routes | L1 features | L2 API | L3 shared | L4 Postgres | ui |
|---|---|---|---|---|---|---|
| **L0 routes** | — | ✅ | ❌ | ❌ | ❌ | ✅ |
| **L1 features** | ❌ | ⚠️ public only | ✅ | ✅ | ❌ | ✅ |
| **L2 API** | ❌ | ❌ | — | ❌ | ❌ | ❌ |
| **L3 shared** | ❌ | ❌ | ❌ | ⚠️ same layer only | ❌ | ❌ |
| **L4 Postgres** | ❌ | ❌ | ❌ | ❌ | — | — |

⚠️ = allowed with a restriction, explained in §6.

`src/ui/` is a leaf: the design system (buttons, inputs, progress bars, chart
primitives). It imports nothing but itself and L3 for formatting.

**Enforcement.** These are lint-checkable, not aspirational. Add
`eslint-plugin-boundaries` (or `no-restricted-imports`) with the matrix above;
the rule fails CI on violation. A module view that isn't enforced becomes
historical documentation within two sprints.

---

## 3. Module catalogue

### L0 — Routes (`app/`)

| Module | Responsibility | Must not |
| --- | --- | --- |
| `app/_layout.tsx` | Providers: QueryClient, auth gate, theme. | Contain logic. |
| `app/(onboarding)/` | Single screen, two questions. | Read the database directly. |
| `app/(tabs)/index.tsx` | This month. | Compute anything. |
| `app/(tabs)/reports.tsx` | Monthly report. | Know what a column is called. |
| `app/(tabs)/settings.tsx` | Preferences, export, delete account. | Hold the export logic itself. |
| `app/expense/[id].tsx` | Create / edit personal expense. | Validate splits. |
| `app/period/[id]/index.tsx` | Period expense list. | Decide who can delete. |
| `app/period/[id]/balances.tsx` | The balances screen. | Sum anything — call L4. |
| `app/period/[id]/budget.tsx` | Period budget progress. | Compare to a total. |

A route is a *signpost*. It maps a URL to a feature screen and nothing more.

### L1 — Features (`src/features/`)

| Module | Responsibility | Public surface |
| --- | --- | --- |
| `accounts/` | Sign up, verify, sign in, reset, sign out, delete account. | `useAuth`, `useSession` |
| `onboarding/` | Choose currency, confirm categories. | `OnboardingScreen`, `useCompleteOnboarding` |
| `categories/` | Create, rename, re-order, archive, seed on signup. | `useCategories` |
| `expenses/` | Personal expenses: list, filter, search, create, edit, delete. | `useExpenses`, `ExpenseScreen`, `useCreateExpense` |
| `periods/` | Create a period, roster, manager transfer, member list. | `usePeriod`, `usePeriods`, `PeriodScreen` |
| `splits/` | Choose participants, preview shares, validate exact amounts. *(UI for L3 logic)* | `useSplitPreview`, `SplitEditor` |
| `budgets/` | Budget create/edit and progress. | `useBudgets`, `useBudgetProgress` |
| `reports/` | Monthly totals, category breakdown, per-day. | `useMonthlyReport` |
| `export/` | CSV and JSON backup generation. | `useExportData` |

**This is the only layer with TanStack Query hooks.** That is deliberate: the
query cache is the app's view of server state, and having hooks in one layer
makes it auditable.

#### Inside a feature folder

```
src/features/<feature>/
├─ index.ts            # public surface — the only import path for outsiders
├─ screens/            # full-screen components
├─ components/         # local presentational pieces
├─ hooks/              # TanStack Query hooks: useX, useCreateX
├─ queries.ts          # query keys + read query definitions
├─ mutations.ts        # write operations, calling the Go API
└─ types.ts            # feature-local view models
```

Rule R5 in practice: `src/features/periods/queries.ts` may import
`../../features/splits` but **never**
`../../features/splits/queries`. If two features genuinely need the same
capability, it belongs in `src/shared/` or the typed `src/api/` client.

### L2 — API client (`src/api/`)

The mobile app talks to the Go API for application data. It does not access
Postgres tables or RPCs directly.

| Module | Responsibility |
| --- | --- |
| `client.ts` | Typed HTTP client for the Go API; attaches the Supabase access token. |
| `types.ts` | **Generated from `backend/openapi.yaml`.** Never hand-edited. |

TanStack Query owns API response caching. API request parsing and authoritative
business validation run in Go; Postgres enforces RLS and persisted integrity.

### L3 — Pure domain (`src/shared/`)

No React. No Expo. No Supabase. No network. Standard library only.

| Module | Responsibility | Depends on |
| --- | --- | --- |
| `money/` | Minor-unit type, parse a user's typed amount, format for display, sum a list, `allocate()` for even splits. | nothing |
| `dates/` | Month ranges **in a user's IANA zone**, DST-safe boundaries, "is this date in this period". | nothing |
| `splits/` | Freeze participant shares at creation; validate that exact shares total the expense; the deterministic remainder rule. | `money/` |
| `validation/` | Client-side mirrors of server rules, **for UX only**. Never a substitute for L4. | `money/`, `dates/` |

The `splits/` module is the one place where the same rule exists in two
languages. See §7.

### L4 — Postgres (`supabase/`)

Postgres is the persisted-data trust boundary. It enforces tenant isolation,
database invariants, and aggregates. Go owns application workflows and
authoritative request validation.

| Module | Responsibility |
| --- | --- |
| `migrations/` | Ordered schema changes. One concern per file. |
| Constraints, RLS, and triggers | Enforce tenant isolation and persisted invariants such as exact split totals and period-manager consistency. |
| `functions/period_balances.sql` | Compute paid / owes / net per member. |
| `functions/expense_totals.sql` | Aggregates for reports and budgets. |
| `period write policies and consistency trigger` | Enforce caller-scoped Go writes, fixed roster finalization, and manager consistency at commit. |
| `tests/rls_test.sql` | Cross-user access must fail. |
| `tests/golden_dataset_test.sql` | Hand-computed fixtures, exact expected values. |
| `seed.sql` | Starter categories. |
| `config.toml` | Local stack configuration. |

---

## 4. Contracts

Signatures are the agreed interface. Implementations belong to the owning layer.

### `src/shared/money`

```ts
// amount: integer minor units. currency: ISO-4217.
type Minor = { minor: number; currency: string }

parseAmount(input: string, currency: string): Minor | null   // accepts "24.50", rejects "24.505"
formatAmount(amount: Minor, locale?: string): string        // "24.50"
sumAmounts(amounts: Minor[], currency: string): Minor       // throws on mixed currency

// Even split with a deterministic remainder (BR-2).
allocate(amount: Minor, participantIds: string[]): Map<string, number>
```

`allocate()` is the mobile preview's only rounding implementation. Go validates
the submitted frozen shares and exact total; Postgres checks the persisted sum.
Whether Go should also compute equal-share rounding remains an open decision.

### `src/shared/dates`

```ts
monthRange(anchor: Date, timeZone: string): { from: LocalDate; to: LocalDate }
isWithinPeriod(date: LocalDate, start: LocalDate, end: LocalDate | null): boolean
```

`monthRange` is the answer to "which month is this in?" It takes an IANA zone,
never UTC, and returns an inclusive range of plain dates. Callers pass the range
to L4 — they never build a date filter themselves.

### `src/shared/splits`

```ts
// Which members share, and how much each owes.
freezeShares(amount: Minor, participants: string[]): Map<string, number>
validateExactShares(amount: Minor, shares: Map<string, number>): string | null
```

`freezeShares` is the even split. `validateExactShares` checks that manual
amounts are non-negative and total exactly the expense amount.

### `src/api` Go API client

```ts
createExpense(input: CreateExpenseInput): Promise<Result<string>>    // returns new id
expenseTotals(scope: Scope, range: DateRange): Promise<Report>
periodBalances(periodId: string): Promise<MemberBalance[]>
createPeriod(input: CreatePeriodInput): Promise<Result<string>>
transferManager(periodId: string, newManagerId: string): Promise<Result<void>>
deleteAccount(): Promise<Result<void>>
exportUsersData(range: DateRange): Promise<Backup>
```

The client talks only to HTTP endpoints implemented by Go, never database
functions. Every write returns a typed `Result`. Failures are never thrown past the hook,
never swallowed, and never represented as a silent empty list.

### Conventions

- **Query keys** are namespaced per feature: `['expenses', userId, range]`,
  `['period', id, 'balances']`. One key per distinct server response.
- **Mutations invalidate** the queries that depend on them. Creating an expense
  invalidates both the expense list and the period balances.
- **Nothing is mirrored.** After a write you invalidate; you do not poke the
  cache by hand to make it "look right".

---

## 5. Tracing the three core flows

A developer who can read these three traces understands the system.

### 5.1 Record a personal expense

```
app/expense/new                       # L0: collect nothing, render a screen
  → features/expenses/screens/NewExpenseScreen
      → shared/money.parseAmount      # L3: "24.50" → {minor: 2450} — instant, local
      → features/expenses/mutations.useCreateExpense
          → Go API POST /v1/expenses
              → Go validates caller, payload, ownership, currency, and date
              → caller-scoped transaction inserts expense + owner split
              → deferred Postgres trigger verifies the exact split total
          ← Result<id>
      → invalidate ['expenses', userId, month]
  → features/expenses/screens renders the updated month
```

**Notice what does not happen.** The screen never computes a total, never decides
whether the amount is valid, and never inserts a row. It types a number and
hands it down.

### 5.2 Record a shared expense with an unequal split

```
app/period/[id]/new
  → features/periods/.../NewSharedExpenseScreen
      → features/splits/useSplitPreview
          → shared/splits.freezeShares or validateExactShares   # L3, local, instant
          ⤷ screen shows each member's share before the user saves (FR-5.5)
      → features/expenses/mutations.useCreateExpense
          → Go API POST /v1/expenses
              · Go checks period membership, date range, currency, participants,
                positive shares, and exact split sum
              · caller-scoped transaction inserts expense + frozen split rows
              · deferred Postgres trigger independently enforces exact total
  → invalidate expenses for the period AND period balances
```

The preview runs locally so typing feels instant. **The server does not trust
it.** Go re-derives and validates everything, and Postgres independently
rejects a persisted split set whose sum does not match the expense.

### 5.3 Show period balances

```
app/period/[id]/balances
  → features/periods/screens/BalancesScreen
      → features/periods/queries usePeriodBalances
          → Go API GET /v1/periods/{id}/balances
              → Postgres period_balances aggregate
                    paid  = Σ amount_minor where paid_by = m, not deleted
                    owes  = Σ split.amount_minor where split.user_id = m, not deleted
                    net   = paid − owes
      → features/periods/components/BalanceRow renders
```

**There is no addition in this flow.** No screen sums anything, no hook
aggregates anything. The SQL returns final numbers, and the phone renders them.

---

## 6. Cross-cutting concerns

### Authentication

`src/features/accounts/` owns the session and exposes `useSession`. The auth
adapter is the only place that reads or writes the Supabase session. Screens
ask a hook, never a store.

### ⚠️ Clearing state on sign-out — a privacy bug waiting to happen

When a user signs out, or switches accounts, the **TanStack Query cache must be
cleared in full**. If it isn't, a cached `['expenses', userId, range]` entry and
its rendered screen survive into the next session, and the next user sees the
previous user's financial data for as long as the cache entry lives.

This is the one failure mode in the app that leaks another person's data without
RLS failing at all — RLS blocks the *database* read, but the cache is outside it.

**Required behaviour:** sign-out clears the query cache, resets all feature
stores, and navigates through a full unmount of the authenticated tree. Same for
a 401 or session-expiry response from any API request.

> **Recommendation:** promote this to a hard rule in `AGENTS.md` and add the
> matching case to the RLS/integration suite. It is a missing precondition, not
> an implementation detail.

### Offline and degraded reads

| State | Reads | Writes |
| --- | --- | --- |
| Online | Normal. | Normal. |
| Offline | Serve cache, banner "showing data from *time*". | Fail with a clear message. Never queue. |

There is no local database in v1. The cache is the only offline artifact, and
it is allowed to be stale as long as the app *says* it is stale.

### Error surfacing

| Origin | Where it is handled | What the user sees |
| --- | --- | --- |
| Split doesn't total the amount | L3, before submit | Inline message on the field. **The save button is disabled.** |
| Expense dated outside the period | Go API, on submit | Banner: "Pick a date inside the period." |
| Date moved by a timezone edge case | Go API, on submit | Banner with the accepted range, not a crash. |
| No connection | Hook, before the call | "You're offline — reconnect to save." |
| Not authorised | Go API or Postgres RLS | Treated as "not found": never reveal that a record exists. |

**RLS denials must be indistinguishable from "does not exist."** That is
deliberate — the alternative tells an attacker which IDs are real.

---

## 7. Where invariants are enforced and tested

The most valuable table in this document. When a developer asks *"where does
this rule live?"*, this is the answer.

| Invariant | Enforced in | Tested by |
| --- | --- | --- |
| Amounts are integer minor units | `shared/money` types + schema columns | Unit: money suite |
| No negative amounts | Schema `CHECK` + Go request validation | pgTAP + Go tests |
| Dates are plain `date`, no timezone | Schema | Golden dataset |
| Month boundaries never in UTC | `shared/dates` + callers passing `(from, to)` | Unit: DST case in dates suite |
| Split shares sum to the expense | Go validation + deferred Postgres integrity trigger | pgTAP + Go tests |
| Splits frozen at creation | Schema: computed columns, stored values | Golden dataset (roster change) |
| Participants must be period members | Go workflow + RLS boundary | Go tests + pgTAP |
| Expense date inside the period range | Go workflow | Go tests |
| Deterministic rounding remainder | `shared/splits` preview; Go accepts validated frozen shares | Canonical fixture in Jest and Go validation tests |
| Exactly one matching manager per live period | Partial unique index + deferred consistency trigger | pgTAP |
| The last manager cannot leave | Go account-deletion workflow + database integrity | pgTAP |
| No user can see another's data | RLS on every table | RLS suite (outsider reads zero rows) |
| Balances are computed, never stored | `period_balances` only | Golden dataset (edit, delete, re-add) |
| Personal and shared money never merge | Report/budget scopes | Golden dataset |
| Budgets are advisory, never blocking | No write path calls a budget check | Unit |
| Cache cleared on sign-out | `accounts` | *⚠️ To be added — see §6* |

---

## 8. The split-rounding duplication (open decision)

The mobile preview applies the deterministic remainder rule. Go currently
validates the submitted frozen shares and exact total, but does not recompute
that rule. Decide whether Go should also calculate equal shares or keep the
rounding implementation in the mobile preview only.

This is still undecided — see `AGENTS.md`, Open decisions. The module view
assumes option A:

**A. Keep both, with a shared conformance fixture.**
`shared/splits/freezeShares` handles previews and Go computes the same values;
one JSON fixture drives assertions in Jest and Go tests, so drift fails CI.

**B. Go only.** Remove the TypeScript rounding copy and preview shares from the
Go API. The editor would need a loading state and a server request to preview.

> Modules affected by reversing this: `shared/splits/`, `features/splits/`,
> and one entry in the ENFORCED column of §7.

---

## 9. Anti-patterns

Named, concrete, with the reason. If a code review sees one of these, it is not
a matter of taste.

1. **Summing in a hook.** `const total = expenses.reduce((a, e) => a + e.amount, 0)`
   in a screen or hook. The database already knows this number, knows the scope
   rules, and applies the same `deleted_at` filter. Recomputing it here creates a
   second, subtly different answer.
2. **A balance table that is updated on write.** It drifts, permanently, the
   first time a transaction rolls back. Balances are computed.
3. **Importing `@supabase/supabase-js` in a component.** It breaches R3, and the
   service-role key becomes one careless import away from shipping.
4. **`import { X } from '../../features/periods/queries'`.** Breaches R5. Two
   features are now coupled at the file level, and renaming an internal breaks
   the other one.
5. **Client-side validation as the only validation.** `validateExactShares` is
   a courtesy to the user. The server re-runs it identically and rejects a
   mismatch.
6. **Filtering `deleted_at` in some screens and not others.** Deleted expenses
   reappear in whichever screen forgot, and they reappear in the totals. Filter
   in one place, and prefer a view that has already filtered them.
7. **A `useEffect` that populates form state from a query.** Mirroring server
   data into local state is the duplicated-cache bug. Read from the query.
8. **An "amount typed by the user goes straight to the screen" number.** Every
   amount crossing a boundary is `{minor, currency}`, not a bare number.
9. **`moment` / `date-fns-tz` / a hand-rolled offset** installed to fix one date
   bug. Month ranges come from the `timeZone` database column, via `monthRange`.
10. **Hand-editing `src/api/types.ts` to make an error go away.** It is generated.
    Fix the schema, regenerate, commit.

---

## 10. Implementation order

A developer starting cold should build in this order. Each step is shippable and
testable at the boundary.

1. **L4 first.** Schema, RLS policies, integrity triggers, `period_balances`,
   and `expense_totals`. Implement expense and period workflows in Go. Write `rls_test.sql` and the golden
   dataset *before* any screen exists.
2. **L3.** `money`, `dates`, `splits` with unit tests. Pure and fast, no
   scaffolding required.
3. **L2.** Generate API types from OpenAPI and implement the typed Go API client.
4. **`accounts` feature**, so there is something to log into.
5. **`categories` + seeding**, so an expense can be recorded.
6. **`expenses`** — the core loop.
7. **`periods` + `splits`** — the shared case.
8. **`reports` + `budgets`** — once real numbers exist to report.
9. **`export`** — portability, once the data model is stable.
10. **`onboarding`** — last, because it is the only part that can look finished
    while hiding an unfinished app.

Writing L4 before L0 is deliberate: the RLS suite and golden dataset are what
prove the app is safe, and both are impossible to write against a UI that does
not exist yet.
