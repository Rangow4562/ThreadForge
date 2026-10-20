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
exports.getPrimeHistoryStatus = getPrimeHistoryStatus;
exports.syncPrimeHistory = syncPrimeHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var cursorVersion = 1;
var cursorName = 'prime-session-cursor.v1.json';
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
function getPrimeHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, _a, roots, statePath, listed, state, checkpointed;
        var _b;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    maximum = boundedInteger((_b = options.maxFiles) !== null && _b !== void 0 ? _b : defaultMaxFiles, 1, maxFiles, '--max-files');
                    return [4 /*yield*/, resolveOptions(options)];
                case 1:
                    _a = _c.sent(), roots = _a.roots, statePath = _a.statePath;
                    return [4 /*yield*/, listFiles(roots, maximum)];
                case 2:
                    listed = _c.sent();
                    return [4 /*yield*/, readState(statePath)];
                case 3:
                    state = _c.sent();
                    checkpointed = listed.files.filter(function (file) {
                        var cursor = state.files[file.sourceSha256];
                        return cursor && !cursor.incomplete && (cursor.last_line > 0 || cursor.started) &&
                            cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
                    }).length;
                    return [2 /*return*/, {
                            schema_version: 'threadforge.prime-history-status.v1', files: listed.files.length,
                            session_files: listed.files.filter(function (file) { return file.kind === 'session'; }).length,
                            artifact_files: listed.files.filter(function (file) { return file.kind === 'artifact'; }).length,
                            checkpointed_files: checkpointed, pending_files: listed.files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped, files_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncPrimeHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var _a, roots, statePath, listed, result, _b, _c;
        var _this = this;
        var _d, _e;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_f) {
            switch (_f.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveOptions(options)];
                case 1:
                    _a = _f.sent(), roots = _a.roots, statePath = _a.statePath;
                    return [4 /*yield*/, listFiles(roots, (_d = options.maxFiles) !== null && _d !== void 0 ? _d : defaultMaxFiles)];
                case 2:
                    listed = _f.sent();
                    result = emptyResult((_e = options.includeContent) !== null && _e !== void 0 ? _e : false);
                    result.files = listed.files.length;
                    result.session_files = listed.files.filter(function (file) { return file.kind === 'session'; }).length;
                    result.artifact_files = listed.files.filter(function (file) { return file.kind === 'artifact'; }).length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.files_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    _b = processFiles;
                    _c = [listed.files, roots];
                    return [4 /*yield*/, readState(statePath)];
                case 3: return [4 /*yield*/, _b.apply(void 0, _c.concat([_f.sent(), options, result, false]))];
                case 4:
                    _f.sent();
                    return [3 /*break*/, 7];
                case 5: return [4 /*yield*/, withStateLock(statePath, function () { return __awaiter(_this, void 0, void 0, function () {
                        var state;
                        return __generator(this, function (_a) {
                            switch (_a.label) {
                                case 0: return [4 /*yield*/, readState(statePath)];
                                case 1:
                                    state = _a.sent();
                                    return [4 /*yield*/, processFiles(listed.files, roots, state, options, result, true, statePath)];
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
                    _f.sent();
                    _f.label = 7;
                case 7:
                    result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_files === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return { schema_version: 'threadforge.prime-history-sync.v1', files: 0, session_files: 0, artifact_files: 0,
        files_changed: 0, events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_files: 0,
        symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false, preview_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false, errors: [] };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : defaultMaxFiles, 1, maxFiles, '--max-files');
    if (options.sessionsDir !== undefined && !node_path_1.default.isAbsolute(options.sessionsDir))
        throw new Error('--sessions-dir must be an absolute path');
    if (options.artifactsDir !== undefined && !node_path_1.default.isAbsolute(options.artifactsDir))
        throw new Error('--artifacts-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveOptions(options) {
    return __awaiter(this, void 0, void 0, function () {
        var home, override, agentDir, roots, _i, _a, _b, kind, value, fallback, _c, _d, statePath, _e, roots_1, root;
        var _f;
        var _g;
        return __generator(this, function (_h) {
            switch (_h.label) {
                case 0:
                    home = node_os_1.default.homedir();
                    override = (_g = process.env.PRIME_AGENT_CODING_AGENT_DIR) === null || _g === void 0 ? void 0 : _g.trim();
                    agentDir = override ? expandTilde(override, home) : node_path_1.default.join(home, '.prime', 'agent');
                    if (!node_path_1.default.isAbsolute(agentDir))
                        throw new Error('PRIME_AGENT_CODING_AGENT_DIR must resolve to an absolute path');
                    roots = [];
                    _i = 0, _a = [
                        ['session', options.sessionsDir, node_path_1.default.join(agentDir, 'sessions')],
                        ['artifact', options.artifactsDir, node_path_1.default.join(agentDir, 'session-artifacts')],
                    ];
                    _h.label = 1;
                case 1:
                    if (!(_i < _a.length)) return [3 /*break*/, 4];
                    _b = _a[_i], kind = _b[0], value = _b[1], fallback = _b[2];
                    _d = (_c = roots).push;
                    _f = {};
                    return [4 /*yield*/, resolveRoot(value !== null && value !== void 0 ? value : fallback, "Prime ".concat(kind, " directory"))];
                case 2:
                    _d.apply(_c, [(_f.path = _h.sent(), _f.kind = kind, _f)]);
                    _h.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4:
                    if (isWithin(roots[0].path, roots[1].path) || isWithin(roots[1].path, roots[0].path)) {
                        throw new Error('Prime sessions and artifacts directories must not overlap');
                    }
                    statePath = resolveStatePath(options.statePath);
                    for (_e = 0, roots_1 = roots; _e < roots_1.length; _e++) {
                        root = roots_1[_e];
                        if (isWithin(root.path, statePath))
                            throw new Error('Prime history cursor must be stored outside Prime data directories');
                    }
                    return [2 /*return*/, { roots: roots, statePath: statePath }];
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
function resolveRoot(value, label) {
    return __awaiter(this, void 0, void 0, function () {
        var resolved, info, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(value))
                        throw new Error("".concat(label, " must be an absolute path"));
                    resolved = node_path_1.default.resolve(value);
                    return [4 /*yield*/, (0, promises_1.lstat)(resolved).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, resolved];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error("".concat(label, " must be a real directory"));
                    return [4 /*yield*/, (0, promises_1.realpath)(resolved).catch(function () { throw new Error("".concat(label, " could not be resolved")); })];
                case 2:
                    canonical = _a.sent();
                    if (canonical !== resolved)
                        throw new Error("".concat(label, " must not traverse symbolic links"));
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listFiles(roots, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var files, entriesScanned, symlinksSkipped, oversizedSkipped, truncated, scanLimitReached, _i, roots_2, root, pending, rootFileCount, rootLimitReached, directory, entries, _a, entries_1, entry, fullPath, info, canonical, relative;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    files = [];
                    entriesScanned = 0;
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    scanLimitReached = false;
                    _i = 0, roots_2 = roots;
                    _b.label = 1;
                case 1:
                    if (!(_i < roots_2.length)) return [3 /*break*/, 11];
                    root = roots_2[_i];
                    pending = [root.path];
                    rootFileCount = 0;
                    rootLimitReached = false;
                    _b.label = 2;
                case 2:
                    if (!(pending.length > 0 && !scanLimitReached && !rootLimitReached)) return [3 /*break*/, 9];
                    directory = pending.pop();
                    return [4 /*yield*/, (0, promises_1.readdir)(directory, { withFileTypes: true }).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return [];
                            throw new Error('unable to enumerate Prime session data');
                        })];
                case 3:
                    entries = _b.sent();
                    entries.sort(function (left, right) { return left.name.localeCompare(right.name); });
                    _a = 0, entries_1 = entries;
                    _b.label = 4;
                case 4:
                    if (!(_a < entries_1.length)) return [3 /*break*/, 8];
                    entry = entries_1[_a];
                    if (++entriesScanned > maxScanEntries) {
                        truncated = true;
                        scanLimitReached = true;
                        return [3 /*break*/, 8];
                    }
                    fullPath = node_path_1.default.join(directory, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(fullPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    info = _b.sent();
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
                    canonical = _b.sent();
                    if (!canonical || canonical !== node_path_1.default.resolve(fullPath) || !isWithin(root.path, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    relative = node_path_1.default.relative(root.path, canonical).split(node_path_1.default.sep).join('/');
                    if (!isSafeRelative(relative)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    if (rootFileCount >= maximum) {
                        truncated = true;
                        rootLimitReached = true;
                        return [3 /*break*/, 8];
                    }
                    files.push({ path: canonical, root: root.path, relative: relative, kind: root.kind,
                        sourceSha256: sha256("".concat(root.kind, ":").concat(relative)), size: info.size, mtimeMs: info.mtimeMs, name: entry.name });
                    rootFileCount++;
                    _b.label = 7;
                case 7:
                    _a++;
                    return [3 /*break*/, 4];
                case 8: return [3 /*break*/, 2];
                case 9:
                    if (scanLimitReached)
                        return [3 /*break*/, 11];
                    _b.label = 10;
                case 10:
                    _i++;
                    return [3 /*break*/, 1];
                case 11:
                    files.sort(function (left, right) { return left.mtimeMs - right.mtimeMs || left.kind.localeCompare(right.kind) || left.relative.localeCompare(right.relative); });
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function readSource(file, roots) {
    return __awaiter(this, void 0, void 0, function () {
        var root, before, canonical, handle, opened, bytes, after, current, error_1;
        var _a, _b;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    root = (_a = roots.find(function (item) { return item.kind === file.kind; })) === null || _a === void 0 ? void 0 : _a.path;
                    if (!root || root !== file.root || !isWithin(root, file.path))
                        throw new Error('Prime transcript source escaped its configured root');
                    return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 1:
                    before = _c.sent();
                    if (!before || !before.isFile() || before.isSymbolicLink())
                        throw new Error('Prime transcript source is no longer a regular file');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _c.sent();
                    if (!canonical || canonical !== file.path || !isWithin(root, canonical))
                        throw new Error('Prime transcript path escaped its source root');
                    _c.label = 3;
                case 3:
                    _c.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(file.path, node_fs_1.constants.O_RDONLY | ((_b = node_fs_1.constants.O_NOFOLLOW) !== null && _b !== void 0 ? _b : 0))];
                case 4:
                    handle = _c.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _c.sent();
                    if (!sameFile(before, opened) || opened.size !== file.size || opened.mtimeMs !== file.mtimeMs)
                        throw new Error('Prime transcript changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _c.sent();
                    return [4 /*yield*/, handle.stat()];
                case 7:
                    after = _c.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(file.path).catch(function () { return null; })];
                case 8:
                    current = _c.sent();
                    if (bytes.byteLength > maxFileBytes || !sameFile(opened, after) || !sameFile(opened, current) ||
                        after.size !== opened.size || after.mtimeMs !== opened.mtimeMs)
                        throw new Error('Prime transcript changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _c.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Prime transcript'))
                        throw error_1;
                    throw new Error('Prime transcript could not be opened safely');
                case 10: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _c.sent();
                    return [7 /*endfinally*/];
                case 12: return [2 /*return*/];
            }
        });
    });
}
function sameFile(left, right) {
    return right !== null && left.dev === right.dev && left.ino === right.ino;
}
function parsePrimeFile(bytes) {
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var hasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var entries = [];
    var offset = 0;
    var line = 0;
    var completeLines = 0;
    var malformed = 0;
    var partialTail = false;
    while (offset < bytes.length) {
        var newline = bytes.indexOf(0x0a, offset);
        if (newline < 0) {
            partialTail = true;
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
        var value = void 0;
        if (raw.toString('utf8').trim() !== '') {
            try {
                value = JSON.parse(decoder.decode(raw));
            }
            catch (_a) {
                malformed++;
                break;
            }
            if (!isRecord(value)) {
                malformed++;
                break;
            }
        }
        hasher.update(bytes.subarray(start, newline + 1));
        completeLines = line;
        prefixHashes.set(line, hasher.copy().digest('hex'));
        if (isRecord(value))
            entries.push({ line: line, value: value });
    }
    return { entries: entries, completeLines: completeLines, prefixHashes: prefixHashes, partialTail: partialTail, malformed: malformed };
}
function mapPrimeEntries(file, parsed, minLine, started, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g;
    var header = (_b = (_a = parsed.entries.find(function (entry) { return typeOf(entry.value) === 'session'; })) === null || _a === void 0 ? void 0 : _a.value) !== null && _b !== void 0 ? _b : {};
    var sessionId = (_c = boundedString(header.id, 512)) !== null && _c !== void 0 ? _c : node_path_1.default.basename(file.name, node_path_1.default.extname(file.name));
    var git = isRecord(header.git) ? header.git : {};
    var state = __assign(__assign(__assign(__assign(__assign(__assign({ sessionIdHash: sha256(sessionId) }, (boundedString(header.cwd, 4096) ? { cwd: String(header.cwd) } : {})), (safeLabel((_d = header.model) !== null && _d !== void 0 ? _d : header.modelId, 160) ? { model: safeLabel((_e = header.model) !== null && _e !== void 0 ? _e : header.modelId, 160) } : {})), (boundedString(git.repoUrl, 2048) ? { repository: String(git.repoUrl) } : {})), (safeLabel(git.branch, 256) ? { branch: safeLabel(git.branch, 256) } : {})), (boundedString(header.parentSession, 512) ? { parentHash: sha256(String(header.parentSession)) } : {})), { calls: new Map(), started: started });
    var groups = [];
    if (!started && parsed.entries.length > 0) {
        var event_1 = primeEvent(file, 0, 'session.started', 'session.started', 'session', 'Prime Agent session start recovered from local transcript', state, timestampValue(header.timestamp, file.mtimeMs), { prime: { source_kind: file.kind } });
        groups.push({ line: 0, prefix: sha256(Buffer.alloc(0)), started: true, events: [event_1] });
        state.started = true;
    }
    var eventCount = (_g = (_f = groups[0]) === null || _f === void 0 ? void 0 : _f.events.length) !== null && _g !== void 0 ? _g : 0;
    for (var _i = 0, _h = parsed.entries; _i < _h.length; _i++) {
        var entry = _h[_i];
        var events = [];
        mapPrimeEntry(events, file, entry, state, entry.line > minLine, includeContent);
        eventCount += events.length;
        if (eventCount > maxEventsPerFile)
            throw new Error("Prime transcript exceeds the ".concat(maxEventsPerFile, "-event limit"));
        var prefix = parsed.prefixHashes.get(entry.line);
        if (prefix)
            groups.push({ line: entry.line, prefix: prefix, started: state.started, events: events });
    }
    return { groups: groups, state: state };
}
function mapPrimeEntry(events, file, entry, state, emit, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g;
    var data = entry.value;
    var type = typeOf(data);
    var timestamp = timestampValue(data.timestamp, file.mtimeMs);
    if (type === 'session') {
        var cwd = boundedString(data.cwd, 4096);
        var model = safeLabel((_a = data.model) !== null && _a !== void 0 ? _a : data.modelId, 160);
        if (cwd)
            state.cwd = cwd;
        if (model)
            state.model = model;
        return;
    }
    if (type === 'model_change') {
        var model = safeLabel((_b = data.modelId) !== null && _b !== void 0 ? _b : data.model, 160);
        if (model) {
            state.model = model;
            if (emit)
                pushEvent(events, primeEvent(file, entry.line, 'model_change', 'session.context', 'session', 'Prime Agent model changed', state, timestamp, { prime: { change: 'model_change', model: model } }));
        }
        return;
    }
    if (type === 'compaction' && emit) {
        var summary = (_d = (_c = data.summary) !== null && _c !== void 0 ? _c : data.details) !== null && _d !== void 0 ? _d : data;
        pushEvent(events, primeEvent(file, entry.line, 'compaction', 'session.compacting', 'session', 'Prime Agent compacted session history', state, timestamp, { content: contentDigest(summary), prime: { source_event_type: 'compaction' } }));
        return;
    }
    if (type === 'custom' && data.customType === 'prime-agent.refinement' && emit) {
        pushEvent(events, primeEvent(file, entry.line, 'refinement', 'session.context', 'session', 'Prime Agent refinement state updated', state, timestamp, { content: contentDigest(data.data), prime: { custom_type: 'prime-agent.refinement' } }));
        return;
    }
    if (type === 'custom_message' && emit) {
        pushEvent(events, primeEvent(file, entry.line, 'custom_message', 'agent.message', 'session', 'Prime Agent custom message observed', state, timestamp, { content: contentDigest(data) }));
        return;
    }
    if (type === 'message') {
        if (isRecord(data.message))
            mapPrimeMessage(events, file, entry.line, data.message, timestamp, state, emit, includeContent);
        return;
    }
    if (type === 'tool_call') {
        var call = makePrimeCall(entry.line, 0, data);
        rememberPrimeCall(state.calls, call);
        if (emit)
            pushEvent(events, primeToolEvent(file, entry.line, 'tool_call', 'tool.invoked', 'tool', 'Prime Agent tool invoked', state, timestamp, call));
    }
    else if (type === 'tool_result' && emit) {
        mapPrimeToolResult(events, file, entry.line, timestamp, state, data, (_g = safeOpaque((_f = (_e = data.toolCallId) !== null && _e !== void 0 ? _e : data.callId) !== null && _f !== void 0 ? _f : data.id, 512)) !== null && _g !== void 0 ? _g : "line-".concat(entry.line));
    }
    else if (type === 'error' && emit)
        mapPrimeError(events, file, entry.line, timestamp, state, data);
}
function mapPrimeMessage(events, file, line, message, fallback, state, emit, includeContent) {
    var _a, _b, _c, _d, _e, _f;
    var role = String((_a = message.role) !== null && _a !== void 0 ? _a : '').toLowerCase();
    var timestamp = timestampValue(message.timestamp, fallback);
    if (role === 'user') {
        if (emit)
            textParts(message.content).forEach(function (text, index) { return pushEvent(events, textEvent(file, line, "user.".concat(index), 'prompt.submitted', 'prompt', 'Prime Agent prompt recovered from local transcript', state, timestamp, text, includeContent, 'user')); });
        return;
    }
    if (role === 'assistant') {
        var model = safeLabel((_b = message.model) !== null && _b !== void 0 ? _b : message.modelId, 160);
        if (model)
            state.model = model;
        var parts = Array.isArray(message.content) ? message.content : typeof message.content === 'string' ? [{ type: 'text', text: message.content }] : [];
        for (var index = 0; index < parts.length; index++) {
            var part = isRecord(parts[index]) ? parts[index] : {};
            var type = String((_c = part.type) !== null && _c !== void 0 ? _c : '').toLowerCase();
            if (type === 'thinking') {
                var thinking = textValue((_d = part.thinking) !== null && _d !== void 0 ? _d : part.text);
                if (emit && thinking)
                    pushEvent(events, primeEvent(file, line, "assistant.".concat(index, ".thinking"), 'agent.reasoning', 'reasoning', 'Prime Agent reasoning observed', state, timestamp, { content: contentDigest(thinking) }));
            }
            else if (type === 'text') {
                var text = textValue(part.text);
                if (emit && text)
                    pushEvent(events, textEvent(file, line, "assistant.".concat(index, ".text"), 'agent.message', 'agent', 'Prime Agent assistant message recovered from local transcript', state, timestamp, text, includeContent, 'assistant'));
            }
            else if (type === 'toolcall' || type === 'tool_call') {
                var call = makePrimeCall(line, index, part);
                rememberPrimeCall(state.calls, call);
                if (emit)
                    pushEvent(events, primeToolEvent(file, line, "assistant.".concat(index, ".tool_call"), 'tool.invoked', 'tool', 'Prime Agent tool invoked', state, timestamp, call));
            }
        }
        if (emit && isRecord(message.usage))
            mapPrimeUsage(events, file, line, timestamp, state, message.usage);
        return;
    }
    if (['toolresult', 'tool_result', 'tool'].includes(role) && emit) {
        mapPrimeToolResult(events, file, line, timestamp, state, message, (_f = safeOpaque((_e = message.toolCallId) !== null && _e !== void 0 ? _e : message.callId, 512)) !== null && _f !== void 0 ? _f : "line-".concat(line));
    }
    else if (role === 'error' && emit)
        mapPrimeError(events, file, line, timestamp, state, message);
}
function makePrimeCall(line, index, value) {
    var _a, _b, _c, _d, _e, _f, _g;
    var id = (_c = safeOpaque((_b = (_a = value.id) !== null && _a !== void 0 ? _a : value.toolCallId) !== null && _b !== void 0 ? _b : value.callId, 512)) !== null && _c !== void 0 ? _c : "line-".concat(line, "-call-").concat(index);
    var name = (_e = safeLabel((_d = value.name) !== null && _d !== void 0 ? _d : value.toolName, 128)) !== null && _e !== void 0 ? _e : 'unknown';
    return { id: id, name: name, args: (_g = (_f = value.arguments) !== null && _f !== void 0 ? _f : value.args) !== null && _g !== void 0 ? _g : {} };
}
function rememberPrimeCall(calls, call) {
    if (calls.size >= maxCallCorrelations && !calls.has(call.id))
        calls.delete(calls.keys().next().value);
    calls.set(call.id, call);
}
function mapPrimeToolResult(events, file, line, timestamp, state, data, callId) {
    var _a, _b, _c, _d, _e;
    var remembered = state.calls.get(callId);
    var call = remembered !== null && remembered !== void 0 ? remembered : { id: callId, name: (_b = safeLabel((_a = data.toolName) !== null && _a !== void 0 ? _a : data.name, 128)) !== null && _b !== void 0 ? _b : 'unknown', args: {} };
    var output = textParts((_d = (_c = data.content) !== null && _c !== void 0 ? _c : data.output) !== null && _d !== void 0 ? _d : data.result).join('\n');
    var details = isRecord(data.details) ? data.details : {};
    var failed = data.isError === true || isErrorStatus(details.status) || isErrorStatus(data.status);
    var command = primeCommandFor(call.name, call.args);
    var action = failed ? 'tool.failed' : command ? 'command.executed' : 'tool.completed';
    var category = !failed && command ? 'command' : 'tool';
    var event = primeToolEvent(file, line, 'tool_result', action, category, 'Prime Agent tool result observed', state, timestamp, call, failed ? 'high' : 'info');
    if (failed)
        event.error = { type: 'tool_error' };
    if (output)
        event.content = contentDigest(output);
    if (command)
        event.command = __assign(__assign({ command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') }, (output ? { output_sha256: sha256(output), output_bytes: Buffer.byteLength(output, 'utf8') } : {})), (call.name.toLowerCase() === 'ipython' ? { language: 'python' } : {}));
    var primeRaw = __assign(__assign({}, (call.name.toLowerCase() === 'ipython' ? { python: true } : {})), (typeof data.status === 'string' ? { status: (_e = safeLabel(data.status, 32)) !== null && _e !== void 0 ? _e : 'unknown' } : {}));
    if (Object.keys(primeRaw).length)
        event.raw = __assign(__assign({}, event.raw), { prime: primeRaw });
    pushEvent(events, event);
    state.calls.delete(callId);
    var attachments = isRecord(details) && Array.isArray(details.attachments) ? details.attachments : [];
    for (var index = 0; index < attachments.length; index++) {
        var attachment = isRecord(attachments[index]) ? attachments[index] : {};
        var attachmentPath = boundedString(attachment.path, 4096);
        if (!attachmentPath)
            continue;
        var fileEvent = primeEvent(file, line, "attachment.".concat(index), 'file.read', 'file', 'Prime Agent context attachment observed', state, timestamp, { prime: __assign({ path_sha256: sha256(attachmentPath) }, (safeLabel(attachment.mimeType, 128) ? { mime_type: safeLabel(attachment.mimeType, 128) } : {})) });
        fileEvent.file = { path_sha256: sha256(attachmentPath), operation: 'read' };
        pushEvent(events, fileEvent);
    }
}
function primeCommandFor(name, args) {
    var _a;
    var object = isRecord(args) ? args : {};
    if (name.toLowerCase() === 'ipython')
        return boundedString(object.code, 1 << 20);
    if (name.toLowerCase() === 'bash')
        return boundedString(object.command, 1 << 20);
    return boundedString((_a = object.command) !== null && _a !== void 0 ? _a : object.cmd, 1 << 20);
}
function primeToolEvent(file, line, suffix, action, category, message, state, timestamp, call, severity) {
    var _a, _b, _c;
    if (severity === void 0) { severity = 'info'; }
    var name = (_a = safeLabel(call.name, 128)) !== null && _a !== void 0 ? _a : 'unknown';
    var callHash = sha256(call.id);
    var args = digestUnknown(call.args);
    var event = primeEvent(file, line, suffix, action, category, message, state, timestamp, __assign(__assign({ severity: severity }, (args ? { content: args } : {})), { prime: __assign({ tool_call_sha256: callHash }, (args ? { arguments: args } : {})) }));
    event.tool = { name: name };
    event.gen_ai = { operation: { name: 'execute_tool' }, tool: { name: name, call: __assign({ id: "prime-call:".concat(callHash) }, (args ? { arguments: args } : {})) } };
    var command = primeCommandFor(name, call.args);
    if (command)
        event.tool = { name: name, command_sha256: sha256(command) };
    var object = isRecord(call.args) ? call.args : {};
    var pathValue = boundedString((_c = (_b = object.path) !== null && _b !== void 0 ? _b : object.file_path) !== null && _c !== void 0 ? _c : object.target_path, 4096);
    if (pathValue)
        event.tool = __assign(__assign({}, (isRecord(event.tool) ? event.tool : { name: name })), { path_sha256: sha256(pathValue) });
    return event;
}
function mapPrimeError(events, file, line, timestamp, state, data) {
    var message = firstText(data.errorMessage, data.message, data.content) || textParts(data.content).join('\n') || 'Prime Agent runtime error observed';
    var event = primeEvent(file, line, 'error', 'agent.error', 'session', 'Prime Agent runtime error observed', state, timestamp, { severity: 'high', content: contentDigest(message), prime: { error_sha256: sha256(message), error_bytes: Buffer.byteLength(message, 'utf8') } });
    event.error = { type: 'runtime_error' };
    pushEvent(events, event);
}
function mapPrimeUsage(events, file, line, timestamp, state, value) {
    var usage = {};
    for (var _i = 0, _a = [['input', 'input_tokens'], ['output', 'output_tokens'], ['cacheRead', 'cache_read_input_tokens'],
        ['cacheWrite', 'cache_creation_input_tokens'], ['reasoning', 'reasoning_tokens']]; _i < _a.length; _i++) {
        var _b = _a[_i], source = _b[0], target = _b[1];
        var count = nonNegativeInteger(value[source]);
        if (count !== undefined)
            usage[target] = count;
    }
    var cost = isRecord(value.cost) ? finiteNonNegative(value.cost.total) : undefined;
    if (cost !== undefined)
        usage.cost_usd = cost;
    if (Object.keys(usage).length === 0)
        return;
    var event = primeEvent(file, line, 'usage', 'token.usage', 'metric', 'Prime Agent token usage recovered from local transcript', state, timestamp);
    event.gen_ai = { usage: usage };
    pushEvent(events, event);
}
function primeEvent(file, line, suffix, action, category, message, state, timestamp, extra) {
    var _a;
    if (extra === void 0) { extra = {}; }
    var event = {
        timestamp: timestamp,
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(file.sourceSha256, ":").concat(state.sessionIdHash, ":").concat(line, ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_a = extra.severity) !== null && _a !== void 0 ? _a : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'prime_agent', collection_method: 'poll' },
        session: { id: "prime:".concat(state.sessionIdHash), source_kind: file.kind },
        raw: __assign(__assign(__assign(__assign(__assign({ source: 'prime_session_jsonl', source_sha256: file.sourceSha256, source_kind: file.kind, line: line, message: message }, (state.cwd ? { cwd_sha256: sha256(state.cwd) } : {})), (state.repository ? { repository_sha256: sha256(state.repository) } : {})), (state.branch ? { branch_sha256: sha256(state.branch) } : {})), (state.parentHash ? { parent_session_sha256: state.parentHash } : {})), (extra.prime ? { prime: extra.prime } : {})),
    };
    if (state.model)
        event.model = state.model;
    if (extra.content)
        event.content = extra.content;
    return event;
}
function textEvent(file, line, suffix, action, category, message, state, timestamp, text, includeContent, role) {
    var included = includeContent && Buffer.byteLength(text, 'utf8') <= maxContentBytes;
    var event = primeEvent(file, line, suffix, action, category, message, state, timestamp, { content: contentDigest(text, included, includeContent) });
    if (included && role === 'user') {
        event.prompt = { text: text };
        event.gen_ai = { input: { messages: [{ role: 'user', parts: [{ type: 'text', content: text }] }] } };
    }
    else if (included)
        event.gen_ai = { output: { messages: [{ role: 'assistant', parts: [{ type: 'text', content: text }] }] } };
    return event;
}
function pushEvent(events, event) {
    if (events.length >= eventBatchSize)
        throw new Error("Prime transcript row exceeds the ".concat(eventBatchSize, "-event batch limit"));
    events.push(event);
}
function textParts(value) {
    if (typeof value === 'string')
        return value.trim() ? [value] : [];
    if (Array.isArray(value))
        return value.flatMap(function (item) { return textParts(item); });
    if (!isRecord(value))
        return [];
    if (typeof value.text === 'string')
        return value.text.trim() ? [value.text] : [];
    return [];
}
function digestUnknown(value) {
    var _a;
    if (value === undefined || value === null)
        return undefined;
    var serialized;
    try {
        serialized = typeof value === 'string' ? value : (_a = JSON.stringify(value)) !== null && _a !== void 0 ? _a : '';
    }
    catch (_b) {
        return undefined;
    }
    return { hash: sha256(serialized), bytes: Buffer.byteLength(serialized, 'utf8') };
}
function contentDigest(value, included, requested) {
    if (included === void 0) { included = false; }
    if (requested === void 0) { requested = false; }
    var digest = digestUnknown(value);
    return __assign(__assign({ retention: included ? 'full' : 'metadata', included: included }, (digest !== null && digest !== void 0 ? digest : {})), (requested && !included ? { truncated: true } : {}));
}
function extractText(value) {
    var _a;
    if (value === undefined || value === null)
        return '';
    var parts = textParts(value);
    if (parts.length)
        return parts.join('\n');
    try {
        return (_a = JSON.stringify(value)) !== null && _a !== void 0 ? _a : '';
    }
    catch (_b) {
        return '';
    }
}
function firstText() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_1 = values; _a < values_1.length; _a++) {
        var value = values_1[_a];
        if (typeof value === 'string' && value.length)
            return value;
        if (isRecord(value) && typeof value.text === 'string')
            return value.text;
    }
    return '';
}
function typeOf(value) { return typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''; }
function processFiles(files, roots, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var totalBytes, _loop_1, _i, files_1, file, state_1;
        var _this = this;
        var _a, _b, _c, _d;
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    totalBytes = 0;
                    _loop_1 = function (file) {
                        var bytes, parsed_1, previous, _f, groups, mapperState, pendingCount, _g, groups_1, group, _h, _j, event_2, pendingGroups_1, pendingEvents_1, flush, _k, groups_2, group, _l, finalPrefix, next, _m;
                        return __generator(this, function (_o) {
                            switch (_o.label) {
                                case 0:
                                    _o.trys.push([0, 14, , 15]);
                                    return [4 /*yield*/, readSource(file, roots)];
                                case 1:
                                    bytes = _o.sent();
                                    totalBytes += bytes.byteLength;
                                    if (totalBytes > maxTotalBytes) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime sync exceeded the aggregate source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    parsed_1 = parsePrimeFile(bytes);
                                    result.malformed_lines += parsed_1.malformed;
                                    if (parsed_1.partialTail)
                                        result.partial_files++;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript has a malformed row; scanning stops at the safe prefix' });
                                    if (parsed_1.partialTail)
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript ends in an incomplete row; waiting for the next sweep' });
                                    previous = state.files[file.sourceSha256];
                                    if (previous && (parsed_1.completeLines < previous.last_line || parsed_1.prefixHashes.get(previous.last_line) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    _f = mapPrimeEntries(file, parsed_1, (_a = previous === null || previous === void 0 ? void 0 : previous.last_line) !== null && _a !== void 0 ? _a : 0, (_b = previous === null || previous === void 0 ? void 0 : previous.started) !== null && _b !== void 0 ? _b : false, (_c = options.includeContent) !== null && _c !== void 0 ? _c : false), groups = _f.groups, mapperState = _f.state;
                                    pendingCount = groups.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    if (pendingCount + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += pendingCount;
                                        (_d = result.events) !== null && _d !== void 0 ? _d : (result.events = []);
                                        for (_g = 0, groups_1 = groups; _g < groups_1.length; _g++) {
                                            group = groups_1[_g];
                                            for (_h = 0, _j = group.events; _h < _j.length; _h++) {
                                                event_2 = _j[_h];
                                                if (result.events.length < maxPreviewEvents)
                                                    result.events.push(event_2);
                                                else
                                                    result.preview_truncated = true;
                                            }
                                        }
                                        return [2 /*return*/, "continue"];
                                    }
                                    pendingGroups_1 = [];
                                    pendingEvents_1 = [];
                                    flush = function () { return __awaiter(_this, void 0, void 0, function () {
                                        var last, next;
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
                                                    next = makeCursor(last.line, last.prefix, last.started, file, parsed_1.partialTail || parsed_1.malformed > 0);
                                                    if (!cursorChanged(state.files[file.sourceSha256], next)) return [3 /*break*/, 4];
                                                    state.files[file.sourceSha256] = next;
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
                                    _o.label = 2;
                                case 2:
                                    _o.trys.push([2, 10, , 11]);
                                    _k = 0, groups_2 = groups;
                                    _o.label = 3;
                                case 3:
                                    if (!(_k < groups_2.length)) return [3 /*break*/, 8];
                                    group = groups_2[_k];
                                    if (!(pendingEvents_1.length > 0 && pendingEvents_1.length + group.events.length > eventBatchSize)) return [3 /*break*/, 5];
                                    return [4 /*yield*/, flush()];
                                case 4:
                                    _o.sent();
                                    _o.label = 5;
                                case 5:
                                    pendingGroups_1.push(group);
                                    pendingEvents_1.push.apply(pendingEvents_1, group.events);
                                    if (!(pendingEvents_1.length >= eventBatchSize)) return [3 /*break*/, 7];
                                    return [4 /*yield*/, flush()];
                                case 6:
                                    _o.sent();
                                    _o.label = 7;
                                case 7:
                                    _k++;
                                    return [3 /*break*/, 3];
                                case 8: return [4 /*yield*/, flush()];
                                case 9:
                                    _o.sent();
                                    return [3 /*break*/, 11];
                                case 10:
                                    _l = _o.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'Threadforge loopback intake rejected Prime history events' });
                                    return [2 /*return*/, "continue"];
                                case 11:
                                    finalPrefix = parsed_1.prefixHashes.get(parsed_1.completeLines);
                                    if (!finalPrefix) return [3 /*break*/, 13];
                                    next = makeCursor(parsed_1.completeLines, finalPrefix, mapperState.started, file, parsed_1.partialTail || parsed_1.malformed > 0);
                                    if (!cursorChanged(state.files[file.sourceSha256], next)) return [3 /*break*/, 13];
                                    state.files[file.sourceSha256] = next;
                                    result.files_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 13];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 12:
                                    _o.sent();
                                    _o.label = 13;
                                case 13: return [3 /*break*/, 15];
                                case 14:
                                    _m = _o.sent();
                                    result.errors.push({ source_sha256: file.sourceSha256, message: 'Prime transcript could not be read or checkpointed safely' });
                                    return [3 /*break*/, 15];
                                case 15: return [2 /*return*/];
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
    return { last_line: line, started: started, prefix_sha256: prefix, size_bytes: file.size, mtime_ms: file.mtimeMs, incomplete: incomplete };
}
function cursorChanged(previous, next) {
    return !previous || previous.last_line !== next.last_line || previous.started !== next.started ||
        previous.prefix_sha256 !== next.prefix_sha256 || previous.size_bytes !== next.size_bytes ||
        previous.mtime_ms !== next.mtime_ms || previous.incomplete !== next.incomplete;
}
function resolveStatePath(value) {
    var _a;
    if (value !== undefined) {
        if (!node_path_1.default.isAbsolute(value))
            throw new Error('--state-path must be an absolute path');
        return node_path_1.default.resolve(value);
    }
    var root = (_a = process.env.THREADFORGE_HOME) === null || _a === void 0 ? void 0 : _a.trim();
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
                        throw new Error('Prime history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Prime history cursor');
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
                    empty = { schema_version: 'threadforge.prime-session-cursor.v1', version: cursorVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxCursorBytes)
                        throw new Error('Prime history cursor must be a bounded regular file');
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
                        throw new Error('Prime cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxCursorBytes)
                        throw new Error('Prime cursor exceeds local size limit');
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== cursorVersion || !isRecord(parsed.files)) {
                        throw new Error('Prime history cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.last_line) ||
                            typeof value.started !== 'boolean' || typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) || value.mtime_ms < 0 ||
                            typeof value.incomplete !== 'boolean')
                            throw new Error('Prime history cursor contains an invalid checkpoint');
                        files[key] = { last_line: value.last_line, started: value.started, prefix_sha256: value.prefix_sha256,
                            size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, incomplete: value.incomplete };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Prime history cursor'))
                        throw error_3;
                    throw new Error('Prime history cursor is malformed or unreadable');
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
                            throw new Error('unable to create private Prime cursor directory'); })];
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
                        throw new Error('Prime cursor path cannot contain symbolic links or non-directory parents');
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
                        throw new Error('Prime history cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write Prime history cursor');
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
function nonNegativeInteger(value) { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined; }
function finiteNonNegative(value) { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined; }
function isNonNegativeInteger(value) { return nonNegativeInteger(value) !== undefined; }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value, maximum) { return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined; }
function textValue(value) { return typeof value === 'string' ? value : ''; }
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
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function firstRecord() {
    var values = [];
    for (var _i = 0; _i < arguments.length; _i++) {
        values[_i] = arguments[_i];
    }
    for (var _a = 0, values_2 = values; _a < values_2.length; _a++) {
        var value = values_2[_a];
        if (isRecord(value))
            return value;
    }
    return {};
}
function isErrorStatus(value) { return typeof value === 'string' && ['error', 'failed', 'failure'].includes(value.trim().toLowerCase()); }
function timestampValue(value, fallback) {
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0) {
        var date = new Date(value < 1000000000000 ? value * 1000 : value);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    if (typeof value === 'string') {
        var numeric = Number(value);
        if (value.trim() && Number.isFinite(numeric) && numeric >= 0)
            return timestampValue(numeric, fallback);
        var date = new Date(value);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    if (typeof fallback === 'number') {
        var date = new Date(fallback);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    if (typeof fallback === 'string') {
        var date = new Date(fallback);
        if (!Number.isNaN(date.getTime()))
            return date.toISOString();
    }
    return new Date(0).toISOString();
}
function normalizedOS(platform) { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
function sha256(value) { return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex'); }
function isSafeRelative(value) { return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
