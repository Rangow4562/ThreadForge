import { createHash } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const maxSourceBytes = 512 << 10;
const projectRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

export async function buildClinePluginPackage(outputDirectory) {
  const outputPath = await validateNewOutputDirectory(outputDirectory);
  const adapterRoot = path.join(projectRoot, 'adapters');
  const [pluginSource, emitSource, adapterManifest, license, notice, thirdParty] = await Promise.all([
    readStableSource(path.join(adapterRoot, 'src', 'cline-plugin.ts')),
    readStableSource(path.join(adapterRoot, 'src', 'emit.ts')),
    readStableSource(path.join(adapterRoot, 'package.json')),
    readStableSource(path.join(projectRoot, 'LICENSE')),
    readStableSource(path.join(projectRoot, 'NOTICE')),
    readStableSource(path.join(projectRoot, 'THIRD_PARTY.md')),
  ]);
  const manifest = JSON.parse(adapterManifest.toString('utf8'));
  if (typeof manifest.version !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(manifest.version)) {
    throw new Error('adapter package version is invalid');
  }

  const indexJavaScript = transpile(pluginSource.toString('utf8'), 'cline-plugin.ts')
    .replaceAll('"./emit.ts"', '"./emit.js"')
    .replaceAll("'./emit.ts'", "'./emit.js'");
  const emitJavaScript = transpile(emitSource.toString('utf8'), 'emit.ts');
  if (indexJavaScript.includes('.ts"') || indexJavaScript.includes(".ts'") ||
      emitJavaScript.includes('.ts"') || emitJavaScript.includes(".ts'")) {
    throw new Error('Cline plugin package contains an unresolved TypeScript import');
  }
  for (const [filename, contents] of [['index.js', indexJavaScript], ['emit.js', emitJavaScript]]) {
    for (const specifier of runtimeImportSpecifiers(contents)) {
      if (!specifier.startsWith('.') && !specifier.startsWith('node:')) {
        throw new Error(`Cline plugin package has an external runtime import in ${filename}`);
      }
    }
  }

  const sourceHashes = {
    'cline-plugin.ts': sha256(pluginSource),
    'emit.ts': sha256(emitSource),
  };
  const packageManifest = {
    name: 'threadforge-observational-cline-plugin',
    version: manifest.version,
    private: true,
    type: 'module',
    main: './index.js',
    license: 'Apache-2.0',
    engines: { node: '>=22.15' },
    files: ['index.js', 'emit.js', 'README.md', 'LICENSE', 'NOTICE', 'THIRD_PARTY.md'],
    threadforge: {
      schema_version: 'threadforge.cline-plugin-package.v1',
      source_sha256: sourceHashes,
      bundle_sha256: sha256(Buffer.from(indexJavaScript + '\0' + emitJavaScript, 'utf8')),
    },
  };
  const readme = [
    '# Threadforge Cline observational plugin',
    '',
    'This generated local package contains the Threadforge Cline callback adapter and its runtime dependency. It has no provider, telemetry-exporter, or other third-party runtime dependencies.',
    '',
    'Install into a project with the Cline CLI using `cline plugin install --cwd <project-root> <this-directory>`. The plugin observes callbacks only; it does not approve, deny, or rewrite host actions. It sends privacy-minimized events to the local Threadforge loopback receiver.',
    '',
    'The package is generated from the current Threadforge checkout. Review the generated manifest and hashes before installing it. This package is not signed and is not a public registry release.',
    '',
  ].join('\n');
  const files = new Map([
    ['package.json', Buffer.from(`${JSON.stringify(packageManifest, null, 2)}\n`, 'utf8')],
    ['index.js', Buffer.from(indexJavaScript, 'utf8')],
    ['emit.js', Buffer.from(emitJavaScript, 'utf8')],
    ['README.md', Buffer.from(readme, 'utf8')],
    ['LICENSE', license],
    ['NOTICE', notice],
    ['THIRD_PARTY.md', thirdParty],
  ]);

  await mkdir(outputPath);
  for (const [name, contents] of files) {
    await writeFile(path.join(outputPath, name), contents, { flag: 'wx', mode: 0o644 });
  }
  return {
    schema_version: 'threadforge.cline-plugin-package-build.v1',
    package_name: packageManifest.name,
    package_version: packageManifest.version,
    bundle_sha256: packageManifest.threadforge.bundle_sha256,
    file_count: files.size,
    output_directory: outputPath,
  };
}

function transpile(source, filename) {
  const result = ts.transpileModule(source, {
    fileName: filename,
    reportDiagnostics: true,
    compilerOptions: {
      module: ts.ModuleKind.ES2022,
      target: ts.ScriptTarget.ES2022,
      sourceMap: false,
      inlineSourceMap: false,
      removeComments: false,
    },
  });
  const errors = (result.diagnostics ?? []).filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error);
  if (errors.length) {
    throw new Error(errors.map((error) => ts.flattenDiagnosticMessageText(error.messageText, '\n')).join('; '));
  }
  return result.outputText;
}

function runtimeImportSpecifiers(source) {
  return [...source.matchAll(/\bfrom\s*["']([^"']+)["']/g)].map((match) => match[1]);
}

async function validateNewOutputDirectory(value) {
  if (typeof value !== 'string' || value.trim() === '' || value.length > 4096 || !path.isAbsolute(value) ||
      /^(?:\\\\|\/\/)/.test(value) || value.split(/[\\/]/).some((part) => part === '.' || part === '..')) {
    throw new Error('Cline plugin package output must be a new absolute local directory without traversal components');
  }
  const outputPath = path.resolve(value);
  const parsed = path.parse(outputPath);
  if (outputPath === parsed.root) throw new Error('refusing to build a Cline plugin package at a filesystem root');

  let current = parsed.root;
  const components = outputPath.slice(parsed.root.length).split(path.sep).filter(Boolean);
  for (const component of components.slice(0, -1)) {
    current = path.join(current, component);
    const info = await lstat(current).catch(() => null);
    if (!info?.isDirectory() || isLinked(info)) {
      throw new Error('Cline plugin package output parent must already exist and contain no linked components');
    }
  }
  const existing = await lstat(outputPath).catch(() => null);
  if (existing) throw new Error('Cline plugin package output directory already exists; refusing to overwrite it');
  return outputPath;
}

async function readStableSource(filePath) {
  const info = await lstat(filePath).catch(() => null);
  if (!info?.isFile() || isLinked(info) || info.size > maxSourceBytes) {
    throw new Error('Cline plugin package source must be a bounded regular file');
  }
  let handle;
  try {
    handle = await open(filePath, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const opened = await handle.stat();
    if (!sameFile(info, opened)) throw new Error('source changed');
    const contents = await handle.readFile();
    const after = await handle.stat();
    const pathAfter = await lstat(filePath);
    if (!sameFile(opened, after) || !sameFile(after, pathAfter) || contents.byteLength !== info.size) {
      throw new Error('source changed');
    }
    return contents;
  } catch {
    throw new Error('Cline plugin package source could not be read consistently');
  } finally {
    await handle?.close().catch(() => {});
  }
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
    process.stdout.write('Usage: npm run build:cline-plugin -- --output <new-absolute-directory>\n');
    return 0;
  }
  if (args.length !== 2 || args[0] !== '--output') {
    process.stderr.write('Usage: npm run build:cline-plugin -- --output <new-absolute-directory>\n');
    return 2;
  }
  const result = await buildClinePluginPackage(args[1]);
  process.stdout.write(`${JSON.stringify(result)}\n`);
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : 'Cline plugin package build failed'}\n`);
    process.exitCode = 1;
  });
}
