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
exports.claudeManagedMarker = void 0;
exports.installClaudeProjectHooks = installClaudeProjectHooks;
exports.removeClaudeProjectHooks = removeClaudeProjectHooks;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_path_1 = require("node:path");
exports.claudeManagedMarker = '--threadforge-hook=claude';
var maxSettingsBytes = 8 << 20;
var hookGroups = {
    SessionStart: [{ hooks: [{ type: 'command', command: '' }] }],
    UserPromptSubmit: [{ hooks: [{ type: 'command', command: '' }] }],
    PreToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
    PostToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
    PostToolUseFailure: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
    Stop: [{ hooks: [{ type: 'command', command: '' }] }],
    SubagentStart: [{ hooks: [{ type: 'command', command: '' }] }],
    SubagentStop: [{ hooks: [{ type: 'command', command: '' }] }],
    PermissionRequest: [{ matcher: '*', hooks: [{ type: 'command', command: '' }] }],
    SessionEnd: [{ hooks: [{ type: 'command', command: '' }] }],
};
/** Installs one non-blocking observational command in project-level Claude hooks. */
function installClaudeProjectHooks(projectRoot, options) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateCommand(options.command);
            return [2 /*return*/, mutateClaudeSettings(projectRoot, function (settings) {
                    var hooks = readHookMap(settings);
                    var cleaned = removeManagedHooks(hooks);
                    var next = __assign({}, cleaned.value);
                    for (var _i = 0, _a = Object.entries(hookGroups); _i < _a.length; _i++) {
                        var _b = _a[_i], event_1 = _b[0], groups = _b[1];
                        var current = readEventGroups(next, event_1);
                        var additions = groups.map(function (group) { return (__assign(__assign({}, group), { hooks: group.hooks.map(function (hook) { return (__assign(__assign({}, hook), { command: options.command })); }) })); });
                        next[event_1] = __spreadArray(__spreadArray([], current, true), additions, true);
                    }
                    settings.hooks = next;
                    var result = cleaned.found ? 'updated' : 'installed';
                    return { result: deepEqual(cleaned.original, next) ? 'unchanged' : result, changed: !deepEqual(hooks, next) };
                })];
        });
    });
}
/** Removes only command hook references carrying Threadforge's explicit ownership marker. */
function removeClaudeProjectHooks(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            return [2 /*return*/, mutateClaudeSettings(projectRoot, function (settings) {
                    if (settings.hooks === undefined || settings.hooks === null)
                        return { result: 'absent', changed: false };
                    var hooks = readHookMap(settings);
                    var cleaned = removeManagedHooks(hooks);
                    if (!cleaned.found)
                        return { result: 'absent', changed: false };
                    if (Object.keys(cleaned.value).length === 0)
                        delete settings.hooks;
                    else
                        settings.hooks = cleaned.value;
                    return { result: 'removed', changed: true };
                })];
        });
    });
}
function removeManagedHooks(hooks) {
    var result = __assign({}, hooks);
    var found = false;
    var _loop_1 = function (event_2, rawGroups) {
        if (rawGroups === null)
            return "continue";
        if (!Array.isArray(rawGroups))
            throw new Error("Claude hooks.".concat(event_2, " must be an array; no changes were made"));
        var retainedGroups = [];
        for (var _c = 0, rawGroups_1 = rawGroups; _c < rawGroups_1.length; _c++) {
            var rawGroup = rawGroups_1[_c];
            if (!isRecord(rawGroup))
                throw new Error("Claude hooks.".concat(event_2, " contains an unsupported group; no changes were made"));
            if (!Array.isArray(rawGroup.hooks))
                throw new Error("Claude hooks.".concat(event_2, " group has no hooks array; no changes were made"));
            var retainedRefs = rawGroup.hooks.filter(function (rawRef) {
                if (!isRecord(rawRef))
                    throw new Error("Claude hooks.".concat(event_2, " contains an unsupported hook reference; no changes were made"));
                var managed = rawRef.type === 'command' && typeof rawRef.command === 'string' && rawRef.command.includes(exports.claudeManagedMarker);
                if (managed)
                    found = true;
                return !managed;
            });
            if (retainedRefs.length === rawGroup.hooks.length || retainedRefs.length > 0) {
                retainedGroups.push(retainedRefs.length === rawGroup.hooks.length ? rawGroup : __assign(__assign({}, rawGroup), { hooks: retainedRefs }));
            }
        }
        if (retainedGroups.length === rawGroups.length && retainedGroups.every(function (group, index) { return group === rawGroups[index]; }))
            return "continue";
        if (retainedGroups.length === 0)
            delete result[event_2];
        else
            result[event_2] = retainedGroups;
    };
    for (var _i = 0, _a = Object.entries(hooks); _i < _a.length; _i++) {
        var _b = _a[_i], event_2 = _b[0], rawGroups = _b[1];
        _loop_1(event_2, rawGroups);
    }
    return { value: result, found: found, original: hooks };
}
function readHookMap(settings) {
    if (settings.hooks === undefined || settings.hooks === null)
        return {};
    if (!isRecord(settings.hooks))
        throw new Error('Claude settings hooks must be an object; no changes were made');
    return settings.hooks;
}
function readEventGroups(hooks, event) {
    var value = hooks[event];
    if (value === undefined || value === null)
        return [];
    if (!Array.isArray(value))
        throw new Error("Claude hooks.".concat(event, " must be an array; no changes were made"));
    return value;
}
function mutateClaudeSettings(projectRoot, mutate) {
    return __awaiter(this, void 0, void 0, function () {
        var canonicalRoot, rootStat, settingsDir, error_1, dirStat, filePath, lockPath, lock, error_2, settings, outcome;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('project root must be absolute');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    canonicalRoot = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(canonicalRoot)];
                case 2:
                    rootStat = _a.sent();
                    if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
                        throw new Error('project root must be a real directory');
                    settingsDir = node_path_1.default.join(canonicalRoot, '.claude');
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(settingsDir)];
                case 4:
                    _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create Claude project settings directory');
                    return [3 /*break*/, 6];
                case 6: return [4 /*yield*/, (0, promises_1.lstat)(settingsDir).catch(function () { return null; })];
                case 7:
                    dirStat = _a.sent();
                    if (!(dirStat === null || dirStat === void 0 ? void 0 : dirStat.isDirectory()) || dirStat.isSymbolicLink())
                        throw new Error('Claude project settings path contains a symbolic link or non-directory');
                    filePath = node_path_1.default.join(settingsDir, 'settings.json');
                    lockPath = node_path_1.default.join(settingsDir, '.threadforge-settings.lock');
                    _a.label = 8;
                case 8:
                    _a.trys.push([8, 10, , 11]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 9:
                    lock = _a.sent();
                    return [3 /*break*/, 11];
                case 10:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'EEXIST'))
                        throw new Error('Claude project settings are locked by another writer or a stale lock');
                    throw new Error('unable to lock Claude project settings');
                case 11:
                    _a.trys.push([11, , 14, 17]);
                    return [4 /*yield*/, readSettings(filePath)];
                case 12:
                    settings = _a.sent();
                    outcome = mutate(settings);
                    if (!outcome.changed)
                        return [2 /*return*/, outcome.result];
                    return [4 /*yield*/, atomicWrite(filePath, settings)];
                case 13:
                    _a.sent();
                    return [2 /*return*/, outcome.result];
                case 14: return [4 /*yield*/, lock.close().catch(function () { })];
                case 15:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 16:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 17: return [2 /*return*/];
            }
        });
    });
}
function readSettings(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, text, value, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _a.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Claude settings must be a regular file');
                    if (stat.size > maxSettingsBytes)
                        throw new Error('Claude settings exceed the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                case 2:
                    text = (_a.sent()).replace(/^\uFEFF/, '');
                    value = JSON.parse(text);
                    if (!isRecord(value))
                        throw new Error('settings root must be an object');
                    return [2 /*return*/, value];
                case 3:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, {}];
                    if (error_3 instanceof Error && error_3.message.startsWith('Claude settings'))
                        throw error_3;
                    throw new Error('Claude project settings are invalid strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function atomicWrite(filePath, value) {
    return __awaiter(this, void 0, void 0, function () {
        var tempPath, handle, mode, error_4, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    tempPath = "".concat(filePath, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 11, , 14]);
                    mode = 384;
                    _b.label = 2;
                case 2:
                    _b.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 3:
                    mode = (_b.sent()).mode & 511;
                    return [3 /*break*/, 5];
                case 4:
                    error_4 = _b.sent();
                    if (!isCode(error_4, 'ENOENT'))
                        throw error_4;
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
                case 6:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.writeFile("".concat(JSON.stringify(value, null, 2), "\n"), 'utf8')];
                case 7:
                    _b.sent();
                    return [4 /*yield*/, handle.sync()];
                case 8:
                    _b.sent();
                    return [4 /*yield*/, handle.close()];
                case 9:
                    _b.sent();
                    handle = undefined;
                    return [4 /*yield*/, (0, promises_1.rename)(tempPath, filePath)];
                case 10:
                    _b.sent();
                    return [3 /*break*/, 14];
                case 11:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 12:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(tempPath).catch(function () { })];
                case 13:
                    _b.sent();
                    throw new Error('unable to atomically update Claude project settings');
                case 14: return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
        throw new Error('Claude hook command is invalid or too long');
    }
    if (!command.includes(exports.claudeManagedMarker)) {
        throw new Error("Claude hook command must include the ownership marker ".concat(exports.claudeManagedMarker));
    }
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
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return isRecord(error) && error.code === code;
}
