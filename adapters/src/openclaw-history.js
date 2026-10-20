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
exports.buildOpenClawSessionListCommand = buildOpenClawSessionListCommand;
exports.buildOpenClawHistoryCommand = buildOpenClawHistoryCommand;
exports.getOpenClawHistoryStatus = getOpenClawHistoryStatus;
exports.syncOpenClawHistory = syncOpenClawHistory;
var node_child_process_1 = require("node:child_process");
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'openclaw-history.v1.json';
var maxStateBytes = 4 << 20;
var maxCliOutputBytes = 16 << 20;
var maxHistoryResponseBytes = 32 << 20;
var maxSessions = 200;
var gatewayPageSize = 200;
var maxHistoryMessages = 2000;
var maxEvents = 20000;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
var maxDigestBytes = 1 << 20;
var cliTimeoutMs = 30000;
function buildOpenClawSessionListCommand(options) {
    var _a;
    if (options === void 0) { options = {}; }
    var executable = validateExecutable(options.openclawCli);
    var limit = boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 50, 1, maxSessions, '--max-sessions');
    return { executable: executable, args: ['sessions', '--all-agents', '--limit', String(limit), '--json'] };
}
function buildOpenClawHistoryCommand(sessionKey, options) {
    var _a;
    if (options === void 0) { options = {}; }
    var executable = validateExecutable(options.openclawCli);
    if (!isBoundedString(sessionKey, 2048))
        throw new Error('OpenClaw session key is invalid');
    var limit = boundedInteger((_a = options.maxMessages) !== null && _a !== void 0 ? _a : gatewayPageSize, 1, gatewayPageSize, '--max-messages');
    if (options.offset !== undefined)
        boundedInteger(options.offset, 0, Number.MAX_SAFE_INTEGER, 'history offset');
    var agentId = options.agentId === undefined ? undefined : boundedString(options.agentId, 128);
    if (options.agentId !== undefined && !agentId)
        throw new Error('OpenClaw agent id is invalid');
    return {
        executable: executable,
        args: ['gateway', 'call', 'chat.history', '--params', JSON.stringify(__assign(__assign({ sessionKey: sessionKey, limit: limit }, (options.offset === undefined ? {} : { offset: options.offset })), (agentId ? { agentId: agentId } : {}))), '--json'],
    };
}
function getOpenClawHistoryStatus() {
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
                        schema_version: 'threadforge.openclaw-history-status.v1',
                        tracked_sessions: Object.keys(state.sessions).length
                    };
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).then(function () { return true; }, function (error) {
                            if (isCode(error, 'ENOENT'))
                                return false;
                            throw new Error('unable to inspect OpenClaw history state');
                        })];
                case 2: return [2 /*return*/, (_a.cursor_file_present = _b.sent(),
                        _a)];
            }
        });
    });
}
function syncOpenClawHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var result, runner, statePath, maxSessionCount, maxMessageCount, operation, _a;
        var _this = this;
        var _b, _c, _d, _e;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_f) {
            switch (_f.label) {
                case 0:
                    validateOptions(options);
                    result = emptyResult((_b = options.includeContent) !== null && _b !== void 0 ? _b : false);
                    runner = (_c = options.commandRunner) !== null && _c !== void 0 ? _c : runCommand;
                    statePath = resolveStatePath(options.statePath);
                    maxSessionCount = (_d = options.maxSessions) !== null && _d !== void 0 ? _d : 50;
                    maxMessageCount = (_e = options.maxMessages) !== null && _e !== void 0 ? _e : maxHistoryMessages;
                    operation = function (state, persist) { return __awaiter(_this, void 0, void 0, function () {
                        var listingText, listing, _loop_1, _i, _a, row;
                        var _b, _c;
                        return __generator(this, function (_d) {
                            switch (_d.label) {
                                case 0: return [4 /*yield*/, runner(buildOpenClawSessionListCommand({ openclawCli: options.openclawCli, maxSessions: maxSessionCount }))];
                                case 1:
                                    listingText = _d.sent();
                                    listing = parseSessionListing(listingText);
                                    result.sessions = listing.sessions.length;
                                    result.sessions_truncated = listing.hasMore !== false;
                                    _loop_1 = function (row) {
                                        var key, sessionHash, previous, history_1, _e, anchorIndex, startIndex, fresh, mapped, _f, mapped_1, item, acknowledgedIdentity, offset, batch, _g, newestIdentity;
                                        return __generator(this, function (_h) {
                                            switch (_h.label) {
                                                case 0:
                                                    key = boundedString(row.key, 2048);
                                                    if (!key) {
                                                        result.errors.push({ message: 'OpenClaw returned a session row without a valid key' });
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    sessionHash = sha256(key);
                                                    previous = state.sessions[sessionHash];
                                                    history_1 = void 0;
                                                    _h.label = 1;
                                                case 1:
                                                    _h.trys.push([1, 3, , 4]);
                                                    return [4 /*yield*/, readBoundedHistory(runner, key, boundedString(row.agentId, 128), options.openclawCli, maxMessageCount)];
                                                case 2:
                                                    history_1 = _h.sent();
                                                    return [3 /*break*/, 4];
                                                case 3:
                                                    _e = _h.sent();
                                                    result.errors.push({ session_sha256: sessionHash, message: 'OpenClaw session history could not be read or validated' });
                                                    return [2 /*return*/, "continue"];
                                                case 4:
                                                    result.messages_seen += history_1.messages.length;
                                                    if (history_1.hasMore)
                                                        result.history_truncated_sessions++;
                                                    if (history_1.messages.length === 0) {
                                                        if (previous) {
                                                            result.cursor_conflicts++;
                                                            result.errors.push({ session_sha256: sessionHash, message: 'history cursor is no longer present; review session reset or retention changes' });
                                                        }
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    anchorIndex = previous
                                                        ? history_1.messages.findIndex(function (message) { return message.identity === previous.latest_message_sha256; })
                                                        : -1;
                                                    if (previous && anchorIndex < 0) {
                                                        result.cursor_conflicts++;
                                                        result.errors.push({ session_sha256: sessionHash, message: 'history cursor is no longer present; refusing to skip or replay messages' });
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    startIndex = previous ? anchorIndex + 1 : 0;
                                                    fresh = history_1.messages.slice(startIndex);
                                                    mapped = fresh.flatMap(function (message) {
                                                        var _a;
                                                        var event = mapMessage(message, sessionHash, (_a = options.includeContent) !== null && _a !== void 0 ? _a : false);
                                                        return event ? [{ identity: message.identity, event: event }] : [];
                                                    });
                                                    if (mapped.length + result.events_emitted + result.events_previewed > maxEvents) {
                                                        result.errors.push({ session_sha256: sessionHash, message: 'OpenClaw history sync exceeded the event processing limit' });
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    if (options.preview) {
                                                        result.events_previewed += mapped.length;
                                                        (_b = result.events) !== null && _b !== void 0 ? _b : (result.events = []);
                                                        for (_f = 0, mapped_1 = mapped; _f < mapped_1.length; _f++) {
                                                            item = mapped_1[_f];
                                                            if (result.events.length < maxPreviewEvents)
                                                                result.events.push(item.event);
                                                            else
                                                                result.preview_truncated = true;
                                                        }
                                                        return [2 /*return*/, "continue"];
                                                    }
                                                    acknowledgedIdentity = previous === null || previous === void 0 ? void 0 : previous.latest_message_sha256;
                                                    offset = 0;
                                                    _h.label = 5;
                                                case 5:
                                                    if (!(offset < mapped.length)) return [3 /*break*/, 12];
                                                    batch = mapped.slice(offset, offset + batchSize);
                                                    _h.label = 6;
                                                case 6:
                                                    _h.trys.push([6, 8, , 9]);
                                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(batch.map(function (item) { return item.event; }), { endpoint: (_c = options.endpoint) !== null && _c !== void 0 ? _c : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                                case 7:
                                                    _h.sent();
                                                    return [3 /*break*/, 9];
                                                case 8:
                                                    _g = _h.sent();
                                                    result.errors.push({ session_sha256: sessionHash, message: 'Threadforge loopback intake rejected OpenClaw history events' });
                                                    return [3 /*break*/, 12];
                                                case 9:
                                                    acknowledgedIdentity = batch[batch.length - 1].identity;
                                                    state.sessions[sessionHash] = { latest_message_sha256: acknowledgedIdentity };
                                                    result.events_emitted += batch.length;
                                                    if (!persist) return [3 /*break*/, 11];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 10:
                                                    _h.sent();
                                                    _h.label = 11;
                                                case 11:
                                                    offset += batchSize;
                                                    return [3 /*break*/, 5];
                                                case 12:
                                                    if (result.errors.some(function (error) { return error.session_sha256 === sessionHash && error.message.startsWith('Threadforge'); }))
                                                        return [2 /*return*/, "continue"];
                                                    newestIdentity = history_1.messages[history_1.messages.length - 1].identity;
                                                    if (!(acknowledgedIdentity !== newestIdentity || !previous)) return [3 /*break*/, 14];
                                                    state.sessions[sessionHash] = { latest_message_sha256: newestIdentity };
                                                    if (!persist) return [3 /*break*/, 14];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 13:
                                                    _h.sent();
                                                    _h.label = 14;
                                                case 14:
                                                    if (newestIdentity !== (previous === null || previous === void 0 ? void 0 : previous.latest_message_sha256))
                                                        result.sessions_changed++;
                                                    return [2 /*return*/];
                                            }
                                        });
                                    };
                                    _i = 0, _a = listing.sessions;
                                    _d.label = 2;
                                case 2:
                                    if (!(_i < _a.length)) return [3 /*break*/, 5];
                                    row = _a[_i];
                                    return [5 /*yield**/, _loop_1(row)];
                                case 3:
                                    _d.sent();
                                    _d.label = 4;
                                case 4:
                                    _i++;
                                    return [3 /*break*/, 2];
                                case 5:
                                    if (!persist) return [3 /*break*/, 7];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 6:
                                    _d.sent();
                                    _d.label = 7;
                                case 7: return [2 /*return*/];
                            }
                        });
                    }); };
                    if (!options.preview) return [3 /*break*/, 3];
                    _a = operation;
                    return [4 /*yield*/, readState(statePath)];
                case 1: return [4 /*yield*/, _a.apply(void 0, [_f.sent(), false])];
                case 2:
                    _f.sent();
                    return [3 /*break*/, 5];
                case 3: return [4 /*yield*/, withStateLock(statePath, function () { return __awaiter(_this, void 0, void 0, function () { var _a; return __generator(this, function (_b) {
                        switch (_b.label) {
                            case 0:
                                _a = operation;
                                return [4 /*yield*/, readState(statePath)];
                            case 1: return [2 /*return*/, _a.apply(void 0, [_b.sent(), true])];
                        }
                    }); }); })];
                case 4:
                    _f.sent();
                    _f.label = 5;
                case 5:
                    result.complete = result.errors.length === 0 && !result.sessions_truncated && result.history_truncated_sessions === 0;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.openclaw-history-sync.v1', sessions: 0, sessions_changed: 0,
        messages_seen: 0, events_emitted: 0, events_previewed: 0, sessions_truncated: false,
        history_truncated_sessions: 0, cursor_conflicts: 0,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
        preview_truncated: false, errors: [],
    };
}
function validateOptions(options) {
    var _a, _b;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 50, 1, maxSessions, '--max-sessions');
    boundedInteger((_b = options.maxMessages) !== null && _b !== void 0 ? _b : maxHistoryMessages, 1, maxHistoryMessages, '--max-messages');
    validateExecutable(options.openclawCli);
}
function validateExecutable(value) {
    var executable = (value === null || value === void 0 ? void 0 : value.trim()) || 'openclaw';
    if (/[\0\r\n]/.test(executable))
        throw new Error('OpenClaw CLI path contains invalid characters');
    return executable;
}
function boundedInteger(value, min, max, label) {
    if (!Number.isSafeInteger(value) || value < min || value > max)
        throw new Error("".concat(label, " must be an integer between ").concat(min, " and ").concat(max));
    return value;
}
function parseSessionListing(text) {
    var parsed = parseJsonOutput(text, 'OpenClaw sessions listing');
    if (!isRecord(parsed) || !Array.isArray(parsed.sessions))
        throw new Error('OpenClaw sessions listing has an unsupported shape');
    if (parsed.sessions.length > maxSessions)
        throw new Error('OpenClaw sessions listing exceeded the local processing limit');
    var sessions = [];
    for (var _i = 0, _a = parsed.sessions; _i < _a.length; _i++) {
        var row = _a[_i];
        if (!isRecord(row))
            throw new Error('OpenClaw sessions listing contains an invalid row');
        sessions.push(row);
    }
    return { sessions: sessions, hasMore: typeof parsed.hasMore === 'boolean' ? parsed.hasMore : undefined };
}
function readBoundedHistory(runner, sessionKey, agentId, openclawCli, maxMessages) {
    return __awaiter(this, void 0, void 0, function () {
        var pages, maxPages, rawMessages, responseBytes, offset, hasMore, pageIndex, limit, responseText, page, unique, _i, _a, page, _b, page_1, message;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    pages = [];
                    maxPages = Math.ceil(maxMessages / gatewayPageSize);
                    rawMessages = 0;
                    responseBytes = 0;
                    hasMore = false;
                    pageIndex = 0;
                    _c.label = 1;
                case 1:
                    if (!(pageIndex < maxPages)) return [3 /*break*/, 4];
                    limit = Math.min(gatewayPageSize, maxMessages - rawMessages);
                    if (limit < 1)
                        return [3 /*break*/, 4];
                    return [4 /*yield*/, runner(buildOpenClawHistoryCommand(sessionKey, __assign(__assign({ openclawCli: openclawCli, maxMessages: limit }, (agentId ? { agentId: agentId } : {})), (offset === undefined ? {} : { offset: offset }))))];
                case 2:
                    responseText = _c.sent();
                    responseBytes += Buffer.byteLength(responseText, 'utf8');
                    if (responseBytes > maxHistoryResponseBytes)
                        throw new Error('OpenClaw history exceeded the per-session response budget');
                    page = parseHistory(responseText, limit);
                    pages.push(page.messages);
                    rawMessages += page.rawCount;
                    hasMore = page.hasMore;
                    if (!hasMore)
                        return [3 /*break*/, 4];
                    if (page.nextOffset === undefined || page.nextOffset === offset)
                        return [3 /*break*/, 4];
                    offset = page.nextOffset;
                    if (pageIndex === maxPages - 1 || rawMessages >= maxMessages)
                        return [3 /*break*/, 4];
                    _c.label = 3;
                case 3:
                    pageIndex++;
                    return [3 /*break*/, 1];
                case 4:
                    unique = new Map();
                    for (_i = 0, _a = pages.reverse(); _i < _a.length; _i++) {
                        page = _a[_i];
                        for (_b = 0, page_1 = page; _b < page_1.length; _b++) {
                            message = page_1[_b];
                            unique.set(message.identity, message);
                        }
                    }
                    return [2 /*return*/, { messages: __spreadArray([], unique.values(), true), hasMore: hasMore }];
            }
        });
    });
}
function parseHistory(text, maxMessages) {
    var _a, _b, _c, _d, _e;
    var parsed = parseJsonOutput(text, 'OpenClaw chat history');
    if (!isRecord(parsed) || !Array.isArray(parsed.messages))
        throw new Error('OpenClaw chat history has an unsupported shape');
    if (parsed.messages.length > maxMessages)
        throw new Error('OpenClaw chat history exceeded the requested message limit');
    var messages = [];
    for (var _i = 0, _f = parsed.messages; _i < _f.length; _i++) {
        var row = _f[_i];
        if (!isRecord(row))
            continue;
        var value = isRecord(row.message) ? row.message : row;
        var role = (_a = boundedString(value.role, 32)) === null || _a === void 0 ? void 0 : _a.toLowerCase();
        if (role !== 'user' && role !== 'assistant')
            continue;
        var textValue = visibleText(value.content);
        if (!textValue.trim())
            continue;
        var rawTimestamp = (_c = (_b = value.timestamp) !== null && _b !== void 0 ? _b : value.createdAt) !== null && _c !== void 0 ? _c : row.timestamp;
        var time = timestamp(rawTimestamp);
        var rawId = (_e = (_d = boundedString(value.id, 512)) !== null && _d !== void 0 ? _d : boundedString(value.messageId, 512)) !== null && _e !== void 0 ? _e : boundedString(row.id, 512);
        if (!rawId)
            throw new Error('OpenClaw history contains a visible message without a stable identifier');
        var contentHash = sha256(textValue.slice(0, maxDigestBytes));
        var identity = sha256("id:".concat(rawId));
        messages.push({ identity: identity, timestamp: time, role: role, text: textValue });
    }
    var unique = new Map();
    for (var _g = 0, messages_1 = messages; _g < messages_1.length; _g++) {
        var message = messages_1[_g];
        unique.set(message.identity, message);
    }
    var nextOffset = Number.isSafeInteger(parsed.nextOffset) && parsed.nextOffset >= 0 ? parsed.nextOffset : undefined;
    return __assign({ messages: __spreadArray([], unique.values(), true), rawCount: parsed.messages.length, hasMore: parsed.hasMore === true }, (nextOffset === undefined ? {} : { nextOffset: nextOffset }));
}
function mapMessage(message, sessionHash, includeContent) {
    var utf8 = Buffer.from(message.text, 'utf8');
    var digestText = utf8.subarray(0, maxDigestBytes);
    var retained = includeContent ? utf8.subarray(0, maxContentBytes).toString('utf8') : undefined;
    var truncated = utf8.byteLength > maxContentBytes;
    var content = __assign(__assign({ retention: includeContent && !truncated ? 'full' : 'metadata', included: includeContent && !truncated, hash: sha256(digestText), bytes: utf8.byteLength }, (utf8.byteLength > maxDigestBytes ? { digest_truncated: true } : {})), (includeContent && truncated ? { truncated: true } : {}));
    var user = message.role === 'user';
    var extra = { content: content };
    if (retained && !truncated) {
        if (user) {
            extra.prompt = { text: retained };
            extra.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: retained }] }] } };
        }
        else {
            extra.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: retained }] }] } };
        }
    }
    return __assign({ timestamp: message.timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0', event: {
            id: sha256("".concat(sessionHash, ":").concat(message.identity)),
            kind: 'agent_runtime',
            action: user ? 'prompt.submitted' : 'agent.message',
            category: user ? 'prompt' : 'agent',
            fidelity: 'observed',
        }, severity: 'info', endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'openclaw-gateway', collection_method: 'poll' }, session: { id: "openclaw:".concat(sessionHash) }, raw: { source: 'openclaw_chat_history', source_sha256: sessionHash, message_sha256: message.identity } }, extra);
}
function visibleText(value) {
    if (typeof value === 'string')
        return value;
    if (!Array.isArray(value))
        return '';
    var chunks = [];
    for (var _i = 0, value_1 = value; _i < value_1.length; _i++) {
        var item = value_1[_i];
        if (!isRecord(item) || item.type !== 'text' || typeof item.text !== 'string')
            continue;
        chunks.push(item.text);
    }
    return chunks.join('\n');
}
function timestamp(value) {
    var date = typeof value === 'number' ? new Date(value) : typeof value === 'string' ? new Date(value) : new Date();
    return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
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
function parseJsonOutput(text, label) {
    if (Buffer.byteLength(text, 'utf8') > maxCliOutputBytes)
        throw new Error("".concat(label, " exceeded the local output limit"));
    try {
        return JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(Buffer.from(text, 'utf8')));
    }
    catch (_a) {
        throw new Error("".concat(label, " returned invalid JSON"));
    }
}
function resolveStatePath(value) {
    var _a;
    if (value !== undefined) {
        if (!node_path_1.default.isAbsolute(value))
            throw new Error('--state-path must be an absolute path');
        return node_path_1.default.resolve(value);
    }
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
                    return [4 /*yield*/, (0, promises_1.mkdir)(parent, { recursive: true, mode: 448 }).catch(function () { throw new Error('unable to create private OpenClaw cursor directory'); })];
                case 1:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(parent).catch(function () { return null; })];
                case 2:
                    info = _a.sent();
                    if (!info || !info.isDirectory() || info.isSymbolicLink())
                        throw new Error('OpenClaw cursor parent must be a real directory');
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
                        throw new Error('OpenClaw history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock OpenClaw history cursor');
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
        var empty, info, handle, opened, bytes, parsed, sessions, _i, _a, _b, key, value, error_2;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.openclaw-history-cursor.v1', version: stateVersion, sessions: {} };
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect OpenClaw history cursor');
                        })];
                case 1:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('OpenClaw history cursor must be a bounded regular file');
                    _d.label = 2;
                case 2:
                    _d.trys.push([2, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.open)(statePath, node_fs_1.constants.O_RDONLY | ((_c = node_fs_1.constants.O_NOFOLLOW) !== null && _c !== void 0 ? _c : 0))];
                case 3:
                    handle = _d.sent();
                    return [4 /*yield*/, handle.stat()];
                case 4:
                    opened = _d.sent();
                    if (opened.dev !== info.dev || opened.ino !== info.ino)
                        throw new Error('OpenClaw cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 5:
                    bytes = _d.sent();
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.sessions)) {
                        throw new Error('OpenClaw history cursor has an unsupported schema; inspect it before retrying');
                    }
                    sessions = {};
                    for (_i = 0, _a = Object.entries(parsed.sessions); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || typeof value.latest_message_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.latest_message_sha256)) {
                            throw new Error('OpenClaw history cursor contains an invalid checkpoint');
                        }
                        sessions[key] = { latest_message_sha256: value.latest_message_sha256 };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { sessions: sessions })];
                case 6:
                    error_2 = _d.sent();
                    if (error_2 instanceof Error && error_2.message.startsWith('OpenClaw history cursor'))
                        throw error_2;
                    throw new Error('OpenClaw history cursor is malformed or unreadable');
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 8:
                    _d.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function writeState(statePath, state) {
    return __awaiter(this, void 0, void 0, function () {
        var data, tempPath, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    data = Buffer.from("".concat(JSON.stringify(state, null, 2), "\n"), 'utf8');
                    if (data.byteLength > maxStateBytes)
                        throw new Error('OpenClaw history cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write OpenClaw history cursor');
                case 11: return [2 /*return*/];
            }
        });
    });
}
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function boundedString(value, max) { return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined; }
function isBoundedString(value, max) { return boundedString(value, max) !== undefined; }
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function runCommand(command) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            if (process.platform === 'win32')
                return [2 /*return*/, runPowerShell(command)];
            return [2 /*return*/, spawnCaptured(command.executable, command.args)];
        });
    });
}
function runPowerShell(command) {
    return __awaiter(this, void 0, void 0, function () {
        var quote, invocation, script;
        return __generator(this, function (_a) {
            quote = function (value) {
                if (/[\0\r\n]/.test(value))
                    throw new Error('OpenClaw CLI argument contains invalid characters');
                return "'".concat(value.replace(/'/g, "''"), "'");
            };
            invocation = __spreadArray([command.executable], command.args, true).map(quote).join(' ');
            script = "$ErrorActionPreference = 'Stop'\n& ".concat(invocation, "\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit");
            return [2 /*return*/, spawnCaptured('powershell.exe', ['-NoProfile', '-EncodedCommand', Buffer.from(script, 'utf16le').toString('base64')])];
        });
    });
}
function spawnCaptured(executable, args) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            return [2 /*return*/, new Promise(function (resolve, reject) {
                    var child = (0, node_child_process_1.spawn)(executable, args, { stdio: ['ignore', 'pipe', 'ignore'], windowsHide: true });
                    var chunks = [];
                    var size = 0;
                    var settled = false;
                    var timeout = setTimeout(function () {
                        child.kill();
                        finish(new Error('OpenClaw CLI timed out'));
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
                            finish(new Error('OpenClaw CLI output exceeded the local limit'));
                        }
                        else
                            chunks.push(Buffer.from(chunk));
                    });
                    child.once('error', function () { return finish(new Error('OpenClaw CLI could not be started')); });
                    child.once('close', function (code) {
                        if (code !== 0)
                            finish(new Error("OpenClaw CLI exited with status ".concat(code)));
                        else
                            finish(undefined, Buffer.concat(chunks).toString('utf8'));
                    });
                })];
        });
    });
}
