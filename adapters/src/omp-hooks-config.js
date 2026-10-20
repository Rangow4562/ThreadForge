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
Object.defineProperty(exports, "__esModule", { value: true });
exports.installOmpExtension = installOmpExtension;
exports.updateOmpExtension = updateOmpExtension;
exports.removeOmpExtension = removeOmpExtension;
exports.ompExtensionPath = ompExtensionPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var omp_extension_ts_1 = require("./omp-extension.ts");
var extensionFileName = 'threadforge.ts';
var lockName = '.threadforge-omp-extension.lock';
var maxExtensionBytes = 512 << 10;
var ownedHeader = "".concat(omp_extension_ts_1.ompManagedMarker, " sha256=");
/** Installs the self-contained observer in an Oh My Pi extension directory. */
function installOmpExtension(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, resolveExtensionPath(options, true)];
                case 1:
                    file = _a.sent();
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var current, desired;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readExtension(file)];
                                    case 1:
                                        current = _a.sent();
                                        if (current.snapshot.identity)
                                            verifyOwnedContent(current.content);
                                        return [4 /*yield*/, managedExtensionSource()];
                                    case 2:
                                        desired = _a.sent();
                                        if (current.content === desired)
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, current.snapshot, desired, options.scope)];
                                    case 3:
                                        _a.sent();
                                        return [2 /*return*/, current.snapshot.identity ? 'updated' : 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function updateOmpExtension(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, resolveExtensionPath(options, false)];
                case 1:
                    file = _a.sent();
                    if (!file)
                        throw new Error('no Threadforge Oh My Pi extension exists to update');
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var current, desired;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readExtension(file)];
                                    case 1:
                                        current = _a.sent();
                                        if (!current.snapshot.identity)
                                            throw new Error('no Threadforge Oh My Pi extension exists to update');
                                        verifyOwnedContent(current.content);
                                        return [4 /*yield*/, managedExtensionSource()];
                                    case 2:
                                        desired = _a.sent();
                                        if (current.content === desired)
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(file, current.snapshot, desired, options.scope)];
                                    case 3:
                                        _a.sent();
                                        return [2 /*return*/, 'updated'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function removeOmpExtension(options) {
    return __awaiter(this, void 0, void 0, function () {
        var file;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, resolveExtensionPath(options, false)];
                case 1:
                    file = _a.sent();
                    if (!file)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(file, function () { return __awaiter(_this, void 0, void 0, function () {
                            var current;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readExtension(file)];
                                    case 1:
                                        current = _a.sent();
                                        if (!current.snapshot.identity)
                                            return [2 /*return*/, 'absent'];
                                        verifyOwnedContent(current.content);
                                        return [4 /*yield*/, assertUnchanged(file, current.snapshot)];
                                    case 2:
                                        _a.sent();
                                        return [4 /*yield*/, (0, promises_1.unlink)(file)];
                                    case 3:
                                        _a.sent();
                                        return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** `root` is an OMP agent directory for user scope, or the repository root for project scope. */
function ompExtensionPath(scope, root) {
    return scope === 'project'
        ? node_path_1.default.join(root, '.omp', 'extensions', extensionFileName)
        : node_path_1.default.join(root, 'extensions', extensionFileName);
}
function resolveExtensionPath(options, create) {
    return __awaiter(this, void 0, void 0, function () {
        var root, directoryParts, home, profileValue, profile, configName, agentOverride;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    if (options.scope !== 'user' && options.scope !== 'project')
                        throw new Error('Oh My Pi scope must be user or project');
                    directoryParts = ['extensions'];
                    if (!(options.scope === 'project')) return [3 /*break*/, 2];
                    if (!options.projectRoot || !node_path_1.default.isAbsolute(options.projectRoot))
                        throw new Error('project scope requires an absolute project root');
                    return [4 /*yield*/, existingDirectory(options.projectRoot, 'Oh My Pi project root')];
                case 1:
                    root = _b.sent();
                    directoryParts = ['.omp', 'extensions'];
                    return [3 /*break*/, 10];
                case 2:
                    if (!options.ompAgentDir) return [3 /*break*/, 4];
                    if (!node_path_1.default.isAbsolute(options.ompAgentDir))
                        throw new Error('--omp-agent-dir must be absolute');
                    return [4 /*yield*/, existingDirectory(options.ompAgentDir, 'Oh My Pi agent directory')];
                case 3:
                    root = _b.sent();
                    return [3 /*break*/, 10];
                case 4: return [4 /*yield*/, existingDirectory((_a = options.homeDir) !== null && _a !== void 0 ? _a : node_os_1.default.homedir(), 'home directory')];
                case 5:
                    home = _b.sent();
                    profileValue = Object.hasOwn(process.env, 'OMP_PROFILE') ? process.env.OMP_PROFILE : process.env.PI_PROFILE;
                    profile = normalizeProfile(profileValue);
                    configName = normalizeConfigDirectory(process.env.PI_CONFIG_DIR);
                    agentOverride = process.env.PI_CODING_AGENT_DIR;
                    if (!profile) return [3 /*break*/, 6];
                    directoryParts = [configName, 'profiles', profile, 'agent', 'extensions'];
                    return [3 /*break*/, 9];
                case 6:
                    if (!agentOverride) return [3 /*break*/, 8];
                    if (!node_path_1.default.isAbsolute(agentOverride))
                        throw new Error('PI_CODING_AGENT_DIR must be an absolute directory');
                    return [4 /*yield*/, existingDirectory(agentOverride, 'PI_CODING_AGENT_DIR')];
                case 7:
                    root = _b.sent();
                    directoryParts = ['extensions'];
                    return [2 /*return*/, extensionFile(root, directoryParts, create, options.scope)];
                case 8:
                    directoryParts = [configName, 'agent', 'extensions'];
                    _b.label = 9;
                case 9: return [2 /*return*/, extensionFile(home, directoryParts, create, options.scope)];
                case 10: return [2 /*return*/, extensionFile(root, directoryParts, create, options.scope)];
            }
        });
    });
}
function extensionFile(root, parts, create, scope) {
    return __awaiter(this, void 0, void 0, function () {
        var canonicalRoot, directory, _i, parts_1, part, info_1, resolved, file, info;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.realpath)(root)];
                case 1:
                    canonicalRoot = _a.sent();
                    directory = canonicalRoot;
                    _i = 0, parts_1 = parts;
                    _a.label = 2;
                case 2:
                    if (!(_i < parts_1.length)) return [3 /*break*/, 6];
                    part = parts_1[_i];
                    directory = node_path_1.default.join(directory, part);
                    return [4 /*yield*/, (0, promises_1.lstat)(directory).catch(function (error) { return __awaiter(_this, void 0, void 0, function () {
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0:
                                        if (!isCode(error, 'ENOENT'))
                                            throw error;
                                        if (!create)
                                            return [2 /*return*/, undefined];
                                        return [4 /*yield*/, (0, promises_1.mkdir)(directory, { mode: scope === 'project' ? 493 : 448 }).catch(function (mkdirError) {
                                                if (!isCode(mkdirError, 'EEXIST'))
                                                    throw new Error('unable to create Oh My Pi extension directory');
                                            })];
                                    case 1:
                                        _a.sent();
                                        return [2 /*return*/, (0, promises_1.lstat)(directory)];
                                }
                            });
                        }); })];
                case 3:
                    info_1 = _a.sent();
                    if (!info_1)
                        return [2 /*return*/, undefined];
                    if (!info_1.isDirectory() || info_1.isSymbolicLink())
                        throw new Error('Oh My Pi extension path contains a symbolic link or non-directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(directory).catch(function () { throw new Error('Oh My Pi extension directory is inaccessible'); })];
                case 4:
                    resolved = _a.sent();
                    if (!isWithin(canonicalRoot, resolved))
                        throw new Error('Oh My Pi extension directory resolves outside its selected root');
                    directory = resolved;
                    _a.label = 5;
                case 5:
                    _i++;
                    return [3 /*break*/, 2];
                case 6:
                    file = node_path_1.default.join(directory, extensionFileName);
                    return [4 /*yield*/, (0, promises_1.lstat)(file).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return undefined;
                            throw error;
                        })];
                case 7:
                    info = _a.sent();
                    if (info && (!info.isFile() || info.isSymbolicLink()))
                        throw new Error('Oh My Pi extension must be a regular file and not a symbolic link');
                    if (info && info.size > maxExtensionBytes)
                        throw new Error('Oh My Pi extension exceeds the local size limit');
                    return [2 /*return*/, info || create ? file : undefined];
            }
        });
    });
}
function existingDirectory(input, label) {
    return __awaiter(this, void 0, void 0, function () {
        var normalized, parsed, current, _i, _a, part, info;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(input))
                        throw new Error("".concat(label, " must be an absolute directory"));
                    normalized = node_path_1.default.resolve(input);
                    parsed = node_path_1.default.parse(normalized);
                    current = parsed.root;
                    _i = 0, _a = normalized.slice(parsed.root.length).split(node_path_1.default.sep).filter(Boolean);
                    _b.label = 1;
                case 1:
                    if (!(_i < _a.length)) return [3 /*break*/, 4];
                    part = _a[_i];
                    current = node_path_1.default.join(current, part);
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { throw new Error("".concat(label, " must exist and be accessible")); })];
                case 2:
                    info = _b.sent();
                    if (info.isSymbolicLink() || !info.isDirectory())
                        throw new Error("".concat(label, " path contains a symbolic link or non-directory"));
                    _b.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/, (0, promises_1.realpath)(normalized).catch(function () { throw new Error("".concat(label, " must exist and be accessible")); })];
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
                        throw new Error('Oh My Pi extension is locked by another writer or a stale lock');
                    throw new Error('unable to lock Oh My Pi extension directory');
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
function readExtension(file) {
    return __awaiter(this, void 0, void 0, function () {
        var info, bytes, content, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(file)];
                case 1:
                    info = _a.sent();
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('Oh My Pi extension must be a regular file and not a symbolic link');
                    if (info.size > maxExtensionBytes)
                        throw new Error('Oh My Pi extension exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(file)];
                case 2:
                    bytes = _a.sent();
                    if (bytes.byteLength > maxExtensionBytes)
                        throw new Error('Oh My Pi extension exceeds the local size limit');
                    content = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
                    return [2 /*return*/, {
                            content: content,
                            snapshot: { content: content, identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } },
                        }];
                case 3:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'ENOENT'))
                        return [2 /*return*/, { content: '', snapshot: { content: '' } }];
                    if (error_2 instanceof Error && error_2.message.startsWith('Oh My Pi '))
                        throw error_2;
                    throw new Error('Oh My Pi extension is invalid UTF-8; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function managedExtensionSource() {
    return __awaiter(this, void 0, void 0, function () {
        var source, _a, _b, _c, newline, firstLine, body, hash, result;
        return __generator(this, function (_d) {
            switch (_d.label) {
                case 0:
                    _d.trys.push([0, 2, , 3]);
                    _b = (_a = new TextDecoder('utf-8', { fatal: true })).decode;
                    return [4 /*yield*/, (0, promises_1.readFile)(new URL('./omp-extension.ts', import.meta.url))];
                case 1:
                    source = _b.apply(_a, [_d.sent()]);
                    return [3 /*break*/, 3];
                case 2:
                    _c = _d.sent();
                    throw new Error('Threadforge Oh My Pi extension source is unavailable');
                case 3:
                    newline = source.indexOf('\n');
                    firstLine = (newline < 0 ? source : source.slice(0, newline)).replace(/\r$/, '');
                    if (firstLine !== omp_extension_ts_1.ompExtensionTemplateMarker || newline < 0)
                        throw new Error('Threadforge Oh My Pi extension template marker is invalid');
                    body = source.slice(newline + 1);
                    hash = (0, node_crypto_1.createHash)('sha256').update(body, 'utf8').digest('hex');
                    result = "".concat(ownedHeader).concat(hash, "\n").concat(body);
                    if (Buffer.byteLength(result, 'utf8') > maxExtensionBytes)
                        throw new Error('Threadforge Oh My Pi extension exceeds the local size limit');
                    return [2 /*return*/, result];
            }
        });
    });
}
function verifyOwnedContent(content) {
    var newline = content.indexOf('\n');
    var header = newline < 0 ? content : content.slice(0, newline).replace(/\r$/, '');
    var expected = header.startsWith(ownedHeader) ? header.slice(ownedHeader.length) : '';
    var body = newline < 0 ? '' : content.slice(newline + 1);
    var actual = (0, node_crypto_1.createHash)('sha256').update(body, 'utf8').digest('hex');
    if (!/^[a-f0-9]{64}$/.test(expected) || actual !== expected) {
        throw new Error('Oh My Pi extension is unowned or was modified; inspect it before updating or removing');
    }
}
function atomicWrite(file, snapshot, content, scope) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, temp, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from(content, 'utf8');
                    if (bytes.byteLength > maxExtensionBytes)
                        throw new Error('Oh My Pi extension would exceed the local size limit');
                    temp = "".concat(file, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    mode = snapshot.identity ? snapshot.identity.mode & 511 : scope === 'project' ? 420 : 384;
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 10, , 13]);
                    return [4 /*yield*/, assertUnchanged(file, snapshot)];
                case 2:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.open)(temp, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
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
                    return [4 /*yield*/, (0, promises_1.rename)(temp, file)];
                case 9:
                    _b.sent();
                    return [3 /*break*/, 13];
                case 10:
                    _a = _b.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 11:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(temp).catch(function () { })];
                case 12:
                    _b.sent();
                    throw new Error('unable to atomically update Oh My Pi extension');
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
                            throw new Error('Oh My Pi extension appeared during update');
                        return [2 /*return*/];
                    }
                    if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
                        info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Oh My Pi extension changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function normalizeProfile(value) {
    var name = value === null || value === void 0 ? void 0 : value.trim();
    if (!name || name === 'default')
        return undefined;
    if (name === '.' || name === '..' || name.endsWith('.') || !/^[a-z0-9][a-z0-9._-]{0,63}$/.test(name) ||
        /^(?:CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(?:\..*)?$/i.test(name)) {
        throw new Error('OMP profile name is invalid; pass --omp-agent-dir for a non-default profile');
    }
    return name;
}
function normalizeConfigDirectory(value) {
    var name = (value === null || value === void 0 ? void 0 : value.trim()) || '.omp';
    if (name === '.' || name === '..' || node_path_1.default.isAbsolute(name) || /[\\/]/.test(name) || /[\0]/.test(name)) {
        throw new Error('PI_CONFIG_DIR must be one relative directory name');
    }
    return name;
}
function isWithin(root, candidate) {
    var relative = node_path_1.default.relative(root, candidate);
    return relative === '' || relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)) && !node_path_1.default.isAbsolute(relative);
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
