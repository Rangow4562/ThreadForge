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
exports.hermesHookEvents = exports.hermesManagedMarker = void 0;
exports.mapHermesHookPayload = mapHermesHookPayload;
exports.renderHermesHookConfig = renderHermesHookConfig;
exports.runHermesHook = runHermesHook;
var node_crypto_1 = require("node:crypto");
var node_url_1 = require("node:url");
var emit_ts_1 = require("./emit.ts");
exports.hermesManagedMarker = '--threadforge-hermes-hook=v1';
exports.hermesHookEvents = [
    'on_session_start', 'pre_llm_call', 'pre_tool_call', 'post_tool_call', 'pre_approval_request',
    'post_approval_response', 'subagent_stop', 'on_session_end', 'on_session_finalize',
];
var maxInputBytes = 1 << 20;
var maxDigestBytes = 64 << 10;
/** Map one Beacon-observed Hermes shell-hook payload to privacy-minimized events. */
function mapHermesHookPayload(eventName, value, capturedAt) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m, _o, _p, _q, _r, _s;
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isSupportedEvent(eventName) || !isRecord(value))
        return [];
    var extra = isRecord(value.extra) ? value.extra : {};
    var sessionId = safeOpaque(firstString(value, ['session_id', 'sessionId', 'session_key', 'task_id']) || firstString(extra, ['session_id', 'sessionId']), 512);
    var cwd = safeText((_c = (_b = (_a = value.cwd) !== null && _a !== void 0 ? _a : value.working_directory) !== null && _b !== void 0 ? _b : extra.cwd) !== null && _c !== void 0 ? _c : extra.working_directory, 4096);
    var timestamp = capturedAt.toISOString();
    var makeEvent = function (action, category, suffix, message, severity) {
        var _a;
        if (severity === void 0) { severity = 'info'; }
        var event = {
            timestamp: timestamp,
            vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
            event: { id: sha256("".concat(eventName, ":").concat(sessionId !== null && sessionId !== void 0 ? sessionId : '', ":").concat(suffix, ":").concat(timestamp)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
            severity: severity,
            endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'hermes', collection_method: 'hook' },
            hook: { name: eventName },
            raw: __assign({ source: 'hermes_hook', event_type: eventName, message: message }, (cwd ? { working_directory_sha256: sha256(cwd) } : {})),
        };
        if (sessionId)
            event.session = { id: "hermes:".concat(sha256(sessionId)) };
        var model = safeLabel((_a = value.model) !== null && _a !== void 0 ? _a : extra.model, 160);
        if (model)
            event.model = model;
        return event;
    };
    if (eventName === 'on_session_start')
        return [makeEvent('session.started', 'session', 'session-start', 'Hermes session started')];
    if (eventName === 'on_session_end' || eventName === 'on_session_finalize') {
        return [makeEvent('session.ended', 'session', eventName, 'Hermes session ended')];
    }
    if (eventName === 'pre_llm_call') {
        var event_1 = makeEvent('prompt.submitted', 'prompt', 'prompt', 'Hermes prompt submitted');
        var prompt_1 = (_d = extra.user_message) !== null && _d !== void 0 ? _d : value.user_message;
        var digest = contentDigest(prompt_1);
        if (digest)
            event_1.content = digest;
        return [event_1];
    }
    if (eventName === 'pre_approval_request' || eventName === 'post_approval_response') {
        return [mapApproval(eventName, value, extra, makeEvent)];
    }
    if (eventName === 'subagent_stop') {
        var event_2 = makeEvent('subagent.stopped', 'agent', 'subagent-stop', 'Hermes subagent stopped');
        var role = safeLabel(extra.child_role, 96);
        var status_1 = safeLabel(extra.child_status, 96);
        var duration = nonNegativeInteger(extra.duration_ms);
        var childId = safeOpaque((_e = extra.child_session_id) !== null && _e !== void 0 ? _e : extra.session_id, 512);
        event_2.raw = __assign(__assign({}, event_2.raw), { subagent: __assign(__assign(__assign(__assign({}, (role ? { role: role } : {})), (status_1 ? { status: status_1 } : {})), (duration !== undefined ? { duration_ms: duration } : {})), (childId ? { session_id_sha256: sha256(childId) } : {})) });
        return [event_2];
    }
    var toolInput = firstRecord(value.tool_input, value.toolInput, extra.tool_input);
    var toolName = (_h = safeLabel((_g = (_f = value.tool_name) !== null && _f !== void 0 ? _f : value.toolName) !== null && _g !== void 0 ? _g : extra.tool_name, 128)) !== null && _h !== void 0 ? _h : 'unknown';
    var toolId = safeOpaque((_l = (_k = (_j = value.tool_call_id) !== null && _j !== void 0 ? _j : value.toolCallId) !== null && _k !== void 0 ? _k : value.tool_use_id) !== null && _l !== void 0 ? _l : extra.tool_call_id, 512);
    var command = boundedString((_m = toolInput.command) !== null && _m !== void 0 ? _m : extra.command, 1 << 20);
    if (eventName === 'pre_tool_call') {
        var event_3 = makeEvent('tool.invoked', 'tool', "pre-tool:".concat(toolName), 'Hermes tool invocation observed');
        event_3.tool = { name: toolName };
        var digest = contentDigest(toolInput);
        if (digest)
            event_3.content = digest;
        if (command)
            event_3.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
        if (toolId)
            event_3.gen_ai = { tool: { call: { id: "hermes-call:".concat(sha256(toolId)) } } };
        return [event_3];
    }
    var result = parseResult((_o = extra.result) !== null && _o !== void 0 ? _o : value.result);
    var resultError = boundedString(result.error, 4096);
    var explicitFailure = result.is_error === true || resultError !== undefined;
    var output = resultText(result);
    var toolAction = classifyHermesTool(toolName, toolInput);
    var event = makeEvent(explicitFailure ? 'tool.failed' : toolAction.action, explicitFailure ? 'tool' : toolAction.category, "post-tool:".concat(toolName), explicitFailure ? 'Hermes tool failed' : 'Hermes tool result observed', explicitFailure ? 'high' : 'info');
    event.tool = { name: toolName };
    var inputDigest = contentDigest(toolInput);
    if (inputDigest)
        event.content = inputDigest;
    if (toolId)
        event.gen_ai = { tool: { call: { id: "hermes-call:".concat(sha256(toolId)) } } };
    if (explicitFailure)
        event.error = { type: 'tool_error' };
    if (command)
        event.command = __assign(__assign({ command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') }, (isInteger(result.exit_code) ? { exit_code: result.exit_code } : {})), (output ? { output_sha256: sha256(output), output_bytes: Buffer.byteLength(output, 'utf8') } : {}));
    if (output)
        event.content = contentDigest(output);
    var filePath = boundedString((_r = (_q = (_p = toolInput.path) !== null && _p !== void 0 ? _p : toolInput.file_path) !== null && _q !== void 0 ? _q : toolInput.target_path) !== null && _r !== void 0 ? _r : result.path, 4096);
    if (filePath && toolAction.category === 'file')
        event.file = { path_sha256: sha256(filePath), operation: (_s = toolAction.operation) !== null && _s !== void 0 ? _s : 'read' };
    return [event];
}
function mapApproval(eventName, value, extra, makeEvent) {
    var _a, _b, _c, _d, _e, _f, _g, _h;
    var command = safeText((_a = extra.command) !== null && _a !== void 0 ? _a : value.command, 1 << 20);
    var description = safeText((_c = (_b = extra.description) !== null && _b !== void 0 ? _b : extra.reason) !== null && _c !== void 0 ? _c : value.description, 4096);
    if (eventName === 'pre_approval_request') {
        var event_4 = makeEvent('approval.requested', 'approval', 'approval-request', 'Hermes approval requested');
        event_4.approval = __assign({ required: true, decision: 'requested' }, (description ? { reason_sha256: sha256(description) } : {}));
        if (command)
            event_4.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
        var tool = safeLabel((_d = extra.tool_name) !== null && _d !== void 0 ? _d : value.tool_name, 128);
        if (tool)
            event_4.tool = { name: tool };
        return event_4;
    }
    var rawChoice = String((_h = (_g = (_f = (_e = extra.choice) !== null && _e !== void 0 ? _e : extra.decision) !== null && _f !== void 0 ? _f : value.choice) !== null && _g !== void 0 ? _g : value.decision) !== null && _h !== void 0 ? _h : '').trim().toLowerCase();
    var decision = approvalDecision(rawChoice);
    var action = decision === 'approve' ? 'approval.allowed' : decision === 'deny' || decision === 'timeout' ? 'approval.denied' : 'approval.requested';
    var event = makeEvent(action, 'approval', 'approval-response', 'Hermes approval response observed');
    event.approval = __assign({ required: true, decision: decision }, (description ? { reason_sha256: sha256(description) } : {}));
    if (command)
        event.command = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
    return event;
}
function approvalDecision(value) {
    if (['approve', 'approved', 'allow', 'allowed', 'yes'].includes(value))
        return 'approve';
    if (['deny', 'denied', 'reject', 'rejected', 'no'].includes(value))
        return 'deny';
    if (['timeout', 'timed_out', 'expired'].includes(value))
        return 'timeout';
    return 'unknown';
}
function classifyHermesTool(name, input) {
    var lower = name.toLowerCase();
    if (lower === 'terminal' || boundedString(input.command, 1 << 20))
        return { action: 'command.executed', category: 'command' };
    if (/read|view|list|search|grep/.test(lower))
        return { action: 'file.read', category: 'file', operation: 'read' };
    if (/create|write/.test(lower))
        return { action: 'file.created', category: 'file', operation: 'create' };
    if (/edit|patch/.test(lower))
        return { action: 'file.modified', category: 'file', operation: 'modify' };
    if (lower.includes('mcp'))
        return { action: 'mcp.tool_invoked', category: 'mcp' };
    return { action: 'tool.completed', category: 'tool' };
}
function parseResult(value) {
    if (isRecord(value))
        return value;
    if (typeof value === 'string' && value.length <= maxInputBytes) {
        try {
            var parsed = JSON.parse(value);
            if (isRecord(parsed))
                return parsed;
        }
        catch ( /* opaque result */_a) { /* opaque result */ }
        return { output: value };
    }
    return {};
}
function resultText(value) {
    if (typeof value.output === 'string')
        return value.output;
    if (typeof value.result === 'string')
        return value.result;
    if (typeof value.content === 'string')
        return value.content;
    return '';
}
function renderHermesHookConfig(commandTemplate) {
    var _a, _b;
    var template = commandTemplate !== null && commandTemplate !== void 0 ? commandTemplate : defaultCommandTemplate();
    var eventSlots = typeof template === 'string' ? (_b = (_a = template.match(/\{event\}/g)) === null || _a === void 0 ? void 0 : _a.length) !== null && _b !== void 0 ? _b : 0 : 0;
    var markerSlots = typeof template === 'string' ? template.split(exports.hermesManagedMarker).length - 1 : 0;
    if (typeof template !== 'string' || eventSlots !== 1 || markerSlots !== 1 || /[\r\n\0]/.test(template) || looksSecretLike(template)) {
        throw new Error("command template must contain exactly one {event} and ".concat(exports.hermesManagedMarker, ", with no credentials or line breaks"));
    }
    var timeouts = {
        pre_llm_call: 30, pre_tool_call: 10, post_tool_call: 10, pre_approval_request: 10,
        post_approval_response: 10, subagent_stop: 10,
    };
    var lines = ['hooks:'];
    for (var _i = 0, hermesHookEvents_1 = exports.hermesHookEvents; _i < hermesHookEvents_1.length; _i++) {
        var event_5 = hermesHookEvents_1[_i];
        lines.push("  ".concat(event_5, ":"), '    - command: ' + JSON.stringify(template.replace('{event}', event_5)));
        if (timeouts[event_5] !== undefined)
            lines.push("      timeout: ".concat(timeouts[event_5]));
        if (event_5 === 'pre_tool_call' || event_5 === 'post_tool_call')
            lines.push('      matcher: ".*"');
    }
    return "".concat(lines.join('\n'), "\n");
}
function defaultCommandTemplate() {
    var cli = (0, node_url_1.fileURLToPath)(new URL('./cli.ts', import.meta.url));
    var quote = function (value) { return process.platform === 'win32' ? "\"".concat(value.replaceAll('"', '\\"'), "\"") : "'".concat(value.replaceAll("'", "'\\''"), "'"); };
    return "".concat(quote(process.execPath), " --experimental-strip-types ").concat(quote(cli), " hermes hook {event} ").concat(exports.hermesManagedMarker);
}
/** Hook runner is fail-open and never writes a decision or context response to stdout. */
function runHermesHook(eventName, input, endpoint) {
    return __awaiter(this, void 0, void 0, function () {
        var chunks, total, chunk, bytes, e_1_1, payload, events, _a;
        var _b, input_1, input_1_1;
        var _c, e_1, _d, _e;
        return __generator(this, function (_f) {
            switch (_f.label) {
                case 0:
                    _f.trys.push([0, 15, , 16]);
                    chunks = [];
                    total = 0;
                    _f.label = 1;
                case 1:
                    _f.trys.push([1, 6, 7, 12]);
                    _b = true, input_1 = __asyncValues(input);
                    _f.label = 2;
                case 2: return [4 /*yield*/, input_1.next()];
                case 3:
                    if (!(input_1_1 = _f.sent(), _c = input_1_1.done, !_c)) return [3 /*break*/, 5];
                    _e = input_1_1.value;
                    _b = false;
                    chunk = _e;
                    bytes = Buffer.from(chunk);
                    total += bytes.byteLength;
                    if (total > maxInputBytes)
                        return [2 /*return*/];
                    chunks.push(bytes);
                    _f.label = 4;
                case 4:
                    _b = true;
                    return [3 /*break*/, 2];
                case 5: return [3 /*break*/, 12];
                case 6:
                    e_1_1 = _f.sent();
                    e_1 = { error: e_1_1 };
                    return [3 /*break*/, 12];
                case 7:
                    _f.trys.push([7, , 10, 11]);
                    if (!(!_b && !_c && (_d = input_1.return))) return [3 /*break*/, 9];
                    return [4 /*yield*/, _d.call(input_1)];
                case 8:
                    _f.sent();
                    _f.label = 9;
                case 9: return [3 /*break*/, 11];
                case 10:
                    if (e_1) throw e_1.error;
                    return [7 /*endfinally*/];
                case 11: return [7 /*endfinally*/];
                case 12:
                    payload = JSON.parse(Buffer.concat(chunks, total).toString('utf8'));
                    events = mapHermesHookPayload(eventName, payload);
                    if (!events.length) return [3 /*break*/, 14];
                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(events, { endpoint: endpoint, timeoutMs: 250 })];
                case 13:
                    _f.sent();
                    _f.label = 14;
                case 14: return [3 /*break*/, 16];
                case 15:
                    _a = _f.sent();
                    return [3 /*break*/, 16];
                case 16: return [2 /*return*/];
            }
        });
    });
}
function isSupportedEvent(value) { return exports.hermesHookEvents.includes(value); }
function firstString(source, keys) { for (var _i = 0, keys_1 = keys; _i < keys_1.length; _i++) {
    var key = keys_1[_i];
    if (typeof source[key] === 'string' && source[key])
        return source[key];
} return ''; }
function firstRecord() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_1 = values; _a < values_1.length; _a++) {
        var value = values_1[_a];
        if (isRecord(value))
            return value;
    }
    return {};
}
function boundedString(value, max) { return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined; }
function safeText(value, max) { return typeof value === 'string' && value.length > 0 && value.length <= max ? value : undefined; }
function safeOpaque(value, max) { var text = boundedString(value, max); return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined; }
function safeLabel(value, max) {
    var text = typeof value === 'string' ? value.trim() : '';
    return text && text.length <= max && /^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) && !looksSecretLike(text) ? text : undefined;
}
function contentDigest(value) {
    var _a;
    if (value === undefined || value === null)
        return undefined;
    var serialized;
    try {
        serialized = typeof value === 'string' ? value : (_a = JSON.stringify(canonicalize(value))) !== null && _a !== void 0 ? _a : '';
    }
    catch (_b) {
        return undefined;
    }
    var bytes = Buffer.byteLength(serialized, 'utf8');
    return __assign(__assign({ retention: 'metadata', included: false }, (bytes <= maxDigestBytes ? { hash: sha256(serialized) } : { omitted: true })), { bytes: bytes });
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function nonNegativeInteger(value) { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined; }
function isInteger(value) { return typeof value === 'number' && Number.isSafeInteger(value); }
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value, 'utf8').digest('hex'); }
function normalizedOS(platform) { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
