/* Client-side smoke: Supabase Auth -> Go API -> RLS-scoped reads.
 * Run: $env:EXPO_PUBLIC_SUPABASE_URL="http://127.0.0.1:54321"
 *      $env:EXPO_PUBLIC_SUPABASE_ANON_KEY="<publishable|anon key from: npx supabase status>"
 *      $env:EXPO_PUBLIC_API_BASE_URL="http://localhost:8082"  # match API_ADDR
 *      $env:SMOKE_EMAIL="smoke@example.test"; $env:SMOKE_PASSWORD="Password123!"
 *      node scripts/client-smoke.mjs
 */
import { createClient } from '@supabase/supabase-js';

const SUPABASE_URL = process.env.EXPO_PUBLIC_SUPABASE_URL;
const ANON_KEY = process.env.EXPO_PUBLIC_SUPABASE_ANON_KEY;
const API_BASE = (process.env.EXPO_PUBLIC_API_BASE_URL || 'http://localhost:8082').replace(/\/+$/, '');
const EMAIL = process.env.SMOKE_EMAIL || 'smoke@example.test';
const PASSWORD = process.env.SMOKE_PASSWORD || 'Password123!';

if (!SUPABASE_URL || !ANON_KEY) {
  console.error('Missing EXPO_PUBLIC_SUPABASE_URL / EXPO_PUBLIC_SUPABASE_ANON_KEY.');
  process.exit(1);
}

const supabase = createClient(SUPABASE_URL, ANON_KEY);

// Sign up, fall back to sign-in when the address already exists.
let session = null;
const signUp = await supabase.auth.signUp({ email: EMAIL, password: PASSWORD });
if (signUp.error && !/already|registered|exists/i.test(signUp.error.message)) {
  console.error('signUp failed:', signUp.error.message);
  process.exit(1);
}
if (signUp.data.session) {
  session = signUp.data.session;
} else {
  const signIn = await supabase.auth.signInWithPassword({ email: EMAIL, password: PASSWORD });
  if (signIn.error) {
    console.error('No session. If signup needs confirmation, open http://127.0.0.1:54324 (Mailpit), confirm, then re-run.');
    console.error('signIn failed:', signIn.error.message);
    process.exit(1);
  }
  session = signIn.data.session;
}
console.log('auth: ok as', session.user.email);

const apiFetch = async (path) => {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { Authorization: `Bearer ${session.access_token}`, Accept: 'application/json' },
  });
  const body = await res.text();
  console.log(`${path} -> ${res.status} ${body.slice(0, 300)}`);
  if (!res.ok) process.exit(1);
  return JSON.parse(body);
};

await apiFetch('/healthz');
await apiFetch('/v1/me');
const expenses = await apiFetch('/v1/expenses?from=2026-03-01&to=2026-03-31&limit=5&offset=0');
const bad = expenses.filter((e) => typeof e.amount_minor !== 'string');
if (bad.length > 0) {
  console.error('FAIL: amount_minor must stay a string (bigint precision).');
  process.exit(1);
}
console.log(`expenses: ok (${expenses.length} rows, amount_minor is string)`);
