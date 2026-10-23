/** Reserved argument that marks Threadforge-owned local MCP server entries. */
export const managedMcpArgument = '--threadforge-managed-mcp=v1';

export function looksCredentialLikeMcpValue(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|id[_ -]?token|token|password|secret|authorization|cookie|private[_ -]?key)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
