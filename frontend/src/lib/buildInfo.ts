import { formatLocal } from './time';

// Deploy-marker helpers (mi-c0sv). The footer reads the compile-time
// constants __GIT_SHA__ / __BUILD_DATE__ (inlined by Vite's `define`), but
// those literals can't be varied in a unit test, so the link-building and
// date-formatting logic lives here as pure functions that take the values
// as arguments. Footer.svelte is then a thin shell over these.

export const REPO_COMMIT_BASE = 'https://github.com/dickeyfPersonalProjects/minerals/commit';

// Local/dev builds have no real commit, so they get no link. CI injects a
// genuine short SHA; GitHub resolves short SHAs in commit URLs.
export function commitUrl(sha: string): string | null {
  return sha === 'dev' || sha === '' ? null : `${REPO_COMMIT_BASE}/${sha}`;
}

// Build date arrives as ISO 8601; render it in the viewer's locale. Guard
// against a malformed value so a bad build arg can't blank the app — fall
// back to the raw string rather than throwing.
export function formatBuildDate(iso: string): string {
  try {
    return formatLocal(iso);
  } catch {
    return iso;
  }
}
