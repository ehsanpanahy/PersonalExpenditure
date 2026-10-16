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
});
