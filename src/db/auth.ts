/**
 * Auth-only Supabase client.
 *
 * ARCHITECTURE.md §2.5 / §6: the mobile app uses Supabase Auth ONLY for
 * signup, verification, sign-in, recovery, and session refresh.
 * All application data goes through the Go API (`src/api/client.ts`),
 * which forwards the caller's JWT so Postgres RLS applies.
 *
 * This module must never query tables, views, or RPCs. It must never hold
 * a service-role key (AGENTS.md rule 3).
 */
import { createClient, type SupabaseClient } from '@supabase/supabase-js';

export interface AuthStorageAdapter {
  getItem(key: string): Promise<string | null> | string | null;
  setItem(key: string, value: string): Promise<void> | void;
  removeItem(key: string): Promise<void> | void;
}

function createMemoryStorage(): AuthStorageAdapter {
  const store = new Map<string, string>();
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => {
      store.set(key, value);
    },
    removeItem: (key: string) => {
      store.delete(key);
    },
  };
}

export interface AuthClientConfig {
  supabaseUrl: string;
  supabaseAnonKey: string;
  /**
   * Session persistence. Defaults to in-memory (works in Jest/Node).
   * In Expo, inject an adapter backed by SecureStore so refresh tokens
   * survive app restarts (REQUIREMENTS.md FR-1.4).
   */
  storage?: AuthStorageAdapter;
}

export function createAuthClient(config: AuthClientConfig): SupabaseClient {
  if (!config.supabaseUrl) {
    throw new Error('supabaseUrl is required (set EXPO_PUBLIC_SUPABASE_URL).');
  }
  if (!config.supabaseAnonKey) {
    throw new Error('supabaseAnonKey is required (publishable/anon key only).');
  }
  return createClient(config.supabaseUrl, config.supabaseAnonKey, {
    auth: {
      storage: config.storage ?? createMemoryStorage(),
      autoRefreshToken: true,
      persistSession: true,
      detectSessionInUrl: false,
    },
  });
}

/** Current access token, or null when signed out. Data clients use this as the bearer token. */
export async function getAccessToken(client: SupabaseClient): Promise<string | null> {
  const { data, error } = await client.auth.getSession();
  if (error) {
    throw error;
  }
  return data.session?.access_token ?? null;
}
