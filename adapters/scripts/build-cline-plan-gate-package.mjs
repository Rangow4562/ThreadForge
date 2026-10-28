import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, realpath, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const maxSourceBytes = 512 << 10;
const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export async function buildClinePlanGatePackage(outputDirectory, options) {
  const outputPath = await validateNewOutputDirectory(outputDirectory);
  const config = await validateConfiguration(options);
  const [pluginSource, adapterManifest, license, notice, thirdParty] = await Promise.all([
    readStableSource(path.join(projectRoot, 'adapters', 'src', 'cline-plan-gate-plugin.ts')),
    readStableSource(path.join(projectRoot, 'adapters', 'package.json')),
    readStableSource(path.join(projectRoot, 'LICENSE')),
    readStableSource(path.join(projectRoot, 'NOTICE')),
    readStableSource(path.join(projectRoot, 'THIRD_PARTY.md')),
  ]);
  const manifest = JSON.parse(adapterManifest.toString('utf8'));
  if (typeof manifest.version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(manifest.version)) {
    throw new Error('adapter package version is invalid');
  }

  const pluginJavaScript = transpile(pluginSource.toString('utf8'), 'cline-plan-gate-plugin.ts');
  if (pluginJavaScript.includes('.ts"') || pluginJavaScript.includes(".ts'")) {
    throw new Error('Cline task-plan gate package contains an unresolved TypeScript import');
  }
  for (const specifier of runtimeImportSpecifiers(pluginJavaScript)) {
    if (!specifier.startsWith('node:')) throw new Error('Cline task-plan gate package has an external runtime dependency');
  }

  const configJson = JSON.stringify(config, null, 2);
  const indexJavaScript = [
    'import { createThreadforgeClinePlanGatePlugin } from "./cline-plan-gate-plugin.js";',
    `const plugin = createThreadforgeClinePlanGatePlugin(${configJson});`,
    'export { plugin };',
    'export default plugin;',
    '',
  ].join('\n');
  const packageManifest = {
    name: 'threadforge-cline-task-plan-gate',
    version: manifest.version,
    private: true,
    type: 'module',
    main: './index.js',
    license: 'Apache-2.0',
    engines: { node: '>=22.15' },
    files: ['index.js', 'cline-plan-gate-plugin.js', 'README.md', 'LICENSE', 'NOTICE', 'THIRD_PARTY.md'],
    threadforge: {
      schema_version: 'threadforge.cline-task-plan-gate-package.v1',
      source_sha256: sha256(pluginSource),
      config_sha256: sha256(Buffer.from(configJson, 'utf8')),
      bundle_sha256: sha256(Buffer.from(indexJavaScript + '\0' + pluginJavaScript, 'utf8')),
    },
  };
  const readme = [
    '# Threadforge Cline task-plan gate',
    '',
    'This locally generated Cline CLI plugin uses the Cline `beforeTool` hook to check the configured Threadforge task, project, and file-scope policy before the `editor` and `apply_patch` tools run. In-scope operations return no hook decision; denied, malformed, unavailable, or timed-out checks skip the call.',
    '',
    'The package is bound to the task, project, project root, and optional database selected at build time. Regenerate it when the active task changes. The package contains local identifiers and filesystem paths; keep it local and do not commit or share it.',
    '',
    'Build from the Threadforge checkout with `npm run build:cline-plan-gate -- --output <new-absolute-directory> --task-id <task-id> --project-id <project-id> --project-root <absolute-project-root>`. Optional flags are `--db <absolute-sqlite-path>`, `--threadforge-bin <command-or-path>`, and `--timeout-ms <100-10000>` (default 5000 ms).',
    '',
    'Install the generated local package for the project with `cline plugin install <package-directory> --cwd <project-root>`. Review the generated manifest and hashes first. This package is unsigned and is not a public registry release.',
    '',
    'The gate covers only Cline `editor` and `apply_patch` direct file operations. It does not constrain shell commands, custom tools, other Cline products, or host-level approvals. It is an opt-in best-effort workflow check, not a security boundary or execution authorization.',
    '',
  ].join('\n');
  const files = new Map([
    ['package.json', Buffer.from(`${JSON.stringify(packageManifest, null, 2)}\n`, 'utf8')],
    ['index.js', Buffer.from(indexJavaScript, 'utf8')],
    ['cline-plan-gate-plugin.js', Buffer.from(pluginJavaScript, 'utf8')],
    ['README.md', Buffer.from(readme, 'utf8')],
    ['LICENSE', license],
    ['NOTICE', notice],
    ['THIRD_PARTY.md', thirdParty],
  ]);

  await mkdir(outputPath);
  for (const [name, contents] of files) {
    await writeFile(path.join(outputPath, name), contents, { flag: 'wx', mode: 0o600 });
  }
  return {
    schema_version: 'threadforge.cline-task-plan-gate-package-build.v1',
    package_name: packageManifest.name,
    package_version: packageManifest.version,
    bundle_sha256: packageManifest.threadforge.bundle_sha256,
    config_sha256: packageManifest.threadforge.config_sha256,
    file_count: files.size,
    output_directory: outputPath,
  };
}

async function validateConfiguration(options) {
  if (!options || typeof options !== 'object' || Array.isArray(options)) throw new Error('Cline task-plan gate configuration is required');
  for (const [key, value] of [['taskId', options.taskId], ['projectId', options.projectId]]) {
    if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)) throw new Error(`Cline ${key} is invalid`);
  }
  const projectRootInput = validateAbsoluteLocalPath(options.projectRoot, 'Cline project root');
  const canonicalRoot = await resolveWithoutLinks(projectRootInput);
  const rootInfo = await lstat(canonicalRoot ?? '').catch(() => null);
  if (!canonicalRoot || !rootInfo?.isDirectory() || isLinked(rootInfo)) throw new Error('Cline project root must be an existing local directory without linked path components');

  let databasePath;
  if (options.databasePath !== undefined) databasePath = validateAbsoluteLocalPath(options.databasePath, 'Threadforge database path');
  const threadforgeCommand = options.threadforgeCommand ?? 'threadforge';
  if (typeof threadforgeCommand !== 'string' || threadforgeCommand.trim() === '' || threadforgeCommand.length > 4096 || /[\u0000-\u001f\u007f]/.test(threadforgeCommand)) {
    throw new Error('Threadforge command is invalid');
  }
  const timeoutMs = options.timeoutMs ?? 5000;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 10_000) throw new Error('Cline task-plan gate timeout must be between 100 and 10000 ms');
  return {
    taskId: options.taskId,
    projectId: options.projectId,
    projectRoot: canonicalRoot,
    ...(databasePath ? { databasePath } : {}),
    threadforgeCommand,
    timeoutMs,
  };
}

function validateAbsoluteLocalPath(value, label) {
  if (typeof value !== 'string' || !path.isAbsolute(value) || /^(?:\\\\|\/\/)/.test(value) || value.split(/[\\/]/).some((part) => part === '.' || part === '..')) {
    throw new Error(`${label} must be an absolute local path without traversal components`);
  }
  return path.resolve(value);
}

async function resolveWithoutLinks(value) {
  const absolute = path.resolve(value);
  const parsed = path.parse(absolute);
  let current = parsed.root;
  const rootInfo = await lstat(current).catch(() => undefined);
  if (!rootInfo || isLinked(rootInfo)) return undefined;
  let canonical = await realpath(current).catch(() => undefined);
  if (!canonical) return undefined;
  for (const component of absolute.slice(parsed.root.length).split(path.sep).filter(Boolean)) {
    current = path.join(current, component);
    const info = await lstat(current).catch(() => undefined);
    if (!info || isLinked(info)) return undefined;
    canonical = await realpath(current).catch(() => undefined);
    if (!canonical) return undefined;
  }
  return canonical;
}

async function validateNewOutputDirectory(value) {
  if (typeof value !== 'string' || value.trim() === '' || value.length > 4096 || !path.isAbsolute(value) ||
      /^(?:\\\\|\/\/)/.test(value) || value.split(/[\\/]/).some((part) => part === '.' || part === '..')) {
    throw new Error('Cline package output must be a new absolute local directory without traversal components');
  }
  const outputPath = path.resolve(value);
  if (outputPath === path.parse(outputPath).root) throw new Error('refusing to build a Cline package at a filesystem root');
  let current = path.parse(outputPath).root;
  const components = outputPath.slice(current.length).split(path.sep).filter(Boolean);
  for (const component of components.slice(0, -1)) {
    current = path.join(current, component);
    const info = await lstat(current).catch(() => null);
    if (!info?.isDirectory() || isLinked(info)) throw new Error('Cline package output parent must exist and contain no linked components');
  }
  if (await lstat(outputPath).catch(() => null)) throw new Error('Cline package output directory already exists; refusing to overwrite it');
  return outputPath;
}

async function readStableSource(filePath) {
  const info = await lstat(filePath).catch(() => null);
  if (!info?.isFile() || isLinked(info) || info.size > maxSourceBytes) throw new Error('Cline package source must be a bounded regular file');
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('source changed');
    const contents = await handle.readFile();
    const after = await handle.stat();
    const pathAfter = await lstat(filePath);
    if (!sameFile(opened, after) || !sameFile(after, pathAfter) || contents.byteLength !== info.size) throw new Error('source changed');
    return contents;
  } catch {
    throw new Error('Cline package source could not be read consistently');
  } finally {
    await handle?.close().catch(() => {});
  }
}

function transpile(source, filename) {
  const result = ts.transpileModule(source, {
    fileName: filename,
    reportDiagnostics: true,
    compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022, sourceMap: false, removeComments: false },
  });
  const errors = (result.diagnostics ?? []).filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error);
  if (errors.length) throw new Error(errors.map((error) => ts.flattenDiagnosticMessageText(error.messageText, '\n')).join('; '));
  return result.outputText;
}

function runtimeImportSpecifiers(source) {
  return [...source.matchAll(/\bfrom\s*["']([^"']+)["']/g)].map((match) => match[1]);
}

function sameFile(left, right) {
  return left.isFile() && right.isFile() && !isLinked(right) && left.dev === right.dev && left.ino === right.ino &&
    left.size === right.size && left.mtimeMs === right.mtimeMs;
}

function isLinked(info) {
  return info.isSymbolicLink() || Boolean((info.st_file_attributes ?? 0) & 0x400);
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length === 1 && ['--help', '-h'].includes(args[0])) {
    process.stdout.write('Usage: npm run build:cline-plan-gate -- --output <new-absolute-directory> --task-id <id> --project-id <id> --project-root <absolute-path> [--db <absolute-path>] [--threadforge-bin <command>] [--timeout-ms <100-10000>]\n');
    return 0;
  }
  const values = new Map();
  for (let index = 0; index < args.length; index += 2) {
    const key = args[index];
    const value = args[index + 1];
    if (!key?.startsWith('--') || !value || values.has(key)) throw new Error('invalid or duplicate Cline task-plan gate build option');
    values.set(key, value);
  }
  const allowed = new Set(['--output', '--task-id', '--project-id', '--project-root', '--db', '--threadforge-bin', '--timeout-ms']);
  if ([...values.keys()].some((key) => !allowed.has(key))) throw new Error('unsupported Cline task-plan gate build option');
  const required = (key) => {
    const value = values.get(key);
    if (!value) throw new Error(`${key} is required`);
    return value;
  };
  const result = await buildClinePlanGatePackage(required('--output'), {
    taskId: required('--task-id'),
    projectId: required('--project-id'),
    projectRoot: required('--project-root'),
    ...(values.has('--db') ? { databasePath: required('--db') } : {}),
    ...(values.has('--threadforge-bin') ? { threadforgeCommand: required('--threadforge-bin') } : {}),
    ...(values.has('--timeout-ms') ? { timeoutMs: Number(required('--timeout-ms')) } : {}),
  });
  process.stdout.write(`${JSON.stringify(result)}\n`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : 'Cline task-plan gate package build failed'}\n`);
    process.exitCode = 1;
  });
}
