// Runs the real build (esbuild.config.mjs) for each target into a temp dir.
// Runtime bundles stay complete and every target carries all applicable notices.
import { describe, it, expect, beforeAll, afterAll, setTestTimeout } from './test-api.js';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { GECKO_ID } from '../../tools/targets.mjs';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const FILES = ['manifest.json', 'sw.js', 'content.js', 'interceptor.js', 'popup.html', 'popup.js', 'options.html', 'options.js'];
const LEGAL_FILES = [
  'LICENSE.agent-beacon',
  'LICENSE.threadforge-apache',
  'NOTICE.threadforge',
  'THIRD_PARTY.md',
  'UPSTREAM_MODIFICATIONS.md',
];

let work: string;
const out = (t: string) => path.join(work, t);

function archiveEntries(filename: string): string[] {
  const archive = readFileSync(filename);
  const minimumEndRecordSize = 22;
  const maximumCommentSize = 0xffff;
  const earliestEndRecord = Math.max(0, archive.length - minimumEndRecordSize - maximumCommentSize);
  let endRecord = -1;

  for (let offset = archive.length - minimumEndRecordSize; offset >= earliestEndRecord; offset -= 1) {
    if (
      archive.readUInt32LE(offset) === 0x06054b50 &&
      offset + minimumEndRecordSize + archive.readUInt16LE(offset + 20) === archive.length
    ) {
      endRecord = offset;
      break;
    }
  }
  if (endRecord < 0) throw new Error(`ZIP end record not found: ${filename}`);

  const entryCount = archive.readUInt16LE(endRecord + 10);
  let offset = archive.readUInt32LE(endRecord + 16);
  const entries: string[] = [];
  for (let index = 0; index < entryCount; index += 1) {
    if (archive.readUInt32LE(offset) !== 0x02014b50) {
      throw new Error(`Invalid ZIP central-directory entry ${index}: ${filename}`);
    }
    const nameLength = archive.readUInt16LE(offset + 28);
    const extraLength = archive.readUInt16LE(offset + 30);
    const commentLength = archive.readUInt16LE(offset + 32);
    entries.push(archive.toString('utf8', offset + 46, offset + 46 + nameLength));
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

beforeAll(() => {
  setTestTimeout(60_000);
  work = mkdtempSync(path.join(tmpdir(), 'beacon-ext-build-'));
  for (const t of ['chrome', 'firefox', 'safari']) {
    execFileSync(process.execPath, ['esbuild.config.mjs', '--target', t, '--outdir', out(t)], {
      cwd: root,
      stdio: 'pipe',
    });
  }
});

afterAll(() => {
  rmSync(work, { recursive: true, force: true });
});

for (const target of ['chrome', 'firefox', 'safari']) {
  describe(`${target} build`, () => {
    for (const file of [...FILES, ...LEGAL_FILES]) {
      it(`emits a non-empty ${file}`, () => {
        const p = path.join(out(target), file);
        expect(existsSync(p)).toBe(true);
        expect(statSync(p).size).toBeGreaterThan(0);
      });
    }
  });
}

describe('chrome build', () => {
  it('ships src/manifest.json byte for byte', () => {
    expect(readFileSync(path.join(out('chrome'), 'manifest.json'))).toEqual(
      readFileSync(path.join(root, 'src', 'manifest.json')),
    );
  });

  it('emits only browser assets and required notices besides source maps', () => {
    const got = readdirSync(out('chrome')).filter((f) => !f.endsWith('.map')).sort();
    expect(got).toEqual([...FILES, ...LEGAL_FILES].sort());
  });

  it('preserves the exact upstream MIT license and repository Apache notices', () => {
    expect(readFileSync(path.join(out('chrome'), 'LICENSE.agent-beacon'))).toEqual(
      readFileSync(path.join(root, 'LICENSE.agent-beacon')),
    );
    expect(readFileSync(path.join(out('chrome'), 'LICENSE.threadforge-apache'))).toEqual(
      readFileSync(path.resolve(root, '..', '..', 'LICENSE')),
    );
    expect(readFileSync(path.join(out('chrome'), 'NOTICE.threadforge'))).toEqual(
      readFileSync(path.resolve(root, '..', '..', 'NOTICE')),
    );
    expect(readFileSync(path.join(out('chrome'), 'THIRD_PARTY.md'))).toEqual(
      readFileSync(path.resolve(root, '..', '..', 'THIRD_PARTY.md')),
    );
    expect(readFileSync(path.join(out('chrome'), 'UPSTREAM_MODIFICATIONS.md'))).toEqual(
      readFileSync(path.join(root, 'UPSTREAM_MODIFICATIONS.md')),
    );
  });
});

describe('safari build', () => {
  it('ships src/manifest.json byte for byte, with the same file set as Chrome', () => {
    expect(readFileSync(path.join(out('safari'), 'manifest.json'))).toEqual(
      readFileSync(path.join(root, 'src', 'manifest.json')),
    );
    const got = readdirSync(out('safari')).filter((f) => !f.endsWith('.map')).sort();
    expect(got).toEqual([...FILES, ...LEGAL_FILES].sort());
  });
});

for (const { target, filename } of [
  { target: 'chrome', filename: 'threadforge-browser-chromium.zip' },
  { target: 'firefox', filename: 'threadforge-browser-firefox.zip' },
]) {
  describe(`${target} unsigned package`, () => {
    it('retains the license, notice, attribution ledger, and source record in the ZIP', () => {
      setTestTimeout(60_000);
      const artifacts = path.join(work, 'artifacts', target);
      mkdirSync(artifacts, { recursive: true });
      const webExt = path.join(root, 'node_modules', 'web-ext', 'bin', 'web-ext.js');
      execFileSync(
        process.execPath,
        [
          webExt,
          'build',
          '--source-dir',
          out(target),
          '--artifacts-dir',
          artifacts,
          '--filename',
          filename,
          '--no-input',
          '--no-config-discovery',
          '--overwrite-dest',
        ],
        { cwd: root, stdio: 'pipe' },
      );

      const entries = archiveEntries(path.join(artifacts, filename));
      expect(entries).toEqual(expect.arrayContaining(LEGAL_FILES));
    });
  });
}

describe('firefox build', () => {
  it('writes the derived Gecko manifest', () => {
    const m = JSON.parse(readFileSync(path.join(out('firefox'), 'manifest.json'), 'utf8'));
    expect(m.background).toEqual({ scripts: ['sw.js'] });
    expect(m.browser_specific_settings.gecko.id).toBe(GECKO_ID);
    const base = JSON.parse(readFileSync(path.join(root, 'src', 'manifest.json'), 'utf8'));
    expect(m.version).toBe(base.version);
  });

  it('bundles the popup with the host-permission banner wired in', () => {
    expect(readFileSync(path.join(out('firefox'), 'popup.js'), 'utf8')).toContain('perm-grant');
    expect(readFileSync(path.join(out('firefox'), 'popup.html'), 'utf8')).toContain('id="perm-grant"');
    expect(readFileSync(path.join(out('firefox'), 'options.html'), 'utf8')).toContain('id="perm-grant"');
  });
});

describe('build CLI', () => {
  it('fails on an unknown target instead of silently building Chrome', () => {
    expect(() =>
      execFileSync(process.execPath, ['esbuild.config.mjs', '--target', 'nope', '--outdir', out('nope')], {
        cwd: root,
        stdio: 'pipe',
      }),
    ).toThrow(/unknown build target "nope"/);
    expect(existsSync(out('nope'))).toBe(false);
  });
});
