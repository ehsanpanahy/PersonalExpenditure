import {
  allocate,
  allocateToMembers,
  parseAmount,
  formatAmount,
} from './index';

describe('allocate', () => {
  it('splits $10.00 three ways as 334/333/333', () => {
    expect(allocate(1000, 3)).toEqual([334, 333, 333]);
  });

  it('splits a single cent three ways as 1/0/0', () => {
    expect(allocate(1, 3)).toEqual([1, 0, 0]);
  });

  it('splits zero as all zeros', () => {
    expect(allocate(0, 3)).toEqual([0, 0, 0]);
  });

  it('n=1 returns the full amount', () => {
    expect(allocate(12345, 1)).toEqual([12345]);
  });

  it('shares always sum exactly to the total', () => {
    for (const [amount, n] of [
      [1000, 3],
      [1, 3],
      [0, 5],
      [999, 7],
      [123456789, 11],
    ] as const) {
      expect(allocate(amount, n).reduce((a, b) => a + b, 0)).toBe(amount);
    }
  });

  it('is deterministic for the same input', () => {
    expect(allocate(1000, 3)).toEqual(allocate(1000, 3));
  });

  it('handles amounts near the safe-integer range without drift', () => {
    const amount = Number.MAX_SAFE_INTEGER - 1;
    const shares = allocate(amount, 3);
    expect(shares.reduce((a, b) => a + b, 0)).toBe(amount);
    expect(Math.max(...shares) - Math.min(...shares)).toBeLessThanOrEqual(1);
  });

  it('rejects negative amounts, non-integers, and n < 1', () => {
    expect(() => allocate(-1, 3)).toThrow(RangeError);
    expect(() => allocate(10.5, 3)).toThrow(RangeError);
    expect(() => allocate(100, 0)).toThrow(RangeError);
    expect(() => allocate(100, 2.5)).toThrow(RangeError);
  });
});

describe('allocateToMembers', () => {
  it('gives the leftover cent to the lexicographically smallest id', () => {
    const shares = allocateToMembers(1000, ['c', 'a', 'b']);
    expect(shares).toEqual([
      { userId: 'a', amountMinor: 334 },
      { userId: 'b', amountMinor: 333 },
      { userId: 'c', amountMinor: 333 },
    ]);
  });

  it('is order-independent for the same member set', () => {
    expect(allocateToMembers(1000, ['c', 'a', 'b'])).toEqual(
      allocateToMembers(1000, ['a', 'b', 'c']),
    );
  });

  it('rejects empty and duplicate member lists', () => {
    expect(() => allocateToMembers(100, [])).toThrow(RangeError);
    expect(() => allocateToMembers(100, ['a', 'a'])).toThrow(RangeError);
  });
});

describe('parseAmount / formatAmount', () => {
  it('parses whole, one-decimal, and grouped input', () => {
    expect(parseAmount('10.00')).toBe(1000);
    expect(parseAmount('10')).toBe(1000);
    expect(parseAmount('10.5')).toBe(1050);
    expect(parseAmount('1,234.56')).toBe(123456);
  });

  it('formats with exactly two decimals (USD)', () => {
    expect(formatAmount(1000, 'USD', 'en-US')).toBe('$10.00');
    expect(formatAmount(0, 'USD', 'en-US')).toBe('$0.00');
  });

  it('round-trips through the formatted string', () => {
    for (const minor of [0, 1, 333, 1000, 123456]) {
      const numeric = formatAmount(minor, 'USD', 'en-US').replace(/[^0-9.,]/g, '');
      expect(parseAmount(numeric, 'USD', 'en-US')).toBe(minor);
    }
  });

  it('never rounds silently: rejects excess precision and negatives', () => {
    expect(() => parseAmount('10.001')).toThrow(RangeError);
    expect(() => parseAmount('-5')).toThrow(RangeError);
    expect(() => parseAmount('')).toThrow(RangeError);
    expect(() => parseAmount('abc')).toThrow(RangeError);
    expect(() => formatAmount(-1)).toThrow(RangeError);
  });
});
