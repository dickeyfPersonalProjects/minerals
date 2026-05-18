import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import LoginButton from './LoginButton.svelte';

beforeEach(() => {
  // The component reads window.location.hash for the return_to
  // param. jsdom defaults to about:blank with an empty hash; tests
  // that care about the value set it explicitly below.
  window.location.hash = '';
});

afterEach(() => {
  cleanup();
  window.location.hash = '';
});

describe('LoginButton (V2 BFF cookie flow, mi-3vc4)', () => {
  it('renders an anchor pointing at the backend /auth/login endpoint', () => {
    render(LoginButton);
    const anchor = screen.getByTestId('login-button');
    expect(anchor.tagName).toBe('A');
    expect(anchor).toHaveAttribute('href', '/auth/login?return_to=%23%2F');
    expect(anchor).toHaveTextContent(/log in/i);
  });

  it('encodes the current hash route as return_to so the backend can bounce back', () => {
    window.location.hash = '#/specimens/abc';
    render(LoginButton);
    const anchor = screen.getByTestId('login-button');
    expect(anchor.getAttribute('href')).toBe('/auth/login?return_to=%23%2Fspecimens%2Fabc');
  });
});
