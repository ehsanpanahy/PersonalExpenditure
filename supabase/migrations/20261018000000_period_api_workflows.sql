-- Period application workflows and request validation run in Go under one
-- app_api transaction. Postgres retains a narrowly scoped account lookup,
-- RLS helpers, and a deferred integrity check.

ALTER TABLE public.periods
    ADD COLUMN roster_finalized boolean NOT NULL DEFAULT true;

CREATE OR REPLACE FUNCTION public.resolve_period_member_emails(p_emails text[])
RETURNS TABLE (email text, profile_id uuid)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public, auth
AS $function$
BEGIN
    -- This is a database access safeguard, not request validation. Go validates
    -- the supplied list before calling this helper; the helper only returns
    -- explicitly requested, confirmed accounts to the caller-scoped API role.
    IF auth.uid() IS NULL OR NOT EXISTS (
        SELECT 1 FROM public.profiles AS caller
        WHERE caller.auth_user_id = auth.uid() AND caller.deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'authenticated caller required' USING ERRCODE = '42501';
    END IF;
    RETURN QUERY
    SELECT requested.value, profile.id
      FROM unnest(p_emails) AS requested(value)
      JOIN auth.users AS auth_user ON lower(auth_user.email) = lower(requested.value)
      JOIN public.profiles AS profile ON profile.auth_user_id = auth_user.id
     WHERE profile.deleted_at IS NULL
       AND auth_user.email_confirmed_at IS NOT NULL;
END;
$function$;

CREATE OR REPLACE FUNCTION public.period_profile_is_live(p_profile_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
    SELECT EXISTS (
        SELECT 1 FROM public.profiles
        WHERE id = p_profile_id AND deleted_at IS NULL
    );
$function$;

CREATE OR REPLACE FUNCTION public.is_caller_period_manager(p_period_id uuid)
RETURNS boolean
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
    SELECT EXISTS (
        SELECT 1 FROM public.periods
        WHERE id = p_period_id
          AND manager_id = public.caller_profile_id()
          AND roster_finalized = false
          AND deleted_at IS NULL
    );
$function$;

REVOKE ALL ON FUNCTION public.resolve_period_member_emails(text[]) FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.resolve_period_member_emails(text[]) TO app_api;
REVOKE ALL ON FUNCTION public.period_profile_is_live(uuid) FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.period_profile_is_live(uuid) TO app_api;
REVOKE ALL ON FUNCTION public.is_caller_period_manager(uuid) FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.is_caller_period_manager(uuid) TO app_api;

GRANT INSERT (name, manager_id, starts_on, ends_on, currency, roster_finalized),
    UPDATE (manager_id, deleted_at, updated_at, roster_finalized) ON public.periods TO app_api;
GRANT INSERT (period_id, user_id, role), UPDATE (role)
    ON public.period_members TO app_api;

CREATE POLICY periods_insert_by_creator ON public.periods
    FOR INSERT TO app_api
    WITH CHECK (manager_id = public.caller_profile_id() AND NOT roster_finalized);

CREATE POLICY periods_select_creator_during_creation ON public.periods
    FOR SELECT TO app_api
    USING (
        manager_id = public.caller_profile_id()
        AND NOT roster_finalized
        AND deleted_at IS NULL
    );

CREATE POLICY periods_update_by_manager ON public.periods
    FOR UPDATE TO app_api
    USING (manager_id = public.caller_profile_id() AND deleted_at IS NULL)
    WITH CHECK (public.period_profile_is_live(manager_id) AND roster_finalized);

CREATE POLICY period_members_insert_by_manager ON public.period_members
    FOR INSERT TO app_api
    WITH CHECK (
        public.period_profile_is_live(user_id)
        AND public.is_caller_period_manager(period_id)
        AND (
            (user_id = public.caller_profile_id() AND role = 'manager')
            OR (user_id <> public.caller_profile_id() AND role = 'member')
        )
    );

CREATE POLICY period_members_update_by_manager ON public.period_members
    FOR UPDATE TO app_api
    USING (EXISTS (
        SELECT 1 FROM public.periods AS period
        WHERE period.id = period_id
          AND period.manager_id = public.caller_profile_id()
          AND period.deleted_at IS NULL
    ))
    WITH CHECK (role IN ('manager', 'member') AND public.period_profile_is_live(user_id));

CREATE FUNCTION public.assert_period_manager_consistency()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $function$
DECLARE
    v_period_id uuid;
    v_manager_id uuid;
    v_deleted_at timestamptz;
    v_roster_finalized boolean;
    v_manager_count bigint;
    v_role_manager_id text;
BEGIN
    IF TG_TABLE_NAME = 'periods' THEN
        v_period_id := NEW.id;
    ELSIF TG_OP = 'DELETE' THEN
        v_period_id := OLD.period_id;
    ELSE
        v_period_id := NEW.period_id;
    END IF;

    SELECT manager_id, deleted_at, roster_finalized
      INTO v_manager_id, v_deleted_at, v_roster_finalized
      FROM public.periods WHERE id = v_period_id;
    IF NOT FOUND OR v_deleted_at IS NOT NULL THEN
        RETURN NULL;
    END IF;
    IF NOT v_roster_finalized THEN
        RAISE EXCEPTION 'live period roster must be finalized before commit'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*) FILTER (WHERE role = 'manager'),
           min(user_id::text) FILTER (WHERE role = 'manager')
      INTO v_manager_count, v_role_manager_id
      FROM public.period_members WHERE period_id = v_period_id;
    IF v_manager_count <> 1 OR v_role_manager_id IS DISTINCT FROM v_manager_id::text THEN
        RAISE EXCEPTION 'live period must have exactly one matching manager'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$function$;

REVOKE ALL ON FUNCTION public.assert_period_manager_consistency() FROM PUBLIC, anon, authenticated, app_api;

CREATE CONSTRAINT TRIGGER periods_manager_consistency_check
    AFTER INSERT OR UPDATE OF manager_id, deleted_at ON public.periods
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.assert_period_manager_consistency();

CREATE CONSTRAINT TRIGGER period_members_manager_consistency_check
    AFTER INSERT OR UPDATE OR DELETE ON public.period_members
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION public.assert_period_manager_consistency();
