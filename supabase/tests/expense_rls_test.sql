BEGIN;
SELECT plan(9);

INSERT INTO auth.users (
    instance_id, id, aud, role, email, encrypted_password,
    email_confirmed_at, created_at, updated_at
) VALUES
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000001', 'authenticated', 'authenticated', 'member-a@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000002', 'authenticated', 'authenticated', 'member-b@example.test', '', now(), now(), now()),
    ('00000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000003', 'authenticated', 'authenticated', 'outsider@example.test', '', now(), now(), now());

SELECT is(
    (SELECT count(*)::integer FROM public.profiles WHERE auth_user_id IN (
        '10000000-0000-0000-0000-000000000001',
        '10000000-0000-0000-0000-000000000002',
        '10000000-0000-0000-0000-000000000003'
    )),
    3,
    'Auth user trigger creates profiles'
);

UPDATE public.profiles SET base_currency = 'USD'
WHERE auth_user_id IN (
    '10000000-0000-0000-0000-000000000001',
    '10000000-0000-0000-0000-000000000002',
    '10000000-0000-0000-0000-000000000003'
);

INSERT INTO public.categories (id, user_id, name) VALUES
    ('20000000-0000-0000-0000-000000000001', (SELECT id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000001'), 'Food'),
    ('20000000-0000-0000-0000-000000000002', (SELECT id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000002'), 'Food'),
    ('20000000-0000-0000-0000-000000000003', (SELECT id FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000003'), 'Food');

INSERT INTO public.periods (id, name, manager_id, starts_on, currency)
SELECT '30000000-0000-0000-0000-000000000001', 'Shared period', id, DATE '2026-01-01', 'USD'
FROM public.profiles WHERE auth_user_id = '10000000-0000-0000-0000-000000000001';

INSERT INTO public.period_members (period_id, user_id, role)
SELECT '30000000-0000-0000-0000-000000000001', id,
       CASE WHEN auth_user_id = '10000000-0000-0000-0000-000000000001'
            THEN 'manager'::public.period_role ELSE 'member'::public.period_role END
FROM public.profiles
WHERE auth_user_id IN ('10000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000002');

INSERT INTO public.expenses (
    id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note
)
SELECT '40000000-0000-0000-0000-000000000001', p.id, NULL,
       '20000000-0000-0000-0000-000000000001', p.id, 1250, 'USD', DATE '2026-03-01', 'A personal expense'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000001';

INSERT INTO public.expenses (
    id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note
)
SELECT '40000000-0000-0000-0000-000000000002', p.id, NULL,
       '20000000-0000-0000-0000-000000000002', p.id, 2500, 'USD', DATE '2026-03-02', 'B personal expense'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000002';

INSERT INTO public.expenses (
    id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note
)
SELECT '40000000-0000-0000-0000-000000000003', p.id, NULL,
       '20000000-0000-0000-0000-000000000003', p.id, 3000, 'USD', DATE '2026-03-03', 'Outsider personal expense'
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000003';

INSERT INTO public.expenses (
    id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note, deleted_at
)
SELECT '40000000-0000-0000-0000-000000000005', p.id, NULL,
       '20000000-0000-0000-0000-000000000001', p.id, 700, 'USD', DATE '2026-03-05', 'Soft deleted', now()
FROM public.profiles p WHERE p.auth_user_id = '10000000-0000-0000-0000-000000000001';

INSERT INTO public.expenses (
    id, user_id, period_id, category_id, paid_by, amount_minor, currency, occurred_on, note
)
SELECT '40000000-0000-0000-0000-000000000004', author.id,
       '30000000-0000-0000-0000-000000000001', NULL, author.id, 4500, 'USD', DATE '2026-03-04', 'Shared expense'
FROM public.profiles author WHERE author.auth_user_id = '10000000-0000-0000-0000-000000000001';

SET LOCAL ROLE authenticated;
SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000001', true);
SELECT is((SELECT count(*)::integer FROM public.expenses WHERE deleted_at IS NULL), 2, 'member A live query contains own personal and period expenses');
SELECT is((SELECT count(*)::integer FROM public.profiles), 1, 'member A reads only own profile');
SELECT is((SELECT count(*)::integer FROM public.expenses WHERE deleted_at IS NOT NULL), 1, 'deleted row remains visible to its owner for recovery');

SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000002', true);
SELECT is((SELECT count(*)::integer FROM public.expenses), 2, 'member B reads own personal and period expenses');

SELECT set_config('request.jwt.claim.sub', '10000000-0000-0000-0000-000000000003', true);
SELECT is((SELECT count(*)::integer FROM public.expenses), 1, 'outsider reads only own personal expense');
SELECT is((SELECT count(*)::integer FROM public.expenses WHERE period_id = '30000000-0000-0000-0000-000000000001'), 0, 'outsider reads no rows from shared period');
SELECT is((SELECT count(*)::integer FROM public.categories), 1, 'outsider reads only own category');
SELECT is((SELECT count(*)::integer FROM public.periods), 0, 'outsider cannot read the period');

RESET ROLE;
SELECT * FROM finish();
ROLLBACK;
