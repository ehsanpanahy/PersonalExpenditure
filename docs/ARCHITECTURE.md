# PersonalExpendiure — Architecture (v1)

Status: draft, v1 scope agreed through design interview.
Last updated: 2026-10-12

---

## 1. Overview

A multi-tenant expense tracking app. Each person tracks their own expenses, and
groups of friends share costs inside a **Period** — a named, time-bounded shared
space (a trip, a semester, a term in a shared flat).

Two things define the product:

1. **Personal tracking** — expenses, flat categories, monthly budgets, monthly
   reports.
2. **Shared periods** — several people record costs into the same period; the
   app works out who owes whom.

### Core principles

These are the rules the rest of the document follows from. If a change violates
one of these, the change needs an explicit decision.

- **Postgres is the single source of truth.** All balance, budget, and report
  math lives in SQL, not in TypeScript. The phone is a cache and a renderer.
- **Tenant isolation is enforced in the database**, via Row Level Security, not
  in application code.
- **Money is never a float.** Every amount is an integer count of minor units
  (cents) paired with an ISO-4217 currency code.
- **Splits are stored as computed results, not as rules.** A share amount is
  frozen at creation time, so history never changes meaning.
- **Soft deletes everywhere.** Nothing is hard-deleted except by explicit
  account deletion.
- **Validate on the server, always.** The client is not trusted, including the
  client's own app.

---

## 2. v1 scope

### 2.1 Accounts and access

- Email + password signup, email verification, password reset, logout.
- Sessions: Supabase JWT + refresh tokens in device secure storage.
- Onboarding: a single screen asking for base currency, then confirming starter
  categories. No demo data, no auto-created periods.
- Minimal PII: `id`, `email`, optional `display_name`. Nothing else.
- Self-service account deletion with full cascade delete of the user's own data.

### 2.2 Personal expenses

- Create / edit / soft-delete an expense: amount, date, category, optional note.
- Flat categories per user (seeded, editable, reorderable, archivable).
- Browse and filter: by month, by category, text search on note.
- Monthly budgets, scoped to the user or to a period.
- Reports: month total, breakdown by category, spend per day, budget progress.

### 2.3 Periods (shared)

- A Period has a name, a manager, a start date, and an optional end date
  (`NULL` = ongoing).
- **Fixed roster.** Members are added when the Period is created. There is no
  invite, join, or accept flow in v1.
- Any member can create an expense in the Period.
- Author chooses how each expense is divided: equal among everyone by default,
  or exclude specific members, or type exact amounts per person.
- Balances screen: for each member, total paid, total owed, net.
- Permission model:

| Action | Member, own expense | Member, another's expense | Manager |
| --- | --- | --- | --- |
| Create | yes | yes (into their period) | yes |
| Edit | yes | no | yes |
| Delete (soft) | yes | no | yes |

- Manager can transfer the role. The last manager cannot leave or be removed.

### 2.4 Import / export

- CSV export of transactions for a date range (human-readable).
- JSON full backup, versioned via `schema_version`, covering periods, members,
  expenses, splits, budgets, and categories as nested structures.

### 2.5 Offline behaviour

- Reads are served from the TanStack Query cache with a clear
  "offline — showing data from *time*" banner.
- Writes require a connection and fail loudly. No write queue, no local
  database in v1.

---

## 3. Out of scope (v1)

Kept as an explicit, maintained list. Many of these are additive later by design
— the notes say which.

**Platform**
- Web app, desktop app. (React Native + Expo only.)

**Money**
- Multi-currency conversion and FX rates. Users have one base currency; each
  Period has one currency. *(Additive: every amount already carries a currency
  code, so this is a new column on new records, not a migration.)*
- Refunds and negative amounts.
- Transfers between own accounts. Every record in v1 is an expense.
- Mid-month budget cycles. *(Additive: one nullable column.)*

**Expenses**
- Receipt photo capture and object storage.
- Recurring / scheduled expenses.
- Split types beyond equal and exact: percentage, "N shares", proportional.
  *(Additive: split amounts are already stored as computed values.)*
- Split edits after creation by non-authors.

**Periods**
- Invite / join / accept flows; members joining or leaving on their own.
- Settle-up minimisation ("who pays whom to zero out in fewest transactions").
- Recording repayments between members.
- Period templates.

**Platform services**
- Push notifications, over-budget alerts, period invitations.
- OAuth / social login, magic-link passwordless login.
- Analytics and telemetry (decision still open — see §9).
- In-app payments or subscriptions.

**Sync**
- Offline write queue. *(Additive: every expense carries a client-generated
  UUID, so replay can be made idempotent later.)*
- Conflict resolution beyond last-write-wins.

**Categories**
- Sub-categories / nested trees. *(Deferred in favour of a `category_groups`
  table that groups existing flat categories without touching history.)*

**Infrastructure**
- Multi-region, backups automation, disaster recovery runbook.
- Admin dashboards and support tooling.

---

## 4. Tech stack and why

| Layer | Choice | Why this and not the alternative |
| --- | --- | --- |
| Client | **React Native + Expo (TypeScript)** | Over Flutter: Over-the-Air updates. For a v1 that will ship bugs in money math, patching JS the same day without store review is worth more than pixel-identical rendering. |
| Navigation | **Expo Router** (file-based) | Default in new Expo apps; no navigation config file to drift out of sync. |
| Server state | **TanStack Query** | The query cache *is* the offline-read story. Over Redux: avoids hand-maintaining a normalised expense cache that duplicates the database. |
| Local UI state | **Context / Zustand (minimal)** | Holds only current month, dismissed onboarding, similar. Never server data. |
| Backend | **Supabase (managed Postgres + Auth)** | Over a custom Node backend: auth is 2–4 weeks of security-sensitive work you don't need to own. RLS enforces tenant isolation in the database, so a missing `WHERE user_id = ?` in app code cannot leak data. |
| SQL logic | **Postgres functions + views** | Balance and report math lives here so there is exactly one implementation across phone, edge functions, and future clients. |
| Email | **Resend or Postmark** | **Not** Supabase's built-in SMTP: it is rate-limited to roughly 2 emails/hour on the free tier, which breaks signup and password reset in ways users experience as "the app is broken". |
| Auth method | **Email + password** | Over OAuth: no custom URL scheme or associated-domain setup to debug in Expo. Apple's Sign-in-with-Apple requirement (App Store rule 4.8) is a further cost for no v1 benefit. |
| Testing | **Jest (jest-expo) + Supabase local stack + pgTAP** | Jest for TypeScript, including pure money/split logic. Local Supabase for real RLS enforcement. |
| CI/CD | **GitHub Actions + EAS Build + EAS Update** | EAS manages signing and store submission. Hosted builds are reversible — `expo prebuild` remains available. |
| Lint/format | **Biome, or ESLint + Prettier** | Either is fine; pick one and don't churn. |

### Release order

1. **Android first.** Sideloading via EAS is free and installs the same day.
2. iOS requires a paid Apple Developer account ($99/yr) plus App Store review
   (~1–3 days). The OTA channel is the only fast lane for fixes.

---

## 5. Data model

All timestamps are `timestamptz` in UTC. All dates are `date` (no time, no
zone). All amounts are `bigint` minor units.

### 5.1 Tables

#### `profiles`
One row per user, created by a trigger on `auth.users`.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | → `auth.users(id)` `ON DELETE CASCADE` |
| `email` | `text` | |
| `display_name` | `text` | nullable |
| `base_currency` | `char(3)` | ISO-4217. Every personal amount uses this. |
| `timezone` | `text` | IANA, e.g. `Europe/Berlin`. Needed for future "today" cutoffs and recurring due dates. |
| `created_at` / `updated_at` | `timestamptz` | |

#### `categories`
| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | |
| `user_id` | `uuid` | → `profiles(id)` `ON DELETE CASCADE` |
| `name` | `text` | unique per user, case-insensitive |
| `icon`, `color` | `text` | nullable |
| `sort_order` | `int` | |
| `is_archived` | `bool` | archived categories stay referenced by old expenses |

Indexes: unique `(user_id, lower(name))`; `(user_id, sort_order)`.

#### `periods`
| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | |
| `name` | `text` | |
| `manager_id` | `uuid` | → `profiles(id)` |
| `starts_on` | `date` | |
| `ends_on` | `date` | nullable — `NULL` means ongoing |
| `currency` | `char(3)` | single currency for the whole period |
| `created_at` / `updated_at` | `timestamptz` | |

Check: `ends_on IS NULL OR ends_on >= starts_on`.

#### `period_members`
| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | |
| `period_id` | `uuid` | → `periods(id)` `ON DELETE CASCADE` |
| `user_id` | `uuid` | → `profiles(id)` `ON DELETE CASCADE` |
| `role` | `enum('manager','member')` | |
| `joined_at` | `timestamptz` | |

Constraints:
- `UNIQUE (period_id, user_id)`
- Partial unique index on `(period_id)` **where `role = 'manager'`** — guarantees
  exactly one manager per period.
- The last manager cannot leave or be removed (enforced in the transfer/removal
  functions).

#### `expenses`
The central table. A row is either **personal** (`period_id IS NULL`) or
**shared** (`period_id IS NOT NULL`). Everything below works for both.

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | **client-generated** — enables idempotent create and an offline queue later |
| `user_id` | `uuid` | author / creator. Also the owner for personal expenses. |
| `period_id` | `uuid` | nullable |
| `category_id` | `uuid` | nullable — shared expenses may be uncategorised |
| `paid_by` | `uuid` | for personal rows this must equal `user_id` |
| `amount_minor` | `bigint` | `CHECK (amount_minor > 0)` |
| `currency` | `char(3)` | v1: equals the user's base currency, or the period's currency |
| `occurred_on` | `date` | accounting date, no time zone |
| `note` | `text` | nullable |
| `deleted_at` | `timestamptz` | nullable — soft delete |
| `created_at` / `updated_at` | `timestamptz` | `created_at` is never shown in the UI |

Check:
```sql
CHECK ((period_id IS NULL AND paid_by = user_id)
    OR (period_id IS NOT NULL AND paid_by IS NOT NULL))
```

Indexes: `(period_id, occurred_on DESC)` where `deleted_at IS NULL`;
`(user_id, occurred_on DESC)` where `period_id IS NULL AND deleted_at IS NULL`.

#### `expense_splits`
One row per participant. **The split result, not the split rule.**

| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | |
| `expense_id` | `uuid` | → `expenses(id)` `ON DELETE CASCADE` |
| `user_id` | `uuid` | participant |
| `amount_minor` | `bigint` | `CHECK (amount_minor >= 0)` |
| `created_at` | `timestamptz` | |

Constraints:
- `UNIQUE (expense_id, user_id)`
- **Invariant: `SUM(amount_minor) == expenses.amount_minor`** for every expense.
  Enforced in `create_expense()`; asserted in tests.

A personal expense has exactly **one** split row: its owner, for the full
amount. This unifies the balance query — there is no separate code path for
"personal" versus "shared".

#### `budgets`
| Column | Type | Notes |
| --- | --- | --- |
| `id` | `uuid` PK | |
| `scope_type` | `enum('user','period')` | |
| `user_id` | `uuid` | nullable; set iff `scope_type = 'user'` |
| `period_id` | `uuid` | nullable; set iff `scope_type = 'period'` |
| `category_id` | `uuid` | nullable — `NULL` means a whole-scope budget |
| `amount_minor` | `bigint` | `CHECK (amount_minor > 0)` |
| `created_at` / `updated_at` | `timestamptz` | |

Check enforcing exactly one scope:
```sql
CHECK ((scope_type = 'user'   AND user_id IS NOT NULL AND period_id IS NULL)
    OR (scope_type = 'period' AND period_id IS NOT NULL AND user_id IS NULL))
```

Cycle: **calendar month**, 1st to last. Budgets are **soft warnings** — the app
never blocks recording an expense.

### 5.2 Derived logic (Postgres)

#### `create_expense(...) → uuid`
A single `SECURITY DEFINER`-free RPC that performs the whole operation in one
transaction. This is the app's main write path and the main trust boundary.

1. Authorise: the caller must be the `user_id`, and if `period_id` is set, the
   caller must be a member of that period.
2. Validate the date: if the expense is shared, `occurred_on` must fall within
   `[starts_on, ends_on]` of the period. **Reject, never clamp.**
3. Validate participants: every split `user_id` must be a member of the period
   (or the owner, for a personal expense).
4. Validate amounts: all non-negative, and `SUM(splits) == amount_minor`
   exactly. No tolerance.
5. Insert the expense, insert the splits.
6. Return the new id.

Because the id is client-generated, a repeat call with the same id is a no-op
rather than a duplicate.

#### `period_balances(period_id) → table`
Returns one row per member:

```
paid_minor  = SUM(expenses.amount_minor)  WHERE period = P AND paid_by = m AND not deleted
owes_minor  = SUM(expense_splits.amount_minor) WHERE split.user_id = m AND expense in P AND not deleted
net_minor   = paid_minor - owes_minor      -- positive: m is owed money
```

Balances are **always computed**, never stored or incrementally maintained. A
maintained balance table drifts; a computed one cannot.

#### `expense_totals(scope, from, to)`
Report aggregates. Scope is `('user', user_id)` or `('period', period_id)`.
Month boundaries are computed **in the user's zone**, never in UTC, and are
passed as explicit `(from, to)` date ranges — never as a `'2026-03'` string.
Never a `'2026-03'` string.

### 5.3 Reporting semantics

These rules prevent the classic "why does October say I spent $12,000" bug.

| Report | Includes |
| --- | --- |
| **Personal total** | Only expenses with `period_id IS NULL` owned by me. Counted at **100%**. |
| **My share of shared costs** | Shown separately as a "shared" line, never folded into the personal total. |
| **Period total** | All non-deleted expenses in the period, at **gross** amount, regardless of payer. |
| **Period balances** | Gross up for the group, net per member (see §5.2). |

A **personal budget** counts only personal expenses. A **period budget** counts
gross period spend. My share of a shared dinner never counts against my personal
Food budget.

### 5.4 Rounding

Never divide naively. `allocate(amount_minor, n)`:

1. `base = floor(amount_minor / n)` for every participant.
2. Distribute the remaining `amount_minor - base * n` minor units, one each, in
   a **stable deterministic order** (participants sorted by `user_id`).

`1000` across 3 people → `334 / 333 / 333`, always in the same order. This is a
pure function, unit-tested, and used identically on client and server.

### 5.5 Row Level Security

Enabled on every table. The client never uses the service-role key.

| Table | select | insert | update | delete |
| --- | --- | --- | --- | --- |
| `profiles` | own row | — | own row | — |
| `categories` | own | own | own | own |
| `periods` | member of the period | caller becomes manager | manager | manager |
| `period_members` | member of the period | manager | manager | manager (not the last one) |
| `expenses` | own, **or** member of its period | author, plus period membership and date-range checks | author of own, or manager of its period | author of own, or manager (soft) |
| `expense_splits` | same as parent expense | via `create_expense()` only | with parent | with parent |
| `budgets` | own scope | own scope | own scope | own scope |

The expense policy in full:
```sql
USING (
  user_id = auth.uid()
  OR period_id IN (SELECT period_id FROM period_members WHERE user_id = auth.uid())
)
```

Triggers or RPCs must enforce membership and the date-range check on insert; RLS
alone cannot reference another table's rows inside `WITH CHECK` safely for this
case. `create_expense()` is the enforcement point.

---

## 6. Module and folder structure

A **single repository, single `package.json`** — no workspace toolchain in v1.
The separation that matters is folders, not packages, and adding `npm
workspaces` later (when a web app exists) is straightforward.

```
PersonalExpendiure/
├─ app/                          # Expo Router routes (thin — no business logic)
│  ├─ _layout.tsx
│  ├─ (onboarding)/
│  ├─ (tabs)/
│  │  ├─ index.tsx               # this month
│  │  ├─ reports.tsx
│  │  └─ settings.tsx
│  ├─ expense/[id].tsx
│  └─ period/[id]/
│     ├─ index.tsx               # period expenses
│     ├─ balances.tsx
│     └─ budget.tsx
│
├─ src/
│  ├─ db/
│  │  ├─ client.ts               # Supabase client + secure session persistence
│  │  └─ types.ts                # generated from the DB schema — never hand-written
│  ├─ features/
│  │  ├─ expenses/               # screens, hooks, components — one folder per feature
│  │  ├─ periods/
│  │  ├─ budgets/
│  │  ├─ reports/
│  │  ├─ categories/
│  │  └─ onboarding/
│  ├─ shared/                    # pure TypeScript, no React, no Supabase imports
│  │  ├─ money/                  # minor-unit parse/format, allocate(), rounding
│  │  ├─ dates/                  # month ranges in a user time zone
│  │  └─ validation/             # client-side mirrors of server rules, for UX only
│  ├─ ui/                        # design system: buttons, inputs, bars, charts
│  └─ lib/                       # query keys, constants, result types
│
├─ supabase/
│  ├─ migrations/                # ordered SQL, one concern per file
│  ├─ functions/                 # create_expense, period_balances, totals…
│  ├─ tests/
│  │  ├─ rls_test.sql            # pgTAP — must fail on cross-tenant access
│  │  └─ golden_dataset_test.sql # pinned expected balances
│  ├─ seed.sql                   # starter categories per test user
│  └─ config.toml
│
├─ e2e/                          # Maestro flows — optional, added after v1
├─ .github/workflows/            # ci.yml, migration-check.yml
├─ docs/ARCHITECTURE.md
└─ package.json
```

Rules:
- `app/` routes are thin. Logic lives in `src/features/*`; pure logic in
  `src/shared`.
- `src/shared` imports nothing from `react`, `expo`, or `@supabase/supabase-js`,
  which is what makes it trivially unit-testable.
- `src/db/types.ts` is **generated** from the live schema and never hand-edited.

---

## 7. Key decisions and trade-offs

| # | Decision | Alternative rejected | Consequence |
| --- | --- | --- | --- |
| 1 | Mobile-only v1 (RN + Expo) | Web-first PWA, desktop | Half the surface to build and ship. Costs: no link sharing, no desktop reporting. |
| 2 | Expo + RN, not Flutter | Flutter | Gave up cross-platform pixel consistency to get OTA updates — a day-one fix without a store review. |
| 3 | Supabase + RLS over a custom backend | Node/Fastify + hand-rolled auth | Tenant isolation is a database invariant, so an app-code bug cannot leak data. Cost: RLS learning curve, vendor lock-in, and service-role keys that must stay server-only. |
| 4 | Client-generated UUIDs on expense create | Server-generated ids | Enables idempotent create now and an offline write queue later without a data migration. |
| 5 | One currency per user and per period | Multi-currency with FX | All totals are trivially correct. The currency column already exists, so this is additive later. |
| 6 | Integer minor units | `float` / `numeric` in JS | No cent-level drift, ever. Cost: every amount crosses the JS boundary as a number that must be formatted explicitly. |
| 7 | Freeze computed split amounts | Store the split rule (equal/percent/…) | Historical balances never change meaning, and new split types become a UI-only change. Cost: you cannot re-derive a split after editing — acceptable, since split edits are not in v1 scope. |
| 8 | Equal + exact custom splits | Full Splitwise rule set | Validation is bounded (non-negative, must total exactly). Percentage and "N shares" are pure UI additions later. |
| 9 | Balance and report math in SQL | Compute in TypeScript on the client | One implementation for the phone, edge functions, and any future client. They cannot disagree. |
| 10 | Server-side `create_expense()` RPC | Client inserts plus RLS checks | All validation happens atomically in one transaction, in the trust boundary, with no partially-written expenses. |
| 11 | Soft delete everywhere | Hard delete | Preserves an audit trail and gives cached clients a tombstone, so deleted rows don't resurrect. Cost: every query must filter `deleted_at IS NULL`. |
| 12 | Last-write-wins, no conflict engine | Optimistic locking / CRDTs | Simplicity. Accepted risk: two simultaneous edits to the same expense silently lose one. Manager-gated deletion keeps the blast radius small. |
| 13 | Cached reads, online-only writes | Full offline-first sync | Avoids a sync protocol entirely. Accepted cost: you cannot record an expense with no signal. |
| 14 | Fixed period roster, no invites | Invite/accept lifecycle | Avoids invite tokens, pending states, "member left" handling, and the "we edited an expense someone already saw" class of bugs. Cost: the manager must know everyone's account up front. |
| 15 | Manager role with transfer | Manager as permanent creator | No single point of failure: a lost account can't freeze a period forever. |
| 16 | Split membership snapshotted at creation | Dynamic membership at query time | Someone joining or leaving never retroactively changes what people owe. |
| 17 | Flat categories | Nested tree | Simple schema and one-level reporting. Escape hatch is `category_groups`, which groups existing categories without migrating history. |
| 18 | Budgets are soft warnings | Hard blocks | Nobody abandons an app because a dinner wouldn't fit under a budget. |
| 19 | One source of truth for server data | Mirroring into a local store | TanStack Query owns all server state; local stores hold only UI state. Prevents the classic duplicated-cache divergence bug. |
| 20 | Single package, folders for structure | npm workspaces monorepo | Avoids module-resolution and test-runner setup cost with no current second app to share code with. |

---

## 8. Testing strategy

Confidence comes from SQL, not from the UI. The two things that can silently
destroy this product are **tenant isolation** and **balance arithmetic** — both
are database concerns.

### 8.1 Unit tests (Jest, `src/shared`)

Pure functions, no mocks, no database. Target: full coverage.

- `allocate()` — including `1000 / 3`, `1 / 3`, `0`, `n = 1`, and a large amount
  near `bigint` range. Assert **determinism**: the same input always yields the
  same per-person distribution in the same order.
- `parseAmount()` / `formatAmount()` — round-tripping, locale separators, and
  exactly two decimal places.
- Month range computation in a time zone, across a DST boundary.
- Date validation against a period's `[starts_on, ends_on]`, inclusive at both
  ends.

### 8.2 Integration tests (local Supabase + pgTAP)

Run against a real Postgres with RLS enabled, in CI and locally.

**Tenant isolation — the suite that must exist.** Seed three users: two in the
same period, one outside it. Then assert:

- A member can read the other member's expense in their shared period.
- The outsider reads **zero** rows from that period.
- A non-member cannot insert into a period they do not belong to.
- A non-manager cannot update or delete another member's expense.
- A member cannot read or write another user's categories or budgets.
- Direct table access (not just through the app's queries) is blocked — this is
  what proves RLS works rather than the app filtering politely.

**Golden dataset — the single most valuable test.** Seed a fixed, *hand-computed*
set of expenses and assert exact expected balances for every member. The seed
must include the awkward cases:

- $10.00 split three ways → `334 / 333 / 333`.
- A $0.01 expense in a period.
- A member who was added mid-period (owes nothing for earlier expenses).
- An expense with exact custom shares that total to the expense amount.
- A period with a manager who paid for everything.
- A soft-deleted expense (must contribute nothing).
- A period expense with no category.

Assert the exact integer values, not just that the query returned rows. Money
bugs produce wrong numbers, not exceptions.

### 8.3 What v1 skips

- End-to-end tests. Add one Maestro flow for
  *signup → create period → add shared expense → see balances* only after the
  rest is solid.

### 8.4 CI

On every pull request:
1. Lint and typecheck.
2. Unit tests.
3. Spin up an ephemeral Supabase branch, apply all migrations.
4. Run the RLS and golden-dataset suites against it.
5. Report.

Migrations are applied in CI, not just on developer machines. A migration that
has only ever run on one laptop will break production.

---

## 9. Open questions

1. **Refunds and negative amounts.** v1 requires `amount_minor > 0`. A refund
   larger than the original has no representation. Options: negative amounts, an
   `expense_kind` column, or model refunds as their own expense type.
2. **Removed period members.** With snapshotted splits, removing someone leaves
   their balance intact and uncollectable. Should the manager be able to remove
   a member, and what happens to their residual balance?
3. **Period currency vs. member currencies.** A Period has one currency, and
   members may live in different countries. Do we display a member's balance in
   the period currency only, or annotate with their local currency?
4. **"Period" as a product name.** It reads as a span of time, which conflicts
   with `starts_on`/`ends_on` being optional and with the period being a *group*.
   `Circle`, `Trip`, or `Household` may fit better. Worth deciding before the
   name hardens into the schema.
5. **Mid-month budget cycles.** Fixed at calendar months. If needed, add
   `cycle_start_day` — but it changes every aggregate query.
6. **Soft-deleted retention.** No expiry policy. Decide whether soft-deleted
   expenses are purged after N days.
7. **Analytics and crash reporting.** Currently none. If Sentry or PostHog is
   added, decide what the consent story is for a multi-tenant app.
8. **Signup abuse.** Rate limiting, email verification enforcement, and bot
   prevention for a public app.
9. **Starter category list.** Final set not agreed. Should include an
   income-ish or refund-ish category so users aren't forced to fake an expense.
10. **Category deletion vs. archival.** v1 archives. Whether a user may ever
    hard-delete a category still referenced by historical expenses is undecided.
