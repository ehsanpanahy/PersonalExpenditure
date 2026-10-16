-- Go owns expense-create workflows. This migration grants its restricted role
-- the minimum write path while keeping Postgres RLS and an exact split-sum
-- constraint as integrity safeguards. The mobile-facing authenticated role
-- remains read-only for expenses and splits.

CREATE ROLE app_api NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
GRANT authenticated TO app_api;
CREATE ROLE app_api_login NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
GRANT app_api TO app_api_login;

-- Existing RLS SELECT policies target authenticated. Membership allows the
-- restricted API role to inherit those policies and read only caller-visible
-- rows after it installs the verified JWT claims in its transaction.
GRANT SELECT ON public.profiles, public.categories, public.periods,
    public.period_members, public.expenses, public.expense_splits TO app_api;

REVOKE INSERT, UPDATE, DELETE ON public.expenses, public.expense_splits
    FROM anon, authenticated;
GRANT INSERT (id, user_id, period_id, category_id, paid_by, amount_minor,
    currency, occurred_on, note) ON public.expenses TO app_api;
GRANT INSERT (expense_id, user_id, amount_minor)
    ON public.expense_splits TO app_api;

CREATE POLICY expenses_insert_via_go_api ON public.expenses
    FOR INSERT TO app_api
    WITH CHECK (
        (
            period_id IS NULL
            AND user_id = (
                SELECT profile.id FROM public.profiles AS profile
                WHERE profile.auth_user_id = (SELECT auth.uid())
                  AND profile.deleted_at IS NULL
            )
        )
        OR (period_id IS NOT NULL AND public.is_live_period_member(period_id))
    );

CREATE POLICY expense_splits_insert_via_go_api ON public.expense_splits
    FOR INSERT TO app_api
    WITH CHECK (
        EXISTS (
            SELECT 1 FROM public.expenses AS expense
            WHERE expense.id = expense_splits.expense_id
              AND expense.user_id = (
                  SELECT profile.id FROM public.profiles AS profile
                  WHERE profile.auth_user_id = (SELECT auth.uid())
                    AND profile.deleted_at IS NULL
              )
        )
    );

-- A split set is valid only as a whole. Deferred triggers see the final
-- transaction state, allowing Go to insert the expense and all shares in one
-- transaction while Postgres rejects missing or mismatched shares at commit.
CREATE FUNCTION public.assert_expense_split_total()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $function$
DECLARE
    v_expense_id uuid;
    v_amount bigint;
    v_count bigint;
    v_total numeric;
BEGIN
    IF TG_TABLE_NAME = 'expenses' THEN
        v_expense_id := NEW.id;
    ELSE
        v_expense_id := NEW.expense_id;
    END IF;

    SELECT expense.amount_minor INTO v_amount
    FROM public.expenses AS expense
    WHERE expense.id = v_expense_id;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    SELECT count(*), COALESCE(sum(split.amount_minor), 0)
      INTO v_count, v_total
    FROM public.expense_splits AS split
    WHERE split.expense_id = v_expense_id;

    IF v_count < 1 OR v_total <> v_amount THEN
        RAISE EXCEPTION 'expense splits must total exactly the expense amount'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$function$;

CREATE CONSTRAINT TRIGGER expenses_split_total_check
    AFTER INSERT OR UPDATE OF amount_minor ON public.expenses
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.assert_expense_split_total();

CREATE CONSTRAINT TRIGGER expense_splits_total_check
    AFTER INSERT ON public.expense_splits
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.assert_expense_split_total();

REVOKE ALL ON FUNCTION public.assert_expense_split_total() FROM PUBLIC;

-- Keep the old SECURITY DEFINER functions available only to the restricted
-- server role while the pgTAP suite still uses them as a SQL conformance
-- harness. The mobile authenticated role cannot invoke these alternate write
-- workflows; production HTTP writes use Go's transaction implementation.
REVOKE ALL ON FUNCTION public.create_expense(uuid, uuid, uuid, uuid, bigint, char(3), date, text, jsonb)
    FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.create_expense(uuid, uuid, uuid, uuid, bigint, char(3), date, text, jsonb)
    TO app_api;
REVOKE ALL ON FUNCTION public.update_expense(uuid, bigint, date, uuid, text, boolean)
    FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.update_expense(uuid, bigint, date, uuid, text, boolean)
    TO app_api;
REVOKE ALL ON FUNCTION public.soft_delete_expense(uuid)
    FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.soft_delete_expense(uuid) TO app_api;

REVOKE ALL ON FUNCTION public.caller_profile_id() FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION public.is_live_member_of(uuid, uuid) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION public.is_live_period_manager(uuid) FROM PUBLIC, anon, authenticated;
