import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { renderDeepSeekHarnessMcpPatch } from '../src/dsh-mcp-patch.ts';
import { managedMcpArgument } from '../src/mcp-config.ts';

test('renders a project-bound DSH Cordis overlay without credentials or permission settings', () => {
  const root = path.resolve('Threadforge Project');
  const yaml = renderDeepSeekHarnessMcpPatch("C:\\Program Files\\Threadforge's CLI.exe", [
    'mcp', 'serve', '--project-root', root, managedMcpArgument,
  ]);

  assert.equal(yaml, [
    '- insert:',
    '    - id: threadforge-mcp',
    "      name: '@deepseek-ai/dsh-mcp-client'",
    '      config:',
    '        serverName: threadforge',
    '        transport: stdio',
    "        command: 'C:\\Program Files\\Threadforge''s CLI.exe'",
    '        args:',
    "          - 'mcp'",
    "          - 'serve'",
    "          - '--project-root'",
    `          - '${root}'`,
    "          - '--threadforge-managed-mcp=v1'",
    '',
  ].join('\n'));
  assert.doesNotMatch(yaml, /\benv:|\btrust:|autoApprove|permissionOverride/i);
});

test('rejects credentials, multiline YAML injection, and non-project MCP arguments', () => {
  const root = path.resolve('fixture-project');
  const args = ['mcp', 'serve', '--project-root', root, managedMcpArgument];
  assert.throws(() => renderDeepSeekHarnessMcpPatch('threadforge token=fixture-secret', args), /credential-free/);
  assert.throws(() => renderDeepSeekHarnessMcpPatch('threadforge\n  env: leaked', args), /bounded and credential-free/);
  assert.throws(() => renderDeepSeekHarnessMcpPatch('threadforge', ['mcp', 'serve', '--project-root', root]), /standard project-bound/);
  assert.throws(() => renderDeepSeekHarnessMcpPatch('threadforge', ['mcp', 'serve', '--project-root', 'relative', managedMcpArgument]), /standard project-bound/);
});
