-- Write slice for expenses: frozen split results, validated writes, computed balances.
--
-- ARCHITECTURE.md §5.1 (expense_splits), §5.2 (create_expense, update_expense,
-- soft_delete_expense, period_balances), §5.4 (rounding), §5.5 (RLS).
--
-- Design notes:
-- - Splits are stored results, not rules (BR-3). The RPC validates
--   SUM(splits) == amount exactly (no tolerance) and freezes rows at creation.
-- - Direct writes to expense_splits by API users are denied: no INSERT/UPDATE/
--   DELETE RLS policies exist for authenticated, and table privileges are
--   SELECT-only. All writes go through SECURITY DEFINER RPCs with a locked
--   search_path, per the "splits via RPC only" rule.
-- - Balances are computed, never stored (BR: balances are computed).
-- - A 1-cent expense cannot be split across 3 members: allocate(1,3) previews
--   as [1,0,0] but the server requires every share > 0, so excluded members
--   get no row. The one-cent golden case is a single-participant expense.

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

-- Membership of an arbitrary profile in a live period (for validating split
-- participants and paid_by). Distinct from is_live_period_member(), which
-- answers the same question for the JWT caller only.
CREATE FUNCTION public.is_live_member_of(p_user_id uuid, p_period_id uuid)
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
          AND pm.user_id = p_user_id
          AND profile.deleted_at IS NULL
          AND p.deleted_at IS NULL
    );
$function$;

CREATE FUNCTION public.is_live_period_manager(p_period_id uuid)
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
          AND pm.role = 'manager'
    );
$function$;

REVOKE ALL ON FUNCTION public.caller_profile_id() FROM PUBLIC;
REVOKE ALL ON FUNCTION public.is_live_member_of(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.is_live_period_manager(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.caller_profile_id() TO authenticated;
GRANT EXECUTE ON FUNCTION public.is_live_member_of(uuid, uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.is_live_period_manager(uuid) TO authenticated;

-- ---------------------------------------------------------------------------
-- 3. create_expense — the only write path (authorise → validate → insert)
-- ---------------------------------------------------------------------------

CREATE FUNCTION public.create_expense(
    p_id uuid,
    p_period_id uuid,
    p_category_id uuid,
    p_paid_by uuid,
    p_amount_minor bigint,
    p_currency char(3),
    p_occurred_on date,
    p_note text,
    p_splits jsonb
)
RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
DECLARE
    v_caller uuid;
    v_tz text;
    v_base_currency char(3);
    v_today date;
    v_is_shared boolean;
    v_period_currency char(3);
    v_starts_on date;
    v_ends_on date;
    v_existing public.expenses%ROWTYPE;
    v_elem jsonb;
    v_split_user uuid;
    v_split_amount bigint;
    v_sum bigint := 0;
    v_count integer := 0;
    v_distinct integer := 0;
    v_matched integer := 0;
BEGIN
    IF p_id IS NULL THEN
        RAISE EXCEPTION 'expense id is required (client-generated UUID)'
            USING ERRCODE = '23514';
    END IF;
    IF p_amount_minor IS NULL OR p_amount_minor <= 0 THEN
        RAISE EXCEPTION 'amount must be greater than zero'
            USING ERRCODE = '23514';
    END IF;
    IF p_currency IS NULL OR btrim(p_currency::text) !~ '^[A-Z]{3}$' THEN
        RAISE EXCEPTION 'currency must be ISO-4217 (e.g. USD)'
            USING ERRCODE = '23514';
    END IF;
    IF p_occurred_on IS NULL THEN
        RAISE EXCEPTION 'occurred_on is required'
            USING ERRCODE = '23514';
    END IF;
    IF p_paid_by IS NULL THEN
        RAISE EXCEPTION 'paid_by is required'
            USING ERRCODE = '23514';
    END IF;
    IF p_splits IS NULL
        OR jsonb_typeof(p_splits) <> 'array'
        OR jsonb_array_length(p_splits) < 1 THEN
        RAISE EXCEPTION 'at least one split is required'
            USING ERRCODE = '23514';
    END IF;

    SELECT public.caller_profile_id() INTO v_caller;
    IF v_caller IS NULL THEN
        RAISE EXCEPTION 'not authenticated or profile is unavailable'
            USING ERRCODE = '42501';
    END IF;

    -- Idempotency on the client-generated id: same id + same payload returns
    -- the id; same id + different payload is a conflict, never an overwrite.
    SELECT * INTO v_existing FROM public.expenses WHERE id = p_id;
    IF FOUND THEN
        IF v_existing.user_id = v_caller
            AND v_existing.period_id IS NOT DISTINCT FROM p_period_id
            AND v_existing.category_id IS NOT DISTINCT FROM p_category_id
            AND v_existing.paid_by IS NOT DISTINCT FROM p_paid_by
            AND v_existing.amount_minor = p_amount_minor
            AND v_existing.currency = p_currency
            AND v_existing.occurred_on = p_occurred_on
            AND v_existing.note IS NOT DISTINCT FROM p_note
            AND (SELECT count(*) FROM public.expense_splits
                 WHERE expense_id = p_id) = jsonb_array_length(p_splits) THEN
            SELECT count(*) INTO v_matched
            FROM jsonb_array_elements(p_splits) AS elem
            WHERE EXISTS (
                SELECT 1 FROM public.expense_splits AS s
                WHERE s.expense_id = p_id
                  AND s.user_id = (elem.value ->> 'user_id')::uuid
                  AND s.amount_minor = (elem.value ->> 'amount_minor')::bigint
            );
            IF v_matched = jsonb_array_length(p_splits) THEN
                RETURN p_id;
            END IF;
        END IF;
        RAISE EXCEPTION 'expense id % already exists with a different payload',
            p_id USING ERRCODE = '23505';
    END IF;

    SELECT timezone, base_currency INTO v_tz, v_base_currency
    FROM public.profiles WHERE id = v_caller;
    BEGIN
        v_today := (now() AT TIME ZONE v_tz)::date;
    EXCEPTION WHEN invalid_parameter_value THEN
        RAISE EXCEPTION 'unknown timezone on profile: %', v_tz
            USING ERRCODE = '23514';
    END;
    IF p_occurred_on > v_today THEN
        RAISE EXCEPTION 'occurred_on % is in the future (today is % in %)',
            p_occurred_on, v_today, v_tz USING ERRCODE = '23514';
    END IF;

    v_is_shared := p_period_id IS NOT NULL;

    IF NOT v_is_shared THEN
        -- Personal: caller is the owner, paid_by is the owner, one split.
        IF p_paid_by <> v_caller THEN
            RAISE EXCEPTION 'personal expenses must be paid by their owner'
                USING ERRCODE = '23514';
        END IF;
        IF p_category_id IS NULL THEN
            RAISE EXCEPTION 'personal expenses require a category'
                USING ERRCODE = '23514';
        END IF;
        PERFORM 1 FROM public.categories AS c
        WHERE c.id = p_category_id AND c.user_id = v_caller;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'category does not belong to the caller'
                USING ERRCODE = '42501';
        END IF;
        IF v_base_currency IS NULL THEN
            RAISE EXCEPTION 'onboarding is incomplete: no base currency'
                USING ERRCODE = '23514';
        END IF;
        IF p_currency <> v_base_currency THEN
            RAISE EXCEPTION 'personal expense currency must match base currency'
                USING ERRCODE = '23514';
        END IF;
        IF jsonb_array_length(p_splits) <> 1 THEN
            RAISE EXCEPTION 'personal expenses have exactly one owner split'
                USING ERRCODE = '23514';
        END IF;
        v_elem := p_splits -> 0;
        BEGIN
            v_split_user := (v_elem ->> 'user_id')::uuid;
            v_split_amount := (v_elem ->> 'amount_minor')::bigint;
        EXCEPTION WHEN OTHERS THEN
            RAISE EXCEPTION 'each split needs {user_id uuid, amount_minor bigint}'
                USING ERRCODE = '23514';
        END;
        IF v_split_user <> v_caller OR v_split_amount <> p_amount_minor THEN
            RAISE EXCEPTION 'personal split must be the owner for the full amount'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        -- Shared: caller must be a member; every participant must be a member;
        -- excluded members are omitted, never stored as zero.
        SELECT p.currency, p.starts_on, p.ends_on
        INTO v_period_currency, v_starts_on, v_ends_on
        FROM public.periods AS p
        WHERE p.id = p_period_id AND p.deleted_at IS NULL;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'period is not available'
                USING ERRCODE = '42501';
        END IF;
        IF p_category_id IS NOT NULL THEN
            RAISE EXCEPTION 'shared expenses are uncategorised in v1'
                USING ERRCODE = '23514';
        END IF;
        IF NOT public.is_live_period_member(p_period_id) THEN
            RAISE EXCEPTION 'caller is not a member of this period'
                USING ERRCODE = '42501';
        END IF;
        IF p_occurred_on < v_starts_on
            OR (v_ends_on IS NOT NULL AND p_occurred_on > v_ends_on) THEN
            RAISE EXCEPTION 'occurred_on % is outside the period [% .. %]',
                p_occurred_on, v_starts_on, COALESCE(v_ends_on::text, 'ongoing')
                USING ERRCODE = '23514';
        END IF;
        IF p_currency <> v_period_currency THEN
            RAISE EXCEPTION 'shared expense currency must match period currency'
                USING ERRCODE = '23514';
        END IF;
        IF NOT public.is_live_member_of(p_paid_by, p_period_id) THEN
            RAISE EXCEPTION 'paid_by must be a member of the period'
                USING ERRCODE = '23514';
        END IF;
        SELECT count(DISTINCT elem.value ->> 'user_id') INTO v_distinct
        FROM jsonb_array_elements(p_splits) AS elem;
        v_count := jsonb_array_length(p_splits);
        IF v_distinct <> v_count THEN
            RAISE EXCEPTION 'duplicate participant in splits'
                USING ERRCODE = '23514';
        END IF;
        FOR v_elem IN SELECT * FROM jsonb_array_elements(p_splits) LOOP
            BEGIN
                v_split_user := (v_elem ->> 'user_id')::uuid;
                v_split_amount := (v_elem ->> 'amount_minor')::bigint;
            EXCEPTION WHEN OTHERS THEN
                RAISE EXCEPTION 'each split needs {user_id uuid, amount_minor bigint}'
                    USING ERRCODE = '23514';
            END;
            IF v_split_amount IS NULL OR v_split_amount <= 0 THEN
                RAISE EXCEPTION 'no share may be zero or negative'
                    USING ERRCODE = '23514';
            END IF;
            IF NOT public.is_live_member_of(v_split_user, p_period_id) THEN
                RAISE EXCEPTION 'every participant must be a period member'
                    USING ERRCODE = '23514';
            END IF;
            v_sum := v_sum + v_split_amount;
        END LOOP;
        IF v_sum <> p_amount_minor THEN
            RAISE EXCEPTION 'splits total % but expense total is %: must match exactly',
                v_sum, p_amount_minor USING ERRCODE = '23514';
        END IF;
    END IF;

    INSERT INTO public.expenses
        (id, user_id, period_id, category_id, paid_by,
         amount_minor, currency, occurred_on, note)
    VALUES
        (p_id, v_caller, p_period_id, p_category_id, p_paid_by,
         p_amount_minor, p_currency, p_occurred_on, p_note);

    FOR v_elem IN SELECT * FROM jsonb_array_elements(p_splits) LOOP
        INSERT INTO public.expense_splits (expense_id, user_id, amount_minor)
        VALUES (p_id,
                (v_elem ->> 'user_id')::uuid,
                (v_elem ->> 'amount_minor')::bigint);
    END LOOP;

    RETURN p_id;
END;
$function$;

REVOKE ALL ON FUNCTION public.create_expense(uuid, uuid, uuid, uuid, bigint, char(3), date, text, jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.create_expense(uuid, uuid, uuid, uuid, bigint, char(3), date, text, jsonb) TO authenticated;

-- ---------------------------------------------------------------------------
-- 4. update_expense — personal: amount/date/category/note; shared: date/note
--    (FR-5.10: shared amount/splits are immutable — delete and recreate)
-- ---------------------------------------------------------------------------

CREATE FUNCTION public.update_expense(
    p_expense_id uuid,
    p_amount_minor bigint DEFAULT NULL,
    p_occurred_on date DEFAULT NULL,
    p_category_id uuid DEFAULT NULL,
    p_note text DEFAULT NULL,
    p_update_note boolean DEFAULT false
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
DECLARE
    v_caller uuid;
    v_exp public.expenses%ROWTYPE;
    v_tz text;
    v_today date;
    v_starts_on date;
    v_ends_on date;
    v_is_manager boolean := false;
BEGIN
    IF p_expense_id IS NULL THEN
        RAISE EXCEPTION 'expense id is required' USING ERRCODE = '23514';
    END IF;
    SELECT public.caller_profile_id() INTO v_caller;
    IF v_caller IS NULL THEN
        RAISE EXCEPTION 'not authenticated or profile is unavailable'
            USING ERRCODE = '42501';
    END IF;

    SELECT * INTO v_exp FROM public.expenses WHERE id = p_expense_id;
    IF NOT FOUND THEN
        -- Indistinguishable from "does not exist" (MODULE_VIEW.md §6).
        RAISE EXCEPTION 'expense is not available' USING ERRCODE = '42501';
    END IF;
    IF v_exp.deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'expense is deleted' USING ERRCODE = '23514';
    END IF;

    IF v_exp.period_id IS NOT NULL THEN
        SELECT p.starts_on, p.ends_on INTO v_starts_on, v_ends_on
        FROM public.periods AS p
        WHERE p.id = v_exp.period_id AND p.deleted_at IS NULL;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'period is not available' USING ERRCODE = '42501';
        END IF;
        v_is_manager := public.is_live_period_manager(v_exp.period_id);
        IF v_exp.user_id <> v_caller AND NOT v_is_manager THEN
            RAISE EXCEPTION 'only the author or the period manager may edit'
                USING ERRCODE = '42501';
        END IF;
        IF p_amount_minor IS NOT NULL THEN
            RAISE EXCEPTION 'shared amount is immutable: delete and recreate (FR-5.10)'
                USING ERRCODE = '23514';
        END IF;
        IF p_category_id IS NOT NULL THEN
            RAISE EXCEPTION 'shared expenses are uncategorised in v1'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        IF v_exp.user_id <> v_caller THEN
            RAISE EXCEPTION 'only the owner may edit a personal expense'
                USING ERRCODE = '42501';
        END IF;
        IF p_amount_minor IS NOT NULL THEN
            IF p_amount_minor <= 0 THEN
                RAISE EXCEPTION 'amount must be greater than zero'
                    USING ERRCODE = '23514';
            END IF;
            UPDATE public.expenses
            SET amount_minor = p_amount_minor, updated_at = now()
            WHERE id = p_expense_id;
            UPDATE public.expense_splits
            SET amount_minor = p_amount_minor
            WHERE expense_id = p_expense_id AND user_id = v_exp.user_id;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'owner split is missing' USING ERRCODE = '23514';
            END IF;
        END IF;
        IF p_category_id IS NOT NULL THEN
            PERFORM 1 FROM public.categories AS c
            WHERE c.id = p_category_id AND c.user_id = v_caller;
            IF NOT FOUND THEN
                RAISE EXCEPTION 'category does not belong to the caller'
                    USING ERRCODE = '42501';
            END IF;
            UPDATE public.expenses
            SET category_id = p_category_id, updated_at = now()
            WHERE id = p_expense_id;
        END IF;
    END IF;

    IF p_occurred_on IS NOT NULL THEN
        SELECT timezone INTO v_tz FROM public.profiles WHERE id = v_caller;
        BEGIN
            v_today := (now() AT TIME ZONE v_tz)::date;
        EXCEPTION WHEN invalid_parameter_value THEN
            RAISE EXCEPTION 'unknown timezone on profile: %', v_tz
                USING ERRCODE = '23514';
        END;
        IF p_occurred_on > v_today THEN
            RAISE EXCEPTION 'occurred_on % is in the future', p_occurred_on
                USING ERRCODE = '23514';
        END IF;
        IF v_exp.period_id IS NOT NULL
            AND (p_occurred_on < v_starts_on
                 OR (v_ends_on IS NOT NULL AND p_occurred_on > v_ends_on)) THEN
            RAISE EXCEPTION 'occurred_on % is outside the period', p_occurred_on
                USING ERRCODE = '23514';
        END IF;
        UPDATE public.expenses
        SET occurred_on = p_occurred_on, updated_at = now()
        WHERE id = p_expense_id;
    END IF;

    IF p_update_note THEN
        UPDATE public.expenses
        SET note = p_note, updated_at = now()
        WHERE id = p_expense_id;
    END IF;
END;
$function$;

REVOKE ALL ON FUNCTION public.update_expense(uuid, bigint, date, uuid, text, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.update_expense(uuid, bigint, date, uuid, text, boolean) TO authenticated;

-- ---------------------------------------------------------------------------
-- 5. soft_delete_expense — hide everywhere, keep the row (BR-7)
-- ---------------------------------------------------------------------------

CREATE FUNCTION public.soft_delete_expense(p_expense_id uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
DECLARE
    v_caller uuid;
    v_exp public.expenses%ROWTYPE;
BEGIN
    IF p_expense_id IS NULL THEN
        RAISE EXCEPTION 'expense id is required' USING ERRCODE = '23514';
    END IF;
    SELECT public.caller_profile_id() INTO v_caller;
    IF v_caller IS NULL THEN
        RAISE EXCEPTION 'not authenticated or profile is unavailable'
            USING ERRCODE = '42501';
    END IF;

    SELECT * INTO v_exp FROM public.expenses WHERE id = p_expense_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'expense is not available' USING ERRCODE = '42501';
    END IF;

    IF v_exp.period_id IS NOT NULL THEN
        IF v_exp.user_id <> v_caller
            AND NOT public.is_live_period_manager(v_exp.period_id) THEN
            RAISE EXCEPTION 'only the author or the period manager may delete'
                USING ERRCODE = '42501';
        END IF;
    ELSIF v_exp.user_id <> v_caller THEN
        RAISE EXCEPTION 'only the owner may delete a personal expense'
            USING ERRCODE = '42501';
    END IF;

    UPDATE public.expenses
    SET deleted_at = now(), updated_at = now()
    WHERE id = p_expense_id AND deleted_at IS NULL;
END;
$function$;

REVOKE ALL ON FUNCTION public.soft_delete_expense(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.soft_delete_expense(uuid) TO authenticated;

-- ---------------------------------------------------------------------------
-- 6. period_balances — computed per member, never stored
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
-- 7. RLS: splits are readable exactly where the parent expense is readable
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

-- No INSERT/UPDATE/DELETE policies: authenticated writes via RPC only.
REVOKE ALL ON public.expense_splits FROM anon, authenticated;
GRANT SELECT ON public.expense_splits TO authenticated;
