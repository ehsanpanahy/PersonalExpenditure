/**
 * Frozen split shares (ARCHITECTURE.md §5.2, §5.4; MODULE_VIEW.md §L3).
 *
 * Pure TypeScript: no React, no Expo, no Supabase. The preview runs locally
 * so typing feels instant; the server re-validates everything in
 * `create_expense()` and rejects mismatches instead of correcting them.
 *
 * Rounding conformance: the cases in `./conformance.json` mirror the SQL
 * assertions in `supabase/tests/golden_dataset_test.sql` (§C). Keep them in
 * sync — drift between the preview and the server is a wrong-number bug.
 */
import { allocateToMembers, type MemberShare } from '../money/allocate';

export type { MemberShare };

/**
 * Freeze an even split: deterministic shares (sorted ids, sorted head gets
 * the leftover minor units) that sum exactly to `amountMinor`.
 */
export function freezeShares(
  amountMinor: number,
  participantIds: readonly string[],
): MemberShare[] {
  return allocateToMembers(amountMinor, participantIds);
}

/**
 * Validate hand-typed exact shares (FR-5.4/5.6/5.7).
 * Returns `null` when valid, otherwise a human-readable reason.
 * Rules: non-empty, unique members, each share a positive safe integer,
 * and the shares total exactly `amountMinor` — never adjusted silently.
 */
export function validateExactShares(
  amountMinor: number,
  shares: readonly { userId: string; amountMinor: number }[],
): string | null {
  if (!Number.isSafeInteger(amountMinor) || amountMinor <= 0) {
    return `total must be a positive integer of minor units, got ${amountMinor}`;
  }
  if (shares.length === 0) {
    return 'at least one share is required';
  }
  const seen = new Set<string>();
  let sum = 0;
  for (const share of shares) {
    if (typeof share.userId !== 'string' || share.userId === '') {
      return 'every share needs a member id';
    }
    if (seen.has(share.userId)) {
      return `duplicate participant: ${share.userId}`;
    }
    seen.add(share.userId);
    if (!Number.isSafeInteger(share.amountMinor) || share.amountMinor <= 0) {
      return `share for ${share.userId} must be greater than zero`;
    }
    sum += share.amountMinor;
  }
  if (sum !== amountMinor) {
    return `shares total ${sum} but the expense total is ${amountMinor}: must match exactly`;
  }
  return null;
}
