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
exports.installMuseHooks = installMuseHooks;
exports.updateMuseHooks = updateMuseHooks;
exports.removeMuseHooks = removeMuseHooks;
exports.museHooksConfigPath = museHooksConfigPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var muse_hook_ts_1 = require("./muse-hook.ts");
var lockName = '.threadforge-muse-hooks.lock';
var maxConfigBytes = 8 << 20;
var definitions = [
    { event: 'SessionStart', timeout: 10 },
    { event: 'UserPromptSubmit', timeout: 30 },
    { event: 'PreToolUse', timeout: 10 },
    { event: 'PermissionRequest', timeout: 10 },
    { event: 'PostToolUse', timeout: 10 },
    { event: 'PostToolUseFailure', timeout: 10 },
    { event: 'PreCompact', timeout: 10 },
    { event: 'PostCompact', timeout: 10 },
    { event: 'SubagentStart', timeout: 10 },
    { event: 'SubagentStop', timeout: 10 },
    { event: 'Notification', timeout: 10 },
    { event: 'Stop', timeout: 45 },
    { event: 'SessionEnd', timeout: 10 },
];
/** Installs user or project Muse hooks. Managed hooks are intentionally not claimed or replaced. */
function installMuseHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateCommand(options.command);
                    return [4 /*yield*/, configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, true)];
                case 1:
                    file = _a.sent();
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, config, before, hooks, cleaned, nextHooks, _i, definitions_1, _a, event_1, timeout, groups, handler;
                            return __generator(this, function (_b) {
                                switch (_b.label) {
                                    case 0: return [4 /*yield*/, readConfig(file, options.scope)];
                                    case 1:
                                        read = _b.sent();
                                        config = read.value;
                                        before = JSON.stringify(canonicalize(config));
                                        hooks = readHookMap(config);
                                        cleaned = removeManagedHooks(hooks);
                                        nextHooks = __assign({}, cleaned.value);
                                        for (_i = 0, definitions_1 = definitions; _i < definitions_1.length; _i++) {
                                            _a = definitions_1[_i], event_1 = _a.event, timeout = _a.timeout;
                                            groups = readEventGroups(nextHooks, event_1);
                                            handler = {
                                                type: 'command', command: "".concat(options.command, " --hook-event=").concat(event_1),
                                                timeout: timeout,
                                            };
                                            nextHooks[event_1] = __spreadArray(__spreadArray([], groups, true), [{ matcher: '*', hooks: [handler] }], false);
                                        }
                                        config.hooks = nextHooks;
                                        if (before === JSON.stringify(canonicalize(config)))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, config, options.scope)];
                                    case 2:
                                        _b.sent();
                                        return [2 /*return*/, cleaned.found ? 'updated' : 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function updateMuseHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateCommand(options.command);
                    return [4 /*yield*/, configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, false)];
                case 1:
                    file = _a.sent();
                    if (!file)
                        throw new Error('no Muse hook configuration exists to update');
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, hooks, cleaned, nextHooks, _i, definitions_2, _a, event_2, timeout, next;
                            return __generator(this, function (_b) {
                                switch (_b.label) {
                                    case 0: return [4 /*yield*/, readConfig(file, options.scope)];
                                    case 1:
                                        read = _b.sent();
                                        hooks = readHookMap(read.value);
                                        cleaned = removeManagedHooks(hooks);
                                        if (!cleaned.found)
                                            throw new Error('no Threadforge-owned Muse hooks exist to update');
                                        nextHooks = __assign({}, cleaned.value);
                                        for (_i = 0, definitions_2 = definitions; _i < definitions_2.length; _i++) {
                                            _a = definitions_2[_i], event_2 = _a.event, timeout = _a.timeout;
                                            nextHooks[event_2] = __spreadArray(__spreadArray([], readEventGroups(nextHooks, event_2), true), [{
                                                    matcher: '*', hooks: [{ type: 'command', command: "".concat(options.command, " --hook-event=").concat(event_2), timeout: timeout }],
                                                }], false);
                                        }
                                        next = __assign(__assign({}, read.value), { hooks: nextHooks });
                                        if (JSON.stringify(canonicalize(read.value)) === JSON.stringify(canonicalize(next)))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, next, options.scope)];
                                    case 2:
                                        _b.sent();
                                        return [2 /*return*/, 'updated'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function removeMuseHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, configPath(options.scope, options.projectRoot, options.xdgConfigHome, options.homeDir, false)];
                case 1:
                    file = _a.sent();
                    if (!file)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, cleaned, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(file, options.scope)];
                                    case 1:
                                        read = _a.sent();
                                        if (read.value.hooks === undefined)
                                            return [2 /*return*/, 'absent'];
                                        cleaned = removeManagedHooks(readHookMap(read.value));
                                        if (!cleaned.found)
                                            return [2 /*return*/, 'absent'];
                                        next = __assign({}, read.value);
                                        if (Object.keys(cleaned.value).length === 0)
                                            delete next.hooks;
                                        else
                                            next.hooks = cleaned.value;
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, next, options.scope)];
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
function museHooksConfigPath(scope, root) {
    return scope === 'project'
        ? node_path_1.default.join(root, '.muse', 'hooks.json')
        : node_path_1.default.join(root, 'muse', 'settings.json');
}
function configPath(scope, projectRoot, xdgConfigHome, homeDir, create) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInput, components, rootInfo, root, directory, _i, components_1, component, info_1, resolved, fileName, file, info;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    if (scope !== 'user' && scope !== 'project')
                        throw new Error('Muse hook scope must be user or project');
                    if (scope === 'project') {
                        if (!projectRoot || !node_path_1.default.isAbsolute(projectRoot))
                            throw new Error('project scope requires an absolute project root');
                        rootInput = projectRoot;
                        components = ['.muse'];
                    }
                    else if (xdgConfigHome !== null && xdgConfigHome !== void 0 ? xdgConfigHome : process.env.XDG_CONFIG_HOME) {
                        rootInput = (_a = xdgConfigHome !== null && xdgConfigHome !== void 0 ? xdgConfigHome : process.env.XDG_CONFIG_HOME) !== null && _a !== void 0 ? _a : '';
                        if (!node_path_1.default.isAbsolute(rootInput))
                            throw new Error('XDG_CONFIG_HOME must be an absolute directory');
                        components = ['muse'];
                    }
                    else {
                        rootInput = homeDir !== null && homeDir !== void 0 ? homeDir : node_os_1.default.homedir();
                        components = ['.config', 'muse'];
                    }
                    if (!node_path_1.default.isAbsolute(rootInput))
                        throw new Error('Muse configuration root must be an absolute directory');
                    return [4 /*yield*/, (0, promises_1.lstat)(rootInput).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                throw new Error('Muse configuration root must exist and be accessible');
                            throw error;
                        })];
                case 1:
                    rootInfo = _b.sent();
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Muse configuration root must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(rootInput).catch(function () { throw new Error('Muse configuration root must exist and be accessible'); })];
                case 2:
                    root = _b.sent();
                    directory = root;
                    _i = 0, components_1 = components;
                    _b.label = 3;
                case 3:
                    if (!(_i < components_1.length)) return [3 /*break*/, 7];
                    component = components_1[_i];
                    directory = node_path_1.default.join(directory, component);
                    return [4 /*yield*/, (0, promises_1.lstat)(directory).catch(function (error) { return __awaiter(_this, void 0, void 0, function () {
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0:
                                        if (!isCode(error, 'ENOENT'))
                                            throw error;
                                        if (!create)
                                            return [2 /*return*/, undefined];
                                        return [4 /*yield*/, (0, promises_1.mkdir)(directory, { mode: scope === 'user' ? 448 : 493 }).catch(function (mkdirError) {
                                                if (!isCode(mkdirError, 'EEXIST'))
                                                    throw new Error('unable to create Muse hooks directory');
                                            })];
                                    case 1:
                                        _a.sent();
                                        return [2 /*return*/, (0, promises_1.lstat)(directory)];
                                }
                            });
                        }); })];
                case 4:
                    info_1 = _b.sent();
                    if (!info_1)
                        return [2 /*return*/, undefined];
                    if (!info_1.isDirectory() || info_1.isSymbolicLink())
                        throw new Error('Muse hooks path contains a symbolic link or non-directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(directory).catch(function () { throw new Error('Muse hooks directory is inaccessible'); })];
                case 5:
                    resolved = _b.sent();
                    if (!isWithin(root, resolved))
                        throw new Error('Muse hooks directory resolves outside its selected root');
                    directory = resolved;
                    _b.label = 6;
                case 6:
                    _i++;
                    return [3 /*break*/, 3];
                case 7:
                    fileName = scope === 'project' ? 'hooks.json' : 'settings.json';
                    file = node_path_1.default.join(directory, fileName);
                    return [4 /*yield*/, (0, promises_1.lstat)(file).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return undefined;
                            throw error;
                        })];
                case 8:
                    info = _b.sent();
                    if (info && (!info.isFile() || info.isSymbolicLink()))
                        throw new Error('Muse hook config must be a regular file and not a symbolic link');
                    if (info && info.size > maxConfigBytes)
                        throw new Error('Muse hook config exceeds the local size limit');
                    return [2 /*return*/, info || create ? file : undefined];
            }
        });
    });
}
function withLock(file, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lockPath, lock, error_1;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    lockPath = node_path_1.default.join(node_path_1.default.dirname(file), lockName);
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    lock = _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'EEXIST'))
                        throw new Error('Muse hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Muse hook config');
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
function readConfig(file, scope) {
    return __awaiter(this, void 0, void 0, function () {
        var info, bytes, text, value, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(file)];
                case 1:
                    info = _a.sent();
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('Muse hook config must be a regular file and not a symbolic link');
                    if (info.size > maxConfigBytes)
                        throw new Error('Muse hook config exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(file)];
                case 2:
                    bytes = _a.sent();
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Muse hook config exceeds the local size limit');
                    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
                    value = JSON.parse(text);
                    if (!isRecord(value))
                        throw new Error('Muse hook config root must be an object');
                    if (scope === 'user' && value.schema_version !== 1)
                        throw new Error('Muse settings must declare schema_version: 1; no changes were made');
                    return [2 /*return*/, { value: value, snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } } }];
                case 3:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'ENOENT'))
                        return [2 /*return*/, { value: scope === 'user' ? { schema_version: 1 } : {}, snapshot: {} }];
                    if (error_2 instanceof Error && error_2.message.startsWith('Muse '))
                        throw error_2;
                    throw new Error('Muse hook config is invalid UTF-8 strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readHookMap(config) {
    if (config.hooks === undefined || config.hooks === null)
        return {};
    if (!isRecord(config.hooks))
        throw new Error('Muse hooks must be an object; no changes were made');
    return config.hooks;
}
function readEventGroups(hooks, event) {
    var groups = hooks[event];
    if (groups === undefined || groups === null)
        return [];
    if (!Array.isArray(groups) || groups.some(function (group) { return !isRecord(group) || !Array.isArray(group.hooks); })) {
        throw new Error("Muse hooks.".concat(event, " must contain matcher groups with hooks arrays; no changes were made"));
    }
    return groups;
}
function removeManagedHooks(hooks) {
    var value = __assign({}, hooks);
    var found = false;
    var _loop_1 = function (event_3, rawGroups) {
        if (rawGroups === null)
            return "continue";
        if (!Array.isArray(rawGroups))
            throw new Error("Muse hooks.".concat(event_3, " must be an array; no changes were made"));
        var retainedGroups = [];
        var _loop_2 = function (group) {
            if (!isRecord(group) || !Array.isArray(group.hooks))
                throw new Error("Muse hooks.".concat(event_3, " has a malformed matcher group; no changes were made"));
            var retainedRefs = group.hooks.filter(function (hook) {
                if (!isRecord(hook))
                    throw new Error("Muse hooks.".concat(event_3, " has a malformed handler; no changes were made"));
                var markerInCommand = typeof hook.command === 'string' && hook.command.includes(muse_hook_ts_1.museManagedMarker);
                if (markerInCommand)
                    validateManagedHandler(event_3, group, hook);
                found || (found = markerInCommand);
                return !markerInCommand;
            });
            if (retainedRefs.length > 0)
                retainedGroups.push(retainedRefs.length === group.hooks.length ? group : __assign(__assign({}, group), { hooks: retainedRefs }));
            else if (Object.keys(group).some(function (key) { return key !== 'hooks' && key !== 'matcher'; }))
                retainedGroups.push(__assign(__assign({}, group), { hooks: [] }));
        };
        for (var _c = 0, rawGroups_1 = rawGroups; _c < rawGroups_1.length; _c++) {
            var group = rawGroups_1[_c];
            _loop_2(group);
        }
        if (retainedGroups.length === 0)
            delete value[event_3];
        else
            value[event_3] = retainedGroups;
    };
    for (var _i = 0, _a = Object.entries(hooks); _i < _a.length; _i++) {
        var _b = _a[_i], event_3 = _b[0], rawGroups = _b[1];
        _loop_1(event_3, rawGroups);
    }
    return { value: value, found: found };
}
function validateManagedHandler(event, group, hook) {
    var definition = definitions.find(function (item) { return item.event === event; });
    var command = typeof hook.command === 'string' ? hook.command : '';
    if (!definition || group.matcher !== '*' || Object.keys(group).some(function (key) { return key !== 'matcher' && key !== 'hooks'; }) ||
        hook.type !== 'command' || hook.timeout !== definition.timeout || !command.endsWith("--hook-event=".concat(event)) ||
        Object.keys(hook).some(function (key) { return key !== 'type' && key !== 'command' && key !== 'timeout'; })) {
        throw new Error('Threadforge Muse hook was modified or is unsupported; inspect it before editing');
    }
}
function atomicWrite(file, snapshot, config, scope) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, tempFile, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from("".concat(JSON.stringify(config, null, 2), "\n"), 'utf8');
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Muse hook config would exceed the local size limit');
                    tempFile = "".concat(file, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    mode = snapshot.identity ? snapshot.identity.mode & 511 : scope === 'user' ? 384 : 420;
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 10, , 13]);
                    return [4 /*yield*/, assertUnchanged(file, snapshot)];
                case 2:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.open)(tempFile, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
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
                    return [4 /*yield*/, assertUnchanged(file, snapshot)];
                case 8:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.rename)(tempFile, file)];
                case 9:
                    _b.sent();
                    return [3 /*break*/, 13];
                case 10:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(tempFile).catch(function () { })];
                case 12:
                    _b.sent();
                    throw new Error('unable to atomically update Muse hook config');
                case 13: return [2 /*return*/];
            }
        });
    });
}
function assertUnchanged(file, snapshot) {
    return __awaiter(this, void 0, void 0, function () {
        var info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(file).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw error;
                    })];
                case 1:
                    info = _a.sent();
                    if (!snapshot.identity) {
                        if (info)
                            throw new Error('Muse hook config appeared during update');
                        return [2 /*return*/];
                    }
                    if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
                        info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Muse hook config changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command) || /--hook-event=/.test(command)) {
        throw new Error('Muse hook command is invalid, too long, or already contains a managed event selector');
    }
    if (!command.includes(muse_hook_ts_1.museManagedMarker))
        throw new Error("Muse hook command must include ".concat(muse_hook_ts_1.museManagedMarker));
    if (looksSecretLike(command))
        throw new Error('Muse hook command must not contain credential-like values');
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value))
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    return value;
}
function isWithin(root, candidate) {
    var relative = node_path_1.default.relative(root, candidate);
    return relative === '' || relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)) && !node_path_1.default.isAbsolute(relative);
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
