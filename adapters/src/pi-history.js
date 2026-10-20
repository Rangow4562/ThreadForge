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
exports.getPiHistoryStatus = getPiHistoryStatus;
exports.syncPiHistory = syncPiHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var cursorVersion = 1;
var cursorName = 'pi-session-cursor.v1.json';
var maxCursorBytes = 8 << 20;
var defaultMaxFiles = 1000;
var maxFiles = 15000;
var maxScanEntries = 100000;
var maxFileBytes = 64 << 20;
var maxTotalBytes = 512 << 20;
var maxLineBytes = 10 << 20;
var maxLinesPerFile = 100000;
var maxEventsPerFile = 10000;
var maxEventsPerSync = 20000;
var maxCallCorrelations = 10000;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
var eventBatchSize = 100;
var toolCallKinds = new Set(['tool_call', 'tool_use', 'function_call', 'toolcall']);
var toolResultKinds = new Set(['tool_result', 'tool_output', 'function_call_output', 'toolresult']);
function getPiHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, sessionsDir, statePath, listed, state, checkpointed;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    maximum = boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : defaultMaxFiles, 1, maxFiles, '--max-files');
                    return [4 /*yield*/, resolveSessionsDir(options.sessionsDir)];
                case 1:
                    sessionsDir = _b.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(sessionsDir, statePath))
                        throw new Error('Pi history cursor must be stored outside Pi sessions');
                    return [4 /*yield*/, listFiles(sessionsDir, maximum)];
                case 2:
                    listed = _b.sent();
                    return [4 /*yield*/, readState(statePath)];
                case 3:
                    state = _b.sent();
                    checkpointed = listed.files.filter(function (file) {
                        var cursor = state.files[file.sourceSha256];
                        return cursor !== undefined && !cursor.incomplete && cursor.last_line > 0 &&
                            cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
                    }).length;
                    return [2 /*return*/, {
                            schema_version: 'threadforge.pi-history-status.v1', files: listed.files.length,
                            checkpointed_files: checkpointed, pending_files: listed.files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
                            files_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncPiHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var sessionsDir, statePath, listed, result, _a, _b;
        var _this = this;
        var _c, _d;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveSessionsDir(options.sessionsDir)];
                case 1:
                    sessionsDir = _e.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(sessionsDir, statePath))
                        throw new Error('Pi history cursor must be stored outside Pi sessions');
                    return [4 /*yield*/, listFiles(sessionsDir, (_c = options.maxFiles) !== null && _c !== void 0 ? _c : defaultMaxFiles)];
                case 2:
                    listed = _e.sent();
                    result = emptyResult((_d = options.includeContent) !== null && _d !== void 0 ? _d : false);
                    result.files = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.files_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    _a = processFiles;
                    _b = [listed.files, sessionsDir];
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
                                    return [4 /*yield*/, processFiles(listed.files, sessionsDir, state, options, result, true, statePath)];
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
                    result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_files === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.pi-history-sync.v1', files: 0, files_changed: 0, events_emitted: 0,
        events_previewed: 0, malformed_lines: 0, partial_files: 0, symlinks_skipped: 0, oversized_files_skipped: 0,
        files_truncated: false, preview_truncated: false, content_mode: includeContent ? 'include-content' : 'metadata',
        complete: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : defaultMaxFiles, 1, maxFiles, '--max-files');
    if (options.sessionsDir !== undefined && !node_path_1.default.isAbsolute(options.sessionsDir))
        throw new Error('--sessions-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveSessionsDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var sessionsDir, home, override, agentDir, info, canonical;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    sessionsDir = value;
                    if (!sessionsDir) {
                        home = node_os_1.default.homedir();
                        override = (_a = process.env.PI_CODING_AGENT_DIR) === null || _a === void 0 ? void 0 : _a.trim();
                        agentDir = override ? expandTilde(override, home) : node_path_1.default.join(home, '.pi', 'agent');
                        if (!node_path_1.default.isAbsolute(agentDir))
                            throw new Error('PI_CODING_AGENT_DIR must resolve to an absolute path');
                        sessionsDir = node_path_1.default.join(agentDir, 'sessions');
                    }
                    if (!node_path_1.default.isAbsolute(sessionsDir))
                        throw new Error('--sessions-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(sessionsDir).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _b.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(sessionsDir)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('Pi sessions path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(sessionsDir).catch(function () { throw new Error('Pi sessions directory could not be resolved'); })];
                case 2:
                    canonical = _b.sent();
                    if (canonical !== node_path_1.default.resolve(sessionsDir))
                        throw new Error('Pi sessions directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function expandTilde(value, home) {
    if (value === '~')
        return home;
    if (value.startsWith('~/') || value.startsWith('~\\'))
        return node_path_1.default.join(home, value.slice(2));
    return value;
}
function listFiles(root, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInfo, files, pending, entriesScanned, symlinksSkipped, oversizedSkipped, truncated, directory, entries, _i, entries_1, entry, fullPath, info, canonical, relative;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    rootInfo = _a.sent();
                    if (!rootInfo)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Pi sessions path must be a real directory');
                    files = [];
                    pending = [root];
                    entriesScanned = 0;
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    _a.label = 2;
                case 2:
                    if (!(pending.length > 0)) return [3 /*break*/, 9];
                    directory = pending.pop();
                    return [4 /*yield*/, (0, promises_1.readdir)(directory, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Pi session files'); })];
                case 3:
                    entries = _a.sent();
                    _i = 0, entries_1 = entries;
                    _a.label = 4;
                case 4:
                    if (!(_i < entries_1.length)) return [3 /*break*/, 8];
                    entry = entries_1[_i];
                    entriesScanned++;
                    if (entriesScanned > maxScanEntries) {
                        truncated = true;
                        return [3 /*break*/, 8];
                    }
                    fullPath = node_path_1.default.join(directory, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(fullPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    info = _a.sent();
                    if (!info)
                        return [3 /*break*/, 7];
                    if (info.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    if (info.isDirectory()) {
                        pending.push(fullPath);
                        return [3 /*break*/, 7];
                    }
                    if (!info.isFile() || node_path_1.default.extname(entry.name).toLowerCase() !== '.jsonl')
                        return [3 /*break*/, 7];
                    if (info.size > maxFileBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 7];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(fullPath).catch(function () { return ''; })];
                case 6:
                    canonical = _a.sent();
                    if (!canonical || canonical !== node_path_1.default.resolve(fullPath) || !isWithin(root, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    relative = node_path_1.default.relative(root, canonical).split(node_path_1.default.sep).join('/');
                    if (!isSafeRelative(relative)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    files.push({ path: canonical, relative: relative, sourceSha256: sha256(relative), name: node_path_1.default.basename(fullPath), size: info.size, mtimeMs: info.mtimeMs });
                    if (files.length > maximum) {
                        truncated = true;
                        return [3 /*break*/, 8];
                    }
                    _a.label = 7;
                case 7:
                    _i++;
                    return [3 /*break*/, 4];
                case 8:
                    if (truncated)
                        return [3 /*break*/, 9];
                    return [3 /*break*/, 2];
                case 9:
                    files.sort(function (left, right) { return left.mtimeMs - right.mtimeMs || (left.relative < right.relative ? -1 : left.relative > right.relative ? 1 : 0); });
                    if (files.length > maximum)
                        files.length = maximum;
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function readPiFile(file, root) {
    return __awaiter(this, void 0, void 0, function () {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, error_1;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 1:
                    before = _b.sent();
                    if (!before || !before.isFile() || before.isSymbolicLink() || before.size !== file.size || before.mtimeMs !== file.mtimeMs || before.size > maxFileBytes) {
                        throw new Error('Pi transcript changed after enumeration');
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || canonical !== file.path || !isWithin(root, canonical))
                        throw new Error('Pi transcript path escaped sessions directory');
                    _b.label = 3;
                case 3:
                    _b.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(file.path, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 4:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _b.sent();
                    if (!sameFile(before, opened) || opened.size !== file.size || opened.mtimeMs !== file.mtimeMs)
                        throw new Error('Pi transcript changed before read');
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
                        throw new Error('Pi transcript changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Pi transcript'))
                        throw error_1;
                    throw new Error('Pi transcript could not be opened safely');
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
function parsePiFile(bytes) {
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var entries = [];
    var offset = 0;
    var line = 0;
    var malformed = 0;
    var partialTail = false;
    var completeLines = 0;
    while (offset < bytes.length) {
        var newline = bytes.indexOf(0x0a, offset);
        if (newline < 0) {
            partialTail = bytes.length > offset;
            break;
        }
        line++;
        if (line > maxLinesPerFile) {
            malformed++;
            break;
        }
        var start = offset;
        var end = newline > offset && bytes[newline - 1] === 0x0d ? newline - 1 : newline;
        var raw = bytes.subarray(offset, end);
        offset = newline + 1;
        if (raw.byteLength > maxLineBytes) {
            malformed++;
            break;
        }
        if (raw.toString('utf8').trim() === '') {
            prefixHasher.update(bytes.subarray(start, newline + 1));
            completeLines = line;
            prefixHashes.set(line, prefixHasher.copy().digest('hex'));
            continue;
        }
        var parsed = void 0;
        try {
            parsed = JSON.parse(decoder.decode(raw));
        }
        catch (_a) {
            malformed++;
            break;
        }
        if (!isRecord(parsed)) {
            malformed++;
            break;
        }
        prefixHasher.update(bytes.subarray(start, newline + 1));
        completeLines = line;
        prefixHashes.set(line, prefixHasher.copy().digest('hex'));
        entries.push({ line: line, value: parsed });
    }
    return { entries: entries, completeLines: completeLines, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex'), partialTail: partialTail, malformed: malformed };
}
function mapPiEntries(file, parsed, minLine, started, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g, _h;
    var header = (_b = (_a = parsed.entries.find(function (entry) { return typeOf(entry.value) === 'session'; })) === null || _a === void 0 ? void 0 : _a.value) !== null && _b !== void 0 ? _b : {};
    var fallbackId = node_path_1.default.basename(file.name, node_path_1.default.extname(file.name));
    var state = __assign(__assign(__assign(__assign(__assign({ sessionId: (_c = boundedString(header.id, 512)) !== null && _c !== void 0 ? _c : fallbackId, sessionIdHash: sha256((_d = boundedString(header.id, 512)) !== null && _d !== void 0 ? _d : fallbackId) }, (boundedString(header.cwd, 4096) ? { cwd: String(header.cwd) } : {})), (safeLabel((_e = header.model) !== null && _e !== void 0 ? _e : header.modelId, 160) ? { model: safeLabel((_f = header.model) !== null && _f !== void 0 ? _f : header.modelId, 160) } : {})), (isRecord(header.git) && boundedString(header.git.repoUrl, 2048) ? { repository: String(header.git.repoUrl) } : {})), (isRecord(header.git) && safeLabel(header.git.branch, 256) ? { branch: safeLabel(header.git.branch, 256) } : {})), { calls: new Map(), started: started });
    var groups = [];
    if (!started && parsed.entries.length > 0) {
        var event_1 = makePiEvent(file, 0, 'session.started', 'session.started', 'session', 'Pi session start recovered from local transcript', state, timestampValue(header.timestamp, file.mtimeMs), { fx: __assign(__assign(__assign({}, (state.cwd ? { cwd_sha256: sha256(state.cwd) } : {})), (state.repository ? { repository_sha256: sha256(state.repository) } : {})), (state.branch ? { branch_sha256: sha256(state.branch) } : {})) });
        groups.push({ line: 0, prefix: sha256(Buffer.alloc(0)), events: [event_1], started: true });
        state.started = true;
    }
    var eventsInFile = (_h = (_g = groups[0]) === null || _g === void 0 ? void 0 : _g.events.length) !== null && _h !== void 0 ? _h : 0;
    for (var _i = 0, _j = parsed.entries; _i < _j.length; _i++) {
        var entry = _j[_i];
        var events = [];
        var emit = entry.line > minLine;
        mapPiEntry(events, file, entry, state, emit, includeContent);
        eventsInFile += events.length;
        if (eventsInFile > maxEventsPerFile)
            throw new Error("Pi transcript exceeds the ".concat(maxEventsPerFile, "-event processing limit"));
        var prefix = parsed.prefixHashes.get(entry.line);
        if (prefix)
            groups.push({ line: entry.line, prefix: prefix, events: events, started: state.started });
    }
    return { groups: groups, state: state };
}
function mapPiEntry(events, file, entry, state, emit, includeContent) {
    var _a, _b, _c, _d, _e, _f;
    var data = entry.value;
    var type = typeOf(data);
    var timestamp = timestampValue(data.timestamp, file.mtimeMs);
    if (type === 'session') {
        var id = boundedString(data.id, 512);
        if (id) {
            state.sessionId = id;
            state.sessionIdHash = sha256(id);
        }
        var cwd = boundedString(data.cwd, 4096);
        if (cwd)
            state.cwd = cwd;
        var model = safeLabel((_a = data.model) !== null && _a !== void 0 ? _a : data.modelId, 160);
        if (model)
            state.model = model;
        if (isRecord(data.git)) {
            var repository = boundedString(data.git.repoUrl, 2048);
            var branch = safeLabel(data.git.branch, 256);
            if (repository)
                state.repository = repository;
            if (branch)
                state.branch = branch;
        }
        return;
    }
    if (type === 'model_change') {
        var model = safeLabel((_b = data.modelId) !== null && _b !== void 0 ? _b : data.model, 160);
        if (model) {
            state.model = model;
            if (emit) {
                var event_2 = makePiEvent(file, entry.line, 'model_change', 'session.context', 'session', 'Pi model changed', state, timestamp, { fx: { change: 'model_change', model: model } });
                pushPiEvent(events, event_2);
            }
        }
        return;
    }
    if (type === 'compaction') {
        if (emit) {
            var summary = textValue(data.summary);
            var event_3 = makePiEvent(file, entry.line, 'compaction', 'session.compacting', 'session', 'Pi compacted session history', state, timestamp, { fx: __assign(__assign(__assign({}, (summary ? { summary: contentDigest(summary) } : {})), (safeOpaque(data.firstKeptEntryId, 512) ? { first_kept_entry_sha256: sha256(String(data.firstKeptEntryId)) } : {})), (nonNegativeInteger(data.tokensBefore) !== undefined ? { tokens_before: data.tokensBefore } : {})) });
            pushPiEvent(events, event_3);
        }
        return;
    }
    if (type === 'thinking_level_change')
        return;
    if (type === 'custom') {
        if (data.customType !== 'context:skill_loaded' || !emit || !isRecord(data.data))
            return;
        var name_1 = safeLabel(data.data.name, 128);
        if (!name_1)
            return;
        var rawId = (_c = safeOpaque(data.id, 512)) !== null && _c !== void 0 ? _c : "line-".concat(entry.line, "-skill");
        var call = { id: rawId, name: 'skill', args: __assign({ name: name_1 }, (boundedString(data.data.path, 4096) ? { path: data.data.path } : {})) };
        var invoke = makeToolEvent(file, entry.line, 'skill.loaded.call', 'tool.invoked', 'tool', 'Pi skill load observed', state, timestamp, call);
        var complete = makeToolEvent(file, entry.line, 'skill.loaded.result', 'tool.completed', 'tool', 'Pi skill load completed', state, timestamp, call);
        complete.raw = __assign(__assign({}, complete.raw), { fx: __assign({ custom_type: 'context:skill_loaded', skill_name: name_1 }, (boundedString(data.data.path, 4096) ? { skill_path_sha256: sha256(String(data.data.path)) } : {})) });
        pushPiEvent(events, invoke);
        pushPiEvent(events, complete);
        return;
    }
    if (type === 'message') {
        if (isRecord(data.message))
            mapMessage(events, file, entry.line, data.message, timestamp, state, emit, includeContent);
        return;
    }
    if (toolCallKinds.has(type)) {
        var call = makeCallState(entry.line, 0, data);
        rememberCall(state.calls, call);
        if (emit)
            pushPiEvent(events, makeToolEvent(file, entry.line, 'tool_call', 'tool.invoked', 'tool', 'Pi tool invoked', state, timestamp, call));
        return;
    }
    if (toolResultKinds.has(type)) {
        if (emit)
            mapToolResult(events, file, entry.line, timestamp, state, data, (_f = safeOpaque((_e = (_d = data.toolCallId) !== null && _d !== void 0 ? _d : data.callId) !== null && _e !== void 0 ? _e : data.id, 512)) !== null && _f !== void 0 ? _f : "line-".concat(entry.line));
        return;
    }
    if (type === 'bashexecution' || type === 'bash_execution') {
        if (emit)
            mapBashExecution(events, file, entry.line, timestamp, state, data);
        return;
    }
    if (type === 'error' && emit)
        mapPiError(events, file, entry.line, timestamp, state, data);
}
function pushPiEvent(events, event) {
    if (events.length >= eventBatchSize)
        throw new Error("Pi transcript row exceeds the ".concat(eventBatchSize, "-event batch limit"));
    events.push(event);
}
function mapMessage(events, file, line, message, fallback, state, emit, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l;
    var role = String((_a = message.role) !== null && _a !== void 0 ? _a : '').toLowerCase();
    var timestamp = timestampValue(message.timestamp, fallback);
    if (role === 'user') {
        if (emit) {
            var prompts = textParts(message.content);
            for (var index = 0; index < prompts.length; index++) {
                pushPiEvent(events, makeTextEvent(file, line, "user.".concat(index), 'prompt.submitted', 'prompt', 'Pi prompt recovered from local transcript', state, timestamp, prompts[index], includeContent, 'user'));
            }
        }
        return;
    }
    if (role === 'assistant') {
        var model = safeLabel((_b = message.model) !== null && _b !== void 0 ? _b : message.modelId, 160);
        if (model)
            state.model = model;
        var parts = Array.isArray(message.content) ? message.content : typeof message.content === 'string' ? [{ type: 'text', text: message.content }] : [];
        for (var index = 0; index < parts.length; index++) {
            var part = isRecord(parts[index]) ? parts[index] : {};
            var kind = String((_c = part.type) !== null && _c !== void 0 ? _c : '').toLowerCase();
            if (kind === 'thinking') {
                var text = textValue((_e = (_d = part.thinking) !== null && _d !== void 0 ? _d : part.text) !== null && _e !== void 0 ? _e : part.content);
                if (emit && text)
                    pushPiEvent(events, makePiEvent(file, line, "assistant.".concat(index, ".thinking"), 'agent.reasoning', 'agent', 'Pi reasoning observed', state, timestamp, { content: contentDigest(text) }));
            }
            else if (kind === 'text') {
                var text = textValue((_f = part.text) !== null && _f !== void 0 ? _f : part.content);
                if (emit && text)
                    pushPiEvent(events, makeTextEvent(file, line, "assistant.".concat(index, ".text"), 'agent.message', 'agent', 'Pi assistant message recovered from local transcript', state, timestamp, text, includeContent, 'assistant'));
            }
            else if (toolCallKinds.has(kind)) {
                var call = makeCallState(line, index, part);
                rememberCall(state.calls, call);
                if (emit)
                    pushPiEvent(events, makeToolEvent(file, line, "assistant.".concat(index, ".tool_call"), 'tool.invoked', 'tool', 'Pi tool invoked', state, timestamp, call));
            }
            else if (toolResultKinds.has(kind) && emit) {
                var callId = (_j = safeOpaque((_h = (_g = part.callId) !== null && _g !== void 0 ? _g : part.toolCallId) !== null && _h !== void 0 ? _h : part.id, 512)) !== null && _j !== void 0 ? _j : "line-".concat(line, "-call-").concat(index);
                mapToolResult(events, file, line, timestamp, state, part, callId, index);
            }
        }
        if (emit)
            mapPiUsage(events, file, line, timestamp, state, isRecord(message.usage) ? message.usage : undefined);
        return;
    }
    if (['toolresult', 'tool_result', 'tool'].includes(role) && emit) {
        var callId = (_l = safeOpaque((_k = message.toolCallId) !== null && _k !== void 0 ? _k : message.callId, 512)) !== null && _l !== void 0 ? _l : "line-".concat(line);
        mapToolResult(events, file, line, timestamp, state, message, callId);
    }
    else if ((role === 'bashexecution' || role === 'bash_execution') && emit) {
        mapBashExecution(events, file, line, timestamp, state, message);
    }
    else if (role === 'error' && emit) {
        mapPiError(events, file, line, timestamp, state, message);
    }
}
function makeCallState(line, index, value) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m, _o, _p;
    var nested = firstRecord(value.toolCall, value.functionCall);
    var id = (_e = safeOpaque((_d = (_c = (_b = (_a = value.callId) !== null && _a !== void 0 ? _a : value.toolCallId) !== null && _b !== void 0 ? _b : nested.id) !== null && _c !== void 0 ? _c : nested.callId) !== null && _d !== void 0 ? _d : value.id, 512)) !== null && _e !== void 0 ? _e : "line-".concat(line, "-call-").concat(index);
    var name = (_j = safeLabel((_h = (_g = (_f = value.name) !== null && _f !== void 0 ? _f : value.toolName) !== null && _g !== void 0 ? _g : nested.name) !== null && _h !== void 0 ? _h : nested.toolName, 128)) !== null && _j !== void 0 ? _j : 'unknown';
    var args = parseArgs((_p = (_o = (_m = (_l = (_k = value.args) !== null && _k !== void 0 ? _k : value.input) !== null && _l !== void 0 ? _l : value.arguments) !== null && _m !== void 0 ? _m : nested.args) !== null && _o !== void 0 ? _o : nested.input) !== null && _p !== void 0 ? _p : nested.arguments);
    return { id: id, name: name, args: args };
}
function rememberCall(calls, call) {
    if (calls.size >= maxCallCorrelations && !calls.has(call.id))
        calls.delete(calls.keys().next().value);
    calls.set(call.id, call);
}
function mapToolResult(events, file, line, timestamp, state, data, callId, partIndex) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m;
    var call = (_a = state.calls.get(callId)) !== null && _a !== void 0 ? _a : {
        id: callId, name: (_c = safeLabel((_b = data.toolName) !== null && _b !== void 0 ? _b : data.name, 128)) !== null && _c !== void 0 ? _c : 'unknown',
        args: parseArgs((_e = (_d = data.args) !== null && _d !== void 0 ? _d : data.input) !== null && _e !== void 0 ? _e : data.arguments),
    };
    var outputValue = (_h = (_g = (_f = data.output) !== null && _f !== void 0 ? _f : data.result) !== null && _g !== void 0 ? _g : data.content) !== null && _h !== void 0 ? _h : (isRecord(data.toolResult) ? data.toolResult.output : undefined);
    var output = extractText(outputValue);
    var failed = data.isError === true || data.is_error === true || isErrorStatus(data.status) ||
        (isRecord(data.details) && isErrorStatus(data.details.status));
    var pathValue = isRecord(call.args) ? boundedString((_k = (_j = call.args.path) !== null && _j !== void 0 ? _j : call.args.file_path) !== null && _k !== void 0 ? _k : call.args.target_path, 4096) : undefined;
    var lower = call.name.toLowerCase();
    var action = 'tool.completed';
    var category = 'tool';
    var severity = 'info';
    var message = 'Pi tool result observed';
    if (lower === 'bash' || commandFor(call.name, call.args)) {
        action = 'command.executed';
        category = 'command';
        message = 'Pi command executed';
    }
    else if (pathValue && lower === 'read') {
        action = 'file.read';
        category = 'file';
        message = 'Pi file read';
    }
    else if (pathValue && lower === 'write') {
        action = 'file.created';
        category = 'file';
        message = 'Pi file created';
    }
    else if (pathValue && lower === 'edit') {
        action = 'file.modified';
        category = 'file';
        message = 'Pi file modified';
    }
    else if (isMcpTool(call.name)) {
        action = 'mcp.tool_invoked';
        category = 'mcp';
        message = 'Pi MCP tool invoked';
    }
    else if (failed) {
        action = 'tool.failed';
        category = 'tool';
        severity = 'high';
        message = 'Pi tool failed';
    }
    var event = makeToolEvent(file, line, "tool_result".concat(partIndex === undefined ? '' : ".".concat(partIndex)), action, category, message, state, timestamp, call, severity);
    if (failed)
        event.error = { type: 'tool_error' };
    if (output)
        event.content = contentDigest(output);
    var args = isRecord(call.args) ? call.args : {};
    if (category === 'command') {
        var command = commandFor(call.name, args);
        var commandInfo = {};
        if (command) {
            commandInfo.command_sha256 = sha256(command);
            commandInfo.command_bytes = Buffer.byteLength(command, 'utf8');
        }
        if (output) {
            commandInfo.output_sha256 = sha256(output);
            commandInfo.output_bytes = Buffer.byteLength(output, 'utf8');
        }
        event.command = commandInfo;
    }
    if (category === 'file') {
        var detail = isRecord(data.details) ? data.details : {};
        var diff = boundedString((_l = detail.patch) !== null && _l !== void 0 ? _l : detail.diff, maxLineBytes);
        var fileInfo = __assign(__assign({}, (pathValue ? { path_sha256: sha256(pathValue) } : {})), { operation: fileOperation(call.name) });
        if (diff) {
            fileInfo.diff_sha256 = sha256(diff);
            fileInfo.diff_bytes = Buffer.byteLength(diff, 'utf8');
            event.content = contentDigest(diff);
        }
        event.file = fileInfo;
    }
    var raw = __assign({}, (data.status === undefined ? {} : { status: (_m = safeLabel(data.status, 32)) !== null && _m !== void 0 ? _m : 'unknown' }));
    if (typeof data.output === 'string')
        raw.output_bytes = Buffer.byteLength(data.output, 'utf8');
    if (typeof data.truncated === 'boolean')
        raw.truncated = data.truncated;
    if (safeOpaque(callId, 512))
        raw.tool_call_sha256 = sha256(callId);
    event.raw = __assign(__assign({}, event.raw), { fx: raw });
    if (category === 'mcp')
        event.mcp = piMcpFields(call.name);
    pushPiEvent(events, event);
    state.calls.delete(callId);
}
function mapBashExecution(events, file, line, timestamp, state, data) {
    var command = boundedString(data.command, 1 << 20);
    if (!command)
        return;
    var output = extractText(data.output);
    var call = { id: "line-".concat(line, "-bash"), name: 'bash', args: { command: command } };
    var event = makeToolEvent(file, line, 'bash_execution', 'command.executed', 'command', 'Pi bash command executed', state, timestamp, call);
    var commandInfo = { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') };
    if (output) {
        commandInfo.output_sha256 = sha256(output);
        commandInfo.output_bytes = Buffer.byteLength(output, 'utf8');
        event.content = contentDigest(output);
    }
    if (nonNegativeInteger(data.exitCode) !== undefined)
        commandInfo.exit_code = data.exitCode;
    event.command = commandInfo;
    if (data.cancelled === true || (typeof data.exitCode === 'number' && data.exitCode !== 0))
        event.error = { type: 'command_failed' };
    event.raw = __assign(__assign({}, event.raw), { fx: __assign({}, (typeof data.cancelled === 'boolean' ? { cancelled: data.cancelled } : {})) });
    pushPiEvent(events, event);
}
function mapPiError(events, file, line, timestamp, state, data) {
    var text = firstText(data.errorMessage, data.message, data.content) || 'Pi runtime error observed';
    var event = makePiEvent(file, line, 'error', 'agent.error', 'session', 'Pi runtime error observed', state, timestamp, {
        severity: 'high', content: contentDigest(text), fx: { error_sha256: sha256(text), error_bytes: Buffer.byteLength(text, 'utf8') },
    });
    event.error = { type: 'runtime_error' };
    pushPiEvent(events, event);
}
function mapPiUsage(events, file, line, timestamp, state, value) {
    if (!value)
        return;
    var usage = {};
    var input = nonNegativeInteger(value.input);
    var output = nonNegativeInteger(value.output);
    var cacheRead = nonNegativeInteger(value.cacheRead);
    var cacheWrite = nonNegativeInteger(value.cacheWrite);
    var reasoning = nonNegativeInteger(value.reasoning);
    var cost = isRecord(value.cost) ? finiteNonNegative(value.cost.total) : undefined;
    if (input !== undefined)
        usage.input_tokens = input;
    if (output !== undefined)
        usage.output_tokens = output;
    if (cacheRead !== undefined)
        usage.cache_read_input_tokens = cacheRead;
    if (cacheWrite !== undefined)
        usage.cache_creation_input_tokens = cacheWrite;
    if (reasoning !== undefined)
        usage.reasoning_tokens = reasoning;
    if (cost !== undefined)
        usage.cost_usd = cost;
    if (Object.keys(usage).length === 0)
        return;
    var event = makePiEvent(file, line, 'usage', 'token.usage', 'metric', 'Pi token usage recovered from local transcript', state, timestamp);
    event.gen_ai = { usage: usage };
    pushPiEvent(events, event);
}
function makeTextEvent(file, line, suffix, action, category, message, state, timestamp, text, includeContent, role) {
    var allowed = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
    var event = makePiEvent(file, line, suffix, action, category, message, state, timestamp, { content: contentDigest(text, allowed, includeContent) });
    if (allowed) {
        if (role === 'user') {
            event.prompt = { text: text };
            event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } };
        }
        else {
            event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } });
        }
    }
    return event;
}
function makeToolEvent(file, line, suffix, action, category, message, state, timestamp, call, severity) {
    var _a, _b, _c;
    if (severity === void 0) { severity = 'info'; }
    var name = (_a = safeLabel(call.name, 128)) !== null && _a !== void 0 ? _a : 'unknown';
    var callIdHash = sha256(call.id);
    var argsDigest = digestUnknown(call.args);
    var event = makePiEvent(file, line, suffix, action, category, message, state, timestamp, __assign({ severity: severity }, (argsDigest ? { content: argsDigest } : {})));
    event.tool = { name: name };
    event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { operation: { name: 'execute_tool' }, tool: { name: name, call: __assign({ id: "pi-call:".concat(callIdHash) }, (argsDigest ? { arguments: argsDigest } : {})) } });
    var args = isRecord(call.args) ? call.args : {};
    var command = commandFor(name, args);
    var filePath = boundedString((_c = (_b = args.path) !== null && _b !== void 0 ? _b : args.file_path) !== null && _c !== void 0 ? _c : args.target_path, 4096);
    if (command)
        event.tool = { name: name, command_sha256: sha256(command) };
    if (filePath)
        event.tool = __assign(__assign({}, (isRecord(event.tool) ? event.tool : { name: name })), { path_sha256: sha256(filePath) });
    if (category === 'mcp')
        event.mcp = piMcpFields(name);
    event.raw = __assign(__assign({}, event.raw), { fx: __assign({ tool_call_sha256: callIdHash }, (argsDigest ? { arguments: argsDigest } : {})) });
    return event;
}
function makePiEvent(file, line, suffix, action, category, message, state, timestamp, extra) {
    var _a;
    if (extra === void 0) { extra = {}; }
    var event = {
        timestamp: timestamp,
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(file.sourceSha256, ":").concat(state.sessionIdHash, ":").concat(line, ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_a = extra.severity) !== null && _a !== void 0 ? _a : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'pi_cli', collection_method: 'poll' }, session: { id: "pi:".concat(state.sessionIdHash) },
        raw: __assign(__assign(__assign(__assign({ source: 'pi_session_jsonl', source_sha256: file.sourceSha256, line: line, message: message }, (state.cwd ? { cwd_sha256: sha256(state.cwd) } : {})), (state.repository ? { repository_sha256: sha256(state.repository) } : {})), (state.branch ? { branch_sha256: sha256(state.branch) } : {})), (extra.fx ? { fx: extra.fx } : {})),
    };
    if (state.model)
        event.model = state.model;
    if (extra.content)
        event.content = extra.content;
    return event;
}
function piMcpFields(name) {
    var parsed = piMcpTool(name);
    return __assign(__assign({}, (parsed.server ? { server: parsed.server } : {})), (parsed.tool ? { tool: parsed.tool } : {}));
}
function piMcpTool(name) {
    var rest = name.trim().startsWith('mcp__') ? name.trim().slice('mcp__'.length) : '';
    if (!rest)
        return {};
    var parts = rest.includes('__') ? rest.split('__') : rest.split('_');
    var server = safeLabel(parts.shift(), 128);
    var tool = safeLabel(parts.join(rest.includes('__') ? '__' : '_'), 128);
    return __assign(__assign({}, (server ? { server: server } : {})), (tool ? { tool: tool } : {}));
}
function isMcpTool(name) { return name.trim().startsWith('mcp__'); }
function commandFor(name, args) {
    var _a, _b, _c;
    if (name.toLowerCase() === 'bash')
        return (_a = boundedString(args.command, 1 << 20)) !== null && _a !== void 0 ? _a : '';
    return (_c = boundedString((_b = args.command) !== null && _b !== void 0 ? _b : args.cmd, 1 << 20)) !== null && _c !== void 0 ? _c : '';
}
function fileOperation(name) {
    if (name.toLowerCase() === 'read')
        return 'read';
    if (name.toLowerCase() === 'write')
        return 'create';
    if (name.toLowerCase() === 'edit')
        return 'modify';
    return undefined;
}
function textParts(value) {
    if (typeof value === 'string')
        return value.trim() ? [value] : [];
    if (!Array.isArray(value)) {
        if (!isRecord(value))
            return [];
        var text = firstText(value.text, value.content, value.output);
        return text.trim() ? [text] : [];
    }
    var result = [];
    for (var _i = 0, value_1 = value; _i < value_1.length; _i++) {
        var item = value_1[_i];
        var texts = textParts(item);
        result.push.apply(result, texts);
    }
    return result;
}
function extractText(value) {
    var _a;
    if (value === undefined || value === null)
        return '';
    var texts = textParts(value);
    if (texts.length)
        return texts.join('\n');
    try {
        return (_a = JSON.stringify(value)) !== null && _a !== void 0 ? _a : '';
    }
    catch (_b) {
        return '';
    }
}
function parseArgs(value) {
    if (isRecord(value))
        return value;
    if (typeof value === 'string' && value.trim()) {
        try {
            return JSON.parse(value);
        }
        catch (_a) {
            return value;
        }
    }
    if (value === undefined || value === null)
        return {};
    return value;
}
function contentDigest(value, included, requested) {
    if (included === void 0) { included = false; }
    if (requested === void 0) { requested = false; }
    var digest = digestUnknown(value);
    return __assign(__assign({ retention: included ? 'full' : 'metadata', included: included }, (digest !== null && digest !== void 0 ? digest : {})), (requested && !included ? { truncated: true } : {}));
}
function digestUnknown(value) {
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
function processFiles(files, root, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var totalBytes, _loop_1, _i, files_1, file, state_1;
        var _this = this;
        var _a, _b, _c, _d;
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    totalBytes = 0;
                    _loop_1 = function (file) {
                        var previous, bytes, parsed_1, _f, groups, mapperState, pendingEventCount, _g, groups_1, group, _h, _j, event_4, pendingGroups_1, pendingEvents_1, flush, _k, groups_2, group, _l, finalPrefix, current, nextCursor, changed, _m;
                        return __generator(this, function (_o) {
                            switch (_o.label) {
                                case 0:
                                    previous = state.files[file.sourceSha256];
                                    _o.label = 1;
                                case 1:
                                    _o.trys.push([1, 15, , 16]);
                                    return [4 /*yield*/, readPiFile(file, root)];
                                case 2:
                                    bytes = _o.sent();
                                    totalBytes += bytes.byteLength;
                                    if (totalBytes > maxTotalBytes) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi sync exceeded the aggregate source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    parsed_1 = parsePiFile(bytes);
                                    result.malformed_lines += parsed_1.malformed;
                                    if (parsed_1.partialTail)
                                        result.partial_files++;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript has a malformed row; scanning stops at the safe prefix' });
                                    if (parsed_1.partialTail)
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript ends in an incomplete row; waiting for the next sweep' });
                                    if (previous && (parsed_1.completeLines < previous.last_line ||
                                        parsed_1.prefixHashes.get(previous.last_line) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    _f = mapPiEntries(file, parsed_1, (_a = previous === null || previous === void 0 ? void 0 : previous.last_line) !== null && _a !== void 0 ? _a : 0, (_b = previous === null || previous === void 0 ? void 0 : previous.started) !== null && _b !== void 0 ? _b : false, (_c = options.includeContent) !== null && _c !== void 0 ? _c : false), groups = _f.groups, mapperState = _f.state;
                                    pendingEventCount = groups.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    if (pendingEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += pendingEventCount;
                                        (_d = result.events) !== null && _d !== void 0 ? _d : (result.events = []);
                                        for (_g = 0, groups_1 = groups; _g < groups_1.length; _g++) {
                                            group = groups_1[_g];
                                            for (_h = 0, _j = group.events; _h < _j.length; _h++) {
                                                event_4 = _j[_h];
                                                if (result.events.length < maxPreviewEvents)
                                                    result.events.push(event_4);
                                                else
                                                    result.preview_truncated = true;
                                            }
                                        }
                                        return [2 /*return*/, "continue"];
                                    }
                                    pendingGroups_1 = [];
                                    pendingEvents_1 = [];
                                    flush = function () { return __awaiter(_this, void 0, void 0, function () {
                                        var last, nextCursor, current, changed;
                                        var _a;
                                        return __generator(this, function (_b) {
                                            switch (_b.label) {
                                                case 0:
                                                    if (pendingGroups_1.length === 0)
                                                        return [2 /*return*/];
                                                    if (!pendingEvents_1.length) return [3 /*break*/, 2];
                                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(pendingEvents_1, { endpoint: (_a = options.endpoint) !== null && _a !== void 0 ? _a : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                                case 1:
                                                    _b.sent();
                                                    result.events_emitted += pendingEvents_1.length;
                                                    _b.label = 2;
                                                case 2:
                                                    last = pendingGroups_1[pendingGroups_1.length - 1];
                                                    nextCursor = makeCursor(last.line, last.prefix, last.started, file, parsed_1.partialTail || parsed_1.malformed > 0);
                                                    current = state.files[file.sourceSha256];
                                                    changed = !current || current.last_line !== nextCursor.last_line || current.started !== nextCursor.started ||
                                                        current.prefix_sha256 !== nextCursor.prefix_sha256 || current.size_bytes !== nextCursor.size_bytes ||
                                                        current.mtime_ms !== nextCursor.mtime_ms || current.incomplete !== nextCursor.incomplete;
                                                    state.files[file.sourceSha256] = nextCursor;
                                                    if (!changed) return [3 /*break*/, 4];
                                                    result.files_changed++;
                                                    if (!(persist && statePath)) return [3 /*break*/, 4];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 3:
                                                    _b.sent();
                                                    _b.label = 4;
                                                case 4:
                                                    pendingGroups_1 = [];
                                                    pendingEvents_1 = [];
                                                    return [2 /*return*/];
                                            }
                                        });
                                    }); };
                                    _o.label = 3;
                                case 3:
                                    _o.trys.push([3, 11, , 12]);
                                    _k = 0, groups_2 = groups;
                                    _o.label = 4;
                                case 4:
                                    if (!(_k < groups_2.length)) return [3 /*break*/, 9];
                                    group = groups_2[_k];
                                    if (!(pendingEvents_1.length > 0 && pendingEvents_1.length + group.events.length > eventBatchSize)) return [3 /*break*/, 6];
                                    return [4 /*yield*/, flush()];
                                case 5:
                                    _o.sent();
                                    _o.label = 6;
                                case 6:
                                    pendingGroups_1.push(group);
                                    pendingEvents_1.push.apply(pendingEvents_1, group.events);
                                    if (!(pendingEvents_1.length >= eventBatchSize)) return [3 /*break*/, 8];
                                    return [4 /*yield*/, flush()];
                                case 7:
                                    _o.sent();
                                    _o.label = 8;
                                case 8:
                                    _k++;
                                    return [3 /*break*/, 4];
                                case 9: return [4 /*yield*/, flush()];
                                case 10:
                                    _o.sent();
                                    return [3 /*break*/, 12];
                                case 11:
                                    _l = _o.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected Pi history events' });
                                    return [2 /*return*/, "continue"];
                                case 12:
                                    finalPrefix = parsed_1.prefixHashes.get(parsed_1.completeLines);
                                    if (!finalPrefix) return [3 /*break*/, 14];
                                    current = state.files[file.sourceSha256];
                                    nextCursor = makeCursor(parsed_1.completeLines, finalPrefix, mapperState.started, file, parsed_1.partialTail || parsed_1.malformed > 0);
                                    changed = !current || current.last_line !== nextCursor.last_line || current.started !== nextCursor.started ||
                                        current.prefix_sha256 !== nextCursor.prefix_sha256 || current.size_bytes !== nextCursor.size_bytes ||
                                        current.mtime_ms !== nextCursor.mtime_ms || current.incomplete !== nextCursor.incomplete;
                                    state.files[file.sourceSha256] = nextCursor;
                                    if (!changed) return [3 /*break*/, 14];
                                    result.files_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 14];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 13:
                                    _o.sent();
                                    _o.label = 14;
                                case 14: return [3 /*break*/, 16];
                                case 15:
                                    _m = _o.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'Pi transcript could not be read or checkpointed safely' });
                                    return [3 /*break*/, 16];
                                case 16: return [2 /*return*/];
                            }
                        });
                    };
                    _i = 0, files_1 = files;
                    _e.label = 1;
                case 1:
                    if (!(_i < files_1.length)) return [3 /*break*/, 4];
                    file = files_1[_i];
                    return [5 /*yield**/, _loop_1(file)];
                case 2:
                    state_1 = _e.sent();
                    if (state_1 === "break")
                        return [3 /*break*/, 4];
                    _e.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function makeCursor(line, prefix, started, file, incomplete) {
    return {
        last_line: line,
        started: started,
        prefix_sha256: prefix, size_bytes: file.size, mtime_ms: file.mtimeMs,
        incomplete: incomplete,
    };
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
    return node_path_1.default.join(root, 'state', cursorName);
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
                        throw new Error('Pi history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Pi history cursor');
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
                    empty = { schema_version: 'threadforge.pi-session-cursor.v1', version: cursorVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes)
                        throw new Error('Pi history cursor must be a bounded regular file');
                    _d.label = 3;
                case 3:
                    _d.trys.push([3, 7, 8, 10]);
                    return [4 /*yield*/, (0, promises_1.open)(statePath, node_fs_1.constants.O_RDONLY | ((_c = node_fs_1.constants.O_NOFOLLOW) !== null && _c !== void 0 ? _c : 0))];
                case 4:
                    handle = _d.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _d.sent();
                    if (!sameFile(info, opened))
                        throw new Error('Pi cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxCursorBytes)
                        throw new Error('Pi cursor exceeds local size limit');
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.files)) {
                        throw new Error('Pi history cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.last_line) ||
                            typeof value.started !== 'boolean' || typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
                            typeof value.incomplete !== 'boolean')
                            throw new Error('Pi history cursor contains an invalid checkpoint');
                        files[key] = { last_line: value.last_line, started: value.started, prefix_sha256: value.prefix_sha256,
                            size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, incomplete: value.incomplete };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Pi history cursor'))
                        throw error_3;
                    throw new Error('Pi history cursor is malformed or unreadable');
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
                            throw new Error('unable to create private Pi cursor directory'); })];
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
                        throw new Error('Pi cursor path cannot contain symbolic links or non-directory parents');
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
        var bytes, temporary, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from("".concat(JSON.stringify(state, null, 2), "\n"), 'utf8');
                    if (bytes.byteLength > maxCursorBytes)
                        throw new Error('Pi history cursor exceeds the local size limit');
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
                    return [4 /*yield*/, handle.writeFile(bytes)];
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
                    throw new Error('unable to atomically write Pi history cursor');
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
function nonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function finiteNonNegative(value) { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined; }
function isNonNegativeInteger(value) { return nonNegativeInteger(value) !== undefined; }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value, maximum) { return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined; }
function safeOpaque(value, maximum) {
    var text = boundedString(value, maximum);
    return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
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
function typeOf(value) { return typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''; }
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
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
function firstText() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_2 = values; _a < values_2.length; _a++) {
        var value = values_2[_a];
        if (typeof value === 'string' && value.length)
            return value;
        if (isRecord(value)) {
            for (var _b = 0, _c = ['text', 'content', 'output']; _b < _c.length; _b++) {
                var key = _c[_b];
                if (typeof value[key] === 'string')
                    return value[key];
            }
        }
    }
    return '';
}
function textValue(value) { return typeof value === 'string' ? value : ''; }
function isErrorStatus(value) { return typeof value === 'string' && ['error', 'failed', 'failure'].includes(value.trim().toLowerCase()); }
function timestampValue(value, fallback) {
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
        var date = new Date(value < 1000000000000 ? value * 1000 : value);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    if (typeof value === 'string') {
        var number = Number(value);
        if (value.trim() !== '' && Number.isFinite(number) && number >= 0)
            return timestampValue(number, fallback);
        var date = new Date(value);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    return timestampValue(fallback, 0);
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
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function isSafeRelative(value) { return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
