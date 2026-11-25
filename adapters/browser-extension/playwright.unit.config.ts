import { defineConfig } from '@playwright/test';

// Browser-free extension tests run on Playwright's Node test runner. Keeping
// them separate from the replay suite avoids launching Chromium for unit tests.
export default defineConfig({
  testDir: './test/unit',
  testMatch: '**/*.test.ts',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: 'list',
});
