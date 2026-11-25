import path from 'node:path';
import { fileURLToPath } from 'node:url';

export interface AdapterCLIEntryPoint {
  scriptPath: string;
  nodeArguments: string[];
}

/** Resolve the source CLI for checkout execution and the compiled bin for installed packages. */
export function resolveAdapterCLIEntry(moduleUrl: string): AdapterCLIEntryPoint {
  const module = resolveModuleURL(moduleUrl);
  const compiled = path.extname(fileURLToPath(module)).toLowerCase() === '.js';
  return {
    scriptPath: fileURLToPath(new URL(compiled ? './bin.js' : './cli.ts', module)),
    nodeArguments: compiled ? [] : ['--experimental-strip-types'],
  };
}

/** Resolve an adapter sibling in the same source/compiled form as its caller. */
export function resolveAdapterSiblingModule(moduleUrl: string, stem: string): string {
  if (!/^[a-z0-9-]+$/i.test(stem)) throw new Error('adapter module name is invalid');
  const module = resolveModuleURL(moduleUrl);
  const compiled = path.extname(fileURLToPath(module)).toLowerCase() === '.js';
  return fileURLToPath(new URL(`./${stem}${compiled ? '.js' : '.ts'}`, module));
}

function resolveModuleURL(value: string): URL {
  let url: URL;
  try { url = new URL(value); }
  catch { throw new Error('adapter module URL is invalid'); }
  if (url.protocol !== 'file:' || url.search || url.hash || url.username || url.password) {
    throw new Error('adapter module URL must be a local file URL');
  }
  return url;
}
