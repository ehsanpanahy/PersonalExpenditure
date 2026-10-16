import { ApiError, createApiClient } from './client';

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}

describe('api client (Go API boundary)', () => {
  it('sends the Supabase access token as a bearer token with explicit from/to', async () => {
    const seen: string[] = [];
    const fetchImpl = (async (url: unknown, init?: { headers?: Record<string, string> }) => {
      seen.push(String(url), init?.headers?.['Authorization'] ?? '');
      return jsonResponse([]);
    }) as unknown as typeof fetch;

    const api = createApiClient({
      baseUrl: 'http://localhost:8080/',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    await api.listPersonalExpenses({ from: '2026-03-01', to: '2026-03-31' });

    expect(seen[0]).toContain('/v1/expenses?from=2026-03-01&to=2026-03-31');
    expect(seen[1]).toBe('Bearer test-token');
  });

  it('keeps amount_minor as a string to preserve bigint precision', async () => {
    const fetchImpl = (async () =>
      jsonResponse([
        {
          id: '40000000-0000-0000-0000-000000000001',
          amount_minor: '9007199254740993',
          currency: 'USD',
          occurred_on: '2026-03-01',
          category_id: null,
          note: null,
          created_at: '2026-03-01T00:00:00Z',
        },
      ])) as unknown as typeof fetch;

    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    const rows = await api.listPersonalExpenses({ from: '2026-03-01', to: '2026-03-31' });
    expect(rows[0]?.amount_minor).toBe('9007199254740993');
  });

  it('requires a session instead of sending an unauthenticated request', async () => {
    let called = false;
    const fetchImpl = (async () => {
      called = true;
      return jsonResponse([]);
    }) as unknown as typeof fetch;

    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => null,
      fetchImpl,
    });
    await expect(
      api.listPersonalExpenses({ from: '2026-03-01', to: '2026-03-31' }),
    ).rejects.toMatchObject({ status: 401 });
    expect(called).toBe(false);
  });

  it('rejects bad dates and pagination client-side (server validates too, BR-10)', async () => {
    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl: (async () => jsonResponse([])) as unknown as typeof fetch,
    });
    await expect(api.listPersonalExpenses({ from: '2026-3-1', to: '2026-03-31' })).rejects.toThrow(
      RangeError,
    );
    await expect(api.listPersonalExpenses({ from: '2026-04-01', to: '2026-03-31' })).rejects.toThrow(
      RangeError,
    );
    await expect(
      api.listPersonalExpenses({ from: '2026-03-01', to: '2026-03-31', limit: 101 }),
    ).rejects.toThrow(RangeError);
  });

  it('rejects a numeric amount_minor that lost bigint precision', async () => {
    const fetchImpl = (async () =>
      jsonResponse([{ id: 'x', amount_minor: 1250 }])) as unknown as typeof fetch;
    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    await expect(
      api.listPersonalExpenses({ from: '2026-03-01', to: '2026-03-31' }),
    ).rejects.toBeInstanceOf(ApiError);
  });

  it('posts a personal expense with string amounts and the bearer token', async () => {
    const seen: { url: string; auth: string; body: unknown }[] = [];
    const fetchImpl = (async (url: unknown, init?: { headers?: Record<string, string>; body?: string }) => {
      seen.push({
        url: String(url),
        auth: init?.headers?.['Authorization'] ?? '',
        body: init?.body !== undefined ? (JSON.parse(init.body) as unknown) : undefined,
      });
      return jsonResponse({ id: '40000000-0000-0000-0000-000000000001' }, 201);
    }) as unknown as typeof fetch;

    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    const result = await api.createExpense({
      id: '40000000-0000-0000-0000-000000000001',
      category_id: '20000000-0000-0000-0000-000000000001',
      paid_by: '11111111-1111-1111-1111-111111111111',
      amount_minor: '9007199254740993',
      currency: 'USD',
      occurred_on: '2026-03-14',
      note: 'groceries',
      splits: [{ user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '9007199254740993' }],
    });

    expect(result).toEqual({ id: '40000000-0000-0000-0000-000000000001' });
    expect(seen).toHaveLength(1);
    expect(seen[0]?.url).toBe('http://localhost:8080/v1/expenses');
    expect(seen[0]?.auth).toBe('Bearer test-token');
    // bigint crosses as an exact decimal string, never a lossy JSON number.
    expect(seen[0]?.body).toMatchObject({
      amount_minor: '9007199254740993',
      currency: 'USD',
      period_id: null,
    });
  });

  it('rejects bad create payloads client-side without sending', async () => {
    let called = false;
    const fetchImpl = (async () => {
      called = true;
      return jsonResponse({ id: '40000000-0000-0000-0000-000000000001' }, 201);
    }) as unknown as typeof fetch;
    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    const base = {
      id: '40000000-0000-0000-0000-000000000001',
      paid_by: '11111111-1111-1111-1111-111111111111',
      amount_minor: '1250',
      currency: 'USD',
      occurred_on: '2026-03-14',
      splits: [{ user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '1250' }],
    };
    await expect(api.createExpense({ ...base, id: 'not-a-uuid' })).rejects.toThrow(RangeError);
    await expect(
      api.createExpense({ ...base, amount_minor: '12.50' }),
    ).rejects.toThrow(RangeError);
    await expect(
      api.createExpense({
        ...base,
        amount_minor: '1000',
        splits: [{ user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '999' }],
      }),
    ).rejects.toThrow(/must match exactly/);
    await expect(
      api.createExpense({
        ...base,
        splits: [
          { user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '625' },
          { user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '625' },
        ],
      }),
    ).rejects.toThrow(/must not repeat/);
    expect(called).toBe(false);
  });

  it('surfaces server validation failures with their code', async () => {
    const fetchImpl = (async () =>
      jsonResponse({ error: { code: 'invalid_expense', message: 'splits total 999' } }, 400)) as unknown as typeof fetch;
    const api = createApiClient({
      baseUrl: 'http://localhost:8080',
      getAccessToken: async () => 'test-token',
      fetchImpl,
    });
    await expect(
      api.createExpense({
        id: '40000000-0000-0000-0000-000000000001',
        paid_by: '11111111-1111-1111-1111-111111111111',
        amount_minor: '1000',
        currency: 'USD',
        occurred_on: '2026-03-14',
        splits: [
          { user_id: '11111111-1111-1111-1111-111111111111', amount_minor: '500' },
          { user_id: '22222222-2222-2222-2222-222222222222', amount_minor: '500' },
        ],
      }),
    ).rejects.toMatchObject({ status: 400, code: 'invalid_expense' });
  });
});
