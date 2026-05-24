import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';

import Footer from './Footer.svelte';

afterEach(() => {
  cleanup();
});

// __GIT_SHA__ / __BUILD_DATE__ are inlined at transform time by the
// vitest.config.ts `define` (defaults: 'dev' / build-time `now`), so these
// tests exercise the local-dev render path. The link/format branches for
// real SHAs are covered directly in buildInfo.test.ts.
describe('Footer — deploy marker (mi-c0sv)', () => {
  it('renders the build info marker', () => {
    render(Footer);
    expect(screen.getByTestId('build-info')).toBeInTheDocument();
  });

  it('shows the dev SHA as plain text (no commit link) in dev builds', () => {
    render(Footer);
    expect(screen.getByTestId('build-sha')).toHaveTextContent('vdev');
    expect(screen.queryByTestId('build-sha-link')).not.toBeInTheDocument();
  });

  it('still shows the collection caption', () => {
    render(Footer);
    expect(screen.getByText('Minerals · personal collection')).toBeInTheDocument();
  });

  it('shows a "built" timestamp', () => {
    render(Footer);
    expect(screen.getByTestId('build-info')).toHaveTextContent(/built/);
  });
});

describe('Footer — legal links (mi-97kr)', () => {
  it('links to the privacy policy', () => {
    render(Footer);
    const link = screen.getByTestId('footer-privacy-link');
    expect(link).toHaveTextContent('Privacy');
    expect(link).toHaveAttribute('href', '#/privacy');
  });

  it('links to the terms of service', () => {
    render(Footer);
    const link = screen.getByTestId('footer-terms-link');
    expect(link).toHaveTextContent('Terms');
    expect(link).toHaveAttribute('href', '#/terms');
  });
});
