"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.renderClaudeCoworkOtelGuidance = renderClaudeCoworkOtelGuidance;
var node_net_1 = require("node:net");
var localHostSuffixes = [
    '.localhost',
    '.local',
    '.internal',
    '.lan',
    '.home.arpa',
    '.test',
    '.invalid',
];
/**
 * Produce manual Claude Cowork OTLP guidance for a separately operated public
 * collector. This deliberately performs no DNS or network checks and handles
 * no credentials.
 */
function renderClaudeCoworkOtelGuidance(endpoint) {
    var raw = endpoint.trim();
    var parsed;
    try {
        parsed = new URL(raw);
    }
    catch (_a) {
        throw new Error('Claude Cowork endpoint must be an absolute public HTTPS URL');
    }
    var hostname = parsed.hostname.toLowerCase().replace(/\.$/, '');
    var labels = hostname.split('.');
    var validDnsName = labels.length >= 2 && labels.every(function (label) {
        return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label);
    });
    var localName = hostname === 'localhost' || localHostSuffixes.some(function (suffix) { return hostname.endsWith(suffix); });
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password || raw.includes('?') || raw.includes('#') ||
        (0, node_net_1.isIP)(hostname) !== 0 || !validDnsName || localName || parsed.origin === 'https://localhost') {
        throw new Error('Claude Cowork requires a public-DNS HTTPS endpoint without credentials, query, or fragment');
    }
    var safeEndpoint = parsed.toString().replace(/\/$/, '');
    return [
        'Claude Cowork OTLP setup (manual; remote export is opt-in)',
        "OTLP endpoint: ".concat(safeEndpoint),
        'OTLP protocol: HTTP/protobuf',
        'OTLP headers: configure directly in the Claude organization console if your collector requires authentication; Threadforge does not accept or store header values.',
        'Resource attributes: deployment.environment=prod,service.name=claude-cowork',
        '',
        'Use a separately operated, durable collector with public DNS and HTTPS that Anthropic services can reach.',
        'Do not use a laptop, loopback address, or temporary tunnel. Threadforge local intake is loopback-only; this command does not relay, receive, validate, or forward Cowork telemetry.',
        'Cowork telemetry may contain prompts, tool parameters/results, file paths, user identifiers, model usage, and errors. Review collection, retention, and access controls before enabling export.',
        'This command only prints guidance. It makes no network requests and does not modify Claude settings or the collector.',
    ].join('\n');
}
