/**
 * Client-side mirrors of the server date rules (ARCHITECTURE.md §5.2,
 * BR-5, FR-3.2, FR-4.5) — for UX only. The RPC re-validates everything.
 */
import { assertDateString } from './month';

/**
 * True iff occurredOn falls within [startsOn, endsOn], inclusive at both
 * ends. A null endsOn means ongoing (no upper bound). Rejects — never clamps.
 */
export function isWithinPeriod(
  occurredOn: string,
  startsOn: string,
  endsOn: string | null,
): boolean {
  assertDateString(occurredOn, 'occurredOn');
  assertDateString(startsOn, 'startsOn');
  if (endsOn !== null) assertDateString(endsOn, 'endsOn');
  if (occurredOn < startsOn) return false;
  if (endsOn !== null && occurredOn > endsOn) return false;
  return true;
}

/** True iff date is strictly after today (both YYYY-MM-DD). */
export function isFutureDate(date: string, today: string): boolean {
  assertDateString(date);
  assertDateString(today, 'today');
  return date > today;
}
