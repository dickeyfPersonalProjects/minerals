import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { envelopeMessage, toastApiError } from './wrapper';
import { clearAllToasts, toasts } from '../stores/toasts';

beforeEach(() => {
  clearAllToasts();
});

afterEach(() => {
  clearAllToasts();
});

describe('envelopeMessage', () => {
  it('prefers error.message', () => {
    expect(
      envelopeMessage(
        { error: { code: 'specimen_not_found', message: 'No specimen with id abc-123' } },
        404,
      ),
    ).toBe('No specimen with id abc-123');
  });

  it('falls back to error.code when message is absent', () => {
    expect(envelopeMessage({ error: { code: 'specimen_not_found' } }, 404)).toBe(
      'specimen_not_found',
    );
  });

  it('falls back to HTTP <status> when the envelope is empty', () => {
    expect(envelopeMessage(undefined, 500)).toBe('HTTP 500');
    expect(envelopeMessage({}, 500)).toBe('HTTP 500');
  });
});

describe('toastApiError', () => {
  it('pushes an error toast with the rendered envelope message', () => {
    toastApiError({ error: { code: 'rate_limited', message: 'too many requests' } }, 429);
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.type).toBe('error');
    expect(list[0]?.message).toBe('too many requests');
  });

  it('toasts the HTTP status when no envelope info is available', () => {
    toastApiError(undefined, 502);
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0]?.message).toBe('HTTP 502');
  });
});
