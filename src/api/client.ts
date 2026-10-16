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
import type { ListPersonalExpensesParams, PersonalExpense } from './types';

export const DEFAULT_PAGE_SIZE = 50;
export const MAX_PAGE_SIZE = 100;
export const MAX_OFFSET = 10000;

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

function assertDate(value: string, name: 'from' | 'to'): void {
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
}

export function createApiClient(config: ApiClientConfig): ApiClient {
  const baseUrl = config.baseUrl.replace(/\/+$/, '');
  const fetchImpl = config.fetchImpl ?? fetch;

  async function authorized(path: string): Promise<unknown> {
    const token = await config.getAccessToken();
    if (!token) {
      throw new ApiError(401, 'unauthorized', 'Sign in to load expenses.');
    }
    let res: Response;
    try {
      res = await fetchImpl(`${baseUrl}${path}`, {
        headers: { Authorization: `Bearer ${token}`, Accept: 'application/json' },
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
  };
}
