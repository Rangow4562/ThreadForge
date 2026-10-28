import path from 'node:path';
import { looksCredentialLikeMcpValue, managedMcpArgument } from './mcp-contract.ts';

export function renderDeepSeekHarnessMcpPatch(command: string, args: string[]): string {
  if (typeof command !== 'string' || command.trim() !== command || command.length < 1 || command.length > 2048 ||
      /[\r\n\0]/.test(command) || looksCredentialLikeMcpValue(command)) {
    throw new Error('DeepSeek Harness MCP executable must be bounded and credential-free');
  }
  if (!Array.isArray(args) || args.length !== 5 ||
      args.some((arg) => typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg) || looksCredentialLikeMcpValue(arg)) ||
      args[0] !== 'mcp' || args[1] !== 'serve' || args[2] !== '--project-root' ||
      !path.isAbsolute(args[3]!) || args[4] !== managedMcpArgument) {
    throw new Error('DeepSeek Harness MCP requires the standard project-bound Threadforge MCP arguments');
  }

  const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
  return [
    '- insert:',
    '    - id: threadforge-mcp',
    "      name: '@deepseek-ai/dsh-mcp-client'",
    '      config:',
    '        serverName: threadforge',
    '        transport: stdio',
    `        command: ${quote(command)}`,
    '        args:',
    ...args.map((arg) => `          - ${quote(arg)}`),
    '',
  ].join('\n');
}
