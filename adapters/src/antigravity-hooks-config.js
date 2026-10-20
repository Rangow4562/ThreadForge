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
exports.installAntigravityHooks = installAntigravityHooks;
exports.removeAntigravityHooks = removeAntigravityHooks;
exports.antigravityHooksConfigPath = antigravityHooksConfigPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var antigravity_hook_ts_1 = require("./antigravity-hook.ts");
var configKey = 'threadforge-endpoint';
var lockName = '.threadforge-antigravity-hooks.lock';
var maxConfigBytes = 8 << 20;
var eventTimeouts = {
    PreInvocation: 30,
    UserPromptSubmit: 30,
    PreToolUse: 10,
    PostToolUse: 10,
    PostInvocation: 30,
    Stop: 30,
};
/** Installs observational hooks into Antigravity's user or project hook file. */
function installAntigravityHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var scope, filePath;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    validateCommand(options.command);
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, configPath(scope, options.projectRoot, options.homeDir, true)];
                case 1:
                    filePath = _b.sent();
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, snapshot, config, oldBlock, block, oldHooks, cleaned, nextHooks, _i, _a, _b, event_1, timeout, current, command, nextBlock, nextConfig;
                            var _c;
                            return __generator(this, function (_d) {
                                switch (_d.label) {
                                    case 0: return [4 /*yield*/, readConfig(filePath)];
                                    case 1:
                                        read = _d.sent();
                                        snapshot = read.snapshot;
                                        config = read.value;
                                        oldBlock = config[configKey];
                                        if (oldBlock !== undefined && !isRecord(oldBlock))
                                            throw new Error('Antigravity Threadforge hook block must be an object; no changes were made');
                                        block = (oldBlock !== null && oldBlock !== void 0 ? oldBlock : {});
                                        oldHooks = readHookMap(block);
                                        cleaned = removeManagedHooks(oldHooks);
                                        if (Object.keys(block).some(function (key) { return key !== 'hooks'; }) && oldBlock !== undefined && !cleaned.found) {
                                            throw new Error('Antigravity Threadforge hook block already exists without Threadforge ownership; no changes were made');
                                        }
                                        if (oldBlock !== undefined && !cleaned.found && Object.keys(oldHooks).length > 0) {
                                            throw new Error('Antigravity Threadforge hook block already exists without Threadforge ownership; no changes were made');
                                        }
                                        nextHooks = __assign({}, cleaned.value);
                                        for (_i = 0, _a = Object.entries(eventTimeouts); _i < _a.length; _i++) {
                                            _b = _a[_i], event_1 = _b[0], timeout = _b[1];
                                            current = readEventGroups(nextHooks, event_1);
                                            command = "".concat(options.command, " --hook-event=").concat(event_1);
                                            nextHooks[event_1] = __spreadArray(__spreadArray([], current, true), [{ hooks: [{ type: 'command', command: command, timeout: timeout }] }], false);
                                        }
                                        nextBlock = __assign(__assign({}, block), { hooks: nextHooks });
                                        nextConfig = __assign(__assign({}, config), (_c = {}, _c[configKey] = nextBlock, _c));
                                        if (deepEqual(config, nextConfig))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(filePath, snapshot, nextConfig)];
                                    case 2:
                                        _d.sent();
                                        return [2 /*return*/, cleaned.found ? 'updated' : 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Removes only marked Threadforge command refs and retains the host config file. */
function removeAntigravityHooks() {
    return __awaiter(this, arguments, void 0, function (options) {
        var scope, filePath;
        var _this = this;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, configPath(scope, options.projectRoot, options.homeDir, false)];
                case 1:
                    filePath = _b.sent();
                    if (!filePath)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, snapshot, config, block, hooks, cleaned, nextBlock, nextConfig;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(filePath)];
                                    case 1:
                                        read = _a.sent();
                                        snapshot = read.snapshot;
                                        config = read.value;
                                        block = config[configKey];
                                        if (block === undefined)
                                            return [2 /*return*/, 'absent'];
                                        if (!isRecord(block))
                                            throw new Error('Antigravity Threadforge hook block is malformed; no changes were made');
                                        hooks = readHookMap(block);
                                        cleaned = removeManagedHooks(hooks);
                                        if (!cleaned.found) {
                                            if (Object.keys(hooks).length > 0)
                                                throw new Error('Antigravity Threadforge hook block has no intact ownership marker; inspect it before removal');
                                            return [2 /*return*/, 'absent'];
                                        }
                                        nextBlock = __assign({}, block);
                                        if (Object.keys(cleaned.value).length === 0)
                                            delete nextBlock.hooks;
                                        else
                                            nextBlock.hooks = cleaned.value;
                                        nextConfig = __assign({}, config);
                                        if (Object.keys(nextBlock).length === 0)
                                            delete nextConfig[configKey];
                                        else
                                            nextConfig[configKey] = nextBlock;
                                        return [4 /*yield*/, atomicWrite(filePath, snapshot, nextConfig)];
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
function antigravityHooksConfigPath(scope, root) {
    return scope === 'project'
        ? node_path_1.default.join(root, '.agents', 'hooks.json')
        : node_path_1.default.join(root, '.gemini', 'config', 'hooks.json');
}
function configPath(scope, projectRoot, homeDir, create) {
    return __awaiter(this, void 0, void 0, function () {
        var root, rootStat, agents, home, canonicalHome, homeStat, gemini, config;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (scope !== 'user' && scope !== 'project')
                        throw new Error('Antigravity scope must be user or project');
                    if (!(scope === 'project')) return [3 /*break*/, 5];
                    if (!projectRoot || !node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('project scope requires an absolute project root');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    root = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(root)];
                case 2:
                    rootStat = _a.sent();
                    if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
                        throw new Error('project root must be a real directory');
                    return [4 /*yield*/, ensureDirectory(root, node_path_1.default.join(root, '.agents'), create)];
                case 3:
                    agents = _a.sent();
                    if (!agents)
                        return [2 /*return*/, undefined];
                    return [4 /*yield*/, configFile(agents, 'hooks.json', create)];
                case 4: return [2 /*return*/, _a.sent()];
                case 5:
                    home = homeDir !== null && homeDir !== void 0 ? homeDir : node_os_1.default.homedir();
                    if (!node_path_1.default.isAbsolute(home))
                        throw new Error('Antigravity home directory must be absolute');
                    return [4 /*yield*/, (0, promises_1.realpath)(home).catch(function () { throw new Error('Antigravity home directory must exist and be accessible'); })];
                case 6:
                    canonicalHome = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(canonicalHome)];
                case 7:
                    homeStat = _a.sent();
                    if (!homeStat.isDirectory() || homeStat.isSymbolicLink())
                        throw new Error('Antigravity home must be a real directory');
                    return [4 /*yield*/, ensureDirectory(canonicalHome, node_path_1.default.join(canonicalHome, '.gemini'), create)];
                case 8:
                    gemini = _a.sent();
                    if (!gemini)
                        return [2 /*return*/, undefined];
                    return [4 /*yield*/, ensureDirectory(canonicalHome, node_path_1.default.join(gemini, 'config'), create)];
                case 9:
                    config = _a.sent();
                    if (!config)
                        return [2 /*return*/, undefined];
                    return [4 /*yield*/, configFile(config, 'hooks.json', create)];
                case 10: return [2 /*return*/, _a.sent()];
            }
        });
    });
}
function ensureDirectory(root, target, create) {
    return __awaiter(this, void 0, void 0, function () {
        var error_1, stat, actual;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!create) return [3 /*break*/, 4];
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(target, { mode: 448 })];
                case 2:
                    _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create Antigravity hook configuration directory');
                    return [3 /*break*/, 4];
                case 4: return [4 /*yield*/, (0, promises_1.lstat)(target).catch(function (error) {
                        if (!create && isCode(error, 'ENOENT'))
                            return null;
                        throw error;
                    })];
                case 5:
                    stat = _a.sent();
                    if (!stat)
                        return [2 /*return*/, undefined];
                    if (!stat.isDirectory() || stat.isSymbolicLink())
                        throw new Error('Antigravity hook path contains a symbolic link or non-directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(target).catch(function () { throw new Error('Antigravity hook directory is inaccessible'); })];
                case 6:
                    actual = _a.sent();
                    if (!isWithin(root, actual))
                        throw new Error('Antigravity hook directory resolves outside its selected root');
                    return [2 /*return*/, actual];
            }
        });
    });
}
function configFile(directory, name, create) {
    return __awaiter(this, void 0, void 0, function () {
        var filePath, stat;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    filePath = node_path_1.default.join(directory, name);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw error;
                        })];
                case 1:
                    stat = _a.sent();
                    if (!stat)
                        return [2 /*return*/, create ? filePath : undefined];
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Antigravity hooks config must be a regular file');
                    if (stat.size > maxConfigBytes)
                        throw new Error('Antigravity hooks config exceeds the local size limit');
                    return [2 /*return*/, filePath];
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
                        throw new Error('Antigravity hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Antigravity hooks configuration');
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
        var stat, bytes, content, parsed, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _a.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Antigravity hooks config must be a regular file');
                    if (stat.size > maxConfigBytes)
                        throw new Error('Antigravity hooks config exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath)];
                case 2:
                    bytes = _a.sent();
                    content = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
                    parsed = JSON.parse(content);
                    if (!isRecord(parsed))
                        throw new Error('Antigravity hooks config root must be an object');
                    return [2 /*return*/, { value: parsed, snapshot: { content: content, identity: identityOf(stat) } }];
                case 3:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, { value: {}, snapshot: { content: '' } }];
                    if (error_3 instanceof Error && error_3.message.startsWith('Antigravity hooks config'))
                        throw error_3;
                    throw new Error('Antigravity hooks config is invalid UTF-8 strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readHookMap(block) {
    if (block.hooks === undefined || block.hooks === null)
        return {};
    if (!isRecord(block.hooks))
        throw new Error('Antigravity Threadforge hooks must be an object; no changes were made');
    return block.hooks;
}
function readEventGroups(hooks, event) {
    var current = hooks[event];
    if (current === undefined || current === null)
        return [];
    if (!Array.isArray(current) || current.some(function (item) { return !isRecord(item) || !Array.isArray(item.hooks); })) {
        throw new Error("Antigravity hooks.".concat(event, " must contain hook groups; no changes were made"));
    }
    return current;
}
function removeManagedHooks(hooks) {
    var value = __assign({}, hooks);
    var found = false;
    var _loop_1 = function (event_2, rawGroups) {
        if (rawGroups === null)
            return "continue";
        if (!Array.isArray(rawGroups))
            throw new Error("Antigravity hooks.".concat(event_2, " must be an array; no changes were made"));
        var retainedGroups = [];
        for (var _c = 0, rawGroups_1 = rawGroups; _c < rawGroups_1.length; _c++) {
            var rawGroup = rawGroups_1[_c];
            if (!isRecord(rawGroup) || !Array.isArray(rawGroup.hooks)) {
                throw new Error("Antigravity hooks.".concat(event_2, " contains an unsupported hook group; no changes were made"));
            }
            var retainedRefs = rawGroup.hooks.filter(function (rawRef) {
                if (!isRecord(rawRef))
                    throw new Error("Antigravity hooks.".concat(event_2, " contains an unsupported hook ref; no changes were made"));
                var managed = rawRef.type === 'command' && typeof rawRef.command === 'string' && rawRef.command.includes(antigravity_hook_ts_1.antigravityManagedMarker);
                found || (found = managed);
                return !managed;
            });
            if (retainedRefs.length > 0)
                retainedGroups.push(retainedRefs.length === rawGroup.hooks.length ? rawGroup : __assign(__assign({}, rawGroup), { hooks: retainedRefs }));
            else if (Object.keys(rawGroup).some(function (key) { return key !== 'hooks'; }))
                retainedGroups.push(__assign(__assign({}, rawGroup), { hooks: [] }));
        }
        if (retainedGroups.length === 0)
            delete value[event_2];
        else
            value[event_2] = retainedGroups;
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
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Antigravity hooks config would exceed the local size limit');
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
                    throw new Error('unable to atomically update Antigravity hooks config');
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
                            throw new Error('Antigravity hooks config appeared during update');
                        return [2 /*return*/];
                    }
                    if (!current || !current.isFile() || current.isSymbolicLink() || current.dev !== snapshot.identity.dev ||
                        current.ino !== snapshot.identity.ino || current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Antigravity hooks config changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
        throw new Error('Antigravity hook command is invalid or too long');
    }
    if (!command.includes(antigravity_hook_ts_1.antigravityManagedMarker))
        throw new Error("Antigravity hook command must include ".concat(antigravity_hook_ts_1.antigravityManagedMarker));
    if (looksSecretLike(command))
        throw new Error('Antigravity hook command must not contain credential-like values');
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
function identityOf(stat) {
    return { dev: stat.dev, ino: stat.ino, mode: stat.mode, size: stat.size, mtimeMs: stat.mtimeMs };
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return isRecord(error) && error.code === code;
}
