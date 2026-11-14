import { existsSync, lstatSync, realpathSync } from 'node:fs';
import path from 'node:path';

export function canonicalSafeTempRoot(candidatePath, externalUserHome, hostConfigNames) {
  let canonical;
  try {
    canonical = realpathSync(candidatePath);
    const stat = lstatSync(canonical);
    if (!stat.isDirectory() || stat.isSymbolicLink()) return undefined;
  } catch {
    return undefined;
  }
  if (isPathWithin(externalUserHome, canonical) || hasHostConfigAncestor(canonical, hostConfigNames)) {
    return undefined;
  }
  return canonical;
}

export function verifiedIsolatedHome(createdPath, canonicalRoot, externalUserHome, hostConfigNames) {
  let canonical;
  try {
    const stat = lstatSync(createdPath);
    if (!stat.isDirectory() || stat.isSymbolicLink()) return undefined;
    canonical = realpathSync(createdPath);
  } catch {
    return undefined;
  }
  if (!isPathWithin(canonicalRoot, canonical) || isPathWithin(externalUserHome, canonical) ||
      hasHostConfigAncestor(canonical, hostConfigNames)) {
    return undefined;
  }
  return canonical;
}

export function isPathWithin(rootPath, candidatePath) {
  const root = path.resolve(rootPath);
  const candidate = path.resolve(candidatePath);
  const relative = path.relative(root, candidate);
  const compared = process.platform === 'win32' ? relative.toLowerCase() : relative;
  return relative === '' || (
    compared !== '..' && !compared.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)
  );
}

function hasHostConfigAncestor(candidatePath, names) {
  let current = path.resolve(candidatePath);
  while (true) {
    if (names.some((name) => existsSync(path.join(current, name)))) return true;
    const parent = path.dirname(current);
    if (parent === current) return false;
    current = parent;
  }
}
