import {
  createCommandCodeEventRecorder,
  type CommandCodeModApi,
} from './telemetry.ts';

/** Register Threadforge's read-only AgentEvent observer in Command Code. */
export default function threadforgeCommandCodeTelemetry(value: unknown): void {
  if (typeof value !== 'object' || value === null || typeof (value as { on?: unknown }).on !== 'function') {
    throw new Error('Command Code ModApi must provide on(event, handler)');
  }
  const cmd = value as CommandCodeModApi;
  const recorder = createCommandCodeEventRecorder();
  const subscriptions: Array<{ dispose(): void }> = [];
  const track = (subscription: { dispose(): void }): void => {
    if (!subscription || typeof subscription.dispose !== 'function') {
      throw new Error('Command Code ModApi on() must return a disposable registration');
    }
    subscriptions.push(subscription);
  };

  try {
    track(cmd.on('session_start', (event) => recorder.observe('session_start', event)));
    track(cmd.on('session_shutdown', (event) => recorder.observe('session_shutdown', event)));
    track(cmd.on('run_start', (event) => recorder.observe('run_start', event)));
    track(cmd.on('run_end', (event) => recorder.observe('run_end', event)));
    track(cmd.on('turn_start', (event) => recorder.observe('turn_start', event)));
    track(cmd.on('turn_end', (event) => recorder.observe('turn_end', event)));
    track(cmd.on('model_request_start', (event) => recorder.observe('model_request_start', event)));
    track(cmd.on('model_request_end', (event) => recorder.observe('model_request_end', event)));
    track(cmd.on('tool_running', (event) => recorder.observe('tool_running', event)));
    track(cmd.on('tool_completed', (event) => recorder.observe('tool_completed', event)));
    track(cmd.on('tool_errored', (event) => recorder.observe('tool_errored', event)));
    track(cmd.on('subagent_start', (event) => recorder.observe('subagent_start', event)));
    track(cmd.on('subagent_stop', (event) => recorder.observe('subagent_stop', event)));
    track(cmd.on('subagent_progress', (event) => recorder.observe('subagent_progress', event)));
    track(cmd.on('compaction_start', (event) => recorder.observe('compaction_start', event)));
    track(cmd.on('compaction_done', (event) => recorder.observe('compaction_done', event)));
  } catch {
    for (const subscription of subscriptions) {
      try {
        subscription.dispose();
      } catch {
        // Partial mod registration must not leave Threadforge listeners installed.
      }
    }
    recorder.dispose();
    throw new Error('Could not register the Command Code telemetry observer');
  }
}
