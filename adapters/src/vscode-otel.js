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
exports.renderVSCodeOtelConfig = renderVSCodeOtelConfig;
exports.resolveVSCodeSettingsPath = resolveVSCodeSettingsPath;
exports.getVSCodeOtelStatus = getVSCodeOtelStatus;
exports.validateVSCodeOtelStatus = validateVSCodeOtelStatus;
exports.parseVSCodeOtelDuration = parseVSCodeOtelDuration;
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var openclaw_otel_ts_1 = require("./openclaw-otel.ts");
var local_otel_status_ts_1 = require("./local-otel-status.ts");
var defaultEndpoint = 'http://127.0.0.1:4318';
var maxSettingsBytes = 2 << 20;
var maxValidationWindowMs = 30 * 24 * 60 * 60000;
/** Print a VS Code Copilot Chat OTLP/HTTP settings fragment without changing VS Code settings. */
function renderVSCodeOtelConfig(endpoint) {
    if (endpoint === void 0) { endpoint = defaultEndpoint; }
    var normalized = validateLoopbackEndpoint(endpoint);
    return [
        'Merge these keys into existing VS Code User or Workspace settings; preserve unrelated JSONC settings:',
        '{',
        '  "github.copilot.chat.otel.enabled": true,',
        '  "github.copilot.chat.otel.exporterType": "otlp-http",',
        "  \"github.copilot.chat.otel.otlpEndpoint\": ".concat(JSON.stringify(normalized), ","),
        '  "github.copilot.chat.otel.captureContent": false,',
        '  "github.copilot.chat.otel.maxAttributeSizeChars": 8192',
        '}',
        '',
        'The endpoint must be local. This fragment does not install hooks, edit settings, or capture message content.',
        'Reload VS Code after a manual settings change; check status and local event validation afterward.',
    ].join('\n');
}
function resolveVSCodeSettingsPath(env, homeDir, platform) {
    var _a, _b;
    if (env === void 0) { env = process.env; }
    if (homeDir === void 0) { homeDir = node_os_1.default.homedir(); }
    if (platform === void 0) { platform = process.platform; }
    var pathApi = platform === 'win32' ? node_path_1.default.win32 : node_path_1.default.posix;
    if (platform === 'win32') {
        var appData = ((_a = env.APPDATA) === null || _a === void 0 ? void 0 : _a.trim()) || pathApi.join(homeDir, 'AppData', 'Roaming');
        return pathApi.join(appData, 'Code', 'User', 'settings.json');
    }
    if (platform === 'darwin')
        return pathApi.join(homeDir, 'Library', 'Application Support', 'Code', 'User', 'settings.json');
    var configHome = ((_b = env.XDG_CONFIG_HOME) === null || _b === void 0 ? void 0 : _b.trim()) || pathApi.join(homeDir, '.config');
    return pathApi.join(configHome, 'Code', 'User', 'settings.json');
}
/** Read VS Code JSONC settings and a bounded local event log without exposing either file's data. */
function getVSCodeOtelStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var env, homeDir, platform, settingsPath, logPath, issues, warnings, settingsResult, settings, telemetryEnabled, exporterSetting, rawExporter, outfileSetting, outfile, endpointSetting, endpointValue, exporterType, configuredEndpoint, contentCapture, telemetry;
        var _a, _b, _c, _d, _e, _f;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_g) {
            switch (_g.label) {
                case 0:
                    env = (_a = options.env) !== null && _a !== void 0 ? _a : process.env;
                    homeDir = (_b = options.homeDir) !== null && _b !== void 0 ? _b : node_os_1.default.homedir();
                    platform = (_c = options.platform) !== null && _c !== void 0 ? _c : process.platform;
                    settingsPath = (_d = options.settingsPath) !== null && _d !== void 0 ? _d : resolveVSCodeSettingsPath(env, homeDir, platform);
                    logPath = (_e = options.logPath) !== null && _e !== void 0 ? _e : (0, openclaw_otel_ts_1.resolveThreadforgeTracePath)(env, homeDir, platform);
                    issues = new Set();
                    warnings = new Set();
                    return [4 /*yield*/, readVSCodeSettings(settingsPath, issues)];
                case 1:
                    settingsResult = _g.sent();
                    settings = settingsResult.settings;
                    telemetryEnabled = parseBooleanSetting(settings, 'github.copilot.chat.otel.enabled');
                    exporterSetting = settings['github.copilot.chat.otel.exporterType'];
                    rawExporter = typeof exporterSetting === 'string' ? exporterSetting.trim() : '';
                    outfileSetting = settings['github.copilot.chat.otel.outfile'];
                    outfile = typeof outfileSetting === 'string' ? outfileSetting.trim() : '';
                    endpointSetting = settings['github.copilot.chat.otel.otlpEndpoint'];
                    endpointValue = typeof endpointSetting === 'string' ? endpointSetting.trim() : '';
                    exporterType = (exporterSetting !== undefined && typeof exporterSetting !== 'string') || (outfileSetting !== undefined && typeof outfileSetting !== 'string')
                        ? 'other'
                        : rawExporter === 'otlp-http' || !rawExporter && !outfile
                            ? rawExporter ? 'otlp_http' : 'unset'
                            : rawExporter === 'file' || outfile ? 'file' : 'other';
                    configuredEndpoint = endpointSetting !== undefined && typeof endpointSetting !== 'string'
                        ? 'invalid'
                        : endpointValue ? classifyEndpoint(endpointValue)
                            : telemetryEnabled === 'enabled' ? 'default_loopback_http' : 'missing';
                    contentCapture = parseBooleanSetting(settings, 'github.copilot.chat.otel.captureContent');
                    if (!settingsResult.inspected)
                        issues.add(settingsResult.missing ? 'settings_missing' : 'settings_unavailable');
                    if (telemetryEnabled === 'disabled' || telemetryEnabled === 'unset')
                        issues.add('otel_not_enabled');
                    if (telemetryEnabled === 'invalid')
                        issues.add('otel_enabled_value_invalid');
                    if (exporterType === 'file')
                        issues.add('file_exporter_bypasses_local_otlp');
                    else if (exporterType === 'other')
                        issues.add('exporter_type_unsupported');
                    if (outfileSetting !== undefined && typeof outfileSetting !== 'string')
                        issues.add('file_exporter_setting_invalid');
                    if (endpointSetting !== undefined && typeof endpointSetting !== 'string')
                        issues.add('endpoint_value_invalid');
                    if (configuredEndpoint === 'remote_http')
                        issues.add('remote_endpoint_not_allowed_by_local_default');
                    if (configuredEndpoint === 'invalid')
                        issues.add('endpoint_invalid');
                    if (configuredEndpoint === 'missing')
                        issues.add('endpoint_missing');
                    if (contentCapture === 'enabled')
                        warnings.add('message_content_capture_enabled');
                    if (contentCapture === 'invalid')
                        issues.add('content_capture_value_invalid');
                    return [4 /*yield*/, (0, local_otel_status_ts_1.scanLocalOtelEventLog)(logPath, 'vscode_copilot', (_f = options.now) !== null && _f !== void 0 ? _f : new Date())];
                case 2:
                    telemetry = _g.sent();
                    if (!telemetry.present)
                        issues.add('event_log_missing');
                    if (!telemetry.complete)
                        issues.add('event_log_incomplete');
                    if (!telemetry.last_event_observed)
                        issues.add('no_vscode_copilot_otlp_events');
                    return [2 /*return*/, {
                            schema_version: 'threadforge.vscode-otel-status.v1',
                            inspection_complete: settingsResult.inspected && telemetry.complete,
                            telemetry_enabled: telemetryEnabled,
                            exporter_type: exporterType,
                            configured_endpoint: configuredEndpoint,
                            content_capture: contentCapture,
                            warnings: __spreadArray([], warnings, true),
                            issues: __spreadArray([], issues, true),
                            telemetry: telemetry,
                        }];
            }
        });
    });
}
function validateVSCodeOtelStatus(status, sinceMs, now) {
    if (sinceMs === void 0) { sinceMs = 10 * 60000; }
    if (now === void 0) { now = new Date(); }
    if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > maxValidationWindowMs) {
        throw new Error('VS Code OTLP validation window must be between 1 ms and 30 days');
    }
    var issues = new Set(status.issues);
    var timestamp = status.telemetry.last_event_observed_at ? Date.parse(status.telemetry.last_event_observed_at) : Number.NaN;
    if (!Number.isFinite(timestamp) || timestamp < now.getTime() - sinceMs || timestamp > now.getTime() + 60000) {
        issues.add('no_recent_vscode_copilot_otlp_event');
    }
    var valid = status.inspection_complete && status.telemetry_enabled === 'enabled' &&
        ['default_loopback_http', 'loopback_http'].includes(status.configured_endpoint) &&
        ['otlp_http', 'unset'].includes(status.exporter_type) && issues.size === 0;
    return { schema_version: 'threadforge.vscode-otel-validation.v1', valid: valid, since_ms: sinceMs, issues: __spreadArray([], issues, true), warnings: status.warnings, status: status };
}
function parseVSCodeOtelDuration(value) {
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
function readVSCodeSettings(filePath, issues) {
    return __awaiter(this, void 0, void 0, function () {
        var handle, pathInfo, info, afterOpen, bytes, bytesRead, text, value, error_1;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    pathInfo = _a.sent();
                    if (pathInfo.isSymbolicLink() || !pathInfo.isFile()) {
                        issues.add(pathInfo.isSymbolicLink() ? 'settings_symlink_refused' : 'settings_not_regular_file');
                        return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                    }
                    if (pathInfo.size > maxSettingsBytes) {
                        issues.add('settings_oversized');
                        return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                    }
                    return [4 /*yield*/, (0, promises_1.open)(filePath, 'r')];
                case 2:
                    handle = _a.sent();
                    return [4 /*yield*/, handle.stat()];
                case 3:
                    info = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 4:
                    afterOpen = _a.sent();
                    if (afterOpen.isSymbolicLink() || !afterOpen.isFile() || info.size > maxSettingsBytes) {
                        issues.add('settings_changed_or_symlinked');
                        return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                    }
                    bytes = Buffer.alloc(info.size);
                    return [4 /*yield*/, handle.read(bytes, 0, bytes.length, 0)];
                case 5:
                    bytesRead = (_a.sent()).bytesRead;
                    text = void 0;
                    try {
                        text = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, bytesRead));
                    }
                    catch (_b) {
                        issues.add('settings_invalid_utf8');
                        return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                    }
                    try {
                        value = JSON.parse(stripJsonc(text));
                        if (!isRecord(value))
                            throw new Error('settings root is not an object');
                        return [2 /*return*/, { inspected: true, missing: false, settings: value }];
                    }
                    catch (_c) {
                        issues.add('settings_invalid_jsonc');
                        return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                    }
                    return [3 /*break*/, 9];
                case 6:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'ENOENT'))
                        return [2 /*return*/, { inspected: false, missing: true, settings: {} }];
                    issues.add('settings_unreadable');
                    return [2 /*return*/, { inspected: false, missing: false, settings: {} }];
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close())];
                case 8:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function stripJsonc(input) {
    var output = '';
    var inString = false;
    var escaped = false;
    var lineComment = false;
    var blockComment = false;
    for (var index = 0; index < input.length; index++) {
        var current = input[index];
        var next = input[index + 1];
        if (lineComment) {
            if (current === '\n' || current === '\r') {
                lineComment = false;
                output += current;
            }
            else
                output += ' ';
            continue;
        }
        if (blockComment) {
            if (current === '*' && next === '/') {
                output += '  ';
                index++;
                blockComment = false;
            }
            else
                output += current === '\n' || current === '\r' ? current : ' ';
            continue;
        }
        if (inString) {
            output += current;
            if (escaped)
                escaped = false;
            else if (current === '\\')
                escaped = true;
            else if (current === '"')
                inString = false;
            continue;
        }
        if (current === '"') {
            inString = true;
            output += current;
            continue;
        }
        if (current === '/' && next === '/') {
            output += '  ';
            index++;
            lineComment = true;
            continue;
        }
        if (current === '/' && next === '*') {
            output += '  ';
            index++;
            blockComment = true;
            continue;
        }
        output += current;
    }
    if (blockComment)
        throw new Error('VS Code settings contain an unterminated JSONC comment');
    var clean = '';
    inString = false;
    escaped = false;
    for (var index = 0; index < output.length; index++) {
        var current = output[index];
        if (inString) {
            clean += current;
            if (escaped)
                escaped = false;
            else if (current === '\\')
                escaped = true;
            else if (current === '"')
                inString = false;
            continue;
        }
        if (current === '"') {
            inString = true;
            clean += current;
            continue;
        }
        if (current === ',') {
            var next = index + 1;
            while (next < output.length && /\s/.test(output[next]))
                next++;
            if (output[next] === '}' || output[next] === ']')
                continue;
        }
        clean += current;
    }
    return clean;
}
function parseBooleanSetting(settings, key) {
    var value = settings[key];
    if (value === undefined)
        return 'unset';
    if (value === true || value === 'true' || value === 1 || value === '1')
        return 'enabled';
    if (value === false || value === 'false' || value === 0 || value === '0')
        return 'disabled';
    return 'invalid';
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
        throw new Error('VS Code OTLP endpoint must be a loopback HTTP base URL without credentials, path, query, or fragment');
    }
    return new URL(value).origin;
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
