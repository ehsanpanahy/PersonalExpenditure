import { allocate } from '../money/allocate';
import { freezeShares, validateExactShares } from './index';
import conformance from './conformance.json';

describe('split-rounding conformance (BR-2, mirrors pgTAP §C)', () => {
  for (const c of (
    conformance as {
      cases: { name: string; amountMinor: number; n: number; expected: number[] }[];
    }
  ).cases) {
    it(`allocate(${c.amountMinor}, ${c.n}) = [${c.expected}]`, () => {
      expect(allocate(c.amountMinor, c.n)).toEqual(c.expected);
    });
  }

  it('freezeShares binds the head share to the smallest member id', () => {
    expect(freezeShares(1000, ['c', 'a', 'b'])).toEqual([
      { userId: 'a', amountMinor: 334 },
      { userId: 'b', amountMinor: 333 },
      { userId: 'c', amountMinor: 333 },
    ]);
  });

  it('freezeShares output always sums exactly to the total', () => {
    for (const c of (
      conformance as {
        cases: { amountMinor: number; n: number }[];
      }
    ).cases) {
      const ids = Array.from({ length: c.n }, (_, i) => `m${i}`);
      const shares = freezeShares(c.amountMinor, ids);
      expect(shares.reduce((a, s) => a + s.amountMinor, 0)).toBe(c.amountMinor);
      expect(validateExactShares(c.amountMinor, shares)).toBeNull();
    }
  });
});

describe('validateExactShares (FR-5.6/5.7, UX mirror of create_expense)', () => {
  it('accepts the canonical 1000/3 frozen split', () => {
    expect(
      validateExactShares(1000, [
        { userId: 'a', amountMinor: 334 },
        { userId: 'b', amountMinor: 333 },
        { userId: 'c', amountMinor: 333 },
      ]),
    ).toBeNull();
  });

  it('rejects a total that is short or over — never adjusts silently', () => {
    expect(
      validateExactShares(1000, [
        { userId: 'a', amountMinor: 333 },
        { userId: 'b', amountMinor: 333 },
        { userId: 'c', amountMinor: 333 },
      ]),
    ).toMatch(/must match exactly/);
    expect(
      validateExactShares(1000, [
        { userId: 'a', amountMinor: 500 },
        { userId: 'b', amountMinor: 501 },
      ]),
    ).toMatch(/must match exactly/);
  });

  it('rejects zero, negative, empty, and duplicate shares', () => {
    expect(validateExactShares(1000, [])).toMatch(/at least one/);
    expect(
      validateExactShares(1000, [
        { userId: 'a', amountMinor: 1000 },
        { userId: 'a', amountMinor: 0 },
      ]),
    ).toMatch(/duplicate/);
    expect(
      validateExactShares(1000, [{ userId: 'a', amountMinor: 0 }]),
    ).toMatch(/greater than zero/);
    expect(
      validateExactShares(1000, [{ userId: 'a', amountMinor: -5 }]),
    ).toMatch(/greater than zero/);
  });

  it('documents the 1-cent limit: allocate(1,3) previews [1,0,0], which the server rejects', () => {
    // A 1-cent expense cannot cover 3 members at >0 each. The preview shows
    // the remainder rule honestly; the RPC requires omitting excluded members
    // (no zero-share rows), so a 1-cent shared expense is single-participant.
    expect(allocate(1, 3)).toEqual([1, 0, 0]);
    expect(
      validateExactShares(1, [
        { userId: 'a', amountMinor: 1 },
        { userId: 'b', amountMinor: 0 },
        { userId: 'c', amountMinor: 0 },
      ]),
    ).toMatch(/greater than zero/);
    expect(validateExactShares(1, [{ userId: 'a', amountMinor: 1 }])).toBeNull();
  });
});
