import { lstat, stat } from 'node:fs/promises';
import path from 'node:path';

/**
 * Confirms that a realpath result names the same filesystem object as the
 * requested path without allowing any path component to be a symlink.
 * Windows may return an 8.3 short-name spelling from realpath even when the
 * input used its long spelling, so compare filesystem identity after walking
 * and rejecting every symlink/junction component instead of comparing paths.
 */
export async function isSameRealPathWithoutLinks(canonical: string, requested: string): Promise<boolean> {
  const absolute = path.resolve(requested);
  if (process.platform !== 'win32') return canonical === absolute;

  const parsed = path.parse(absolute);
  let current = parsed.root;
  try {
    const rootInfo = await lstat(current);
    if (rootInfo.isSymbolicLink()) return false;
    for (const segment of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
      current = path.join(current, segment);
      const info = await lstat(current);
      if (info.isSymbolicLink()) return false;
    }
    const [canonicalInfo, requestedInfo] = await Promise.all([stat(canonical), stat(absolute)]);
    return canonicalInfo.dev === requestedInfo.dev && canonicalInfo.ino === requestedInfo.ino && requestedInfo.ino !== 0;
  } catch {
    return false;
  }
}
