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
exports.codexManagedMarker = void 0;
exports.installCodexProjectHooks = installCodexProjectHooks;
exports.removeCodexProjectHooks = removeCodexProjectHooks;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_path_1 = require("node:path");
exports.codexManagedMarker = '--threadforge-hook=codex';
var maxSettingsBytes = 8 << 20;
var installedHooks = {
    SessionStart: [{ hooks: [{ type: 'command', command: '', timeout: 5 }] }],
    PostToolUse: [{ hooks: [{ type: 'command', command: '', timeout: 5 }] }],
};
/** Installs observational hooks only; PreToolUse and permission hooks are deliberately excluded. */
function installCodexProjectHooks(projectRoot, command) {
    return __awaiter(this, void 0, void 0, function () {
        var filePath;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateCommand(command);
                    return [4 /*yield*/, hooksPath(projectRoot, true)];
                case 1:
                    filePath = _a.sent();
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var config, hooks, cleaned, next, _i, _a, _b, event_1, refs, additions;
                            return __generator(this, function (_c) {
                                switch (_c.label) {
                                    case 0: return [4 /*yield*/, readConfig(filePath)];
                                    case 1:
                                        config = _c.sent();
                                        hooks = readHookMap(config);
                                        cleaned = removeManagedHooks(hooks);
                                        next = __assign({}, cleaned.value);
                                        for (_i = 0, _a = Object.entries(installedHooks); _i < _a.length; _i++) {
                                            _b = _a[_i], event_1 = _b[0], refs = _b[1];
                                            additions = refs.map(function (group) { return (__assign(__assign({}, group), { hooks: group.hooks.map(function (hook) { return (__assign(__assign({}, hook), { command: command })); }) })); });
                                            next[event_1] = __spreadArray(__spreadArray([], readEventRefs(next, event_1), true), additions, true);
                                        }
                                        config.hooks = next;
                                        if (deepEqual(hooks, next))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(filePath, config)];
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
function removeCodexProjectHooks(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        var filePath;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, hooksPath(projectRoot, false)];
                case 1:
                    filePath = _a.sent();
                    if (!filePath)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var config, hooks, cleaned;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(filePath)];
                                    case 1:
                                        config = _a.sent();
                                        hooks = readHookMap(config);
                                        cleaned = removeManagedHooks(hooks);
                                        if (!cleaned.found)
                                            return [2 /*return*/, 'absent'];
                                        if (Object.keys(cleaned.value).length === 0)
                                            delete config.hooks;
                                        else
                                            config.hooks = cleaned.value;
                                        if (!(Object.keys(config).length === 0)) return [3 /*break*/, 3];
                                        return [4 /*yield*/, (0, promises_1.unlink)(filePath)];
                                    case 2:
                                        _a.sent();
                                        return [3 /*break*/, 5];
                                    case 3: return [4 /*yield*/, atomicWrite(filePath, config)];
                                    case 4:
                                        _a.sent();
                                        _a.label = 5;
                                    case 5: return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function hooksPath(projectRoot, create) {
    return __awaiter(this, void 0, void 0, function () {
        var root, rootStat, codexDir, error_1, dirStat;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('project root must be absolute');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    root = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(root)];
                case 2:
                    rootStat = _a.sent();
                    if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
                        throw new Error('project root must be a real directory');
                    codexDir = node_path_1.default.join(root, '.codex');
                    if (!create) return [3 /*break*/, 6];
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(codexDir)];
                case 4:
                    _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create Codex project directory');
                    return [3 /*break*/, 6];
                case 6: return [4 /*yield*/, (0, promises_1.lstat)(codexDir).catch(function () { return null; })];
                case 7:
                    dirStat = _a.sent();
                    if (!dirStat)
                        return [2 /*return*/, undefined];
                    if (!dirStat.isDirectory() || dirStat.isSymbolicLink())
                        throw new Error('Codex project path contains a symbolic link or non-directory');
                    return [2 /*return*/, node_path_1.default.join(codexDir, 'hooks.json')];
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
                    lockPath = node_path_1.default.join(node_path_1.default.dirname(filePath), '.threadforge-codex-hooks.lock');
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
                        throw new Error('Codex hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Codex hooks configuration');
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
function readConfig(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, value, _a, _b, error_3;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    _c.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _c.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Codex hooks config must be a regular file');
                    if (stat.size > maxSettingsBytes)
                        throw new Error('Codex hooks config exceeds the local size limit');
                    _b = (_a = JSON).parse;
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                case 2:
                    value = _b.apply(_a, [(_c.sent()).replace(/^\uFEFF/, '')]);
                    if (!isRecord(value))
                        throw new Error('Codex hooks config root must be an object');
                    return [2 /*return*/, value];
                case 3:
                    error_3 = _c.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, {}];
                    if (error_3 instanceof Error && error_3.message.startsWith('Codex hooks config'))
                        throw error_3;
                    throw new Error('Codex hooks config is invalid strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readHookMap(config) {
    if (config.hooks === undefined || config.hooks === null)
        return {};
    if (!isRecord(config.hooks))
        throw new Error('Codex hooks config hooks must be an object; no changes were made');
    return config.hooks;
}
function readEventRefs(hooks, event) {
    var raw = hooks[event];
    if (raw === undefined || raw === null)
        return [];
    if (!Array.isArray(raw) || raw.some(function (group) { return !isRecord(group) || !Array.isArray(group.hooks); })) {
        throw new Error("Codex hooks.".concat(event, " must contain hook groups; no changes were made"));
    }
    return raw;
}
function removeManagedHooks(hooks) {
    var value = __assign({}, hooks);
    var found = false;
    var _loop_1 = function (event_2, rawGroups) {
        if (rawGroups === null)
            return "continue";
        if (!Array.isArray(rawGroups))
            throw new Error("Codex hooks.".concat(event_2, " must be an array; no changes were made"));
        var keptGroups = [];
        for (var _c = 0, rawGroups_1 = rawGroups; _c < rawGroups_1.length; _c++) {
            var group = rawGroups_1[_c];
            if (!isRecord(group) || !Array.isArray(group.hooks))
                throw new Error("Codex hooks.".concat(event_2, " contains an unsupported group; no changes were made"));
            var keptRefs = group.hooks.filter(function (ref) {
                if (!isRecord(ref))
                    throw new Error("Codex hooks.".concat(event_2, " contains an unsupported hook reference; no changes were made"));
                var managed = ref.type === 'command' && typeof ref.command === 'string' && ref.command.includes(exports.codexManagedMarker);
                found || (found = managed);
                return !managed;
            });
            if (keptRefs.length === group.hooks.length)
                keptGroups.push(group);
            else if (keptRefs.length > 0)
                keptGroups.push(__assign(__assign({}, group), { hooks: keptRefs }));
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
                    throw new Error('unable to atomically update Codex hooks config');
                case 14: return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || command.trim() === '' || command.length > 8192 || /[\r\n\0]/.test(command)) {
        throw new Error('Codex hook command is invalid or too long');
    }
    if (!command.includes(exports.codexManagedMarker))
        throw new Error("Codex hook command must include ".concat(exports.codexManagedMarker));
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
