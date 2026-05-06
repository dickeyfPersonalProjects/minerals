import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatLocal, relative } from './time';

describe('formatLocal', () => {
  it('formats an ISO 8601 timestamp using Intl.DateTimeFormat', () => {
    const iso = '2026-05-06T14:23:11Z';
    const formatted = formatLocal(iso, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      timeZone: 'UTC',
      hourCycle: 'h23',
    });
    expect(formatted).toMatch(/2026/);
    expect(formatted).toMatch(/05/);
    expect(formatted).toMatch(/06/);
    expect(formatted).toMatch(/14:23/);
  });

  it('throws RangeError on invalid input', () => {
    expect(() => formatLocal('not-a-date')).toThrow(RangeError);
  });
});

describe('relative', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-05-06T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns a past phrase for an earlier timestamp', () => {
    const result = relative('2026-05-06T11:57:00Z');
    expect(result).toMatch(/3 minutes ago/);
  });

  it('returns a future phrase for a later timestamp', () => {
    const result = relative('2026-05-06T14:00:00Z');
    expect(result).toMatch(/in 2 hours/);
  });

  it('throws RangeError on invalid input', () => {
    expect(() => relative('garbage')).toThrow(RangeError);
  });
});
