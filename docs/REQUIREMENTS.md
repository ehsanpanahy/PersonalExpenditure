# PersonalExpendiure — Product Requirements (v1)

Status: draft, agreed during design interview.
Last updated: 2026-10-14

Companion to [`ARCHITECTURE.md`](./ARCHITECTURE.md), which covers *how* this is
built. This document covers *what* it does and *why*. If the two ever disagree,
this document is the source of truth for intent and the architecture document is
the source of truth for mechanism.

---

## 1. Purpose

Personal expense tracking that is useful on its own, and genuinely better when
you share costs with other people.

Most expense apps treat sharing as an afterthought, and most split apps treat
individual tracking as an afterthought. This app does both, but the shared case
is the one that earns the product its value: a group of people recording costs
into one place and seeing who owes whom.

### 1.1 Who it is for

Anyone with a phone who wants to know where their money goes, and who
occasionally shares costs — a trip, a term in a shared flat, a group of friends
splitting a holiday.

The app is public and multi-tenant: strangers sign up, each with isolated data.

### 1.2 Product principles

1. **Correctness over convenience on money.** A wrong number is worse than a
   missing feature. Never estimate, never round silently, never recompute
   history.
2. **One number, one meaning.** Every figure shown has exactly one definition,
   stated where it is shown.
3. **History is immutable.** What you owed last month was what you owed last
   month. People joining or leaving, or features changing, must not rewrite the
   past.
4. **Small v1.** Anything not needed to make the core loop good is deferred, and
   deferred items are chosen so they can be added later without disturbing
   existing data.

---

## 2. Roles

| Role | Description | Capabilities |
| --- | --- | --- |
| **Member** (the default) | A signed-up person. | Everything personal; create expenses in periods they belong to. |
| **Manager** | A member of exactly one or more periods, marked as such. | Everything a member can do, **plus** edit or delete any expense in their periods, and transfer the manager role. The roster is fixed in v1 (FR-4.4), so “manage the roster” is not a v1 capability. |
| **Outsider** | Any other signed-up user. | No visibility into your data at all. |

A person is a Member in one context and a Manager in another. There is no
"admin" role in v1.

---

## 3. Core user journeys

### 3.1 Sign up and first run

1. Sign up with email and password.
2. Verify the email.
3. **One onboarding screen:** choose a base currency, confirm the starter
   category list. The IANA time zone is taken from the device (used for "today"
   and month bounds) and can be changed later in settings.
4. Land on an empty "this month" view that is clearly empty rather than
   broken.

The app never creates fake demo data. A screen full of invented expenses trains
the user on data that isn't theirs.

### 3.2 Record a personal expense

1. Tap add. Amount, date (defaults to today), category, optional note.
2. Save. The month total and category breakdown update immediately.
3. If a budget is now exceeded, the app shows a warning. **It never blocks the
   save.**

### 3.3 Create a shared period

1. Name it and give it a date range (a single day counts; an end date may be
   left blank for something ongoing, like a shared flat).
2. Add the members from existing accounts and pick one as manager.
3. Everyone in the period can now record costs into it.

### 3.4 Record a shared cost

1. Inside the period, tap add. Amount, date, who paid, optional note.
   Shared expenses are uncategorised in v1 — categories are private to each
   user (FR-2.5), so a period cannot show another person's category list.
2. Choose who it is split between — everyone by default, with the option to
   exclude specific people or type an exact amount per person.
3. The app shows each person's share before saving, and the shares must add up
   to exactly the total.

### 3.5 Settle up (informational only)

1. Open the period's balances screen.
2. See, per member, how much they paid, how much they owe, and the net.
3. A positive net means the rest of the group owes them.

v1 does **not** suggest who should pay whom to settle up in the fewest
transactions, and does **not** record repayments.

---

## 4. Functional requirements

### 4.1 Accounts

| ID | Requirement |
| --- | --- |
| FR-1.1 | A visitor can create an account with an email address and password. |
| FR-1.2 | The system sends a verification email; unverified accounts cannot be used. |
| FR-1.3 | A user can request a password reset by email and set a new password. |
| FR-1.4 | A user can sign in and sign out, and the session survives an app restart. |
| FR-1.5 | The app stores only an email address and an optional display name. No phone number, no address, no payment details. |
| FR-1.6 | A user can permanently delete their own account from within the app, without contacting support. |
| FR-1.7 | Deleting an account removes all of that user's personal data. It removes them from periods they belong to but **never** deletes those periods or anyone else's expenses, splits, or budgets in them. Shared rows that named the deleted user keep a non-identifying tombstone (no email, no display name). |
| FR-1.8 | A user who is the manager of a period cannot delete their account until they transfer the role or delete that period. This is how FR-4.7 (a period always has a manager) survives FR-1.7. |

> **Operational note:** email delivery must use a real transactional email
> provider from the first release. A rate-limited free tier will break signup
> and password reset in a way that looks, to the user, like a broken app.

### 4.2 Categories

| ID | Requirement |
| --- | --- |
| FR-2.1 | Every new account is seeded with a starter set of flat categories. |
| FR-2.2 | A user can rename, recolour, re-order and archive their categories. |
| FR-2.3 | A category used by past expenses can be archived but is never deleted, so history stays readable. |
| FR-2.4 | Categories are strictly one level deep in v1. There are no sub-categories. |
| FR-2.5 | Categories are private to their owner. No user sees another's categories. |

### 4.3 Personal expenses

| ID | Requirement |
| --- | --- |
| FR-3.1 | A user can record an expense with an amount, a date, a category and an optional note. |
| FR-3.2 | The date defaults to today in the user's IANA time zone and can be any past date. Future dates are rejected. The same "not in the future" rule applies to shared expenses. |
| FR-3.3 | Amounts must be greater than zero. The app does not accept negative amounts in v1. |
| FR-3.4 | A user can edit or delete any of their own personal expenses, at any time. Personal edits may change amount, date, category, and note. Changing the amount updates the single owner split in the same save so the total still matches. |
| FR-3.5 | Deleted expenses are hidden everywhere but remain recoverable by the user for a period of time. Soft-deleted means hidden, not erased. Only account deletion (FR-1.7) truly erases personal data. |
| FR-3.6 | A user can browse their expenses by month and by category, and search their notes by text. |

### 4.4 Periods

| ID | Requirement |
| --- | --- |
| FR-4.1 | A user can create a period with a name, a start date, and an optional end date. A blank end date means the period is ongoing. |
| FR-4.2 | The end date must not be earlier than the start date. |
| FR-4.3 | Creating a period requires assigning at least one member and exactly one manager. The creator is always a member. |
| FR-4.4 | The member list is fixed when the period is created. In v1, members cannot be added, removed, or invited later. |
| FR-4.5 | A period's expenses must have a date within the period's range. An expense dated outside the range is rejected with a clear message — never silently moved. |
| FR-4.6 | Every period has exactly one manager, and that manager can transfer the role to another member. |
| FR-4.7 | A period always has a manager. The last manager cannot leave or be removed, including by deleting their account (FR-1.8). |
| FR-4.8 | The manager can delete a period. Deleting a period hides it and its expenses and budgets from every member (soft-delete). It does not hard-erase rows. Those rows stay out of every total. |
| FR-4.9 | Members of a period can see every expense in it, including who paid and who it was split between. |
| FR-4.10 | A user who is not a member of a period has no access to it — not its name, not its existence, not its totals. |
| FR-4.11 | A period has a single currency, chosen when it is created. |

### 4.5 Splitting a shared cost

| ID | Requirement |
| --- | --- |
| FR-5.1 | Every expense in a period states who paid, and which members it is shared between. |
| FR-5.2 | By default the cost is shared equally between all members of the period. |
| FR-5.3 | The author can exclude specific members from an expense. |
| FR-5.4 | The author can instead assign an exact amount to each member, for uneven costs. |
| FR-5.5 | The app shows each member's share before the expense is saved. |
| FR-5.6 | The shares must add up to **exactly** the expense total. The app must not accept a total that is short or over, and must not adjust anyone's share silently. |
| FR-5.7 | No share may be negative. |
| FR-5.8 | **Who shares in an expense is fixed when it is created.** If the roster later changed, past expenses would not change with it. (v1 has a fixed roster; this rule exists so that adding join/leave later is safe.) |
| FR-5.9 | The author chooses the split; no other member is asked to confirm it. |
| FR-5.10 | After a shared expense is saved, its amount and splits cannot be edited. To change the split, the author or manager soft-deletes it and records a new expense. Date (still inside the period, not in the future) and note may be edited. |

### 4.6 Balances

| ID | Requirement |
| --- | --- |
| FR-6.1 | A period shows, for each member: the total they paid, the total they owe, and the net. |
| FR-6.2 | A positive net means the group owes that person money. A negative net means that person owes the group. |
| FR-6.3 | Balances are always recalculated from the expenses that produced them, never stored as a running total. |
| FR-6.4 | The period shows its gross total spend regardless of who paid. |
| FR-6.5 | v1 does not compute a minimum-transfer settlement plan and does not record repayments. |

### 4.7 Budgets

| ID | Requirement |
| --- | --- |
| FR-7.1 | A budget is an amount, for a calendar month, targeting either the user or a period. |
| FR-7.2 | A budget targets one scope only: either a personal scope or a specific period. The two are never mixed. |
| FR-7.3 | A **personal** budget applies either to a single category or to the whole personal scope. A **period** budget in v1 is whole-scope only — shared expenses have no category (FR-8.4). |
| FR-7.4 | **A personal budget counts only personal expenses.** It never counts a user's share of a shared cost. |
| FR-7.5 | **A period budget counts that period's gross spend**, regardless of who paid. |
| FR-7.6 | Budgets run from the first to the last day of a calendar month. |
| FR-7.7 | Budgets are **advisory**. Exceeding a budget produces a warning and never prevents recording an expense. |
| FR-7.8 | The app shows remaining amount and a clear over/under state. |

### 4.8 Reports

| ID | Requirement |
| --- | --- |
| FR-8.1 | The personal view shows this month's total spend, broken down by category. |
| FR-8.2 | The personal view shows spend per day for the month. |
| FR-8.3 | The personal total counts **only personal expenses**. A user's share of shared costs is shown separately and is never folded into the personal total. |
| FR-8.4 | The period view shows gross total spend. In v1 there is no category breakdown for a period: shared expenses are uncategorised so that no member has to read another member's private categories (FR-2.5). |
| FR-8.5 | Reports respect budget progress bars for the relevant scope. |
| FR-8.6 | Every reported figure excludes deleted expenses. |
| FR-8.7 | In v1, reports are monthly. Choosing a custom date range or comparing months is deferred. |

### 4.9 Import and export

| ID | Requirement |
| --- | --- |
| FR-9.1 | A user can export their transactions to CSV for a chosen date range, for use in a spreadsheet. |
| FR-9.2 | A user can export a complete backup, including periods, members, expenses, splits, budgets and categories. |
| FR-9.3 | A full backup carries a version marker, so a future version of the app knows how to read it. |
| FR-9.4 | Re-importing a backup is not supported in v1. The export is a portability and safety net, not a sync mechanism. |
| FR-9.5 | Importing bank statements is not supported in v1. |

### 4.10 Connectivity

| ID | Requirement |
| --- | --- |
| FR-10.1 | The app opens instantly using previously loaded data, rather than showing a loading spinner. |
| FR-10.2 | When the device is offline, the app shows a clear notice that the data on screen may be out of date, and says how old it is. |
| FR-10.3 | Recording or editing anything requires a connection, and fails with a clear message when offline. |

---

## 5. Business rules

These are the rules where a wrong choice would be expensive to undo.

**BR-1 — Money is never estimated or fractional.** All amounts are whole minor
units (e.g. cents). No calculation anywhere uses a decimal fraction, so no
cent-level drift can accumulate.

**BR-2 — Split division rounds deterministically.** When a total does not divide
evenly, everyone receives the rounded-down share and the leftover minor units are
handed out one each in a fixed, repeatable order. A $10.00 cost split three ways
is always $3.34 / $3.33 / $3.33 — never a different arrangement on a different
device, and never dependent on which person happens to appear first.

**BR-3 — Shares are frozen at creation.** An expense's shares are recorded as
final amounts, not as an instruction to divide later. Adding a new split type in
a future version therefore cannot change what past months meant.

**BR-4 — History is immutable.** Nothing that happens to a roster, a period, or
a feature changes the meaning of a past balance.

**BR-5 — Period dates are authoritative for period expenses.** An expense dated
outside its period is rejected, not moved.

**BR-6 — Personal and shared money are never merged.** "My spending this month"
answers a question about me and only counts my personal spending. This is the
single most common source of "the app says I spent $12,000" complaints.

**BR-7 — Deleted means hidden, not erased.** Deleted expenses vanish from every
figure but remain recoverable. Only account deletion truly erases data.

**BR-8 — One account can never see another's data.** Not even by guessing an
identifier, and not even for a period they were once in.

**BR-9 — The manager exists to fix mistakes, not to gatekeep.** Members can
soft-delete their own expenses immediately, and can edit date and note on a
shared expense they authored. Amount and splits on a shared expense are not
edited in place (FR-5.10). The manager's extra power is for other people's
errors, and for keeping the period healthy.

**BR-10 — The server is the judge.** Split totals, period membership, date
ranges, and permissions are all verified before anything is saved. Correctness
never depends on the device behaving well.

---

## 6. Non-functional requirements

| ID | Requirement |
| --- | --- |
| NFR-1 | The app works on both Android and iOS from one codebase. |
| NFR-2 | Android is the first release; iOS follows. |
| NFR-3 | The app opens and is usable in under two seconds on a typical phone. |
| NFR-4 | Fixes that do not require a new native capability can ship without waiting for store review. |
| NFR-5 | No user's data is visible to any other user under any circumstance, including a mistake in the app's own code. |
| NFR-6 | The app holds no payment card details and requests no banking credentials, ever. |
| NFR-7 | Financial calculations are covered by automated tests with known-good expected values, not merely "no errors". |
| NFR-8 | Every database change ships as a versioned migration that is verified in continuous integration before release. |

---

## 7. Out of scope for v1

Anything here is a deliberate deferral, not an oversight. Several are designed
to be added later **without changing the meaning of existing data.**

**Platform**
- Web and desktop apps. *(v1 is mobile only.)*

**Money**
- Multiple currencies in one account, and currency conversion. *(An account has
  one currency; a period has one currency. Adding conversion later means new
  records carry more information, not that old ones change.)*
- Refunds and negative amounts.
- Transfers between a user's own accounts. In v1, every record is a spend.
- Budget cycles that do not start on the first of the month.

**Expenses**
- Receipt photos.
- Recurring or scheduled expenses. *(Adding one later needs a "has this already
  run?" question answered, which is a new subsystem, not a new field.)*
- Splitting by percentage, by weights such as "adults pay double", or by ratio.
  *(Possible later as a new way to enter amounts; stored shares do not change.)*
- Changing a shared expense's amount or splits after it is saved. *(Delete
  and recreate instead; FR-5.10.)*
- Categories on shared expenses, and period budgets scoped to a category.

**Periods**
- Inviting people, joining a period, or leaving one. *(The roster is fixed at
  creation. This is what keeps the app free of pending invitations, accept/decline
  flows, and the awkward case of a member leaving mid-trip.)*
- Settling up in the fewest possible transfers.
- Recording that one member has paid another back.
- Reusing a period as a template for the next trip.

**Notifications**
- Push notifications, including over-budget warnings and period invitations.

**Platform services**
- Sign-in with Google, Apple, or a passwordless magic link.
- In-app payments or a subscription.

**Sync**
- Recording expenses with no connection. *(Reads work offline from a cache;
  writes need a connection.)*
- Resolving two people editing the same expense at the same moment. The last save
  wins, silently.

**Categories**
- Sub-categories. *(Existing categories can be grouped under a heading later
  without touching any expense.)*

**Infrastructure**
- Automated backups and a disaster-recovery runbook.
- An admin dashboard for support.

---

## 8. Definition of done for v1

v1 is complete when all of the following are true.

1. A new user can sign up, verify their email, choose a currency, and record
   their first expense without help.
2. A user can record, edit, delete, browse and search their personal expenses,
   and see correct monthly totals and category breakdowns.
3. A user can create a period with several members, name a manager, and transfer
   that role.
4. Two people can each record shared costs in a period and both see the same
   correct balances, including an uneven split and a cost that does not divide
   evenly.
5. Personal budgets and period budgets both show correct progress and warn
   without blocking.
6. A user can export their data as CSV and as a full backup.
7. **A user cannot see another user's expenses, categories, budgets or
   periods — verified by automated tests, not by inspection.**
8. **All financial calculations are covered by tests with known-good expected
   values**, including the awkward cases: an amount that does not divide evenly,
   a one-cent expense, an expense that excludes a member, and a deleted expense.
9. An Android build is installable and passes the full journey. iOS uses the
   same codebase (NFR-1) and follows Android (NFR-2); it is not a v1 gate.

---

## 9. Open questions

These were not settled during design and need a decision before the relevant
part is built.

1. **Refunds.** v1 has no way to record money coming back. A refund larger than
   the original purchase is currently unrepresentable. Options: allow negative
   amounts, add an expense "kind", or treat refunds as their own type.
2. **Members who leave a period.** With shares frozen at creation, removing
   someone leaves their balance frozen and uncollectable. Should removal be
   allowed at all, and who inherits the debt?
3. **Period currency across borders.** Members may live in different countries.
   Should a balance be shown only in the period's currency, or also annotated in
   each member's local currency?
4. **The name "Period".** It reads as a span of time, but it is actually a group
   of people — and its dates are optional. `Circle`, `Trip`, or `Household` may
   describe it better. Worth deciding before it becomes permanent.
5. **How long deleted expenses are kept**, and whether they are eventually purged
   permanently.
6. **Crash reporting and analytics.** Currently none planned. If added, decide
   what is collected and what consent that requires.
7. **Sign-up abuse.** Rate limiting, bot prevention, and whether email
   verification can be bypassed for testing.
8. **The final starter category list.** Should include something for money
   coming in, so users are not forced to record fake spending.

Resolved in the 2026-10-14 alignment pass (see also `ARCHITECTURE.md` §9):
shared expenses are uncategorised; period budgets are whole-scope; shared
amount/splits are immutable after create; account deletion tombstones the
profile and refuses deletion while the user is still a period manager;
unverified accounts cannot be used; v1 tests do not include mid-period joins;
Android is the v1 installable gate.

---

## 10. Glossary

| Term | Meaning |
| --- | --- |
| **Period** | A named, time-bounded group of people who share costs together. A trip, a term in a shared flat. Has a start date and an optional end date. |
| **Manager** | The single member of a period responsible for it. Can edit and delete anyone's expense in that period, and transfer the role. |
| **Member** | A person in a period who can record costs into it. |
| **Share** | The exact amount one person owes from one shared expense. |
| **Paid** | The total a person fronted with their own money. |
| **Net** | Paid minus owed. Positive means the group owes them. |
| **Personal expense** | An expense belonging to one person only. Never involves a period. |
| **Shared expense** | An expense inside a period, split between members. |
| **Scope** | What a budget or report is about: either one person or one period. |
| **Base currency** | The single currency a person records in. |
| **Occurred on** | The date money actually changed hands — as opposed to the date the record was created. |
| **Advisory budget** | A budget that warns but never blocks an expense from being saved. |
