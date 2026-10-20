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
exports.buildOpenCodeSessionListCommand = buildOpenCodeSessionListCommand;
exports.buildOpenCodeExportCommand = buildOpenCodeExportCommand;
exports.getOpenCodeHistoryStatus = getOpenCodeHistoryStatus;
exports.syncOpenCodeHistory = syncOpenCodeHistory;
var node_child_process_1 = require("node:child_process");
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'opencode-history.v1.json';
var maxStateBytes = 8 << 20;
var maxCliOutputBytes = 16 << 20;
var maxSyncInputBytes = 128 << 20;
var maxSessions = 200;
var maxMessagesPerSession = 20000;
var maxPartsPerSession = 50000;
var maxEventsPerSync = 20000;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxRetainedContentBytes = 4 << 10;
var cliTimeoutMs = 45000;
function buildOpenCodeSessionListCommand(projectRoot, options) {
    var _a;
    if (options === void 0) { options = {}; }
    var root = absolutePath(projectRoot, '--project-root');
    var executable = validateExecutable(options.opencodeCli);
    var limit = boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 50, 1, maxSessions, '--max-sessions');
    return { executable: executable, cwd: root, args: ['session', 'list', '--format', 'json', '--max-count', String(limit + 1)] };
}
function buildOpenCodeExportCommand(projectRoot, sessionId, options) {
    if (options === void 0) { options = {}; }
    var root = absolutePath(projectRoot, '--project-root');
    var executable = validateExecutable(options.opencodeCli);
    if (!validSessionId(sessionId))
        throw new Error('OpenCode session id is invalid');
    return { executable: executable, cwd: root, args: ['export', sessionId, '--sanitize'] };
}
function getOpenCodeHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var statePath, state;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    statePath = resolveStatePath(options.statePath);
                    return [4 /*yield*/, readState(statePath)];
                case 1:
                    state = _b.sent();
                    _a = {
                        schema_version: 'threadforge.opencode-history-status.v1',
                        tracked_sessions: Object.keys(state.sessions).length
                    };
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).then(function () { return true; }, function (error) {
                            if (isCode(error, 'ENOENT'))
                                return false;
                            throw new Error('unable to inspect OpenCode history state');
                        })];
                case 2: return [2 /*return*/, (_a.cursor_file_present = _b.sent(),
                        _a)];
            }
        });
    });
}
function syncOpenCodeHistory(options) {
    return __awaiter(this, void 0, void 0, function () {
        var root, statePath, projectHash, result, runner, maxSessionCount, operation, _a;
        var _this = this;
        var _b, _c, _d;
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveProjectRoot(options.projectRoot)];
                case 1:
                    root = _e.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(root, statePath))
                        throw new Error('OpenCode history cursor must be stored outside the selected project root');
                    projectHash = sha256(root);
                    result = emptyResult(projectHash, (_b = options.includeContent) !== null && _b !== void 0 ? _b : false);
                    runner = (_c = options.commandRunner) !== null && _c !== void 0 ? _c : runCommand;
                    maxSessionCount = (_d = options.maxSessions) !== null && _d !== void 0 ? _d : 50;
                    operation = function (state, persist) { return __awaiter(_this, void 0, void 0, function () {
                        var listingText, listed, _a, totalSourceBytes, sessions, mappedEvents, _loop_1, _i, sessions_1, row, state_1;
                        var _b, _c, _d, _e, _f, _g, _h;
                        return __generator(this, function (_j) {
                            switch (_j.label) {
                                case 0:
                                    _j.trys.push([0, 2, , 3]);
                                    return [4 /*yield*/, runner(buildOpenCodeSessionListCommand(root, { opencodeCli: options.opencodeCli, maxSessions: maxSessionCount }))];
                                case 1:
                                    listingText = _j.sent();
                                    listed = parseSessionList(listingText, maxSessionCount + 1);
                                    return [3 /*break*/, 3];
                                case 2:
                                    _a = _j.sent();
                                    result.errors.push({ message: 'OpenCode session listing could not be read or validated' });
                                    return [2 /*return*/];
                                case 3:
                                    totalSourceBytes = Buffer.byteLength(listingText, 'utf8');
                                    result.sessions_truncated = listed.length > maxSessionCount;
                                    sessions = listed.slice(0, maxSessionCount);
                                    result.sessions = sessions.length;
                                    mappedEvents = 0;
                                    _loop_1 = function (row) {
                                        var sessionId, sessionHash, mapped, output, _k, previous, changed, _l, changed_1, candidate, failed, offset, batch, _m, cursorEvents, _o, batch_1, candidate, nextEvents, _p, _q, candidate, before, after;
                                        return __generator(this, function (_r) {
                                            switch (_r.label) {
                                                case 0:
                                                    sessionId = boundedString(row.id, 256);
                                                    if (!sessionId) {
                                                        result.errors.push({ message: 'OpenCode returned a session without a valid identifier' });
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    sessionHash = sha256(sessionId);
                                                    mapped = void 0;
                                                    _r.label = 1;
                                                case 1:
                                                    _r.trys.push([1, 3, , 4]);
                                                    return [4 /*yield*/, runner(buildOpenCodeExportCommand(root, sessionId, { opencodeCli: options.opencodeCli }))];
                                                case 2:
                                                    output = _r.sent();
                                                    totalSourceBytes += Buffer.byteLength(output, 'utf8');
                                                    if (totalSourceBytes > maxSyncInputBytes)
                                                        throw new Error('OpenCode history sync exceeded its total source-byte budget');
                                                    mapped = mapExport(parseExport(output, sessionId), sessionHash, (_b = options.includeContent) !== null && _b !== void 0 ? _b : false);
                                                    return [3 /*break*/, 4];
                                                case 3:
                                                    _k = _r.sent();
                                                    result.errors.push({ session_sha256: sessionHash, message: 'OpenCode session export could not be read or validated' });
                                                    return [2 /*return*/, "continue"];
                                                case 4:
                                                    mappedEvents += mapped.events.length;
                                                    if (mappedEvents > maxEventsPerSync) {
                                                        result.errors.push({ session_sha256: sessionHash, message: 'OpenCode history sync exceeded the event processing limit' });
                                                        return [2 /*return*/, "break"];
                                                    }
                                                    previous = (_d = (_c = state.sessions[sessionHash]) === null || _c === void 0 ? void 0 : _c.events) !== null && _d !== void 0 ? _d : {};
                                                    changed = mapped.events.filter(function (candidate) { return previous[candidate.key] !== candidate.fingerprint; });
                                                    if (changed.length + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                                        result.errors.push({ session_sha256: sessionHash, message: 'OpenCode history sync exceeded the event processing limit' });
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    if (options.preview) {
                                                        result.events_previewed += changed.length;
                                                        (_e = result.events) !== null && _e !== void 0 ? _e : (result.events = []);
                                                        for (_l = 0, changed_1 = changed; _l < changed_1.length; _l++) {
                                                            candidate = changed_1[_l];
                                                            if (result.events.length < maxPreviewEvents)
                                                                result.events.push(candidate.event);
                                                            else
                                                                result.preview_truncated = true;
                                                        }
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    failed = false;
                                                    offset = 0;
                                                    _r.label = 5;
                                                case 5:
                                                    if (!(offset < changed.length)) return [3 /*break*/, 12];
                                                    batch = changed.slice(offset, offset + batchSize);
                                                    _r.label = 6;
                                                case 6:
                                                    _r.trys.push([6, 8, , 9]);
                                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(batch.map(function (candidate) { return candidate.event; }), { endpoint: (_f = options.endpoint) !== null && _f !== void 0 ? _f : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                                case 7:
                                                    _r.sent();
                                                    return [3 /*break*/, 9];
                                                case 8:
                                                    _m = _r.sent();
                                                    result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected OpenCode history events' });
                                                    failed = true;
                                                    return [3 /*break*/, 12];
                                                case 9:
                                                    cursorEvents = (_h = (_g = state.sessions[sessionHash]) === null || _g === void 0 ? void 0 : _g.events) !== null && _h !== void 0 ? _h : {};
                                                    for (_o = 0, batch_1 = batch; _o < batch_1.length; _o++) {
                                                        candidate = batch_1[_o];
                                                        cursorEvents[candidate.key] = candidate.fingerprint;
                                                    }
                                                    state.sessions[sessionHash] = { events: cursorEvents };
                                                    result.events_emitted += batch.length;
                                                    if (!persist) return [3 /*break*/, 11];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 10:
                                                    _r.sent();
                                                    _r.label = 11;
                                                case 11:
                                                    offset += batchSize;
                                                    return [3 /*break*/, 5];
                                                case 12:
                                                    if (failed)
                                                        return [2 /*return*/, "continue"];
                                                    nextEvents = {};
                                                    for (_p = 0, _q = mapped.events; _p < _q.length; _p++) {
                                                        candidate = _q[_p];
                                                        nextEvents[candidate.key] = candidate.fingerprint;
                                                    }
                                                    before = JSON.stringify(previous);
                                                    after = JSON.stringify(nextEvents);
                                                    if (!(before !== after)) return [3 /*break*/, 14];
                                                    state.sessions[sessionHash] = { events: nextEvents };
                                                    result.sessions_changed++;
                                                    if (!persist) return [3 /*break*/, 14];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 13:
                                                    _r.sent();
                                                    _r.label = 14;
                                                case 14: return [2 /*return*/];
                                            }
                                        });
                                    };
                                    _i = 0, sessions_1 = sessions;
                                    _j.label = 4;
                                case 4:
                                    if (!(_i < sessions_1.length)) return [3 /*break*/, 7];
                                    row = sessions_1[_i];
                                    return [5 /*yield**/, _loop_1(row)];
                                case 5:
                                    state_1 = _j.sent();
                                    if (state_1 === "break")
                                        return [3 /*break*/, 7];
                                    _j.label = 6;
                                case 6:
                                    _i++;
                                    return [3 /*break*/, 4];
                                case 7:
                                    if (!persist) return [3 /*break*/, 9];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 8:
                                    _j.sent();
                                    _j.label = 9;
                                case 9: return [2 /*return*/];
                            }
                        });
                    }); };
                    if (!options.preview) return [3 /*break*/, 4];
                    _a = operation;
                    return [4 /*yield*/, readState(statePath)];
                case 2: return [4 /*yield*/, _a.apply(void 0, [_e.sent(), false])];
                case 3:
                    _e.sent();
                    return [3 /*break*/, 6];
                case 4: return [4 /*yield*/, withStateLock(statePath, function () { return __awaiter(_this, void 0, void 0, function () { var _a; return __generator(this, function (_b) {
                        switch (_b.label) {
                            case 0:
                                _a = operation;
                                return [4 /*yield*/, readState(statePath)];
                            case 1: return [2 /*return*/, _a.apply(void 0, [_b.sent(), true])];
                        }
                    }); }); })];
                case 5:
                    _e.sent();
                    _e.label = 6;
                case 6:
                    result.complete = result.errors.length === 0 && !result.sessions_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(projectHash, includeContent) {
    return {
        schema_version: 'threadforge.opencode-history-sync.v1', project_sha256: projectHash,
        sessions: 0, sessions_changed: 0, events_emitted: 0, events_previewed: 0, sessions_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false, preview_truncated: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 50, 1, maxSessions, '--max-sessions');
    absolutePath(options.projectRoot, '--project-root');
    validateExecutable(options.opencodeCli);
}
function validateExecutable(value) {
    var executable = (value === null || value === void 0 ? void 0 : value.trim()) || 'opencode';
    if (/[\0\r\n]/.test(executable))
        throw new Error('OpenCode CLI path contains invalid characters');
    return executable;
}
function boundedInteger(value, min, max, label) {
    if (!Number.isSafeInteger(value) || value < min || value > max)
        throw new Error("".concat(label, " must be an integer between ").concat(min, " and ").concat(max));
    return value;
}
function absolutePath(value, label) {
    if (!node_path_1.default.isAbsolute(value))
        throw new Error("".concat(label, " must be an absolute path"));
    return node_path_1.default.resolve(value);
}
function resolveProjectRoot(value) {
    return __awaiter(this, void 0, void 0, function () {
        var root, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    root = absolutePath(value, '--project-root');
                    return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function () { return null; })];
                case 1:
                    info = _a.sent();
                    if (!info || !info.isDirectory())
                        throw new Error('--project-root must be an existing directory');
                    return [2 /*return*/, (0, promises_1.realpath)(root).catch(function () { throw new Error('--project-root could not be resolved'); })];
            }
        });
    });
}
function parseSessionList(text, maxRows) {
    if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes)
        throw new Error('OpenCode session listing exceeded the local output limit');
    if (!text.trim())
        return [];
    var parsed = parseJson(text, 'OpenCode session listing');
    if (!Array.isArray(parsed) || parsed.length > maxRows)
        throw new Error('OpenCode session listing has an unsupported shape');
    return parsed.map(function (row) {
        if (!isRecord(row) || !validSessionId(row.id))
            throw new Error('OpenCode session listing contains an invalid row');
        return row;
    });
}
function parseExport(text, expectedSessionId) {
    if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes)
        throw new Error('OpenCode export exceeded the local size limit');
    var parsed = parseJson(text, 'OpenCode session export');
    if (!isRecord(parsed) || !isRecord(parsed.info) || parsed.info.id !== expectedSessionId || !Array.isArray(parsed.messages)) {
        throw new Error('OpenCode session export has an unsupported shape');
    }
    if (parsed.messages.length > maxMessagesPerSession)
        throw new Error('OpenCode session export exceeded the message limit');
    var parts = 0;
    var messages = parsed.messages.map(function (message) {
        if (!isRecord(message) || !isRecord(message.info) || !Array.isArray(message.parts))
            throw new Error('OpenCode export contains an invalid message');
        parts += message.parts.length;
        if (parts > maxPartsPerSession || !boundedString(message.info.id, 256))
            throw new Error('OpenCode export exceeded the part limit or omitted a message id');
        for (var _i = 0, _a = message.parts; _i < _a.length; _i++) {
            var part = _a[_i];
            if (!isRecord(part))
                throw new Error('OpenCode export contains an invalid part');
        }
        return message;
    });
    return { info: parsed.info, messages: messages };
}
function mapExport(source, sessionHash, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m, _o, _p, _q, _r, _s, _t;
    var result = { events: [], liveKeys: new Set() };
    var add = function (keyMaterial, fingerprintMaterial, event) {
        var key = sha256(keyMaterial);
        var fingerprint = sha256(fingerprintMaterial);
        if (result.liveKeys.has(key))
            throw new Error('OpenCode export contains a duplicate event identity');
        result.liveKeys.add(key);
        event.event.id = sha256("".concat(sessionHash, ":").concat(key, ":").concat(fingerprint));
        result.events.push({ key: key, fingerprint: fingerprint, event: event });
    };
    for (var _i = 0, _u = source.messages; _i < _u.length; _i++) {
        var message = _u[_i];
        var info = message.info;
        var messageId = boundedString(info.id, 256);
        var role = (_a = boundedString(info.role, 32)) === null || _a === void 0 ? void 0 : _a.toLowerCase();
        if (!messageId || (role !== 'user' && role !== 'assistant') || (role === 'assistant' && info.summary === true))
            continue;
        var model = modelName(info);
        var time = isRecord(info.time) ? info.time : {};
        var createdAt = toTimestamp((_b = time.created) !== null && _b !== void 0 ? _b : info.timestamp);
        for (var _v = 0, _w = message.parts; _v < _w.length; _v++) {
            var item = _w[_v];
            var partId = boundedString(item.id, 256);
            var type = boundedString(item.type, 64);
            if (!type || !['text', 'reasoning', 'tool-invocation', 'tool', 'compaction'].includes(type))
                continue;
            if (!partId)
                throw new Error('OpenCode export contains a supported part without a stable identifier');
            if (type === 'text' || type === 'reasoning') {
                if (type === 'text' && (item.ignored === true || item.synthetic === true))
                    continue;
                if (typeof item.text === 'string' && item.text.length > (4 << 20))
                    throw new Error('OpenCode text part exceeded the local processing limit');
                var text = boundedString(item.text, 4 << 20);
                if (!text)
                    continue;
                var digest = sha256(text);
                var bytes = Buffer.byteLength(text, 'utf8');
                var included = type === 'text' && includeContent && bytes <= maxRetainedContentBytes;
                var event_1 = createEvent(__assign({ sessionHash: sessionHash, timestamp: partTimestamp(item, createdAt), model: model, action: type === 'reasoning' ? 'agent.reasoning' : role === 'user' ? 'prompt.submitted' : 'agent.message', category: type === 'reasoning' ? 'agent' : role === 'user' ? 'prompt' : 'agent', message: type === 'reasoning' ? 'OpenCode reasoning part observed' : role === 'user' ? 'OpenCode user message recovered from session export' : 'OpenCode assistant message recovered from session export', identityHash: sha256("".concat(messageId, ":").concat(partId)), content: __assign({ retention: included ? 'full' : 'metadata', included: included, hash: digest, bytes: bytes }, (includeContent && type === 'text' && bytes > maxRetainedContentBytes ? { truncated: true } : {})) }, (included ? { text: text } : {})));
                add("".concat(messageId, ":").concat(partId, ":").concat(type), "".concat(type, ":").concat(role, ":").concat(digest, ":").concat(bytes), event_1);
                continue;
            }
            if (type === 'compaction') {
                var event_2 = createEvent({
                    sessionHash: sessionHash,
                    timestamp: partTimestamp(item, createdAt),
                    model: model,
                    action: 'session.compacting', category: 'session',
                    message: 'OpenCode compaction recovered from session export', identityHash: sha256("".concat(messageId, ":").concat(partId)),
                });
                add("".concat(messageId, ":").concat(partId, ":compaction"), 'compaction', event_2);
                continue;
            }
            var tool = isRecord(item.toolInvocation) ? item.toolInvocation : item;
            var toolState = isRecord(tool.state) ? tool.state : {};
            var toolName = safeLabel((_d = (_c = tool.toolName) !== null && _c !== void 0 ? _c : tool.tool) !== null && _d !== void 0 ? _d : item.toolName, 128);
            var callId = (_g = boundedString((_f = (_e = tool.toolCallId) !== null && _e !== void 0 ? _e : tool.callID) !== null && _f !== void 0 ? _f : item.callID, 512)) !== null && _g !== void 0 ? _g : partId;
            var stage = (_j = boundedString((_h = toolState.type) !== null && _h !== void 0 ? _h : toolState.status, 64)) === null || _j === void 0 ? void 0 : _j.toLowerCase();
            var input = (_l = (_k = toolState.input) !== null && _k !== void 0 ? _k : toolState.args) !== null && _l !== void 0 ? _l : toolState.partialCall;
            var output = (_m = toolState.output) !== null && _m !== void 0 ? _m : toolState.result;
            var finished = stage === 'result' || stage === 'completed' || stage === 'error' || stage === 'failed';
            var supportedStage = finished || stage === 'pending' || stage === 'running' || stage === 'call' || stage === 'partial-call' || stage === 'invoked';
            if (!supportedStage)
                throw new Error('OpenCode export contains a tool state this adapter does not yet understand');
            var failed = toolState.isError === true || stage === 'error' || stage === 'failed';
            var commandLike = Boolean(toolName && /^(bash|shell|terminal|powershell|exec)$/i.test(toolName));
            var action = finished
                ? failed ? 'tool.failed' : commandLike ? 'command.executed' : 'tool.completed'
                : 'tool.invoked';
            var category = finished && commandLike ? 'command' : 'tool';
            var inputDigest = digestValue(input);
            var outputDigest = digestValue(output);
            var content = __assign(__assign({ retention: 'metadata', included: false }, (inputDigest ? { input_hash: inputDigest.hash, input_bytes: inputDigest.bytes } : {})), (outputDigest ? { output_hash: outputDigest.hash, output_bytes: outputDigest.bytes } : {}));
            var event_3 = createEvent({
                sessionHash: sessionHash,
                timestamp: toolTimestamp(toolState, partTimestamp(item, createdAt)),
                model: model,
                action: action,
                category: category,
                message: "OpenCode ".concat(finished ? (failed ? 'tool failure' : 'tool result') : 'tool invocation', " recovered from session export"),
                identityHash: sha256("".concat(messageId, ":").concat(partId)), severity: failed ? 'medium' : 'info',
                toolName: toolName !== null && toolName !== void 0 ? toolName : undefined,
                toolCallHash: sha256(callId),
                content: content,
            });
            var phase = finished ? 'result' : 'invoke';
            var fingerprint = "".concat(phase, ":").concat(stage, ":").concat(failed, ":").concat((_o = inputDigest === null || inputDigest === void 0 ? void 0 : inputDigest.hash) !== null && _o !== void 0 ? _o : '', ":").concat((_p = outputDigest === null || outputDigest === void 0 ? void 0 : outputDigest.hash) !== null && _p !== void 0 ? _p : '');
            add("".concat(messageId, ":").concat(partId, ":tool:").concat(phase), fingerprint, event_3);
        }
        if (role === 'assistant') {
            var usage = normalizedUsage(info.tokens, info.cost);
            if (usage) {
                var event_4 = createEvent({
                    sessionHash: sessionHash,
                    timestamp: toTimestamp((_q = time.completed) !== null && _q !== void 0 ? _q : time.created),
                    model: model,
                    action: 'token.usage', category: 'metric',
                    message: 'OpenCode token usage recovered from session export', identityHash: sha256("".concat(messageId, ":usage")),
                    genAi: { usage: usage },
                });
                add("".concat(messageId, ":usage"), JSON.stringify(usage), event_4);
            }
            if (isRecord(info.error)) {
                var errorType = safeLabel((_s = (_r = info.error.name) !== null && _r !== void 0 ? _r : info.error.type) !== null && _s !== void 0 ? _s : info.error.code, 96);
                var event_5 = createEvent(__assign({ sessionHash: sessionHash, timestamp: toTimestamp((_t = time.completed) !== null && _t !== void 0 ? _t : time.created), model: model, action: 'session.error', category: 'session', message: 'OpenCode session error recovered from export', identityHash: sha256("".concat(messageId, ":error")), severity: 'medium' }, (errorType ? { raw: { error_type: errorType } } : {})));
                add("".concat(messageId, ":error"), "error:".concat(errorType !== null && errorType !== void 0 ? errorType : 'unknown'), event_5);
            }
        }
    }
    if (result.events.length > maxEventsPerSync)
        throw new Error('OpenCode session exceeded the event processing limit');
    return result;
}
function createEvent(input) {
    var _a;
    var event = {
        timestamp: input.timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { kind: 'agent_runtime', action: input.action, category: input.category, fidelity: 'observed' },
        severity: (_a = input.severity) !== null && _a !== void 0 ? _a : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'opencode', collection_method: 'poll' },
        session: { id: "opencode:".concat(input.sessionHash) },
        raw: { source: 'opencode_session_export', session_sha256: input.sessionHash, record_sha256: input.identityHash, message: input.message },
    };
    if (input.model)
        event.model = input.model;
    if (input.toolName)
        event.tool = { name: input.toolName };
    if (input.toolCallHash)
        event.raw = __assign(__assign({}, event.raw), { tool_call_sha256: input.toolCallHash });
    if (input.content && Object.keys(input.content).length > 0)
        event.content = input.content;
    if (input.text !== undefined) {
        if (input.action === 'prompt.submitted') {
            event.prompt = { text: input.text };
            event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: input.text }] }] } };
        }
        else {
            event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: input.text }] }] } };
        }
    }
    if (input.genAi)
        event.gen_ai = input.genAi;
    if (input.raw)
        event.raw = __assign(__assign({}, event.raw), input.raw);
    return event;
}
function normalizedUsage(value, costValue) {
    if (!isRecord(value))
        return undefined;
    var cache = isRecord(value.cache) ? value.cache : {};
    var numbers = {};
    var input = nonNegativeInteger(value.input);
    var output = nonNegativeInteger(value.output);
    var reasoning = nonNegativeInteger(value.reasoning);
    var cacheRead = nonNegativeInteger(cache.read);
    var cacheWrite = nonNegativeInteger(cache.write);
    if (input !== undefined)
        numbers.input_tokens = input;
    if (output !== undefined)
        numbers.output_tokens = output;
    if (reasoning !== undefined)
        numbers.reasoning = { output_tokens: reasoning };
    if (cacheRead !== undefined)
        numbers.cache_read = { input_tokens: cacheRead };
    if (cacheWrite !== undefined)
        numbers.cache_creation = { input_tokens: cacheWrite };
    var cost = typeof costValue === 'number' && Number.isFinite(costValue) && costValue >= 0 ? costValue : undefined;
    if (cost !== undefined)
        numbers.cost_usd = cost;
    return Object.keys(numbers).length > 0 ? numbers : undefined;
}
function digestValue(value) {
    if (value === undefined || value === null)
        return undefined;
    var serialized;
    try {
        serialized = typeof value === 'string' ? value : JSON.stringify(value);
    }
    catch (_a) {
        return undefined;
    }
    if (typeof serialized !== 'string')
        return undefined;
    return { hash: sha256(serialized), bytes: Buffer.byteLength(serialized, 'utf8') };
}
function modelName(info) {
    var _a, _b;
    var provider = safeLabel((_a = info.providerID) !== null && _a !== void 0 ? _a : (isRecord(info.model) ? info.model.providerID : undefined), 64);
    var model = safeLabel((_b = info.modelID) !== null && _b !== void 0 ? _b : (isRecord(info.model) ? info.model.id : undefined), 96);
    return provider && model ? "".concat(provider, "/").concat(model) : undefined;
}
function safeLabel(value, max) {
    var text = boundedString(value, max);
    if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text))
        return undefined;
    return text;
}
function partTimestamp(part, fallback) {
    var _a, _b, _c;
    var time = isRecord(part.time) ? part.time : {};
    return toTimestamp((_c = (_b = (_a = time.created) !== null && _a !== void 0 ? _a : time.start) !== null && _b !== void 0 ? _b : time.end) !== null && _c !== void 0 ? _c : fallback);
}
function toolTimestamp(state, fallback) {
    var _a, _b;
    var time = isRecord(state.time) ? state.time : {};
    return toTimestamp((_b = (_a = time.end) !== null && _a !== void 0 ? _a : time.start) !== null && _b !== void 0 ? _b : fallback);
}
function toTimestamp(value) {
    var date = typeof value === 'number' ? new Date(value) : typeof value === 'string' ? new Date(value) : new Date();
    return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}
function nonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function parseJson(text, label) {
    try {
        return JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(text, 'utf8')));
    }
    catch (_a) {
        throw new Error("".concat(label, " returned invalid JSON"));
    }
}
function resolveStatePath(value) {
    var _a;
    if (value !== undefined)
        return absolutePath(value, '--state-path');
    var explicitRoot = (_a = process.env.THREADFORGE_HOME) === null || _a === void 0 ? void 0 : _a.trim();
    var root = explicitRoot;
    if (!root) {
        if (process.platform === 'win32' && process.env.LOCALAPPDATA)
            root = node_path_1.default.join(process.env.LOCALAPPDATA, 'threadforge');
        else if (process.platform === 'darwin')
            root = node_path_1.default.join(node_os_1.default.homedir(), 'Library', 'Caches', 'threadforge');
        else
            root = node_path_1.default.join(process.env.XDG_CACHE_HOME || node_path_1.default.join(node_os_1.default.homedir(), '.cache'), 'threadforge');
    }
    if (!node_path_1.default.isAbsolute(root))
        throw new Error('THREADFORGE_HOME/cache root must be an absolute path');
    return node_path_1.default.join(root, 'state', stateFileName);
}
function withStateLock(statePath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var parent, info, lock, error_1;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    parent = node_path_1.default.dirname(statePath);
                    return [4 /*yield*/, verifyStateParent(statePath, true)];
                case 1:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(parent).catch(function () { return null; })];
                case 2:
                    info = _a.sent();
                    if (!info || !info.isDirectory() || info.isSymbolicLink())
                        throw new Error('OpenCode cursor parent must be a real directory');
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.open)("".concat(statePath, ".lock"), node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 4:
                    lock = _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'EEXIST'))
                        throw new Error('OpenCode history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock OpenCode history cursor');
                case 6:
                    _a.trys.push([6, , 8, 11]);
                    return [4 /*yield*/, action()];
                case 7: return [2 /*return*/, _a.sent()];
                case 8: return [4 /*yield*/, lock.close().catch(function () { })];
                case 9:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)("".concat(statePath, ".lock")).catch(function () { })];
                case 10:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 11: return [2 /*return*/];
            }
        });
    });
}
function readState(statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var empty, info, handle, opened, bytes, parsed, sessions, _i, _a, _b, sessionHash, value, events, _c, _d, _e, key, fingerprint, error_2;
        var _f;
        return __generator(this, function (_g) {
            switch (_g.label) {
                case 0:
                    empty = { schema_version: 'threadforge.opencode-history-cursor.v1', version: stateVersion, sessions: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _g.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect OpenCode history cursor');
                        })];
                case 2:
                    info = _g.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('OpenCode history cursor must be a bounded regular file');
                    _g.label = 3;
                case 3:
                    _g.trys.push([3, 7, 8, 10]);
                    return [4 /*yield*/, (0, promises_1.open)(statePath, node_fs_1.constants.O_RDONLY | ((_f = node_fs_1.constants.O_NOFOLLOW) !== null && _f !== void 0 ? _f : 0))];
                case 4:
                    handle = _g.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _g.sent();
                    if (opened.dev !== info.dev || opened.ino !== info.ino)
                        throw new Error('OpenCode cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _g.sent();
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
                        throw new Error('OpenCode history cursor has an unsupported schema; inspect it before retrying');
                    }
                    sessions = {};
                    for (_i = 0, _a = Object.entries(parsed.sessions); _i < _a.length; _i++) {
                        _b = _a[_i], sessionHash = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(sessionHash) || !isRecord(value) || !isRecord(value.events))
                            throw new Error('OpenCode cursor has an invalid session entry');
                        events = {};
                        for (_c = 0, _d = Object.entries(value.events); _c < _d.length; _c++) {
                            _e = _d[_c], key = _e[0], fingerprint = _e[1];
                            if (!/^[0-9a-f]{64}$/.test(key) || typeof fingerprint !== 'string' || !/^[0-9a-f]{64}$/.test(fingerprint)) {
                                throw new Error('OpenCode cursor contains an invalid event checkpoint');
                            }
                            events[key] = fingerprint;
                        }
                        sessions[sessionHash] = { events: events };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { sessions: sessions })];
                case 7:
                    error_2 = _g.sent();
                    if (error_2 instanceof Error && error_2.message.startsWith('OpenCode history cursor'))
                        throw error_2;
                    throw new Error('OpenCode history cursor is malformed or unreadable');
                case 8: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 9:
                    _g.sent();
                    return [7 /*endfinally*/];
                case 10: return [2 /*return*/];
            }
        });
    });
}
function verifyStateParent(statePath, create) {
    return __awaiter(this, void 0, void 0, function () {
        var absoluteParent, parsed, segments, current, _i, segments_1, segment, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    absoluteParent = node_path_1.default.dirname(node_path_1.default.resolve(statePath));
                    parsed = node_path_1.default.parse(absoluteParent);
                    segments = absoluteParent.slice(parsed.root.length).split(node_path_1.default.sep).filter(Boolean);
                    current = parsed.root;
                    _i = 0, segments_1 = segments;
                    _a.label = 1;
                case 1:
                    if (!(_i < segments_1.length)) return [3 /*break*/, 7];
                    segment = segments_1[_i];
                    current = node_path_1.default.join(current, segment);
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect OpenCode cursor directory');
                        })];
                case 2:
                    info = _a.sent();
                    if (!(!info && create)) return [3 /*break*/, 5];
                    return [4 /*yield*/, (0, promises_1.mkdir)(current, { mode: 448 }).catch(function (error) {
                            if (!isCode(error, 'EEXIST'))
                                throw new Error('unable to create private OpenCode cursor directory');
                        })];
                case 3:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { return null; })];
                case 4:
                    info = _a.sent();
                    _a.label = 5;
                case 5:
                    if (!info)
                        return [2 /*return*/];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('OpenCode cursor path cannot contain symbolic links or non-directory parents');
                    _a.label = 6;
                case 6:
                    _i++;
                    return [3 /*break*/, 1];
                case 7: return [2 /*return*/];
            }
        });
    });
}
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function writeState(statePath, state) {
    return __awaiter(this, void 0, void 0, function () {
        var data, tempPath, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    data = Buffer.from("".concat(JSON.stringify(state, null, 2), "\n"), 'utf8');
                    if (data.byteLength > maxStateBytes)
                        throw new Error('OpenCode history cursor exceeds the local size limit');
                    tempPath = "".concat(statePath, ".").concat((0, node_crypto_1.randomBytes)(12).toString('hex'), ".tmp");
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 8, , 11]);
                    return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.chmod(384)];
                case 3:
                    _b.sent();
                    return [4 /*yield*/, handle.writeFile(data)];
                case 4:
                    _b.sent();
                    return [4 /*yield*/, handle.sync()];
                case 5:
                    _b.sent();
                    return [4 /*yield*/, handle.close()];
                case 6:
                    _b.sent();
                    handle = undefined;
                    return [4 /*yield*/, (0, promises_1.rename)(tempPath, statePath)];
                case 7:
                    _b.sent();
                    return [3 /*break*/, 11];
                case 8:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 9:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(tempPath).catch(function () { })];
                case 10:
                    _b.sent();
                    throw new Error('unable to atomically write OpenCode history cursor');
                case 11: return [2 /*return*/];
            }
        });
    });
}
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function boundedString(value, max) { return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined; }
function isBoundedString(value, max) { return boundedString(value, max) !== undefined; }
function validSessionId(value) {
    return typeof value === 'string' && value.length <= 256 && /^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(value);
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function normalizedOS(platform) {
    if (platform === 'win32')
        return 'windows';
    if (platform === 'darwin')
        return 'macos';
    if (platform === 'linux')
        return 'linux';
    return 'other';
}
function runCommand(command) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            if (process.platform === 'win32')
                return [2 /*return*/, runPowerShell(command)];
            return [2 /*return*/, spawnCaptured(command.executable, command.args, command.cwd)];
        });
    });
}
function runPowerShell(command) {
    return __awaiter(this, void 0, void 0, function () {
        var quote, invocation, script;
        return __generator(this, function (_a) {
            quote = function (value) {
                if (/[\0\r\n]/.test(value))
                    throw new Error('OpenCode CLI argument contains invalid characters');
                return "'".concat(value.replace(/'/g, "''"), "'");
            };
            invocation = __spreadArray([command.executable], command.args, true).map(quote).join(' ');
            script = "$ErrorActionPreference = 'Stop'\nSet-Location -LiteralPath ".concat(quote(command.cwd), "\n& ").concat(invocation, "\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit");
            return [2 /*return*/, spawnCaptured('powershell.exe', ['-NoProfile', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')], command.cwd)];
        });
    });
}
function spawnCaptured(executable, args, cwd) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            return [2 /*return*/, new Promise(function (resolve, reject) {
                    var child = (0, node_child_process_1.spawn)(executable, args, { cwd: cwd, stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true });
                    var chunks = [];
                    var size = 0;
                    var settled = false;
                    var timeout = setTimeout(function () {
                        child.kill();
                        finish(new Error('OpenCode CLI timed out'));
                    }, cliTimeoutMs);
                    var finish = function (error, output) {
                        if (settled)
                            return;
                        settled = true;
                        clearTimeout(timeout);
                        if (error)
                            reject(error);
                        else
                            resolve(output !== null && output !== void 0 ? output : '');
                    };
                    child.stdout.on('data', function (chunk) {
                        size += chunk.byteLength;
                        if (size > maxCliOutputBytes) {
                            child.kill();
                            finish(new Error('OpenCode CLI output exceeded the local limit'));
                        }
                        else
                            chunks.push(Buffer.from(chunk));
                    });
                    child.once('error', function () { return finish(new Error('OpenCode CLI could not be started')); });
                    child.once('close', function (code) {
                        if (code !== 0)
                            finish(new Error("OpenCode CLI exited with status ".concat(code)));
                        else
                            finish(undefined, Buffer.concat(chunks).toString('utf8'));
                    });
                })];
        });
    });
}
