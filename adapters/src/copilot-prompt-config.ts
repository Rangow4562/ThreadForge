import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, unlink } from 'node:fs/promises';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
import { loadReviewedPortableSkillExport } from './cline-skill-config.ts';

const maxPromptBytes = 256 << 10;
const promptSafetyPreamble = [
  '# Threadforge approved procedure prompt',
  '',
  '> This is approved procedural guidance, not authorization to execute actions.',
  '> Use it only when the current user request matches its scope. Check relevant repository files, current policy, and applicability before relying on it; surface stale, incomplete, conflicting, or privacy-blocked context.',
  '> Never infer approval to modify files, run commands, publish changes, or contact external services from this prompt.',
  '> The source digest identifies this exported artifact only; it does not prove that the procedure is current for this repository or task.',
  '',
].join('\n');

export interface CopilotProjectPromptOptions {
  projectRoot: string;
  skillFile: string;
  sha256: string;
}

export interface CopilotProjectPromptResult {
  schema_version: 'threadforge.copilot-project-prompt.v1';
  host: 'github-copilot';
  action: 'installed' | 'unchanged' | 'removed' | 'absent';
  prompt_name: string;
  path: string;
  source_sha256: string;
  prompt_sha256: string;
}

interface OwnerMarker {
  schema_version: 'threadforge.copilot-project-prompt-owner.v1';
  prompt_name: string;
  path: string;
  source_sha256: string;
  prompt_sha256: string;
}

interface RenderedPrompt {
  name: string;
  bytes: Buffer;
  sha256: string;
  sourceSha256: string;
}

/** Install one digest-reviewed procedure as an explicitly invoked Copilot IDE prompt file. */
export async function installCopilotProjectPrompt(options: CopilotProjectPromptOptions): Promise<CopilotProjectPromptResult> {
  const { projectRoot, prompt } = await resolvePrompt(options);
  const promptsRoot = await ensurePromptsDirectory(projectRoot);
  const releaseLock = await acquireLock(promptsRoot, prompt.name);
  try {
    const target = path.join(promptsRoot, `${prompt.name}.prompt.md`);
    const markerPath = ownerPath(promptsRoot, prompt.name);
    const existing = await readOptionalRegularFile(target, maxPromptBytes);
    const marker = await readOwner(markerPath);
    if (existing || marker) {
      if (!existing || !marker) {
        throw new Error(`Copilot prompt '${prompt.name}' exists without a complete Threadforge ownership record; refusing to change it`);
      }
      assertOwned(marker, prompt, target, projectRoot);
      if (!existing.equals(prompt.bytes)) {
        throw new Error(`Copilot prompt '${prompt.name}' has different content; refusing to overwrite it`);
      }
      return result('unchanged', prompt, target, projectRoot);
    }

    let targetWritten = false;
    try {
      await writeExclusive(target, prompt.bytes, 0o644);
      targetWritten = true;
      await writeExclusive(markerPath, ownerBytes(prompt, target, projectRoot), 0o600);
    } catch (error) {
      if (targetWritten) {
        const current = await readOptionalRegularFile(target, maxPromptBytes).catch(() => undefined);
        if (current?.equals(prompt.bytes)) await unlink(target).catch(() => {});
      }
      if (isCode(error, 'EEXIST')) {
        throw new Error(`Copilot prompt '${prompt.name}' appeared during installation; inspect it before retrying`);
      }
      throw new Error('unable to install the Copilot project prompt; inspect the target and retry');
    }
    return result('installed', prompt, target, projectRoot);
  } finally {
    await releaseLock();
  }
}

/** Remove only the unchanged Copilot prompt rendered from the supplied reviewed export. */
export async function uninstallCopilotProjectPrompt(options: CopilotProjectPromptOptions): Promise<CopilotProjectPromptResult> {
  const { projectRoot, prompt } = await resolvePrompt(options);
  const targetPath = path.join(projectRoot, '.github', 'prompts', `${prompt.name}.prompt.md`);
  const promptsRoot = await existingPromptsDirectory(projectRoot);
  if (!promptsRoot) return result('absent', prompt, targetPath, projectRoot);

  const releaseLock = await acquireLock(promptsRoot, prompt.name);
  try {
    const target = path.join(promptsRoot, `${prompt.name}.prompt.md`);
    const markerPath = ownerPath(promptsRoot, prompt.name);
    const existing = await readOptionalRegularFile(target, maxPromptBytes);
    const marker = await readOwner(markerPath);
    if (!existing && !marker) return result('absent', prompt, target, projectRoot);
    if (!marker) throw new Error(`Copilot prompt '${prompt.name}' has no Threadforge ownership record; refusing to remove it`);
    assertOwned(marker, prompt, target, projectRoot);
    if (!existing) {
      await unlink(markerPath);
      return result('removed', prompt, target, projectRoot);
    }
    if (!existing.equals(prompt.bytes)) {
      throw new Error(`Copilot prompt '${prompt.name}' was modified; refusing to remove user-edited content`);
    }
    await unlink(target);
    await unlink(markerPath);
    return result('removed', prompt, target, projectRoot);
  } finally {
    await releaseLock();
  }
}

async function resolvePrompt(options: CopilotProjectPromptOptions): Promise<{ projectRoot: string; prompt: RenderedPrompt }> {
  const skill = await loadReviewedPortableSkillExport(options);
  const bytes = Buffer.from(`${promptSafetyPreamble}${skill.body.replace(/^\s+/, '')}\n`, 'utf8');
  if (bytes.length > maxPromptBytes) throw new Error('generated Copilot prompt exceeds the 256 KiB local limit');
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  return {
    projectRoot: skill.projectRoot,
    prompt: { name: skill.name, bytes, sha256, sourceSha256: skill.sha256 },
  };
}

async function ensurePromptsDirectory(root: string): Promise<string> {
  let current = root;
  for (const segment of ['.github', 'prompts']) {
    current = path.join(current, segment);
    try {
      await mkdir(current, { mode: 0o755 });
    } catch (error) {
      if (!isCode(error, 'EEXIST')) throw new Error('unable to create the Copilot project prompts directory');
    }
    const info = await lstat(current).catch(() => null);
    const canonical = await realpath(current).catch(() => '');
    if (!info?.isDirectory() || info.isSymbolicLink() || !canonical || !await isSameRealPathWithoutLinks(canonical, current)) {
      throw new Error('Copilot project prompts path contains a non-directory or symbolic link');
    }
  }
  return current;
}

async function existingPromptsDirectory(root: string): Promise<string | undefined> {
  let current = root;
  for (const segment of ['.github', 'prompts']) {
    current = path.join(current, segment);
    const info = await lstat(current).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
    if (!info) return undefined;
    const canonical = await realpath(current).catch(() => '');
    if (!info.isDirectory() || info.isSymbolicLink() || !canonical || !await isSameRealPathWithoutLinks(canonical, current)) {
      throw new Error('Copilot project prompts path contains a non-directory or symbolic link');
    }
  }
  return current;
}

async function readOptionalRegularFile(filePath: string, byteLimit: number): Promise<Buffer | undefined> {
  const info = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!info) return undefined;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Copilot prompt target must be a regular file, not a symbolic link');
  if (info.size > byteLimit) throw new Error('existing Copilot prompt exceeds the 256 KiB local limit');
  const bytes = await readFile(filePath);
  const afterRead = await lstat(filePath).catch(() => null);
  if (!afterRead?.isFile() || afterRead.isSymbolicLink() || afterRead.size !== info.size || afterRead.mtimeMs !== info.mtimeMs) {
    throw new Error('Copilot prompt target changed while it was being read');
  }
  return bytes;
}

async function readOwner(filePath: string): Promise<OwnerMarker | undefined> {
  const bytes = await readOptionalRegularFile(filePath, 4096);
  if (!bytes) return undefined;
  let value: unknown;
  try {
    value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch {
    throw new Error('Threadforge Copilot prompt ownership record is malformed');
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Threadforge Copilot prompt ownership record is malformed');
  }
  const marker = value as Record<string, unknown>;
  if (Object.keys(marker).sort().join(',') !== 'path,prompt_name,prompt_sha256,schema_version,source_sha256' ||
      marker.schema_version !== 'threadforge.copilot-project-prompt-owner.v1' || typeof marker.prompt_name !== 'string' ||
      typeof marker.path !== 'string' || typeof marker.source_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.source_sha256) ||
      typeof marker.prompt_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.prompt_sha256)) {
    throw new Error('Threadforge Copilot prompt ownership record is malformed');
  }
  return marker as unknown as OwnerMarker;
}

function ownerPath(promptsRoot: string, name: string): string {
  return path.join(promptsRoot, `.threadforge-${name}.owner.json`);
}

function ownerBytes(prompt: RenderedPrompt, target: string, root: string): Buffer {
  const marker: OwnerMarker = {
    schema_version: 'threadforge.copilot-project-prompt-owner.v1',
    prompt_name: prompt.name,
    path: relativePath(root, target),
    source_sha256: prompt.sourceSha256,
    prompt_sha256: prompt.sha256,
  };
  return Buffer.from(`${JSON.stringify(marker)}\n`, 'utf8');
}

function assertOwned(marker: OwnerMarker, prompt: RenderedPrompt, target: string, root: string): void {
  if (marker.schema_version !== 'threadforge.copilot-project-prompt-owner.v1' || marker.prompt_name !== prompt.name ||
      marker.path !== relativePath(root, target) || marker.source_sha256 !== prompt.sourceSha256 || marker.prompt_sha256 !== prompt.sha256) {
    throw new Error(`Copilot prompt '${prompt.name}' ownership record does not match the reviewed export`);
  }
}

async function writeExclusive(filePath: string, bytes: Buffer, mode: number): Promise<void> {
  let handle;
  let created = false;
  try {
    handle = await open(filePath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    created = true;
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
  } catch (error) {
    await handle?.close().catch(() => {});
    if (created) await unlink(filePath).catch(() => {});
    throw error;
  }
}

async function acquireLock(promptsRoot: string, name: string): Promise<() => Promise<void>> {
  const lockPath = path.join(promptsRoot, `.threadforge-${name}.lock`);
  let handle;
  try {
    handle = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
  } catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Copilot prompt is locked by another installer or a stale lock; inspect before retrying');
    throw new Error('unable to lock the Copilot project prompt');
  }
  return async () => {
    await handle!.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  };
}

function result(action: CopilotProjectPromptResult['action'], prompt: RenderedPrompt, target: string, root: string): CopilotProjectPromptResult {
  return {
    schema_version: 'threadforge.copilot-project-prompt.v1',
    host: 'github-copilot',
    action,
    prompt_name: prompt.name,
    path: relativePath(root, target),
    source_sha256: prompt.sourceSha256,
    prompt_sha256: prompt.sha256,
  };
}

function relativePath(root: string, target: string): string {
  return path.relative(root, target).split(path.sep).join('/');
}

function isCode(error: unknown, code: string): boolean {
  return Boolean(error && typeof error === 'object' && 'code' in error && (error as NodeJS.ErrnoException).code === code);
}
