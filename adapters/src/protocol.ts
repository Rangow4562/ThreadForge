export const procedureGraphSchema = 'threadforge.procedure-graph.v1' as const;

export type HostCapability = 'capture' | 'mcp' | 'skill' | 'context-injection' | 'poll';

export interface AdapterManifest {
  schema_version: 'threadforge.adapter.v1';
  harness: string;
  adapter_version: string;
  capabilities: HostCapability[];
  injection_contract_verified: boolean;
}

export interface ProcedureContextRequest {
  schema_version: 'threadforge.context-request.v2';
  project_id: string;
  query: string;
  paths: string[];
  modules?: string[];
  path_fingerprints?: Array<{ path: string; sha256: string }>;
  max_items: number;
  max_tokens: number;
}

export interface ProcedureContextResponse {
  schema_version: 'threadforge.context-response.v2';
  status: 'complete' | 'incomplete' | 'stale' | 'conflicting' | 'privacy_blocked' | 'unavailable';
  action_ready: false;
  procedures: Array<{ id: string; revision: number; title: string; freshness: 'verified' | 'unverified'; guidance: string[] }>;
  memory?: Array<{
    id: string;
    kind: 'fact' | 'decision' | 'policy';
    revision: number;
    title: string;
    content: Record<string, unknown>;
    evidence: Array<{ origin: string; content_sha256: string; harness?: string; redacted: true }>;
  }>;
  gaps: string[];
}

export function validateAdapterManifest(value: unknown): AdapterManifest {
  if (!isRecord(value) || value.schema_version !== 'threadforge.adapter.v1') {
    throw new Error('unsupported adapter manifest');
  }
  if (typeof value.harness !== 'string' || value.harness.trim() === '') {
    throw new Error('adapter harness is required');
  }
  if (typeof value.adapter_version !== 'string' || value.adapter_version.trim() === '') {
    throw new Error('adapter version is required');
  }
  if (!Array.isArray(value.capabilities) || value.capabilities.some((item) => !isCapability(item))) {
    throw new Error('adapter capabilities are invalid');
  }
  if (typeof value.injection_contract_verified !== 'boolean') {
    throw new Error('injection contract verification must be explicit');
  }
  if (value.capabilities.includes('context-injection') && !value.injection_contract_verified) {
    throw new Error('context injection cannot be enabled without a verified host contract');
  }
  return value as unknown as AdapterManifest;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCapability(value: unknown): value is HostCapability {
  return value === 'capture' || value === 'mcp' || value === 'skill' ||
    value === 'context-injection' || value === 'poll';
}
