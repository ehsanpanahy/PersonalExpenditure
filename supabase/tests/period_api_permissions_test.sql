BEGIN;
SELECT plan(25);

INSERT INTO auth.users (
    instance_id, id, aud, role, email, encrypted_password,
    email_confirmed_at, created_at, updated_at
) VALUES
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000011', 'authenticated', 'authenticated', 'period-creator@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000012', 'authenticated', 'authenticated', 'period-manager@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000013', 'authenticated', 'authenticated', 'period-member@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000014', 'authenticated', 'authenticated', 'period-late-member@example.test', '', now(), now(), now());

-- Stable profile ids make the RLS workflow actions easy to read and assert.
UPDATE public.profiles SET id = CASE auth_user_id
    WHEN '10000000-0000-0000-0000-000000000011' THEN '20000000-0000-0000-0000-000000000011'::uuid
    WHEN '10000000-0000-0000-0000-000000000012' THEN '20000000-0000-0000-0000-000000000012'::uuid
    WHEN '10000000-0000-0000-0000-000000000013' THEN '20000000-0000-0000-0000-000000000013'::uuid
    WHEN '10000000-0000-0000-0000-000000000014' THEN '20000000-0000-0000-0000-000000000014'::uuid
END
WHERE auth_user_id IN ('10000000-0000-0000-0000-000000000011', '10000000-0000-0000-0000-000000000012', '10000000-0000-0000-0000-000000000013', '10000000-0000-0000-0000-000000000014');

SELECT ok(has_column_privilege('app_api', 'public.periods', 'name', 'INSERT'),
    'app_api can insert period rows');
SELECT ok(has_column_privilege('app_api', 'public.periods', 'manager_id', 'UPDATE'),
    'app_api can transfer the period manager column');
SELECT ok(has_column_privilege('app_api', 'public.periods', 'roster_finalized', 'INSERT')
        AND has_column_privilege('app_api', 'public.periods', 'roster_finalized', 'UPDATE'),
    'app_api can open and finalize the fixed roster during creation');
SELECT ok(NOT has_table_privilege('authenticated', 'public.periods', 'INSERT'),
    'authenticated cannot insert period rows directly');
SELECT ok(has_column_privilege('app_api', 'public.period_members', 'role', 'INSERT'),
    'app_api can insert period membership rows');
SELECT ok(has_column_privilege('app_api', 'public.period_members', 'role', 'UPDATE'),
    'app_api can transfer manager membership roles');
SELECT ok(NOT has_table_privilege('authenticated', 'public.period_members', 'INSERT'),
    'authenticated cannot insert period membership rows directly');
SELECT ok(has_function_privilege('app_api', 'public.resolve_period_member_emails(text[])', 'EXECUTE'),
    'app_api can resolve requested accounts without listing profiles');
SELECT ok(NOT has_function_privilege('authenticated', 'public.resolve_period_member_emails(text[])', 'EXECUTE'),
    'authenticated cannot call the restricted account-resolution helper');
SELECT ok(has_function_privilege('app_api', 'public.period_profile_is_live(uuid)', 'EXECUTE'),
    'app_api can use the narrow live-profile check required by RLS');
SELECT ok(NOT has_function_privilege('authenticated', 'public.period_profile_is_live(uuid)', 'EXECUTE'),
    'authenticated cannot call the restricted live-profile helper');

SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SET LOCAL ROLE app_api;
SELECT is(
    (SELECT profile_id::text FROM public.resolve_period_member_emails(ARRAY['period-manager@example.test'])),
    '20000000-0000-0000-0000-000000000012',
    'email helper resolves only the requested confirmed account'
);

-- Go inserts the period with the verified caller as its temporary manager,
-- creates the fixed roster, then transfers manager_id and role in this same tx.
INSERT INTO public.periods (name, manager_id, starts_on, ends_on, currency, roster_finalized)
VALUES ('Workflow test period', '20000000-0000-0000-0000-000000000011', DATE '2026-03-01', NULL, 'USD', false);
INSERT INTO public.period_members (period_id, user_id, role)
SELECT period.id, member.user_id,
       CASE WHEN member.user_id = '20000000-0000-0000-0000-000000000011' THEN 'manager'::public.period_role
            ELSE 'member'::public.period_role END
FROM public.periods AS period
CROSS JOIN (VALUES
    ('20000000-0000-0000-0000-000000000011'::uuid),
    ('20000000-0000-0000-0000-000000000012'::uuid),
    ('20000000-0000-0000-0000-000000000013'::uuid)
) AS member(user_id)
WHERE period.name = 'Workflow test period';
UPDATE public.periods SET roster_finalized = true
 WHERE name = 'Workflow test period';
UPDATE public.period_members SET role = 'member'
 WHERE period_id = (SELECT id FROM public.periods WHERE name = 'Workflow test period')
   AND user_id = '20000000-0000-0000-0000-000000000011';
UPDATE public.period_members SET role = 'manager'
 WHERE period_id = (SELECT id FROM public.periods WHERE name = 'Workflow test period')
   AND user_id = '20000000-0000-0000-0000-000000000012';
UPDATE public.periods SET manager_id = '20000000-0000-0000-0000-000000000012'
 WHERE name = 'Workflow test period';
SET CONSTRAINTS ALL IMMEDIATE;
SET CONSTRAINTS ALL DEFERRED;
RESET ROLE;

SELECT set_config('test.period_id', (SELECT id::text FROM public.periods WHERE name = 'Workflow test period'), true);
SELECT is((SELECT count(*)::integer FROM public.periods WHERE name = 'Workflow test period'), 1,
    'app_api direct DML creates the period');
SELECT is((SELECT count(*)::integer FROM public.period_members pm JOIN public.periods p ON p.id = pm.period_id WHERE p.name = 'Workflow test period'), 3,
    'direct DML creates the fixed roster with caller and requested members');
SELECT is((SELECT count(*)::integer FROM public.period_members WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000012' AND role = 'manager'), 1,
    'period and membership manager agree after creation');

SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000012', true);
SET LOCAL ROLE app_api;
SELECT throws_ok(
    $$INSERT INTO public.period_members (period_id, user_id, role)
      VALUES (current_setting('test.period_id')::uuid, '20000000-0000-0000-0000-000000000014', 'member')$$,
    '42501', NULL, 'RLS rejects adding a member after the roster is finalized'
);
RESET ROLE;

INSERT INTO public.expenses (id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note)
SELECT '40000000-0000-0000-0000-000000000011', creator.id, period.id, NULL, creator.id,
       1, 'USD', DATE '2026-03-02', 'Workflow soft-delete visibility test'
FROM public.profiles AS creator CROSS JOIN public.periods AS period
WHERE creator.auth_user_id = '10000000-0000-0000-0000-000000000011'
  AND period.name = 'Workflow test period';
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT '40000000-0000-0000-0000-000000000011', id, 1
FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000011';

-- The current manager transfers directly by updating both sides of the
-- invariant within one app_api transaction.
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000012', true);
SET LOCAL ROLE app_api;
UPDATE public.period_members SET role = 'member'
 WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000012';
UPDATE public.period_members SET role = 'manager'
 WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000011';
UPDATE public.periods SET manager_id = '20000000-0000-0000-0000-000000000011'
 WHERE id = current_setting('test.period_id')::uuid;
SET CONSTRAINTS ALL IMMEDIATE;
SET CONSTRAINTS ALL DEFERRED;
RESET ROLE;
SELECT is((SELECT manager_id FROM public.periods WHERE id = current_setting('test.period_id')::uuid), '20000000-0000-0000-0000-000000000011'::uuid,
    'direct DML transfer updates periods.manager_id');
SELECT is((SELECT count(*)::integer FROM public.period_members WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000011' AND role = 'manager'), 1,
    'direct DML transfer promotes the new manager membership');
SELECT is((SELECT count(*)::integer FROM public.period_members WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000012' AND role = 'member'), 1,
    'direct DML transfer demotes the former manager');

-- A member can read the shared rows but cannot change the manager or delete.
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000012', true);
SET LOCAL ROLE app_api;
SELECT is((WITH changed AS (
    UPDATE public.periods SET deleted_at = now() WHERE id = current_setting('test.period_id')::uuid RETURNING id
) SELECT count(*)::integer FROM changed), 0,
    'non-manager update affects no period rows under RLS');
RESET ROLE;

-- A tombstoned roster member cannot be promoted, even by the current manager.
UPDATE public.profiles SET deleted_at = now() WHERE auth_user_id = '10000000-0000-0000-0000-000000000013';
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SET LOCAL ROLE app_api;
SELECT throws_ok(
    $$UPDATE public.period_members SET role = 'manager'
      WHERE period_id = current_setting('test.period_id')::uuid AND user_id = '20000000-0000-0000-0000-000000000013'$$,
    '42501', NULL, 'RLS rejects promoting a tombstoned profile'
);
UPDATE public.periods SET deleted_at = now(), updated_at = now()
 WHERE id = current_setting('test.period_id')::uuid;
SET CONSTRAINTS ALL IMMEDIATE;
RESET ROLE;

SELECT ok((SELECT deleted_at IS NOT NULL FROM public.periods WHERE id = current_setting('test.period_id')::uuid),
    'manager direct update soft-deletes the period');
SET LOCAL ROLE authenticated;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000011', true);
SELECT is((SELECT count(*)::integer FROM public.periods WHERE id = current_setting('test.period_id')::uuid), 0,
    'soft-deleted period is hidden from its former manager');
SELECT is((SELECT count(*)::integer FROM public.expenses WHERE period_id = current_setting('test.period_id')::uuid), 0,
    'soft-deleted period expenses are hidden through direct table access');
SELECT throws_ok(
    $$SELECT * FROM public.period_balances(current_setting('test.period_id')::uuid)$$,
    '42501', NULL, 'period balances are unavailable after period soft-delete'
);
RESET ROLE;

SELECT * FROM finish();
ROLLBACK;
