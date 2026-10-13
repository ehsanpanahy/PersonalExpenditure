/**
 * UI prototype logic — plain-JS mirror of src/shared/money/*.ts.
 * Kept 1:1 with the TS module so the demo behaves exactly like the app.
 * (Wiring it to the real module needs a bundler/Expo; that's a later step.)
 */

const ISO_CURRENCY_RE = /^[A-Z]{3}$/;

function assertCurrency(currency) {
  if (!ISO_CURRENCY_RE.test(currency)) {
    throw new RangeError(`currency must be ISO-4217 (e.g. USD), got ${currency}`);
  }
}

function minorDigits(currency, locale = 'en-US') {
  assertCurrency(currency);
  return (
    new Intl.NumberFormat(locale, { style: 'currency', currency })
      .resolvedOptions().maximumFractionDigits ?? 2
  );
}

function parseAmount(input, currency = 'USD', locale = 'en-US') {
  const digits = minorDigits(currency, locale);
  const compact = String(input).trim().replace(/[\s,]/g, '');
  const match = /^(\d+)(?:\.(\d+))?$/.exec(compact);
  if (!match) throw new RangeError(`invalid amount: ${JSON.stringify(input)}`);
  const [, majorPart, fracPart = ''] = match;
  if (fracPart.length > digits) {
    throw new RangeError(`too many decimal places for ${currency}: ${JSON.stringify(input)}`);
  }
  if (digits === 0 && fracPart.length > 0) {
    throw new RangeError(`${currency} has no minor units: ${JSON.stringify(input)}`);
  }
  const factor = 10 ** digits;
  const value =
    Number(majorPart) * factor + Number(fracPart.padEnd(digits, '0') || '0');
  if (!Number.isSafeInteger(value)) {
    throw new RangeError(`amount out of range: ${JSON.stringify(input)}`);
  }
  return value;
}

function formatAmount(amountMinor, currency = 'USD', locale = 'en-US') {
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

function allocate(amountMinor, n) {
  if (!Number.isSafeInteger(amountMinor) || amountMinor < 0) {
    throw new RangeError(`amountMinor must be a safe non-negative integer, got ${amountMinor}`);
  }
  if (!Number.isSafeInteger(n) || n < 1) {
    throw new RangeError(`n must be a safe integer >= 1, got ${n}`);
  }
  const base = Math.floor(amountMinor / n);
  const remainder = amountMinor - base * n;
  return Array.from({ length: n }, (_, i) => base + (i < remainder ? 1 : 0));
}

function allocateToMembers(amountMinor, memberIds) {
  if (memberIds.length === 0) {
    throw new RangeError('memberIds must contain at least one member');
  }
  const sorted = [...memberIds].sort();
  for (let i = 1; i < sorted.length; i++) {
    if (sorted[i] === sorted[i - 1]) throw new RangeError(`duplicate member id: ${sorted[i]}`);
  }
  const shares = allocate(amountMinor, sorted.length);
  return sorted.map((userId, i) => ({ userId, amountMinor: shares[i] }));
}

// Export for Node smoke tests; wire up the DOM only in a browser.
if (typeof module !== 'undefined' && module.exports) {
  module.exports = { parseAmount, formatAmount, minorDigits, allocate, allocateToMembers };
}

if (typeof document !== 'undefined') {
  document.addEventListener('DOMContentLoaded', () => {
    const $ = (id) => document.getElementById(id);

    $('parseBtn').addEventListener('click', () => {
      const out = $('parseOut');
      try {
        const minor = parseAmount($('amountInput').value, $('currencySel').value);
        out.className = 'ok';
        out.textContent =
          `${minor} minor units → ${formatAmount(minor, $('currencySel').value)}`;
      } catch (e) {
        out.className = 'err';
        out.textContent = e.message;
      }
    });

    const runSplit = (presetAmount, presetMembers) => {
      const out = $('splitOut');
      try {
        const amountStr = presetAmount ?? $('splitAmount').value;
        const members = (presetMembers ?? $('membersInput').value.split('\n'))
          .map((s) => s.trim()).filter(Boolean);
        const minor = parseAmount(amountStr, $('currencySel').value);
        const shares = allocateToMembers(minor, members);
        const cur = $('currencySel').value;
        out.innerHTML =
          '<table><tr><th>Member</th><th>Share</th><th>Minor</th></tr>' +
          shares.map((s) =>
            `<tr><td>${s.userId}</td><td>${formatAmount(s.amountMinor, cur)}</td><td>${s.amountMinor}</td></tr>`
          ).join('') +
          `</table><p class="ok">Sum: ${shares.reduce((a, s) => a + s.amountMinor, 0)} = ${minor} ✓</p>`;
      } catch (e) {
        out.innerHTML = `<p class="err">${e.message}</p>`;
      }
    };

    $('splitBtn').addEventListener('click', () => runSplit());
    $('presetBtn').addEventListener('click', () =>
      runSplit('10.00', ['cara', 'ali', 'bo']));
  });
}
