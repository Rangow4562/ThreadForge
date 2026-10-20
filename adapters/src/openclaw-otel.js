"use strict";
var __awaiter = (this && this.__awaiter) || function (thisArg, _arguments, P, generator) {
    function adopt(value) { return value instanceof P ? value : new P(function (resolve) { resolve(value); }); }
    return new (P || (P = Promise))(function (resolve, reject) {
        function fulfilled(value) { try { step(generator.next(value)); } catch (e) { reject(e); } }
        function rejected(value) { try { step(generator["throw"](value)); } catch (e) { reject(e); } }
        function step(result) { result.done ? resolve(result.value) : adopt(result.value).then(fulfilled, rejected); }
        step((generator = generator.apply(thisArg, _arguments || [])).next());
    });
};
var __generator = (this && this.__generator) || function (thisArg, body) {
    var _ = { label: 0, sent: function() { if (t[0] & 1) throw t[1]; return t[1]; }, trys: [], ops: [] }, f, y, t, g = Object.create((typeof Iterator === "function" ? Iterator : Object).prototype);
    return g.next = verb(0), g["throw"] = verb(1), g["return"] = verb(2), typeof Symbol === "function" && (g[Symbol.iterator] = function() { return this; }), g;
    function verb(n) { return function (v) { return step([n, v]); }; }
    function step(op) {
        if (f) throw new TypeError("Generator is already executing.");
        while (g && (g = 0, op[0] && (_ = 0)), _) try {
            if (f = 1, y && (t = op[0] & 2 ? y["return"] : op[0] ? y["throw"] || ((t = y["return"]) && t.call(y), 0) : y.next) && !(t = t.call(y, op[1])).done) return t;
            if (y = 0, t) op = [op[0] & 2, t.value];
            switch (op[0]) {
                case 0: case 1: t = op; break;
                case 4: _.label++; return { value: op[1], done: false };
                case 5: _.label++; y = op[1]; op = [0]; continue;
                case 7: op = _.ops.pop(); _.trys.pop(); continue;
                default:
                    if (!(t = _.trys, t = t.length > 0 && t[t.length - 1]) && (op[0] === 6 || op[0] === 2)) { _ = 0; continue; }
                    if (op[0] === 3 && (!t || (op[1] > t[0] && op[1] < t[3]))) { _.label = op[1]; break; }
                    if (op[0] === 6 && _.label < t[1]) { _.label = t[1]; t = op; break; }
                    if (t && _.label < t[2]) { _.label = t[2]; _.ops.push(op); break; }
                    if (t[2]) _.ops.pop();
                    _.trys.pop(); continue;
            }
            op = body.call(thisArg, _);
        } catch (e) { op = [6, e]; y = 0; } finally { f = t = 0; }
        if (op[0] & 5) throw op[1]; return { value: op[0] ? op[1] : void 0, done: true };
    }
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.renderOpenClawOtelConfig = renderOpenClawOtelConfig;
exports.resolveThreadforgeTracePath = resolveThreadforgeTracePath;
exports.getOpenClawOtelStatus = getOpenClawOtelStatus;
exports.validateOpenClawOtelStatus = validateOpenClawOtelStatus;
exports.parseOpenClawDuration = parseOpenClawDuration;
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var maxStatusTailBytes = 16 << 20;
var maxStatusLineBytes = 64 << 10;
var defaultEndpoint = 'http://127.0.0.1:4318';
function renderOpenClawOtelConfig(options) {
    var _a, _b;
    if (options === void 0) { options = {}; }
    var endpoint = validateEndpoint((_a = options.endpoint) !== null && _a !== void 0 ? _a : defaultEndpoint, (_b = options.allowRemote) !== null && _b !== void 0 ? _b : false);
    var captureContent = options.captureContent === true;
    var rows = [
        'Install/enable OpenClaw diagnostics-otel using the OpenClaw CLI:',
        '  openclaw plugins install @openclaw/diagnostics-otel',
        '  openclaw plugins enable diagnostics-otel',
        '',
        'Merge this fragment into the existing OpenClaw JSON5 config; do not replace other diagnostics, plugin, or allowlist entries:',
        '{',
        '  diagnostics: {',
        '    enabled: true,',
        '    otel: {',
        '      enabled: true,',
        "      endpoint: ".concat(JSON.stringify(endpoint), ","),
        "      tracesEndpoint: ".concat(JSON.stringify(signalEndpoint(endpoint, 'traces')), ","),
        "      metricsEndpoint: ".concat(JSON.stringify(signalEndpoint(endpoint, 'metrics')), ","),
        "      logsEndpoint: ".concat(JSON.stringify(signalEndpoint(endpoint, 'logs')), ","),
        '      protocol: "http/protobuf",',
        '      serviceName: "openclaw-gateway",',
        '      traces: true,',
        '      metrics: true,',
        '      logs: true,',
        '      logsExporter: "otlp",',
        '      sampleRate: 1.0,',
        '      flushIntervalMs: 60000,',
        "      captureContent: ".concat(captureContent, ","),
        '    },',
        '  },',
        '}',
        '',
        'Capture content is off unless --capture-content is explicitly selected. Check existing per-signal OTLP endpoints and environment overrides before enabling export.',
    ];
    if (options.allowRemote)
        rows.push('WARNING: remote TLS export was explicitly enabled; Threadforge does not manage or retain the remote credentials.');
    if (captureContent)
        rows.push('WARNING: OpenClaw may export bounded, redacted prompts, responses, tool inputs/outputs, and tool definitions. System prompts remain excluded by OpenClaw.');
    return rows.join('\n');
}
function resolveThreadforgeTracePath(env, homeDir, platform) {
    var _a, _b, _c, _d;
    if (env === void 0) { env = process.env; }
    if (homeDir === void 0) { homeDir = node_os_1.default.homedir(); }
    if (platform === void 0) { platform = process.platform; }
    if ((_a = env.THREADFORGE_TRACE_FILE) === null || _a === void 0 ? void 0 : _a.trim())
        return env.THREADFORGE_TRACE_FILE.trim();
    var pathApi = platform === 'win32' ? node_path_1.default.win32 : node_path_1.default.posix;
    var root = (_b = env.THREADFORGE_HOME) === null || _b === void 0 ? void 0 : _b.trim();
    if (!root) {
        if (platform === 'win32')
            root = pathApi.join(((_c = env.LOCALAPPDATA) === null || _c === void 0 ? void 0 : _c.trim()) || pathApi.join(homeDir, 'AppData', 'Local'), 'threadforge');
        else if (platform === 'darwin')
            root = pathApi.join(homeDir, 'Library', 'Caches', 'threadforge');
        else
            root = pathApi.join(((_d = env.XDG_CACHE_HOME) === null || _d === void 0 ? void 0 : _d.trim()) || pathApi.join(homeDir, '.cache'), 'threadforge');
    }
    return pathApi.join(root, 'traces', 'events.jsonl');
}
/** Scans a bounded local JSONL tail and returns aggregate status only, never event bodies or paths. */
function getOpenClawOtelStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var logPath, file, error_1, info, size, offset, bytes, bytesRead, contents, olderOmitted, firstBreak, lines, status_1, latest, _i, lines_1, line, event_1, timestamp;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    logPath = (_a = options.logPath) !== null && _a !== void 0 ? _a : resolveThreadforgeTracePath(options.env, options.homeDir, options.platform);
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.open)(logPath, 'r')];
                case 2:
                    file = _b.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_1 = _b.sent();
                    if (isCode(error_1, 'ENOENT'))
                        return [2 /*return*/, emptyStatus(false)];
                    throw new Error('Threadforge local event log is inaccessible');
                case 4:
                    _b.trys.push([4, , 7, 9]);
                    return [4 /*yield*/, (0, promises_1.stat)(logPath)];
                case 5:
                    info = _b.sent();
                    size = Math.min(info.size, maxStatusTailBytes);
                    offset = Math.max(0, info.size - size);
                    bytes = Buffer.alloc(size);
                    return [4 /*yield*/, file.read(bytes, 0, bytes.byteLength, offset)];
                case 6:
                    bytesRead = (_b.sent()).bytesRead;
                    contents = bytes.subarray(0, bytesRead).toString('utf8');
                    olderOmitted = offset > 0;
                    if (olderOmitted) {
                        firstBreak = contents.indexOf('\n');
                        contents = firstBreak < 0 ? '' : contents.slice(firstBreak + 1);
                    }
                    lines = contents.split(/\r?\n/);
                    if (lines.at(-1) === '')
                        lines.pop();
                    status_1 = emptyStatus(true);
                    status_1.older_events_omitted = olderOmitted;
                    latest = 0;
                    for (_i = 0, lines_1 = lines; _i < lines_1.length; _i++) {
                        line = lines_1[_i];
                        if (!line)
                            continue;
                        if (Buffer.byteLength(line, 'utf8') > maxStatusLineBytes) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        event_1 = void 0;
                        try {
                            event_1 = JSON.parse(line);
                        }
                        catch (_c) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        if (!isRecord(event_1) || !isOpenClawOtlpEvent(event_1))
                            continue;
                        timestamp = typeof event_1.timestamp === 'string' ? Date.parse(event_1.timestamp) : Number.NaN;
                        if (!Number.isFinite(timestamp)) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        status_1.observed_events++;
                        if (timestamp > latest)
                            latest = timestamp;
                    }
                    status_1.complete = status_1.malformed_lines === 0 && !olderOmitted;
                    if (status_1.observed_events) {
                        status_1.last_event_observed = true;
                        status_1.last_event_observed_at = new Date(latest).toISOString();
                    }
                    return [2 /*return*/, status_1];
                case 7: return [4 /*yield*/, file.close()];
                case 8:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function validateOpenClawOtelStatus(status, sinceMs, now) {
    if (sinceMs === void 0) { sinceMs = 10 * 60000; }
    if (now === void 0) { now = new Date(); }
    if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > 30 * 24 * 60 * 60000) {
        throw new Error('OpenClaw OTLP validation window must be between 1 ms and 30 days');
    }
    var timestamp = status.last_event_observed_at ? Date.parse(status.last_event_observed_at) : Number.NaN;
    return {
        schema_version: 'threadforge.openclaw-otel-validation.v1',
        valid: Number.isFinite(timestamp) && timestamp >= now.getTime() - sinceMs && timestamp <= now.getTime() + 60000,
        since_ms: sinceMs,
        status: status,
    };
}
function parseOpenClawDuration(value) {
    var match = /^(\d+)(ms|s|m|h|d)$/.exec(value.trim());
    if (!match)
        throw new Error('duration must use ms, s, m, h, or d units');
    var multiplier = { ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000 };
    var amount = Number(match[1]);
    var result = amount * multiplier[match[2]];
    if (!Number.isSafeInteger(result) || result < 1 || result > 30 * 24 * 60 * 60000) {
        throw new Error('duration must be between 1 ms and 30 days');
    }
    return result;
}
function validateEndpoint(value, allowRemote) {
    var url;
    try {
        url = new URL(value);
    }
    catch (_a) {
        throw new Error('OpenClaw OTLP endpoint must be a valid HTTP URL');
    }
    var hostname = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    var ipv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(hostname) && hostname.split('.').every(function (part) { return Number(part) <= 255; });
    var loopback = hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1' || ipv4Loopback;
    if ((!loopback && (!allowRemote || url.protocol !== 'https:')) || loopback && !['http:', 'https:'].includes(url.protocol) ||
        url.username || url.password || url.search || url.hash || url.pathname !== '/') {
        throw new Error('OpenClaw OTLP endpoint must be a loopback HTTP endpoint; remote endpoints require HTTPS and --allow-remote');
    }
    return url.origin;
}
function signalEndpoint(endpoint, signal) {
    return "".concat(endpoint, "/v1/").concat(signal);
}
function isOpenClawOtlpEvent(value) {
    var harness = isRecord(value.harness) ? value.harness : {};
    return typeof harness.name === 'string' && /open[-_]?claw/i.test(harness.name) && harness.collection_method === 'otlp';
}
function emptyStatus(present) {
    return {
        schema_version: 'threadforge.openclaw-otel-status.v1',
        present: present,
        complete: present,
        older_events_omitted: false, malformed_lines: 0, observed_events: 0, last_event_observed: false,
    };
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
