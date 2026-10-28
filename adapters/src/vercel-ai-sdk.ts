/**
 * Create content-minimizing per-call telemetry settings for Vercel AI SDK v7.
 * The application registers its telemetry integration and configures any
 * OpenTelemetry provider/exporter; this helper performs no global setup.
 */
export function createThreadforgeVercelTelemetryOptions(): {
  recordInputs: false;
  recordOutputs: false;
  includeRuntimeContext: false;
  includeToolsContext: false;
} {
  return {
    recordInputs: false,
    recordOutputs: false,
    includeRuntimeContext: false,
    includeToolsContext: false,
  };
}
