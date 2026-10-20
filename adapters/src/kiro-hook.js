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
exports.kiroHookEvents = exports.kiroManagedMarker = void 0;
exports.mapKiroHookPayload = mapKiroHookPayload;
var node_crypto_1 = require("node:crypto");
var node_url_1 = require("node:url");
var node_path_1 = require("node:path");
var emit_ts_1 = require("./emit.ts");
exports.kiroManagedMarker = '--threadforge-hook=kiro';
exports.kiroHookEvents = ['SessionStart', 'AgentSpawn', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop'];
var maxInputBytes = 1 << 20;
var maxContentBytes = 64 << 10;
var actions = {
    SessionStart: 'session.started',
    AgentSpawn: 'session.started',
    UserPromptSubmit: 'prompt.submitted',
    PreToolUse: 'tool.invoked',
    PostToolUse: 'tool.completed',
    Stop: 'assistant.turn_stopped',
};
/** Maps Kiro v1 hook payloads to privacy-minimized local Threadforge events. */
function mapKiroHookPayload(value, hookEvent, capturedAt) {
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isRecord(value))
        return undefined;
    var eventName = normalizeHookEvent(hookEvent);
    if (!eventName)
        return undefined;
    var event = {
        timestamp: capturedAt.toISOString(),
        vendor: 'beacon',
        product: 'endpoint-agent',
        schema_version: '1.0',
        event: { kind: 'agent_runtime', action: actions[eventName], fidelity: 'observed' },
        severity: 'info',
        endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'kiro', collection_method: 'hook' },
        hook: { name: eventName },
    };
    var sessionId = boundedString(value.session_id, 512);
    if (sessionId)
        event.session = { id: "kiro:".concat(sha256(sessionId)) };
    if (eventName === 'UserPromptSubmit') {
        var prompt_1 = digest(value.prompt);
        if (prompt_1)
            event.content = contentMetadata({ prompt_sha256: prompt_1.hash, prompt_bytes: prompt_1.bytes });
    }
    if (eventName === 'PreToolUse' || eventName === 'PostToolUse') {
        var toolName = boundedString(value.tool_name, 256);
        var toolInput = isRecord(value.tool_input) ? value.tool_input : {};
        var toolResponse = isRecord(value.tool_response) ? value.tool_response : {};
        if (toolName) {
            var safeName = safeLabel(toolName, 128);
            if (safeName)
                event.tool = { name: safeName };
            var callId = boundedString(value.tool_call_id, 512);
            if (callId)
                event.raw = { tool_call_id_sha256: sha256(callId) };
            var input = digest(value.tool_input);
            var output = eventName === 'PostToolUse' ? digest(value.tool_response) : undefined;
            if (input || output)
                event.content = contentMetadata(__assign(__assign({}, (input ? { input_sha256: input.hash, input_bytes: input.bytes } : {})), (output ? { output_sha256: output.hash, output_bytes: output.bytes } : {})));
            if (eventName === 'PostToolUse') {
                if (toolResponse.success === false) {
                    event.event = __assign(__assign({}, event.event), { action: 'tool.failed' });
                    event.severity = 'medium';
                }
                else if (toolResponse.success === true) {
                    var classification = classifyTool(toolName);
                    if (classification.action)
                        event.event = __assign(__assign({}, event.event), { action: classification.action });
                    if (classification.mcp)
                        event.mcp = classification.mcp;
                    if (classification.operation)
                        event.file = { operation: classification.operation };
                }
            }
        }
    }
    if (eventName === 'Stop') {
        var response = digest(value.assistant_response);
        if (response)
            event.content = contentMetadata({ response_sha256: response.hash, response_bytes: response.bytes });
    }
    return event;
}
function normalizeHookEvent(value) {
    if (typeof value !== 'string')
        return undefined;
    var normalized = value.trim();
    return exports.kiroHookEvents.includes(normalized) ? normalized : undefined;
}
function classifyTool(name) {
    var normalized = name.trim().toLowerCase();
    if (['read', 'fs_read', 'fsread', 'read_file', 'read_files', 'list_directory', 'file_search', 'glob', 'grep_search', 'grep', 'code', 'read_code'].includes(normalized)) {
        return { action: 'file.read', operation: 'read' };
    }
    if (['write', 'fs_write', 'fswrite'].includes(normalized))
        return { action: 'file.modified', operation: 'create' };
    if (['fs_append', 'str_replace'].includes(normalized))
        return { action: 'file.modified', operation: 'modify' };
    if (normalized === 'delete_file')
        return { action: 'file.modified', operation: 'delete' };
    if (['shell', 'execute_bash', 'execute_cmd', 'control_bash_process'].includes(normalized))
        return { action: 'command.executed' };
    var mcp = /^@([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)$/.exec(name.trim());
    if (mcp)
        return { action: 'mcp.tool_invoked', mcp: { server: mcp[1], tool: mcp[2] } };
    return {};
}
function digest(value) {
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
    return bytes > maxContentBytes ? undefined : { hash: sha256(serialized), bytes: bytes };
}
function contentMetadata(fields) {
    return __assign({ retention: 'metadata', included: false }, fields);
}
function boundedString(value, max) {
    if (typeof value !== 'string')
        return undefined;
    var normalized = value.trim();
    return normalized.length > 0 && normalized.length <= max ? normalized : undefined;
}
function safeLabel(value, max) {
    if (value.length > max || !/^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(value) || looksSecretLike(value))
        return undefined;
    return value;
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function sha256(value) {
    return (0, node_crypto_1.createHash)('sha256').update(value, 'utf8').digest('hex');
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
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
                        throw new Error('Kiro hook payload exceeds the local size limit');
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
function hookEventFromArgs(args) {
    var _a;
    var value = (_a = args.find(function (arg) { return arg.startsWith('--hook-event='); })) === null || _a === void 0 ? void 0 : _a.slice('--hook-event='.length);
    return normalizeHookEvent(value);
}
function main() {
    return __awaiter(this, void 0, void 0, function () {
        var input, payload, eventName, event_1, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    _b.trys.push([0, 4, , 5]);
                    return [4 /*yield*/, readBoundedStdin()];
                case 1:
                    input = _b.sent();
                    payload = JSON.parse(input.toString('utf8'));
                    eventName = hookEventFromArgs(process.argv.slice(2));
                    event_1 = mapKiroHookPayload(payload, eventName);
                    if (!event_1) return [3 /*break*/, 3];
                    return [4 /*yield*/, (0, emit_ts_1.emitEvent)(event_1, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT, timeoutMs: 250 })];
                case 2:
                    _b.sent();
                    _b.label = 3;
                case 3: return [3 /*break*/, 5];
                case 4:
                    _a = _b.sent();
                    process.stderr.write('threadforge Kiro hook capture skipped: invalid payload or local intake unavailable\n');
                    return [3 /*break*/, 5];
                case 5: return [2 /*return*/];
            }
        });
    });
}
if (process.argv[1] && import.meta.url === (0, node_url_1.pathToFileURL)(node_path_1.default.resolve(process.argv[1])).href) {
    await main();
}
