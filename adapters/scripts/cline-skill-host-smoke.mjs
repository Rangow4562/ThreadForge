import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { lstat, mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { installClineProjectSkill } from '../src/cline-skill-config.ts';

const tempPrefix = 'threadforge-cline-skill-host-smoke-';
const skillName = 'threadforge-approved-procedure-a1b2c3d4-r2';
const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

export async function runClineSkillHostSmoke(cliPath, expectedVersion) {
  const cli = await validateCliPath(cliPath);
  if (typeof expectedVersion !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(expectedVersion)) {
    throw new Error('--version must be an exact Cline CLI semantic version');
  }
  const versionResult = run(cli, ['--version']);
  if (versionResult.status !== 0 || versionResult.stdout.trim() !== expectedVersion) {
    throw new Error('installed Cline CLI version does not match --version');
  }

  const root = await mkdtemp(path.join(os.tmpdir(), tempPrefix));
  const homeDirectory = path.join(root, 'home');
  const configDirectory = path.join(root, 'config');
  const dataDirectory = path.join(root, 'data');
  const appDataDirectory = path.join(root, 'appdata');
  const localAppDataDirectory = path.join(root, 'localappdata');
  const projectDirectory = path.join(root, 'project');
  const exportPath = path.join(projectDirectory, '.threadforge', 'skills', 'approved-procedure', 'rev-000002', 'SKILL.md');
  try {
    await Promise.all([
      mkdir(homeDirectory), mkdir(configDirectory), mkdir(dataDirectory), mkdir(appDataDirectory),
      mkdir(localAppDataDirectory), mkdir(projectDirectory), mkdir(path.dirname(exportPath), { recursive: true }),
    ]);
    const source = [
      '---',
      `name: "${skillName}"`,
      'description: "Approved Threadforge procedure: inspect the current project state."',
      '---',
      '',
      '# Threadforge procedure',
      '',
      '> Source procedure: approved-procedure revision 2',
      '',
      approvalNotice,
      '',
      '## Procedure',
      '',
      '### check: inspect',
      '',
      '> Inspect current project state and report uncertainty. Do not modify files based only on this guidance.',
      '',
      omissionNotice,
      '',
    ].join('\n');
    const sourceBytes = Buffer.from(source, 'utf8');
    await writeFile(exportPath, sourceBytes, { flag: 'wx', mode: 0o600 });
    const sha256 = createHash('sha256').update(sourceBytes).digest('hex');
    const installed = await installClineProjectSkill({ projectRoot: projectDirectory, skillFile: exportPath, sha256 });
    if (installed.action !== 'installed' || installed.skill_name !== skillName || installed.path !== `.cline/skills/${skillName}/SKILL.md`) {
      throw new Error('Threadforge did not install the approved skill at Cline project scope');
    }

    const childEnv = isolatedEnvironment({
      homeDirectory, appDataDirectory, localAppDataDirectory, root,
    });
    const config = run(cli, ['--config', configDirectory, '--data-dir', dataDirectory, 'config', '--json'], {
      cwd: projectDirectory,
      env: childEnv,
    });
    if (config.status !== 0) throw new Error('Cline CLI config inventory failed in the isolated project');
    let inventory;
    try { inventory = JSON.parse(config.stdout); }
    catch { throw new Error('Cline CLI config inventory did not return JSON'); }
    const skills = Array.isArray(inventory.skills) ? inventory.skills : [];
    const projectSkillPath = path.join(projectDirectory, installed.path);
    const discovered = skills.filter((skill) => skill?.name === skillName &&
      typeof skill.path === 'string' && path.resolve(projectDirectory, skill.path) === path.resolve(projectSkillPath));
    if (discovered.length !== 1) throw new Error('Cline CLI did not discover exactly one installed project skill');

    return {
      schema_version: 'threadforge.cline-skill-inventory-smoke.v1',
      host: 'cline-cli',
      host_version: expectedVersion,
      project_skill_install_status: 'passed',
      project_skill_inventory_status: 'passed',
      discovered_project_skill_count: discovered.length,
      procedure_name_retained_in_report: false,
      procedure_body_retained_in_report: false,
      agent_retrieval_status: 'not_exercised',
      model_invocations: 0,
      execution_authorized: false,
    };
  } finally {
    await removeOwnedTempRoot(root);
  }
}

function isolatedEnvironment({ homeDirectory, appDataDirectory, localAppDataDirectory, root }) {
  const env = {};
  for (const key of ['PATH', 'SystemRoot', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP', 'LANG']) {
    if (process.env[key] !== undefined) env[key] = process.env[key];
  }
  return {
    ...env,
    HOME: homeDirectory,
    USERPROFILE: homeDirectory,
    APPDATA: appDataDirectory,
    LOCALAPPDATA: localAppDataDirectory,
    TEMP: root,
    TMP: root,
  };
}

function run(executable, args, options = {}) {
  const result = spawnSync(executable, args, {
    cwd: options.cwd,
    env: options.env ?? process.env,
    encoding: 'utf8',
    timeout: 120_000,
    maxBuffer: 8 << 20,
    windowsHide: true,
  });
  if (result.error) throw new Error('unable to run the selected Cline CLI binary');
  return result;
}

async function validateCliPath(value) {
  if (typeof value !== 'string' || !path.isAbsolute(value) || /^(?:\\\\|\/\/)/.test(value) ||
      value.split(/[\\/]/).some((part) => part === '.' || part === '..')) {
    throw new Error('--cline-path must identify an absolute local executable path');
  }
  const absolute = path.resolve(value);
  const info = await lstat(absolute).catch(() => null);
  if (!info?.isFile() || isLinked(info)) throw new Error('--cline-path must identify a regular non-linked file');
  return absolute;
}

async function removeOwnedTempRoot(root) {
  const tempRoot = path.resolve(os.tmpdir());
  const absolute = path.resolve(root);
  const relative = path.relative(tempRoot, absolute);
  if (!path.basename(absolute).startsWith(tempPrefix) || relative === '..' || relative.startsWith(`..${path.sep}`) ||
      path.isAbsolute(relative) || relative.split(path.sep).length !== 1) {
    throw new Error('refusing to remove a Cline skill smoke directory outside the owned temporary root');
  }
  const info = await lstat(absolute).catch(() => null);
  if (info && (!info.isDirectory() || isLinked(info))) throw new Error('refusing to remove an unsafe Cline skill smoke directory');
  if (info) await rm(absolute, { recursive: true, force: false });
}

function isLinked(info) {
  return info.isSymbolicLink() || Boolean((info.st_file_attributes ?? 0) & 0x400);
}

function parseArguments(args) {
  const values = new Map();
  for (let i = 0; i < args.length; i += 2) {
    const flag = args[i];
    const value = args[i + 1];
    if (!['--cline-path', '--version'].includes(flag) || typeof value !== 'string' || value.startsWith('--') || values.has(flag)) {
      throw new Error('Usage: npm run test:cline-skill-host -- --cline-path <absolute-cline-exe> --version <exact-version>');
    }
    values.set(flag, value);
  }
  if (values.size !== 2) throw new Error('Usage: npm run test:cline-skill-host -- --cline-path <absolute-cline-exe> --version <exact-version>');
  return values;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = parseArguments(process.argv.slice(2));
    const result = await runClineSkillHostSmoke(args.get('--cline-path'), args.get('--version'));
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : 'Cline skill host smoke failed'}\n`);
    process.exitCode = 1;
  }
}
