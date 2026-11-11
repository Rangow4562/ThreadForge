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
const memorySkillID = 'threadforge-memory-continuity';
const memorySkillRevision = 2;
const memoryApprovalNotice = '> This portable memory skill provides advisory guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

export async function runClineSkillHostSmoke(cliPath, expectedVersion, { runCommand = run, processCleanup = {} } = {}) {
  const cli = await validateCliPath(cliPath);
  if (typeof expectedVersion !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(expectedVersion)) {
    throw new Error('--version must be an exact Cline CLI semantic version');
  }
  const versionResult = runCommand(cli, ['--version']);
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
  const memorySkillName = `threadforge-${memorySkillID}-${createHash('sha256').update(memorySkillID).digest('hex').slice(0, 8)}-r${memorySkillRevision}`;
  const memoryExportPath = path.join(projectDirectory, '.threadforge', 'skills', memorySkillID,
    `rev-${String(memorySkillRevision).padStart(6, '0')}`, 'SKILL.md');
  try {
    await Promise.all([
      mkdir(homeDirectory), mkdir(configDirectory), mkdir(dataDirectory), mkdir(appDataDirectory),
      mkdir(localAppDataDirectory), mkdir(path.dirname(exportPath), { recursive: true }),
      mkdir(path.dirname(memoryExportPath), { recursive: true }),
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

    const memorySource = [
      '---',
      `name: ${JSON.stringify(memorySkillName)}`,
      `description: ${JSON.stringify('Retrieve scoped, freshness-aware Threadforge memory and task continuity. Advisory only; never authorization.')}`,
      '---',
      '',
      '# Threadforge memory continuity',
      '',
      `> Source memory continuity skill: ${memorySkillID} revision ${memorySkillRevision}`,
      '',
      memoryApprovalNotice,
      '',
      '## Explicit retrieval',
      '',
      'Use threadforge_context_get only with a user-provided or trusted project_id. Verify exact records with threadforge_project_memory_read_v2.',
      '',
      omissionNotice,
      '',
    ].join('\n');
    const memorySourceBytes = Buffer.from(memorySource, 'utf8');
    await writeFile(memoryExportPath, memorySourceBytes, { flag: 'wx', mode: 0o600 });
    const memorySHA256 = createHash('sha256').update(memorySourceBytes).digest('hex');
    const installedMemory = await installClineProjectSkill({
      projectRoot: projectDirectory, skillFile: memoryExportPath, sha256: memorySHA256,
    });
    if (installedMemory.action !== 'installed' || installedMemory.skill_name !== memorySkillName ||
        installedMemory.path !== `.cline/skills/${memorySkillName}/SKILL.md`) {
      throw new Error('Threadforge did not install the typed memory skill at Cline project scope');
    }

    const childEnv = isolatedEnvironment({
      homeDirectory, appDataDirectory, localAppDataDirectory, root,
    });
    const config = runCommand(cli, ['--config', configDirectory, '--data-dir', dataDirectory, 'config', '--json'], {
      cwd: projectDirectory,
      env: childEnv,
    });
    if (config.status !== 0) throw new Error('Cline CLI config inventory failed in the isolated project');
    let inventory;
    try { inventory = JSON.parse(config.stdout); }
    catch { throw new Error('Cline CLI config inventory did not return JSON'); }
    const skills = Array.isArray(inventory.skills) ? inventory.skills : [];
    const projectSkillPath = path.join(projectDirectory, installed.path);
    const discoveredProcedure = skills.filter((skill) => skill?.name === skillName &&
      typeof skill.path === 'string' && path.resolve(projectDirectory, skill.path) === path.resolve(projectSkillPath));
    const memorySkillPath = path.join(projectDirectory, installedMemory.path);
    const discoveredMemory = skills.filter((skill) => skill?.name === memorySkillName &&
      typeof skill.path === 'string' && path.resolve(projectDirectory, skill.path) === path.resolve(memorySkillPath));
    if (discoveredProcedure.length !== 1 || discoveredMemory.length !== 1) {
      throw new Error('Cline CLI did not discover exactly one procedure skill and one memory-continuity skill');
    }

    return {
      schema_version: 'threadforge.cline-skill-inventory-smoke.v2',
      host: 'cline-cli',
      host_version: expectedVersion,
      project_skill_install_status: 'passed',
      memory_skill_install_status: 'passed',
      project_skill_inventory_status: 'passed',
      discovered_project_skill_count: discoveredProcedure.length + discoveredMemory.length,
      discovered_procedure_skill_count: discoveredProcedure.length,
      discovered_memory_skill_count: discoveredMemory.length,
      procedure_name_retained_in_report: false,
      procedure_body_retained_in_report: false,
      memory_skill_name_retained_in_report: false,
      memory_skill_body_retained_in_report: false,
      agent_retrieval_status: 'not_exercised',
      model_invocations: 0,
      execution_authorized: false,
    };
  } finally {
    await reapClineHubDaemon(cli, projectDirectory, processCleanup);
    await removeOwnedTempRoot(root);
  }
}

export async function reapClineHubDaemon(cliPath, projectDirectory, {
  listProcesses = listRunningProcesses,
  signalProcess = (pid, signal) => process.kill(pid, signal),
  isProcessAlive = processExists,
  wait = delay,
  timeoutMs = 2_000,
} = {}) {
  const processRows = await listProcesses(cliPath, projectDirectory);
  const ownedPids = [...new Set(processRows
    .filter((row) => isSmokeOwnedHub(row, cliPath, projectDirectory))
    .map((row) => row.pid))];
  for (const pid of ownedPids) signalIfRunning(signalProcess, pid, 'SIGTERM');

  const deadline = Date.now() + timeoutMs;
  let remaining = ownedPids.filter(isProcessAlive);
  while (remaining.length > 0 && Date.now() < deadline) {
    await wait(50);
    remaining = remaining.filter(isProcessAlive);
  }
  if (remaining.length > 0) {
    const stillOwned = new Set((await listProcesses(cliPath, projectDirectory))
      .filter((row) => isSmokeOwnedHub(row, cliPath, projectDirectory))
      .map((row) => row.pid));
    for (const pid of remaining) {
      if (stillOwned.has(pid)) signalIfRunning(signalProcess, pid, 'SIGKILL');
    }
    await wait(100);
    remaining = remaining.filter(isProcessAlive);
  }
  if (remaining.length > 0) throw new Error('Cline hub daemon did not stop; preserving its temporary project for inspection');
}

function signalIfRunning(signalProcess, pid, signal) {
  try { signalProcess(pid, signal); }
  catch (error) {
    if (error?.code !== 'ESRCH') throw new Error('unable to stop the smoke-owned Cline hub daemon');
  }
}

function isSmokeOwnedHub(row, cliPath, projectDirectory) {
  if (!Number.isSafeInteger(row?.pid) || row.pid <= 0 || typeof row.commandLine !== 'string' ||
      !row.commandLine.includes('--cline-hub-daemon') || !commandUsesProject(row.commandLine, projectDirectory)) return false;
  if (typeof row.executablePath === 'string' && row.executablePath.length > 0) {
    const actual = path.resolve(row.executablePath);
    const expected = path.resolve(cliPath);
    return process.platform === 'win32'
      ? actual.toLowerCase() === expected.toLowerCase()
      : actual === expected;
  }
  return row.commandLine.includes(cliPath) || row.commandLine.includes(path.basename(cliPath));
}

function commandUsesProject(commandLine, projectDirectory) {
  const escaped = projectDirectory.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp(`(?:^|\\s)--cwd(?:=|\\s+)(?:"${escaped}"|'${escaped}'|${escaped})(?=\\s|$)`).test(commandLine);
}

function listRunningProcesses(cliPath, projectDirectory) {
  if (process.platform === 'win32') return listWindowsProcesses(cliPath, projectDirectory);
  const args = process.platform === 'darwin'
    ? ['-ww', '-axo', 'pid=,command=']
    : ['-ww', '-eo', 'pid=,args='];
  const result = spawnSync('ps', args, { encoding: 'utf8', timeout: 10_000, windowsHide: true });
  if (result.error || result.status !== 0) throw new Error('unable to inspect Cline smoke processes');
  return result.stdout.split(/\r?\n/).flatMap((line) => {
    const match = /^\s*(\d+)\s+(.+)$/.exec(line);
    if (!match) return [];
    const row = { pid: Number(match[1]), commandLine: match[2] };
    return isSmokeOwnedHub(row, cliPath, projectDirectory) ? [row] : [];
  });
}

function listWindowsProcesses(cliPath, projectDirectory) {
  const systemRoot = process.env.SystemRoot ?? process.env.WINDIR;
  if (typeof systemRoot !== 'string' || !path.isAbsolute(systemRoot)) throw new Error('Windows process inspection is unavailable');
  const powershell = path.join(systemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
  const script = "$targetExe = $env:THREADFORGE_CLINE_SMOKE_EXE; $targetProject = $env:THREADFORGE_CLINE_SMOKE_PROJECT; " +
    "Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -eq $targetExe -and $_.CommandLine -and " +
    "$_.CommandLine.Contains('--cline-hub-daemon') -and $_.CommandLine.Contains($targetProject) } | " +
    "Select-Object @{Name='pid';Expression={$_.ProcessId}}, @{Name='executablePath';Expression={$_.ExecutablePath}}, " +
    "@{Name='commandLine';Expression={$_.CommandLine}} | ConvertTo-Json -Compress";
  const result = spawnSync(powershell, [
    '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-Command', script,
  ], {
    encoding: 'utf8',
    timeout: 10_000,
    maxBuffer: 1 << 20,
    windowsHide: true,
    env: {
      SystemRoot: systemRoot,
      WINDIR: systemRoot,
      TEMP: process.env.TEMP ?? os.tmpdir(),
      TMP: process.env.TMP ?? os.tmpdir(),
      THREADFORGE_CLINE_SMOKE_EXE: cliPath,
      THREADFORGE_CLINE_SMOKE_PROJECT: projectDirectory,
    },
  });
  if (result.error || result.status !== 0) throw new Error('unable to inspect Cline smoke processes');
  if (!result.stdout.trim()) return [];
  let rows;
  try { rows = JSON.parse(result.stdout); }
  catch { throw new Error('Windows process inspection returned malformed data'); }
  return (Array.isArray(rows) ? rows : [rows]).map((row) => ({
    pid: row.pid,
    executablePath: row.executablePath,
    commandLine: row.commandLine,
  })).filter((row) => isSmokeOwnedHub(row, cliPath, projectDirectory));
}

function processExists(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) {
    if (error?.code === 'ESRCH') return false;
    if (error?.code === 'EPERM') return true;
    throw error;
  }
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
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
