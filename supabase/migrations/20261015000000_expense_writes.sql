-- Expense persistence support: frozen split rows and computed balances.
--
-- Go owns expense workflows and request validation. Postgres owns RLS, exact
-- split-total integrity, and balance aggregates.
--
-- Design notes:
-- - Splits are stored results, not rules (BR-3). Go writes them with the
--   expense in one caller-scoped transaction; the deferred trigger in the Go
--   writes migration checks SUM(splits) == amount exactly at commit.
-- - The authenticated mobile role is read-only. The restricted app_api role
--   receives narrow direct-write grants and remains subject to RLS.
-- - Balances are computed, never stored (BR: balances are computed).
-- - Application validation, including positive shares and participant rules,
--   is performed by Go. Database constraints continue to protect row shape.

-- ---------------------------------------------------------------------------
-- 1. expense_splits table
-- ---------------------------------------------------------------------------

CREATE TABLE public.expense_splits (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    expense_id uuid NOT NULL REFERENCES public.expenses(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES public.profiles(id) ON DELETE RESTRICT,
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT expense_splits_expense_user_unique UNIQUE (expense_id, user_id)
);

CREATE INDEX expense_splits_expense_idx ON public.expense_splits (expense_id);
CREATE INDEX expense_splits_user_idx ON public.expense_splits (user_id);

-- Backfill personal expenses created by the read-only slice (each had exactly
-- one owner for the full amount). Shared expenses pre-dating this migration
-- have no recorded rule to backfill from, so they are left split-less and
-- contribute only what their rows state; new shared writes always carry splits.
INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
SELECT id, user_id, amount_minor
FROM public.expenses
WHERE period_id IS NULL
ON CONFLICT (expense_id, user_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. Auth helpers (SECURITY DEFINER, locked search_path, no RLS recursion)
-- ---------------------------------------------------------------------------

CREATE FUNCTION public.caller_profile_id()
RETURNS uuid
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
    SELECT profile.id
    FROM public.profiles AS profile
    WHERE profile.auth_user_id = auth.uid()
      AND profile.deleted_at IS NULL;
$function$;

REVOKE ALL ON FUNCTION public.caller_profile_id() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.caller_profile_id() TO authenticated;

-- ---------------------------------------------------------------------------
-- 3. period_balances — computed per member, never stored
-- ---------------------------------------------------------------------------

CREATE FUNCTION public.period_balances(p_period_id uuid)
RETURNS TABLE (
    user_id uuid,
    paid_minor bigint,
    owes_minor bigint,
    net_minor bigint
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
BEGIN
    IF p_period_id IS NULL OR NOT public.is_live_period_member(p_period_id) THEN
        RAISE EXCEPTION 'period is not available' USING ERRCODE = '42501';
    END IF;

    RETURN QUERY
    SELECT
        pm.user_id,
        COALESCE(paid.total, 0)::bigint AS paid_minor,
        COALESCE(owes.total, 0)::bigint AS owes_minor,
        (COALESCE(paid.total, 0) - COALESCE(owes.total, 0))::bigint AS net_minor
    FROM public.period_members AS pm
    LEFT JOIN LATERAL (
        SELECT SUM(e.amount_minor) AS total
        FROM public.expenses AS e
        WHERE e.period_id = p_period_id
          AND e.deleted_at IS NULL
          AND e.paid_by = pm.user_id
    ) AS paid ON true
    LEFT JOIN LATERAL (
        SELECT SUM(s.amount_minor) AS total
        FROM public.expense_splits AS s
        JOIN public.expenses AS e ON e.id = s.expense_id
        WHERE e.period_id = p_period_id
          AND e.deleted_at IS NULL
          AND s.user_id = pm.user_id
    ) AS owes ON true
    WHERE pm.period_id = p_period_id
    ORDER BY pm.user_id;
END;
$function$;

REVOKE ALL ON FUNCTION public.period_balances(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.period_balances(uuid) TO authenticated;

-- ---------------------------------------------------------------------------
-- 4. RLS: splits are readable exactly where the parent expense is readable
-- ---------------------------------------------------------------------------

ALTER TABLE public.expense_splits ENABLE ROW LEVEL SECURITY;

CREATE POLICY expense_splits_select_with_parent ON public.expense_splits
    FOR SELECT TO authenticated
    USING (
        EXISTS (
            SELECT 1 FROM public.expenses AS e
            WHERE e.id = expense_splits.expense_id
              AND (
                    (
                        e.period_id IS NULL
                        AND e.user_id = (
                            SELECT profile.id FROM public.profiles AS profile
                            WHERE profile.auth_user_id = (SELECT auth.uid())
                              AND profile.deleted_at IS NULL
                        )
                    )
                    OR public.is_live_period_member(e.period_id)
              )
        )
    );

-- No INSERT/UPDATE/DELETE policies for authenticated; the restricted app_api role writes through Go.
REVOKE ALL ON public.expense_splits FROM anon, authenticated;
GRANT SELECT ON public.expense_splits TO authenticated;
