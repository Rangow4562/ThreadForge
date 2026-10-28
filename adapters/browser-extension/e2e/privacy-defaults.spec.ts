import { test, expect, seedSettings } from './fixtures.js';

const COMPLETION_PATH = '/api/organizations/org-test/chat_conversations/{conv}/completion';

test('installation alone captures nothing until capture and a site are opted in', async ({
  context,
  serviceWorker,
  replay,
  mockCollector,
}) => {
  replay.setCase({
    site: 'claude', name: 'simple-turn', conversationId: 'default-off',
    prompt: 'private prompt must not be captured', completionPath: COMPLETION_PATH,
  });
  const page = await context.newPage();
  await page.goto('https://claude.ai/chat');
  await expect(page.locator('[data-testid="assistant-message"]')).toHaveAttribute('data-complete', '1');
  await expect.poll(
    async () => serviceWorker.evaluate(async () => (await chrome.storage.local.get('settings')).settings),
    { timeout: 5_000 },
  ).toMatchObject({
    enabled: false,
    retention: 'metadata',
    sites: { claude_web: false, chatgpt_web: false },
  });
  await expect.poll(() => mockCollector.received.length, { timeout: 500 }).toBe(0);
});

test('metadata retention reports prompt metadata without exporting prompt or response text', async ({
  context,
  serviceWorker,
  replay,
  mockCollector,
}) => {
  replay.setCase({
    site: 'claude', name: 'simple-turn', conversationId: 'metadata-only',
    prompt: 'private prompt must not be exported', completionPath: COMPLETION_PATH,
  });
  await seedSettings(serviceWorker, {
    enabled: true,
    retention: 'metadata',
    sites: { claude_web: true, chatgpt_web: false },
    endpoint: mockCollector.url,
  });
  const optedIn = await serviceWorker.evaluate(async () => (await chrome.storage.local.get('settings')).settings);
  expect(optedIn).toMatchObject({ enabled: true, retention: 'metadata', sites: { claude_web: true, chatgpt_web: false } });
  const page = await context.newPage();
  await page.goto('https://claude.ai/chat');
  await expect(page.locator('[data-testid="assistant-message"]')).toHaveAttribute('data-complete', '1');
  await expect.poll(() => mockCollector.received.length, { timeout: 10_000 }).toBeGreaterThan(0);

  const payload = JSON.stringify(mockCollector.received);
  expect(payload).not.toContain('private prompt must not be exported');
  expect(payload).not.toContain('Paris');
  const records = mockCollector.received.flatMap((envelope) => envelope.resourceLogs.flatMap((group) =>
    group.scopeLogs.flatMap((scope) => scope.logRecords)));
  const prompt = records.find((record) => record.attributes.some((attribute) =>
    attribute.key === 'beacon.event.action' && 'stringValue' in attribute.value && attribute.value.stringValue === 'prompt.submitted'));
  expect(prompt).toBeTruthy();
  const attrs = Object.fromEntries(prompt!.attributes.map((attribute) => [
    attribute.key,
    'stringValue' in attribute.value ? attribute.value.stringValue :
      'intValue' in attribute.value ? attribute.value.intValue : undefined,
  ]));
  expect(attrs['beacon.prompt.chars']).toBe('35');
  expect(attrs['beacon.prompt.text']).toBeUndefined();
});
