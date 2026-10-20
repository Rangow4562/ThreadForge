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
Object.defineProperty(exports, "__esModule", { value: true });
exports.getCopilotHistoryStatus = getCopilotHistoryStatus;
exports.syncCopilotHistory = syncCopilotHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'copilot-sessions.v1.json';
var maxStateBytes = 8 << 20;
var maxSessions = 5000;
var maxFileBytes = 64 << 20;
var maxTotalBytes = 512 << 20;
var maxLineBytes = 8 << 20;
var maxLinesPerFile = 100000;
var maxEventsPerFile = 10000;
var maxEventsPerSync = 20000;
var maxToolCorrelations = 10000;
var maxModels = 128;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
function getCopilotHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, copilotDir, listed, state, checkpointed;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    maximum = boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 1000, 1, maxSessions, '--max-sessions');
                    return [4 /*yield*/, resolveCopilotDir(options.copilotDir)];
                case 1:
                    copilotDir = _b.sent();
                    return [4 /*yield*/, listSessionFiles(copilotDir, maximum)];
                case 2:
                    listed = _b.sent();
                    return [4 /*yield*/, readState(resolveStatePath(options.statePath))];
                case 3:
                    state = _b.sent();
                    checkpointed = listed.files.filter(function (file) {
                        var cursor = state.files[sha256(file.key)];
                        return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
                    }).length;
                    return [2 /*return*/, {
                            schema_version: 'threadforge.copilot-history-status.v1', sessions: listed.files.length,
                            checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
                            sessions_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncCopilotHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var copilotDir, statePath, listed, result, _a, _b;
        var _this = this;
        var _c, _d;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveCopilotDir(options.copilotDir)];
                case 1:
                    copilotDir = _e.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(copilotDir, statePath))
                        throw new Error('Copilot history cursor must be stored outside the Copilot directory');
                    return [4 /*yield*/, listSessionFiles(copilotDir, (_c = options.maxSessions) !== null && _c !== void 0 ? _c : 1000)];
                case 2:
                    listed = _e.sent();
                    result = emptyResult((_d = options.includeContent) !== null && _d !== void 0 ? _d : false);
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.sessions_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    _a = processFiles;
                    _b = [listed.files, copilotDir];
                    return [4 /*yield*/, readState(statePath)];
                case 3: return [4 /*yield*/, _a.apply(void 0, _b.concat([_e.sent(), options, result, false]))];
                case 4:
                    _e.sent();
                    return [3 /*break*/, 7];
                case 5: return [4 /*yield*/, withStateLock(statePath, function () { return __awaiter(_this, void 0, void 0, function () {
                        var state;
                        return __generator(this, function (_a) {
                            switch (_a.label) {
                                case 0: return [4 /*yield*/, readState(statePath)];
                                case 1:
                                    state = _a.sent();
                                    return [4 /*yield*/, processFiles(listed.files, copilotDir, state, options, result, true, statePath)];
                                case 2:
                                    _a.sent();
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 3:
                                    _a.sent();
                                    return [2 /*return*/];
                            }
                        });
                    }); })];
                case 6:
                    _e.sent();
                    _e.label = 7;
                case 7:
                    result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.copilot-history-sync.v1', sessions: 0, sessions_changed: 0,
        events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
        symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
        preview_truncated: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 1000, 1, maxSessions, '--max-sessions');
    if (options.copilotDir !== undefined && !node_path_1.default.isAbsolute(options.copilotDir))
        throw new Error('--copilot-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveCopilotDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var root, info, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    root = value !== null && value !== void 0 ? value : node_path_1.default.join(node_os_1.default.homedir(), '.copilot');
                    if (!node_path_1.default.isAbsolute(root))
                        throw new Error('--copilot-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Copilot directory');
                        })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(root)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('Copilot directory must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(root).catch(function () { throw new Error('Copilot directory could not be resolved'); })];
                case 2:
                    canonical = _a.sent();
                    if (canonical !== node_path_1.default.resolve(root))
                        throw new Error('Copilot directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listSessionFiles(copilotDir, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var root, rootInfo, canonicalRoot, files, symlinksSkipped, oversizedSkipped, truncated, entries, _i, entries_1, entry, directory, dirInfo, sessionDir, sourcePath, fileInfo, canonicalFile, key;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    root = node_path_1.default.join(copilotDir, 'session-state');
                    return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Copilot session-state directory');
                        })];
                case 1:
                    rootInfo = _a.sent();
                    if (!rootInfo)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Copilot session-state path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(root).catch(function () { throw new Error('Copilot session-state directory could not be resolved'); })];
                case 2:
                    canonicalRoot = _a.sent();
                    if (!isWithin(copilotDir, canonicalRoot))
                        throw new Error('Copilot session-state path escaped the Copilot directory');
                    files = [];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    return [4 /*yield*/, (0, promises_1.readdir)(canonicalRoot, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Copilot sessions'); })];
                case 3:
                    entries = _a.sent();
                    _i = 0, entries_1 = entries;
                    _a.label = 4;
                case 4:
                    if (!(_i < entries_1.length)) return [3 /*break*/, 10];
                    entry = entries_1[_i];
                    directory = node_path_1.default.join(canonicalRoot, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(directory).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    dirInfo = _a.sent();
                    if (!dirInfo)
                        return [3 /*break*/, 9];
                    if (dirInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 9];
                    }
                    if (!dirInfo.isDirectory())
                        return [3 /*break*/, 9];
                    return [4 /*yield*/, (0, promises_1.realpath)(directory).catch(function () { return ''; })];
                case 6:
                    sessionDir = _a.sent();
                    if (!sessionDir || !isWithin(canonicalRoot, sessionDir)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 9];
                    }
                    sourcePath = node_path_1.default.join(sessionDir, 'events.jsonl');
                    return [4 /*yield*/, (0, promises_1.lstat)(sourcePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 7:
                    fileInfo = _a.sent();
                    if (!fileInfo)
                        return [3 /*break*/, 9];
                    if (fileInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 9];
                    }
                    if (!fileInfo.isFile())
                        return [3 /*break*/, 9];
                    if (fileInfo.size > maxFileBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 9];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(sourcePath).catch(function () { return ''; })];
                case 8:
                    canonicalFile = _a.sent();
                    if (!canonicalFile || !isWithin(canonicalRoot, canonicalFile)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 9];
                    }
                    key = node_path_1.default.relative(canonicalRoot, canonicalFile).split(node_path_1.default.sep).join('/');
                    if (!isSafeRelative(key)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 9];
                    }
                    files.push({ key: key, path: canonicalFile, sessionId: entry.name, size: fileInfo.size, mtimeMs: fileInfo.mtimeMs });
                    if (files.length > maximum) {
                        truncated = true;
                        return [3 /*break*/, 10];
                    }
                    _a.label = 9;
                case 9:
                    _i++;
                    return [3 /*break*/, 4];
                case 10:
                    files.sort(function (left, right) { return left.key < right.key ? -1 : left.key > right.key ? 1 : 0; });
                    if (truncated)
                        files.length = maximum;
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function isSafeRelative(value) {
    return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..');
}
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function readSessionFile(file, root) {
    return __awaiter(this, void 0, void 0, function () {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, error_1;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 1:
                    before = _b.sent();
                    if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes)
                        throw new Error('Copilot session is not a bounded regular file');
                    if (before.size !== file.size || before.mtimeMs !== file.mtimeMs)
                        throw new Error('Copilot session changed after enumeration');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || !isWithin(root, canonical))
                        throw new Error('Copilot session path escaped the Copilot directory');
                    _b.label = 3;
                case 3:
                    _b.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(file.path, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 4:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _b.sent();
                    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs)
                        throw new Error('Copilot session changed before reading');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 7:
                    afterHandle = _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 8:
                    afterPath = _b.sent();
                    if (bytes.byteLength > maxFileBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
                        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs)
                        throw new Error('Copilot session changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Copilot session'))
                        throw error_1;
                    throw new Error('Copilot session could not be opened safely');
                case 10: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 12: return [2 /*return*/];
            }
        });
    });
}
function sameFile(left, right) {
    return right !== null && left.dev === right.dev && left.ino === right.ino;
}
function parseAndMap(bytes, file, cursor, includeContent) {
    var _a, _b;
    var groups = [];
    var fileHash = sha256(file.key);
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var cursorLine = (_a = cursor === null || cursor === void 0 ? void 0 : cursor.line) !== null && _a !== void 0 ? _a : 0;
    var state = {
        sessionId: file.sessionId, started: (_b = cursor === null || cursor === void 0 ? void 0 : cursor.session_started) !== null && _b !== void 0 ? _b : false,
        tools: new Map(), lastUsage: new Map(), outputSinceTotals: new Map(),
    };
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var line = 0;
    var safeLines = 0;
    var start = 0;
    var malformed = 0;
    var eventCount = 0;
    var stoppedOnMalformed = false;
    for (var offset = 0; offset < bytes.length; offset++) {
        if (bytes[offset] !== 0x0a)
            continue;
        line++;
        if (line > maxLinesPerFile)
            throw new Error("Copilot session exceeds the ".concat(maxLinesPerFile, "-line processing limit"));
        var chunkStart = start;
        var end = offset;
        if (end > start && bytes[end - 1] === 0x0d)
            end--;
        var raw = bytes.subarray(start, end);
        start = offset + 1;
        var record = void 0;
        if (raw.length > maxLineBytes) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        if (raw.length > 0) {
            try {
                var decoded = JSON.parse(decoder.decode(raw));
                if (!isRecord(decoded) || !safeLabel(decoded.type, 96))
                    throw new Error('invalid event row');
                record = decoded;
            }
            catch (_c) {
                malformed++;
                stoppedOnMalformed = true;
                break;
            }
        }
        prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
        safeLines = line;
        prefixHashes.set(line, prefixHasher.copy().digest('hex'));
        if (!record)
            continue;
        var events = mapRecord(record, line, file, fileHash, state, cursorLine, includeContent);
        if (line <= cursorLine || events.length === 0)
            continue;
        if (events.length > batchSize)
            throw new Error("Copilot session line ".concat(line, " maps to more than ").concat(batchSize, " events"));
        groups.push({ line: line, events: events });
        eventCount += events.length;
        if (eventCount > maxEventsPerFile)
            throw new Error("Copilot session exceeds the ".concat(maxEventsPerFile, "-event processing limit"));
    }
    return {
        completeLines: safeLines, partialTail: !stoppedOnMalformed && start < bytes.length,
        malformed: malformed,
        prefixHashes: prefixHashes,
        completePrefixHash: prefixHasher.copy().digest('hex'),
        groups: groups,
        started: state.started,
    };
}
function mapRecord(record, line, file, fileHash, state, cursorLine, includeContent) {
    var _a, _b, _c, _d;
    var type = (_a = safeLabel(record.type, 96)) !== null && _a !== void 0 ? _a : '';
    var data = isRecord(record.data) ? record.data : {};
    var events = [];
    var id = boundedString(record.id, 512);
    if (type === 'session.start' || type === 'session.resume') {
        updateSessionContext(data, state);
        var wasStarted = state.started;
        state.started = true;
        if (line > cursorLine && !wasStarted) {
            events.push(makeEvent(record, data, file, fileHash, state, line, 'session.started', 'session', 'GitHub Copilot CLI session start recovered from local transcript', "session.start:".concat(id !== null && id !== void 0 ? id : line)));
        }
        else if (line > cursorLine && type === 'session.resume') {
            events.push(makeEvent(record, data, file, fileHash, state, line, 'session.context', 'session', 'GitHub Copilot CLI session resumed', "session.resume:".concat(id !== null && id !== void 0 ? id : line)));
        }
        return events;
    }
    if (type === 'session.model_change') {
        var model = safeLabel((_b = data.newModel) !== null && _b !== void 0 ? _b : data.selectedModel, 128);
        if (model)
            state.model = model;
        if (line > cursorLine)
            events.push(makeEvent(record, data, file, fileHash, state, line, 'session.context', 'session', 'GitHub Copilot CLI model context changed', "session.model_change:".concat(id !== null && id !== void 0 ? id : line)));
        return events;
    }
    if (type === 'user.message') {
        var text = firstText(data.content, data.transformedContent);
        if (line > cursorLine)
            pushTextEvent(events, record, data, file, fileHash, state, line, 'prompt.submitted', 'prompt', text, includeContent, 'user');
        return events;
    }
    if (type === 'assistant.message') {
        var model = safeLabel(data.model, 128);
        if (model)
            state.model = model;
        for (var _i = 0, _e = parseToolRequests(data); _i < _e.length; _i++) {
            var _f = _e[_i], toolId = _f[0], call = _f[1];
            rememberCall(state, toolId, call);
        }
        var outputTokens = nonNegativeInteger(data.outputTokens);
        if (outputTokens !== undefined && outputTokens > 0) {
            var usageModel = (_c = state.model) !== null && _c !== void 0 ? _c : '_unknown';
            state.outputSinceTotals.set(usageModel, ((_d = state.outputSinceTotals.get(usageModel)) !== null && _d !== void 0 ? _d : 0) + outputTokens);
        }
        if (line > cursorLine) {
            var reasoning = boundedString(data.reasoningText, 1 << 20);
            if (reasoning)
                events.push(makeEvent(record, data, file, fileHash, state, line, 'agent.reasoning', 'agent', 'GitHub Copilot CLI reasoning digest recovered from local transcript', "assistant.reasoning:".concat(id !== null && id !== void 0 ? id : line), { content: contentDigest(reasoning, false) }));
            var text = firstText(data.content);
            pushTextEvent(events, record, data, file, fileHash, state, line, 'agent.message', 'agent', text, includeContent, 'assistant');
            if (outputTokens !== undefined && outputTokens > 0)
                events.push(makeEvent(record, data, file, fileHash, state, line, 'token.usage', 'metric', 'GitHub Copilot CLI assistant output token usage', "assistant.usage:".concat(id !== null && id !== void 0 ? id : line), { genAi: { usage: { output_tokens: outputTokens } } }));
        }
        return events;
    }
    if (type === 'tool.execution_start') {
        var call = parseToolCall(data);
        if (call.id)
            rememberCall(state, call.id, call);
        if (line > cursorLine && call.name) {
            var input = digestValue(call.input);
            events.push(makeEvent(record, data, file, fileHash, state, line, 'tool.invoked', 'tool', 'GitHub Copilot CLI tool invocation recovered from local transcript', "tool.invoke:".concat(id !== null && id !== void 0 ? id : line), { toolName: call.name, toolCallHash: call.id ? sha256(call.id) : undefined,
                content: __assign({ retention: 'metadata', included: false }, (input ? { input_hash: input.hash, input_bytes: input.bytes } : {})) }));
        }
        return events;
    }
    if (type === 'tool.execution_complete') {
        var callId = firstString(data.toolCallId, data.tool_call_id);
        var call = callId ? state.tools.get(callId) : undefined;
        if (callId)
            state.tools.delete(callId);
        var name_1 = safeLabel(firstString(data.toolName, data.tool_name, call === null || call === void 0 ? void 0 : call.name), 128);
        if (line > cursorLine && name_1) {
            var success = data.success !== false;
            var category = toolCategory(name_1, success);
            var output = digestValue(data.result);
            events.push(makeEvent(record, data, file, fileHash, state, line, category.action, category.category, "GitHub Copilot CLI ".concat(category.label, " recovered from local transcript"), "tool.result:".concat(id !== null && id !== void 0 ? id : line), { severity: success ? 'info' : 'medium', toolName: name_1, toolCallHash: callId ? sha256(callId) : undefined,
                content: __assign({ retention: 'metadata', included: false }, (output ? { output_hash: output.hash, output_bytes: output.bytes } : {})) }));
        }
        return events;
    }
    if (type === 'session.shutdown') {
        if (line > cursorLine)
            events.push.apply(events, mapShutdownUsage(record, data, file, fileHash, state, line, id));
        else
            rememberShutdownUsage(data, state);
        return events;
    }
    if (type === 'abort' && line > cursorLine) {
        events.push(makeEvent(record, data, file, fileHash, state, line, 'agent.error', 'session', 'GitHub Copilot CLI request interrupted', "abort:".concat(id !== null && id !== void 0 ? id : line), { severity: 'medium' }));
    }
    return events;
}
function updateSessionContext(data, state) {
    var _a, _b, _c;
    var sessionId = safeIdentifier((_a = data.sessionId) !== null && _a !== void 0 ? _a : data.session_id, 512);
    if (sessionId)
        state.sessionId = sessionId;
    var version = safeLabel((_b = data.copilotVersion) !== null && _b !== void 0 ? _b : data.version, 96);
    if (version)
        state.version = version;
    var model = safeLabel((_c = data.selectedModel) !== null && _c !== void 0 ? _c : data.currentModel, 128);
    if (model)
        state.model = model;
}
function parseToolCall(data) {
    var _a, _b, _c, _d, _e, _f;
    var id = (_c = safeIdentifier((_b = (_a = data.toolCallId) !== null && _a !== void 0 ? _a : data.tool_call_id) !== null && _b !== void 0 ? _b : data.id, 512)) !== null && _c !== void 0 ? _c : '';
    var name = (_f = safeLabel((_e = (_d = data.toolName) !== null && _d !== void 0 ? _d : data.tool_name) !== null && _e !== void 0 ? _e : data.name, 128)) !== null && _f !== void 0 ? _f : '';
    return { id: id, name: name, input: parseArguments(data.arguments) };
}
function parseToolRequests(data) {
    var _a, _b, _c, _d;
    if (!Array.isArray(data.toolRequests))
        return [];
    var calls = [];
    for (var _i = 0, _e = data.toolRequests.slice(0, 1000); _i < _e.length; _i++) {
        var item = _e[_i];
        if (!isRecord(item))
            continue;
        var id = safeIdentifier((_b = (_a = item.toolCallId) !== null && _a !== void 0 ? _a : item.tool_call_id) !== null && _b !== void 0 ? _b : item.id, 512);
        var name_2 = safeLabel((_d = (_c = item.toolName) !== null && _c !== void 0 ? _c : item.tool_name) !== null && _d !== void 0 ? _d : item.name, 128);
        if (id && name_2)
            calls.push([id, { name: name_2 }]);
    }
    return calls;
}
function parseArguments(value) {
    if (typeof value !== 'string')
        return value;
    try {
        return JSON.parse(value);
    }
    catch (_a) {
        return value;
    }
}
function rememberCall(state, id, call) {
    if (state.tools.size >= maxToolCorrelations && !state.tools.has(id))
        state.tools.delete(state.tools.keys().next().value);
    state.tools.set(id, call);
}
function toolCategory(name, success) {
    var tool = name.toLowerCase();
    if (!success)
        return { action: 'tool.failed', category: 'tool', label: 'tool failure' };
    if (tool.startsWith('mcp__'))
        return { action: 'mcp.tool_invoked', category: 'mcp', label: 'MCP tool result' };
    if (tool === 'bash')
        return { action: 'command.executed', category: 'command', label: 'command result' };
    if (['view', 'read', 'read_file', 'glob', 'rg', 'grep', 'search'].includes(tool))
        return { action: 'file.read', category: 'file', label: 'file activity' };
    if (['create', 'write'].includes(tool))
        return { action: 'file.created', category: 'file', label: 'file activity' };
    if (['edit', 'str_replace_editor', 'replace', 'patch'].includes(tool))
        return { action: 'file.modified', category: 'file', label: 'file activity' };
    return { action: 'tool.completed', category: 'tool', label: 'tool result' };
}
function mapShutdownUsage(record, data, file, fileHash, state, line, id) {
    var _a, _b;
    if (isRecord(data.modelMetrics)) {
        var events = [];
        for (var _i = 0, _c = Object.entries(data.modelMetrics).slice(0, maxModels); _i < _c.length; _i++) {
            var _d = _c[_i], modelName = _d[0], value = _d[1];
            if (!isRecord(value) || !isRecord(value.usage))
                continue;
            var model = safeLabel(modelName, 128);
            if (!model)
                continue;
            var current = usageTotalsFrom(value.usage);
            var previous = (_a = state.lastUsage.get(model)) !== null && _a !== void 0 ? _a : zeroUsage();
            var outputReported = (_b = state.outputSinceTotals.get(model)) !== null && _b !== void 0 ? _b : 0;
            var usage = {};
            assignPositive(usage, 'input_tokens', current.input - previous.input);
            assignPositive(usage, 'output_tokens', current.output - previous.output - outputReported);
            if (current.cacheRead > previous.cacheRead)
                usage.cache_read = { input_tokens: current.cacheRead - previous.cacheRead };
            if (current.cacheWrite > previous.cacheWrite)
                usage.cache_creation = { input_tokens: current.cacheWrite - previous.cacheWrite };
            if (current.reasoning > previous.reasoning)
                usage.reasoning = { output_tokens: current.reasoning - previous.reasoning };
            state.lastUsage.set(model, current);
            state.outputSinceTotals.set(model, 0);
            if (Object.keys(usage).length > 0)
                events.push(makeEvent(record, data, file, fileHash, __assign(__assign({}, state), { model: model }), line, 'token.usage', 'metric', 'GitHub Copilot CLI shutdown token usage', "shutdown.usage:".concat(model, ":").concat(id !== null && id !== void 0 ? id : line), { genAi: { usage: usage } }));
        }
        return events;
    }
    return [];
}
function rememberShutdownUsage(data, state) {
    if (!isRecord(data.modelMetrics))
        return;
    for (var _i = 0, _a = Object.entries(data.modelMetrics).slice(0, maxModels); _i < _a.length; _i++) {
        var _b = _a[_i], modelName = _b[0], value = _b[1];
        if (isRecord(value) && isRecord(value.usage)) {
            var model = safeLabel(modelName, 128);
            if (model) {
                state.lastUsage.set(model, usageTotalsFrom(value.usage));
                state.outputSinceTotals.set(model, 0);
            }
        }
    }
}
function usageTotalsFrom(value) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k;
    return {
        input: (_b = nonNegativeInteger((_a = value.inputTokens) !== null && _a !== void 0 ? _a : value.input_tokens)) !== null && _b !== void 0 ? _b : 0,
        output: (_d = nonNegativeInteger((_c = value.outputTokens) !== null && _c !== void 0 ? _c : value.output_tokens)) !== null && _d !== void 0 ? _d : 0,
        cacheRead: (_f = nonNegativeInteger((_e = value.cacheReadTokens) !== null && _e !== void 0 ? _e : value.cache_read_tokens)) !== null && _f !== void 0 ? _f : 0,
        cacheWrite: (_h = nonNegativeInteger((_g = value.cacheWriteTokens) !== null && _g !== void 0 ? _g : value.cache_write_tokens)) !== null && _h !== void 0 ? _h : 0,
        reasoning: (_k = nonNegativeInteger((_j = value.reasoningTokens) !== null && _j !== void 0 ? _j : value.reasoning_tokens)) !== null && _k !== void 0 ? _k : 0,
    };
}
function zeroUsage() { return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 }; }
function assignPositive(target, key, value) {
    if (Number.isSafeInteger(value) && value > 0)
        target[key] = value;
}
function pushTextEvent(events, record, data, file, fileHash, state, line, action, category, text, includeContent, role) {
    var _a;
    var value = text.trim();
    if (!value)
        return;
    var included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
    events.push(makeEvent(record, data, file, fileHash, state, line, action, category, role === 'user' ? 'GitHub Copilot CLI prompt recovered from local transcript' : 'GitHub Copilot CLI assistant message recovered from local transcript', "".concat(role, ":").concat((_a = safeIdentifier(record.id, 512)) !== null && _a !== void 0 ? _a : line), __assign({ content: contentDigest(value, included, includeContent) }, (included ? { text: value } : {}))));
}
function makeEvent(record, data, file, fileHash, state, line, action, category, message, suffix, extra) {
    var _a, _b, _c, _d;
    if (extra === void 0) { extra = {}; }
    var recordId = safeIdentifier(record.id, 512);
    var sessionId = (_b = safeIdentifier((_a = data.sessionId) !== null && _a !== void 0 ? _a : data.session_id, 512)) !== null && _b !== void 0 ? _b : state.sessionId;
    var event = {
        timestamp: parseTimestamp(record.timestamp, file.mtimeMs),
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(fileHash, ":").concat(line, ":").concat(recordId ? sha256(recordId) : '', ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_c = extra.severity) !== null && _c !== void 0 ? _c : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: __assign(__assign({ name: 'copilot-cli' }, (state.version ? { version: state.version } : {})), { collection_method: 'poll' }),
        session: { id: "copilot-cli:".concat(sha256(sessionId)) },
        raw: __assign(__assign({ source: 'copilot_cli_session_jsonl', source_sha256: fileHash, line: line, record_type: (_d = safeLabel(record.type, 96)) !== null && _d !== void 0 ? _d : 'unknown' }, (recordId ? { record_id_sha256: sha256(recordId) } : {})), { message: message }),
    };
    var model = state.model;
    if (model)
        event.model = model;
    if (extra.toolName)
        event.tool = { name: extra.toolName };
    if (extra.toolCallHash)
        event.raw = __assign(__assign({}, event.raw), { tool_call_sha256: extra.toolCallHash });
    if (extra.content)
        event.content = extra.content;
    if (extra.text !== undefined) {
        if (action === 'prompt.submitted') {
            event.prompt = { text: extra.text };
            event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: extra.text }] }] } };
        }
        else {
            event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: extra.text }] }] } };
        }
    }
    if (extra.genAi)
        event.gen_ai = extra.genAi;
    return event;
}
function contentDigest(text, included, requested) {
    if (requested === void 0) { requested = false; }
    return __assign({ retention: included ? 'full' : 'metadata', included: included, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') }, (requested && !included ? { truncated: true } : {}));
}
function digestValue(value) {
    if (value === undefined || value === null)
        return undefined;
    var text;
    try {
        text = typeof value === 'string' ? value : JSON.stringify(value);
    }
    catch (_a) {
        return undefined;
    }
    if (typeof text !== 'string')
        return undefined;
    return { hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}
function firstText() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_1 = values; _a < values_1.length; _a++) {
        var value = values_1[_a];
        if (typeof value === 'string')
            return value;
        if (isRecord(value)) {
            for (var _b = 0, _c = ['text', 'content', 'value']; _b < _c.length; _b++) {
                var key = _c[_b];
                if (typeof value[key] === 'string')
                    return value[key];
            }
        }
    }
    return '';
}
function firstString() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_2 = values; _a < values_2.length; _a++) {
        var value = values_2[_a];
        var text = boundedString(value, 2048);
        if (text)
            return text;
    }
    return '';
}
function safeIdentifier(value, maximum) {
    return boundedString(value, maximum);
}
function safeLabel(value, maximum) {
    var text = boundedString(value, maximum);
    if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text))
        return undefined;
    return text;
}
function parseTimestamp(value, fallbackMs) {
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
        var milliseconds = value < 1000000000000 ? value * 1000 : value;
        var date = new Date(milliseconds);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    if (typeof value === 'string') {
        var numeric = Number(value);
        if (value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0)
            return parseTimestamp(numeric, fallbackMs);
        var date = new Date(value);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    return new Date(fallbackMs).toISOString();
}
function processFiles(files, root, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var totalBytes, _loop_1, _i, files_1, file, state_1;
        var _this = this;
        var _a, _b, _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    totalBytes = 0;
                    _loop_1 = function (file) {
                        var key, previous, bytes, parsed_1, cursorLine, fileEvents, _e, _f, group, _g, _h, event_1, batch_1, batchEvents_1, failed, flush, _j, _k, group, _l, _m;
                        return __generator(this, function (_o) {
                            switch (_o.label) {
                                case 0:
                                    key = sha256(file.key);
                                    previous = state.files[key];
                                    _o.label = 1;
                                case 1:
                                    _o.trys.push([1, 14, , 15]);
                                    return [4 /*yield*/, readSessionFile(file, root)];
                                case 2:
                                    bytes = _o.sent();
                                    totalBytes += bytes.byteLength;
                                    if (totalBytes > maxTotalBytes) {
                                        result.sessions_truncated = true;
                                        result.errors.push({ source_sha256: key, message: 'Copilot sync exceeded the total source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    parsed_1 = parseAndMap(bytes, file, previous, (_a = options.includeContent) !== null && _a !== void 0 ? _a : false);
                                    result.malformed_lines += parsed_1.malformed;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: key, message: 'Copilot transcript contains a malformed row; scanning stops before it' });
                                    if (parsed_1.partialTail)
                                        result.partial_sessions++;
                                    cursorLine = (_b = previous === null || previous === void 0 ? void 0 : previous.line) !== null && _b !== void 0 ? _b : 0;
                                    if (previous && (parsed_1.completeLines < cursorLine || parsed_1.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: key, message: 'Copilot transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    fileEvents = parsed_1.groups.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    if (fileEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: key, message: 'Copilot sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += fileEvents;
                                        (_c = result.events) !== null && _c !== void 0 ? _c : (result.events = []);
                                        for (_e = 0, _f = parsed_1.groups; _e < _f.length; _e++) {
                                            group = _f[_e];
                                            for (_g = 0, _h = group.events; _g < _h.length; _g++) {
                                                event_1 = _h[_g];
                                                if (result.events.length < maxPreviewEvents)
                                                    result.events.push(event_1);
                                                else
                                                    result.preview_truncated = true;
                                            }
                                        }
                                        return [2 /*return*/, "continue"];
                                    }
                                    batch_1 = [];
                                    batchEvents_1 = 0;
                                    failed = false;
                                    flush = function () { return __awaiter(_this, void 0, void 0, function () {
                                        var send, lastLine, started, prefix;
                                        var _a;
                                        return __generator(this, function (_b) {
                                            switch (_b.label) {
                                                case 0:
                                                    if (batch_1.length === 0)
                                                        return [2 /*return*/];
                                                    send = batch_1.flatMap(function (group) { return group.events; });
                                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(send, { endpoint: (_a = options.endpoint) !== null && _a !== void 0 ? _a : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                                case 1:
                                                    _b.sent();
                                                    lastLine = batch_1[batch_1.length - 1].line;
                                                    started = (previous === null || previous === void 0 ? void 0 : previous.session_started) === true || batch_1.some(function (group) { return group.events.some(function (event) { return event.event.action === 'session.started'; }); });
                                                    prefix = parsed_1.prefixHashes.get(lastLine);
                                                    if (!prefix)
                                                        throw new Error('Copilot session checkpoint prefix is unavailable');
                                                    state.files[key] = makeCursor(lastLine, file, prefix, started);
                                                    result.events_emitted += send.length;
                                                    if (!(persist && statePath)) return [3 /*break*/, 3];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 2:
                                                    _b.sent();
                                                    _b.label = 3;
                                                case 3:
                                                    batch_1 = [];
                                                    batchEvents_1 = 0;
                                                    return [2 /*return*/];
                                            }
                                        });
                                    }); };
                                    _o.label = 3;
                                case 3:
                                    _o.trys.push([3, 10, , 11]);
                                    _j = 0, _k = parsed_1.groups;
                                    _o.label = 4;
                                case 4:
                                    if (!(_j < _k.length)) return [3 /*break*/, 8];
                                    group = _k[_j];
                                    if (!(batchEvents_1 > 0 && batchEvents_1 + group.events.length > batchSize)) return [3 /*break*/, 6];
                                    return [4 /*yield*/, flush()];
                                case 5:
                                    _o.sent();
                                    _o.label = 6;
                                case 6:
                                    batch_1.push(group);
                                    batchEvents_1 += group.events.length;
                                    _o.label = 7;
                                case 7:
                                    _j++;
                                    return [3 /*break*/, 4];
                                case 8: return [4 /*yield*/, flush()];
                                case 9:
                                    _o.sent();
                                    return [3 /*break*/, 11];
                                case 10:
                                    _l = _o.sent();
                                    failed = true;
                                    result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Copilot history events' });
                                    return [3 /*break*/, 11];
                                case 11:
                                    if (failed)
                                        return [2 /*return*/, "continue"];
                                    if (!(parsed_1.completeLines > cursorLine || (previous !== undefined && previous.session_started !== parsed_1.started))) return [3 /*break*/, 13];
                                    state.files[key] = makeCursor(parsed_1.completeLines, file, parsed_1.completePrefixHash, parsed_1.started);
                                    result.sessions_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 13];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 12:
                                    _o.sent();
                                    _o.label = 13;
                                case 13: return [3 /*break*/, 15];
                                case 14:
                                    _m = _o.sent();
                                    result.errors.push({ source_sha256: key, message: 'Copilot session could not be read or checkpointed safely' });
                                    return [3 /*break*/, 15];
                                case 15: return [2 /*return*/];
                            }
                        });
                    };
                    _i = 0, files_1 = files;
                    _d.label = 1;
                case 1:
                    if (!(_i < files_1.length)) return [3 /*break*/, 4];
                    file = files_1[_i];
                    return [5 /*yield**/, _loop_1(file)];
                case 2:
                    state_1 = _d.sent();
                    if (state_1 === "break")
                        return [3 /*break*/, 4];
                    _d.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function makeCursor(line, file, prefix, started) {
    return { line: line, size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix, session_started: started };
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
        var lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, verifyStateParent(statePath, true)];
                case 1:
                    _a.sent();
                    _a.label = 2;
                case 2:
                    _a.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, (0, promises_1.open)("".concat(statePath, ".lock"), node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 3:
                    lock = _a.sent();
                    return [3 /*break*/, 5];
                case 4:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'EEXIST'))
                        throw new Error('Copilot history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Copilot history cursor');
                case 5:
                    _a.trys.push([5, , 7, 10]);
                    return [4 /*yield*/, action()];
                case 6: return [2 /*return*/, _a.sent()];
                case 7: return [4 /*yield*/, lock.close().catch(function () { })];
                case 8:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)("".concat(statePath, ".lock")).catch(function () { })];
                case 9:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 10: return [2 /*return*/];
            }
        });
    });
}
function readState(statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var empty, info, handle, opened, bytes, parsed, files, _i, _a, _b, key, value, error_3;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.copilot-session-cursor.v1', version: stateVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Copilot history cursor');
                        })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('Copilot history cursor must be a bounded regular file');
                    _d.label = 3;
                case 3:
                    _d.trys.push([3, 7, 8, 10]);
                    return [4 /*yield*/, (0, promises_1.open)(statePath, node_fs_1.constants.O_RDONLY | ((_c = node_fs_1.constants.O_NOFOLLOW) !== null && _c !== void 0 ? _c : 0))];
                case 4:
                    handle = _d.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _d.sent();
                    if (opened.dev !== info.dev || opened.ino !== info.ino)
                        throw new Error('Copilot cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxStateBytes)
                        throw new Error('Copilot cursor exceeds local size limit');
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
                        throw new Error('Copilot history cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean') {
                            throw new Error('Copilot history cursor contains an invalid checkpoint');
                        }
                        files[key] = { line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
                            prefix_sha256: value.prefix_sha256, session_started: value.session_started };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Copilot history cursor'))
                        throw error_3;
                    throw new Error('Copilot history cursor is malformed or unreadable');
                case 8: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 9:
                    _d.sent();
                    return [7 /*endfinally*/];
                case 10: return [2 /*return*/];
            }
        });
    });
}
function verifyStateParent(statePath, create) {
    return __awaiter(this, void 0, void 0, function () {
        var parent, parsed, segments, current, _i, segments_1, segment, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    parent = node_path_1.default.dirname(node_path_1.default.resolve(statePath));
                    parsed = node_path_1.default.parse(parent);
                    segments = parent.slice(parsed.root.length).split(node_path_1.default.sep).filter(Boolean);
                    current = parsed.root;
                    _i = 0, segments_1 = segments;
                    _a.label = 1;
                case 1:
                    if (!(_i < segments_1.length)) return [3 /*break*/, 7];
                    segment = segments_1[_i];
                    current = node_path_1.default.join(current, segment);
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    info = _a.sent();
                    if (!(!info && create)) return [3 /*break*/, 5];
                    return [4 /*yield*/, (0, promises_1.mkdir)(current, { mode: 448 }).catch(function (error) { if (!isCode(error, 'EEXIST'))
                            throw new Error('unable to create private Copilot cursor directory'); })];
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
                        throw new Error('Copilot cursor path cannot contain symbolic links or non-directory parents');
                    _a.label = 6;
                case 6:
                    _i++;
                    return [3 /*break*/, 1];
                case 7: return [2 /*return*/];
            }
        });
    });
}
function writeState(statePath, state) {
    return __awaiter(this, void 0, void 0, function () {
        var payload, temporary, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    payload = Buffer.from("".concat(JSON.stringify(state, null, 2), "\n"), 'utf8');
                    if (payload.byteLength > maxStateBytes)
                        throw new Error('Copilot history cursor exceeds the local size limit');
                    temporary = "".concat(statePath, ".").concat((0, node_crypto_1.randomBytes)(12).toString('hex'), ".tmp");
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 8, , 11]);
                    return [4 /*yield*/, (0, promises_1.open)(temporary, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.chmod(384)];
                case 3:
                    _b.sent();
                    return [4 /*yield*/, handle.writeFile(payload)];
                case 4:
                    _b.sent();
                    return [4 /*yield*/, handle.sync()];
                case 5:
                    _b.sent();
                    return [4 /*yield*/, handle.close()];
                case 6:
                    _b.sent();
                    handle = undefined;
                    return [4 /*yield*/, (0, promises_1.rename)(temporary, statePath)];
                case 7:
                    _b.sent();
                    return [3 /*break*/, 11];
                case 8:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 9:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(temporary).catch(function () { })];
                case 10:
                    _b.sent();
                    throw new Error('unable to atomically write Copilot history cursor');
                case 11: return [2 /*return*/];
            }
        });
    });
}
function boundedInteger(value, minimum, maximum, label) {
    if (!Number.isSafeInteger(value) || value < minimum || value > maximum)
        throw new Error("".concat(label, " must be an integer between ").concat(minimum, " and ").concat(maximum));
    return value;
}
function boundedString(value, maximum) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}
function nonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function isNonNegativeInteger(value) { return nonNegativeInteger(value) !== undefined; }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function normalizedOS(platform) {
    if (platform === 'win32')
        return 'windows';
    if (platform === 'darwin')
        return 'macos';
    if (platform === 'linux')
        return 'linux';
    return 'other';
}
