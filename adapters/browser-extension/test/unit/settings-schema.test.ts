import { describe, expect, it } from 'vitest';
import { mergeSettings, normalizeSettings } from '../../src/shared/settings-schema.js';
import { DEFAULT_SETTINGS } from '../../src/shared/types.js';

describe('browser capture settings privacy defaults and validation', () => {
  it('defaults to capture off, both sites off, and metadata retention', () => {
    expect(normalizeSettings(undefined)).toEqual(DEFAULT_SETTINGS);
    expect(DEFAULT_SETTINGS).toEqual({
      enabled: false,
      retention: 'metadata',
      endpoint: 'http://127.0.0.1:4318/v1/logs',
      sites: { claude_web: false, chatgpt_web: false },
    });
  });

  it('fails closed on corrupted settings and a nonlocal stored endpoint', () => {
    expect(normalizeSettings({
      enabled: true, retention: 'full', endpoint: 'https://remote.example/v1/logs',
      sites: { claude_web: true, chatgpt_web: true },
    })).toEqual(DEFAULT_SETTINGS);
    expect(normalizeSettings({
      enabled: true, retention: 'unknown', endpoint: DEFAULT_SETTINGS.endpoint,
      sites: { claude_web: true, chatgpt_web: 'yes' },
    })).toEqual({ ...DEFAULT_SETTINGS, enabled: true, sites: { claude_web: true, chatgpt_web: false } });
  });

  it('allows only typed local settings changes and preserves unmentioned site choices', () => {
    const selected = mergeSettings(DEFAULT_SETTINGS, { sites: { claude_web: true } });
    expect(selected.sites).toEqual({ claude_web: true, chatgpt_web: false });
    const enabled = mergeSettings(selected, { enabled: true, retention: 'redacted' });
    expect(enabled).toMatchObject({ enabled: true, retention: 'redacted', sites: selected.sites });
    expect(() => mergeSettings(enabled, { endpoint: 'https://collector.example/v1/logs' })).toThrow(/local Threadforge/);
    expect(() => mergeSettings(enabled, { retention: 'raw' as 'full' })).toThrow(/retention/);
    expect(() => mergeSettings(enabled, { sites: { claude_web: 'true' } as never })).toThrow(/sites/);
    expect(() => mergeSettings(enabled, { unrelated: true } as never)).toThrow(/unsupported field/);
  });
});
