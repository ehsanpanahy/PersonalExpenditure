/**
 * Deterministic split of an integer minor-unit amount across participants.
 *
 * Per ARCHITECTURE.md §5.4: base = floor(amount / n) each, then the
 * `amount - base * n` leftover minor units go out one each in a stable
 * deterministic order. For the positional form the head of the array gets
 * the extras; use {@link allocateToMembers} to bind that order to sorted
 * member ids so every client and the server agree.
 */

/** One member's frozen share of an expense. */
export interface MemberShare {
  userId: string;
  amountMinor: number;
}

function assertAmountMinor(amountMinor: number): void {
  if (!Number.isSafeInteger(amountMinor) || amountMinor < 0) {
    throw new RangeError(
      `amountMinor must be a safe non-negative integer, got ${amountMinor}`,
    );
  }
}

function assertCount(n: number): void {
  if (!Number.isSafeInteger(n) || n < 1) {
    throw new RangeError(`n must be a safe integer >= 1, got ${n}`);
  }
}

/**
 * Split `amountMinor` into `n` integer shares that sum exactly to the total.
 * The first `amountMinor - base * n` entries each get one extra minor unit.
 */
export function allocate(amountMinor: number, n: number): number[] {
  assertAmountMinor(amountMinor);
  assertCount(n);
  const base = Math.floor(amountMinor / n);
  const remainder = amountMinor - base * n;
  const out = new Array<number>(n);
  for (let i = 0; i < n; i++) {
    out[i] = base + (i < remainder ? 1 : 0);
  }
  return out;
}

/**
 * Split `amountMinor` across members, deterministically: ids are sorted with
 * plain string comparison (never locale-dependent) and the sorted head
 * receives the leftover minor units. Returns shares in sorted-id order.
 */
export function allocateToMembers(
  amountMinor: number,
  memberIds: readonly string[],
): MemberShare[] {
  assertAmountMinor(amountMinor);
  if (memberIds.length === 0) {
    throw new RangeError('memberIds must contain at least one member');
  }
  const sorted = [...memberIds].sort();
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i] === sorted[i - 1]) {
      throw new RangeError(`duplicate member id: ${sorted[i]}`);
    }
  }
  const shares = allocate(amountMinor, sorted.length);
  return sorted.map((userId, i) => ({ userId, amountMinor: shares[i] as number }));
}
