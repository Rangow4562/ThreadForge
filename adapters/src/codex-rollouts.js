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
exports.getCodexRolloutStatus = getCodexRolloutStatus;
exports.syncCodexRollouts = syncCodexRollouts;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var maxStateBytes = 8 << 20;
var maxRolloutBytes = 64 << 20;
var maxLineBytes = 8 << 20;
var maxFiles = 10000;
var maxLinesPerFile = 100000;
var maxEventsPerFile = 10000;
var eventBatchSize = 100;
var maxPreviewEvents = 500;
var maxRetainedContentBytes = 4 << 10;
var genAIStoredStringLimit = 2 << 10;
var stateFileName = 'codex-rollouts.v1.json';
var supportedEntryTypes = new Set(['session_meta', 'turn_context', 'response_item', 'event_msg', 'token_usage_record']);
function getCodexRolloutStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var codexHome, sessionsDir, dirInfo, empty, canonicalSessionsDir, listed, statePath, _a, _b, state, files, _i, _c, file, info, cursor, matches, checkpointed;
        var _d, _e, _f;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_g) {
            switch (_g.label) {
                case 0: return [4 /*yield*/, resolveCodexHome((_e = (_d = options.codexHome) !== null && _d !== void 0 ? _d : process.env.CODEX_HOME) !== null && _e !== void 0 ? _e : node_path_1.default.join(node_os_1.default.homedir(), '.codex'))];
                case 1:
                    codexHome = _g.sent();
                    sessionsDir = node_path_1.default.join(codexHome, 'sessions');
                    return [4 /*yield*/, (0, promises_1.lstat)(sessionsDir).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Codex sessions directory');
                        })];
                case 2:
                    dirInfo = _g.sent();
                    empty = {
                        schema_version: 'threadforge.codex-rollout-status.v1', sessions: 0, checkpointed_sessions: 0,
                        pending_sessions: 0, symlinks_skipped: 0, oversized_files_skipped: 0, files: [],
                    };
                    if (!dirInfo)
                        return [2 /*return*/, empty];
                    if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink())
                        throw new Error('Codex sessions path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(sessionsDir).catch(function () { throw new Error('Codex sessions directory could not be resolved'); })];
                case 3:
                    canonicalSessionsDir = _g.sent();
                    if (!isWithin(codexHome, canonicalSessionsDir))
                        throw new Error('Codex sessions directory must remain inside CODEX_HOME');
                    return [4 /*yield*/, listRolloutFiles(canonicalSessionsDir)];
                case 4:
                    listed = _g.sent();
                    statePath = resolveStatePath(options.statePath);
                    _a = isWithin;
                    _b = [codexHome];
                    return [4 /*yield*/, prospectiveRealPath(statePath)];
                case 5:
                    if (_a.apply(void 0, _b.concat([_g.sent()]))) {
                        throw new Error('rollout cursor state must not be stored inside CODEX_HOME');
                    }
                    return [4 /*yield*/, readState(statePath)];
                case 6:
                    state = _g.sent();
                    files = [];
                    _i = 0, _c = listed.files;
                    _g.label = 7;
                case 7:
                    if (!(_i < _c.length)) return [3 /*break*/, 10];
                    file = _c[_i];
                    return [4 /*yield*/, (0, promises_1.lstat)(file.absolutePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Codex rollout metadata');
                        })];
                case 8:
                    info = _g.sent();
                    if (!info || !info.isFile() || info.isSymbolicLink())
                        return [3 /*break*/, 9];
                    cursor = state.files[file.key];
                    matches = cursor !== undefined && cursor.size_bytes === info.size && cursor.mtime_ms === info.mtimeMs;
                    files.push({
                        source_sha256: sha256(file.key),
                        size_bytes: info.size,
                        cursor_line: (_f = cursor === null || cursor === void 0 ? void 0 : cursor.line) !== null && _f !== void 0 ? _f : 0,
                        checkpoint_matches_metadata: matches,
                    });
                    _g.label = 9;
                case 9:
                    _i++;
                    return [3 /*break*/, 7];
                case 10:
                    checkpointed = files.filter(function (file) { return file.checkpoint_matches_metadata; }).length;
                    return [2 /*return*/, {
                            schema_version: empty.schema_version,
                            sessions: files.length,
                            checkpointed_sessions: checkpointed,
                            pending_sessions: files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped,
                            oversized_files_skipped: listed.oversizedSkipped,
                            files: files,
                        }];
            }
        });
    });
}
function syncCodexRollouts() {
    return __awaiter(this, arguments, void 0, function (options) {
        var codexHome, sessionsDir, dirInfo, canonicalSessionsDir, listed, result, statePath, _a, _b, state;
        var _this = this;
        var _c, _d;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    if (options.preview && options.includeContent)
                        throw new Error('--print cannot be combined with --include-content');
                    return [4 /*yield*/, resolveCodexHome((_d = (_c = options.codexHome) !== null && _c !== void 0 ? _c : process.env.CODEX_HOME) !== null && _d !== void 0 ? _d : node_path_1.default.join(node_os_1.default.homedir(), '.codex'))];
                case 1:
                    codexHome = _e.sent();
                    sessionsDir = node_path_1.default.join(codexHome, 'sessions');
                    return [4 /*yield*/, (0, promises_1.lstat)(sessionsDir).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Codex sessions directory');
                        })];
                case 2:
                    dirInfo = _e.sent();
                    if (!dirInfo)
                        return [2 /*return*/, emptyResult()];
                    if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink())
                        throw new Error('Codex sessions path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(sessionsDir).catch(function () { throw new Error('Codex sessions directory could not be resolved'); })];
                case 3:
                    canonicalSessionsDir = _e.sent();
                    if (!isWithin(codexHome, canonicalSessionsDir))
                        throw new Error('Codex sessions directory must remain inside CODEX_HOME');
                    return [4 /*yield*/, listRolloutFiles(canonicalSessionsDir)];
                case 4:
                    listed = _e.sent();
                    result = emptyResult();
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.content_mode = options.includeContent ? 'include-content' : 'metadata';
                    statePath = resolveStatePath(options.statePath);
                    _a = isWithin;
                    _b = [codexHome];
                    return [4 /*yield*/, prospectiveRealPath(statePath)];
                case 5:
                    if (_a.apply(void 0, _b.concat([_e.sent()]))) {
                        throw new Error('rollout cursor state must not be stored inside CODEX_HOME');
                    }
                    if (!options.preview) return [3 /*break*/, 8];
                    return [4 /*yield*/, readState(statePath)];
                case 6:
                    state = _e.sent();
                    result.events = [];
                    return [4 /*yield*/, processRolloutFiles(listed.files, canonicalSessionsDir, state, options, result, false)];
                case 7:
                    _e.sent();
                    return [2 /*return*/, result];
                case 8: return [4 /*yield*/, withStateLock(statePath, function () { return __awaiter(_this, void 0, void 0, function () {
                        var state;
                        return __generator(this, function (_a) {
                            switch (_a.label) {
                                case 0: return [4 /*yield*/, readState(statePath)];
                                case 1:
                                    state = _a.sent();
                                    return [4 /*yield*/, processRolloutFiles(listed.files, canonicalSessionsDir, state, options, result, true, statePath)];
                                case 2:
                                    _a.sent();
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 3:
                                    _a.sent();
                                    return [2 /*return*/];
                            }
                        });
                    }); })];
                case 9:
                    _e.sent();
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult() {
    return {
        sessions: 0, sessions_changed: 0, events_emitted: 0, events_previewed: 0,
        malformed_lines: 0, partial_sessions: 0, symlinks_skipped: 0, oversized_files_skipped: 0, content_mode: 'metadata',
        preview_truncated: false, errors: [],
    };
}
function resolveCodexHome(value) {
    return __awaiter(this, void 0, void 0, function () {
        var info, canonical, resolved;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(value))
                        throw new Error('CODEX_HOME must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(value).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect CODEX_HOME');
                        })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(value)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('CODEX_HOME must be a real directory, not a symbolic link');
                    return [4 /*yield*/, (0, promises_1.realpath)(value).catch(function () { throw new Error('CODEX_HOME could not be resolved'); })];
                case 2:
                    canonical = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(canonical).catch(function () { return null; })];
                case 3:
                    resolved = _a.sent();
                    if (!resolved || !resolved.isDirectory() || resolved.isSymbolicLink())
                        throw new Error('CODEX_HOME must resolve to a real directory');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listRolloutFiles(root) {
    return __awaiter(this, void 0, void 0, function () {
        var files, symlinksSkipped, oversizedSkipped, pending, directory, entries, _i, entries_1, entry, absolutePath, info, relative, canonicalFile;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    files = [];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    pending = [root];
                    _a.label = 1;
                case 1:
                    if (!(pending.length > 0)) return [3 /*break*/, 8];
                    directory = pending.pop();
                    return [4 /*yield*/, (0, promises_1.readdir)(directory, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Codex rollout files'); })];
                case 2:
                    entries = _a.sent();
                    _i = 0, entries_1 = entries;
                    _a.label = 3;
                case 3:
                    if (!(_i < entries_1.length)) return [3 /*break*/, 7];
                    entry = entries_1[_i];
                    absolutePath = node_path_1.default.join(directory, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(absolutePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Codex rollout path');
                        })];
                case 4:
                    info = _a.sent();
                    if (!info)
                        return [3 /*break*/, 6];
                    if (info.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 6];
                    }
                    if (info.isDirectory()) {
                        if (node_path_1.default.relative(root, absolutePath).split(node_path_1.default.sep).length > 10)
                            throw new Error('Codex sessions exceed the directory-depth scan limit');
                        pending.push(absolutePath);
                        return [3 /*break*/, 6];
                    }
                    if (!info.isFile() || node_path_1.default.extname(entry.name).toLowerCase() !== '.jsonl')
                        return [3 /*break*/, 6];
                    if (info.size > maxRolloutBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 6];
                    }
                    relative = node_path_1.default.relative(root, absolutePath);
                    if (!isSafeRelative(relative)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 6];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(absolutePath).catch(function () { return ''; })];
                case 5:
                    canonicalFile = _a.sent();
                    if (!canonicalFile || !isWithin(root, canonicalFile)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 6];
                    }
                    files.push({ key: relative.split(node_path_1.default.sep).join('/'), absolutePath: canonicalFile });
                    if (files.length > maxFiles)
                        throw new Error("Codex sessions exceed the ".concat(maxFiles, "-file scan limit"));
                    _a.label = 6;
                case 6:
                    _i++;
                    return [3 /*break*/, 3];
                case 7: return [3 /*break*/, 1];
                case 8:
                    files.sort(function (left, right) { return left.key < right.key ? -1 : left.key > right.key ? 1 : 0; });
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped }];
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
function readRolloutFile(filePath, root) {
    return __awaiter(this, void 0, void 0, function () {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, error_1;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function () { return null; })];
                case 1:
                    before = _b.sent();
                    if (!before || !before.isFile() || before.isSymbolicLink())
                        throw new Error('rollout is not a regular file');
                    if (before.size > maxRolloutBytes)
                        throw new Error('rollout exceeds the local file-size limit');
                    return [4 /*yield*/, (0, promises_1.realpath)(filePath).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || !isWithin(root, canonical))
                        throw new Error('rollout path escaped the Codex sessions directory');
                    _b.label = 3;
                case 3:
                    _b.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 4:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _b.sent();
                    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs) {
                        throw new Error('rollout changed before reading');
                    }
                    if (opened.size > maxRolloutBytes)
                        throw new Error('rollout exceeds the local file-size limit');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 7:
                    afterHandle = _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function () { return null; })];
                case 8:
                    afterPath = _b.sent();
                    if (bytes.byteLength > maxRolloutBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
                        afterHandle.size !== opened.size || afterHandle.mtimeMs !== opened.mtimeMs) {
                        throw new Error('rollout changed while it was being read');
                    }
                    return [2 /*return*/, { bytes: bytes, identity: identityOf(opened) }];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('rollout '))
                        throw error_1;
                    throw new Error('Codex rollout could not be opened safely');
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
function identityOf(info) {
    return { dev: info.dev, ino: info.ino, size: info.size, mtimeMs: info.mtimeMs };
}
function parseRollout(bytes) {
    var records = [];
    var lineEnds = [];
    var malformed = 0;
    var line = 0;
    var start = 0;
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    for (var offset = 0; offset < bytes.byteLength; offset++) {
        if (bytes[offset] !== 0x0a)
            continue;
        line++;
        if (line > maxLinesPerFile)
            throw new Error("rollout exceeds the ".concat(maxLinesPerFile, "-line processing limit"));
        lineEnds.push(offset + 1);
        var end = offset;
        if (end > start && bytes[end - 1] === 0x0d)
            end--;
        var raw = bytes.subarray(start, end);
        start = offset + 1;
        if (raw.byteLength === 0)
            continue;
        if (raw.byteLength > maxLineBytes) {
            malformed++;
            continue;
        }
        try {
            var parsed = JSON.parse(decoder.decode(raw));
            if (!isRecord(parsed) || !boundedString(parsed.type, 128)) {
                malformed++;
                continue;
            }
            if (!supportedEntryTypes.has(parsed.type))
                continue;
            var payload = isRecord(parsed.payload) ? parsed.payload : {};
            records.push({ line: line, entry: parsed, payload: payload, type: parsed.type, fingerprint: sha256(raw) });
        }
        catch (_a) {
            malformed++;
        }
    }
    var partialTail = start < bytes.byteLength;
    return { records: records, completeLines: line, partialTail: partialTail, malformed: malformed, lineEnds: lineEnds };
}
function prefixHash(bytes, line, parsed) {
    var end = line > 0 ? parsed.lineEnds[line - 1] : 0;
    return sha256(bytes.subarray(0, end));
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
function prospectiveRealPath(value) {
    return __awaiter(this, void 0, void 0, function () {
        var parent, suffix, info, canonical, next;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    parent = node_path_1.default.dirname(node_path_1.default.resolve(value));
                    suffix = [node_path_1.default.basename(value)];
                    _a.label = 1;
                case 1:
                    if (!true) return [3 /*break*/, 5];
                    return [4 /*yield*/, (0, promises_1.lstat)(parent).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect rollout cursor state path');
                        })];
                case 2:
                    info = _a.sent();
                    if (!info) return [3 /*break*/, 4];
                    if (!info.isDirectory())
                        throw new Error('rollout cursor state parent must be a directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(parent).catch(function () { throw new Error('rollout cursor state parent could not be resolved'); })];
                case 3:
                    canonical = _a.sent();
                    return [2 /*return*/, node_path_1.default.join.apply(node_path_1.default, __spreadArray([canonical], suffix, false))];
                case 4:
                    next = node_path_1.default.dirname(parent);
                    if (next === parent)
                        throw new Error('rollout cursor state path has no existing parent');
                    suffix.unshift(node_path_1.default.basename(parent));
                    parent = next;
                    return [3 /*break*/, 1];
                case 5: return [2 /*return*/];
            }
        });
    });
}
function withStateLock(statePath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var parent, parentInfo, lockPath, lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    parent = node_path_1.default.dirname(statePath);
                    return [4 /*yield*/, (0, promises_1.mkdir)(parent, { recursive: true, mode: 448 }).catch(function () { throw new Error('unable to create private rollout state directory'); })];
                case 1:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(parent).catch(function () { return null; })];
                case 2:
                    parentInfo = _a.sent();
                    if (!parentInfo || !parentInfo.isDirectory() || parentInfo.isSymbolicLink())
                        throw new Error('rollout state parent must be a real directory');
                    lockPath = "".concat(statePath, ".lock");
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 4:
                    lock = _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'EEXIST'))
                        throw new Error('Codex rollout sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Codex rollout state');
                case 6:
                    _a.trys.push([6, , 8, 11]);
                    return [4 /*yield*/, action()];
                case 7: return [2 /*return*/, _a.sent()];
                case 8: return [4 /*yield*/, lock.close().catch(function () { })];
                case 9:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
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
        var empty, info, handle, opened, bytes, parsed, files, _i, _a, _b, key, value, error_3;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.codex-rollout-cursor.v1', version: stateVersion, files: {} };
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect rollout cursor state');
                        })];
                case 1:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('rollout cursor state must be a regular, non-symlink file');
                    if (info.size > maxStateBytes)
                        throw new Error('rollout cursor state exceeds the local size limit');
                    _d.label = 2;
                case 2:
                    _d.trys.push([2, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.open)(statePath, node_fs_1.constants.O_RDONLY | ((_c = node_fs_1.constants.O_NOFOLLOW) !== null && _c !== void 0 ? _c : 0))];
                case 3:
                    handle = _d.sent();
                    return [4 /*yield*/, handle.stat()];
                case 4:
                    opened = _d.sent();
                    if (!sameFile(info, opened))
                        throw new Error('rollout cursor state changed before reading');
                    return [4 /*yield*/, handle.readFile()];
                case 5:
                    bytes = _d.sent();
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
                        throw new Error('rollout cursor state has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!isSafeRelative(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
                            typeof value.session_started !== 'boolean') {
                            throw new Error('rollout cursor state contains an invalid file checkpoint');
                        }
                        files[key] = {
                            line: value.line,
                            size_bytes: value.size_bytes,
                            mtime_ms: value.mtime_ms,
                            prefix_sha256: value.prefix_sha256,
                            session_started: value.session_started,
                        };
                    }
                    return [2 /*return*/, { schema_version: empty.schema_version, version: stateVersion, files: files }];
                case 6:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('rollout cursor state'))
                        throw error_3;
                    throw new Error('rollout cursor state is malformed or unreadable');
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 8:
                    _d.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function isNonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}
function writeState(statePath, state) {
    return __awaiter(this, void 0, void 0, function () {
        var payload, tempPath, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    payload = Buffer.from("".concat(JSON.stringify(state, null, 2), "\n"), 'utf8');
                    if (payload.byteLength > maxStateBytes)
                        throw new Error('rollout cursor state would exceed the local size limit');
                    tempPath = "".concat(statePath, ".").concat((0, node_crypto_1.createHash)('sha256').update(String(Date.now()) + String(Math.random())).digest('hex'), ".tmp");
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 8, , 11]);
                    return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
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
                    throw new Error('unable to atomically write Codex rollout cursor state');
                case 11: return [2 /*return*/];
            }
        });
    });
}
function sha256(value) {
    return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex');
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function boundedString(value, max) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined;
}
function mapRolloutRecords(records, sourceHash, startLine, started, fallbackTimestamp, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l;
    var tokenRecordTurns = new Set();
    for (var _i = 0, records_1 = records; _i < records_1.length; _i++) {
        var record = records_1[_i];
        if (record.type !== 'token_usage_record')
            continue;
        var turnID = boundedString(record.payload.turn_id, 256);
        if (turnID)
            tokenRecordTurns.add(turnID);
    }
    var state = { sessionID: sourceHash, model: '', started: started, tools: new Map() };
    var events = [];
    var emit = function (record, action, category, message, suffix, extra, severity) {
        if (extra === void 0) { extra = {}; }
        if (severity === void 0) { severity = 'info'; }
        if (record.line <= startLine)
            return;
        if (events.length >= maxEventsPerFile)
            throw new Error("rollout exceeds the ".concat(maxEventsPerFile, "-event processing limit"));
        var timestamp = recordTimestamp(record, fallbackTimestamp);
        var eventID = sha256("".concat(sourceHash, ":").concat(record.line, ":").concat(suffix, ":").concat(record.fingerprint));
        var sessionHash = "codex:".concat(sha256(state.sessionID || sourceHash));
        var event = __assign({ timestamp: timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0', event: { id: eventID, kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' }, severity: severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'codex-cli', collection_method: 'poll' }, session: { id: sessionHash }, raw: __assign({ source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type }, (safeLabel(record.payload.type, 128) ? { payload_type: safeLabel(record.payload.type, 128) } : {})) }, extra);
        if (state.model)
            event.model = state.model;
        events.push({ sourceLine: record.line, event: event });
    };
    for (var _m = 0, records_2 = records; _m < records_2.length; _m++) {
        var record = records_2[_m];
        var payload = record.payload;
        if (record.type === 'session_meta') {
            state.sessionID = (_a = firstString(payload, ['id', 'session_id'])) !== null && _a !== void 0 ? _a : state.sessionID;
            if (record.line > startLine && !state.started) {
                emit(record, 'session.started', 'session', 'Codex session start recovered from local rollout', 'session.started');
                state.started = true;
            }
            continue;
        }
        if (record.type === 'turn_context') {
            var model = safeLabel(payload.model, 128);
            if (model)
                state.model = model;
            continue;
        }
        if (record.type === 'response_item') {
            var itemType = (_b = boundedString(payload.type, 128)) !== null && _b !== void 0 ? _b : '';
            if (itemType === 'message') {
                var text = textFromContent(payload.content);
                if (!text)
                    continue;
                var role = (_c = boundedString(payload.role, 32)) === null || _c === void 0 ? void 0 : _c.toLowerCase();
                if (role !== 'user' && role !== 'assistant')
                    continue;
                var content = contentMetadata(text, includeContent);
                if (role === 'user') {
                    var extra = { content: content };
                    if (includeContent) {
                        var retained = retainText(text);
                        extra.prompt = { text: retained };
                        extra.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: retained }] }] } };
                    }
                    emit(record, 'prompt.submitted', 'prompt', 'Codex prompt recovered from local rollout', 'prompt', extra);
                }
                else {
                    var extra = { content: content };
                    if (includeContent) {
                        var retained = retainText(text);
                        extra.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: retained }] }] } };
                    }
                    emit(record, 'agent.message', 'agent', 'Codex assistant message recovered from local rollout', 'agent.message', extra);
                }
                continue;
            }
            if (['custom_tool_call', 'function_call'].includes(itemType)) {
                var callID = (_d = boundedString(payload.call_id, 512)) !== null && _d !== void 0 ? _d : '';
                var name_1 = (_e = safeLabel(payload.name, 128)) !== null && _e !== void 0 ? _e : '';
                var input = itemType === 'function_call' ? parseArguments(payload.arguments, payload.input) : payload.input;
                var call = { id: callID, name: name_1, input: input };
                if (callID)
                    state.tools.set(callID, call);
                if (!name_1)
                    continue;
                var serialized = stableText(input);
                var content = input === undefined ? undefined : contentMetadata(serialized, includeContent);
                var retainArguments = includeContent && Buffer.byteLength(serialized, 'utf8') <= maxRetainedContentBytes;
                if (content && includeContent && !retainArguments) {
                    content.included = false;
                    content.retention = 'metadata';
                    content.truncated = true;
                }
                var extra = __assign({ tool: { name: name_1 } }, (content ? { content: content } : {}));
                if (callID)
                    extra.raw = { source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type, tool_call_id_sha256: sha256(callID) };
                if (retainArguments) {
                    extra.gen_ai = { tool: { name: name_1, call: __assign(__assign({}, (callID ? { id: sha256(callID) } : {})), (input === undefined ? {} : { arguments: input })) } };
                    if (isCommandTool(name_1))
                        extra.tool = { name: name_1, command: retainText(commandFromTool(input)) };
                }
                emit(record, 'tool.invoked', 'tool', 'Codex tool invocation recovered from local rollout', "tool.invoked.".concat(callID || record.line), extra);
                continue;
            }
            if (['custom_tool_call_output', 'function_call_output'].includes(itemType)) {
                var callID = (_f = boundedString(payload.call_id, 512)) !== null && _f !== void 0 ? _f : '';
                var call = (_g = state.tools.get(callID)) !== null && _g !== void 0 ? _g : { id: callID, name: '', input: undefined };
                var output = toolOutputText(payload.output);
                var failed = ['failed', 'failure', 'error'].includes(((_h = boundedString(payload.status, 32)) !== null && _h !== void 0 ? _h : '').toLowerCase());
                var content = output ? contentMetadata(output, includeContent) : undefined;
                var extra = content ? { content: content } : {};
                if (call.name)
                    extra.tool = __assign({ name: call.name }, (includeContent && isCommandTool(call.name) ? { command: retainText(commandFromTool(call.input)) } : {}));
                if (callID)
                    extra.raw = { source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type, tool_call_id_sha256: sha256(callID) };
                var outputRetained = includeContent && Buffer.byteLength(output, 'utf8') <= maxRetainedContentBytes;
                if (includeContent && !outputRetained) {
                    if (content) {
                        content.included = false;
                        content.retention = 'metadata';
                        content.truncated = true;
                    }
                }
                if (includeContent && outputRetained) {
                    var inputSerialized = stableText(call.input);
                    var retainInput = call.input !== undefined && Buffer.byteLength(inputSerialized, 'utf8') <= maxRetainedContentBytes;
                    extra.gen_ai = { tool: __assign(__assign({}, (call.name ? { name: call.name } : {})), { call: __assign(__assign(__assign({}, (callID ? { id: sha256(callID) } : {})), (retainInput ? { arguments: call.input } : {})), (output ? { result: retainText(output) } : {})) }) };
                }
                if (isCommandTool(call.name)) {
                    if (outputRetained)
                        extra.command = { command: retainText(commandFromTool(call.input)), output: retainText(output) };
                    emit(record, 'command.executed', 'command', 'Codex command result recovered from local rollout', "tool.result.".concat(callID || record.line), extra, failed ? 'medium' : 'info');
                }
                else {
                    emit(record, failed ? 'tool.failed' : 'tool.completed', 'tool', 'Codex tool result recovered from local rollout', "tool.result.".concat(callID || record.line), extra, failed ? 'medium' : 'info');
                }
                continue;
            }
        }
        if (record.type === 'event_msg') {
            var messageType = (_j = boundedString(payload.type, 128)) !== null && _j !== void 0 ? _j : '';
            if (messageType === 'task_complete') {
                var extra = {};
                var duration = nonNegativeNumber(payload.duration_ms);
                var firstToken = nonNegativeNumber(payload.time_to_first_token_ms);
                var turnID = boundedString(payload.turn_id, 256);
                extra.raw = __assign(__assign(__assign({ source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type, payload_type: messageType }, (turnID ? { turn_id_sha256: sha256(turnID) } : {})), (duration === undefined ? {} : { duration_ms: duration })), (firstToken === undefined ? {} : { time_to_first_token_ms: firstToken }));
                emit(record, 'session.status', 'session', 'Codex task completion recovered from local rollout', 'task.complete', extra);
            }
            else if (messageType === 'token_count') {
                consumeTokenCount(record, sourceHash, payload, state, tokenRecordTurns, emit);
            }
            continue;
        }
        if (record.type === 'token_usage_record') {
            var turnID = (_k = boundedString(payload.turn_id, 256)) !== null && _k !== void 0 ? _k : '';
            var sessionID = boundedString(payload.session_id, 256);
            if (sessionID)
                state.sessionID = sessionID;
            var usage = (_l = tokenUsage(payload.turn_token_usage)) !== null && _l !== void 0 ? _l : tokenUsage(payload.usage);
            if (!usage || record.line <= startLine)
                continue;
            emitTokenUsage(record, sourceHash, usage, state, emit, turnID, boundedString(payload.response_id, 256));
        }
    }
    return { events: events };
}
function consumeTokenCount(record, sourceHash, payload, state, authoritativeTurns, emit) {
    var _a;
    var info = isRecord(payload.info) ? payload.info : {};
    var turnID = (_a = boundedString(payload.turn_id, 256)) !== null && _a !== void 0 ? _a : '';
    var total = tokenUsage(info.total_token_usage);
    var previous = state.lastSessionTotal;
    if (total) {
        if (previous && sameUsage(previous, total))
            return;
        state.lastSessionTotal = total;
    }
    if (turnID && authoritativeTurns.has(turnID))
        return;
    if (record.line <= startLine)
        return;
    var hasLastSnapshot = isRecord(info.last_token_usage);
    var usage = hasLastSnapshot
        ? tokenUsage(info.last_token_usage)
        : total ? usageDelta(total, previous) : undefined;
    if (usage)
        emitTokenUsage(record, sourceHash, usage, state, emit, turnID);
}
function emitTokenUsage(record, sourceHash, usage, state, emit, turnID, responseID) {
    if (turnID === void 0) { turnID = ''; }
    var cached = Math.max(usage.cached_input_tokens, 0);
    var cacheWrite = Math.max(usage.cache_write_input_tokens, 0);
    var extra = {
        gen_ai: __assign({ conversation: { id: "codex:".concat(sha256(state.sessionID || sourceHash)) }, usage: {
                input_tokens: Math.max(usage.input_tokens - cached - cacheWrite, 0),
                output_tokens: Math.max(usage.output_tokens, 0),
                cache_read: { input_tokens: cached },
                cache_creation: { input_tokens: cacheWrite },
                reasoning: { output_tokens: Math.max(usage.reasoning_output_tokens, 0) },
            } }, (responseID ? { response: __assign({ id: sha256(responseID) }, (state.model ? { model: state.model } : {})) } : {})),
        raw: __assign({ source: 'codex_rollout_backfill', source_sha256: sourceHash, line: record.line, entry_type: record.type }, (turnID ? { turn_id_sha256: sha256(turnID) } : {})),
    };
    emit(record, 'token.usage', 'metric', 'Codex token usage recovered from local rollout', "usage.".concat(turnID ? sha256(turnID) : record.line), extra);
}
function tokenUsage(value) {
    if (!isRecord(value))
        return undefined;
    var usage = {
        input_tokens: integerOrZero(value.input_tokens),
        cached_input_tokens: integerOrZero(value.cached_input_tokens),
        cache_write_input_tokens: integerOrZero(value.cache_write_input_tokens),
        output_tokens: integerOrZero(value.output_tokens),
        reasoning_output_tokens: integerOrZero(value.reasoning_output_tokens),
        total_tokens: integerOrZero(value.total_tokens),
    };
    return Object.values(usage).some(function (number) { return number > 0; }) ? usage : undefined;
}
function integerOrZero(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) ? value : 0;
}
function nonNegativeNumber(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function sameUsage(left, right) {
    return Object.keys(left).every(function (key) { return left[key] === right[key]; });
}
function usageDelta(current, previous) {
    if (!previous)
        return current;
    return {
        input_tokens: Math.max(current.input_tokens - previous.input_tokens, 0),
        cached_input_tokens: Math.max(current.cached_input_tokens - previous.cached_input_tokens, 0),
        cache_write_input_tokens: Math.max(current.cache_write_input_tokens - previous.cache_write_input_tokens, 0),
        output_tokens: Math.max(current.output_tokens - previous.output_tokens, 0),
        reasoning_output_tokens: Math.max(current.reasoning_output_tokens - previous.reasoning_output_tokens, 0),
        total_tokens: Math.max(current.total_tokens - previous.total_tokens, 0),
    };
}
function contentMetadata(value, includeContent) {
    var bytes = Buffer.byteLength(value, 'utf8');
    var included = includeContent;
    return __assign({ retention: included ? 'full' : 'metadata', included: included, hash: sha256(value), bytes: bytes }, (includeContent && bytes > genAIStoredStringLimit ? { truncated: true } : {}));
}
function firstString(value, keys) {
    for (var _i = 0, keys_1 = keys; _i < keys_1.length; _i++) {
        var key = keys_1[_i];
        var candidate = boundedString(value[key], 256);
        if (candidate)
            return candidate;
    }
    return undefined;
}
function safeLabel(value, max) {
    var label = boundedString(value, max);
    if (!label || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(label) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(label) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(label))
        return undefined;
    return label;
}
function stableText(value) {
    var _a;
    if (value === undefined)
        return '';
    try {
        return (_a = JSON.stringify(canonicalize(value))) !== null && _a !== void 0 ? _a : '';
    }
    catch (_b) {
        return '';
    }
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
}
function parseArguments(value, fallback) {
    if (typeof value !== 'string' || !value.trim())
        return fallback;
    try {
        return JSON.parse(value);
    }
    catch (_a) {
        return value;
    }
}
function textFromContent(value) {
    var _a;
    if (typeof value === 'string')
        return value.trim();
    if (!Array.isArray(value))
        return '';
    var parts = [];
    for (var _i = 0, value_1 = value; _i < value_1.length; _i++) {
        var raw = value_1[_i];
        if (!isRecord(raw))
            continue;
        var type = (_a = boundedString(raw.type, 64)) !== null && _a !== void 0 ? _a : '';
        if (!['input_text', 'output_text', 'text', 'summary_text', 'reasoning', 'reasoning_text'].includes(type))
            continue;
        var text = firstText(raw, ['text', 'content', 'summary']);
        if (text)
            parts.push(text);
    }
    return parts.join('\n').trim();
}
function toolOutputText(value) {
    if (typeof value === 'string')
        return value;
    if (Array.isArray(value)) {
        return value.map(function (part) {
            var _a;
            if (typeof part === 'string')
                return part;
            return isRecord(part) ? (_a = firstText(part, ['text', 'content', 'output'])) !== null && _a !== void 0 ? _a : '' : '';
        }).filter(Boolean).join('\n');
    }
    return stableText(value);
}
function commandFromTool(value) {
    if (typeof value === 'string')
        return value.trim();
    if (!isRecord(value))
        return stableText(value);
    for (var _i = 0, _a = ['command', 'cmd', 'shell_command']; _i < _a.length; _i++) {
        var key = _a[_i];
        if (typeof value[key] === 'string' && value[key].trim())
            return value[key].trim();
        if (Array.isArray(value[key]))
            return value[key].filter(function (part) { return typeof part === 'string'; }).join(' ').trim();
    }
    return stableText(value);
}
function isCommandTool(name) {
    return ['exec', 'exec_command', 'shell', 'bash'].includes(name.toLowerCase());
}
function recordTimestamp(record, fallback) {
    var candidates = [record.entry.timestamp, record.payload.timestamp];
    if (record.type === 'session_meta')
        candidates.push(record.payload.timestamp);
    for (var _i = 0, candidates_1 = candidates; _i < candidates_1.length; _i++) {
        var candidate = candidates_1[_i];
        if (typeof candidate !== 'string')
            continue;
        var date = new Date(candidate);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    return fallback;
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
function firstText(value, keys) {
    for (var _i = 0, keys_2 = keys; _i < keys_2.length; _i++) {
        var key = keys_2[_i];
        if (typeof value[key] === 'string')
            return value[key];
    }
    return undefined;
}
function retainText(value) {
    if (Buffer.byteLength(value, 'utf8') <= maxRetainedContentBytes)
        return value;
    var output = '';
    for (var _i = 0, value_2 = value; _i < value_2.length; _i++) {
        var char = value_2[_i];
        if (Buffer.byteLength(output + char, 'utf8') > maxRetainedContentBytes)
            break;
        output += char;
    }
    return output;
}
function processRolloutFiles(files, root, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var _i, files_1, file, sourceHash, _a, bytes, identity, parsed, stored, cursorLine, sessionStarted, fallbackTimestamp, mapped, _b, _c, item, acknowledgedLine, offset, batch, previousLine, error_4;
        var _d, _e, _f, _g, _h, _j;
        return __generator(this, function (_k) {
            switch (_k.label) {
                case 0:
                    _i = 0, files_1 = files;
                    _k.label = 1;
                case 1:
                    if (!(_i < files_1.length)) return [3 /*break*/, 13];
                    file = files_1[_i];
                    sourceHash = sha256(file.key);
                    _k.label = 2;
                case 2:
                    _k.trys.push([2, 11, , 12]);
                    return [4 /*yield*/, readRolloutFile(file.absolutePath, root)];
                case 3:
                    _a = _k.sent(), bytes = _a.bytes, identity = _a.identity;
                    parsed = parseRollout(bytes);
                    result.malformed_lines += parsed.malformed;
                    if (parsed.partialTail)
                        result.partial_sessions++;
                    stored = state.files[file.key];
                    cursorLine = (_d = stored === null || stored === void 0 ? void 0 : stored.line) !== null && _d !== void 0 ? _d : 0;
                    sessionStarted = (_e = stored === null || stored === void 0 ? void 0 : stored.session_started) !== null && _e !== void 0 ? _e : false;
                    if (cursorLine > parsed.completeLines || prefixHash(bytes, cursorLine, parsed) !== ((_f = stored === null || stored === void 0 ? void 0 : stored.prefix_sha256) !== null && _f !== void 0 ? _f : sha256(Buffer.alloc(0)))) {
                        cursorLine = 0;
                        sessionStarted = false;
                    }
                    fallbackTimestamp = new Date(identity.mtimeMs).toISOString();
                    mapped = mapRolloutRecords(parsed.records, sourceHash, cursorLine, sessionStarted, fallbackTimestamp, (_g = options.includeContent) !== null && _g !== void 0 ? _g : false);
                    if (mapped.events.length > maxEventsPerFile)
                        throw new Error("rollout exceeds the ".concat(maxEventsPerFile, "-event processing limit"));
                    if (options.preview) {
                        result.events_previewed += mapped.events.length;
                        for (_b = 0, _c = mapped.events; _b < _c.length; _b++) {
                            item = _c[_b];
                            if (result.events.length < maxPreviewEvents)
                                result.events.push(item.event);
                            else
                                result.preview_truncated = true;
                        }
                        return [3 /*break*/, 12];
                    }
                    acknowledgedLine = cursorLine;
                    offset = 0;
                    _k.label = 4;
                case 4:
                    if (!(offset < mapped.events.length)) return [3 /*break*/, 8];
                    batch = mapped.events.slice(offset, offset + eventBatchSize);
                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)(batch.map(function (item) { return item.event; }), { endpoint: (_h = options.endpoint) !== null && _h !== void 0 ? _h : process.env.THREADFORGE_EVENT_ENDPOINT })];
                case 5:
                    _k.sent();
                    acknowledgedLine = Math.max.apply(Math, __spreadArray([acknowledgedLine], batch.map(function (item) { return item.sourceLine; }), false));
                    if (batch.some(function (item) { return item.event.event.action === 'session.started'; }))
                        sessionStarted = true;
                    state.files[file.key] = makeCursor(acknowledgedLine, identity, prefixHash(bytes, acknowledgedLine, parsed), sessionStarted);
                    result.events_emitted += batch.length;
                    if (!statePath) return [3 /*break*/, 7];
                    return [4 /*yield*/, writeState(statePath, state)];
                case 6:
                    _k.sent();
                    _k.label = 7;
                case 7:
                    offset += eventBatchSize;
                    return [3 /*break*/, 4];
                case 8:
                    previousLine = (_j = stored === null || stored === void 0 ? void 0 : stored.line) !== null && _j !== void 0 ? _j : 0;
                    if (parsed.completeLines > acknowledgedLine)
                        acknowledgedLine = parsed.completeLines;
                    if (!(mapped.events.length > 0 || acknowledgedLine > previousLine || !stored)) return [3 /*break*/, 10];
                    state.files[file.key] = makeCursor(acknowledgedLine, identity, prefixHash(bytes, acknowledgedLine, parsed), sessionStarted);
                    result.sessions_changed++;
                    if (!(persist && statePath)) return [3 /*break*/, 10];
                    return [4 /*yield*/, writeState(statePath, state)];
                case 9:
                    _k.sent();
                    _k.label = 10;
                case 10: return [3 /*break*/, 12];
                case 11:
                    error_4 = _k.sent();
                    result.errors.push({
                        source_sha256: sourceHash,
                        message: error_4 instanceof Error ? error_4.message.slice(0, 256) : 'Codex rollout could not be processed',
                    });
                    return [3 /*break*/, 12];
                case 12:
                    _i++;
                    return [3 /*break*/, 1];
                case 13: return [2 /*return*/];
            }
        });
    });
}
function makeCursor(line, identity, prefix, sessionStarted) {
    return {
        line: line,
        size_bytes: identity.size,
        mtime_ms: identity.mtimeMs,
        prefix_sha256: prefix,
        session_started: sessionStarted,
    };
}
