import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { readable } from 'svelte/store';

const { mockGET, mockPOST, mockPUT } = vi.hoisted(() => ({
  mockGET: vi.fn(),
  mockPOST: vi.fn(),
  mockPUT: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  client: { GET: mockGET, POST: mockPOST, PUT: mockPUT },
}));

// Both role hints true: the console renders, the moderation panel shows,
// and the suspend buttons are shown. The backend is the real gate; these
// are cosmetic.
vi.mock('../lib/auth', () => ({
  canAccessAdminConsole: readable(true),
  canEditDevops: readable(true),
}));

import AdminConsole from './AdminConsole.svelte';

// overviewBody builds the manifest the landing endpoint returns. The
// moderation section drives whether the moderation panel renders.
function overviewBody(moderationStatus: 'planned' | 'available') {
  return {
    console: 'admin',
    message: 'shell live',
    sections: [
      { key: 'moderation', title: 'Moderation', status: moderationStatus, description: 'x' },
      { key: 'site-management', title: 'Site', status: 'planned', description: 'x' },
    ],
  };
}

function contentItem(over: Partial<Record<string, unknown>> = {}) {
  return {
    kind: 'specimen',
    id: 'spec-1',
    specimen_id: 'spec-1',
    title: 'Bad Quartz',
    preview: '',
    visibility: 'public',
    owner_id: 'owner-1',
    owner_display_name: 'Mallory',
    created_at: '2026-01-01T00:00:00Z',
    ...over,
  };
}

// overviewWithUsers returns a manifest whose "users" section is
// available, so the component loads the user list on mount.
function overviewWithUsers() {
  return {
    console: 'admin',
    message: 'live',
    sections: [
      { key: 'users', title: 'Users', status: 'available', description: 'd' },
      { key: 'site-management', title: 'Site', status: 'planned', description: 'd' },
    ],
  };
}

function user(id: string, status: string, name = 'Ada') {
  return {
    id,
    display_name: name,
    status,
    specimen_count: 0,
    photo_count: 0,
    journal_count: 0,
    created_at: '2026-01-01T00:00:00Z',
  };
}

// routeGET dispatches the mocked GET by path so overview and users each
// return their own fixture.
function routeGET(users: ReturnType<typeof user>[]) {
  mockGET.mockImplementation((path: string) => {
    if (path === '/api/v1/admin/overview') {
      return Promise.resolve({ data: overviewWithUsers(), response: { status: 200 } });
    }
    if (path === '/api/v1/admin/users') {
      return Promise.resolve({ data: { items: users, next_cursor: null } });
    }
    return Promise.resolve({ data: undefined, response: { status: 404 } });
  });
}

beforeEach(() => {
  mockGET.mockReset();
  mockPOST.mockReset();
  mockPUT.mockReset();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('AdminConsole — moderation panel (mi-jjzc)', () => {
  it('does not render the panel when the moderation section is "planned"', async () => {
    mockGET.mockResolvedValue({
      data: overviewBody('planned'),
      response: { status: 200 },
      error: undefined,
    });
    render(AdminConsole);

    await screen.findByTestId('admin-console-sections');
    expect(screen.queryByTestId('moderation-panel')).toBeNull();
    // The published-content feed is never fetched when moderation is planned.
    expect(mockGET).toHaveBeenCalledTimes(1);
  });

  it('lists published content and labels actions by kind when "available"', async () => {
    mockGET.mockImplementation((path: string) => {
      if (path === '/api/v1/admin/overview') {
        return Promise.resolve({
          data: overviewBody('available'),
          response: { status: 200 },
          error: undefined,
        });
      }
      // published-content feed
      return Promise.resolve({
        data: {
          items: [
            contentItem({ kind: 'specimen', id: 'spec-1', title: 'Bad Quartz' }),
            contentItem({ kind: 'photo', id: 'photo-1', title: 'Bad Photo' }),
            contentItem({ kind: 'journal', id: 'journal-1', title: 'Bad Note' }),
          ],
          next_cursor: null,
        },
        error: undefined,
      });
    });

    render(AdminConsole);

    await screen.findByTestId('moderation-list');
    expect(screen.getByTestId('moderation-action-spec-1')).toHaveTextContent('Take down');
    expect(screen.getByTestId('moderation-action-photo-1')).toHaveTextContent('Remove');
    expect(screen.getByTestId('moderation-action-journal-1')).toHaveTextContent('Remove');
  });

  it('POSTs takedown for a specimen and drops the row on success', async () => {
    mockGET.mockImplementation((path: string) => {
      if (path === '/api/v1/admin/overview') {
        return Promise.resolve({
          data: overviewBody('available'),
          response: { status: 200 },
          error: undefined,
        });
      }
      return Promise.resolve({
        data: { items: [contentItem({ kind: 'specimen', id: 'spec-1' })], next_cursor: null },
        error: undefined,
      });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mockPOST.mockResolvedValue({ data: {}, response: { ok: true }, error: undefined });

    render(AdminConsole);

    const btn = await screen.findByTestId('moderation-action-spec-1');
    await fireEvent.click(btn);

    await waitFor(() => expect(mockPOST).toHaveBeenCalled());
    const [path, opts] = mockPOST.mock.calls[0] as [string, { params: { path: { id: string } } }];
    expect(path).toBe('/api/v1/admin/specimens/{id}/takedown');
    expect(opts.params.path.id).toBe('spec-1');
    await waitFor(() => expect(screen.queryByTestId('moderation-item-spec-1')).toBeNull());
  });

  it('POSTs the remove endpoint for a photo', async () => {
    mockGET.mockImplementation((path: string) => {
      if (path === '/api/v1/admin/overview') {
        return Promise.resolve({
          data: overviewBody('available'),
          response: { status: 200 },
          error: undefined,
        });
      }
      return Promise.resolve({
        data: { items: [contentItem({ kind: 'photo', id: 'photo-1' })], next_cursor: null },
        error: undefined,
      });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mockPOST.mockResolvedValue({ data: {}, response: { ok: true }, error: undefined });

    render(AdminConsole);
    await fireEvent.click(await screen.findByTestId('moderation-action-photo-1'));

    await waitFor(() => expect(mockPOST).toHaveBeenCalled());
    const [path] = mockPOST.mock.calls[0] as [string, unknown];
    expect(path).toBe('/api/v1/admin/photos/{id}/remove');
  });

  it('does not POST when the operator cancels the confirm dialog', async () => {
    mockGET.mockImplementation((path: string) => {
      if (path === '/api/v1/admin/overview') {
        return Promise.resolve({
          data: overviewBody('available'),
          response: { status: 200 },
          error: undefined,
        });
      }
      return Promise.resolve({
        data: { items: [contentItem({ kind: 'specimen', id: 'spec-1' })], next_cursor: null },
        error: undefined,
      });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    render(AdminConsole);
    await fireEvent.click(await screen.findByTestId('moderation-action-spec-1'));

    expect(mockPOST).not.toHaveBeenCalled();
    // Row stays — nothing was acted on.
    expect(screen.getByTestId('moderation-item-spec-1')).toBeTruthy();
  });

  it('keeps the row and shows an error when the action fails', async () => {
    mockGET.mockImplementation((path: string) => {
      if (path === '/api/v1/admin/overview') {
        return Promise.resolve({
          data: overviewBody('available'),
          response: { status: 200 },
          error: undefined,
        });
      }
      return Promise.resolve({
        data: { items: [contentItem({ kind: 'journal', id: 'journal-1' })], next_cursor: null },
        error: undefined,
      });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    mockPOST.mockResolvedValue({ data: undefined, response: { ok: false }, error: { error: {} } });

    render(AdminConsole);
    await fireEvent.click(await screen.findByTestId('moderation-action-journal-1'));

    await waitFor(() => expect(screen.getByTestId('moderation-error')).toBeTruthy());
    expect(screen.getByTestId('moderation-item-journal-1')).toBeTruthy();
  });
});

describe('AdminConsole — account suspension panel (mi-3gxz)', () => {
  it('lists users and shows a Suspend button for an active account', async () => {
    routeGET([user('u-1', 'active')]);
    render(AdminConsole);

    const btn = await screen.findByTestId('admin-user-suspend-button');
    expect(btn.textContent?.trim()).toBe('Suspend');
    expect(screen.getByTestId('admin-user-status').textContent?.trim()).toBe('active');
  });

  it('suspends an active account and flips its status to suspended in place', async () => {
    routeGET([user('u-1', 'active')]);
    mockPOST.mockResolvedValue({
      data: { id: 'u-1', status: 'suspended', identity_synced: true },
      error: undefined,
    });
    render(AdminConsole);

    const btn = await screen.findByTestId('admin-user-suspend-button');
    await fireEvent.click(btn);

    await waitFor(() => {
      expect(screen.getByTestId('admin-user-status').textContent?.trim()).toBe('suspended');
    });
    // Hit the suspend endpoint, not unsuspend.
    expect(mockPOST).toHaveBeenCalledWith(
      '/api/v1/admin/users/{id}/suspend',
      expect.objectContaining({ params: { path: { id: 'u-1' } } }),
    );
    // The row now offers the inverse action.
    expect(screen.getByTestId('admin-user-suspend-button').textContent?.trim()).toBe('Unsuspend');
  });

  it('unsuspends a suspended account back to active', async () => {
    routeGET([user('u-1', 'suspended')]);
    mockPOST.mockResolvedValue({
      data: { id: 'u-1', status: 'active', identity_synced: true },
      error: undefined,
    });
    render(AdminConsole);

    const btn = await screen.findByTestId('admin-user-suspend-button');
    expect(btn.textContent?.trim()).toBe('Unsuspend');
    await fireEvent.click(btn);

    await waitFor(() => {
      expect(screen.getByTestId('admin-user-status').textContent?.trim()).toBe('active');
    });
    expect(mockPOST).toHaveBeenCalledWith(
      '/api/v1/admin/users/{id}/unsuspend',
      expect.objectContaining({ params: { path: { id: 'u-1' } } }),
    );
  });

  it('surfaces an error when the suspend call fails and leaves the status unchanged', async () => {
    routeGET([user('u-1', 'active')]);
    mockPOST.mockResolvedValue({
      data: undefined,
      error: { error: { code: 'identity_sync_failed' } },
    });
    render(AdminConsole);

    const btn = await screen.findByTestId('admin-user-suspend-button');
    await fireEvent.click(btn);

    await waitFor(() => {
      expect(screen.getByRole('alert').textContent).toMatch(/failed to suspend/i);
    });
    expect(screen.getByTestId('admin-user-status').textContent?.trim()).toBe('active');
  });
});
