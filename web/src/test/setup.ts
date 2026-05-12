// Vitest global setup. Loaded once before every test file.
import { beforeEach } from 'vitest';
import '@testing-library/jest-dom/vitest';

// jsdom doesn't ship a matchMedia, which some libs reach for during
// render. A no-op shim keeps tests stable.
if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

// Clean storage between tests so the API-key persistence behaviour
// stays deterministic across files.
beforeEach(() => {
  localStorage.clear();
});
