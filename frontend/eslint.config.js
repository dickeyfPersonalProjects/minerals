import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import tseslint from 'typescript-eslint';
import globals from 'globals';
import svelteParser from 'svelte-eslint-parser';
import prettier from 'eslint-config-prettier';

export default [
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...svelte.configs['flat/recommended'],
  prettier,
  ...svelte.configs['flat/prettier'],
  {
    languageOptions: {
      globals: {
        ...globals.browser,
        ...globals.node,
        // Compile-time deploy markers injected by Vite's `define`
        // (mi-c0sv). Declared as readonly globals so no-undef recognises
        // them; their TS types live in src/buildinfo.d.ts.
        __GIT_SHA__: 'readonly',
        __BUILD_DATE__: 'readonly',
      },
    },
  },
  {
    files: ['**/*.svelte'],
    languageOptions: {
      parser: svelteParser,
      parserOptions: {
        parser: tseslint.parser,
      },
    },
  },
  {
    // Generated API client (mi-cy4): rewritten by `make gen-api-client`.
    // Playwright report/output dirs (mi-dwx) are gitignored and have
    // no source to lint.
    ignores: [
      'dist/',
      'node_modules/',
      'coverage/',
      '.svelte-kit/',
      'playwright-report/',
      'test-results/',
      'src/lib/api/schema.d.ts',
    ],
  },
];
