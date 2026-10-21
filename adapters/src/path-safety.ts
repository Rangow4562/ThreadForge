import { lstat, realpath } from 'node:fs/promises';
import path from 'node:path';

/**
 * Confirms that a realpath result names the same filesystem object as the
 * requested path without allowing any path component to be a symlink.
 * Windows may return an 8.3 short-name spelling from realpath even when the
 * input used its long spelling, so compare filesystem identity after walking
 * and rejecting every symlink/junction component instead of comparing paths.
 */
export async function isSameRealPathWithoutLinks(canonical: string, requested: string): Promise<boolean> {
  const [canonicalPath, requestedPath] = await Promise.all([
    resolveRealPathWithoutLinks(canonical), resolveRealPathWithoutLinks(requested),
  ]);
  return canonicalPath !== undefined && requestedPath !== undefined && sameCanonicalPath(canonicalPath, requestedPath);
}

/** Resolves existing ancestors to filesystem spelling and appends any missing suffix safely. */
export async function resolveRealPathWithoutLinks(input: string): Promise<string | undefined> {
  const absolute = path.resolve(input);
  const parsed = path.parse(absolute);
  let lexical = parsed.root;
  const rootInfo = await lstat(parsed.root).catch(() => undefined);
  if (!rootInfo || rootInfo.isSymbolicLink()) return undefined;
  let canonical = await realpath(parsed.root).catch(() => undefined);
  if (!canonical) return undefined;

  const segments = absolute.slice(parsed.root.length).split(path.sep).filter(Boolean);
  for (let index = 0; index < segments.length; index++) {
    lexical = path.join(lexical, segments[index]!);
    let info;
    try { info = await lstat(lexical); }
    catch (error) {
      if (isCode(error, 'ENOENT')) return path.join(canonical, ...segments.slice(index));
      return undefined;
    }
    if (info.isSymbolicLink()) return undefined;
    const resolved = await realpath(lexical).catch(() => undefined);
    if (!resolved) return undefined;
    canonical = resolved;
  }
  return canonical;
}

/** Returns true/false for safe paths, or undefined if either path is unsafe or inaccessible. */
export async function isWithinRealPaths(root: string, candidate: string): Promise<boolean | undefined> {
  const [canonicalRoot, canonicalCandidate] = await Promise.all([
    resolveRealPathWithoutLinks(root), resolveRealPathWithoutLinks(candidate),
  ]);
  if (canonicalRoot === undefined || canonicalCandidate === undefined) return undefined;
  const relative = path.relative(canonicalRoot, canonicalCandidate);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}

/** Returns undefined when either path is inaccessible or traverses a link. */
export async function areSameRealPathsWithoutLinks(left: string, right: string): Promise<boolean | undefined> {
  const [canonicalLeft, canonicalRight] = await Promise.all([
    resolveRealPathWithoutLinks(left), resolveRealPathWithoutLinks(right),
  ]);
  if (canonicalLeft === undefined || canonicalRight === undefined) return undefined;
  return sameCanonicalPath(canonicalLeft, canonicalRight);
}

function sameCanonicalPath(left: string, right: string): boolean {
  return process.platform === 'win32' ? left.toLowerCase() === right.toLowerCase() : left === right;
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && (error as NodeJS.ErrnoException).code === code;
}
