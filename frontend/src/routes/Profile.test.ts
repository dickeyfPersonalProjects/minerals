import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';

import Profile from './Profile.svelte';
import { __authenticate, __resetAuthStore } from '../lib/auth';

beforeEach(() => {
  __resetAuthStore();
});

afterEach(() => {
  cleanup();
  __resetAuthStore();
});

describe('Profile route', () => {
  it('renders the heading and the current user info from the auth store', () => {
    __authenticate({ display_name: 'Ada Lovelace', email: 'ada@example.com' });
    render(Profile);
    expect(screen.getByRole('heading', { name: 'Profile' })).toBeInTheDocument();
    expect(screen.getByTestId('profile-display-name')).toHaveTextContent('Ada Lovelace');
    expect(screen.getByTestId('profile-email')).toHaveTextContent('ada@example.com');
  });

  it('does not render the field-defaults section (moved to /settings in mi-1ygd)', () => {
    __authenticate({ display_name: 'Ada', email: 'ada@example.com' });
    render(Profile);
    // The field-defaults form moved to Settings; assert the Profile
    // page no longer surfaces any of its testids or its label.
    expect(screen.queryByTestId('profile-field-defaults-form')).not.toBeInTheDocument();
    expect(screen.queryByTestId('profile-default-price')).not.toBeInTheDocument();
    expect(screen.queryByText('Field defaults')).not.toBeInTheDocument();
  });

  it('shows em-dash placeholders when the user has no name or email', () => {
    __authenticate({ display_name: '', email: '' });
    render(Profile);
    expect(screen.getByTestId('profile-display-name')).toHaveTextContent('—');
    expect(screen.getByTestId('profile-email')).toHaveTextContent('—');
  });
});
