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
exports.getFxHistoryStatus = getFxHistoryStatus;
exports.syncFxHistory = syncFxHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var cursorVersion = 1;
var cursorName = 'fx-session-cursor.v1.json';
var maxCursorBytes = 8 << 20;
var defaultMaxSessions = 1000;
var maxSessions = 5000;
var maxManifestBytes = 1 << 20;
var maxLogBytes = 64 << 20;
var maxTotalBytes = 512 << 20;
var maxFrameBytes = 8 << 20;
var maxLinesPerLog = 100000;
var maxEventsPerLog = 10000;
var maxEventsPerSync = 20000;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
var eventBatchSize = 100;
function getFxHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, sessionsDir, statePath, listed, state, checkpointed, unreadableManifests, _i, _a, file, cursor, manifest, _b;
        var _c;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    maximum = boundedInteger((_c = options.maxSessions) !== null && _c !== void 0 ? _c : defaultMaxSessions, 1, maxSessions, '--max-sessions');
                    return [4 /*yield*/, resolveSessionsDir(options.sessionsDir)];
                case 1:
                    sessionsDir = _d.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(sessionsDir, statePath))
                        throw new Error('fx history cursor must be stored outside fx sessions');
                    return [4 /*yield*/, listSessions(sessionsDir, maximum)];
                case 2:
                    listed = _d.sent();
                    return [4 /*yield*/, readState(statePath)];
                case 3:
                    state = _d.sent();
                    checkpointed = 0;
                    unreadableManifests = 0;
                    _i = 0, _a = listed.files;
                    _d.label = 4;
                case 4:
                    if (!(_i < _a.length)) return [3 /*break*/, 10];
                    file = _a[_i];
                    cursor = state.sessions[file.sourceSha256];
                    manifest = void 0;
                    _d.label = 5;
                case 5:
                    _d.trys.push([5, 7, , 8]);
                    return [4 /*yield*/, readManifest(file)];
                case 6:
                    manifest = _d.sent();
                    return [3 /*break*/, 8];
                case 7:
                    _b = _d.sent();
                    unreadableManifests++;
                    return [3 /*break*/, 9];
                case 8:
                    if (!cursor || cursor.incomplete || cursor.size_bytes !== file.size || cursor.mtime_ms !== file.mtimeMs)
                        return [3 /*break*/, 9];
                    if (manifest) {
                        if (cursor.generation_sha256 === sha256(manifest.log_generation) && cursor.last_seq >= manifest.last_event_seq &&
                            cursor.updated_at_ms === manifest.updated_at_ms)
                            checkpointed++;
                    }
                    else if (cursor.generation_sha256 && cursor.last_seq > 0)
                        checkpointed++;
                    _d.label = 9;
                case 9:
                    _i++;
                    return [3 /*break*/, 4];
                case 10: return [2 /*return*/, {
                        schema_version: 'threadforge.fx-history-status.v1', sessions: listed.files.length,
                        checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
                        unreadable_manifests: unreadableManifests, symlinks_skipped: listed.symlinksSkipped,
                        oversized_files_skipped: listed.oversizedSkipped, sessions_truncated: listed.truncated,
                    }];
            }
        });
    });
}
function syncFxHistory() {
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
                        throw new Error('fx history cursor must be stored outside fx sessions');
                    return [4 /*yield*/, listSessions(sessionsDir, (_c = options.maxSessions) !== null && _c !== void 0 ? _c : defaultMaxSessions)];
                case 2:
                    listed = _e.sent();
                    result = emptyResult((_d = options.includeContent) !== null && _d !== void 0 ? _d : false);
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.sessions_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    _a = processFxFiles;
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
                                    return [4 /*yield*/, processFxFiles(listed.files, sessionsDir, state, options, result, true, statePath)];
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
        schema_version: 'threadforge.fx-history-sync.v1', sessions: 0, sessions_changed: 0,
        events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0, skipped_records: 0,
        symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false, preview_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : defaultMaxSessions, 1, maxSessions, '--max-sessions');
    if (options.sessionsDir !== undefined && !node_path_1.default.isAbsolute(options.sessionsDir))
        throw new Error('--sessions-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveSessionsDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var directory, info, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    directory = value !== null && value !== void 0 ? value : node_path_1.default.join(node_os_1.default.homedir(), '.fx', 'sessions');
                    if (!node_path_1.default.isAbsolute(directory))
                        throw new Error('--sessions-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(directory).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(directory)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('fx sessions path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(directory).catch(function () { throw new Error('fx sessions directory could not be resolved'); })];
                case 2:
                    canonical = _a.sent();
                    if (canonical !== node_path_1.default.resolve(directory))
                        throw new Error('fx sessions directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listSessions(root, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var info, files, symlinksSkipped, oversizedSkipped, truncated, entries, _i, entries_1, entry, dir, dirInfo, logPath, logInfo, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('fx sessions path must be a real directory');
                    files = [];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    return [4 /*yield*/, (0, promises_1.readdir)(root, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate fx session directories'); })];
                case 2:
                    entries = _a.sent();
                    _i = 0, entries_1 = entries;
                    _a.label = 3;
                case 3:
                    if (!(_i < entries_1.length)) return [3 /*break*/, 8];
                    entry = entries_1[_i];
                    if (!entry.isDirectory() && !entry.isSymbolicLink())
                        return [3 /*break*/, 7];
                    if (!validSessionId(entry.name))
                        return [3 /*break*/, 7];
                    dir = node_path_1.default.join(root, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(dir).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 4:
                    dirInfo = _a.sent();
                    if (!dirInfo)
                        return [3 /*break*/, 7];
                    if (dirInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    if (!dirInfo.isDirectory())
                        return [3 /*break*/, 7];
                    logPath = node_path_1.default.join(dir, 'events.jsonl');
                    return [4 /*yield*/, (0, promises_1.lstat)(logPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    logInfo = _a.sent();
                    if (!logInfo)
                        return [3 /*break*/, 7];
                    if (logInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    if (!logInfo.isFile())
                        return [3 /*break*/, 7];
                    if (logInfo.size > maxLogBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 7];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(logPath).catch(function () { return ''; })];
                case 6:
                    canonical = _a.sent();
                    if (!canonical || canonical !== node_path_1.default.resolve(logPath) || !isWithin(root, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    files.push({
                        id: entry.name,
                        dir: dir,
                        logPath: canonical, manifestPath: node_path_1.default.join(dir, 'session.json'),
                        sourceSha256: sha256(entry.name), size: logInfo.size, mtimeMs: logInfo.mtimeMs,
                    });
                    if (files.length > maximum) {
                        truncated = true;
                        return [3 /*break*/, 8];
                    }
                    _a.label = 7;
                case 7:
                    _i++;
                    return [3 /*break*/, 3];
                case 8:
                    files.sort(function (left, right) { return left.mtimeMs - right.mtimeMs || (left.id < right.id ? -1 : left.id > right.id ? 1 : 0); });
                    if (truncated)
                        files.length = maximum;
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function readManifest(file) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, value;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, readBoundedRegularFile(file.manifestPath, file.dir, maxManifestBytes, 'fx manifest', true)];
                case 1:
                    bytes = _a.sent();
                    if (!bytes)
                        return [2 /*return*/, undefined];
                    try {
                        value = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    }
                    catch (_b) {
                        throw new Error('fx manifest is malformed');
                    }
                    if (!isRecord(value) || value.schema_version !== 3 || value.storage_format !== 'event_log_v1' ||
                        !validSessionId(value.id) || value.id !== file.id || !safeGeneration(value.log_generation) ||
                        !nonNegativeInteger(value.last_event_seq) || !nonNegativeInteger(value.event_log_bytes) ||
                        !nonNegativeInteger(value.updated_at_ms) || (value.preferences !== undefined && !isRecord(value.preferences))) {
                        throw new Error('fx manifest has an unsupported or invalid schema');
                    }
                    return [2 /*return*/, value];
            }
        });
    });
}
function readBoundedRegularFile(filePath_1, root_1, maximum_1, label_1) {
    return __awaiter(this, arguments, void 0, function (filePath, root, maximum, label, allowMissing) {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, error_1;
        var _a;
        if (allowMissing === void 0) { allowMissing = false; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) { return isCode(error, 'ENOENT') && allowMissing ? null : Promise.reject(error); })];
                case 1:
                    before = _b.sent();
                    if (!before)
                        return [2 /*return*/, undefined];
                    if (!before.isFile() || before.isSymbolicLink() || before.size > maximum)
                        throw new Error("".concat(label, " must be a bounded regular file"));
                    return [4 /*yield*/, (0, promises_1.realpath)(filePath).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || canonical !== node_path_1.default.resolve(filePath) || !isWithin(root, canonical))
                        throw new Error("".concat(label, " escaped its source directory"));
                    _b.label = 3;
                case 3:
                    _b.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 4:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _b.sent();
                    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs)
                        throw new Error("".concat(label, " changed before reading"));
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 7:
                    afterHandle = _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function () { return null; })];
                case 8:
                    afterPath = _b.sent();
                    if (bytes.byteLength > maximum || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
                        opened.size !== afterHandle.size || opened.mtimeMs !== afterHandle.mtimeMs)
                        throw new Error("".concat(label, " changed during read"));
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith(label))
                        throw error_1;
                    throw new Error("".concat(label, " could not be opened safely"));
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
function validSessionId(value) {
    return typeof value === 'string' && value.length >= 1 && value.length <= 255 && value !== '.' && value !== '..' && /^[A-Za-z0-9_.-]+$/.test(value);
}
function safeGeneration(value) {
    return typeof value === 'string' && value.length > 0 && value.length <= 128 && /^[A-Za-z0-9_.-]+$/.test(value);
}
function isSafeRelative(value) { return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function parseFxLog(bytes, committedBytes, expectedGeneration) {
    var limit = committedBytes === undefined ? bytes.length : Math.min(committedBytes, bytes.length);
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var groups = [];
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var offset = 0;
    var malformed = 0;
    var skipped = 0;
    var partialTail = false;
    var previousSeq = 0;
    var generation = expectedGeneration;
    var lineCount = 0;
    while (offset < limit) {
        var newline = bytes.indexOf(0x0a, offset);
        if (newline < 0 || newline >= limit) {
            partialTail = limit > offset;
            break;
        }
        lineCount++;
        if (lineCount > maxLinesPerLog) {
            malformed++;
            break;
        }
        var lineStart = offset;
        var end = newline > offset && bytes[newline - 1] === 0x0d ? newline - 1 : newline;
        var raw = bytes.subarray(offset, end);
        offset = newline + 1;
        if (raw.byteLength > maxFrameBytes) {
            malformed++;
            break;
        }
        if (raw.toString('utf8').trim() === '') {
            prefixHasher.update(bytes.subarray(lineStart, newline + 1));
            continue;
        }
        var decoded = void 0;
        try {
            decoded = JSON.parse(decoder.decode(raw));
        }
        catch (_a) {
            malformed++;
            break;
        }
        if (!isRecord(decoded) || decoded.schema_version !== 1 || !safeGeneration(decoded.log_generation) ||
            !nonNegativeInteger(decoded.seq) || decoded.seq < 1 || !safeRecordKind(decoded.kind)) {
            malformed++;
            break;
        }
        var recordGeneration = decoded.log_generation;
        if (generation === undefined)
            generation = recordGeneration;
        if (recordGeneration !== generation || (expectedGeneration !== undefined && recordGeneration !== expectedGeneration) || decoded.seq <= previousSeq) {
            malformed++;
            break;
        }
        var kind = decoded.kind;
        if (knownMappedKinds.has(kind) && !isRecord(decoded.payload)) {
            malformed++;
            break;
        }
        prefixHasher.update(bytes.subarray(lineStart, newline + 1));
        var prefix = prefixHasher.copy().digest('hex');
        var record = __assign(__assign(__assign({ schema_version: 1, log_generation: recordGeneration, seq: decoded.seq }, (nonNegativeInteger(decoded.timestamp_ms) ? { timestamp_ms: decoded.timestamp_ms } : {})), { kind: kind }), (decoded.payload === undefined ? {} : { payload: decoded.payload }));
        groups.push({ seq: record.seq, record: record, prefix: prefix });
        prefixHashes.set(record.seq, prefix);
        previousSeq = record.seq;
        if (!knownMappedKinds.has(kind))
            skipped++;
    }
    return __assign({ groups: groups, maxSeq: previousSeq, prefixHashes: prefixHashes, partialTail: partialTail, malformed: malformed, skipped: skipped }, (generation ? { generation: generation } : {}));
}
var knownMappedKinds = new Set([
    'session_started', 'preferences_changed', 'workspace_rebound', 'history_turn_committed', 'usage_checkpointed',
]);
var mcpControlTools = new Set(['mcp_search_tools', 'mcp_select_tool', 'mcp_features']);
function safeRecordKind(value) {
    return typeof value === 'string' && value.length > 0 && value.length <= 96 && /^[A-Za-z0-9_]+$/.test(value);
}
function mapFxGroups(file, generation, parsed, minSeq, state, includeContent) {
    var _a, _b, _c;
    var generationHash = sha256(generation);
    var mapped = [];
    var totalEvents = 0;
    for (var _i = 0, _d = parsed.groups; _i < _d.length; _i++) {
        var group = _d[_i];
        var record = group.record;
        var payload = isRecord(record.payload) ? record.payload : {};
        var emit = record.seq > minSeq;
        var events = [];
        switch (record.kind) {
            case 'session_started': {
                var workspace = boundedString(payload.workspace_root, 4096);
                if (workspace)
                    state.workspace = workspace;
                applyPreferences(state, isRecord(payload.preferences) ? payload.preferences : undefined);
                var usage = isRecord(payload.usage) ? payload.usage : undefined;
                if (usage) {
                    state.lastUsage = usage;
                    state.lastInput = (_a = nonNegativeNumber(usage.input_tokens)) !== null && _a !== void 0 ? _a : state.lastInput;
                    state.lastOutput = (_b = nonNegativeNumber(usage.output_tokens)) !== null && _b !== void 0 ? _b : state.lastOutput;
                }
                if (emit && !state.started) {
                    var event_1 = makeFxEvent(file, generationHash, record, 'session.started', 'session', 'fx session start recovered from local session log', 'session.started', state);
                    var createdAt = nonNegativeNumber(payload.created_at_ms);
                    if (createdAt !== undefined)
                        event_1.timestamp = timestampFromMs(createdAt, file.mtimeMs);
                    event_1.raw = __assign(__assign({}, event_1.raw), { fx: __assign({ created_at_ms: createdAt, workspace_sha256: workspace ? sha256(workspace) : undefined, origin_workspace_sha256: boundedString(payload.origin_workspace_root, 4096)
                                ? sha256(String(payload.origin_workspace_root)) : undefined, conversation_language: safeLabel(payload.conversation_language, 32) }, ((_c = safePreferences(payload.preferences)) !== null && _c !== void 0 ? _c : {})) });
                    pushFxEvent(events, event_1);
                }
                state.started = true;
                break;
            }
            case 'preferences_changed': {
                applyPreferences(state, payload);
                if (emit) {
                    var event_2 = makeFxEvent(file, generationHash, record, 'session.context', 'session', 'fx session preferences changed', 'preferences_changed', state);
                    event_2.raw = __assign(__assign({}, event_2.raw), { fx: __assign({ change: 'preferences_changed' }, safePreferences(payload)) });
                    pushFxEvent(events, event_2);
                }
                break;
            }
            case 'workspace_rebound': {
                var previousWorkspace = boundedString(payload.previous_workspace_root, 4096);
                var nextWorkspace = boundedString(payload.workspace_root, 4096);
                if (nextWorkspace)
                    state.workspace = nextWorkspace;
                if (emit) {
                    var event_3 = makeFxEvent(file, generationHash, record, 'session.context', 'session', 'fx session rebound to a new workspace', 'workspace_rebound', state);
                    event_3.raw = __assign(__assign({}, event_3.raw), { fx: __assign(__assign({ change: 'workspace_rebound' }, (previousWorkspace ? { previous_workspace_sha256: sha256(previousWorkspace) } : {})), (nextWorkspace ? { workspace_sha256: sha256(nextWorkspace) } : {})) });
                    pushFxEvent(events, event_3);
                }
                break;
            }
            case 'history_turn_committed': {
                var turn = isRecord(payload.turn) ? payload.turn : {};
                if (emit)
                    mapCommittedTurn(events, file, generationHash, record, payload, turn, state, includeContent);
                updateTurnTotals(payload, state);
                break;
            }
            case 'usage_checkpointed': {
                var current = isRecord(payload.usage) ? payload.usage : undefined;
                if (current && emit) {
                    var event_4 = makeUsageCheckpoint(file, generationHash, record, current, state);
                    if (event_4)
                        pushFxEvent(events, event_4);
                }
                if (current)
                    state.lastUsage = current;
                break;
            }
            default:
                break;
        }
        totalEvents += events.length;
        if (totalEvents > maxEventsPerLog)
            throw new Error("fx session exceeds the ".concat(maxEventsPerLog, "-event processing limit"));
        mapped.push({ seq: group.seq, prefix: group.prefix, events: events, started: state.started });
    }
    return mapped;
}
function applyPreferences(state, preferences) {
    if (!preferences)
        return;
    var model = safeLabel(preferences.model, 160);
    var provider = safeLabel(preferences.provider, 96);
    if (model)
        state.model = model;
    if (provider)
        state.provider = provider;
}
function pushFxEvent(events, event) {
    if (events.length >= eventBatchSize)
        throw new Error("fx record exceeds the ".concat(eventBatchSize, "-event batch limit"));
    events.push(event);
}
function safePreferences(value) {
    if (!isRecord(value))
        return undefined;
    var result = {};
    for (var _i = 0, _a = ['provider', 'model', 'effort']; _i < _a.length; _i++) {
        var key = _a[_i];
        var label = safeLabel(value[key], key === 'model' ? 160 : 96);
        if (label)
            result[key] = label;
    }
    if (typeof value.fast_mode === 'boolean')
        result.fast_mode = value.fast_mode;
    return Object.keys(result).length ? result : undefined;
}
function mapCommittedTurn(events, file, generationHash, record, payload, turn, state, includeContent) {
    var _a, _b, _c;
    var user = isRecord(turn.user) ? turn.user : undefined;
    if (user) {
        var text = durableText(user.text);
        if (text) {
            var summary = isRecord(turn.execution) && isRecord(turn.execution.turn_summary) ? turn.execution.turn_summary : undefined;
            var startedAt = summary ? nonNegativeNumber(summary.started_at_ms) : undefined;
            var prompt_1 = makeTextEvent(file, generationHash, record, 'prompt.submitted', 'prompt', 'fx prompt recovered from committed turn', "turn.prompt", state, text, includeContent, 'user');
            if (startedAt !== undefined)
                prompt_1.timestamp = timestampFromMs(startedAt, file.mtimeMs);
            var images = Array.isArray(user.images) ? user.images.length : 0;
            if (images > 0)
                prompt_1.raw = __assign(__assign({}, prompt_1.raw), { fx: { image_count: Math.min(images, 100) } });
            pushFxEvent(events, prompt_1);
        }
    }
    var execution = isRecord(turn.execution) ? turn.execution : undefined;
    var steps = execution && Array.isArray(execution.tool_steps) ? execution.tool_steps : [];
    for (var stepIndex = 0; stepIndex < Math.min(steps.length, 500); stepIndex++) {
        var step = isRecord(steps[stepIndex]) ? steps[stepIndex] : {};
        var calls = Array.isArray(step.tool_calls) ? step.tool_calls : [];
        var results = Array.isArray(step.tool_results) ? step.tool_results : [];
        var resultById = new Map();
        for (var _i = 0, results_1 = results; _i < results_1.length; _i++) {
            var item = results_1[_i];
            if (!isRecord(item))
                continue;
            var callId = opaqueString(item.tool_call_id, 512);
            if (callId)
                resultById.set(callId, item);
        }
        for (var callIndex = 0; callIndex < Math.min(calls.length, 500); callIndex++) {
            var call = isRecord(calls[callIndex]) ? calls[callIndex] : {};
            var callId = (_a = opaqueString(call.id, 512)) !== null && _a !== void 0 ? _a : "seq-".concat(record.seq, "-step-").concat(stepIndex, "-call-").concat(callIndex);
            var outcome = resultById.get(callId);
            if (outcome)
                mapToolOutcome(events, file, generationHash, record, turn, call, outcome, state, stepIndex, callIndex);
            else
                mapToolWithoutResult(events, file, generationHash, record, call, state, stepIndex, callIndex, turn.kind === 'interrupted' ? 'interrupted' : 'no_result');
        }
    }
    if (turn.kind === 'interrupted' && isRecord(turn.tool_call)) {
        mapToolWithoutResult(events, file, generationHash, record, turn.tool_call, state, -1, 0, 'interrupted');
    }
    var assistant = durableText(turn.assistant);
    if (assistant === null || assistant === void 0 ? void 0 : assistant.trim()) {
        pushFxEvent(events, makeTextEvent(file, generationHash, record, 'agent.message', 'agent', 'fx assistant response recovered from committed turn', 'turn.assistant', state, assistant, includeContent, 'assistant'));
    }
    if (turn.kind === 'compacted_summary') {
        var summary = durableText(turn.summary);
        var event_5 = makeFxEvent(file, generationHash, record, 'session.compacting', 'session', 'fx compacted session history', 'turn.compaction', state);
        event_5.raw = __assign(__assign({}, event_5.raw), { fx: __assign({ removed_turn_count: (_b = nonNegativeInteger(turn.removed_turn_count)) !== null && _b !== void 0 ? _b : 0, compaction_count: (_c = nonNegativeInteger(turn.compaction_count)) !== null && _c !== void 0 ? _c : 0 }, (summary ? { summary: contentDigest(summary) } : {})) });
        pushFxEvent(events, event_5);
    }
    var usageEvent = makeTurnUsage(file, generationHash, record, payload, execution, state);
    if (usageEvent)
        pushFxEvent(events, usageEvent);
}
function makeTextEvent(file, generationHash, record, action, category, message, suffix, state, text, includeContent, role) {
    var allowed = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
    var event = makeFxEvent(file, generationHash, record, action, category, message, suffix, state, {
        content: contentDigest(text, allowed, includeContent),
    });
    if (allowed) {
        if (role === 'user') {
            event.prompt = { text: text };
            event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } });
        }
        else {
            event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } });
        }
    }
    return event;
}
function mapToolWithoutResult(events, file, generationHash, record, call, state, stepIndex, callIndex, reason) {
    var _a;
    var name = (_a = safeLabel(call.name, 128)) !== null && _a !== void 0 ? _a : 'unknown';
    var callId = opaqueString(call.id, 512);
    var args = parseToolArguments(call.arguments_json);
    var argsDigest = digestUnknown(args);
    var event = makeFxEvent(file, generationHash, record, 'tool.invoked', 'tool', 'fx tool call has no recorded result', "turn.".concat(stepIndex, ".").concat(callIndex, ".invoked"), state, { content: argsDigest });
    event.tool = { name: name };
    event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { operation: { name: 'execute_tool' }, tool: { name: name, call: { id: callId ? "fxcall:".concat(sha256(callId)) : undefined, arguments: argsDigest } } });
    event.raw = __assign(__assign({}, event.raw), { fx: __assign({ result_missing: reason }, (callId ? { tool_call_sha256: sha256(callId) } : {})) });
    pushFxEvent(events, event);
}
function mapToolOutcome(events, file, generationHash, record, turn, call, result, state, stepIndex, callIndex) {
    var _a, _b, _c, _d, _e, _f;
    var name = (_b = (_a = safeLabel(result.tool_name, 128)) !== null && _a !== void 0 ? _a : safeLabel(call.name, 128)) !== null && _b !== void 0 ? _b : 'unknown';
    var callId = (_c = opaqueString(result.tool_call_id, 512)) !== null && _c !== void 0 ? _c : opaqueString(call.id, 512);
    var status = result.status === 'failure' ? 'failure' : result.status === 'success' ? 'success' : 'unknown';
    var toolLower = name.toLowerCase();
    var args = parseToolArguments(call.arguments_json);
    var argsDigest = digestUnknown(args);
    var evidence = findFileEvidence(turn, callId);
    var committed = isRecord(result.committed_file_presentation) ? result.committed_file_presentation : undefined;
    var action = 'tool.completed';
    var category = 'tool';
    var severity = 'info';
    var message = 'fx tool completed';
    if (toolLower.startsWith('mcp_') && !mcpControlTools.has(toolLower)) {
        action = 'mcp.tool_invoked';
        category = 'mcp';
        message = 'fx MCP tool invoked';
    }
    else if (toolLower === 'run_command' || toolLower === 'terminal') {
        action = 'command.executed';
        category = 'command';
        message = 'fx command executed';
    }
    else if (committed) {
        action = committed.kind === 'added' ? 'file.created' : 'file.modified';
        category = 'file';
        message = committed.kind === 'added' ? 'fx file created' : 'fx file modified';
    }
    else if (evidence) {
        var fileAction = String((_d = evidence.action) !== null && _d !== void 0 ? _d : '');
        if (['read', 'search', 'list'].includes(fileAction)) {
            action = 'file.read';
            category = 'file';
            message = 'fx file read';
        }
        else if (fileAction === 'write') {
            action = 'file.created';
            category = 'file';
            message = 'fx file created';
        }
        else if (['edit', 'delete', 'rename', 'copy'].includes(fileAction)) {
            action = 'file.modified';
            category = 'file';
            message = 'fx file modified';
        }
    }
    else if (status === 'failure') {
        action = 'tool.failed';
        category = 'tool';
        severity = 'high';
        message = 'fx tool failed';
    }
    var event = makeFxEvent(file, generationHash, record, action, category, message, "turn.".concat(stepIndex, ".").concat(callIndex, ".result"), state, { severity: severity });
    var createdAt = nonNegativeNumber(result.created_at_ms);
    if (createdAt !== undefined)
        event.timestamp = timestampFromMs(createdAt, file.mtimeMs);
    event.tool = { name: name };
    event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { operation: { name: 'execute_tool' }, tool: { name: name, call: __assign(__assign({}, (callId ? { id: "fxcall:".concat(sha256(callId)) } : {})), { arguments: argsDigest }) } });
    if (action === 'mcp.tool_invoked')
        event.mcp = { tool: name };
    if (action === 'command.executed')
        mapCommandFields(event, call, result, args);
    if (action === 'file.read' || action === 'file.created' || action === 'file.modified')
        mapFileFields(event, result, evidence, committed, args);
    var output = durableText(result.output);
    if (output)
        event.content = contentDigest(output);
    var statusRaw = __assign({ status: status, output_bytes: nonNegativeInteger(result.output_bytes), stored_output_bytes: nonNegativeInteger(result.stored_output_bytes), truncated: result.truncated === true, provider_native: result.provider_native === true }, (callId ? { tool_call_sha256: sha256(callId) } : {}));
    var feedback = Array.isArray(result.permission_feedback) ? result.permission_feedback.map(durableText).filter(Boolean) : [];
    if (feedback.length)
        statusRaw.permission_feedback = { count: feedback.length, sha256: sha256(feedback.join('\n')) };
    var process = isRecord(result.command_process_presentation) ? result.command_process_presentation : undefined;
    if (process && safeLabel(process.kind, 64)) {
        statusRaw.process_outcome = process.kind;
        var value = typeof process.value === 'number' && Number.isSafeInteger(process.value) ? process.value : undefined;
        if (value !== undefined)
            statusRaw.process_value = value;
    }
    var terminal = isRecord(result.terminal_action_presentation) ? result.terminal_action_presentation : undefined;
    if (terminal && safeLabel(terminal.kind, 64))
        statusRaw.terminal_outcome = terminal.kind;
    if (committed) {
        statusRaw.diff_additions = (_e = nonNegativeInteger(committed.additions)) !== null && _e !== void 0 ? _e : 0;
        statusRaw.diff_deletions = (_f = nonNegativeInteger(committed.deletions)) !== null && _f !== void 0 ? _f : 0;
        statusRaw.diff_truncated = committed.truncated === true;
    }
    event.raw = __assign(__assign({}, event.raw), { fx: __assign(__assign({}, (isRecord(event.raw.fx) ? event.raw.fx : {})), statusRaw) });
    if (status === 'failure')
        event.error = { type: 'tool_execution_failed' };
    pushFxEvent(events, event);
}
function mapCommandFields(event, call, result, args) {
    var _a, _b;
    var command = isRecord(args) ? boundedString((_a = args.command) !== null && _a !== void 0 ? _a : args.cmd, 1 << 20) : undefined;
    var output = durableText(result.output);
    var process = isRecord(result.command_process_presentation) ? result.command_process_presentation : undefined;
    var commandInfo = {};
    if (command) {
        commandInfo.command_sha256 = sha256(command);
        commandInfo.command_bytes = Buffer.byteLength(command, 'utf8');
    }
    if (output) {
        commandInfo.output_sha256 = sha256(output);
        commandInfo.output_bytes = Buffer.byteLength(output, 'utf8');
    }
    if ((process === null || process === void 0 ? void 0 : process.kind) === 'exit_code' && typeof process.value === 'number' && Number.isSafeInteger(process.value))
        commandInfo.exit_code = process.value;
    event.command = commandInfo;
    if (command)
        event.tool = { name: (_b = safeLabel(call.name, 128)) !== null && _b !== void 0 ? _b : 'command', command_sha256: sha256(command) };
}
function mapFileFields(event, result, evidence, committed, args) {
    var _a, _b, _c, _d;
    var committedPath = committed ? durableText(committed.path) : '';
    var evidencePath = evidence ? durableText(evidence.path) : '';
    var argsPath = isRecord(args) ? (_c = boundedString((_b = (_a = args.path) !== null && _a !== void 0 ? _a : args.file_path) !== null && _b !== void 0 ? _b : args.target_path, 4096)) !== null && _c !== void 0 ? _c : '' : '';
    var selectedPath = committedPath || evidencePath || argsPath;
    var operation = evidence ? fileOperation(String((_d = evidence.action) !== null && _d !== void 0 ? _d : '')) : (committed === null || committed === void 0 ? void 0 : committed.kind) === 'added' ? 'create' : committed ? 'modify' : undefined;
    var fileInfo = {};
    if (selectedPath)
        fileInfo.path_sha256 = sha256(selectedPath);
    if (operation)
        fileInfo.operation = operation;
    if (committed) {
        var diff = committedDiff(committed);
        if (diff) {
            fileInfo.diff_sha256 = sha256(diff);
            fileInfo.diff_bytes = Buffer.byteLength(diff, 'utf8');
            event.content = contentDigest(diff);
        }
    }
    if (committed && nonNegativeInteger(committed.additions) !== undefined)
        fileInfo.additions = committed.additions;
    if (committed && nonNegativeInteger(committed.deletions) !== undefined)
        fileInfo.deletions = committed.deletions;
    event.file = fileInfo;
    void result;
}
function findFileEvidence(turn, callId) {
    if (!callId || !isRecord(turn.execution) || !Array.isArray(turn.execution.files))
        return undefined;
    return turn.execution.files.find(function (value) { return isRecord(value) && opaqueString(value.tool_call_id, 512) === callId; });
}
function fileOperation(action) {
    if (['read', 'search', 'list'].includes(action))
        return 'read';
    if (action === 'write')
        return 'create';
    if (action === 'edit')
        return 'modify';
    if (['delete', 'rename', 'copy'].includes(action))
        return action;
    return undefined;
}
function committedDiff(value) {
    if (!Array.isArray(value.lines))
        return '';
    var output = '';
    for (var _i = 0, _a = value.lines.slice(0, 20000); _i < _a.length; _i++) {
        var row = _a[_i];
        if (!isRecord(row))
            continue;
        var kind = row.kind;
        if (kind === 'elision') {
            output += '...\n';
            continue;
        }
        var prefix = kind === 'addition' ? '+' : kind === 'deletion' ? '-' : kind === 'context' ? ' ' : '# ';
        output += prefix + durableText(row.text) + '\n';
        if (Buffer.byteLength(output, 'utf8') > maxFrameBytes)
            return output.slice(0, maxFrameBytes);
    }
    return output;
}
function parseToolArguments(value) {
    var text = durableText(value);
    if (!text)
        return undefined;
    try {
        return JSON.parse(text);
    }
    catch (_a) {
        return text;
    }
}
function makeTurnUsage(file, generationHash, record, payload, execution, state) {
    var _a, _b, _c, _d;
    var summary = execution && isRecord(execution.turn_summary) ? execution.turn_summary : undefined;
    var progress = summary && isRecord(summary.token_progress) ? summary.token_progress : undefined;
    var inputSummary = progress ? (_a = nonNegativeInteger(progress.input_tokens)) !== null && _a !== void 0 ? _a : 0 : 0;
    var outputSummary = progress ? (_b = nonNegativeInteger(progress.output_tokens)) !== null && _b !== void 0 ? _b : 0 : 0;
    var input = 0;
    var output = 0;
    var raw = {};
    if ((inputSummary > 0 || outputSummary > 0) && progress) {
        input = inputSummary;
        output = outputSummary;
        raw.token_source = 'turn_summary';
        raw.input_exact = progress.input_exact === true;
        raw.output_exact = progress.output_exact === true;
        raw.turn_duration_ms = nonNegativeInteger(summary === null || summary === void 0 ? void 0 : summary.turn_duration_ms);
        raw.thinking_duration_ms = nonNegativeInteger(summary === null || summary === void 0 ? void 0 : summary.thinking_duration_ms);
    }
    else {
        input = positiveDelta((_c = nonNegativeNumber(payload.total_input_tokens)) !== null && _c !== void 0 ? _c : 0, state.lastInput);
        output = positiveDelta((_d = nonNegativeNumber(payload.total_output_tokens)) !== null && _d !== void 0 ? _d : 0, state.lastOutput);
        raw.token_source = 'session_totals_delta';
    }
    if (input <= 0 && output <= 0)
        return undefined;
    var usage = {};
    if (input > 0)
        usage.input_tokens = input;
    if (output > 0)
        usage.output_tokens = output;
    var event = makeFxEvent(file, generationHash, record, 'token.usage', 'metric', 'fx token usage recovered from committed turn', 'turn.usage', state);
    event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { usage: usage });
    event.raw = __assign(__assign({}, event.raw), { fx: raw });
    return event;
}
function updateTurnTotals(payload, state) {
    var input = nonNegativeNumber(payload.total_input_tokens);
    var output = nonNegativeNumber(payload.total_output_tokens);
    if (input !== undefined)
        state.lastInput = input;
    if (output !== undefined)
        state.lastOutput = output;
}
function makeUsageCheckpoint(file, generationHash, record, current, state) {
    var _a, _b, _c, _d, _e, _f;
    var previous = state.lastUsage;
    var cacheRead = positiveDelta((_a = nonNegativeNumber(current.cache_read_tokens)) !== null && _a !== void 0 ? _a : 0, (_b = nonNegativeNumber(previous === null || previous === void 0 ? void 0 : previous.cache_read_tokens)) !== null && _b !== void 0 ? _b : 0);
    var cacheWrite = positiveDelta((_c = nonNegativeNumber(current.cache_write_tokens)) !== null && _c !== void 0 ? _c : 0, (_d = nonNegativeNumber(previous === null || previous === void 0 ? void 0 : previous.cache_write_tokens)) !== null && _d !== void 0 ? _d : 0);
    var costNow = (_e = finiteNonNegative(current.total_cost)) !== null && _e !== void 0 ? _e : 0;
    var costBefore = (_f = finiteNonNegative(previous === null || previous === void 0 ? void 0 : previous.total_cost)) !== null && _f !== void 0 ? _f : 0;
    var cost = costNow > costBefore ? costNow - costBefore : 0;
    if (cacheRead <= 0 && cacheWrite <= 0 && cost <= 0)
        return undefined;
    var usage = {};
    if (cacheRead > 0)
        usage.cache_read_input_tokens = cacheRead;
    if (cacheWrite > 0)
        usage.cache_creation_input_tokens = cacheWrite;
    if (cost > 0)
        usage.cost_usd = cost;
    var models = Array.isArray(current.models) ? current.models.filter(isRecord).slice(0, 20).map(function (model) { return (__assign(__assign(__assign(__assign(__assign(__assign(__assign({}, (safeLabel(model.model, 160) ? { model: safeLabel(model.model, 160) } : {})), (nonNegativeInteger(model.input_tokens) !== undefined ? { input_tokens: model.input_tokens } : {})), (nonNegativeInteger(model.output_tokens) !== undefined ? { output_tokens: model.output_tokens } : {})), (nonNegativeInteger(model.cache_read_tokens) !== undefined ? { cache_read_tokens: model.cache_read_tokens } : {})), (nonNegativeInteger(model.cache_write_tokens) !== undefined ? { cache_write_tokens: model.cache_write_tokens } : {})), (nonNegativeInteger(model.reasoning_tokens) !== undefined ? { reasoning_tokens: model.reasoning_tokens } : {})), (finiteNonNegative(model.total_cost) !== undefined ? { cost_usd: model.total_cost } : {}))); }) : [];
    var event = makeFxEvent(file, generationHash, record, 'token.usage', 'metric', 'fx usage checkpoint recovered from local session log', 'usage.checkpoint', state);
    event.gen_ai = __assign(__assign({}, (isRecord(event.gen_ai) ? event.gen_ai : {})), { usage: usage });
    event.raw = __assign(__assign({}, event.raw), { fx: __assign(__assign(__assign({}, (safeLabel(current.billing, 32) ? { billing: current.billing } : {})), { cumulative_input_tokens: nonNegativeInteger(current.input_tokens), cumulative_output_tokens: nonNegativeInteger(current.output_tokens), cumulative_cache_read_tokens: nonNegativeInteger(current.cache_read_tokens), cumulative_cache_write_tokens: nonNegativeInteger(current.cache_write_tokens), cumulative_cost_usd: costNow, api_duration_ms: nonNegativeInteger(current.api_duration_ms), wall_duration_ms: nonNegativeInteger(current.wall_duration_ms), api_duration_complete: current.api_duration_complete === true, wall_duration_complete: current.wall_duration_complete === true, code_complete: current.code_complete === true, billable_web_search_calls: nonNegativeInteger(current.billable_web_search_calls), cumulative_lines_added: nonNegativeInteger(current.lines_added), cumulative_lines_removed: nonNegativeInteger(current.lines_removed), reported_as_delta_from_prior: previous !== undefined }), (models.length ? { cumulative_models: models } : {})) });
    return event;
}
function makeFxEvent(file, generationHash, record, action, category, message, suffix, state, extra) {
    var _a, _b;
    if (extra === void 0) { extra = {}; }
    var event = {
        timestamp: timestampFromMs((_a = nonNegativeNumber(record.timestamp_ms)) !== null && _a !== void 0 ? _a : file.mtimeMs, file.mtimeMs),
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: {
            id: sha256("".concat(file.sourceSha256, ":").concat(generationHash, ":").concat(record.seq, ":").concat(suffix)),
            kind: 'agent_runtime',
            action: action,
            category: category,
            fidelity: 'observed',
        },
        severity: (_b = extra.severity) !== null && _b !== void 0 ? _b : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'vercel_fx', collection_method: 'poll' },
        session: { id: "vercel-fx:".concat(file.sourceSha256) },
        raw: __assign({ source: 'fx_session_events_jsonl', source_sha256: file.sourceSha256, seq: record.seq, log_generation_sha256: generationHash, record_kind: record.kind, message: message }, (state.workspace ? { workspace_sha256: sha256(state.workspace) } : {})),
    };
    if (state.model)
        event.model = state.model;
    if (state.provider)
        event.gen_ai = { provider: { name: state.provider } };
    if (extra.content)
        event.content = extra.content;
    return event;
}
function contentDigest(value, include, requested) {
    if (include === void 0) { include = false; }
    if (requested === void 0) { requested = false; }
    var digest = digestUnknown(value);
    return __assign(__assign({ retention: include ? 'full' : 'metadata', included: include }, (digest ? digest : {})), (requested && !include ? { truncated: true } : {}));
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
function processFxFiles(files, root, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var totalBytes, _loop_1, _i, files_1, file, state_1;
        var _this = this;
        var _a, _b, _c, _d, _e, _f, _g, _h, _j;
        return __generator(this, function (_k) {
            switch (_k.label) {
                case 0:
                    totalBytes = 0;
                    _loop_1 = function (file) {
                        var previous, manifest_1, _l, manifestInfo, sourceBytes, before, bytes, after, expectedGeneration, watermark, parsed, parseIncomplete, generation, generationHash_1, cursorIncomplete_1, generationChanged, minSeq, mapper, mapped, pendingEventCount, _m, mapped_1, group, _o, _p, event_6, pendingGroups_1, pendingEvents_1, flush, _q, mapped_2, group, _r, lastPrefix, current, _s;
                        return __generator(this, function (_t) {
                            switch (_t.label) {
                                case 0:
                                    previous = state.sessions[file.sourceSha256];
                                    _t.label = 1;
                                case 1:
                                    _t.trys.push([1, 28, , 29]);
                                    _t.label = 2;
                                case 2:
                                    _t.trys.push([2, 4, , 5]);
                                    return [4 /*yield*/, readManifest(file)];
                                case 3:
                                    manifest_1 = _t.sent();
                                    return [3 /*break*/, 5];
                                case 4:
                                    _l = _t.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'fx session manifest is unreadable or unsupported' });
                                    return [2 /*return*/, "continue"];
                                case 5: return [4 /*yield*/, (0, promises_1.lstat)(file.manifestPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                                case 6:
                                    manifestInfo = _t.sent();
                                    sourceBytes = file.size + ((_a = manifestInfo === null || manifestInfo === void 0 ? void 0 : manifestInfo.size) !== null && _a !== void 0 ? _a : 0);
                                    totalBytes += sourceBytes;
                                    if (totalBytes > maxTotalBytes) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx sync exceeded the aggregate source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    return [4 /*yield*/, (0, promises_1.lstat)(file.logPath).catch(function () { return null; })];
                                case 7:
                                    before = _t.sent();
                                    if (!before || before.isSymbolicLink() || !before.isFile() || before.size !== file.size || before.mtimeMs !== file.mtimeMs) {
                                        throw new Error('fx event log changed after enumeration');
                                    }
                                    return [4 /*yield*/, readBoundedRegularFile(file.logPath, root, maxLogBytes, 'fx event log')];
                                case 8:
                                    bytes = _t.sent();
                                    if (!bytes)
                                        throw new Error('fx event log disappeared');
                                    return [4 /*yield*/, (0, promises_1.lstat)(file.logPath).catch(function () { return null; })];
                                case 9:
                                    after = _t.sent();
                                    if (!after || after.size !== file.size || after.mtimeMs !== file.mtimeMs || !sameFile(before, after)) {
                                        throw new Error('fx event log changed while being read');
                                    }
                                    expectedGeneration = manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.log_generation;
                                    watermark = manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.event_log_bytes;
                                    parsed = parseFxLog(bytes, watermark, expectedGeneration);
                                    result.malformed_lines += parsed.malformed;
                                    result.skipped_records += parsed.skipped;
                                    if (parsed.partialTail)
                                        result.partial_sessions++;
                                    parseIncomplete = parsed.partialTail || parsed.malformed > 0 || (watermark !== undefined && watermark > bytes.byteLength);
                                    if (parsed.malformed > 0)
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx event log contains a malformed or unsupported committed frame' });
                                    if (watermark !== undefined && watermark > bytes.byteLength) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest committed watermark extends beyond the event log' });
                                    }
                                    generation = (_b = manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.log_generation) !== null && _b !== void 0 ? _b : parsed.generation;
                                    if (!!generation) return [3 /*break*/, 12];
                                    if (!(manifest_1 && manifest_1.last_event_seq === 0 && manifest_1.event_log_bytes === 0)) return [3 /*break*/, 11];
                                    state.sessions[file.sourceSha256] = makeFxCursor('', 0, (_c = previous === null || previous === void 0 ? void 0 : previous.started) !== null && _c !== void 0 ? _c : false, parsed.prefixHashes.get(0), file, manifest_1.updated_at_ms, cursorIncomplete_1);
                                    result.sessions_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 11];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 10:
                                    _t.sent();
                                    _t.label = 11;
                                case 11: return [2 /*return*/, "continue"];
                                case 12:
                                    generationHash_1 = sha256(generation);
                                    if (manifest_1 && manifest_1.last_event_seq > parsed.maxSeq && !(manifest_1.last_event_seq === 0 && parsed.maxSeq === 0)) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest event sequence is ahead of committed records' });
                                    }
                                    if (manifest_1 && parsed.maxSeq > manifest_1.last_event_seq) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx manifest event sequence is behind committed records' });
                                    }
                                    cursorIncomplete_1 = parseIncomplete || (manifest_1 !== undefined && manifest_1.last_event_seq !== parsed.maxSeq);
                                    generationChanged = previous !== undefined && previous.generation_sha256 !== generationHash_1;
                                    minSeq = previous && !generationChanged ? previous.last_seq : 0;
                                    if (previous && !generationChanged && (parsed.maxSeq < previous.last_seq ||
                                        parsed.prefixHashes.get(previous.last_seq) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx event-log prefix changed within the same generation; refusing replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    mapper = __assign(__assign({}, ((manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.workspace_root) ? { workspace: manifest_1.workspace_root } : {})), { lastInput: 0, lastOutput: 0, started: (_d = previous === null || previous === void 0 ? void 0 : previous.started) !== null && _d !== void 0 ? _d : false });
                                    if (manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.preferences)
                                        applyPreferences(mapper, manifest_1.preferences);
                                    mapped = mapFxGroups(file, generation, parsed, minSeq, mapper, (_e = options.includeContent) !== null && _e !== void 0 ? _e : false);
                                    pendingEventCount = mapped.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    if (pendingEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'fx sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += pendingEventCount;
                                        (_f = result.events) !== null && _f !== void 0 ? _f : (result.events = []);
                                        for (_m = 0, mapped_1 = mapped; _m < mapped_1.length; _m++) {
                                            group = mapped_1[_m];
                                            for (_o = 0, _p = group.events; _o < _p.length; _o++) {
                                                event_6 = _p[_o];
                                                if (result.events.length < maxPreviewEvents)
                                                    result.events.push(event_6);
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
                                        var _a, _b;
                                        return __generator(this, function (_c) {
                                            switch (_c.label) {
                                                case 0:
                                                    if (pendingGroups_1.length === 0)
                                                        return [2 /*return*/];
                                                    if (!pendingEvents_1.length) return [3 /*break*/, 2];
                                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(pendingEvents_1, { endpoint: (_a = options.endpoint) !== null && _a !== void 0 ? _a : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                                case 1:
                                                    _c.sent();
                                                    result.events_emitted += pendingEvents_1.length;
                                                    _c.label = 2;
                                                case 2:
                                                    last = pendingGroups_1[pendingGroups_1.length - 1];
                                                    nextCursor = makeFxCursor(generationHash_1, last.seq, last.started, last.prefix, file, (_b = manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.updated_at_ms) !== null && _b !== void 0 ? _b : file.mtimeMs, cursorIncomplete_1);
                                                    current = state.sessions[file.sourceSha256];
                                                    changed = !current || current.generation_sha256 !== nextCursor.generation_sha256 || current.last_seq !== nextCursor.last_seq ||
                                                        current.started !== nextCursor.started || current.prefix_sha256 !== nextCursor.prefix_sha256 ||
                                                        current.size_bytes !== nextCursor.size_bytes || current.mtime_ms !== nextCursor.mtime_ms ||
                                                        current.updated_at_ms !== nextCursor.updated_at_ms || current.incomplete !== nextCursor.incomplete;
                                                    state.sessions[file.sourceSha256] = nextCursor;
                                                    if (!changed) return [3 /*break*/, 4];
                                                    result.sessions_changed++;
                                                    if (!(persist && statePath)) return [3 /*break*/, 4];
                                                    return [4 /*yield*/, writeState(statePath, state)];
                                                case 3:
                                                    _c.sent();
                                                    _c.label = 4;
                                                case 4:
                                                    pendingGroups_1 = [];
                                                    pendingEvents_1 = [];
                                                    return [2 /*return*/];
                                            }
                                        });
                                    }); };
                                    _t.label = 13;
                                case 13:
                                    _t.trys.push([13, 21, , 22]);
                                    _q = 0, mapped_2 = mapped;
                                    _t.label = 14;
                                case 14:
                                    if (!(_q < mapped_2.length)) return [3 /*break*/, 19];
                                    group = mapped_2[_q];
                                    if (!(pendingEvents_1.length > 0 && pendingEvents_1.length + group.events.length > eventBatchSize)) return [3 /*break*/, 16];
                                    return [4 /*yield*/, flush()];
                                case 15:
                                    _t.sent();
                                    _t.label = 16;
                                case 16:
                                    pendingGroups_1.push(group);
                                    pendingEvents_1.push.apply(pendingEvents_1, group.events);
                                    if (!(pendingEvents_1.length >= eventBatchSize)) return [3 /*break*/, 18];
                                    return [4 /*yield*/, flush()];
                                case 17:
                                    _t.sent();
                                    _t.label = 18;
                                case 18:
                                    _q++;
                                    return [3 /*break*/, 14];
                                case 19: return [4 /*yield*/, flush()];
                                case 20:
                                    _t.sent();
                                    return [3 /*break*/, 22];
                                case 21:
                                    _r = _t.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected fx history events' });
                                    return [2 /*return*/, "continue"];
                                case 22:
                                    if (!(mapped.length === 0 && parsed.maxSeq === 0 && (manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.last_event_seq) === 0)) return [3 /*break*/, 25];
                                    state.sessions[file.sourceSha256] = makeFxCursor(generationHash_1, 0, (_g = previous === null || previous === void 0 ? void 0 : previous.started) !== null && _g !== void 0 ? _g : false, parsed.prefixHashes.get(0), file, manifest_1.updated_at_ms, cursorIncomplete_1);
                                    if (!(persist && statePath)) return [3 /*break*/, 24];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 23:
                                    _t.sent();
                                    _t.label = 24;
                                case 24: return [3 /*break*/, 27];
                                case 25:
                                    if (!(mapped.length > 0 && mapped[mapped.length - 1].seq < parsed.maxSeq)) return [3 /*break*/, 27];
                                    lastPrefix = parsed.prefixHashes.get(parsed.maxSeq);
                                    if (!lastPrefix) return [3 /*break*/, 27];
                                    current = (_h = state.sessions[file.sourceSha256]) !== null && _h !== void 0 ? _h : previous;
                                    state.sessions[file.sourceSha256] = makeFxCursor(generationHash_1, parsed.maxSeq, mapper.started, lastPrefix, file, (_j = manifest_1 === null || manifest_1 === void 0 ? void 0 : manifest_1.updated_at_ms) !== null && _j !== void 0 ? _j : file.mtimeMs, cursorIncomplete_1);
                                    if ((current === null || current === void 0 ? void 0 : current.last_seq) !== parsed.maxSeq || generationChanged)
                                        result.sessions_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 27];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 26:
                                    _t.sent();
                                    _t.label = 27;
                                case 27: return [3 /*break*/, 29];
                                case 28:
                                    _s = _t.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'fx session could not be read or checkpointed safely' });
                                    return [3 /*break*/, 29];
                                case 29: return [2 /*return*/];
                            }
                        });
                    };
                    _i = 0, files_1 = files;
                    _k.label = 1;
                case 1:
                    if (!(_i < files_1.length)) return [3 /*break*/, 4];
                    file = files_1[_i];
                    return [5 /*yield**/, _loop_1(file)];
                case 2:
                    state_1 = _k.sent();
                    if (state_1 === "break")
                        return [3 /*break*/, 4];
                    _k.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function makeFxCursor(generationHash, lastSeq, started, prefix, file, updatedAtMs, incomplete) {
    return {
        generation_sha256: generationHash, last_seq: lastSeq,
        started: started,
        prefix_sha256: prefix,
        size_bytes: file.size, mtime_ms: file.mtimeMs, updated_at_ms: updatedAtMs,
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
                        throw new Error('fx history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock fx history cursor');
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
        var empty, info, handle, opened, bytes, parsed, sessions, _i, _a, _b, key, value, error_3;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.fx-session-cursor.v1', version: cursorVersion, sessions: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes)
                        throw new Error('fx history cursor must be a bounded regular file');
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
                        throw new Error('fx cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxCursorBytes)
                        throw new Error('fx cursor exceeds local size limit');
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.sessions)) {
                        throw new Error('fx history cursor has an unsupported schema; inspect it before retrying');
                    }
                    sessions = {};
                    for (_i = 0, _a = Object.entries(parsed.sessions); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) ||
                            (value.generation_sha256 !== '' && (typeof value.generation_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.generation_sha256))) ||
                            !isNonNegativeInteger(value.last_seq) || typeof value.started !== 'boolean' ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
                            typeof value.updated_at_ms !== 'number' || !Number.isFinite(value.updated_at_ms) || value.updated_at_ms < 0 ||
                            typeof value.incomplete !== 'boolean')
                            throw new Error('fx history cursor contains an invalid checkpoint');
                        sessions[key] = {
                            generation_sha256: value.generation_sha256, last_seq: value.last_seq, started: value.started,
                            prefix_sha256: value.prefix_sha256, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
                            updated_at_ms: value.updated_at_ms, incomplete: value.incomplete,
                        };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { sessions: sessions })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('fx history cursor'))
                        throw error_3;
                    throw new Error('fx history cursor is malformed or unreadable');
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
                            throw new Error('unable to create private fx cursor directory'); })];
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
                        throw new Error('fx cursor path cannot contain symbolic links or non-directory parents');
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
                        throw new Error('fx history cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write fx history cursor');
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
function nonNegativeNumber(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function finiteNonNegative(value) {
    return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined;
}
function positiveDelta(current, previous) { return current > previous ? current - previous : 0; }
function isNonNegativeInteger(value) { return nonNegativeInteger(value) !== undefined; }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value, maximum) {
    return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined;
}
function opaqueString(value, maximum) {
    var text = boundedString(value, maximum);
    return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
}
function safeLabel(value, maximum) {
    var text = typeof value === 'string' ? value.trim() : '';
    if (!text || text.length > maximum || !/^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text) ||
        /^(?:sk|rk|pk|ghp|gho|github_pat|xox[baprs])[_-]/i.test(text) ||
        /^[A-Za-z0-9_-]{40,}$/.test(text) ||
        /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(text))
        return undefined;
    return text;
}
function durableText(value) {
    if (typeof value === 'string')
        return value;
    if (isRecord(value) && value.encoding === 'base64' && typeof value.data === 'string' && value.data.length <= (maxFrameBytes * 4 / 3 + 8) &&
        /^[A-Za-z0-9+/]*={0,2}$/.test(value.data)) {
        return Buffer.from(value.data, 'base64').toString('utf8');
    }
    return '';
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function timestampFromMs(value, fallbackMs) {
    var timestamp = new Date(value);
    return Number.isNaN(timestamp.getTime()) ? new Date(fallbackMs).toISOString() : timestamp.toISOString();
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
