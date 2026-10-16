/**
 * Typed Go API client (ARCHITECTURE.md §2.5, §6).
 *
 * The ONLY data path for the mobile app. Never talks to PostgREST or RPCs
 * directly — it sends the Supabase access token as a bearer token and the
 * Go API forwards that same JWT so Postgres RLS applies.
 *
 * Month bounds are the caller's job: compute them in the user's IANA zone
 * (via `src/shared/dates`) and pass explicit `from`/`to` — never 'YYYY-MM'.
 * `amount_minor` stays a string end-to-end to preserve bigint precision.
 */
import type {
  CreateExpenseInput,
  CreateExpenseResult,
  ListPersonalExpensesParams,
  PersonalExpense,
} from './types';

export const DEFAULT_PAGE_SIZE = 50;
export const MAX_PAGE_SIZE = 100;
export const MAX_OFFSET = 10000;
export const MAX_SPLITS = 200;
/** Postgres bigint range; amounts are strings at this boundary, validated before send. */
export const MAX_AMOUNT_MINOR = 9223372036854775807n;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const AMOUNT_RE = /^[0-9]+$/;
const CURRENCY_RE = /^[A-Z]{3}$/;

function assertUuid(value: unknown, name: string): asserts value is string {
  if (typeof value !== 'string' || !UUID_RE.test(value)) {
    throw new RangeError(`${name} must be a UUID`);
  }
}

function assertAmountString(value: unknown, name: string): asserts value is string {
  if (typeof value !== 'string' || !AMOUNT_RE.test(value)) {
    throw new RangeError(`${name} must be a digits-only integer string`);
  }
  const parsed = BigInt(value);
  if (parsed <= 0n || parsed > MAX_AMOUNT_MINOR) {
    throw new RangeError(`${name} must be greater than zero and fit in bigint range`);
  }
}

function assertDate(value: string, name: string): void {
  if (!DATE_RE.test(value)) {
    throw new RangeError(`${name} must be a valid YYYY-MM-DD date`);
  }
  const [y, m, d] = value.split('-').map(Number);
  const dt = new Date(Date.UTC(y as number, (m as number) - 1, d as number));
  if (
    dt.getUTCFullYear() !== y ||
    dt.getUTCMonth() !== (m as number) - 1 ||
    dt.getUTCDate() !== d
  ) {
    throw new RangeError(`${name} must be a valid YYYY-MM-DD date`);
  }
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

export interface ApiClientConfig {
  baseUrl: string;
  /** Resolves the current Supabase access token (from `src/db/auth.ts`). */
  getAccessToken: () => Promise<string | null>;
  fetchImpl?: typeof fetch;
}

export interface ApiClient {
  getCurrentUser(): Promise<{ user_id: string }>;
  listPersonalExpenses(params: ListPersonalExpensesParams): Promise<PersonalExpense[]>;
  /**
   * Record an expense. The id is client-generated: retrying with the same id
   * and identical payload confirms the same expense (the server returns 201
   * with that id). Client-side checks mirror the server for fast feedback;
   * the SQL RPC re-validates everything (BR-10).
   */
  createExpense(input: CreateExpenseInput): Promise<CreateExpenseResult>;
}

export function createApiClient(config: ApiClientConfig): ApiClient {
  const baseUrl = config.baseUrl.replace(/\/+$/, '');
  const fetchImpl = config.fetchImpl ?? fetch;

  async function authorized(path: string, init?: { method?: string; body?: unknown }): Promise<unknown> {
    const token = await config.getAccessToken();
    if (!token) {
      throw new ApiError(401, 'unauthorized', 'Sign in to load expenses.');
    }
    let res: Response;
    try {
      res = await fetchImpl(`${baseUrl}${path}`, {
        method: init?.method ?? 'GET',
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'application/json',
          ...(init?.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        },
        ...(init?.body !== undefined ? { body: JSON.stringify(init.body) } : {}),
      });
    } catch {
      throw new ApiError(0, 'network_unavailable', "You're offline — reconnect to load data.");
    }
    if (res.ok) {
      return (await res.json()) as unknown;
    }
    let code = 'request_failed';
    let message = `Request failed (${res.status}).`;
    try {
      const body = (await res.json()) as { error?: { code?: string; message?: string } };
      if (body.error?.code) {
        code = body.error.code;
      }
      if (body.error?.message) {
        message = body.error.message;
      }
    } catch {
      // Keep the default message when the body is not JSON.
    }
    throw new ApiError(res.status, code, message);
  }

  return {
    async getCurrentUser(): Promise<{ user_id: string }> {
      const body = (await authorized('/v1/me')) as { user_id?: unknown };
      if (typeof body.user_id !== 'string' || body.user_id === '') {
        throw new ApiError(502, 'data_provider_error', 'Invalid identity response.');
      }
      return { user_id: body.user_id };
    },

    async listPersonalExpenses(params: ListPersonalExpensesParams): Promise<PersonalExpense[]> {
      assertDate(params.from, 'from');
      assertDate(params.to, 'to');
      if (params.from > params.to) {
        throw new RangeError('from must be on or before to');
      }
      const limit = params.limit ?? DEFAULT_PAGE_SIZE;
      const offset = params.offset ?? 0;
      if (!Number.isInteger(limit) || limit < 1 || limit > MAX_PAGE_SIZE) {
        throw new RangeError(`limit must be between 1 and ${MAX_PAGE_SIZE}`);
      }
      if (!Number.isInteger(offset) || offset < 0 || offset > MAX_OFFSET) {
        throw new RangeError(`offset must be between 0 and ${MAX_OFFSET}`);
      }
      const query =
        `from=${encodeURIComponent(params.from)}` +
        `&to=${encodeURIComponent(params.to)}` +
        `&limit=${limit}&offset=${offset}`;
      const body = (await authorized(`/v1/expenses?${query}`)) as unknown;
      if (!Array.isArray(body)) {
        throw new ApiError(502, 'data_provider_error', 'Invalid expenses response.');
      }
      for (const row of body) {
        const expense = row as Partial<PersonalExpense>;
        if (
          typeof expense.id !== 'string' ||
          typeof expense.amount_minor !== 'string' ||
          !/^[0-9]+$/.test(expense.amount_minor)
        ) {
          throw new ApiError(502, 'data_provider_error', 'Invalid expense record.');
        }
      }
      return body as PersonalExpense[];
    },

    async createExpense(input: CreateExpenseInput): Promise<CreateExpenseResult> {
      assertUuid(input.id, 'id');
      assertUuid(input.paid_by, 'paid_by');
      if (input.period_id !== undefined && input.period_id !== null) {
        assertUuid(input.period_id, 'period_id');
      }
      if (input.category_id !== undefined && input.category_id !== null) {
        assertUuid(input.category_id, 'category_id');
      }
      assertAmountString(input.amount_minor, 'amount_minor');
      if (!CURRENCY_RE.test(input.currency)) {
        throw new RangeError('currency must be ISO-4217 (e.g. USD)');
      }
      assertDate(input.occurred_on, 'occurred_on');
      if (!Array.isArray(input.splits) || input.splits.length < 1 || input.splits.length > MAX_SPLITS) {
        throw new RangeError(`splits must contain between 1 and ${MAX_SPLITS} entries`);
      }
      const seen = new Set<string>();
      let sum = 0n;
      const total = BigInt(input.amount_minor);
      for (const split of input.splits) {
        assertUuid(split.user_id, 'splits[].user_id');
        assertAmountString(split.amount_minor, 'splits[].amount_minor');
        if (seen.has(split.user_id)) {
          throw new RangeError('splits must not repeat a participant');
        }
        seen.add(split.user_id);
        sum += BigInt(split.amount_minor);
      }
      if (sum !== total) {
        throw new RangeError(
          `splits total ${sum} but the expense total is ${total}: must match exactly`,
        );
      }
      const body = (await authorized('/v1/expenses', {
        method: 'POST',
        body: {
          id: input.id,
          period_id: input.period_id ?? null,
          category_id: input.category_id ?? null,
          paid_by: input.paid_by,
          amount_minor: input.amount_minor,
          currency: input.currency,
          occurred_on: input.occurred_on,
          note: input.note ?? null,
          splits: input.splits,
        },
      })) as { id?: unknown };
      if (typeof body.id !== 'string' || !UUID_RE.test(body.id)) {
        throw new ApiError(502, 'data_provider_error', 'Invalid create-expense response.');
      }
      return { id: body.id };
    },
  };
}
