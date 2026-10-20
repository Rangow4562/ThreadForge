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
exports.getFactoryHistoryStatus = getFactoryHistoryStatus;
exports.syncFactoryHistory = syncFactoryHistory;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var emit_ts_1 = require("./emit.ts");
var stateVersion = 1;
var stateFileName = 'factory-sessions.v1.json';
var maxStateBytes = 8 << 20;
var maxSessions = 5000;
var maxFileBytes = 64 << 20;
var maxSettingsBytes = 1 << 20;
var maxTotalBytes = 512 << 20;
var maxLineBytes = 4 << 20;
var maxLinesPerFile = 100000;
var maxEventsPerFile = 10000;
var maxEventsPerSync = 20000;
var maxToolCorrelations = 10000;
var batchSize = 100;
var maxPreviewEvents = 500;
var maxContentBytes = 4 << 10;
function getFactoryHistoryStatus() {
    return __awaiter(this, arguments, void 0, function (options) {
        var maximum, sessionsDir, statePath, listed, state, checkpointed;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    maximum = boundedInteger((_a = options.maxSessions) !== null && _a !== void 0 ? _a : 1000, 1, maxSessions, '--max-sessions');
                    return [4 /*yield*/, resolveSessionsDir(options.sessionsDir)];
                case 1:
                    sessionsDir = _b.sent();
                    statePath = resolveStatePath(options.statePath);
                    if (isWithin(sessionsDir, statePath))
                        throw new Error('Factory history cursor must be stored outside Factory sessions');
                    return [4 /*yield*/, listSessions(sessionsDir, maximum)];
                case 2:
                    listed = _b.sent();
                    return [4 /*yield*/, readState(statePath)];
                case 3:
                    state = _b.sent();
                    checkpointed = listed.files.filter(function (file) {
                        var cursor = state.files[sha256(file.key)];
                        return cursor !== undefined && cursor.size_bytes === file.size && cursor.mtime_ms === file.mtimeMs &&
                            cursor.settings_mtime_ms === file.settingsMtimeMs;
                    }).length;
                    return [2 /*return*/, {
                            schema_version: 'threadforge.factory-history-status.v1', sessions: listed.files.length,
                            checkpointed_sessions: checkpointed, pending_sessions: listed.files.length - checkpointed,
                            symlinks_skipped: listed.symlinksSkipped, oversized_files_skipped: listed.oversizedSkipped,
                            sessions_truncated: listed.truncated,
                        }];
            }
        });
    });
}
function syncFactoryHistory() {
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
                        throw new Error('Factory history cursor must be stored outside Factory sessions');
                    return [4 /*yield*/, listSessions(sessionsDir, (_c = options.maxSessions) !== null && _c !== void 0 ? _c : 1000)];
                case 2:
                    listed = _e.sent();
                    result = emptyResult((_d = options.includeContent) !== null && _d !== void 0 ? _d : false);
                    result.sessions = listed.files.length;
                    result.symlinks_skipped = listed.symlinksSkipped;
                    result.oversized_files_skipped = listed.oversizedSkipped;
                    result.sessions_truncated = listed.truncated;
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
                    result.complete = result.errors.length === 0 && result.malformed_lines === 0 && result.partial_sessions === 0 &&
                        result.symlinks_skipped === 0 && result.oversized_files_skipped === 0 && !result.sessions_truncated;
                    return [2 /*return*/, result];
            }
        });
    });
}
function emptyResult(includeContent) {
    return {
        schema_version: 'threadforge.factory-history-sync.v1', sessions: 0, sessions_changed: 0,
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
    if (options.sessionsDir !== undefined && !node_path_1.default.isAbsolute(options.sessionsDir))
        throw new Error('--sessions-dir must be an absolute path');
    if (options.statePath !== undefined && !node_path_1.default.isAbsolute(options.statePath))
        throw new Error('--state-path must be an absolute path');
    if (options.endpoint !== undefined)
        (0, emit_ts_1.assertLoopbackEndpoint)(options.endpoint);
}
function resolveSessionsDir(value) {
    return __awaiter(this, void 0, void 0, function () {
        var sessionsDir, info, canonical;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    sessionsDir = value !== null && value !== void 0 ? value : node_path_1.default.join(node_os_1.default.homedir(), '.factory', 'sessions');
                    if (!node_path_1.default.isAbsolute(sessionsDir))
                        throw new Error('--sessions-dir must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(sessionsDir).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, node_path_1.default.resolve(sessionsDir)];
                    if (!info.isDirectory() || info.isSymbolicLink())
                        throw new Error('Factory sessions path must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(sessionsDir).catch(function () { throw new Error('Factory sessions directory could not be resolved'); })];
                case 2:
                    canonical = _a.sent();
                    if (canonical !== node_path_1.default.resolve(sessionsDir))
                        throw new Error('Factory sessions directory must not traverse symbolic links');
                    return [2 /*return*/, canonical];
            }
        });
    });
}
function listSessions(root, maximum) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInfo, files, symlinksSkipped, oversizedSkipped, truncated, projects, _i, projects_1, project, projectPath, projectInfo, entries, _a, entries_1, entry, sessionId, sourcePath, info, canonical, key, settingsPath, settingsInfo, settingsMtimeMs;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    rootInfo = _b.sent();
                    if (!rootInfo)
                        return [2 /*return*/, { files: [], symlinksSkipped: 0, oversizedSkipped: 0, truncated: false }];
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Factory sessions path must be a real directory');
                    files = [];
                    symlinksSkipped = 0;
                    oversizedSkipped = 0;
                    truncated = false;
                    return [4 /*yield*/, (0, promises_1.readdir)(root, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Factory project folders'); })];
                case 2:
                    projects = _b.sent();
                    _i = 0, projects_1 = projects;
                    _b.label = 3;
                case 3:
                    if (!(_i < projects_1.length)) return [3 /*break*/, 13];
                    project = projects_1[_i];
                    projectPath = node_path_1.default.join(root, project.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(projectPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 4:
                    projectInfo = _b.sent();
                    if (!projectInfo)
                        return [3 /*break*/, 12];
                    if (projectInfo.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 12];
                    }
                    if (!projectInfo.isDirectory())
                        return [3 /*break*/, 12];
                    return [4 /*yield*/, (0, promises_1.readdir)(projectPath, { withFileTypes: true }).catch(function () { throw new Error('unable to enumerate Factory session files'); })];
                case 5:
                    entries = _b.sent();
                    _a = 0, entries_1 = entries;
                    _b.label = 6;
                case 6:
                    if (!(_a < entries_1.length)) return [3 /*break*/, 11];
                    entry = entries_1[_a];
                    if (!entry.name.toLowerCase().endsWith('.jsonl'))
                        return [3 /*break*/, 10];
                    sessionId = entry.name.slice(0, -'.jsonl'.length);
                    if (!/^[A-Za-z0-9_-]{1,255}$/.test(sessionId))
                        return [3 /*break*/, 10];
                    sourcePath = node_path_1.default.join(projectPath, entry.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(sourcePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 7:
                    info = _b.sent();
                    if (!info)
                        return [3 /*break*/, 10];
                    if (info.isSymbolicLink()) {
                        symlinksSkipped++;
                        return [3 /*break*/, 10];
                    }
                    if (!info.isFile())
                        return [3 /*break*/, 10];
                    if (info.size > maxFileBytes) {
                        oversizedSkipped++;
                        return [3 /*break*/, 10];
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(sourcePath).catch(function () { return ''; })];
                case 8:
                    canonical = _b.sent();
                    if (!canonical || !isWithin(root, canonical)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 10];
                    }
                    key = node_path_1.default.relative(root, canonical).split(node_path_1.default.sep).join('/');
                    if (!isSafeRelative(key)) {
                        symlinksSkipped++;
                        return [3 /*break*/, 10];
                    }
                    settingsPath = node_path_1.default.join(projectPath, "".concat(sessionId, ".settings.json"));
                    return [4 /*yield*/, (0, promises_1.lstat)(settingsPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 9:
                    settingsInfo = _b.sent();
                    settingsMtimeMs = !settingsInfo ? 0 : settingsInfo.isFile() && !settingsInfo.isSymbolicLink() ? settingsInfo.mtimeMs : -1;
                    files.push({ key: key, path: canonical, settingsPath: settingsPath, settingsMtimeMs: settingsMtimeMs, sessionId: sessionId, size: info.size, mtimeMs: info.mtimeMs });
                    if (files.length > maximum) {
                        truncated = true;
                        return [3 /*break*/, 11];
                    }
                    _b.label = 10;
                case 10:
                    _a++;
                    return [3 /*break*/, 6];
                case 11:
                    if (truncated)
                        return [3 /*break*/, 13];
                    _b.label = 12;
                case 12:
                    _i++;
                    return [3 /*break*/, 3];
                case 13:
                    files.sort(function (left, right) { return left.key < right.key ? -1 : left.key > right.key ? 1 : 0; });
                    if (truncated)
                        files.length = maximum;
                    return [2 /*return*/, { files: files, symlinksSkipped: symlinksSkipped, oversizedSkipped: oversizedSkipped, truncated: truncated }];
            }
        });
    });
}
function isSafeRelative(value) { return value !== '' && !node_path_1.default.isAbsolute(value) && !value.split(/[\\/]+/).includes('..'); }
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
                        throw new Error('Factory session is not a bounded regular file');
                    if (before.size !== file.size || before.mtimeMs !== file.mtimeMs)
                        throw new Error('Factory session changed after enumeration');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.path).catch(function () { return ''; })];
                case 2:
                    canonical = _b.sent();
                    if (!canonical || canonical !== file.path || !isWithin(root, canonical))
                        throw new Error('Factory session path escaped the sessions directory');
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
                        throw new Error('Factory session changed before reading');
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
                        throw new Error('Factory session changed during read');
                    return [2 /*return*/, bytes];
                case 9:
                    error_1 = _b.sent();
                    if (error_1 instanceof Error && error_1.message.startsWith('Factory session'))
                        throw error_1;
                    throw new Error('Factory session could not be opened safely');
                case 10: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 12: return [2 /*return*/];
            }
        });
    });
}
function readSettingsFile(file, root) {
    return __awaiter(this, void 0, void 0, function () {
        var before, canonical, handle, opened, bytes, afterHandle, afterPath, decoded, model, usageValue, usage, allowed, error_2;
        var _a, _b;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(file.settingsPath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    before = _c.sent();
                    if (!before)
                        return [2 /*return*/, undefined];
                    if (!before.isFile() || before.isSymbolicLink() || before.size > maxSettingsBytes)
                        throw new Error('Factory settings must be a bounded regular file');
                    return [4 /*yield*/, (0, promises_1.realpath)(file.settingsPath).catch(function () { return ''; })];
                case 2:
                    canonical = _c.sent();
                    if (!canonical || canonical !== node_path_1.default.resolve(file.settingsPath) || !isWithin(root, canonical) || node_path_1.default.dirname(canonical) !== node_path_1.default.dirname(file.path)) {
                        throw new Error('Factory settings path escaped the sessions directory');
                    }
                    _c.label = 3;
                case 3:
                    _c.trys.push([3, 9, 10, 12]);
                    return [4 /*yield*/, (0, promises_1.open)(file.settingsPath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 4:
                    handle = _c.sent();
                    return [4 /*yield*/, handle.stat()];
                case 5:
                    opened = _c.sent();
                    if (!sameFile(before, opened) || before.size !== opened.size || before.mtimeMs !== opened.mtimeMs)
                        throw new Error('Factory settings changed before reading');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _c.sent();
                    return [4 /*yield*/, handle.stat()];
                case 7:
                    afterHandle = _c.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(file.settingsPath).catch(function () { return null; })];
                case 8:
                    afterPath = _c.sent();
                    if (bytes.byteLength > maxSettingsBytes || !sameFile(opened, afterHandle) || !sameFile(opened, afterPath) ||
                        opened.size !== afterHandle.size || opened.mtimeMs !== afterHandle.mtimeMs)
                        throw new Error('Factory settings changed during read');
                    decoded = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(decoded))
                        throw new Error('Factory settings are malformed');
                    model = safeLabel(decoded.model, 128);
                    usageValue = isRecord(decoded.tokenUsage) ? decoded.tokenUsage : isRecord(decoded.inclusiveTokenUsage) ? decoded.inclusiveTokenUsage : undefined;
                    usage = usageValue ? parseSettingsUsage(usageValue, (_b = usageValue.factoryCredits) !== null && _b !== void 0 ? _b : decoded.factoryCredits) : undefined;
                    allowed = __assign(__assign({}, (model ? { model: model } : {})), (usage ? { usage: usage } : {}));
                    return [2 /*return*/, __assign(__assign(__assign({}, (model ? { model: model } : {})), (usage ? { usage: usage } : {})), { modifiedMs: opened.mtimeMs, sizeBytes: bytes.byteLength, fingerprint: sha256(JSON.stringify(allowed)) })];
                case 9:
                    error_2 = _c.sent();
                    if (error_2 instanceof Error && error_2.message.startsWith('Factory settings'))
                        throw error_2;
                    throw new Error('Factory settings are malformed or unreadable');
                case 10: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _c.sent();
                    return [7 /*endfinally*/];
                case 12: return [2 /*return*/];
            }
        });
    });
}
function parseSettingsUsage(value, creditsValue) {
    var usage = {};
    var input = nonNegativeInteger(value.inputTokens);
    var output = nonNegativeInteger(value.outputTokens);
    var cacheWrite = nonNegativeInteger(value.cacheCreationTokens);
    var cacheRead = nonNegativeInteger(value.cacheReadTokens);
    var reasoning = nonNegativeInteger(value.thinkingTokens);
    var credits = typeof creditsValue === 'number' && Number.isFinite(creditsValue) && creditsValue >= 0 && creditsValue <= 1000000000000
        ? creditsValue : undefined;
    if (input !== undefined)
        usage.input = input;
    if (output !== undefined)
        usage.output = output;
    if (cacheWrite !== undefined)
        usage.cacheWrite = cacheWrite;
    if (cacheRead !== undefined)
        usage.cacheRead = cacheRead;
    if (reasoning !== undefined)
        usage.reasoning = reasoning;
    if (credits !== undefined)
        usage.credits = credits;
    return Object.keys(usage).length > 0 ? usage : undefined;
}
function makeSettingsUsageEvent(file, settings) {
    if (!settings.usage)
        return undefined;
    var usage = {};
    if (settings.usage.input !== undefined)
        usage.input_tokens = settings.usage.input;
    if (settings.usage.output !== undefined)
        usage.output_tokens = settings.usage.output;
    if (settings.usage.cacheRead !== undefined)
        usage.cache_read_input_tokens = settings.usage.cacheRead;
    if (settings.usage.cacheWrite !== undefined)
        usage.cache_creation_input_tokens = settings.usage.cacheWrite;
    if (settings.usage.reasoning !== undefined)
        usage.reasoning_tokens = settings.usage.reasoning;
    if (settings.usage.credits !== undefined)
        usage.factory_credits = settings.usage.credits;
    var fileHash = sha256(file.key);
    var event = {
        timestamp: new Date(settings.modifiedMs).toISOString(), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(fileHash, ":settings:").concat(settings.fingerprint)), kind: 'agent_runtime', action: 'token.usage', category: 'metric', fidelity: 'observed' },
        severity: 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'factory', collection_method: 'poll' }, session: { id: "factory:".concat(sha256(file.sessionId)) },
        raw: { source: 'factory_session_settings_json', source_sha256: fileHash, message: 'Factory Droid token usage recovered from allowlisted local settings' },
        gen_ai: { usage: usage },
    };
    if (settings.model)
        event.model = settings.model;
    return event;
}
function processFiles(files, root, state, options, result, persist, statePath) {
    return __awaiter(this, void 0, void 0, function () {
        var totalBytes, _loop_1, _i, files_1, file, state_1;
        var _this = this;
        var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k;
        return __generator(this, function (_l) {
            switch (_l.label) {
                case 0:
                    totalBytes = 0;
                    _loop_1 = function (file) {
                        var key, previous, bytes, settings, _m, parsed_1, cursorLine, fileEvents, settingsFingerprint, settingsChanged, settingsCheckpointChanged, settingsEvent, pendingEvents, _o, _p, group, _q, _r, event_1, failed, batch_1, batchEvents_1, flush, _s, _t, group, _u, completePrefix, transcriptAdvanced, _v, current, _w;
                        return __generator(this, function (_x) {
                            switch (_x.label) {
                                case 0:
                                    key = sha256(file.key);
                                    previous = state.files[key];
                                    _x.label = 1;
                                case 1:
                                    _x.trys.push([1, 24, , 25]);
                                    return [4 /*yield*/, readSessionFile(file, root)];
                                case 2:
                                    bytes = _x.sent();
                                    totalBytes += bytes.byteLength;
                                    if (totalBytes > maxTotalBytes) {
                                        result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the total source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    settings = void 0;
                                    _x.label = 3;
                                case 3:
                                    _x.trys.push([3, 5, , 6]);
                                    return [4 /*yield*/, readSettingsFile(file, root)];
                                case 4:
                                    settings = _x.sent();
                                    if (settings)
                                        totalBytes += settings.sizeBytes;
                                    if (totalBytes > maxTotalBytes) {
                                        result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the total source-byte limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    return [3 /*break*/, 6];
                                case 5:
                                    _m = _x.sent();
                                    result.errors.push({ source_sha256: key, message: 'Factory session settings could not be read safely' });
                                    return [2 /*return*/, "continue"];
                                case 6:
                                    parsed_1 = parseSessionFile(bytes, file, previous, settings === null || settings === void 0 ? void 0 : settings.model, (_a = options.includeContent) !== null && _a !== void 0 ? _a : false);
                                    result.malformed_lines += parsed_1.malformed;
                                    if (parsed_1.malformed > 0)
                                        result.errors.push({ source_sha256: key, message: 'Factory transcript contains a malformed row; scanning stops before it' });
                                    if (parsed_1.partialTail)
                                        result.partial_sessions++;
                                    cursorLine = (_b = previous === null || previous === void 0 ? void 0 : previous.line) !== null && _b !== void 0 ? _b : 0;
                                    if (previous && (parsed_1.completeLines < cursorLine || parsed_1.prefixHashes.get(cursorLine) !== previous.prefix_sha256)) {
                                        result.errors.push({ source_sha256: key, message: 'Factory transcript prefix changed; refusing automatic replay' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    fileEvents = parsed_1.groups.reduce(function (count, group) { return count + group.events.length; }, 0);
                                    settingsFingerprint = (_c = settings === null || settings === void 0 ? void 0 : settings.fingerprint) !== null && _c !== void 0 ? _c : '';
                                    settingsChanged = settingsFingerprint !== ((_d = previous === null || previous === void 0 ? void 0 : previous.settings_fingerprint) !== null && _d !== void 0 ? _d : '');
                                    settingsCheckpointChanged = settingsChanged || ((_e = settings === null || settings === void 0 ? void 0 : settings.modifiedMs) !== null && _e !== void 0 ? _e : 0) !== ((_f = previous === null || previous === void 0 ? void 0 : previous.settings_mtime_ms) !== null && _f !== void 0 ? _f : 0);
                                    settingsEvent = settings && settingsChanged ? makeSettingsUsageEvent(file, settings) : undefined;
                                    pendingEvents = fileEvents + (settingsEvent ? 1 : 0);
                                    if (pendingEvents + result.events_emitted + result.events_previewed > maxEventsPerSync) {
                                        result.errors.push({ source_sha256: key, message: 'Factory sync exceeded the event processing limit' });
                                        return [2 /*return*/, "break"];
                                    }
                                    if (options.preview) {
                                        result.events_previewed += pendingEvents;
                                        (_g = result.events) !== null && _g !== void 0 ? _g : (result.events = []);
                                        for (_o = 0, _p = parsed_1.groups; _o < _p.length; _o++) {
                                            group = _p[_o];
                                            for (_q = 0, _r = group.events; _q < _r.length; _q++) {
                                                event_1 = _r[_q];
                                                if (result.events.length < maxPreviewEvents)
                                                    result.events.push(event_1);
                                                else
                                                    result.preview_truncated = true;
                                            }
                                        }
                                        if (settingsEvent) {
                                            if (result.events.length < maxPreviewEvents)
                                                result.events.push(settingsEvent);
                                            else
                                                result.preview_truncated = true;
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
                                                        throw new Error('Factory session checkpoint prefix is unavailable');
                                                    state.files[key] = makeCursor(lastLine, file, prefix, started, previous);
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
                                    _x.label = 7;
                                case 7:
                                    _x.trys.push([7, 14, , 15]);
                                    _s = 0, _t = parsed_1.groups;
                                    _x.label = 8;
                                case 8:
                                    if (!(_s < _t.length)) return [3 /*break*/, 12];
                                    group = _t[_s];
                                    if (!(batchEvents_1 > 0 && batchEvents_1 + group.events.length > batchSize)) return [3 /*break*/, 10];
                                    return [4 /*yield*/, flush()];
                                case 9:
                                    _x.sent();
                                    _x.label = 10;
                                case 10:
                                    batch_1.push(group);
                                    batchEvents_1 += group.events.length;
                                    _x.label = 11;
                                case 11:
                                    _s++;
                                    return [3 /*break*/, 8];
                                case 12: return [4 /*yield*/, flush()];
                                case 13:
                                    _x.sent();
                                    return [3 /*break*/, 15];
                                case 14:
                                    _u = _x.sent();
                                    failed = true;
                                    result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Factory history events' });
                                    return [3 /*break*/, 15];
                                case 15:
                                    if (failed)
                                        return [2 /*return*/, "continue"];
                                    completePrefix = parsed_1.completePrefixHash;
                                    if (!completePrefix) {
                                        result.errors.push({ source_sha256: key, message: 'Factory session checkpoint prefix is unavailable' });
                                        return [2 /*return*/, "continue"];
                                    }
                                    transcriptAdvanced = parsed_1.completeLines > cursorLine || previous === undefined ||
                                        (previous !== undefined && previous.session_started !== parsed_1.sessionStarted);
                                    if (!transcriptAdvanced) return [3 /*break*/, 17];
                                    state.files[key] = makeCursor(parsed_1.completeLines, file, completePrefix, parsed_1.sessionStarted, previous);
                                    if (!(persist && statePath)) return [3 /*break*/, 17];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 16:
                                    _x.sent();
                                    _x.label = 17;
                                case 17:
                                    if (!settingsCheckpointChanged) return [3 /*break*/, 23];
                                    if (!(settingsChanged && settingsEvent)) return [3 /*break*/, 21];
                                    _x.label = 18;
                                case 18:
                                    _x.trys.push([18, 20, , 21]);
                                    return [4 /*yield*/, (0, emit_ts_1.emitEvents)([settingsEvent], { endpoint: (_h = options.endpoint) !== null && _h !== void 0 ? _h : process.env.THREADFORGE_EVENT_ENDPOINT })];
                                case 19:
                                    _x.sent();
                                    result.events_emitted++;
                                    return [3 /*break*/, 21];
                                case 20:
                                    _v = _x.sent();
                                    result.errors.push({ source_sha256: key, message: 'Threadforge loopback intake rejected Factory settings usage' });
                                    return [2 /*return*/, "continue"];
                                case 21:
                                    current = (_j = state.files[key]) !== null && _j !== void 0 ? _j : makeCursor(parsed_1.completeLines, file, completePrefix, parsed_1.sessionStarted, previous);
                                    state.files[key] = __assign(__assign({}, current), { settings_mtime_ms: (_k = settings === null || settings === void 0 ? void 0 : settings.modifiedMs) !== null && _k !== void 0 ? _k : 0, settings_fingerprint: settingsFingerprint });
                                    if (!(persist && statePath)) return [3 /*break*/, 23];
                                    return [4 /*yield*/, writeState(statePath, state)];
                                case 22:
                                    _x.sent();
                                    _x.label = 23;
                                case 23:
                                    if (transcriptAdvanced || settingsCheckpointChanged)
                                        result.sessions_changed++;
                                    return [3 /*break*/, 25];
                                case 24:
                                    _w = _x.sent();
                                    result.errors.push({ source_sha256: key, message: 'Factory session could not be read or checkpointed safely' });
                                    return [3 /*break*/, 25];
                                case 25: return [2 /*return*/];
                            }
                        });
                    };
                    _i = 0, files_1 = files;
                    _l.label = 1;
                case 1:
                    if (!(_i < files_1.length)) return [3 /*break*/, 4];
                    file = files_1[_i];
                    return [5 /*yield**/, _loop_1(file)];
                case 2:
                    state_1 = _l.sent();
                    if (state_1 === "break")
                        return [3 /*break*/, 4];
                    _l.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function makeCursor(line, file, prefix, started, previous) {
    var _a, _b;
    return {
        line: line,
        size_bytes: file.size, mtime_ms: file.mtimeMs, prefix_sha256: prefix, session_started: started,
        settings_mtime_ms: (_a = previous === null || previous === void 0 ? void 0 : previous.settings_mtime_ms) !== null && _a !== void 0 ? _a : 0, settings_fingerprint: (_b = previous === null || previous === void 0 ? void 0 : previous.settings_fingerprint) !== null && _b !== void 0 ? _b : '',
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
    return node_path_1.default.join(root, 'state', stateFileName);
}
function withStateLock(statePath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lock, error_3;
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
                    error_3 = _a.sent();
                    if (isCode(error_3, 'EEXIST'))
                        throw new Error('Factory history sync is locked by another writer or a stale lock');
                    throw new Error('unable to lock Factory history cursor');
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
        var empty, info, handle, opened, bytes, parsed, files, _i, _a, _b, key, value, error_4;
        var _c;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    empty = { schema_version: 'threadforge.factory-session-cursor.v1', version: stateVersion, files: {} };
                    return [4 /*yield*/, verifyStateParent(statePath, false)];
                case 1:
                    _d.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(statePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect Factory history cursor');
                        })];
                case 2:
                    info = _d.sent();
                    if (!info)
                        return [2 /*return*/, empty];
                    if (!info.isFile() || info.isSymbolicLink() || info.size > maxStateBytes)
                        throw new Error('Factory history cursor must be a bounded regular file');
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
                        throw new Error('Factory cursor changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 6:
                    bytes = _d.sent();
                    if (bytes.byteLength > maxStateBytes)
                        throw new Error('Factory cursor exceeds local size limit');
                    parsed = JSON.parse(new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    if (!isRecord(parsed) || parsed.schema_version !== empty.schema_version || parsed.version !== stateVersion || !isRecord(parsed.files)) {
                        throw new Error('Factory history cursor has an unsupported schema; inspect it before retrying');
                    }
                    files = {};
                    for (_i = 0, _a = Object.entries(parsed.files); _i < _a.length; _i++) {
                        _b = _a[_i], key = _b[0], value = _b[1];
                        if (!/^[0-9a-f]{64}$/.test(key) || !isRecord(value) || !isNonNegativeInteger(value.line) ||
                            !isNonNegativeInteger(value.size_bytes) || typeof value.mtime_ms !== 'number' || !Number.isFinite(value.mtime_ms) ||
                            typeof value.prefix_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.prefix_sha256) || typeof value.session_started !== 'boolean' ||
                            typeof value.settings_mtime_ms !== 'number' || !Number.isFinite(value.settings_mtime_ms) || value.settings_mtime_ms < 0 ||
                            typeof value.settings_fingerprint !== 'string' ||
                            (value.settings_fingerprint !== '' && !/^[0-9a-f]{64}$/.test(value.settings_fingerprint))) {
                            throw new Error('Factory history cursor contains an invalid checkpoint');
                        }
                        files[key] = {
                            line: value.line, size_bytes: value.size_bytes, mtime_ms: value.mtime_ms, prefix_sha256: value.prefix_sha256,
                            session_started: value.session_started, settings_mtime_ms: value.settings_mtime_ms, settings_fingerprint: value.settings_fingerprint,
                        };
                    }
                    return [2 /*return*/, __assign(__assign({}, empty), { files: files })];
                case 7:
                    error_4 = _d.sent();
                    if (error_4 instanceof Error && error_4.message.startsWith('Factory history cursor'))
                        throw error_4;
                    throw new Error('Factory history cursor is malformed or unreadable');
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
                            throw new Error('unable to create private Factory cursor directory'); })];
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
                        throw new Error('Factory cursor path cannot contain symbolic links or non-directory parents');
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
                        throw new Error('Factory history cursor exceeds the local size limit');
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
                    throw new Error('unable to atomically write Factory history cursor');
                case 11: return [2 /*return*/];
            }
        });
    });
}
function sameFile(left, right) {
    return right !== null && left.dev === right.dev && left.ino === right.ino;
}
function parseSessionFile(bytes, file, previous, settingsModel, includeContent) {
    var _a, _b, _c;
    if (includeContent === void 0) { includeContent = false; }
    var prefixHasher = (0, node_crypto_1.createHash)('sha256');
    var prefixHashes = new Map([[0, sha256(Buffer.alloc(0))]]);
    var groups = [];
    var decoder = new node_util_1.TextDecoder('utf-8', { fatal: true });
    var mapper = __assign(__assign({ sessionStarted: (_a = previous === null || previous === void 0 ? void 0 : previous.session_started) !== null && _a !== void 0 ? _a : false }, (settingsModel ? { model: settingsModel } : {})), { tools: new Map() });
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
            throw new Error("Factory session exceeds ".concat(maxLinesPerFile, " lines"));
        var chunkStart = start;
        var end = offset > start && bytes[offset - 1] === 0x0d ? offset - 1 : offset;
        var raw = bytes.subarray(start, end);
        start = offset + 1;
        if (raw.byteLength > maxLineBytes) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        if (raw.toString('utf8').trim() === '') {
            prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
            safeLines = line;
            prefixHashes.set(line, prefixHasher.copy().digest('hex'));
            continue;
        }
        var record = void 0;
        try {
            var decoded = JSON.parse(decoder.decode(raw));
            if (!isRecord(decoded) || !safeLabel(decoded.type, 96))
                throw new Error('invalid record');
            record = decoded;
        }
        catch (_d) {
            malformed++;
            stoppedOnMalformed = true;
            break;
        }
        prefixHasher.update(bytes.subarray(chunkStart, offset + 1));
        safeLines = line;
        prefixHashes.set(line, prefixHasher.copy().digest('hex'));
        var events = mapSessionRecord(record, line, file, mapper, line > ((_b = previous === null || previous === void 0 ? void 0 : previous.line) !== null && _b !== void 0 ? _b : 0), includeContent);
        if (line <= ((_c = previous === null || previous === void 0 ? void 0 : previous.line) !== null && _c !== void 0 ? _c : 0) || events.length === 0)
            continue;
        if (events.length > batchSize)
            throw new Error("Factory session line ".concat(line, " maps to more than ").concat(batchSize, " events"));
        groups.push({ line: line, events: events });
        eventCount += events.length;
        if (eventCount > maxEventsPerFile)
            throw new Error("Factory session exceeds ".concat(maxEventsPerFile, " events"));
    }
    return {
        completeLines: safeLines, partialTail: !stoppedOnMalformed && start < bytes.length,
        malformed: malformed,
        prefixHashes: prefixHashes,
        completePrefixHash: prefixHasher.copy().digest('hex'),
        groups: groups,
        sessionStarted: mapper.sessionStarted,
    };
}
function mapSessionRecord(record, line, file, state, shouldEmit, includeContent) {
    var _a, _b, _c, _d, _e, _f, _g, _h, _j, _k, _l, _m, _o, _p;
    var type = (_b = (_a = safeLabel(record.type, 96)) === null || _a === void 0 ? void 0 : _a.toLowerCase()) !== null && _b !== void 0 ? _b : '';
    var id = safeIdentifier(record.id, 512);
    var events = [];
    if (type === 'session_start') {
        var previousStart = state.sessionStarted;
        state.sessionStarted = true;
        var model_1 = safeLabel((_c = record.model) !== null && _c !== void 0 ? _c : record.selectedModel, 128);
        if (model_1)
            state.model = model_1;
        if (shouldEmit && !previousStart)
            events.push(makeEvent(record, file, line, 'session.started', 'session', 'Factory Droid session start recovered from local transcript', "session.start:".concat(id !== null && id !== void 0 ? id : line), state));
        return events;
    }
    if (type === 'todo_state')
        return events;
    if (type === 'compaction_state') {
        if (shouldEmit) {
            var summary = boundedString(record.summaryText, 1 << 20);
            events.push(makeEvent(record, file, line, 'session.compacted', 'session', 'Factory Droid compaction observed', "session.compaction:".concat(id !== null && id !== void 0 ? id : line), state, { content: summary ? digestContent(summary) : undefined }));
        }
        return events;
    }
    if (type !== 'message' || !isRecord(record.message))
        return events;
    var message = record.message;
    if (message.visibility === 'user_only' || message.visibility === 'llm_only')
        return events;
    var model = safeLabel(message.model, 128);
    if (model)
        state.model = model;
    var role = (_e = (_d = boundedString(message.role, 32)) === null || _d === void 0 ? void 0 : _d.toLowerCase()) !== null && _e !== void 0 ? _e : '';
    var blocks = messageBlocks(message.content);
    for (var index = 0; index < blocks.length; index++) {
        var block = blocks[index];
        var blockType = (_g = (_f = safeLabel(block.type, 64)) === null || _f === void 0 ? void 0 : _f.toLowerCase()) !== null && _g !== void 0 ? _g : 'text';
        if (role === 'user' && blockType === 'text') {
            if (shouldEmit)
                pushTextEvent(events, record, file, line, 'prompt.submitted', 'prompt', blockText(block), includeContent, state, "prompt:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index));
            continue;
        }
        if (role !== 'assistant' && role !== 'tool')
            continue;
        if (blockType === 'thinking' && shouldEmit) {
            var text = blockText(block);
            if (text)
                events.push(makeEvent(record, file, line, 'agent.reasoning', 'agent', 'Factory Droid reasoning digest observed', "reasoning:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index), state, { content: digestContent(text) }));
        }
        else if (blockType === 'tool_use') {
            var toolId = (_h = safeIdentifier(block.id, 512)) !== null && _h !== void 0 ? _h : "".concat(id !== null && id !== void 0 ? id : line, ":tool:").concat(index);
            var name_1 = (_j = safeLabel(block.name, 128)) !== null && _j !== void 0 ? _j : '';
            var input = parseJsonValue(block.input);
            if (name_1) {
                rememberTool(state.tools, toolId, name_1);
                if (shouldEmit) {
                    var digest = digestContent(input);
                    events.push(makeEvent(record, file, line, 'tool.invoked', 'tool', 'Factory Droid tool invocation observed', "tool.invoke:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index), state, { toolName: name_1, toolCallHash: sha256(toolId), content: digest }));
                }
            }
        }
        else if (blockType === 'tool_result') {
            var toolId = (_l = safeIdentifier((_k = block.tool_use_id) !== null && _k !== void 0 ? _k : block.id, 512)) !== null && _l !== void 0 ? _l : "".concat(id !== null && id !== void 0 ? id : line, ":tool:").concat(index);
            var name_2 = (_p = safeLabel((_o = (_m = state.tools.get(toolId)) === null || _m === void 0 ? void 0 : _m.name) !== null && _o !== void 0 ? _o : block.name, 128)) !== null && _p !== void 0 ? _p : '';
            state.tools.delete(toolId);
            if (name_2 && shouldEmit) {
                var isError = block.is_error === true;
                events.push(makeEvent(record, file, line, isError ? 'tool.failed' : 'tool.completed', 'tool', isError ? 'Factory Droid tool failure observed' : 'Factory Droid tool result observed', "tool.result:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index), state, { severity: isError ? 'medium' : 'info', toolName: name_2, toolCallHash: sha256(toolId), content: digestContent(block.content) }));
            }
        }
        else if (role === 'tool' && blockType === 'text' && shouldEmit) {
            var text = blockText(block);
            var name_3 = safeLabel(message.name, 128);
            if (text)
                events.push(makeEvent(record, file, line, 'tool.completed', 'tool', 'Factory Droid tool output observed', "tool.message:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index), state, __assign(__assign({}, (name_3 ? { toolName: name_3 } : {})), { content: digestContent(text) })));
        }
        else if (role === 'assistant' && blockType === 'text' && shouldEmit) {
            var text = blockText(block);
            if (text)
                pushTextEvent(events, record, file, line, 'agent.message', 'agent', text, includeContent, state, "assistant:".concat(id !== null && id !== void 0 ? id : line, ":").concat(index));
        }
    }
    return events;
}
function messageBlocks(value) {
    if (Array.isArray(value))
        return value.filter(isRecord);
    if (typeof value === 'string' && value.trim())
        return [{ type: 'text', text: value }];
    if (isRecord(value))
        return [value];
    return [];
}
function blockText(block) {
    var _a, _b;
    var value = (_b = (_a = block.text) !== null && _a !== void 0 ? _a : block.thinking) !== null && _b !== void 0 ? _b : block.content;
    if (typeof value === 'string')
        return value.trim();
    if (Array.isArray(value))
        return value.flatMap(function (item) { return isRecord(item) ? [blockText(item)] : []; }).filter(Boolean).join('\n');
    return '';
}
function parseJsonValue(value) {
    if (typeof value !== 'string')
        return value;
    try {
        return JSON.parse(value);
    }
    catch (_a) {
        return value;
    }
}
function rememberTool(tools, id, name) {
    if (tools.size >= maxToolCorrelations && !tools.has(id))
        tools.delete(tools.keys().next().value);
    tools.set(id, { name: name });
}
function digestContent(value) {
    if (value === undefined || value === null)
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
function pushTextEvent(events, record, file, line, action, category, text, includeContent, state, suffix) {
    var value = text.trim();
    if (!value)
        return;
    var included = includeContent && Buffer.byteLength(value, 'utf8') <= maxContentBytes;
    events.push(makeEvent(record, file, line, action, category, action === 'prompt.submitted' ? 'Factory Droid prompt recovered from local transcript' : 'Factory Droid assistant message recovered from local transcript', suffix, state, __assign({ content: contentDigest(value, included, includeContent) }, (included ? { text: value } : {}))));
}
function makeEvent(record, file, line, action, category, message, suffix, state, extra) {
    var _a, _b, _c;
    if (extra === void 0) { extra = {}; }
    var recordId = safeIdentifier(record.id, 512);
    var rawSession = (_a = safeIdentifier(record.sessionId, 512)) !== null && _a !== void 0 ? _a : file.sessionId;
    var event = {
        timestamp: parseTimestamp(record.timestamp, file.mtimeMs), vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
        event: { id: sha256("".concat(sha256(file.key), ":").concat(line, ":").concat(recordId ? sha256(recordId) : '', ":").concat(suffix)), kind: 'agent_runtime', action: action, category: category, fidelity: 'observed' },
        severity: (_b = extra.severity) !== null && _b !== void 0 ? _b : 'info', endpoint: { os: normalizedOS(process.platform) },
        harness: { name: 'factory', collection_method: 'poll' },
        session: { id: "factory:".concat(sha256(rawSession)) },
        raw: __assign(__assign({ source: 'factory_session_jsonl', source_sha256: sha256(file.key), line: line, record_type: (_c = safeLabel(record.type, 96)) !== null && _c !== void 0 ? _c : 'unknown' }, (recordId ? { record_id_sha256: sha256(recordId) } : {})), { message: message }),
    };
    if (state.model)
        event.model = state.model;
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
function parseTimestamp(value, fallbackMs) {
    var date;
    if (typeof value === 'number' && Number.isFinite(value) && value >= 0)
        date = new Date(value < 1000000000000 ? value * 1000 : value);
    else if (typeof value === 'string') {
        var numeric = Number(value);
        date = value.trim() !== '' && Number.isFinite(numeric) && numeric >= 0
            ? new Date(numeric < 1000000000000 ? numeric * 1000 : numeric) : new Date(value);
    }
    return date && !Number.isNaN(date.getTime()) ? date.toISOString() : new Date(fallbackMs).toISOString();
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
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function boundedString(value, maximum) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= maximum ? value.trim() : undefined;
}
function safeIdentifier(value, maximum) {
    var text = boundedString(value, maximum);
    return text && /^[A-Za-z0-9_.:-]+$/.test(text) ? text : undefined;
}
function safeLabel(value, maximum) {
    var text = boundedString(value, maximum);
    if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
        /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text))
        return undefined;
    return text;
}
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
