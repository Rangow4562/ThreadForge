import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { reapClineHubDaemon, runClineSkillHostSmoke } from '../scripts/cline-skill-host-smoke.mjs';

test('Cline inventory smoke installs procedure and typed memory skills without invoking an agent', async () => {
  const version = '3.0.73';
  const terminated = [];
  const liveProcesses = new Set([41001]);
  const runCommand = (_executable, args, options = {}) => {
    if (args.length === 1 && args[0] === '--version') return { status: 0, stdout: `${version}\n` };
    assert.deepEqual(args.slice(-2), ['config', '--json']);
    const skillsRoot = path.join(options.cwd, '.cline', 'skills');
    const skills = readdirSync(skillsRoot, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => {
        const skillPath = path.join('.cline', 'skills', entry.name, 'SKILL.md');
        const body = readFileSync(path.join(options.cwd, skillPath), 'utf8');
        const name = /^name: ("(?:\\.|[^"\\])*")$/m.exec(body)?.[1];
        return { name: name ? JSON.parse(name) : undefined, path: skillPath };
      });
    return { status: 0, stdout: JSON.stringify({ skills }) };
  };

  const receipt = await runClineSkillHostSmoke(process.execPath, version, {
    runCommand,
    processCleanup: {
      listProcesses: async (cli, project) => [
        { pid: 41001, executablePath: cli, commandLine: `${cli} --cline-hub-daemon --cwd ${project} --host 127.0.0.1` },
        { pid: 41002, executablePath: cli, commandLine: `${cli} --cline-hub-daemon --cwd ${project}-neighbor --host 127.0.0.1` },
        { pid: 41003, executablePath: path.join(path.dirname(cli), 'not-cline.exe'), commandLine: `${cli} --cline-hub-daemon --cwd ${project}` },
        { pid: 41004, executablePath: cli, commandLine: `${cli} --version` },
      ],
      signalProcess: (pid, signal) => {
        terminated.push({ pid, signal });
        liveProcesses.delete(pid);
      },
      isProcessAlive: (pid) => liveProcesses.has(pid),
      wait: async () => {},
      timeoutMs: 0,
    },
  });
  assert.equal(receipt.schema_version, 'threadforge.cline-skill-inventory-smoke.v2');
  assert.equal(receipt.discovered_project_skill_count, 2);
  assert.equal(receipt.discovered_procedure_skill_count, 1);
  assert.equal(receipt.discovered_memory_skill_count, 1);
  assert.equal(receipt.procedure_name_retained_in_report, false);
  assert.equal(receipt.memory_skill_name_retained_in_report, false);
  assert.equal(receipt.procedure_body_retained_in_report, false);
  assert.equal(receipt.memory_skill_body_retained_in_report, false);
  assert.equal(receipt.agent_retrieval_status, 'not_exercised');
  assert.equal(receipt.model_invocations, 0);
  assert.equal(receipt.execution_authorized, false);
  assert.deepEqual(terminated, [{ pid: 41001, signal: 'SIGTERM' }]);
});

test('Cline smoke force-stops only a daemon still bound to its exact CLI and temporary project', async () => {
  const cli = process.execPath;
  const project = path.join(process.cwd(), '.cline-smoke-owned-project');
  const commandLine = `${cli} --cline-hub-daemon --cwd "${project}" --host 127.0.0.1`;
  const live = new Set([42001]);
  const signals = [];
  let inspections = 0;

  await reapClineHubDaemon(cli, project, {
    listProcesses: async () => {
      inspections += 1;
      return [{ pid: 42001, executablePath: cli, commandLine }];
    },
    signalProcess: (pid, signal) => {
      signals.push({ pid, signal });
      if (signal === 'SIGKILL') live.delete(pid);
    },
    isProcessAlive: (pid) => live.has(pid),
    wait: async () => {},
    timeoutMs: 0,
  });

  assert.equal(inspections, 2);
  assert.deepEqual(signals, [
    { pid: 42001, signal: 'SIGTERM' },
    { pid: 42001, signal: 'SIGKILL' },
  ]);
});

test('Cline smoke will not force-stop a PID that no longer matches its temporary daemon', async () => {
  const cli = process.execPath;
  const project = path.join(process.cwd(), '.cline-smoke-owned-project');
  const ownedCommand = `${cli} --cline-hub-daemon --cwd "${project}" --host 127.0.0.1`;
  const signals = [];
  let inspections = 0;

  await assert.rejects(reapClineHubDaemon(cli, project, {
    listProcesses: async () => {
      inspections += 1;
      return inspections === 1
        ? [{ pid: 42002, executablePath: cli, commandLine: ownedCommand }]
        : [{ pid: 42002, executablePath: cli, commandLine: `${cli} --version` }];
    },
    signalProcess: (pid, signal) => signals.push({ pid, signal }),
    isProcessAlive: () => true,
    wait: async () => {},
    timeoutMs: 0,
  }), /preserving its temporary project/);

  assert.deepEqual(signals, [{ pid: 42002, signal: 'SIGTERM' }]);
});
