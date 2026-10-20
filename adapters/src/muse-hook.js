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
var __asyncValues = (this && this.__asyncValues) || function (o) {
    if (!Symbol.asyncIterator) throw new TypeError("Symbol.asyncIterator is not defined.");
    var m = o[Symbol.asyncIterator], i;
    return m ? m.call(o) : (o = typeof __values === "function" ? __values(o) : o[Symbol.iterator](), i = {}, verb("next"), verb("throw"), verb("return"), i[Symbol.asyncIterator] = function () { return this; }, i);
    function verb(n) { i[n] = o[n] && function (v) { return new Promise(function (resolve, reject) { v = o[n](v), settle(resolve, reject, v.done, v.value); }); }; }
    function settle(resolve, reject, d, v) { Promise.resolve(v).then(function(v) { resolve({ value: v, done: d }); }, reject); }
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.museHookEvents = exports.museManagedMarker = void 0;
exports.mapMuseHookPayload = mapMuseHookPayload;
var node_crypto_1 = require("node:crypto");
var node_url_1 = require("node:url");
var node_path_1 = require("node:path");
var emit_ts_1 = require("./emit.ts");
exports.museManagedMarker = '--threadforge-hook=muse';
exports.museHookEvents = [
    'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PermissionRequest', 'PostToolUse',
    'PostToolUseFailure', 'PreCompact', 'PostCompact', 'SubagentStart', 'SubagentStop',
    'Notification', 'Stop', 'SessionEnd',
];
var maxInputBytes = 1 << 20;
var maxContentBytes = 64 << 10;
var actions = {
    SessionStart: 'session.started',
    UserPromptSubmit: 'prompt.submitted',
    PreToolUse: 'tool.invoked',
    PermissionRequest: 'permission.requested',
    PostToolUse: 'tool.completed',
    PostToolUseFailure: 'tool.failed',
    PreCompact: 'session.compacting',
    PostCompact: 'session.compacted',
    SubagentStart: 'subagent.started',
    SubagentStop: 'subagent.completed',
    Notification: 'agent.notification',
    Stop: 'assistant.turn_stopped',
    SessionEnd: 'session.ended',
};
/** Maps the documented Muse event names and Beacon-observed payload envelope to local events. */
function mapMuseHookPayload(value, hookEvent, capturedAt) {
    var _a, _b;
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isRecord(value) || typeof hookEvent !== 'string' || !exports.museHookEvents.includes(hookEvent))
        return undefined;
    var eventName = hookEvent;
    var event = {
        timestamp: capturedAt.toISOString(),
        vendor: 'beacon',
        product: 'endpoint-agent',
        schema_version: '1.0',
        event: { kind: 'agent_runtime', action: actions[eventName], fidelity: 'observed' },
        severity: eventName === 'PostToolUseFailure' ? 'medium' : 'info',
        endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'muse_code', collection_method: 'hook' },
        hook: { name: eventName },
    };
    var sessionID = boundedString(value.session_id, 512);
    if (sessionID)
        event.session = { id: "muse:".concat(sha256(sessionID)) };
    var model = safeLabel(value.model, 128);
    if (model && model !== 'unknown')
        event.harness = __assign(__assign({}, event.harness), { version: model });
    var turnID = boundedString(value.turn_id, 512);
    if (turnID)
        event.raw = { turn_id_sha256: sha256(turnID) };
    if (eventName === 'SessionStart') {
        var source = enumLabel(value.source, ['startup', 'resume', 'clear', 'compact']);
        if (source)
            event.raw = __assign(__assign({}, (isRecord(event.raw) ? event.raw : {})), { source: source });
    }
    if (eventName === 'UserPromptSubmit') {
        var prompt_1 = contentDigest(value.prompt);
        if (prompt_1)
            event.content = contentMetadata(__assign(__assign(__assign({}, (prompt_1.hash ? { prompt_sha256: prompt_1.hash } : {})), { prompt_bytes: prompt_1.bytes }), (prompt_1.omitted ? { prompt_omitted: true } : {})));
    }
    if (['PreToolUse', 'PostToolUse', 'PostToolUseFailure', 'PermissionRequest'].includes(eventName)) {
        var toolName = safeLabel(value.tool_name, 128);
        if (toolName)
            event.tool = { name: toolName };
        var callID = boundedString((_a = value.tool_use_id) !== null && _a !== void 0 ? _a : value.tool_call_id, 512);
        if (callID)
            event.raw = __assign(__assign({}, (isRecord(event.raw) ? event.raw : {})), { tool_call_id_sha256: sha256(callID) });
        var permissionMode = enumLabel(value.permission_mode, ['default', 'acceptEdits', 'plan', 'bypassPermissions', 'dontAsk']);
        if (permissionMode)
            event.raw = __assign(__assign({}, (isRecord(event.raw) ? event.raw : {})), { permission_mode: permissionMode });
        var input = contentDigest(value.tool_input);
        var output = eventName === 'PostToolUse' || eventName === 'PostToolUseFailure' ? contentDigest(value.tool_response) : undefined;
        if (input || output)
            event.content = contentMetadata(__assign(__assign({}, (input ? __assign(__assign(__assign({}, (input.hash ? { input_sha256: input.hash } : {})), { input_bytes: input.bytes }), (input.omitted ? { input_omitted: true } : {})) : {})), (output ? __assign(__assign(__assign({}, (output.hash ? { output_sha256: output.hash } : {})), { output_bytes: output.bytes }), (output.omitted ? { output_omitted: true } : {})) : {})));
        if (eventName === 'PostToolUse' && toolName) {
            var classification = classifyTool(toolName, value.tool_input);
            if (classification.action)
                event.event = __assign(__assign({}, event.event), { action: classification.action });
            if (classification.operation)
                event.file = { operation: classification.operation };
        }
        if (eventName === 'PermissionRequest') {
            event.approval = { required: true, reason: 'permission request observed; no decision inferred' };
        }
    }
    if (eventName === 'Notification') {
        var notificationType = safeLabel(value.notification_type, 96);
        if (notificationType)
            event.raw = __assign(__assign({}, (isRecord(event.raw) ? event.raw : {})), { notification_type: notificationType });
    }
    if (eventName === 'SubagentStart' || eventName === 'SubagentStop') {
        var agentID = boundedString((_b = value.subagent_id) !== null && _b !== void 0 ? _b : value.agent_id, 512);
        var childSessionID = boundedString(value.child_session_id, 512);
        var agentType = safeLabel(value.agent_type, 96);
        event.raw = __assign(__assign(__assign(__assign({}, (isRecord(event.raw) ? event.raw : {})), (agentID ? { subagent_id_sha256: sha256(agentID) } : {})), (childSessionID ? { child_session_id_sha256: sha256(childSessionID) } : {})), (agentType ? { agent_type: agentType } : {}));
    }
    if (eventName === 'Stop') {
        var response = contentDigest(value.last_assistant_message);
        if (response)
            event.content = contentMetadata(__assign(__assign(__assign({}, (response.hash ? { response_sha256: response.hash } : {})), { response_bytes: response.bytes }), (response.omitted ? { response_omitted: true } : {})));
    }
    return event;
}
function classifyTool(toolName, input) {
    var normalized = toolName.toLowerCase();
    var objectInput = isRecord(input) ? input : {};
    if (['bash', 'shell', 'execute_command'].includes(normalized))
        return { action: 'command.executed' };
    if (normalized.startsWith('subagent_'))
        return {};
    var hasPath = ['path', 'file_path', 'target_file'].some(function (key) { return typeof objectInput[key] === 'string' && objectInput[key].length > 0; });
    if (normalized === 'read_skill')
        return { action: 'file.read', operation: 'read' };
    if (hasPath && /(?:read|search|list)/.test(normalized))
        return { action: 'file.read', operation: 'read' };
    if (hasPath && /(?:edit|write|create|patch)/.test(normalized)) {
        return { action: 'file.modified', operation: /create/.test(normalized) ? 'create' : 'modify' };
    }
    return {};
}
function contentDigest(value) {
    if (value === undefined || value === null)
        return undefined;
    var serialized;
    try {
        serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value));
    }
    catch (_a) {
        return undefined;
    }
    if (typeof serialized !== 'string')
        return undefined;
    var bytes = Buffer.byteLength(serialized, 'utf8');
    return __assign(__assign({}, (bytes > maxContentBytes ? {} : { hash: sha256(serialized) })), { bytes: bytes, omitted: bytes > maxContentBytes });
}
function contentMetadata(fields) {
    return __assign({ retention: 'metadata', included: false }, fields);
}
function enumLabel(value, allowed) {
    var normalized = boundedString(value, 64);
    return normalized && allowed.includes(normalized) ? normalized : undefined;
}
function boundedString(value, max) {
    if (typeof value !== 'string')
        return undefined;
    var normalized = value.trim();
    return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}
function safeLabel(value, max) {
    var normalized = boundedString(value, max);
    if (!normalized || !/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(normalized) || looksSecretLike(normalized))
        return undefined;
    return normalized;
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
}
function sha256(value) {
    return (0, node_crypto_1.createHash)('sha256').update(value, 'utf8').digest('hex');
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function normalizedOS(platform) {
    if (platform === 'win32')
        return 'windows';
    if (platform === 'darwin')
        return 'macos';
    if (platform === 'linux')
        return 'linux';
    return 'other';
}
function readBoundedStdin() {
    return __awaiter(this, void 0, void 0, function () {
        var chunks, total, _a, _b, _c, chunk, bytes, e_1_1;
        var _d, e_1, _e, _f;
        return __generator(this, function (_g) {
            switch (_g.label) {
                case 0:
                    chunks = [];
                    total = 0;
                    _g.label = 1;
                case 1:
                    _g.trys.push([1, 6, 7, 12]);
                    _a = true, _b = __asyncValues(process.stdin);
                    _g.label = 2;
                case 2: return [4 /*yield*/, _b.next()];
                case 3:
                    if (!(_c = _g.sent(), _d = _c.done, !_d)) return [3 /*break*/, 5];
                    _f = _c.value;
                    _a = false;
                    chunk = _f;
                    bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
                    total += bytes.byteLength;
                    if (total > maxInputBytes)
                        throw new Error('Muse hook payload exceeds the local size limit');
                    chunks.push(bytes);
                    _g.label = 4;
                case 4:
                    _a = true;
                    return [3 /*break*/, 2];
                case 5: return [3 /*break*/, 12];
                case 6:
                    e_1_1 = _g.sent();
                    e_1 = { error: e_1_1 };
                    return [3 /*break*/, 12];
                case 7:
                    _g.trys.push([7, , 10, 11]);
                    if (!(!_a && !_d && (_e = _b.return))) return [3 /*break*/, 9];
                    return [4 /*yield*/, _e.call(_b)];
                case 8:
                    _g.sent();
                    _g.label = 9;
                case 9: return [3 /*break*/, 11];
                case 10:
                    if (e_1) throw e_1.error;
                    return [7 /*endfinally*/];
                case 11: return [7 /*endfinally*/];
                case 12: return [2 /*return*/, Buffer.concat(chunks, total)];
            }
        });
    });
}
function main() {
    return __awaiter(this, void 0, void 0, function () {
        var input, payload, eventName, event_1, _a;
        var _b;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    _c.trys.push([0, 4, , 5]);
                    return [4 /*yield*/, readBoundedStdin()];
                case 1:
                    input = _c.sent();
                    payload = JSON.parse(input.toString('utf8'));
                    eventName = (_b = process.argv.slice(2).find(function (arg) { return arg.startsWith('--hook-event='); })) === null || _b === void 0 ? void 0 : _b.slice('--hook-event='.length);
                    event_1 = mapMuseHookPayload(payload, eventName);
                    if (!event_1) return [3 /*break*/, 3];
                    return [4 /*yield*/, (0, emit_ts_1.emitEvent)(event_1, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 })];
                case 2:
                    _c.sent();
                    _c.label = 3;
                case 3: return [3 /*break*/, 5];
                case 4:
                    _a = _c.sent();
                    process.stderr.write('threadforge Muse hook capture skipped: invalid payload or local intake unavailable\n');
                    return [3 /*break*/, 5];
                case 5:
                    // Muse's command-hook protocol uses an empty no-decision object; never print memory or raw data.
                    process.stdout.write('{}\n');
                    return [2 /*return*/];
            }
        });
    });
}
if (process.argv[1] && import.meta.url === (0, node_url_1.pathToFileURL)(node_path_1.default.resolve(process.argv[1])).href) {
    await main();
}
