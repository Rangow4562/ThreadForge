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
exports.installQwenHooks = installQwenHooks;
exports.removeQwenHooks = removeQwenHooks;
exports.qwenSettingsPath = qwenSettingsPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var qwen_hook_ts_1 = require("./qwen-hook.ts");
var maxSettingsBytes = 8 << 20;
var lockName = '.threadforge-qwen-hooks.lock';
var allToolsMatcher = '*';
var hookEvents = {
    SessionStart: {},
    UserPromptSubmit: { timeout: 30000 },
    PreToolUse: { matcher: allToolsMatcher, timeout: 10000 },
    PostToolUse: { matcher: allToolsMatcher, timeout: 10000 },
    PostToolUseFailure: { matcher: allToolsMatcher, timeout: 10000 },
    PermissionRequest: { matcher: allToolsMatcher, timeout: 10000 },
    Stop: { timeout: 45000 },
    SubagentStart: {},
    SubagentStop: {},
    SessionEnd: {},
};
/** Installs Qwen hooks without changing its global hook-disable or project-trust controls. */
function installQwenHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var scope, filePath;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    validateCommand(options.command);
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, settingsPath(scope, options.projectRoot, options.homeDir, true)];
                case 1:
                    filePath = _b.sent();
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, settings, before, hooks, cleaned, next, _i, _a, _b, event_1, eventOptions, groups, hookRef, group;
                            return __generator(this, function (_c) {
                                switch (_c.label) {
                                    case 0: return [4 /*yield*/, readSettings(filePath)];
                                    case 1:
                                        read = _c.sent();
                                        settings = read.value;
                                        before = JSON.stringify(canonicalize(settings));
                                        if (settings.disableAllHooks === true)
                                            throw new Error('Qwen disableAllHooks is true; enable hooks in Qwen settings before installing Threadforge');
                                        hooks = readHookMap(settings);
                                        cleaned = removeManagedHooks(hooks);
                                        next = __assign({}, cleaned.value);
                                        for (_i = 0, _a = Object.entries(hookEvents); _i < _a.length; _i++) {
                                            _b = _a[_i], event_1 = _b[0], eventOptions = _b[1];
                                            groups = readEventGroups(next, event_1);
                                            hookRef = __assign({ type: 'command', shell: 'bash', command: options.command }, (eventOptions.timeout === undefined ? {} : { timeout: eventOptions.timeout }));
                                            group = __assign(__assign({}, (eventOptions.matcher === undefined ? {} : { matcher: eventOptions.matcher })), { hooks: [hookRef] });
                                            next[event_1] = appendToEmptyGroup(groups, group, hookRef, eventOptions.matcher);
                                        }
                                        settings.hooks = next;
                                        if (before === JSON.stringify(canonicalize(settings)))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(filePath, read.snapshot, settings)];
                                    case 2:
                                        _c.sent();
                                        return [2 /*return*/, cleaned.found ? 'updated' : 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function removeQwenHooks() {
    return __awaiter(this, arguments, void 0, function (options) {
        var scope, filePath;
        var _this = this;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, settingsPath(scope, options.projectRoot, options.homeDir, false)];
                case 1:
                    filePath = _b.sent();
                    if (!filePath)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, settings, hooks, cleaned;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readSettings(filePath)];
                                    case 1:
                                        read = _a.sent();
                                        settings = read.value;
                                        if (settings.hooks === undefined || settings.hooks === null)
                                            return [2 /*return*/, 'absent'];
                                        hooks = readHookMap(settings);
                                        cleaned = removeManagedHooks(hooks);
                                        if (!cleaned.found)
                                            return [2 /*return*/, 'absent'];
                                        if (Object.keys(cleaned.value).length === 0)
                                            delete settings.hooks;
                                        else
                                            settings.hooks = cleaned.value;
                                        return [4 /*yield*/, atomicWrite(filePath, read.snapshot, settings)];
                                    case 2:
                                        _a.sent();
                                        return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function qwenSettingsPath(scope, root) {
    return node_path_1.default.join(root, '.qwen', 'settings.json');
}
function settingsPath(scope, projectRoot, homeDir, create) {
    return __awaiter(this, void 0, void 0, function () {
        var root, home, rootStat, qwenDir, error_1, dirStat, actualDir, filePath, fileStat;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (scope !== 'user' && scope !== 'project')
                        throw new Error('Qwen hook scope must be user or project');
                    if (!(scope === 'project')) return [3 /*break*/, 2];
                    if (!projectRoot || !node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('project scope requires an absolute project root');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    root = _a.sent();
                    return [3 /*break*/, 4];
                case 2:
                    home = homeDir !== null && homeDir !== void 0 ? homeDir : node_os_1.default.homedir();
                    if (!node_path_1.default.isAbsolute(home))
                        throw new Error('Qwen home directory must be absolute');
                    return [4 /*yield*/, (0, promises_1.realpath)(home).catch(function () { throw new Error('Qwen home directory must exist and be accessible'); })];
                case 3:
                    root = _a.sent();
                    _a.label = 4;
                case 4: return [4 /*yield*/, (0, promises_1.lstat)(root)];
                case 5:
                    rootStat = _a.sent();
                    if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
                        throw new Error('Qwen settings root must be a real directory');
                    qwenDir = node_path_1.default.join(root, '.qwen');
                    if (!create) return [3 /*break*/, 9];
                    _a.label = 6;
                case 6:
                    _a.trys.push([6, 8, , 9]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(qwenDir, { mode: 448 })];
                case 7:
                    _a.sent();
                    return [3 /*break*/, 9];
                case 8:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create Qwen settings directory');
                    return [3 /*break*/, 9];
                case 9: return [4 /*yield*/, (0, promises_1.lstat)(qwenDir).catch(function (error) {
                        if (!create && isCode(error, 'ENOENT'))
                            return null;
                        throw error;
                    })];
                case 10:
                    dirStat = _a.sent();
                    if (!dirStat)
                        return [2 /*return*/, undefined];
                    if (!dirStat.isDirectory() || dirStat.isSymbolicLink())
                        throw new Error('Qwen settings path contains a symbolic link or non-directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(qwenDir).catch(function () { throw new Error('Qwen settings directory is inaccessible'); })];
                case 11:
                    actualDir = _a.sent();
                    if (!isWithin(root, actualDir))
                        throw new Error('Qwen settings directory resolves outside its selected root');
                    filePath = node_path_1.default.join(actualDir, 'settings.json');
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw error;
                        })];
                case 12:
                    fileStat = _a.sent();
                    if (fileStat && (!fileStat.isFile() || fileStat.isSymbolicLink()))
                        throw new Error('Qwen settings file must be regular and not a symbolic link');
                    if (fileStat && fileStat.size > maxSettingsBytes)
                        throw new Error('Qwen settings exceed the local size limit');
                    return [2 /*return*/, fileStat || create ? filePath : undefined];
            }
        });
    });
}
function withLock(filePath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lockPath, lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    lockPath = node_path_1.default.join(node_path_1.default.dirname(filePath), lockName);
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    lock = _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'EEXIST'))
                        throw new Error('Qwen hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Qwen settings');
                case 4:
                    _a.trys.push([4, , 6, 9]);
                    return [4 /*yield*/, action()];
                case 5: return [2 /*return*/, _a.sent()];
                case 6: return [4 /*yield*/, lock.close().catch(function () { })];
                case 7:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 8:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function readSettings(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, bytes, text, value, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _a.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Qwen settings file must be regular and not a symbolic link');
                    if (stat.size > maxSettingsBytes)
                        throw new Error('Qwen settings exceed the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath)];
                case 2:
                    bytes = _a.sent();
                    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
                    value = JSON.parse(text);
                    if (!isRecord(value))
                        throw new Error('Qwen settings root must be an object');
                    return [2 /*return*/, { value: value, snapshot: { identity: { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs } } }];
                case 3:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, { value: {}, snapshot: {} }];
                    if (error_3 instanceof Error && error_3.message.startsWith('Qwen settings'))
                        throw error_3;
                    throw new Error('Qwen settings are invalid UTF-8 strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readHookMap(settings) {
    if (settings.hooks === undefined || settings.hooks === null)
        return {};
    if (!isRecord(settings.hooks))
        throw new Error('Qwen settings hooks must be an object; no changes were made');
    return settings.hooks;
}
function readEventGroups(hooks, event) {
    var current = hooks[event];
    if (current === undefined || current === null)
        return [];
    if (!Array.isArray(current) || current.some(function (group) { return !isRecord(group) || !Array.isArray(group.hooks); })) {
        throw new Error("Qwen hooks.".concat(event, " must contain hook groups; no changes were made"));
    }
    return current;
}
function appendToEmptyGroup(groups, desired, hook, matcher) {
    var index = groups.findIndex(function (group) { return isRecord(group) && Array.isArray(group.hooks) && group.hooks.length === 0 &&
        (matcher === undefined ? !Object.hasOwn(group, 'matcher') : group.matcher === matcher); });
    if (index < 0)
        return __spreadArray(__spreadArray([], groups, true), [desired], false);
    return groups.map(function (group, current) { return current === index && isRecord(group) ? __assign(__assign({}, group), { hooks: [hook] }) : group; });
}
function removeManagedHooks(hooks) {
    var value = __assign({}, hooks);
    var found = false;
    var _loop_1 = function (event_2, rawGroups) {
        if (rawGroups === null)
            return "continue";
        if (!Array.isArray(rawGroups))
            throw new Error("Qwen hooks.".concat(event_2, " must be an array; no changes were made"));
        var keptGroups = [];
        for (var _c = 0, rawGroups_1 = rawGroups; _c < rawGroups_1.length; _c++) {
            var group = rawGroups_1[_c];
            if (!isRecord(group) || !Array.isArray(group.hooks))
                throw new Error("Qwen hooks.".concat(event_2, " contains an unsupported group; no changes were made"));
            var keptRefs = group.hooks.filter(function (hook) {
                if (!isRecord(hook))
                    throw new Error("Qwen hooks.".concat(event_2, " contains an unsupported hook ref; no changes were made"));
                var managed = hook.type === 'command' && typeof hook.command === 'string' && hook.command.includes(qwen_hook_ts_1.qwenManagedMarker);
                found || (found = managed);
                return !managed;
            });
            if (keptRefs.length > 0)
                keptGroups.push(keptRefs.length === group.hooks.length ? group : __assign(__assign({}, group), { hooks: keptRefs }));
            else if (Object.keys(group).some(function (key) { return key !== 'hooks' && key !== 'matcher'; }))
                keptGroups.push(__assign(__assign({}, group), { hooks: [] }));
        }
        if (keptGroups.length === 0)
            delete value[event_2];
        else
            value[event_2] = keptGroups;
    };
    for (var _i = 0, _a = Object.entries(hooks); _i < _a.length; _i++) {
        var _b = _a[_i], event_2 = _b[0], rawGroups = _b[1];
        _loop_1(event_2, rawGroups);
    }
    return { value: value, found: found };
}
function atomicWrite(filePath, snapshot, value) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, tempPath, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from("".concat(JSON.stringify(value, null, 2), "\n"), 'utf8');
                    if (bytes.byteLength > maxSettingsBytes)
                        throw new Error('Qwen settings would exceed the local size limit');
                    tempPath = "".concat(filePath, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    mode = snapshot.identity ? snapshot.identity.mode & 511 : 384;
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 10, , 13]);
                    return [4 /*yield*/, assertUnchanged(filePath, snapshot)];
                case 2:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
                case 3:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.chmod(mode)];
                case 4:
                    _b.sent();
                    return [4 /*yield*/, handle.writeFile(bytes)];
                case 5:
                    _b.sent();
                    return [4 /*yield*/, handle.sync()];
                case 6:
                    _b.sent();
                    return [4 /*yield*/, handle.close()];
                case 7:
                    _b.sent();
                    handle = undefined;
                    return [4 /*yield*/, assertUnchanged(filePath, snapshot)];
                case 8:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.rename)(tempPath, filePath)];
                case 9:
                    _b.sent();
                    return [3 /*break*/, 13];
                case 10:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(tempPath).catch(function () { })];
                case 12:
                    _b.sent();
                    throw new Error('unable to atomically update Qwen settings');
                case 13: return [2 /*return*/];
            }
        });
    });
}
function assertUnchanged(filePath, snapshot) {
    return __awaiter(this, void 0, void 0, function () {
        var current;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw error;
                    })];
                case 1:
                    current = _a.sent();
                    if (!snapshot.identity) {
                        if (current)
                            throw new Error('Qwen settings appeared during update');
                        return [2 /*return*/];
                    }
                    if (!current || !current.isFile() || current.isSymbolicLink() || current.dev !== snapshot.identity.dev ||
                        current.ino !== snapshot.identity.ino || current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Qwen settings changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
        throw new Error('Qwen hook command is invalid or too long');
    }
    if (!command.includes(qwen_hook_ts_1.qwenManagedMarker))
        throw new Error("Qwen hook command must include ".concat(qwen_hook_ts_1.qwenManagedMarker));
    if (looksSecretLike(command))
        throw new Error('Qwen hook command must not contain credential-like values');
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function deepEqual(left, right) {
    return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
}
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return isRecord(error) && error.code === code;
}
