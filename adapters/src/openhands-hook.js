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
exports.mapOpenHandsHookPayload = mapOpenHandsHookPayload;
exports.runOpenHandsHook = runOpenHandsHook;
var node_crypto_1 = require("node:crypto");
var emit_ts_1 = require("./emit.ts");
var maxInputBytes = 1 << 20;
var maxDigestBytes = 64 << 10;
var maxHookEvents = 100;
var supportedEvents = new Set(['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end']);
/** Convert one documented OpenHands hook payload into privacy-minimized observed events. */
function mapOpenHandsHookPayload(eventType, value, capturedAt, workingDirectory) {
    var _a, _b, _c, _d, _e, _f, _g;
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (workingDirectory === void 0) { workingDirectory = process.env.OPENHANDS_PROJECT_DIR; }
    if (!supportedEvents.has(eventType) || !isRecord(value))
        return [];
    var timestamp = capturedAt.toISOString();
    var sessionId = boundedString(value.session_id, 512);
    var cwd = boundedString((_b = (_a = value.working_dir) !== null && _a !== void 0 ? _a : value.workingDir) !== null && _b !== void 0 ? _b : workingDirectory, 4096);
    var base = function (action, category, suffix, message, severity) {
        if (severity === void 0) { severity = 'info'; }
        return (__assign(__assign({ timestamp: timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0', event: { id: sha256("".concat(eventType, ":").concat(sessionId !== null && sessionId !== void 0 ? sessionId : '', ":").concat(suffix, ":").concat(timestamp)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' }, severity: severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'openhands', collection_method: 'hook' }, hook: { name: eventType } }, (sessionId ? { session: { id: "openhands:".concat(sha256(sessionId)) } } : {})), { raw: __assign(__assign({ source: 'openhands_hook', event_type: eventType }, (cwd ? { working_directory_sha256: sha256(cwd) } : {})), { message: message }) }));
    };
    if (eventType === 'session_start')
        return [base('session.started', 'session', 'start', 'OpenHands session started')];
    if (eventType === 'session_end')
        return [base('session.ended', 'session', 'end', 'OpenHands session ended')];
    if (eventType === 'user_prompt_submit') {
        var event_1 = base('prompt.submitted', 'prompt', 'prompt', 'OpenHands prompt submitted');
        var digest = contentDigest(value.message);
        if (digest)
            event_1.content = digest;
        return [event_1];
    }
    if (eventType === 'stop')
        return [base('assistant.turn_stopped', 'agent', 'stop', 'OpenHands agent turn stopped')];
    var toolName = (_c = safeLabel(value.tool_name, 128)) !== null && _c !== void 0 ? _c : 'unknown';
    var input = isRecord(value.tool_input) ? value.tool_input : {};
    var response = isRecord(value.tool_response) ? value.tool_response : {};
    var toolDigest = contentDigest(value.tool_input);
    if (eventType === 'pre_tool_use') {
        var event_2 = base('tool.invoked', 'tool', "pre:".concat(toolName), 'OpenHands tool invocation observed');
        event_2.tool = { name: toolName };
        if (toolDigest)
            event_2.content = toolDigest;
        return [event_2];
    }
    var failed = response.is_error === true || (typeof response.is_error === 'string' && response.is_error.toLowerCase() === 'true');
    var outputText = observationText(response);
    var command = toolName.toLowerCase() === 'terminal' ? boundedString(input.command, 1 << 20) : undefined;
    var eventFor = function (action, category, suffix, message) {
        var _a;
        var event = base(action, category, "post:".concat(suffix), message, failed ? 'high' : 'info');
        event.tool = { name: toolName };
        event.raw = __assign(__assign({}, event.raw), { openhands: __assign({}, (typeof response.kind === 'string' ? { observation_kind: (_a = safeLabel(response.kind, 96)) !== null && _a !== void 0 ? _a : 'unknown' } : {})) });
        if (failed)
            event.error = { type: 'tool_error' };
        if (toolDigest)
            event.content = toolDigest;
        return event;
    };
    if (response.kind === 'MCPToolObservation') {
        var event_3 = eventFor(failed ? 'tool.failed' : 'mcp.tool_invoked', failed ? 'tool' : 'mcp', 'mcp', 'OpenHands MCP tool result observed');
        event_3.mcp = { tool: toolName };
        return [event_3];
    }
    if (command) {
        var event_4 = eventFor(failed ? 'tool.failed' : 'command.executed', failed ? 'tool' : 'command', 'command', 'OpenHands terminal command observed');
        event_4.command = __assign(__assign({ command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') }, (isInteger(response.exit_code) ? { exit_code: response.exit_code } : {})), (outputText ? { output_sha256: sha256(outputText), output_bytes: Buffer.byteLength(outputText, 'utf8') } : {}));
        if (outputText)
            event_4.content = contentDigest(outputText);
        return [event_4];
    }
    var lower = toolName.toLowerCase();
    var editorCommand = (_d = safeLabel(input.command, 64)) === null || _d === void 0 ? void 0 : _d.toLowerCase();
    var editorOperation = lower === 'file_editor' || lower === 'planning_file_editor'
        ? editorCommand === 'view' ? 'read' : ['create', 'str_replace', 'insert', 'undo_edit'].includes(editorCommand !== null && editorCommand !== void 0 ? editorCommand : '') ? 'modify' : undefined
        : undefined;
    var readTool = ['read_file', 'list_directory', 'glob', 'grep'].includes(lower);
    var editTool = ['apply_patch', 'write_file', 'edit'].includes(lower);
    if (!failed && (editorOperation || readTool || editTool)) {
        var filePath = boundedString((_g = (_f = (_e = input.path) !== null && _e !== void 0 ? _e : input.file_path) !== null && _f !== void 0 ? _f : response.path) !== null && _g !== void 0 ? _g : response.file_path, 4096);
        var operation = editorOperation !== null && editorOperation !== void 0 ? editorOperation : (readTool ? 'read' : lower === 'write_file' ? 'create' : 'modify');
        if (lower === 'apply_patch') {
            var changes = isRecord(response.commit) && isRecord(response.commit.changes)
                ? response.commit.changes : {};
            var entries = Object.entries(changes);
            if (entries.length > 0) {
                if (entries.length > maxHookEvents) {
                    var event_5 = eventFor('tool.completed', 'tool', 'patch-overflow', 'OpenHands patch exceeded per-hook file event limit');
                    event_5.raw = __assign(__assign({}, event_5.raw), { openhands: { mapping_truncated: true, changed_file_count: entries.length } });
                    return [event_5];
                }
                return entries.map(function (_a, index) {
                    var _b;
                    var sourcePath = _a[0], change = _a[1];
                    var record = isRecord(change) ? change : {};
                    var target = (_b = boundedString(record.move_path, 4096)) !== null && _b !== void 0 ? _b : sourcePath;
                    return makeFileEvent(eventFor, target, index, 'modify', record);
                });
            }
        }
        return [makeFileEvent(eventFor, filePath, 0, operation, response)];
    }
    var event = eventFor(failed ? 'tool.failed' : 'tool.completed', 'tool', 'result', 'OpenHands tool result observed');
    if (outputText)
        event.content = contentDigest(outputText);
    return [event];
}
function makeFileEvent(eventFor, filePath, index, operation, details) {
    var event = eventFor(operation === 'read' ? 'file.read' : 'file.modified', 'file', "file:".concat(index), 'OpenHands file activity observed');
    var pathHash = filePath ? sha256(filePath) : undefined;
    event.file = __assign(__assign({}, (pathHash ? { path_sha256: pathHash } : {})), { operation: operation });
    var oldContent = boundedString(details.old_content, maxInputBytes);
    var newContent = boundedString(details.new_content, maxInputBytes);
    if (oldContent !== undefined || newContent !== undefined) {
        var before = oldContent !== null && oldContent !== void 0 ? oldContent : '';
        var after = newContent !== null && newContent !== void 0 ? newContent : '';
        event.file = __assign(__assign({}, (pathHash ? { path_sha256: pathHash } : {})), { operation: operation, before_sha256: sha256(before), after_sha256: sha256(after), before_bytes: Buffer.byteLength(before, 'utf8'), after_bytes: Buffer.byteLength(after, 'utf8') });
        event.content = contentDigest({ before: before, after: after });
    }
    return event;
}
/** Read, map, and send a host hook event. Errors are swallowed so telemetry cannot affect the run. */
function runOpenHandsHook(eventType, input, endpoint) {
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
                    events = mapOpenHandsHookPayload(eventType, payload);
                    if (!events.length) return [3 /*break*/, 14];
                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(events, { endpoint: endpoint })];
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
function observationText(response) {
    if (!Array.isArray(response.content))
        return '';
    return response.content.flatMap(function (part) { return isRecord(part) && (!part.type || part.type === 'text') && typeof part.text === 'string' ? [part.text] : []; }).join('\n');
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
function boundedString(value, maximum) {
    return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined;
}
function safeLabel(value, maximum) {
    var text = typeof value === 'string' ? value.trim() : '';
    if (!text || text.length > maximum || !/^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text) || /^(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[_-]/i.test(text) ||
        /^[A-Za-z0-9_-]{40,}$/.test(text) || /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(text))
        return undefined;
    return text;
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isInteger(value) { return typeof value === 'number' && Number.isSafeInteger(value); }
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value, 'utf8').digest('hex'); }
function normalizedOS(platform) { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
