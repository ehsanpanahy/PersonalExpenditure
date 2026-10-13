import {
  monthRange,
  monthRangeForDate,
  daysInMonth,
  zonedToday,
  isValidTimeZone,
  isWithinPeriod,
  isFutureDate,
  isDateString,
} from './index';

describe('monthRange', () => {
  it('returns explicit inclusive from/to, never a month string', () => {
    expect(monthRange(2026, 3)).toEqual({ from: '2026-03-01', to: '2026-03-31' });
  });

  it('handles February in leap and common years', () => {
    expect(monthRange(2024, 2)).toEqual({ from: '2024-02-01', to: '2024-02-29' });
    expect(monthRange(2026, 2)).toEqual({ from: '2026-02-01', to: '2026-02-28' });
  });

  it('derives the range from a contained date', () => {
    expect(monthRangeForDate('2026-03-15')).toEqual({
      from: '2026-03-01',
      to: '2026-03-31',
    });
  });

  it('rejects out-of-range months and non-dates', () => {
    expect(() => monthRange(2026, 0)).toThrow(RangeError);
    expect(() => monthRange(2026, 13)).toThrow(RangeError);
    expect(() => monthRangeForDate('2026-02-30')).toThrow(RangeError);
    expect(() => monthRangeForDate('2026-3-5')).toThrow(RangeError);
    expect(isDateString('2026-02-30')).toBe(false);
    expect(daysInMonth(2026, 3)).toBe(31);
  });
});

describe('zonedToday (DST behaviour)', () => {
  it('differs from the UTC date when the zone is behind', () => {
    // 04:00Z Jan 1 is still Dec 31 in New York.
    expect(zonedToday('America/New_York', new Date('2026-01-01T04:00:00Z'))).toBe(
      '2025-12-31',
    );
  });

  it('is stable across the spring-forward transition (US, 2026-03-08)', () => {
    // 01:59 EST and 03:00 EDT are both March 8 locally; UTC says March 8 either way.
    expect(zonedToday('America/New_York', new Date('2026-03-08T06:59:00Z'))).toBe(
      '2026-03-08',
    );
    expect(zonedToday('America/New_York', new Date('2026-03-08T07:00:00Z'))).toBe(
      '2026-03-08',
    );
    // …but late evening March 7 EST is already March 8 in UTC.
    expect(zonedToday('America/New_York', new Date('2026-03-08T03:00:00Z'))).toBe(
      '2026-03-07',
    );
  });

  it('is stable across the fall-back transition (Berlin, 2026-10-25)', () => {
    expect(zonedToday('Europe/Berlin', new Date('2026-10-25T00:30:00Z'))).toBe(
      '2026-10-25',
    );
    expect(zonedToday('Europe/Berlin', new Date('2026-10-25T01:30:00Z'))).toBe(
      '2026-10-25',
    );
    // Ahead of UTC: already Jan 1 locally while UTC is still Dec 31.
    expect(zonedToday('Europe/Berlin', new Date('2025-12-31T23:30:00Z'))).toBe(
      '2026-01-01',
    );
  });

  it('rejects unknown zones', () => {
    expect(isValidTimeZone('Europe/Berlin')).toBe(true);
    expect(isValidTimeZone('Not/AZone')).toBe(false);
    expect(() => zonedToday('Not/AZone')).toThrow(RangeError);
  });
});

describe('isWithinPeriod', () => {
  it('is inclusive at both ends', () => {
    expect(isWithinPeriod('2026-03-01', '2026-03-01', '2026-03-31')).toBe(true);
    expect(isWithinPeriod('2026-03-31', '2026-03-01', '2026-03-31')).toBe(true);
    expect(isWithinPeriod('2026-03-15', '2026-03-01', '2026-03-31')).toBe(true);
  });

  it('rejects outside dates — never clamps', () => {
    expect(isWithinPeriod('2026-02-28', '2026-03-01', '2026-03-31')).toBe(false);
    expect(isWithinPeriod('2026-04-01', '2026-03-01', '2026-03-31')).toBe(false);
  });

  it('treats null endsOn as ongoing', () => {
    expect(isWithinPeriod('2027-06-15', '2026-03-01', null)).toBe(true);
    expect(isWithinPeriod('2026-02-28', '2026-03-01', null)).toBe(false);
  });

  it('rejects malformed dates', () => {
    expect(() => isWithinPeriod('not-a-date', '2026-03-01', null)).toThrow(RangeError);
  });
});

describe('isFutureDate (FR-3.2)', () => {
  it('flags strictly-future dates only', () => {
    expect(isFutureDate('2026-03-02', '2026-03-01')).toBe(true);
    expect(isFutureDate('2026-03-01', '2026-03-01')).toBe(false);
    expect(isFutureDate('2026-02-28', '2026-03-01')).toBe(false);
  });
});
