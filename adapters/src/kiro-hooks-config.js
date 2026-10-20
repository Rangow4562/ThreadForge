"use strict";
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
exports.installKiroHooks = installKiroHooks;
exports.updateKiroHooks = updateKiroHooks;
exports.removeKiroHooks = removeKiroHooks;
exports.kiroHooksConfigPath = kiroHooksConfigPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var kiro_hook_ts_1 = require("./kiro-hook.ts");
var configFileName = 'threadforge-observational.json';
var lockName = '.threadforge-kiro-hooks.lock';
var maxConfigBytes = 1 << 20;
var definitions = [
    { event: 'SessionStart', timeout: 10 },
    { event: 'AgentSpawn', timeout: 10 },
    { event: 'UserPromptSubmit', timeout: 30 },
    { event: 'PreToolUse', timeout: 10 },
    { event: 'PostToolUse', timeout: 10 },
    { event: 'Stop', timeout: 45 },
];
var description = 'Threadforge local observational telemetry; no prompt or policy output.';
/** Installs Kiro v1 command hooks in a dedicated file without changing sibling hook files. */
function installKiroHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var scope, file;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    validateCommand(options.command);
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, true)];
                case 1:
                    file = _b.sent();
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, prior, owned, nextHooks, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readHooksFile(file)];
                                    case 1:
                                        read = _a.sent();
                                        prior = read.value;
                                        owned = splitOwnedHooks(prior.hooks);
                                        nextHooks = __spreadArray(__spreadArray([], owned.unowned, true), createManagedHooks(options.command), true);
                                        assertNoNameCollisions(owned.unowned, nextHooks);
                                        next = { version: 'v1', hooks: nextHooks };
                                        if (JSON.stringify(canonicalize(prior)) === JSON.stringify(canonicalize(next)))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, next)];
                                    case 2:
                                        _a.sent();
                                        return [2 /*return*/, owned.managed.length > 0 ? 'updated' : 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Updates only after a Threadforge-owned hook set has been installed. */
function updateKiroHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var scope, file;
        var _this = this;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    validateCommand(options.command);
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, false)];
                case 1:
                    file = _b.sent();
                    if (!file)
                        throw new Error('no Kiro hook configuration exists to update');
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, owned, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readHooksFile(file)];
                                    case 1:
                                        read = _a.sent();
                                        owned = splitOwnedHooks(read.value.hooks);
                                        if (owned.managed.length === 0)
                                            throw new Error('no Threadforge-owned Kiro hooks exist to update');
                                        next = { version: 'v1', hooks: __spreadArray(__spreadArray([], owned.unowned, true), createManagedHooks(options.command), true) };
                                        assertNoNameCollisions(owned.unowned, next.hooks);
                                        if (JSON.stringify(canonicalize(read.value)) === JSON.stringify(canonicalize(next)))
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, next)];
                                    case 2:
                                        _a.sent();
                                        return [2 /*return*/, 'updated'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Removes Threadforge hook objects only; unrelated hook objects and files remain intact. */
function removeKiroHooks() {
    return __awaiter(this, arguments, void 0, function (options) {
        var scope, file;
        var _this = this;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    scope = (_a = options.scope) !== null && _a !== void 0 ? _a : 'user';
                    return [4 /*yield*/, configPath(scope, options.projectRoot, options.kiroHome, options.homeDir, false)];
                case 1:
                    file = _b.sent();
                    if (!file)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, owned;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readHooksFile(file)];
                                    case 1:
                                        read = _a.sent();
                                        owned = splitOwnedHooks(read.value.hooks);
                                        if (owned.managed.length === 0)
                                            return [2 /*return*/, 'absent'];
                                        return [4 /*yield*/, atomicWrite(file, read.snapshot, { version: 'v1', hooks: owned.unowned })];
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
function kiroHooksConfigPath(scope, root) {
    return scope === 'project'
        ? node_path_1.default.join(root, '.kiro', 'hooks', configFileName)
        : node_path_1.default.join(root, 'hooks', configFileName);
}
function configPath(scope, projectRoot, kiroHome, homeDir, create) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInput, rootInfo, root, directories, current, _loop_1, _i, directories_1, directory, state_1, file, fileInfo;
        var _this = this;
        var _a, _b;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    if (scope !== 'user' && scope !== 'project')
                        throw new Error('Kiro scope must be user or project');
                    if (scope === 'project') {
                        if (!projectRoot || !node_path_1.default.isAbsolute(projectRoot))
                            throw new Error('project scope requires an absolute project root');
                        rootInput = projectRoot;
                    }
                    else {
                        rootInput = (_b = (_a = kiroHome !== null && kiroHome !== void 0 ? kiroHome : process.env.KIRO_HOME) !== null && _a !== void 0 ? _a : homeDir) !== null && _b !== void 0 ? _b : node_os_1.default.homedir();
                        if (!node_path_1.default.isAbsolute(rootInput))
                            throw new Error('Kiro home must be an absolute directory');
                    }
                    return [4 /*yield*/, (0, promises_1.lstat)(rootInput).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                throw new Error('Kiro scope root must exist and be accessible');
                            throw error;
                        })];
                case 1:
                    rootInfo = _c.sent();
                    if (!rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('Kiro scope root must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(rootInput).catch(function () { throw new Error('Kiro scope root must exist and be accessible'); })];
                case 2:
                    root = _c.sent();
                    directories = scope === 'project' ? [node_path_1.default.join(root, '.kiro'), node_path_1.default.join(root, '.kiro', 'hooks')] : [node_path_1.default.join(root, 'hooks')];
                    _loop_1 = function (directory) {
                        var info, resolved;
                        return __generator(this, function (_d) {
                            switch (_d.label) {
                                case 0: return [4 /*yield*/, (0, promises_1.lstat)(directory).catch(function (error) { return __awaiter(_this, void 0, void 0, function () {
                                        return __generator(this, function (_a) {
                                            switch (_a.label) {
                                                case 0:
                                                    if (!isCode(error, 'ENOENT'))
                                                        throw error;
                                                    if (!create)
                                                        return [2 /*return*/, undefined];
                                                    return [4 /*yield*/, (0, promises_1.mkdir)(directory, { mode: 493 }).catch(function (mkdirError) {
                                                            if (!isCode(mkdirError, 'EEXIST'))
                                                                throw new Error('unable to create Kiro hooks directory');
                                                        })];
                                                case 1:
                                                    _a.sent();
                                                    return [2 /*return*/, (0, promises_1.lstat)(directory)];
                                            }
                                        });
                                    }); })];
                                case 1:
                                    info = _d.sent();
                                    if (!info)
                                        return [2 /*return*/, { value: undefined }];
                                    if (!info.isDirectory() || info.isSymbolicLink())
                                        throw new Error('Kiro hooks path contains a symbolic link or non-directory');
                                    return [4 /*yield*/, (0, promises_1.realpath)(directory).catch(function () { throw new Error('Kiro hooks directory is inaccessible'); })];
                                case 2:
                                    resolved = _d.sent();
                                    if (!isWithin(root, resolved))
                                        throw new Error('Kiro hooks directory resolves outside its selected root');
                                    current = resolved;
                                    return [2 /*return*/];
                            }
                        });
                    };
                    _i = 0, directories_1 = directories;
                    _c.label = 3;
                case 3:
                    if (!(_i < directories_1.length)) return [3 /*break*/, 6];
                    directory = directories_1[_i];
                    return [5 /*yield**/, _loop_1(directory)];
                case 4:
                    state_1 = _c.sent();
                    if (typeof state_1 === "object")
                        return [2 /*return*/, state_1.value];
                    _c.label = 5;
                case 5:
                    _i++;
                    return [3 /*break*/, 3];
                case 6:
                    if (!current)
                        return [2 /*return*/, undefined];
                    file = node_path_1.default.join(current, configFileName);
                    return [4 /*yield*/, (0, promises_1.lstat)(file).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return undefined;
                            throw error;
                        })];
                case 7:
                    fileInfo = _c.sent();
                    if (fileInfo && (!fileInfo.isFile() || fileInfo.isSymbolicLink()))
                        throw new Error('Kiro Threadforge hook file must be regular and not a symbolic link');
                    if (fileInfo && fileInfo.size > maxConfigBytes)
                        throw new Error('Kiro hook file exceeds the local size limit');
                    return [2 /*return*/, fileInfo || create ? file : undefined];
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
                        throw new Error('Kiro hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Kiro hooks');
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
function readHooksFile(file) {
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
                        throw new Error('Kiro Threadforge hook file must be regular and not a symbolic link');
                    if (info.size > maxConfigBytes)
                        throw new Error('Kiro hook file exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(file)];
                case 2:
                    bytes = _a.sent();
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Kiro hook file exceeds the local size limit');
                    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes).replace(/^\uFEFF/, '');
                    value = JSON.parse(text);
                    if (!isRecord(value) || value.version !== 'v1' || !Array.isArray(value.hooks) || Object.keys(value).some(function (key) { return key !== 'version' && key !== 'hooks'; })) {
                        throw new Error('Kiro hook file must use the documented v1 {version,hooks} structure; no changes were made');
                    }
                    if (value.hooks.some(function (hook) { return !isRecord(hook); }))
                        throw new Error('Kiro hook file contains a malformed hook object; no changes were made');
                    return [2 /*return*/, {
                            value: value,
                            snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } },
                        }];
                case 3:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'ENOENT'))
                        return [2 /*return*/, { value: { version: 'v1', hooks: [] }, snapshot: {} }];
                    if (error_2 instanceof Error && error_2.message.startsWith('Kiro '))
                        throw error_2;
                    throw new Error('Kiro hook file is invalid UTF-8 strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function splitOwnedHooks(hooks) {
    var managed = [];
    var unowned = [];
    var _loop_2 = function (hook) {
        var action = isRecord(hook.action) ? hook.action : undefined;
        var command = typeof (action === null || action === void 0 ? void 0 : action.command) === 'string' ? action.command : '';
        var markedByName = typeof hook.name === 'string' && hook.name.startsWith('threadforge-kiro-');
        var markedByCommand = command.includes(kiro_hook_ts_1.kiroManagedMarker);
        if (markedByName !== markedByCommand)
            throw new Error('Kiro hook has an incomplete Threadforge ownership marker; inspect it before editing');
        if (!markedByName) {
            unowned.push(hook);
            return "continue";
        }
        var definition = definitions.find(function (item) { return hook.trigger === item.event; });
        var allowed = ['name', 'description', 'trigger', 'action', 'timeout', 'enabled'];
        if (!definition || hook.name !== hookName(definition.event) || hook.description !== description ||
            (action === null || action === void 0 ? void 0 : action.type) !== 'command' || hook.timeout !== definition.timeout || hook.enabled !== true ||
            Object.keys(hook).some(function (key) { return !allowed.includes(key); }) || Object.keys(action !== null && action !== void 0 ? action : {}).some(function (key) { return key !== 'type' && key !== 'command'; })) {
            throw new Error('Threadforge Kiro hook was modified or is unsupported; inspect it before editing');
        }
        managed.push(hook);
    };
    for (var _i = 0, hooks_1 = hooks; _i < hooks_1.length; _i++) {
        var hook = hooks_1[_i];
        _loop_2(hook);
    }
    return { managed: managed, unowned: unowned };
}
function createManagedHooks(command) {
    return definitions.map(function (_a) {
        var event = _a.event, timeout = _a.timeout;
        return ({
            name: hookName(event),
            description: description,
            trigger: event,
            action: { type: 'command', command: "".concat(command, " --hook-event=").concat(event) },
            timeout: timeout,
            enabled: true,
        });
    });
}
function hookName(event) {
    return "threadforge-kiro-".concat(event.replace(/[A-Z]/g, function (character, index) { return "".concat(index ? '-' : '').concat(character.toLowerCase()); }));
}
function assertNoNameCollisions(unowned, next) {
    var managedNames = new Set(next.filter(function (hook) { return typeof hook.name === 'string' && hook.name.startsWith('threadforge-kiro-'); }).map(function (hook) { return hook.name; }));
    if (unowned.some(function (hook) { return typeof hook.name === 'string' && managedNames.has(hook.name); })) {
        throw new Error('an unrelated Kiro hook uses a Threadforge hook name; no changes were made');
    }
}
function atomicWrite(file, snapshot, value) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, tempPath, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from("".concat(JSON.stringify(value, null, 2), "\n"), 'utf8');
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Kiro hook file would exceed the local size limit');
                    tempPath = "".concat(file, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    mode = snapshot.identity ? snapshot.identity.mode & 511 : 420;
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 10, , 13]);
                    return [4 /*yield*/, assertUnchanged(file, snapshot)];
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
                    return [4 /*yield*/, assertUnchanged(file, snapshot)];
                case 8:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.rename)(tempPath, file)];
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
                    throw new Error('unable to atomically update Kiro hook file');
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
                            throw new Error('Kiro hook file appeared during update');
                        return [2 /*return*/];
                    }
                    if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
                        info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Kiro hook file changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command) || /--hook-event=/.test(command)) {
        throw new Error('Kiro hook command is invalid, too long, or already contains a managed event selector');
    }
    if (!command.includes(kiro_hook_ts_1.kiroManagedMarker))
        throw new Error("Kiro hook command must include ".concat(kiro_hook_ts_1.kiroManagedMarker));
    if (looksSecretLike(command))
        throw new Error('Kiro hook command must not contain credential-like values');
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
