/**
 * Minor-unit parse/format. No floats cross this boundary: parsing is done
 * with string manipulation so `parseAmount(formatAmount(m))` round-trips.
 * v1 amounts are non-negative (refunds out of scope, ARCHITECTURE.md §3).
 */

const ISO_CURRENCY_RE = /^[A-Z]{3}$/;

function assertCurrency(currency: string): void {
  if (!ISO_CURRENCY_RE.test(currency)) {
    throw new RangeError(`currency must be ISO-4217 (e.g. USD), got ${currency}`);
  }
}

/** Minor-unit digits for a currency, from the runtime's CLDR data. */
export function minorDigits(currency: string, locale = 'en-US'): number {
  assertCurrency(currency);
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
  }).resolvedOptions().maximumFractionDigits ?? 2;
}

/**
 * Parse a user-typed decimal string (e.g. "1,234.56", "10", "10.5") into
 * integer minor units. Rejects negatives, empty input, and precision beyond
 * the currency's minor digits — never rounds silently.
 */
export function parseAmount(
  input: string,
  currency = 'USD',
  locale = 'en-US',
): number {
  assertCurrency(currency);
  const digits = minorDigits(currency, locale);
  const compact = input.trim().replace(/[\s,]/g, '');
  const match = /^(\d+)(?:\.(\d+))?$/.exec(compact);
  if (!match) {
    throw new RangeError(`invalid amount: ${JSON.stringify(input)}`);
  }
  const [, majorPart, fracPart = ''] = match;
  if (fracPart.length > digits) {
    throw new RangeError(
      `too many decimal places for ${currency}: ${JSON.stringify(input)}`,
    );
  }
  // Zero-decimal currencies must not carry a fraction at all.
  if (digits === 0 && fracPart.length > 0) {
    throw new RangeError(
      `${currency} has no minor units: ${JSON.stringify(input)}`,
    );
  }
  const factor = 10 ** digits;
  const value =
    Number(majorPart as string) * factor +
    Number((fracPart as string).padEnd(digits, '0') || '0');
  if (!Number.isSafeInteger(value)) {
    throw new RangeError(`amount out of range: ${JSON.stringify(input)}`);
  }
  return value;
}

/**
 * Format integer minor units for display, always with the currency's full
 * minor digits (e.g. exactly two decimals for USD).
 */
export function formatAmount(
  amountMinor: number,
  currency = 'USD',
  locale = 'en-US',
): string {
  assertCurrency(currency);
  if (!Number.isSafeInteger(amountMinor) || amountMinor < 0) {
    throw new RangeError(
      `amountMinor must be a safe non-negative integer, got ${amountMinor}`,
    );
  }
  const digits = minorDigits(currency, locale);
  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency,
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(amountMinor / 10 ** digits);
}
