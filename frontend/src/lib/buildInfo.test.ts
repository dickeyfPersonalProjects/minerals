import { describe, expect, it } from 'vitest';
import { commitUrl, formatBuildDate, REPO_COMMIT_BASE } from './buildInfo';

describe('buildInfo — commitUrl', () => {
  it('links a real short SHA to the GitHub commit', () => {
    expect(commitUrl('abc1234')).toBe(`${REPO_COMMIT_BASE}/abc1234`);
  });

  it('returns null for the local "dev" placeholder (no real commit)', () => {
    expect(commitUrl('dev')).toBeNull();
  });

  it('returns null for an empty SHA', () => {
    expect(commitUrl('')).toBeNull();
  });
});

describe('buildInfo — formatBuildDate', () => {
  it('formats a valid ISO 8601 timestamp', () => {
    // formatLocal is locale-dependent; assert it produced a non-empty,
    // different-from-input string rather than pinning an exact format.
    const out = formatBuildDate('2026-05-20T01:19:39Z');
    expect(out).toBeTruthy();
    expect(out).not.toBe('2026-05-20T01:19:39Z');
  });

  it('falls back to the raw value when the date is malformed', () => {
    expect(formatBuildDate('not-a-date')).toBe('not-a-date');
  });
});
