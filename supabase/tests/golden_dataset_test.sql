-- Golden dataset: hand-computed balances asserted as exact integers.
-- (ARCHITECTURE.md §8.2; AGENTS.md "Golden dataset test".)
--
-- Members: A (manager), B, C share period P1. O is the outsider.
-- Period P2 (same roster) isolates RPC validation writes from P1's math.
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
-- Split-rounding conformance (§C) mirrors
-- src/shared/splits/conformance.json — keep the 1000/3 values in sync.
-- Money bugs produce wrong numbers, not exceptions: every figure below is an
-- exact integer assertion, never "returned rows".

BEGIN;
SELECT plan(35);

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

INSERT INTO public.periods (id, name, manager_id, starts_on, ends_on, currency)
SELECT '30000000-0000-0000-0000-000000000002', 'RPC validation period', id, DATE '2026-01-01', NULL, 'USD'
FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000011';

INSERT INTO public.period_members (period_id, user_id, role)
SELECT '30000000-0000-0000-0000-000000000001', id,
       CASE WHEN auth_user_id = '10000000-0000-0000-0000-000000000011'
            THEN 'manager'::public.period_role ELSE 'member'::public.period_role END
FROM public.profiles
WHERE auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000013');

INSERT INTO public.period_members (period_id, user_id, role)
SELECT '30000000-0000-0000-0000-000000000002', id,
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
INSERT INTO golden_ids (key, id) VALUES
    ('p1', '30000000-0000-0000-0000-000000000001'),
    ('p2', '30000000-0000-0000-0000-000000000002'),
    ('e2', '40000000-0000-0000-0000-000000000002'),
    ('e4', '40000000-0000-0000-0000-000000000004'),
    ('r1', '40000000-0000-0000-0000-000000000011'),
    ('rz', '40000000-0000-0000-0000-000000000015'),
    ('ep', '40000000-0000-0000-0000-000000000021');
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

-- ----------------------- §C rounding conformance via restricted RPC (P2, as A) ----
-- Mirrors src/shared/splits/conformance.json case "1000/3".

SET ROLE app_api;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SELECT lives_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000011',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        1000, 'USD', DATE '2026-03-10', 'conformance 1000/3',
        (SELECT jsonb_agg(elem ORDER BY (elem ->> 'amount_minor')::bigint DESC)
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 334) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', 333)
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'c'), 'amount_minor', 333)) AS t))$$,
    'RPC accepts the frozen 334/333/333 split for 1000'
);
SELECT is(
    (SELECT string_agg(s.amount_minor::text, ',' ORDER BY s.amount_minor DESC)
     FROM public.expense_splits AS s WHERE s.expense_id = '40000000-0000-0000-0000-000000000011'),
    '334,333,333',
    'frozen shares stored exactly as given'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000012',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        1000, 'USD', DATE '2026-03-10', 'short total',
        (SELECT jsonb_agg(jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = gk), 'amount_minor', 333))
         FROM (VALUES ('a'), ('b'), ('c')) AS t(gk)))$$,
    '23514',
    'splits total 999 but expense total is 1000: must match exactly',
    'RPC rejects a short total (333x3=999 for 1000): never adjusts silently'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000013',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', DATE '2026-03-10', 'zero share',
        (SELECT jsonb_agg(elem)
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', 0)) AS t))$$,
    '23514',
    'no share may be zero or negative',
    'RPC rejects a zero share: excluded members get no row, not a zero'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000014',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', DATE '2026-03-10', 'negative share',
        (SELECT jsonb_agg(elem)
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 600) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', -100)) AS t))$$,
    '23514',
    'no share may be zero or negative',
    'RPC rejects a negative share (FR-5.7)'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000016',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        1000, 'USD', DATE '2026-03-10', 'non-member participant',
        (SELECT jsonb_agg(elem)
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500) AS elem
               UNION ALL SELECT jsonb_build_object(
                   'user_id', '10000000-0000-0000-0000-000000000099', 'amount_minor', 500)) AS t))$$,
    '23514',
    'every participant must be a period member',
    'RPC rejects a participant who is not a period member'
);
RESET ROLE;

-- --------------------------------- §D restricted RPC authorisation (P2) ----

SET ROLE app_api;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000014', true);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000017',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', DATE '2026-03-10', 'outsider write',
        (SELECT jsonb_agg(jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500))))$$,
    '42501',
    'caller is not a member of this period',
    'outsider cannot create into a period they do not belong to'
);
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000018',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', ((now() AT TIME ZONE 'UTC')::date + 1), 'future expense',
        (SELECT jsonb_agg(jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500))))$$,
    '23514',
    'occurred_on ' || (((now() AT TIME ZONE 'UTC')::date + 1))::text
        || ' is in the future (today is ' || ((now() AT TIME ZONE 'UTC')::date)::text || ' in UTC)',
    'RPC rejects a future occurred_on (reject, never clamp)'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000019',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', DATE '2025-12-31', 'before period start',
        (SELECT jsonb_agg(jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500))))$$,
    '23514',
    'occurred_on 2025-12-31 is outside the period [2026-01-01 .. ongoing]',
    'RPC rejects an expense dated before the period (BR-5)'
);
SELECT lives_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000015',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        900, 'USD', DATE '2026-03-11', 'idempotent',
        (SELECT jsonb_agg(elem ORDER BY (elem ->> 'amount_minor')::bigint DESC, elem ->> 'user_id')
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 300) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', 300)
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'c'), 'amount_minor', 300)) AS t))$$,
    'first create with a client-generated id succeeds'
);
SELECT is(
    (SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000015',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        900, 'USD', DATE '2026-03-11', 'idempotent',
        (SELECT jsonb_agg(elem ORDER BY (elem ->> 'amount_minor')::bigint DESC, elem ->> 'user_id')
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 300) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', 300)
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'c'), 'amount_minor', 300)) AS t))::text),
    '40000000-0000-0000-0000-000000000015',
    'same id + same payload returns the id (idempotent retry)'
);
SELECT throws_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000015',
        '30000000-0000-0000-0000-000000000002', NULL,
        (SELECT id FROM golden_ids WHERE key = 'a'),
        901, 'USD', DATE '2026-03-11', 'conflicting payload',
        (SELECT jsonb_agg(elem ORDER BY (elem ->> 'amount_minor')::bigint DESC, elem ->> 'user_id')
         FROM (SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 301) AS elem
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'b'), 'amount_minor', 300)
               UNION ALL SELECT jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'c'), 'amount_minor', 300)) AS t))$$,
    '23505',
    'expense id 40000000-0000-0000-0000-000000000015 already exists with a different payload',
    'same id + different payload is a conflict, never an overwrite'
);
RESET ROLE;

-- --------------------------------- §E SQL conformance for legacy writes (as A) ----

SET ROLE app_api;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SELECT lives_ok(
    $$SELECT public.create_expense(
        '40000000-0000-0000-0000-000000000021', NULL,
        '20000000-0000-0000-0000-000000000011',
        (SELECT id FROM golden_ids WHERE key = 'a'),
        500, 'USD', DATE '2026-03-11', 'editable personal',
        (SELECT jsonb_agg(jsonb_build_object('user_id', (SELECT id FROM golden_ids WHERE key = 'a'), 'amount_minor', 500))))$$,
    'personal create via RPC succeeds'
);
SELECT lives_ok(
    $$SELECT public.update_expense('40000000-0000-0000-0000-000000000021', p_amount_minor := 700)$$,
    'personal amount edit succeeds'
);
SELECT is(
    (SELECT amount_minor FROM public.expenses WHERE id = '40000000-0000-0000-0000-000000000021'),
    700::bigint, 'personal amount updated to 700');
SELECT is(
    (SELECT amount_minor FROM public.expense_splits WHERE expense_id = '40000000-0000-0000-0000-000000000021'),
    700::bigint, 'owner split rewritten in the same save (sum invariant holds)');
SELECT throws_ok(
    $$SELECT public.update_expense('40000000-0000-0000-0000-000000000002', p_amount_minor := 1001)$$,
    '23514',
    'shared amount is immutable: delete and recreate (FR-5.10)',
    'shared amount edit rejected: delete and recreate (FR-5.10)'
);
SELECT lives_ok(
    $$SELECT public.update_expense('40000000-0000-0000-0000-000000000002', p_note := 'Updated note', p_update_note := true)$$,
    'shared note edit allowed'
);
SELECT is(
    (SELECT note FROM public.expenses WHERE id = '40000000-0000-0000-0000-000000000002'),
    'Updated note', 'shared note updated');
SELECT lives_ok(
    $$SELECT public.soft_delete_expense('40000000-0000-0000-0000-000000000004')$$,
    'author soft-deletes their shared expense'
);
SELECT is(
    (SELECT COALESCE(SUM(amount_minor), 0)::bigint FROM public.expenses
     WHERE period_id = '30000000-0000-0000-0000-000000000001' AND deleted_at IS NULL),
    1001::bigint, 'gross drops to 1001 after the soft-delete (deleted means hidden)');
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000014', true);
SELECT throws_ok(
    $$SELECT * FROM public.period_balances('30000000-0000-0000-0000-000000000001')$$,
    '42501',
    'period is not available',
    'outsider cannot read balances (no visibility at all, FR-4.10)'
);
RESET ROLE;

SELECT * FROM finish();
ROLLBACK;
