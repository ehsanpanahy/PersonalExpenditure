-- Golden dataset: hand-computed balances asserted as exact integers.
-- (ARCHITECTURE.md §8.2; AGENTS.md "Golden dataset test".)
--
-- Members: A (manager), B, C share period P1. O is the outsider.
-- SQL setup inserts a fixed ledger directly; Go tests cover request workflows.
--
-- P1 ledger (all USD, March 2026 — past relative to any CI run in 2026+):
--   E1  personal A   2500 . . . . . . . . . . ignored by period math
--   E2  A paid 1000, splits A334/B333/C333 ($10.00 three ways, BR-2)
--   E3  B paid    1, splits B1             (one-cent, single participant:
--                                          allocate(1,3)=[1,0,0] is preview-only;
--                                          the server requires shares > 0)
--   E4  A paid 1250, splits A500/B750      (exact custom, C excluded)
--   E5  C paid 2000, splits A1000/C1000     (soft-deleted → contributes nothing)
--   All shared expenses have category_id NULL (FR-8.4).
--
-- Expected P1 balances:
--   paid: A 2250 (=1000+1250), B 1, C 0
--   owes: A  834 (=334+500),  B 1084 (=333+1+750), C 333
--   net:  A 1416, B -1083, C -333        (sums to 0)
--   gross: 2251 (=1000+1+1250; excludes E1 personal + E5 deleted)
--
-- E2 uses the canonical 1000/3 values from src/shared/splits/conformance.json.
-- Money bugs produce wrong numbers, not exceptions: every figure below is an
-- exact integer assertion, never "returned rows".

BEGIN;
SELECT plan(15);

-- ---------------------------------------------------------------- setup ----

INSERT INTO auth.users (
    instance_id, id, aud, role, email, encrypted_password,
    email_confirmed_at, created_at, updated_at
) VALUES
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000011', 'authenticated', 'authenticated', 'golden-a@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000012', 'authenticated', 'authenticated', 'golden-b@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000013', 'authenticated', 'authenticated', 'golden-c@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000014', 'authenticated', 'authenticated', 'golden-o@example.test', '', now(), now(), now());

UPDATE public.profiles SET base_currency = 'USD', timezone = 'UTC'
WHERE auth_user_id IN (
    '10000000-0000-0000-0000-000000000011',
    '10000000-0000-0000-0000-000000000012',
    '10000000-0000-0000-0000-000000000013',
    '10000000-0000-0000-0000-000000000014'
);

INSERT INTO public.categories (id, user_id, name)
SELECT '20000000-0000-0000-0000-000000000011', id, 'Food'
FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000011';

INSERT INTO public.periods (id, name, manager_id, starts_on, ends_on, currency)
SELECT '30000000-0000-0000-0000-000000000001', 'Golden period', id, DATE '2026-01-01', NULL, 'USD'
FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000011';


INSERT INTO public.period_members (period_id, user_id, role)
SELECT '30000000-0000-0000-0000-000000000001', id,
       CASE WHEN auth_user_id = '10000000-0000-0000-0000-000000000011'
            THEN 'manager'::public.period_role ELSE 'member'::public.period_role END
FROM public.profiles
WHERE auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000013');


-- E1: personal expense of A (never part of period math).
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000001', p.id, NULL,
       '20000000-0000-0000-0000-000000000011', p.id, 2500, 'USD', DATE '2026-03-01', 'personal groceries'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000011';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000001', p.id, 2500
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000011';

-- E2: $10.00 split three ways.
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000002', p.id,
       '30000000-0000-0000-0000-000000000001', NULL, p.id, 1000, 'USD', DATE '2026-03-04', 'shared dinner'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000011';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000002', p.id,
       CASE p.auth_user_id WHEN '10000000-0000-0000-0000-000000000011' THEN 334 ELSE 333 END
FROM public.profiles p
WHERE p.auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000013');

-- E3: one-cent expense, single participant.
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000003', p.id,
       '30000000-0000-0000-0000-000000000001', NULL, p.id, 1, 'USD', DATE '2026-03-05', 'one cent'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000012';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000003', p.id, 1
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000012';

-- E4: exact custom split, C excluded (no row, not zero).
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000004', p.id,
       '30000000-0000-0000-0000-000000000001', NULL, p.id, 1250, 'USD', DATE '2026-03-06', 'uneven taxi'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000011';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000004', p.id,
       CASE p.auth_user_id WHEN '10000000-0000-0000-0000-000000000011' THEN 500 ELSE 750 END
FROM public.profiles p
WHERE p.auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000012');

-- E5: soft-deleted shared expense — contributes nothing.
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note, deleted_at)
SELECT '40000000-0000-0000-0000-000000000005', p.id,
       '30000000-0000-0000-0000-000000000001', NULL, p.id, 2000, 'USD', DATE '2026-03-07', 'deleted dinner', now()
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000013';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000005', p.id,
       CASE p.auth_user_id WHEN '10000000-0000-0000-0000-000000000011' THEN 1000 ELSE 1000 END
FROM public.profiles p
WHERE p.auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000013');

-- Stable ids for assertions below (temp tables bypass RLS after SET ROLE).
CREATE TEMPORARY TABLE golden_ids (key text PRIMARY KEY, id uuid NOT NULL);
INSERT INTO golden_ids (key, id)
SELECT 'a', id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000011';
INSERT INTO golden_ids (key, id)
SELECT 'b', id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000012';
INSERT INTO golden_ids (key, id)
SELECT 'c', id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000013';
GRANT SELECT ON golden_ids TO authenticated;

-- ------------------------------------------------- §A splits RLS ----

SET ROLE authenticated;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000012', true);
SELECT is(
    (SELECT count(*)::integer FROM public.expense_splits),
    8,
    'member B reads all 8 golden splits (E2x3 + E3x1 + E4x2 + deleted E5x2)'
);
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000014', true);
SELECT is(
    (SELECT count(*)::integer
     FROM public.expense_splits AS s
     JOIN public.expenses AS e ON e.id = s.expense_id
     WHERE e.period_id = '30000000-0000-0000-0000-000000000001'),
    0,
    'outsider reads zero splits from the golden period'
);
RESET ROLE;

-- --------------------------------------- §B golden balances (as A) ----

SET ROLE authenticated;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SELECT is(
    (SELECT paid_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'a')),
    2250::bigint, 'A paid 2250 (1000 + 1250)');
SELECT is(
    (SELECT paid_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'b')),
    1::bigint, 'B paid 1 (one-cent expense)');
SELECT is(
    (SELECT paid_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'c')),
    0::bigint, 'C paid 0 (deleted expense excluded)');
SELECT is(
    (SELECT owes_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'a')),
    834::bigint, 'A owes 834 (334 + 500)');
SELECT is(
    (SELECT owes_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'b')),
    1084::bigint, 'B owes 1084 (333 + 1 + 750)');
SELECT is(
    (SELECT owes_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'c')),
    333::bigint, 'C owes 333 (excluded from E3/E4: no rows, not zeros)');
SELECT is(
    (SELECT net_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'a')),
    1416::bigint, 'A net +1416 (group owes A)');
SELECT is(
    (SELECT net_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'b')),
    (-1083)::bigint, 'B net -1083 (B owes the group)');
SELECT is(
    (SELECT net_minor FROM public.period_balances('30000000-0000-0000-0000-000000000001')
     WHERE user_id = (SELECT id FROM golden_ids WHERE key = 'c')),
    (-333)::bigint, 'C net -333 (nets sum to zero: 1416 - 1083 - 333)');
SELECT is(
    (SELECT COALESCE(SUM(amount_minor), 0)::bigint FROM public.expenses
     WHERE period_id = '30000000-0000-0000-0000-000000000001' AND deleted_at IS NULL),
    2251::bigint, 'period gross is 2251 (excludes personal E1 and deleted E5)');
SELECT is(
    (SELECT COALESCE(SUM(amount_minor), 0)::bigint FROM public.expenses
     WHERE period_id IS NULL
       AND user_id = (SELECT id FROM golden_ids WHERE key = 'a')
       AND deleted_at IS NULL
       AND occurred_on BETWEEN DATE '2026-03-01' AND DATE '2026-03-31'),
    2500::bigint, 'personal March total is 2500 (shared costs never merged, BR-6)');
RESET ROLE;

-- ----------------------- Database-owned integrity and RLS ------------------

-- Direct setup bypasses Go by design. Postgres still rejects a persisted
-- expense whose frozen splits do not total the expense amount.
INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000031', p.id,
       '30000000-0000-0000-0000-000000000001', NULL, p.id, 500, 'USD', DATE '2026-03-12', 'invalid split total'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000011';
SELECT throws_ok(
    $$SET CONSTRAINTS expenses_split_total_check IMMEDIATE$$,
    '23514',
    'expense splits must total exactly the expense amount',
    'deferred database trigger rejects an expense with no split rows'
);
SET CONSTRAINTS ALL DEFERRED;

SET ROLE authenticated;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000014', true);
SELECT throws_ok(
    $$SELECT * FROM public.period_balances('30000000-0000-0000-0000-000000000001')$$,
    '42501',
    'period is not available',
    'outsider cannot read balances'
);
RESET ROLE;
SELECT * FROM finish();
ROLLBACK;
