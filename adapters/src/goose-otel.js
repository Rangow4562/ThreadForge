"use strict";
var __assign = (this && this.__assign) || function () {
    __assign = Object.assign || function(t) {
        for (var s, i = 1, n = arguments.length; i < n; i++) {
            s = arguments[i];
            for (var p in s) if (Object.prototype.hasOwnProperty.call(s, p))
                t[p] = s[p];
        }
        return t;
    };
    return __assign.apply(this, arguments);
};
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
var __spreadArray = (this && this.__spreadArray) || function (to, from, pack) {
    if (pack || arguments.length === 2) for (var i = 0, l = from.length, ar; i < l; i++) {
        if (ar || !(i in from)) {
            if (!ar) ar = Array.prototype.slice.call(from, 0, i);
            ar[i] = from[i];
        }
    }
    return to.concat(ar || Array.prototype.slice.call(from));
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.renderGooseOtelConfig = renderGooseOtelConfig;
exports.getGooseOtelStatus = getGooseOtelStatus;
exports.validateGooseOtelStatus = validateGooseOtelStatus;
exports.parseGooseOtelDuration = parseGooseOtelDuration;
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var openclaw_otel_ts_1 = require("./openclaw-otel.ts");
var local_otel_status_ts_1 = require("./local-otel-status.ts");
var defaultEndpoint = 'http://127.0.0.1:4318';
var maxConfigBytes = 2 << 20;
var maxValidationWindowMs = 30 * 24 * 60 * 60000;
/** Print-only local OTLP guidance. Threadforge never edits Goose's user-owned config. */
function renderGooseOtelConfig(endpoint) {
    if (endpoint === void 0) { endpoint = defaultEndpoint; }
    var normalized = validateLoopbackEndpoint(endpoint);
    return [
        '# Merge these keys into the existing Goose config.yaml; preserve all other settings.',
        "otel_exporter_otlp_endpoint: ".concat(JSON.stringify(normalized)),
        'otel_exporter_otlp_timeout: 10000',
        '',
        '# This targets the local OTLP/HTTP receiver on port 4318 by default.',
        '# Threadforge does not enable message-content capture or write Goose configuration.',
        '# Inspect OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SDK_DISABLED, and OTEL_EXPORTER_OTLP_PROTOCOL',
        '# in the environment that launches Goose; they can override or disable this config.',
    ].join('\n');
}
/** Read config and local JSONL evidence without exposing config values, paths, or event bodies. */
function getGooseOtelStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var env, homeDir, platform, configPath, logPath, issues, config, environment, _i, _a, issue, telemetry;
        var _b, _c, _d, _e, _f, _g;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_h) {
            switch (_h.label) {
                case 0:
                    env = (_b = options.env) !== null && _b !== void 0 ? _b : process.env;
                    homeDir = (_c = options.homeDir) !== null && _c !== void 0 ? _c : node_os_1.default.homedir();
                    platform = (_d = options.platform) !== null && _d !== void 0 ? _d : process.platform;
                    configPath = (_e = options.configPath) !== null && _e !== void 0 ? _e : node_path_1.default.join(homeDir, '.config', 'goose', 'config.yaml');
                    logPath = (_f = options.logPath) !== null && _f !== void 0 ? _f : (0, openclaw_otel_ts_1.resolveThreadforgeTracePath)(env, homeDir, platform);
                    issues = new Set();
                    return [4 /*yield*/, inspectGooseConfig(configPath, issues)];
                case 1:
                    config = _h.sent();
                    environment = inspectGooseEnvironment(env, config.endpoint);
                    for (_i = 0, _a = environment.issues; _i < _a.length; _i++) {
                        issue = _a[_i];
                        issues.add(issue);
                    }
                    return [4 /*yield*/, (0, local_otel_status_ts_1.scanLocalOtelEventLog)(logPath, 'goose', (_g = options.now) !== null && _g !== void 0 ? _g : new Date())];
                case 2:
                    telemetry = _h.sent();
                    if (!telemetry.present)
                        issues.add('event_log_missing');
                    if (!telemetry.complete)
                        issues.add('event_log_incomplete');
                    if (!telemetry.last_event_observed)
                        issues.add('no_goose_otlp_events');
                    return [2 /*return*/, __assign(__assign({ schema_version: 'threadforge.goose-otel-status.v1', inspection_complete: config.inspected && telemetry.complete, configured_endpoint: config.endpointClass }, (config.timeoutMs !== undefined ? { timeout_ms: config.timeoutMs } : {})), { environment: environment.summary, issues: __spreadArray([], issues, true), telemetry: telemetry })];
            }
        });
    });
}
function validateGooseOtelStatus(status, sinceMs, now) {
    if (sinceMs === void 0) { sinceMs = 10 * 60000; }
    if (now === void 0) { now = new Date(); }
    if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > maxValidationWindowMs) {
        throw new Error('Goose OTLP validation window must be between 1 ms and 30 days');
    }
    var issues = new Set(status.issues);
    var timestamp = status.telemetry.last_event_observed_at
        ? Date.parse(status.telemetry.last_event_observed_at)
        : Number.NaN;
    if (!Number.isFinite(timestamp) || timestamp < now.getTime() - sinceMs || timestamp > now.getTime() + 60000) {
        issues.add('no_recent_goose_otlp_event');
    }
    var valid = status.inspection_complete && status.configured_endpoint === 'loopback_http' && issues.size === 0;
    return { schema_version: 'threadforge.goose-otel-validation.v1', valid: valid, since_ms: sinceMs, issues: __spreadArray([], issues, true), status: status };
}
function parseGooseOtelDuration(value) {
    var match = /^(\d+)(ms|s|m|h|d)$/.exec(value.trim());
    if (!match)
        throw new Error('duration must use ms, s, m, h, or d units');
    var multiplier = { ms: 1, s: 1000, m: 60000, h: 3600000, d: 86400000 };
    var result = Number(match[1]) * multiplier[match[2]];
    if (!Number.isSafeInteger(result) || result < 1 || result > maxValidationWindowMs) {
        throw new Error('duration must be between 1 ms and 30 days');
    }
    return result;
}
function inspectGooseConfig(filePath, issues) {
    return __awaiter(this, void 0, void 0, function () {
        var handle, pathInfo, openedInfo, latestPathInfo, bytes, bytesRead, content, values, _i, _a, issue, endpoint, rawTimeout, endpointClass, timeoutMs, error_1;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    _b.trys.push([0, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    pathInfo = _b.sent();
                    if (pathInfo.isSymbolicLink() || !pathInfo.isFile()) {
                        issues.add(pathInfo.isSymbolicLink() ? 'config_symlink_refused' : 'config_not_regular_file');
                        return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                    }
                    if (pathInfo.size > maxConfigBytes) {
                        issues.add('config_oversized');
                        return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                    }
                    return [4 /*yield*/, (0, promises_1.open)(filePath, 'r')];
                case 2:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 3:
                    openedInfo = _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 4:
                    latestPathInfo = _b.sent();
                    if (latestPathInfo.isSymbolicLink() || !latestPathInfo.isFile() || openedInfo.size > maxConfigBytes) {
                        issues.add('config_changed_or_symlinked');
                        return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                    }
                    bytes = Buffer.alloc(openedInfo.size);
                    return [4 /*yield*/, handle.read(bytes, 0, bytes.length, 0)];
                case 5:
                    bytesRead = (_b.sent()).bytesRead;
                    content = void 0;
                    try {
                        content = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, bytesRead));
                    }
                    catch (_c) {
                        issues.add('config_invalid_utf8');
                        return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                    }
                    values = readKnownYamlScalars(content);
                    if (values.issues.length) {
                        for (_i = 0, _a = values.issues; _i < _a.length; _i++) {
                            issue = _a[_i];
                            issues.add(issue);
                        }
                        return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                    }
                    endpoint = values.values.get('otel_exporter_otlp_endpoint');
                    rawTimeout = values.values.get('otel_exporter_otlp_timeout');
                    if (!endpoint)
                        issues.add('endpoint_missing');
                    endpointClass = endpoint ? classifyEndpoint(endpoint) : 'missing';
                    if (endpointClass === 'remote_http')
                        issues.add('remote_endpoint_not_allowed_by_local_default');
                    if (endpointClass === 'invalid')
                        issues.add('endpoint_invalid');
                    timeoutMs = void 0;
                    if (rawTimeout !== undefined) {
                        if (/^\d+$/.test(rawTimeout) && Number.isSafeInteger(Number(rawTimeout)) && Number(rawTimeout) > 0 && Number(rawTimeout) <= 120000) {
                            timeoutMs = Number(rawTimeout);
                        }
                        else
                            issues.add('timeout_invalid');
                    }
                    return [2 /*return*/, __assign(__assign(__assign({ inspected: true }, (endpoint ? { endpoint: endpoint } : {})), { endpointClass: endpointClass }), (timeoutMs !== undefined ? { timeoutMs: timeoutMs } : {}))];
                case 6:
                    error_1 = _b.sent();
                    if (isCode(error_1, 'ENOENT')) {
                        issues.add('config_missing');
                        return [2 /*return*/, { inspected: false, endpointClass: 'missing' }];
                    }
                    issues.add('config_unreadable');
                    return [2 /*return*/, { inspected: false, endpointClass: 'invalid' }];
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close())];
                case 8:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function readKnownYamlScalars(content) {
    var wanted = new Set(['otel_exporter_otlp_endpoint', 'otel_exporter_otlp_timeout']);
    var values = new Map();
    var issues = new Set();
    for (var _i = 0, _a = content.replace(/^\uFEFF/, '').split(/\r?\n/); _i < _a.length; _i++) {
        var line = _a[_i];
        if (!line.trim() || line.trimStart().startsWith('#') || /^\s/.test(line))
            continue;
        var match = /^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*?)\s*$/.exec(line);
        if (!match || !wanted.has(match[1]))
            continue;
        var key = match[1];
        if (values.has(key)) {
            issues.add('config_duplicate_otel_key');
            continue;
        }
        var scalar = parseYamlScalar(match[2]);
        if (scalar === undefined)
            issues.add('config_unsupported_otel_value');
        else
            values.set(key, scalar);
    }
    return { values: values, issues: __spreadArray([], issues, true) };
}
function parseYamlScalar(value) {
    var uncommented = stripYamlComment(value).trim();
    if (!uncommented || ['|', '>', 'null', '~'].includes(uncommented) || uncommented.startsWith('[') || uncommented.startsWith('{'))
        return undefined;
    if (uncommented.startsWith('"')) {
        try {
            var parsed = JSON.parse(uncommented);
            return typeof parsed === 'string' ? parsed : undefined;
        }
        catch (_a) {
            return undefined;
        }
    }
    if (uncommented.startsWith("'")) {
        if (!uncommented.endsWith("'") || uncommented.length < 2)
            return undefined;
        return uncommented.slice(1, -1).replaceAll("''", "'");
    }
    return uncommented;
}
function stripYamlComment(value) {
    var quote;
    for (var index = 0; index < value.length; index++) {
        var current = value[index];
        if (quote === 'double' && current === '\\') {
            index++;
            continue;
        }
        if (quote === 'single' && current === "'" && value[index + 1] === "'") {
            index++;
            continue;
        }
        if (quote === 'double' && current === '"' || quote === 'single' && current === "'") {
            quote = undefined;
            continue;
        }
        if (!quote && current === '"')
            quote = 'double';
        else if (!quote && current === "'")
            quote = 'single';
        else if (!quote && current === '#' && (index === 0 || /\s/.test(value[index - 1])))
            return value.slice(0, index);
    }
    return value;
}
function classifyEndpoint(value) {
    try {
        var url = new URL(value);
        var host = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
        var ipv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(host) && host.split('.').every(function (part) { return Number(part) <= 255; });
        var loopback = host === 'localhost' || host.endsWith('.localhost') || host === '::1' || ipv4Loopback;
        if (url.protocol !== 'http:' || url.username || url.password || url.search || url.hash || url.pathname !== '/')
            return 'invalid';
        return loopback ? 'loopback_http' : 'remote_http';
    }
    catch (_a) {
        return 'invalid';
    }
}
function validateLoopbackEndpoint(value) {
    if (classifyEndpoint(value) !== 'loopback_http') {
        throw new Error('Goose OTLP endpoint must be a loopback HTTP base URL without credentials, path, query, or fragment');
    }
    return new URL(value).origin;
}
function inspectGooseEnvironment(env, configEndpoint) {
    var _a, _b, _c;
    var issues = new Set();
    var override = (_a = env.OTEL_EXPORTER_OTLP_ENDPOINT) === null || _a === void 0 ? void 0 : _a.trim();
    var endpointOverride = 'unset';
    if (override) {
        var overrideClass = classifyEndpoint(override);
        if (overrideClass === 'invalid')
            endpointOverride = 'invalid';
        else if (configEndpoint && normalizeEndpoint(override) === normalizeEndpoint(configEndpoint))
            endpointOverride = 'matches_config';
        else
            endpointOverride = 'differs';
        if (endpointOverride !== 'matches_config')
            issues.add('environment_endpoint_override');
    }
    var disabledValue = (_b = env.OTEL_SDK_DISABLED) === null || _b === void 0 ? void 0 : _b.trim().toLowerCase();
    var sdkDisabled = 'unset';
    if (disabledValue === 'true' || disabledValue === '1') {
        sdkDisabled = 'true';
        issues.add('otel_sdk_disabled');
    }
    else if (disabledValue === 'false' || disabledValue === '0')
        sdkDisabled = 'false';
    else if (disabledValue) {
        sdkDisabled = 'invalid';
        issues.add('otel_sdk_disabled_value_invalid');
    }
    var rawProtocol = (_c = env.OTEL_EXPORTER_OTLP_PROTOCOL) === null || _c === void 0 ? void 0 : _c.trim().toLowerCase();
    var protocol = 'unset';
    if (rawProtocol) {
        if (rawProtocol === 'grpc' || rawProtocol.startsWith('grpc/')) {
            protocol = 'grpc';
            issues.add('grpc_protocol_override');
        }
        else if (rawProtocol === 'http/protobuf' || rawProtocol === 'http/json')
            protocol = 'http';
        else {
            protocol = 'other';
            issues.add('protocol_override_unknown');
        }
    }
    return { summary: { endpoint_override: endpointOverride, sdk_disabled: sdkDisabled, protocol: protocol }, issues: __spreadArray([], issues, true) };
}
function normalizeEndpoint(value) {
    try {
        return new URL(value).origin;
    }
    catch (_a) {
        return undefined;
    }
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
