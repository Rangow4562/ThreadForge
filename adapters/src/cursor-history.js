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
exports.getCursorHistoryStatus = getCursorHistoryStatus;
exports.syncCursorHistory = syncCursorHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'cursor-transcripts.v1.json';
var maxStateBytes = 8 << 20;
var maxFiles = 5000;
var maxFileBytes = 64 << 20;
var maxTotalBytes = 512 << 20;
var maxLineBytes = 8 << 20;
var maxRecordsPerFile = 100000;
var maxEventsPerFile = 10000;
var maxEventsPerSync = 20000;
var maxToolCorrelations = 10000;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
var maxDepth = 6;
function getCursorHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, projectsDir, listed, state, checkpointed;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    maximum = boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : 1000, 1, maxFiles, '--max-files');
                    return [4 /*yield*/, resolveProjectsDir(options.projectsDir)];
                case 1:
                    projectsDir = _b.sent();
                    return [4 /*yield*/, listTranscriptFiles(projectsDir, maximum)];
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
                            schema_version: 'threadforge.cursor-history-status.v1', sessions: listed.files.length,
                            checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
                            files_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncCursorHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var projectsDir, statePath, listed, result, _a, _b;
        var _this = this;
        var _c, _d;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveProjectsDir(options.projectsDir)];
                case 1:
                    projectsDir = _e.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(projectsDir, statePath))
                        throw new Error('Cursor transcript cursor must be stored outside Cursor projects');
                    return [4 /*yield*/, listTranscriptFiles(projectsDir, (_c = options.maxFiles) !== null && _c !== void 0 ? _c : 1000)];
                case 2:
                    listed = _e.sent();
                    result = emptyResult((_d = options.includeContent) !== null && _d !== void 0 ? _d : false);
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.files_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    _a = processFiles;
                    _b = [listed.files, projectsDir];
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
                                    return [4 /*yield*/, processFiles(listed.files, projectsDir, state, options, result, true, statePath)];
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
                    result.complete = result.errors.length === 0 && result.malformed_records === 0 && result.partial_sessions === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.cursor-history-sync.v1', sessions: 0, sessions_changed: 0,
        events_emitted: 0, events_previewed: 0, malformed_records: 0, partial_sessions: 0,
        symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
        preview_truncated: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : 1000, 1, maxFiles, '--max-files');
    if (options.projectsDir !== undefined && !node_path_1.default.isAbsolute(options.projectsDir))
        throw new Error('--projects-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveProjectsDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var projectsDir, info, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    projectsDir = value !== null && value !== void 0 ? value : node_path_1.default.join(node_os_1.default.homedir(), '.cursor', 'projects');
                    if (!node_path_1.default.isAbsolute(projectsDir))
                        throw new Error('--projects-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(projectsDir).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Cursor projects directory');
                        })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(projectsDir)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('Cursor projects path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectsDir).catch(function () { throw new Error('Cursor projects directory could not be resolved'); })];
                case 2:
                    canonical = _a.sent();
                    if (canonical !== node_path_1.default.resolve(projectsDir))
                        throw new Error('Cursor projects directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listTranscriptFiles(root, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInfo, files, symlinksSkipped, oversizedSkipped, truncated, projectEntries, pending, _i, projectEntries_1, project, projectPath, projectInfo, transcriptDir, transcriptInfo, current, entries, _a, entries_1, entry, absolute, info, canonical, relative;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    rootInfo = _b.sent();
                    if (!rootInfo)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Cursor projects path must be a real directory');
                    files = [];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    return [4 /*yield*/, (0, promises_1.readdir)(root, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Cursor projects'); })];
                case 2:
                    projectEntries = _b.sent();
                    pending = [];
                    _i = 0, projectEntries_1 = projectEntries;
                    _b.label = 3;
                case 3:
                    if (!(_i < projectEntries_1.length)) return [3 /*break*/, 7];
                    project = projectEntries_1[_i];
                    projectPath = node_path_1.default.join(root, project.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(projectPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 4:
                    projectInfo = _b.sent();
                    if (!projectInfo)
                        return [3 /*break*/, 6];
                    if (projectInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 6];
                    }
                    if (!projectInfo.isDirectory())
                        return [3 /*break*/, 6];
                    transcriptDir = node_path_1.default.join(projectPath, 'agent-transcripts');
                    return [4 /*yield*/, (0, promises_1.lstat)(transcriptDir).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    transcriptInfo = _b.sent();
                    if (!transcriptInfo)
                        return [3 /*break*/, 6];
                    if (transcriptInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 6];
                    }
                    if (transcriptInfo.isDirectory())
                        pending.push({ path: transcriptDir, depth: 0 });
                    _b.label = 6;
                case 6:
                    _i++;
                    return [3 /*break*/, 3];
                case 7:
                    if (!(pending.length > 0)) return [3 /*break*/, 14];
                    current = pending.pop();
                    if (current.depth > maxDepth)
                        throw new Error("Cursor transcript directories exceed depth ".concat(maxDepth));
                    return [4 /*yield*/, (0, promises_1.readdir)(current.path, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Cursor transcript files'); })];
                case 8:
                    entries = _b.sent();
                    _a = 0, entries_1 = entries;
                    _b.label = 9;
                case 9:
                    if (!(_a < entries_1.length)) return [3 /*break*/, 13];
                    entry = entries_1[_a];
                    absolute = node_path_1.default.join(current.path, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(absolute).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 10:
                    info = _b.sent();
                    if (!info)
                        return [3 /*break*/, 12];
                    if (info.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 12];
                    }
                    if (info.isDirectory()) {
                        pending.push({ path: absolute, depth: current.depth + 1 });
                        return [3 /*break*/, 12];
                    }
                    if (!info.isFile() || entry.name.startsWith('.') || !isTranscriptFilename(entry.name))
                        return [3 /*break*/, 12];
                    if (info.size > maxFileBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 12];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(absolute).catch(function () { return ''; })];
                case 11:
                    canonical = _b.sent();
                    if (!canonical || !isWithin(root, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 12];
                    }
                    relative = node_path_1.default.relative(root, canonical);
                    if (!isSafeRelative(relative)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 12];
                    }
                    files.push({ key: relative.split(node_path_1.default.sep).join('/'), path: canonical, size: info.size, mtimeMs: info.mtimeMs });
                    if (files.length > maximum) {
                        truncated = true;
                        return [3 /*break*/, 13];
                    }
                    _b.label = 12;
                case 12:
                    _a++;
                    return [3 /*break*/, 9];
                case 13:
                    if (truncated)
                        return [3 /*break*/, 14];
                    return [3 /*break*/, 7];
                case 14:
                    files.sort(function (left, right) { return left.key < right.key ? -1 : left.key > right.key ? 1 : 0; });
                    if (truncated)
                        files.length = maximum;
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function isTranscriptFilename(name) {
    var lower = name.toLowerCase();
    return lower.endsWith('.json') || lower.endsWith('.jsonl') || node_path_1.default.extname(name) === '';
}
function isSafeRelative(value) { return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function readTranscriptFile(file, root) {
    return __awaiter(this, void 0, void 0, function () {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, error_1;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 1:
                    before = _b.sent();
                    if (!before || !before.isFile() || before.isSymbolicLink() || before.size > maxFileBytes)
                        throw new Error('Cursor transcript is not a bounded regular file');
                    if (before.size !== file.size || before.mtimeMs !== file.mtimeMs)
                        throw new Error('Cursor transcript changed after enumeration');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || canonical !== file.path || !isWithin(root, canonical))
                        throw new Error('Cursor transcript path escaped Cursor projects');
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
                        throw new Error('Cursor transcript changed before reading');
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
                        throw new Error('Cursor transcript changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Cursor transcript'))
                        throw error_1;
                    throw new Error('Cursor transcript could not be opened safely');
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
function parseTranscript(bytes, sourceKey) {
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var trimmed;
    try {
        trimmed = decoder.decode(bytes).trim();
    }
    catch (_a) {
        return { records: records, partialTail: false, malformed: 1, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
    }
    var records = [];
    var partialTail = false;
    var malformed = 0;
    if (trimmed.length === 0)
        return { records: records, partialTail: partialTail, malformed: malformed, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
    var whole;
    if (/\.jsonl$/i.test(sourceKey))
        whole = undefined;
    else {
        try {
            whole = JSON.parse(trimmed);
        }
        catch (_b) {
            whole = undefined;
        }
    }
    if (whole !== undefined) {
        var candidates = transcriptCandidates(whole);
        if (!candidates)
            return { records: records, partialTail: partialTail, malformed: 1, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
        for (var _i = 0, candidates_1 = candidates; _i < candidates_1.length; _i++) {
            var candidate = candidates_1[_i];
            if (!isRecord(candidate)) {
                malformed++;
                break;
            }
            if (records.length >= maxRecordsPerFile) {
                malformed++;
                break;
            }
            var encoded = Buffer.from(JSON.stringify(candidate), 'utf8');
            if (encoded.byteLength > maxLineBytes) {
                malformed++;
                break;
            }
            records.push(candidate);
            prefixHasher.update(encoded);
            prefixHasher.update(Buffer.from('\n'));
            prefixHashes.set(records.length, prefixHasher.copy().digest('hex'));
        }
        return { records: records, partialTail: partialTail, malformed: malformed, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
    }
    var start = 0;
    var stoppedOnMalformed = false;
    for (var offset = 0; offset < bytes.length; offset++) {
        if (bytes[offset] !== 0x0a)
            continue;
        var end = offset > start && bytes[offset - 1] === 0x0d ? offset - 1 : offset;
        var raw = bytes.subarray(start, end);
        start = offset + 1;
        if (raw.byteLength > maxLineBytes) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        if (raw.toString('utf8').trim() === '')
            continue;
        if (records.length >= maxRecordsPerFile) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        var decoded = void 0;
        try {
            decoded = JSON.parse(decoder.decode(raw));
        }
        catch (_c) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        var candidates = transcriptCandidates(decoded);
        if (!candidates || candidates.length !== 1 || !isRecord(candidates[0])) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        records.push(candidates[0]);
        prefixHasher.update(Buffer.from("".concat(JSON.stringify(candidates[0]), "\n")));
        prefixHashes.set(records.length, prefixHasher.copy().digest('hex'));
    }
    partialTail = !stoppedOnMalformed && start < bytes.length;
    return { records: records, partialTail: partialTail, malformed: malformed, prefixHashes: prefixHashes, completePrefixHash: prefixHasher.copy().digest('hex') };
}
function transcriptCandidates(value) {
    if (Array.isArray(value))
        return value;
    if (!isRecord(value))
        return undefined;
    for (var _i = 0, _a = ['messages', 'events', 'entries']; _i < _a.length; _i++) {
        var key = _a[_i];
        if (Array.isArray(value[key]))
            return value[key];
    }
    return [value];
}
function mapTranscriptRecord(record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k;
    var type = (_b = (_a = safeLabel(record.type, 96)) === null || _a === void 0 ? void 0 : _a.toLowerCase()) !== null && _b !== void 0 ? _b : '';
    var id = safeIdentifier(record.id, 512);
    var timestamp = timestampMs((_d = (_c = record.timestamp) !== null && _c !== void 0 ? _c : record.createdAt) !== null && _d !== void 0 ? _d : record.time, file.mtimeMs);
    var model = safeLabel((_j = (_h = (_g = (_f = (_e = record.modelId) !== null && _e !== void 0 ? _e : record.model) !== null && _f !== void 0 ? _f : record.newModel) !== null && _g !== void 0 ? _g : nestedString(record, 'data', 'model')) !== null && _h !== void 0 ? _h : nestedString(record, 'message', 'model')) !== null && _j !== void 0 ? _j : nestedString(record, 'message', 'modelId'), 128);
    var context = contextFrom(record, record.data, record.message);
    var events = [];
    if (type === 'message') {
        var message = isRecord(record.message) ? record.message : {
            role: record.role,
            text: record.text,
            content: record.content,
            model: model,
        };
        mapMessage(message, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
        return events;
    }
    if (type === 'user_message') {
        var message = { role: 'user', text: (_k = record.text) !== null && _k !== void 0 ? _k : record.message, content: record.content };
        mapMessage(message, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
        return events;
    }
    if (type === 'tool_call' || type === 'tool_use' || type === 'function_call') {
        var call = isRecord(record.toolCall) ? record.toolCall : isRecord(record.functionCall) ? record.functionCall :
            isRecord(record.data) ? record.data : record;
        var parsed = parseToolCall(call, id !== null && id !== void 0 ? id : "record-".concat(recordIndex));
        if (parsed.id)
            rememberTool(correlations, parsed.id, parsed.name, parsed.args);
        if (shouldEmit && parsed.name)
            events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context, 'tool.invoked', 'tool', 'Cursor tool invocation recovered from local transcript', "tool.call:".concat(id !== null && id !== void 0 ? id : recordIndex), { toolName: parsed.name, toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.args) }));
        return events;
    }
    if (type === 'tool_result' || type === 'tool_output' || type === 'function_call_output') {
        var result = isRecord(record.toolResult) ? record.toolResult : isRecord(record.functionResult) ? record.functionResult :
            isRecord(record.data) ? record.data : record;
        var parsed = parseToolResult(result, id !== null && id !== void 0 ? id : "record-".concat(recordIndex), correlations);
        if (shouldEmit && parsed.name) {
            var classified = classifyTool(parsed.name, parsed.args, parsed.failed);
            events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context, classified.action, classified.category, "Cursor ".concat(classified.label, " recovered from local transcript"), "tool.result:".concat(id !== null && id !== void 0 ? id : recordIndex), { severity: parsed.failed ? 'medium' : 'info', toolName: parsed.name,
                toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.output) }));
        }
        return events;
    }
    if (type === 'error' && shouldEmit) {
        events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context, 'tool.failed', 'tool', 'Cursor transcript records an agent error', "error:".concat(id !== null && id !== void 0 ? id : recordIndex), { severity: 'medium' }));
        return events;
    }
    if (type === 'system_event' && shouldEmit) {
        events.push(makeEvent(record, fileHash, recordIndex, id, timestamp, file, model, context, 'session.compacting', 'session', 'Cursor transcript records session compaction', "system:".concat(id !== null && id !== void 0 ? id : recordIndex)));
        return events;
    }
    var role = normalizeRole(firstString(record.role, nestedString(record, 'message', 'role')));
    if (role) {
        mapMessage(record, id, timestamp, context, model, record, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events);
    }
    return events;
}
function mapMessage(message, recordId, timestamp, context, model, source, recordIndex, file, fileHash, correlations, includeContent, shouldEmit, events) {
    var role = normalizeRole(firstString(message.role));
    if (role === 'user') {
        var text = cleanUserText(messageText(message));
        if (shouldEmit)
            pushTextEvent(events, source, fileHash, recordIndex, recordId, timestamp, file, model, context, 'prompt.submitted', 'prompt', text, includeContent, 'user');
        return;
    }
    if (role !== 'assistant')
        return;
    if (shouldEmit) {
        for (var _i = 0, _a = reasoningTexts(message).entries(); _i < _a.length; _i++) {
            var _b = _a[_i], index = _b[0], reasoning = _b[1];
            if (!reasoning.trim())
                continue;
            events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context, 'agent.reasoning', 'agent', 'Cursor reasoning digest recovered from local transcript', "reasoning:".concat(recordId !== null && recordId !== void 0 ? recordId : recordIndex, ":").concat(index), { content: contentDigest(reasoning, false) }));
        }
        var text = messageText(message);
        pushTextEvent(events, source, fileHash, recordIndex, recordId, timestamp, file, model, context, 'agent.message', 'agent', text, includeContent, 'assistant');
    }
    for (var _c = 0, _d = toolCalls(message).entries(); _c < _d.length; _c++) {
        var _e = _d[_c], callIndex = _e[0], value = _e[1];
        var parsed = parseToolCall(value, "".concat(recordId !== null && recordId !== void 0 ? recordId : recordIndex, ":tool:").concat(callIndex));
        if (parsed.id)
            rememberTool(correlations, parsed.id, parsed.name, parsed.args);
        if (shouldEmit && parsed.name)
            events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context, 'tool.invoked', 'tool', 'Cursor tool invocation recovered from local transcript', "tool.call:".concat(recordId !== null && recordId !== void 0 ? recordId : recordIndex, ":").concat(callIndex), { toolName: parsed.name, toolCallHash: parsed.id ? sha256(parsed.id) : undefined, content: digestContent(parsed.args) }));
    }
}
function parseToolCall(value, fallbackId) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j;
    var id = (_c = safeIdentifier((_b = (_a = value.id) !== null && _a !== void 0 ? _a : value.toolCallId) !== null && _b !== void 0 ? _b : value.callId, 512)) !== null && _c !== void 0 ? _c : fallbackId;
    var name = (_f = safeLabel((_e = (_d = value.name) !== null && _d !== void 0 ? _d : value.toolName) !== null && _e !== void 0 ? _e : value.tool, 128)) !== null && _f !== void 0 ? _f : '';
    return { id: id, name: name, args: parseArguments((_j = (_h = (_g = value.args) !== null && _g !== void 0 ? _g : value.arguments) !== null && _h !== void 0 ? _h : value.params) !== null && _j !== void 0 ? _j : value.input) };
}
function parseToolResult(value, fallbackId, correlations) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m;
    var id = (_c = safeIdentifier((_b = (_a = value.toolCallId) !== null && _a !== void 0 ? _a : value.callId) !== null && _b !== void 0 ? _b : value.id, 512)) !== null && _c !== void 0 ? _c : fallbackId;
    var call = correlations.get(id);
    var name = (_g = safeLabel((_f = (_e = (_d = value.toolName) !== null && _d !== void 0 ? _d : value.name) !== null && _e !== void 0 ? _e : value.tool) !== null && _f !== void 0 ? _f : call === null || call === void 0 ? void 0 : call.name, 128)) !== null && _g !== void 0 ? _g : '';
    var status = (_h = boundedString(value.status, 48)) === null || _h === void 0 ? void 0 : _h.toLowerCase();
    var failed = value.success === false || ['error', 'failed', 'failure'].includes(status !== null && status !== void 0 ? status : '');
    var output = (_m = (_l = (_k = (_j = value.output) !== null && _j !== void 0 ? _j : value.result) !== null && _k !== void 0 ? _k : value.content) !== null && _l !== void 0 ? _l : value.text) !== null && _m !== void 0 ? _m : value.data;
    if (call)
        correlations.delete(id);
    return { id: id, name: name, args: call ? { path: call.pathArgument, command: call.commandArgument } : {}, output: output, failed: failed };
}
function rememberTool(correlations, id, name, args) {
    if (correlations.size >= maxToolCorrelations && !correlations.has(id))
        correlations.delete(correlations.keys().next().value);
    var value = isRecord(args) ? args : {};
    var pathArgument = ['path', 'file_path', 'filePath', 'targetFile', 'target_file', 'target_path', 'relativeWorkspacePath']
        .some(function (key) { return typeof value[key] === 'string' && value[key].trim() !== ''; });
    var commandArgument = ['command', 'cmd', 'commandLine'].some(function (key) { return typeof value[key] === 'string' && value[key].trim() !== ''; });
    correlations.set(id, { name: name, pathArgument: pathArgument, commandArgument: commandArgument });
}
function classifyTool(name, args, failed) {
    var lower = name.toLowerCase();
    var shape = isRecord(args) ? args : {};
    var pathPresent = shape.path === true || ['path', 'file_path', 'filePath', 'targetFile', 'target_file', 'target_path', 'relativeWorkspacePath']
        .some(function (key) { return typeof shape[key] === 'string' && shape[key].trim() !== ''; });
    var commandPresent = shape.command === true || ['command', 'cmd', 'commandLine']
        .some(function (key) { return typeof shape[key] === 'string' && shape[key].trim() !== ''; });
    if (failed)
        return { action: 'tool.failed', category: 'tool', label: 'tool failure' };
    if (lower.startsWith('mcp_') || lower.includes('__'))
        return { action: 'mcp.tool_invoked', category: 'mcp', label: 'MCP tool result' };
    if (commandPresent || ['terminal', 'command', 'bash', 'shell'].some(function (part) { return lower.includes(part); }))
        return { action: 'command.executed', category: 'command', label: 'command result' };
    if (pathPresent) {
        if (['read', 'grep', 'search', 'list'].some(function (part) { return lower.includes(part); }))
            return { action: 'file.read', category: 'file', label: 'file activity' };
        if (['write', 'create'].some(function (part) { return lower.includes(part); }))
            return { action: 'file.created', category: 'file', label: 'file activity' };
        if (['edit', 'delete', 'apply'].some(function (part) { return lower.includes(part); }))
            return { action: 'file.modified', category: 'file', label: 'file activity' };
    }
    return { action: 'tool.completed', category: 'tool', label: 'tool result' };
}
function toolCalls(message) {
    var out = [];
    for (var _i = 0, _a = ['toolCalls', 'tool_calls']; _i < _a.length; _i++) {
        var key = _a[_i];
        if (!Array.isArray(message[key]))
            continue;
        for (var _b = 0, _c = message[key].slice(0, 500); _b < _c.length; _b++) {
            var item = _c[_b];
            if (isRecord(item))
                out.push(item);
        }
    }
    return out;
}
function messageText(message) {
    var _a;
    for (var _i = 0, _b = [message.text, message.content, message.message]; _i < _b.length; _i++) {
        var value = _b[_i];
        if (typeof value === 'string' && value.trim())
            return value;
    }
    var parts = [];
    for (var _c = 0, _d = normalizeBlocks(message.content); _c < _d.length; _c++) {
        var block = _d[_c];
        var type = ((_a = safeLabel(block.type, 64)) !== null && _a !== void 0 ? _a : 'text').toLowerCase();
        if (type !== 'text')
            continue;
        var text = firstText(block.text, block.content);
        if (text.trim())
            parts.push(text);
    }
    return parts.join('\n').trim();
}
function reasoningTexts(message) {
    var _a;
    var out = [];
    for (var _i = 0, _b = [message.thinking, message.reasoning]; _i < _b.length; _i++) {
        var value = _b[_i];
        if (typeof value === 'string' && value.trim())
            out.push(value);
    }
    for (var _c = 0, _d = normalizeBlocks(message.content); _c < _d.length; _c++) {
        var block = _d[_c];
        var type = ((_a = safeLabel(block.type, 64)) !== null && _a !== void 0 ? _a : 'text').toLowerCase();
        if (type === 'thinking' || type === 'reasoning') {
            var text = firstText(block.thinking, block.text, block.content);
            if (text.trim())
                out.push(text);
        }
    }
    return out;
}
function normalizeBlocks(value) {
    if (Array.isArray(value))
        return value.filter(isRecord);
    if (isRecord(value))
        return [value];
    return [];
}
function normalizeRole(value) {
    var role = value.toLowerCase();
    if (role === 'user' || role === 'human')
        return 'user';
    if (role === 'assistant' || role === 'agent' || role === 'model')
        return 'assistant';
    return '';
}
function cleanUserText(text) {
    return text.replace(/<user_query>\s*([\s\S]*?)\s*<\/user_query>/g, '$1').trim();
}
function contextFrom() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    var usedKeys = ['context_tokens', 'contextTokens', 'contextTokenCount', 'context_usage_tokens', 'contextUsageTokens', 'context_used_tokens', 'contextUsedTokens', 'used_context_tokens', 'usedContextTokens'];
    var limitKeys = ['context_window_size', 'contextWindowSize', 'context_window_tokens', 'contextWindowTokens', 'context_token_budget', 'contextTokenBudget', 'max_context_tokens', 'maxContextTokens', 'context_limit', 'contextLimit', 'context_limit_tokens', 'contextLimitTokens'];
    var used;
    var limit;
    var visit = function (value, depth) {
        if (depth > 4 || value === null || typeof value !== 'object')
            return;
        if (Array.isArray(value)) {
            for (var _i = 0, value_1 = value; _i < value_1.length; _i++) {
                var child = value_1[_i];
                visit(child, depth + 1);
            }
            return;
        }
        var object = value;
        if (used === undefined)
            used = firstInteger(object, usedKeys);
        if (limit === undefined)
            limit = firstInteger(object, limitKeys);
        if (used !== undefined && limit !== undefined)
            return;
        for (var _a = 0, _b = Object.entries(object); _a < _b.length; _a++) {
            var _c = _b[_a], key = _c[0], child = _c[1];
            if (key.toLowerCase().includes('context') || key === 'data' || key === 'message')
                visit(child, depth + 1);
        }
    };
    for (var _a = 0, values_1 = values; _a < values_1.length; _a++) {
        var value = values_1[_a];
        visit(value, 0);
    }
    return __assign(__assign({}, (used !== undefined ? { used: used } : {})), (limit !== undefined ? { limit: limit } : {}));
}
function firstInteger(object, keys) {
    for (var _i = 0, keys_1 = keys; _i < keys_1.length; _i++) {
        var key = keys_1[_i];
        var value = object[key];
        var number = typeof value === 'number' ? value : typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : NaN;
        if (Number.isSafeInteger(number) && number > 0)
            return number;
    }
    return undefined;
}
function pushTextEvent(events, source, fileHash, recordIndex, recordId, timestamp, file, model, context, action, category, text, includeContent, role) {
    var value = text.trim();
    if (!value)
        return;
    var included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
    events.push(makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context, action, category, role === 'user' ? 'Cursor prompt recovered from local transcript' : 'Cursor assistant message recovered from local transcript', "".concat(role, ":").concat(recordId !== null && recordId !== void 0 ? recordId : recordIndex), __assign({ content: contentDigest(value, included, includeContent) }, (included ? { text: value } : {}))));
}
function makeEvent(source, fileHash, recordIndex, recordId, timestamp, file, model, context, action, category, message, suffix, extra) {
    var _a, _b;
    if (extra === void 0) { extra = {}; }
    var event = {
        timestamp: timestamp,
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(fileHash, ":").concat(recordIndex, ":").concat(recordId ? sha256(recordId) : '', ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_a = extra.severity) !== null && _a !== void 0 ? _a : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'cursor', collection_method: 'poll' },
        session: { id: "cursor:".concat(fileHash) },
        raw: __assign(__assign({ source: 'cursor_agent_transcript', source_sha256: fileHash, record_index: recordIndex, record_type: (_b = safeLabel(source.type, 96)) !== null && _b !== void 0 ? _b : 'unknown' }, (recordId ? { record_id_sha256: sha256(recordId) } : {})), { message: message }),
    };
    if (model)
        event.model = model;
    if (context.used !== undefined || context.limit !== undefined) {
        event.gen_ai = { context: __assign(__assign({}, (context.used !== undefined ? { used_tokens: context.used } : {})), (context.limit !== undefined ? { limit_tokens: context.limit } : {})) };
    }
    if (extra.toolName)
        event.tool = { name: extra.toolName };
    if (extra.toolCallHash)
        event.raw = __assign(__assign({}, event.raw), { tool_call_sha256: extra.toolCallHash });
    if (extra.content)
        event.content = extra.content;
    if (extra.text !== undefined) {
        if (action === 'prompt.submitted') {
            event.prompt = { text: extra.text };
            event.gen_ai = __assign(__assign({}, event.gen_ai), { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: extra.text }] }] } });
        }
        else {
            event.gen_ai = __assign(__assign({}, event.gen_ai), { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: extra.text }] }] } });
        }
    }
    return event;
}
function digestContent(value) {
    if (value === null || value === undefined)
        return { retention: 'metadata', included: false };
    var text;
    try {
        text = typeof value === 'string' ? value : JSON.stringify(value);
    }
    catch (_a) {
        return { retention: 'metadata', included: false };
    }
    if (typeof text !== 'string')
        return { retention: 'metadata', included: false };
    return { retention: 'metadata', included: false, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') };
}
function contentDigest(text, included, requested) {
    if (requested === void 0) { requested = false; }
    return __assign({ retention: included ? 'full' : 'metadata', included: included, hash: sha256(text), bytes: Buffer.byteLength(text, 'utf8') }, (requested && !included ? { truncated: true } : {}));
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
                        var key, previous, bytes, parsed_1, cursorIndex, correlations, groups, fileEventCount, index, recordIndex, shouldEmit, events, _e, groups_1, group, _f, _g, event_1, batch_1, batchEvents_1, failed, flush, _h, groups_2, group, _j, _k;
                        return __generator(this, function (_l) {
                            switch (_l.label) {
                                case 0:
                                    key = sha256(file.key);
                                    previous = state.files[key];
                                    _l.label = 1;
                                case 1:
                                    _l.trys.push([1, 14, , 15]);
                                    return [4 /*yield*/, readTranscriptFile(file, root)];
                                case 2:
                                    bytes = _l.sent();
                                    totalBytes += bytes.byteLength;
                                    if (totalBytes > maxTotalBytes) {
                                        result.files_truncated = true;
                                        result.errors.push({ source_sha256: key, message: 'Cursor history sync exceeded the total source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    parsed_1 = parseTranscript(bytes, file.key);
                                    result.malformed_records += parsed_1.malformed;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: key, message: 'Cursor transcript contains a malformed record; scanning stops before it' });
                                    if (parsed_1.partialTail)
                                        result.partial_sessions++;
                                    cursorIndex = (_a = previous === null || previous === void 0 ? void 0 : previous.record_index) !== null && _a !== void 0 ? _a : 0;
                                    if (previous && (parsed_1.records.length < cursorIndex || parsed_1.prefixHashes.get(cursorIndex) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: key, message: 'Cursor transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    correlations = new Map();
                                    groups = [];
                                    fileEventCount = 0;
                                    for (index = 0; index < parsed_1.records.length; index++) {
                                        recordIndex = index + 1;
                                        shouldEmit = recordIndex > cursorIndex;
                                        events = mapTranscriptRecord(parsed_1.records[index], recordIndex, file, key, correlations, (_b = options.includeContent) !== null && _b !== void 0 ? _b : false, shouldEmit);
                                        if (!shouldEmit || events.length === 0)
                                            continue;
                                        if (events.length > batchSize)
                                            throw new Error("Cursor transcript record ".concat(recordIndex, " maps to too many events"));
                                        groups.push({ recordIndex: recordIndex, events: events });
                                        fileEventCount += events.length;
                                        if (fileEventCount > maxEventsPerFile)
                                            throw new Error("Cursor transcript exceeds ".concat(maxEventsPerFile, " events"));
                                    }
                                    if (fileEventCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: key, message: 'Cursor sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += fileEventCount;
                                        (_c = result.events) !== null && _c !== void 0 ? _c : (result.events = []);
                                        for (_e = 0, groups_1 = groups; _e < groups_1.length; _e++) {
                                            group = groups_1[_e];
                                            for (_f = 0, _g = group.events; _f < _g.length; _f++) {
                                                event_1 = _g[_f];
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
                                        var send, lastIndex, prefix;
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
                                                    lastIndex = batch_1[batch_1.length - 1].recordIndex;
                                                    prefix = parsed_1.prefixHashes.get(lastIndex);
                                                    if (!prefix)
                                                        throw new Error('Cursor transcript checkpoint prefix is unavailable');
                                                    state.files[key] = makeCursor(lastIndex, file, prefix);
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
                                    _l.label = 3;
                                case 3:
                                    _l.trys.push([3, 10, , 11]);
                                    _h = 0, groups_2 = groups;
                                    _l.label = 4;
                                case 4:
                                    if (!(_h < groups_2.length)) return [3 /*break*/, 8];
                                    group = groups_2[_h];
                                    if (!(batchEvents_1 > 0 && batchEvents_1 + group.events.length > batchSize)) return [3 /*break*/, 6];
                                    return [4 /*yield*/, flush()];
                                case 5:
                                    _l.sent();
                                    _l.label = 6;
                                case 6:
                                    batch_1.push(group);
                                    batchEvents_1 += group.events.length;
                                    _l.label = 7;
                                case 7:
                                    _h++;
                                    return [3 /*break*/, 4];
                                case 8: return [4 /*yield*/, flush()];
                                case 9:
                                    _l.sent();
                                    return [3 /*break*/, 11];
                                case 10:
                                    _j = _l.sent();
                                    failed = true;
                                    result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Cursor history events' });
                                    return [3 /*break*/, 11];
                                case 11:
                                    if (failed)
                                        return [2 /*return*/, "continue"];
                                    if (!(parsed_1.records.length > cursorIndex)) return [3 /*break*/, 13];
                                    state.files[key] = makeCursor(parsed_1.records.length, file, parsed_1.completePrefixHash);
                                    result.sessions_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 13];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 12:
                                    _l.sent();
                                    _l.label = 13;
                                case 13: return [3 /*break*/, 15];
                                case 14:
                                    _k = _l.sent();
                                    result.errors.push({ source_sha256: key, message: 'Cursor transcript could not be read or checkpointed safely' });
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
function makeCursor(recordIndex, file, prefix) {
    return { record_index: recordIndex, size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix };
}
function timestampMs(value, fallbackMs) {
    var date;
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
        date = new Date(value < 1000000000000 ? value * 1000 : value);
    }
    else if (typeof value === 'string') {
        var numeric = Number(value);
        date = value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0
            ? new Date(numeric < 1000000000000 ? numeric * 1000 : numeric)
            : new Date(value);
    }
    return date && !Number.isNaN(date.getTime()) ? date.toISOString() : new Date(fallbackMs).toISOString();
}
function firstText() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_2 = values; _a < values_2.length; _a++) {
        var value = values_2[_a];
        if (typeof value === 'string' && value.length > 0)
            return value;
    }
    return '';
}
function firstString() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_3 = values; _a < values_3.length; _a++) {
        var value = values_3[_a];
        var text = boundedString(value, 2048);
        if (text)
            return text;
    }
    return '';
}
function nestedString(value, outer, inner) {
    return isRecord(value) && isRecord(value[outer]) ? boundedString(value[outer][inner], 128) : undefined;
}
function safeLabel(value, maximum) {
    var text = boundedString(value, maximum);
    if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text))
        return undefined;
    return text;
}
function safeIdentifier(value, maximum) { return boundedString(value, maximum); }
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
function boundedString(value, maximum) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}
function boundedInteger(value, minimum, maximum, label) {
    if (!Number.isSafeInteger(value) || value < minimum || value > maximum)
        throw new Error("".concat(label, " must be an integer between ").concat(minimum, " and ").concat(maximum));
    return value;
}
function nonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function isNonNegativeInteger(value) { return nonNegativeInteger(value) !== undefined; }
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
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
                        throw new Error('Cursor history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Cursor transcript cursor');
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
        var empty, info, handle, opened, bytes, decoded, files, _i, _a, _b, key, value, error_3;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.cursor-transcript-cursor.v1', version: stateVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('Cursor transcript cursor must be a bounded regular file');
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
                        throw new Error('Cursor transcript cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxStateBytes)
                        throw new Error('Cursor transcript cursor exceeds the local size limit');
                    decoded = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(decoded) || decoded.schema_version !== empty.schema_version || decoded.version !== stateVersion || !isRecord(decoded.files)) {
                        throw new Error('Cursor transcript cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(decoded.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.record_index) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256)) {
                            throw new Error('Cursor transcript cursor contains an invalid checkpoint');
                        }
                        files[key] = { record_index: value.record_index, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, prefix_sha256: value.prefix_sha256 };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Cursor transcript cursor'))
                        throw error_3;
                    throw new Error('Cursor transcript cursor is malformed or unreadable');
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
                            throw new Error('unable to create private Cursor transcript cursor directory'); })];
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
                        throw new Error('Cursor transcript cursor path cannot contain symbolic links or non-directory parents');
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
                        throw new Error('Cursor transcript cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write Cursor transcript cursor');
                case 11: return [2 /*return*/];
            }
        });
    });
}
