import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import RegisterButton from './RegisterButton.svelte';

beforeEach(() => {
  // RegisterButton reads window.location.hash for its return_to
  // param, same as LoginButton. jsdom defaults to about:blank with
  // an empty hash; tests that care set it explicitly below.
  window.location.hash = '';
});

afterEach(() => {
  cleanup();
  window.location.hash = '';
});

describe('RegisterButton (V2 BFF cookie flow, mi-eb3b)', () => {
  it('renders an anchor pointing at the backend /auth/register endpoint', () => {
    render(RegisterButton);
    const anchor = screen.getByTestId('register-link');
    expect(anchor.tagName).toBe('A');
    expect(anchor).toHaveAttribute('href', '/auth/register?return_to=%23%2F');
    expect(anchor).toHaveTextContent(/register/i);
  });

  it('encodes the current hash route as return_to so the backend can bounce back', () => {
    window.location.hash = '#/specimens/abc';
    render(RegisterButton);
    const anchor = screen.getByTestId('register-link');
    expect(anchor.getAttribute('href')).toBe('/auth/register?return_to=%23%2Fspecimens%2Fabc');
  });
});
