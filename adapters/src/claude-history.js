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
exports.getClaudeHistoryStatus = getClaudeHistoryStatus;
exports.syncClaudeHistory = syncClaudeHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'claude-sessions.v1.json';
var maxStateBytes = 8 << 20;
var maxRootFiles = 5000;
var maxFileBytes = 64 << 20;
var maxTotalBytes = 512 << 20;
var maxLineBytes = 8 << 20;
var maxLinesPerFile = 100000;
var maxEventsPerFile = 10000;
var maxEventsPerSync = 20000;
var maxToolCorrelations = 10000;
var maxDepth = 12;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
function getClaudeHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maxFiles, projectsDir, listed, state, sessions, checkpointed;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    maxFiles = boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : 1000, 1, maxRootFiles, '--max-files');
                    return [4 /*yield*/, resolveProjectsDir(options.projectsDir)];
                case 1:
                    projectsDir = _b.sent();
                    return [4 /*yield*/, listSessionFiles(projectsDir, maxFiles)];
                case 2:
                    listed = _b.sent();
                    return [4 /*yield*/, readState(resolveStatePath(options.statePath))];
                case 3:
                    state = _b.sent();
                    sessions = listed.files;
                    checkpointed = sessions.filter(function (file) {
                        var cursor = state.files[sha256(file.key)];
                        return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs;
                    }).length;
                    return [2 /*return*/, {
                            schema_version: 'threadforge.claude-history-status.v1', sessions: sessions.length,
                            checkpointed_sessions: checkpointed, pending_sessions: sessions.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
                            files_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncClaudeHistory() {
    return __awaiter(this, arguments, void 0, function (options) {
        var projectsDir, result, statePath, listed, state;
        var _this = this;
        var _a, _b;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    validateOptions(options);
                    return [4 /*yield*/, resolveProjectsDir(options.projectsDir)];
                case 1:
                    projectsDir = _c.sent();
                    result = emptyResult((_a = options.includeContent) !== null && _a !== void 0 ? _a : false);
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(projectsDir, statePath))
                        throw new Error('Claude history cursor must be stored outside Claude projects');
                    return [4 /*yield*/, listSessionFiles(projectsDir, (_b = options.maxFiles) !== null && _b !== void 0 ? _b : 1000)];
                case 2:
                    listed = _c.sent();
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.files_truncated = listed.truncated;
                    if (!options.preview) return [3 /*break*/, 5];
                    return [4 /*yield*/, readState(statePath)];
                case 3:
                    state = _c.sent();
                    return [4 /*yield*/, processFiles(listed.files, projectsDir, state, options, result, false)];
                case 4:
                    _c.sent();
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
                    _c.sent();
                    _c.label = 7;
                case 7:
                    result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.files_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.claude-history-sync.v1', sessions: 0, sessions_changed: 0,
        events_emitted: 0, events_previewed: 0, malformed_lines: 0, partial_sessions: 0,
        symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
        content_mode: includeContent ? 'include-content' : 'metadata', complete: false,
        preview_truncated: false, errors: [],
    };
}
function validateOptions(options) {
    var _a;
    if (options.preview && options.includeContent)
        throw new Error('--print cannot be combined with --include-content');
    boundedInteger((_a = options.maxFiles) !== null && _a !== void 0 ? _a : 1000, 1, maxRootFiles, '--max-files');
    if (options.projectsDir !== undefined && !node_path_1.default.isAbsolute(options.projectsDir))
        throw new Error('--projects-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveProjectsDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var projectsDir, configDir, home, dirInfo, canonical;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    projectsDir = value;
                    if (!projectsDir) {
                        configDir = (_a = process.env.CLAUDE_CONFIG_DIR) === null || _a === void 0 ? void 0 : _a.trim();
                        home = configDir || node_path_1.default.join(node_os_1.default.homedir(), '.claude');
                        if (!node_path_1.default.isAbsolute(home))
                            throw new Error('CLAUDE_CONFIG_DIR must be an absolute path');
                        projectsDir = node_path_1.default.join(home, 'projects');
                    }
                    if (!node_path_1.default.isAbsolute(projectsDir))
                        throw new Error('--projects-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(projectsDir).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Claude projects directory');
                        })];
                case 1:
                    dirInfo = _b.sent();
                    if (!dirInfo)
                        return [2 /*return*/, node_path_1.default.resolve(projectsDir)];
                    if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink())
                        throw new Error('Claude projects path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectsDir).catch(function () { throw new Error('Claude projects directory could not be resolved'); })];
                case 2:
                    canonical = _b.sent();
                    if (canonical !== node_path_1.default.resolve(projectsDir))
                        throw new Error('Claude projects directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listSessionFiles(root, maxFiles) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInfo, files, pending, symlinksSkipped, oversizedSkipped, truncated, directory, entries, _i, entries_1, entry, absolute, info, depth, relative, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw new Error('unable to inspect Claude projects directory');
                    })];
                case 1:
                    rootInfo = _a.sent();
                    if (!rootInfo)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Claude projects path must be a real directory');
                    files = [];
                    pending = [root];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    _a.label = 2;
                case 2:
                    if (!(pending.length > 0)) return [3 /*break*/, 9];
                    directory = pending.pop();
                    return [4 /*yield*/, (0, promises_1.readdir)(directory, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Claude session paths'); })];
                case 3:
                    entries = _a.sent();
                    _i = 0, entries_1 = entries;
                    _a.label = 4;
                case 4:
                    if (!(_i < entries_1.length)) return [3 /*break*/, 8];
                    entry = entries_1[_i];
                    absolute = node_path_1.default.join(directory, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(absolute).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Claude session path');
                        })];
                case 5:
                    info = _a.sent();
                    if (!info)
                        return [3 /*break*/, 7];
                    if (info.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    if (info.isDirectory()) {
                        depth = node_path_1.default.relative(root, absolute).split(node_path_1.default.sep).length;
                        if (depth > maxDepth)
                            throw new Error("Claude projects exceed the directory-depth scan limit (".concat(maxDepth, ")"));
                        pending.push(absolute);
                        return [3 /*break*/, 7];
                    }
                    if (!info.isFile() || node_path_1.default.extname(entry.name).toLowerCase() !== '.jsonl')
                        return [3 /*break*/, 7];
                    if (info.size > maxFileBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 7];
                    }
                    relative = node_path_1.default.relative(root, absolute);
                    if (!isSafeRelative(relative)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(absolute).catch(function () { return ''; })];
                case 6:
                    canonical = _a.sent();
                    if (!canonical || !isWithin(root, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 7];
                    }
                    files.push({ key: relative.split(node_path_1.default.sep).join('/'), path: canonical, size: info.size, mtimeMs: info.mtimeMs });
                    if (files.length > maxFiles) {
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
                    files.sort(function (left, right) { return left.key < right.key ? -1 : left.key > right.key ? 1 : 0; });
                    if (truncated)
                        files.length = maxFiles;
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
                        throw new Error('Claude session is not a bounded regular file');
                    if (before.size !== file.size || before.mtimeMs !== file.mtimeMs)
                        throw new Error('Claude session changed after enumeration');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || !isWithin(root, canonical))
                        throw new Error('Claude session path escaped the projects directory');
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
                        throw new Error('Claude session changed before reading');
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
                        throw new Error('Claude session changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Claude session'))
                        throw error_1;
                    throw new Error('Claude session could not be opened safely');
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
    var _a, _b, _c;
    var groups = [];
    var tools = new Map();
    var fileHash = sha256(file.key);
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map();
    prefixHashes.set(0, sha256(Buffer.alloc(0)));
    var line = 0;
    var start = 0;
    var safeLines = 0;
    var malformed = 0;
    var eventCount = 0;
    var started = (_a = cursor === null || cursor === void 0 ? void 0 : cursor.session_started) !== null && _a !== void 0 ? _a : false;
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var stoppedOnMalformed = false;
    for (var offset = 0; offset < bytes.length; offset++) {
        if (bytes[offset] !== 0x0a)
            continue;
        line++;
        if (line > maxLinesPerFile)
            throw new Error("Claude session exceeds the ".concat(maxLinesPerFile, "-line processing limit"));
        var chunkStart = start;
        var end = offset;
        if (end > start && bytes[end - 1] === 0x0d)
            end--;
        var raw = bytes.subarray(start, end);
        start = offset + 1;
        var entry = void 0;
        if (raw.length > maxLineBytes) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        if (raw.length > 0) {
            try {
                var parsed = JSON.parse(decoder.decode(raw));
                if (!isRecord(parsed) || !boundedString(parsed.type, 96))
                    throw new Error('invalid row');
                entry = parsed;
            }
            catch (_d) {
                malformed++;
                stoppedOnMalformed = true;
                break;
            }
        }
        prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
        safeLines = line;
        prefixHashes.set(line, prefixHasher.copy().digest('hex'));
        if (!entry)
            continue;
        var events = [];
        var sessionId = boundedString(entry.sessionId, 256);
        if (line > ((_b = cursor === null || cursor === void 0 ? void 0 : cursor.line) !== null && _b !== void 0 ? _b : 0) && !started && sessionId) {
            events.push(makeEvent(entry, fileHash, sessionId, line, 'session.started', 'session', 'Claude Code session start recovered from local transcript', 'session.started'));
            started = true;
        }
        mapEntry(entry, line, fileHash, sessionId, tools, includeContent, events);
        if (line > ((_c = cursor === null || cursor === void 0 ? void 0 : cursor.line) !== null && _c !== void 0 ? _c : 0) && events.length > 0) {
            if (events.length > batchSize)
                throw new Error("Claude session line ".concat(line, " maps to more than ").concat(batchSize, " events"));
            groups.push({ line: line, events: events });
            eventCount += events.length;
            if (eventCount > maxEventsPerFile)
                throw new Error("Claude session exceeds the ".concat(maxEventsPerFile, "-event processing limit"));
            prefixHashes.set(line, prefixHasher.copy().digest('hex'));
        }
    }
    var partialTail = !stoppedOnMalformed && start < bytes.length;
    return {
        completeLines: safeLines,
        partialTail: partialTail,
        malformed: malformed,
        prefixHashes: prefixHashes,
        completePrefixHash: prefixHasher.copy().digest('hex'),
        groups: groups,
        started: started,
    };
}
function mapEntry(entry, line, fileHash, sessionId, tools, includeContent, events) {
    var _a, _b, _c, _d, _e, _f;
    var type = (_a = boundedString(entry.type, 96)) !== null && _a !== void 0 ? _a : '';
    var message = isRecord(entry.message) ? entry.message : {};
    var content = message.content;
    var model = safeLabel(message.model, 128);
    if (type === 'user' && entry.isMeta !== true) {
        var blocks = Array.isArray(content) ? content : undefined;
        if (typeof content === 'string')
            pushTextEvent(events, entry, fileHash, sessionId, line, 'prompt.submitted', 'prompt', cleanUserText(content), includeContent, model, 'user');
        if (blocks) {
            var prompts = [];
            for (var index = 0; index < blocks.length; index++) {
                var block = blocks[index];
                if (!isRecord(block))
                    continue;
                if (block.type === 'text' && typeof block.text === 'string') {
                    var clean = cleanUserText(block.text);
                    if (clean)
                        prompts.push(clean);
                }
                else if (block.type === 'tool_result') {
                    pushToolResult(events, entry, fileHash, sessionId, line, block, tools, model, index);
                }
            }
            if (prompts.length > 0)
                pushTextEvent(events, entry, fileHash, sessionId, line, 'prompt.submitted', 'prompt', prompts.join('\n'), includeContent, model, 'user');
        }
        return;
    }
    if (type === 'assistant') {
        if (typeof content === 'string') {
            pushTextEvent(events, entry, fileHash, sessionId, line, 'agent.message', 'agent', content, includeContent, model, 'assistant');
        }
        var blocks = Array.isArray(content) ? content : [];
        var text = [];
        var reasoning = [];
        for (var index = 0; index < blocks.length; index++) {
            var block = blocks[index];
            if (!isRecord(block))
                continue;
            if (block.type === 'text' && typeof block.text === 'string')
                text.push(block.text.trim());
            else if (block.type === 'thinking') {
                var thinking = boundedString((_b = block.thinking) !== null && _b !== void 0 ? _b : block.text, 1 << 20);
                if (thinking)
                    reasoning.push(thinking);
            }
            else if (block.type === 'tool_use') {
                var id = (_c = boundedString(block.id, 512)) !== null && _c !== void 0 ? _c : '';
                var name_1 = (_d = safeLabel(block.name, 128)) !== null && _d !== void 0 ? _d : '';
                var input = block.input;
                if (id) {
                    if (tools.size >= maxToolCorrelations && !tools.has(id))
                        tools.delete(tools.keys().next().value);
                    tools.set(id, { name: name_1 });
                }
                if (id && name_1)
                    pushToolInvoke(events, entry, fileHash, sessionId, line, model, index, { id: id, name: name_1, input: input });
            }
        }
        if (text.some(function (part) { return part.length > 0; }))
            pushTextEvent(events, entry, fileHash, sessionId, line, 'agent.message', 'agent', text.filter(Boolean).join('\n'), includeContent, model, 'assistant');
        if (reasoning.length > 0)
            pushReasoningEvent(events, entry, fileHash, sessionId, line, reasoning.join('\n'), model);
        var usage = normalizedUsage(message.usage);
        if (usage)
            events.push(makeEvent(entry, fileHash, sessionId, line, 'token.usage', 'metric', 'Claude Code token usage recovered from local transcript', "usage:".concat((_e = boundedString(message.id, 256)) !== null && _e !== void 0 ? _e : line), { model: model, genAi: { usage: usage } }));
        return;
    }
    if (type === 'summary') {
        var summary = boundedString((_f = entry.summary) !== null && _f !== void 0 ? _f : entry.content, 1 << 20);
        if (summary)
            pushReasoningEvent(events, entry, fileHash, sessionId, line, summary, model, 'session.summary');
    }
}
function pushTextEvent(events, entry, fileHash, sessionId, line, action, category, text, includeContent, model, role) {
    var _a;
    var value = text.trim();
    if (!value)
        return;
    var included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
    var event = makeEvent(entry, fileHash, sessionId, line, action, category, role === 'user' ? 'Claude Code prompt recovered from local transcript' : 'Claude Code assistant message recovered from local transcript', "".concat(role, ":").concat((_a = boundedString(entry.uuid, 256)) !== null && _a !== void 0 ? _a : line), __assign({ model: model, content: contentDigest(value, included, includeContent) }, (included ? { text: value } : {})));
    events.push(event);
}
function pushReasoningEvent(events, entry, fileHash, sessionId, line, text, model, action) {
    var _a;
    if (action === void 0) { action = 'agent.reasoning'; }
    var value = text.trim();
    if (!value)
        return;
    events.push(makeEvent(entry, fileHash, sessionId, line, action, 'agent', action === 'session.summary' ? 'Claude Code session summary metadata observed' : 'Claude Code reasoning digest recovered from local transcript', "".concat(action, ":").concat((_a = boundedString(entry.uuid, 256)) !== null && _a !== void 0 ? _a : line), { model: model, content: contentDigest(value, false) }));
}
function pushToolInvoke(events, entry, fileHash, sessionId, line, model, index, call) {
    var _a;
    var input = digestValue(call.input);
    events.push(makeEvent(entry, fileHash, sessionId, line, 'tool.invoked', 'tool', 'Claude Code tool invocation recovered from local transcript', "tool.invoke:".concat((_a = boundedString(entry.uuid, 256)) !== null && _a !== void 0 ? _a : line, ":").concat(index), {
        model: model,
        toolName: call.name, toolCallHash: sha256(call.id),
        content: __assign({ retention: 'metadata', included: false }, (input ? { input_hash: input.hash, input_bytes: input.bytes } : {})),
    }));
}
function pushToolResult(events, entry, fileHash, sessionId, line, block, tools, model, index) {
    var _a;
    var callId = boundedString(block.tool_use_id, 512);
    var call = callId ? tools.get(callId) : undefined;
    if (callId)
        tools.delete(callId);
    var value = toolResultText(block.content);
    var failed = block.is_error === true;
    var name = call === null || call === void 0 ? void 0 : call.name;
    var command = Boolean(name && ['bash', 'shell', 'terminal'].includes(name.toLowerCase()));
    var action = command && !failed ? 'command.executed' : failed ? 'tool.failed' : 'tool.completed';
    var output = digestValue(value);
    events.push(makeEvent(entry, fileHash, sessionId, line, action, command && !failed ? 'command' : 'tool', "Claude Code tool result recovered from local transcript", "tool.result:".concat((_a = boundedString(entry.uuid, 256)) !== null && _a !== void 0 ? _a : line, ":").concat(index), __assign(__assign(__assign({ model: model, severity: failed ? 'medium' : 'info' }, (name ? { toolName: name } : {})), (callId ? { toolCallHash: sha256(callId) } : {})), { content: __assign({ retention: 'metadata', included: false }, (output ? { output_hash: output.hash, output_bytes: output.bytes } : {})) })));
}
function makeEvent(entry, fileHash, sessionId, line, action, category, message, suffix, extra) {
    var _a, _b;
    if (extra === void 0) { extra = {}; }
    var uuid = boundedString(entry.uuid, 256);
    var sourceSession = sessionId !== null && sessionId !== void 0 ? sessionId : "file:".concat(fileHash);
    var event = {
        timestamp: parseTimestamp(entry.timestamp),
        vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(fileHash, ":").concat(line, ":").concat(uuid !== null && uuid !== void 0 ? uuid : '', ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_a = extra.severity) !== null && _a !== void 0 ? _a : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'claude-code', collection_method: 'poll' },
        session: { id: "claude-code:".concat(sha256(sourceSession)) },
        raw: __assign(__assign({ source: 'claude_session_jsonl', source_sha256: fileHash, line: line, entry_type: (_b = boundedString(entry.type, 96)) !== null && _b !== void 0 ? _b : 'unknown' }, (uuid ? { uuid_sha256: sha256(uuid) } : {})), { message: message }),
    };
    if (extra.model)
        event.model = extra.model;
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
function normalizedUsage(value) {
    var _a, _b;
    if (!isRecord(value))
        return undefined;
    var usage = {};
    var input = nonNegativeInteger(value.input_tokens);
    var output = nonNegativeInteger(value.output_tokens);
    var cacheWrite = nonNegativeInteger((_a = value.cache_creation_input_tokens) !== null && _a !== void 0 ? _a : value.cache_creation_tokens);
    var cacheRead = nonNegativeInteger((_b = value.cache_read_input_tokens) !== null && _b !== void 0 ? _b : value.cache_read_tokens);
    if (input !== undefined)
        usage.input_tokens = input;
    if (output !== undefined)
        usage.output_tokens = output;
    if (cacheWrite !== undefined)
        usage.cache_creation = { input_tokens: cacheWrite };
    if (cacheRead !== undefined)
        usage.cache_read = { input_tokens: cacheRead };
    return Object.keys(usage).length > 0 ? usage : undefined;
}
function toolResultText(value) {
    if (typeof value === 'string')
        return value;
    if (!Array.isArray(value))
        return '';
    return value.flatMap(function (item) { var _a, _b; return isRecord(item) && typeof ((_a = item.text) !== null && _a !== void 0 ? _a : item.content) === 'string' ? [String((_b = item.text) !== null && _b !== void 0 ? _b : item.content)] : []; }).join('\n');
}
function cleanUserText(text) {
    var value = text.trim();
    return value.includes('<local-command-caveat>') || value.includes('<command-name>') ? '' : value;
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
function parseTimestamp(value) {
    if (typeof value !== 'string')
        return new Date().toISOString();
    var date = new Date(value);
    return Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
}
function safeLabel(value, max) {
    var text = boundedString(value, max);
    if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text))
        return undefined;
    return text;
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
                        var key, previous, bytes, parsed_1, cursorLine, fileEvents, _e, _f, group, _g, _h, event_1, failed, batch_1, batchEvents_1, flush, _j, _k, group, _l, completePrefix, _m;
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
                                        result.files_truncated = true;
                                        result.errors.push({ source_sha256: key, message: 'Claude sync exceeded the total source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    parsed_1 = parseAndMap(bytes, file, previous, (_a = options.includeContent) !== null && _a !== void 0 ? _a : false);
                                    result.malformed_lines += parsed_1.malformed;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: key, message: 'Claude transcript contains a malformed row; scanning stops before it' });
                                    if (parsed_1.partialTail)
                                        result.partial_sessions++;
                                    cursorLine = (_b = previous === null || previous === void 0 ? void 0 : previous.line) !== null && _b !== void 0 ? _b : 0;
                                    if (previous && (parsed_1.completeLines < cursorLine || parsed_1.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: key, message: 'Claude transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    fileEvents = parsed_1.groups.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    if (fileEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: key, message: 'Claude sync exceeded the event processing limit' });
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
                                    failed = false;
                                    batch_1 = [];
                                    batchEvents_1 = 0;
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
                                                        throw new Error('Claude session checkpoint prefix is unavailable');
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
                                    result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Claude history events' });
                                    return [3 /*break*/, 11];
                                case 11:
                                    if (failed)
                                        return [2 /*return*/, "continue"];
                                    completePrefix = parsed_1.completePrefixHash;
                                    if (!completePrefix) {
                                        result.errors.push({ source_sha256: key, message: 'Claude session checkpoint prefix is unavailable' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    if (!(parsed_1.completeLines > cursorLine || (previous !== undefined && previous.session_started !== parsed_1.started))) return [3 /*break*/, 13];
                                    state.files[key] = makeCursor(parsed_1.completeLines, file, completePrefix, parsed_1.started);
                                    result.sessions_changed++;
                                    if (!(persist && statePath)) return [3 /*break*/, 13];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 12:
                                    _o.sent();
                                    _o.label = 13;
                                case 13: return [3 /*break*/, 15];
                                case 14:
                                    _m = _o.sent();
                                    result.errors.push({ source_sha256: key, message: 'Claude session could not be read or checkpointed safely' });
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
        var parent, lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, verifyStateParent(statePath, true)];
                case 1:
                    _a.sent();
                    parent = node_path_1.default.dirname(statePath);
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
                        throw new Error('Claude history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Claude history cursor');
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
                    empty = { schema_version: 'threadforge.claude-session-cursor.v1', version: stateVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Claude history cursor');
                        })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('Claude history cursor must be a bounded regular file');
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
                        throw new Error('Claude cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
                        throw new Error('Claude history cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean') {
                            throw new Error('Claude history cursor contains an invalid checkpoint');
                        }
                        files[key] = {
                            line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms,
                            prefix_sha256: value.prefix_sha256, session_started: value.session_started,
                        };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_3 = _d.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Claude history cursor'))
                        throw error_3;
                    throw new Error('Claude history cursor is malformed or unreadable');
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
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Claude cursor directory');
                        })];
                case 2:
                    info = _a.sent();
                    if (!(!info && create)) return [3 /*break*/, 5];
                    return [4 /*yield*/, (0, promises_1.mkdir)(current, { mode: 448 }).catch(function (error) {
                            if (!isCode(error, 'EEXIST'))
                                throw new Error('unable to create private Claude cursor directory');
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
                        throw new Error('Claude cursor path cannot contain symbolic links or non-directory parents');
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
                        throw new Error('Claude history cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write Claude history cursor');
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
function isNonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
function boundedString(value, max) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= max ? value.trim() : undefined;
}
function nonNegativeInteger(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function sha256(value) {
    return (0, node_crypto_1.createHash)('sha256').update(value).digest('hex');
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
