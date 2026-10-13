/**
 * Calendar-month ranges. Months are addressed by (year, month) and returned
 * as explicit inclusive { from, to } date strings — never as a '2026-03'
 * shorthand (ARCHITECTURE.md §5.2). Calendar month boundaries are the same
 * in every zone; the time zone matters when mapping an instant to a date
 * (see zonedToday), not for the length of March.
 */

const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;

/** True iff s is a real calendar date in YYYY-MM-DD form. */
export function isDateString(s: string): boolean {
  if (!DATE_RE.test(s)) return false;
  const [y, m, d] = s.split('-').map(Number) as [number, number, number];
  if (!Number.isSafeInteger(y) || (m as number) < 1 || (m as number) > 12) return false;
  const dt = new Date(Date.UTC(y as number, (m as number) - 1, d as number));
  return (
    dt.getUTCFullYear() === y &&
    dt.getUTCMonth() === (m as number) - 1 &&
    dt.getUTCDate() === d
  );
}

/** Throw unless s is a real YYYY-MM-DD date. */
export function assertDateString(s: string, name = 'date'): void {
  if (!isDateString(s)) {
    throw new RangeError(`${name} must be YYYY-MM-DD, got ${JSON.stringify(s)}`);
  }
}

function assertYearMonth(year: number, month: number): void {
  if (!Number.isSafeInteger(year) || year < 1 || year > 9999) {
    throw new RangeError(`year must be 1..9999, got ${year}`);
  }
  if (!Number.isSafeInteger(month) || month < 1 || month > 12) {
    throw new RangeError(`month must be 1..12, got ${month}`);
  }
}

/** Days in a calendar month (proleptic Gregorian, as used by Postgres `date`). */
export function daysInMonth(year: number, month: number): number {
  assertYearMonth(year, month);
  return new Date(Date.UTC(year, month, 0)).getUTCDate();
}

const pad2 = (n: number): string => String(n).padStart(2, '0');

/** Inclusive [from, to] date range of a calendar month. */
export function monthRange(
  year: number,
  month: number,
): { from: string; to: string } {
  assertYearMonth(year, month);
  return {
    from: `${year}-${pad2(month)}-01`,
    to: `${year}-${pad2(month)}-${daysInMonth(year, month)}`,
  };
}

/** The month range containing the given date. */
export function monthRangeForDate(date: string): { from: string; to: string } {
  assertDateString(date);
  const [y, m] = date.split('-').map(Number) as [number, number];
  return monthRange(y as number, m as number);
}

/** True iff tz is a known IANA zone in this runtime. */
export function isValidTimeZone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/** Throw unless tz is a known IANA zone. */
export function assertTimeZone(tz: string): void {
  if (!isValidTimeZone(tz)) {
    throw new RangeError(`unknown IANA time zone: ${JSON.stringify(tz)}`);
  }
}

/**
 * The calendar date in `timeZone` at instant `now` (defaults to this moment),
 * as YYYY-MM-DD. DST-safe: resolved via Intl in the named zone, never via
 * UTC getters. This is the "today" default for new expenses (FR-3.2) and the
 * basis for month boundaries in reports.
 */
export function zonedToday(timeZone: string, now: Date = new Date()): string {
  assertTimeZone(timeZone);
  // en-CA formats as YYYY-MM-DD.
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now);
}
