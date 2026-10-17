-- Category mutations are owned by the Go API, like the other application writes.
-- Keep authenticated clients read-only and let the restricted app_api role
-- write only rows visible to the verified caller under RLS.
REVOKE INSERT, UPDATE, DELETE ON public.categories FROM anon, authenticated;
GRANT SELECT, INSERT, UPDATE ON public.categories TO app_api;
GRANT EXECUTE ON FUNCTION public.caller_profile_id() TO app_api;

CREATE POLICY categories_insert_via_go_api ON public.categories
    FOR INSERT TO app_api
    WITH CHECK (user_id = public.caller_profile_id());

CREATE POLICY categories_update_via_go_api ON public.categories
    FOR UPDATE TO app_api
    USING (user_id = public.caller_profile_id())
    WITH CHECK (user_id = public.caller_profile_id());

-- Category history is retained: deletion is intentionally unavailable.
REVOKE DELETE ON public.categories FROM app_api;
