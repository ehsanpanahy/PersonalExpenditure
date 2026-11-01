-- Expense workflows and authoritative request validation live in Go. Remove
-- the former SECURITY DEFINER application workflows from databases that ran
-- the older migrations; fresh databases no longer create these functions.
DROP FUNCTION IF EXISTS public.create_expense(
    uuid, uuid, uuid, uuid, bigint, char(3), date, text, jsonb
);
DROP FUNCTION IF EXISTS public.update_expense(
    uuid, bigint, date, uuid, text, boolean
);
DROP FUNCTION IF EXISTS public.soft_delete_expense(uuid);

-- These helpers existed only to support the retired SQL workflows.
DROP FUNCTION IF EXISTS public.is_live_member_of(uuid, uuid);
DROP FUNCTION IF EXISTS public.is_live_period_manager(uuid);
