import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const compiler = path.join(packageRoot, 'node_modules', 'typescript', 'bin', 'tsc');
const build = spawnSync(process.execPath, [compiler, '-p', path.join(packageRoot, 'tsconfig.package.json')], {
  cwd: packageRoot,
  stdio: 'inherit',
});

if (build.error) throw new Error('TypeScript package build could not be started');
if (build.status !== 0) process.exit(build.status ?? 1);

const outputDirectory = path.join(packageRoot, 'dist');
mkdirSync(outputDirectory, { recursive: true });
copyFileSync(
  path.join(packageRoot, 'src', 'senpi-extension.ts'),
  path.join(outputDirectory, 'senpi-extension.ts'),
);
