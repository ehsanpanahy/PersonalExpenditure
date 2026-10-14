-- Initial database slice for authenticated expense reads.
-- Writes remain disabled until the SQL RPCs and their validation are added.

CREATE TYPE public.period_role AS ENUM ('manager', 'member');

CREATE TABLE public.profiles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_user_id uuid UNIQUE REFERENCES auth.users(id) ON DELETE SET NULL,
    email text,
    display_name text,
    base_currency char(3),
    timezone text NOT NULL DEFAULT 'UTC',
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT profiles_base_currency_iso CHECK (
        base_currency IS NULL OR btrim(base_currency::text) ~ '^[A-Z]{3}$'
    )
);

CREATE TABLE public.categories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES public.profiles(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    icon text,
    color text,
    sort_order integer NOT NULL DEFAULT 0,
    is_archived boolean NOT NULL DEFAULT false,
    CONSTRAINT categories_id_user_id_unique UNIQUE (id, user_id)
);

CREATE UNIQUE INDEX categories_user_name_unique
    ON public.categories (user_id, lower(name));
CREATE INDEX categories_user_sort_order_idx
    ON public.categories (user_id, sort_order);

CREATE TABLE public.periods (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    manager_id uuid NOT NULL REFERENCES public.profiles(id) ON DELETE RESTRICT,
    starts_on date NOT NULL,
    ends_on date,
    currency char(3) NOT NULL CHECK (btrim(currency::text) ~ '^[A-Z]{3}$'),
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT periods_date_range CHECK (ends_on IS NULL OR ends_on >= starts_on)
);

CREATE TABLE public.period_members (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id uuid NOT NULL REFERENCES public.periods(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES public.profiles(id) ON DELETE RESTRICT,
    role public.period_role NOT NULL,
    joined_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT period_members_period_user_unique UNIQUE (period_id, user_id)
);

CREATE UNIQUE INDEX period_members_one_manager_per_period
    ON public.period_members (period_id) WHERE role = 'manager';
CREATE INDEX period_members_user_idx ON public.period_members (user_id);

CREATE TABLE public.expenses (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.profiles(id) ON DELETE RESTRICT,
    period_id uuid REFERENCES public.periods(id) ON DELETE RESTRICT,
    category_id uuid,
    paid_by uuid NOT NULL REFERENCES public.profiles(id) ON DELETE RESTRICT,
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    currency char(3) NOT NULL CHECK (btrim(currency::text) ~ '^[A-Z]{3}$'),
    occurred_on date NOT NULL,
    note text,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expenses_personal_or_period_shape CHECK (
        (period_id IS NULL AND paid_by = user_id AND category_id IS NOT NULL)
        OR
        (period_id IS NOT NULL AND category_id IS NULL)
    ),
    CONSTRAINT expenses_category_owner_fk
        FOREIGN KEY (category_id, user_id)
        REFERENCES public.categories (id, user_id)
        ON DELETE RESTRICT
);

CREATE INDEX expenses_period_occurred_live_idx
    ON public.expenses (period_id, occurred_on DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX expenses_personal_user_occurred_live_idx
    ON public.expenses (user_id, occurred_on DESC, id DESC)
    WHERE period_id IS NULL AND deleted_at IS NULL;

CREATE FUNCTION public.enforce_expense_currency()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
DECLARE
    expected_currency char(3);
BEGIN
    IF NEW.period_id IS NULL THEN
        SELECT profile.base_currency INTO expected_currency
        FROM public.profiles AS profile
        WHERE profile.id = NEW.user_id AND profile.deleted_at IS NULL;
    ELSE
        SELECT period.currency INTO expected_currency
        FROM public.periods AS period
        WHERE period.id = NEW.period_id AND period.deleted_at IS NULL;
    END IF;

    IF expected_currency IS NULL THEN
        RAISE EXCEPTION 'currency scope is not configured or no longer available'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.currency <> expected_currency THEN
        RAISE EXCEPTION 'expense currency must match its owner or period currency'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$function$;

CREATE TRIGGER expenses_currency_matches_scope
    BEFORE INSERT OR UPDATE OF user_id, period_id, currency ON public.expenses
    FOR EACH ROW EXECUTE FUNCTION public.enforce_expense_currency();

-- A profile is created for every Auth identity, including users who have not
-- completed onboarding yet. Currency stays NULL until onboarding sets it.
CREATE FUNCTION public.create_profile_for_auth_user()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
BEGIN
    INSERT INTO public.profiles (auth_user_id, email)
    VALUES (NEW.id, NEW.email)
    ON CONFLICT (auth_user_id) DO NOTHING;
    RETURN NEW;
END;
$function$;

CREATE TRIGGER on_auth_user_created_create_profile
    AFTER INSERT ON auth.users
    FOR EACH ROW EXECUTE FUNCTION public.create_profile_for_auth_user();

-- Backfill identities that existed before this trigger was installed.
INSERT INTO public.profiles (auth_user_id, email)
SELECT id, email FROM auth.users
ON CONFLICT (auth_user_id) DO NOTHING;

-- Only return membership for the JWT caller, and only in a live period. The
-- definer function avoids recursive RLS lookups between periods and members.
CREATE FUNCTION public.is_live_period_member(p_period_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
    SELECT EXISTS (
        SELECT 1
        FROM public.period_members AS pm
        JOIN public.periods AS p ON p.id = pm.period_id
        JOIN public.profiles AS profile ON profile.id = pm.user_id
        WHERE pm.period_id = p_period_id
          AND profile.auth_user_id = auth.uid()
          AND profile.deleted_at IS NULL
          AND p.deleted_at IS NULL
    );
$function$;
REVOKE ALL ON FUNCTION public.is_live_period_member(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.is_live_period_member(uuid) TO authenticated;

ALTER TABLE public.profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.period_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.expenses ENABLE ROW LEVEL SECURITY;

CREATE POLICY profiles_select_self ON public.profiles
    FOR SELECT TO authenticated
    USING (auth_user_id = (SELECT auth.uid()) AND deleted_at IS NULL);

CREATE POLICY categories_select_owner ON public.categories
    FOR SELECT TO authenticated
    USING (user_id = (
        SELECT profile.id FROM public.profiles AS profile
        WHERE profile.auth_user_id = (SELECT auth.uid())
          AND profile.deleted_at IS NULL
    ));
CREATE POLICY categories_insert_owner ON public.categories
    FOR INSERT TO authenticated
    WITH CHECK (user_id = (
        SELECT profile.id FROM public.profiles AS profile
        WHERE profile.auth_user_id = (SELECT auth.uid())
          AND profile.deleted_at IS NULL
    ));
CREATE POLICY categories_update_owner ON public.categories
    FOR UPDATE TO authenticated
    USING (user_id = (
        SELECT profile.id FROM public.profiles AS profile
        WHERE profile.auth_user_id = (SELECT auth.uid())
          AND profile.deleted_at IS NULL
    ))
    WITH CHECK (user_id = (
        SELECT profile.id FROM public.profiles AS profile
        WHERE profile.auth_user_id = (SELECT auth.uid())
          AND profile.deleted_at IS NULL
    ));
CREATE POLICY categories_delete_owner ON public.categories
    FOR DELETE TO authenticated
    USING (user_id = (
        SELECT profile.id FROM public.profiles AS profile
        WHERE profile.auth_user_id = (SELECT auth.uid())
          AND profile.deleted_at IS NULL
    ));

CREATE POLICY periods_select_member ON public.periods
    FOR SELECT TO authenticated
    USING (public.is_live_period_member(id));
CREATE POLICY period_members_select_member ON public.period_members
    FOR SELECT TO authenticated
    USING (public.is_live_period_member(period_id));

CREATE POLICY expenses_select_owner_or_period_member ON public.expenses
    FOR SELECT TO authenticated
    USING (
        (
            period_id IS NULL
            AND user_id = (
                SELECT profile.id FROM public.profiles AS profile
                WHERE profile.auth_user_id = (SELECT auth.uid())
                  AND profile.deleted_at IS NULL
            )
        )
        OR public.is_live_period_member(period_id)
    );

-- This initial API slice is read-only for expenses, profiles, periods and
-- memberships. Category writes remain owner-scoped per the agreed data model.
GRANT USAGE ON SCHEMA public TO authenticated;
REVOKE ALL ON public.profiles, public.periods, public.period_members, public.expenses
    FROM anon, authenticated;
GRANT SELECT ON public.profiles, public.periods, public.period_members, public.expenses
    TO authenticated;
REVOKE ALL ON public.categories FROM anon, authenticated;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.categories TO authenticated;
