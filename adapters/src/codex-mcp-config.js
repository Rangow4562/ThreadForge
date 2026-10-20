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
exports.installCodexProjectMcpServer = installCodexProjectMcpServer;
exports.updateCodexProjectMcpServer = updateCodexProjectMcpServer;
exports.removeCodexProjectMcpServer = removeCodexProjectMcpServer;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var mcp_config_ts_1 = require("./mcp-config.ts");
var maxConfigBytes = 8 << 20;
var beginPrefix = '# >>> THREADFORGE CODEX MCP BEGIN v1';
var endMarker = '# <<< THREADFORGE CODEX MCP END v1';
var serverName = 'threadforge';
/** Adds one hash-owned project-scoped stdio server to `.codex/config.toml`. */
function installCodexProjectMcpServer(projectRoot, command, args) {
    return __awaiter(this, void 0, void 0, function () {
        var requestedBody, paths;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, buildManagedBody(projectRoot, command, args, '\n')];
                case 1:
                    requestedBody = _a.sent();
                    return [4 /*yield*/, configPaths(projectRoot, true)];
                case 2:
                    paths = _a.sent();
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var snapshot, managed, outside, eol, body, prefixAdded, block, separator;
                            var _a;
                            return __generator(this, function (_b) {
                                switch (_b.label) {
                                    case 0: return [4 /*yield*/, readConfig(paths.config)];
                                    case 1:
                                        snapshot = _b.sent();
                                        managed = findManagedBlock(snapshot.content);
                                        outside = managed ? snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end) : snapshot.content;
                                        if (hasUnmanagedThreadforgeServer(outside))
                                            throw new Error('Codex MCP server "threadforge" already exists outside the Threadforge-owned block');
                                        eol = (_a = managed === null || managed === void 0 ? void 0 : managed.eol) !== null && _a !== void 0 ? _a : detectLineEnding(snapshot.content);
                                        body = requestedBody.replaceAll('\n', eol);
                                        if (managed) {
                                            if (managed.body === body)
                                                return [2 /*return*/, 'unchanged'];
                                            throw new Error('Threadforge Codex MCP config already exists with different settings; use update');
                                        }
                                        prefixAdded = snapshot.content.length > 0 && !/(?:\r\n|\n)$/.test(snapshot.content);
                                        block = renderManagedBlock(body, eol, prefixAdded, !snapshot.existed);
                                        separator = prefixAdded ? eol : '';
                                        return [4 /*yield*/, atomicWrite(paths.config, "".concat(snapshot.content).concat(separator).concat(block).concat(eol), snapshot)];
                                    case 2:
                                        _b.sent();
                                        return [2 /*return*/, 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Updates only an unchanged Threadforge-owned block; unowned config is never overwritten. */
function updateCodexProjectMcpServer(projectRoot, command, args) {
    return __awaiter(this, void 0, void 0, function () {
        var paths, requestedBody;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, configPaths(projectRoot, false)];
                case 1:
                    paths = _a.sent();
                    if (!paths)
                        throw new Error('project Codex config is absent; install it before updating');
                    return [4 /*yield*/, buildManagedBody(projectRoot, command, args, '\n')];
                case 2:
                    requestedBody = _a.sent();
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var snapshot, managed, outside, nextBody, replacement;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(paths.config)];
                                    case 1:
                                        snapshot = _a.sent();
                                        managed = findManagedBlock(snapshot.content);
                                        if (!managed)
                                            throw new Error('Threadforge Codex MCP config is absent; refusing to create it during update');
                                        outside = snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end);
                                        if (hasUnmanagedThreadforgeServer(outside))
                                            throw new Error('an unmanaged Codex MCP server named threadforge conflicts with the managed block');
                                        nextBody = requestedBody.replaceAll('\n', managed.eol);
                                        if (managed.body === nextBody)
                                            return [2 /*return*/, 'unchanged'];
                                        replacement = renderManagedBlock(nextBody, managed.eol, managed.prefixAdded, managed.createdConfig);
                                        return [4 /*yield*/, atomicWrite(paths.config, snapshot.content.slice(0, managed.start) + replacement + snapshot.content.slice(managed.end), snapshot)];
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
/** Removes only the unchanged Threadforge-owned project MCP block. */
function removeCodexProjectMcpServer(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        var paths;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, configPaths(projectRoot, false)];
                case 1:
                    paths = _a.sent();
                    if (!paths)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var snapshot, managed, outside, start, end, suffix, separator, beforeBlock, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(paths.config)];
                                    case 1:
                                        snapshot = _a.sent();
                                        managed = findManagedBlock(snapshot.content);
                                        if (!managed)
                                            return [2 /*return*/, 'absent'];
                                        outside = snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end);
                                        if (hasUnmanagedThreadforgeServer(outside))
                                            throw new Error('an unmanaged Codex MCP server named threadforge conflicts with the managed block');
                                        start = managed.start;
                                        if (managed.prefixAdded && start >= managed.eol.length && snapshot.content.slice(start - managed.eol.length, start) === managed.eol) {
                                            start -= managed.eol.length;
                                        }
                                        end = managed.end;
                                        suffix = snapshot.content.slice(end);
                                        separator = suffix.startsWith('\r\n') ? '\r\n' : suffix.startsWith('\n') ? '\n' : '';
                                        beforeBlock = snapshot.content.slice(0, start);
                                        if (separator && (suffix.length === separator.length || (!managed.prefixAdded && beforeBlock.endsWith(separator))))
                                            end += separator.length;
                                        next = snapshot.content.slice(0, start) + snapshot.content.slice(end);
                                        if (!(managed.createdConfig && next.length === 0)) return [3 /*break*/, 3];
                                        return [4 /*yield*/, unlinkIfUnchanged(paths.config, snapshot)];
                                    case 2:
                                        _a.sent();
                                        return [2 /*return*/, 'removed'];
                                    case 3: return [4 /*yield*/, atomicWrite(paths.config, next, snapshot)];
                                    case 4:
                                        _a.sent();
                                        return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function buildManagedBody(projectRoot, command, args, eol) {
    return __awaiter(this, void 0, void 0, function () {
        var canonicalRoot, normalizedArgs;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, validateProjectRoot(projectRoot)];
                case 1:
                    canonicalRoot = _a.sent();
                    validateCommand(command);
                    return [4 /*yield*/, validateArgs(args, canonicalRoot)];
                case 2:
                    normalizedArgs = _a.sent();
                    return [2 /*return*/, buildManagedBodyText(command, normalizedArgs, eol)];
            }
        });
    });
}
function buildManagedBodyText(command, args, eol) {
    return [
        '[mcp_servers.threadforge]',
        "command = ".concat(tomlString(command)),
        "args = [".concat(args.map(tomlString).join(', '), "]"),
    ].join(eol);
}
function validateProjectRoot(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        var absolute, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('project root must be an absolute path');
                    absolute = node_path_1.default.resolve(projectRoot);
                    return [4 /*yield*/, (0, promises_1.lstat)(absolute).catch(function () { return null; })];
                case 1:
                    info = _a.sent();
                    if (!(info === null || info === void 0 ? void 0 : info.isDirectory()) || info.isSymbolicLink())
                        throw new Error('project root must be a real directory, not a symbolic link');
                    return [2 /*return*/, (0, promises_1.realpath)(absolute).catch(function () { throw new Error('project root could not be resolved'); })];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 2048 || /[\r\n\0]/.test(command) || looksSecretLike(command)) {
        throw new Error('Codex MCP command is invalid, too long, or appears to contain a credential');
    }
}
function validateArgs(args, canonicalRoot) {
    return __awaiter(this, void 0, void 0, function () {
        var rootIndexes, root, normalized, same, normalizedArgs;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!Array.isArray(args) || args.length < 5 || args.length > 128 ||
                        args.some(function (arg) { return typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg) || looksSecretLike(arg); })) {
                        throw new Error('Codex MCP arguments must be bounded, credential-free strings');
                    }
                    if (args[0] !== 'mcp' || args[1] !== 'serve' || args.filter(function (arg) { return arg === mcp_config_ts_1.managedMcpArgument; }).length !== 1) {
                        throw new Error("Codex MCP command must begin with mcp serve and include ".concat(mcp_config_ts_1.managedMcpArgument));
                    }
                    rootIndexes = args.flatMap(function (arg, index) { return arg === '--project-root' ? [index] : []; });
                    if (rootIndexes.length !== 1 || args.some(function (arg) { return arg.startsWith('--project-root='); })) {
                        throw new Error('Codex MCP command must contain exactly one --project-root <same-project-root>');
                    }
                    root = args[rootIndexes[0] + 1];
                    if (!root || root.startsWith('--') || !node_path_1.default.isAbsolute(root))
                        throw new Error('Codex MCP command requires an absolute --project-root');
                    return [4 /*yield*/, (0, promises_1.realpath)(root).catch(function () { throw new Error('Codex MCP --project-root must exist and match the selected project root'); })];
                case 1:
                    normalized = _a.sent();
                    same = process.platform === 'win32'
                        ? normalized.toLowerCase() === canonicalRoot.toLowerCase()
                        : normalized === canonicalRoot;
                    if (!same)
                        throw new Error('Codex MCP --project-root must match the selected project root');
                    normalizedArgs = __spreadArray([], args, true);
                    normalizedArgs[rootIndexes[0] + 1] = canonicalRoot;
                    return [2 /*return*/, normalizedArgs];
            }
        });
    });
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|id[_ -]?token|token|password|secret|authorization|cookie|private[_ -]?key)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value);
}
function tomlString(value) {
    return JSON.stringify(value);
}
function configPaths(projectRoot, create) {
    return __awaiter(this, void 0, void 0, function () {
        var root, codexDir, dir, error_1, canonicalDir;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, validateProjectRoot(projectRoot)];
                case 1:
                    root = _a.sent();
                    codexDir = node_path_1.default.join(root, '.codex');
                    return [4 /*yield*/, (0, promises_1.lstat)(codexDir).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect project .codex directory');
                        })];
                case 2:
                    dir = _a.sent();
                    if (!!dir) return [3 /*break*/, 8];
                    if (!create)
                        return [2 /*return*/, undefined];
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(codexDir, { mode: 448 })];
                case 4:
                    _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create project .codex directory');
                    return [3 /*break*/, 6];
                case 6: return [4 /*yield*/, (0, promises_1.lstat)(codexDir).catch(function () { return null; })];
                case 7:
                    dir = _a.sent();
                    _a.label = 8;
                case 8:
                    if (!(dir === null || dir === void 0 ? void 0 : dir.isDirectory()) || dir.isSymbolicLink())
                        throw new Error('project .codex path must be a real directory, not a symbolic link');
                    return [4 /*yield*/, (0, promises_1.realpath)(codexDir).catch(function () { throw new Error('project .codex directory could not be resolved'); })];
                case 9:
                    canonicalDir = _a.sent();
                    if (!isWithin(root, canonicalDir))
                        throw new Error('project .codex directory must remain within the project root');
                    return [2 /*return*/, {
                            config: node_path_1.default.join(canonicalDir, 'config.toml'),
                            lock: node_path_1.default.join(canonicalDir, '.threadforge-mcp-config.lock'),
                        }];
            }
        });
    });
}
function withLock(lockPath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 2, , 3]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 1:
                    lock = _a.sent();
                    return [3 /*break*/, 3];
                case 2:
                    error_2 = _a.sent();
                    if (isCode(error_2, 'EEXIST'))
                        throw new Error('project Codex MCP config is locked by another writer or a stale lock');
                    throw new Error('unable to lock project Codex MCP config');
                case 3:
                    _a.trys.push([3, , 5, 8]);
                    return [4 /*yield*/, action()];
                case 4: return [2 /*return*/, _a.sent()];
                case 5: return [4 /*yield*/, lock.close().catch(function () { })];
                case 6:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 7:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 8: return [2 /*return*/];
            }
        });
    });
}
function readConfig(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var initial, handle, opened, bytes, content, error_3;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw new Error('unable to inspect project Codex config.toml');
                    })];
                case 1:
                    initial = _b.sent();
                    if (!initial)
                        return [2 /*return*/, { content: '', existed: false }];
                    if (!initial.isFile() || initial.isSymbolicLink())
                        throw new Error('project Codex config.toml must be a regular, non-symlink file');
                    if (initial.size > maxConfigBytes)
                        throw new Error('project Codex config.toml exceeds the local size limit');
                    _b.label = 2;
                case 2:
                    _b.trys.push([2, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 3:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 4:
                    opened = _b.sent();
                    if (!sameFile(initial, opened) || initial.size !== opened.size || initial.mtimeMs !== opened.mtimeMs) {
                        throw new Error('project Codex config.toml changed before reading');
                    }
                    return [4 /*yield*/, handle.readFile()];
                case 5:
                    bytes = _b.sent();
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('project Codex config.toml exceeds the local size limit');
                    content = void 0;
                    try {
                        content = new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes);
                    }
                    catch (_c) {
                        throw new Error('project Codex config.toml is not valid UTF-8; no changes were made');
                    }
                    return [2 /*return*/, { content: content, existed: true, identity: identityOf(initial) }];
                case 6:
                    error_3 = _b.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('project Codex config.toml'))
                        throw error_3;
                    throw new Error('project Codex config.toml could not be opened safely');
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 8:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function atomicWrite(filePath, content, snapshot) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, tempPath, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from(content, 'utf8');
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('project Codex config.toml would exceed the local size limit');
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
                    throw new Error('unable to atomically update project Codex config.toml');
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
                            throw new Error('project Codex config.toml appeared during update');
                        return [2 /*return*/];
                    }
                    if (!current || !current.isFile() || current.isSymbolicLink() || !sameFile(snapshot.identity, current) ||
                        current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('project Codex config.toml changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function unlinkIfUnchanged(filePath, snapshot) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, assertUnchanged(filePath, snapshot)];
                case 1:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(filePath)];
                case 2:
                    _a.sent();
                    return [2 /*return*/];
            }
        });
    });
}
function sameFile(left, right) {
    return left.dev === right.dev && left.ino === right.ino;
}
function identityOf(info) {
    return { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs };
}
function managedBodyHash(body) {
    return (0, node_crypto_1.createHash)('sha256').update(body, 'utf8').digest('hex');
}
function renderManagedBlock(body, eol, prefixAdded, createdConfig) {
    var marker = "".concat(beginPrefix, " sha256=").concat(managedBodyHash(body), " prefix=").concat(prefixAdded ? 1 : 0, " created=").concat(createdConfig ? 1 : 0);
    return "".concat(marker).concat(eol).concat(body).concat(eol).concat(endMarker);
}
function findManagedBlock(content) {
    var _a, _b;
    var begins = __spreadArray([], content.matchAll(/^# >>> THREADFORGE CODEX MCP BEGIN v1 sha256=([0-9a-f]{64}) prefix=([01]) created=([01])(?=\r?$)/gm), true);
    var ends = __spreadArray([], content.matchAll(/^# <<< THREADFORGE CODEX MCP END v1(?=\r?$)/gm), true);
    var hasMarker = content.includes('THREADFORGE CODEX MCP BEGIN') || content.includes('THREADFORGE CODEX MCP END');
    if (begins.length === 0 && ends.length === 0 && !hasMarker)
        return undefined;
    if (begins.length !== 1 || ends.length !== 1)
        throw new Error('Threadforge Codex MCP ownership markers are malformed or duplicated');
    var begin = begins[0];
    var end = ends[0];
    var start = (_a = begin.index) !== null && _a !== void 0 ? _a : -1;
    var markerEnd = start + begin[0].length;
    var eol = lineEndingAt(content, markerEnd);
    var bodyEnd = (_b = end.index) !== null && _b !== void 0 ? _b : -1;
    if (start < 0 || !eol || bodyEnd <= markerEnd || bodyEnd <= 0)
        throw new Error('Threadforge Codex MCP block is incomplete');
    var rawBody = content.slice(markerEnd + eol.length, bodyEnd);
    if (!rawBody.endsWith(eol))
        throw new Error('Threadforge Codex MCP block has invalid separators');
    var body = rawBody.slice(0, -eol.length);
    if (managedBodyHash(body) !== begin[1])
        throw new Error('Threadforge Codex MCP block was edited; inspect it before retrying');
    return {
        start: start,
        end: bodyEnd + end[0].length,
        body: body,
        eol: eol,
        prefixAdded: begin[2] === '1',
        createdConfig: begin[3] === '1',
    };
}
function lineEndingAt(content, index) {
    if (content.startsWith('\r\n', index))
        return '\r\n';
    if (content.startsWith('\n', index))
        return '\n';
    return undefined;
}
function detectLineEnding(content) {
    return content.includes('\r\n') ? '\r\n' : '\n';
}
function hasUnmanagedThreadforgeServer(content) {
    var currentTable = [];
    var valueDepth = 0;
    var stringState = {};
    for (var _i = 0, _a = content.split(/\r?\n/); _i < _a.length; _i++) {
        var line = _a[_i];
        var code = stripTomlComment(line, stringState).trim();
        if (!code)
            continue;
        if (valueDepth > 0) {
            valueDepth = updateTomlValueDepth(code, valueDepth);
            continue;
        }
        if (code.startsWith('[')) {
            currentTable = parseTomlTableHeader(code);
            if (currentTable[0] === 'mcp_servers' && currentTable[1] === serverName)
                return true;
            continue;
        }
        var equals = findUnquotedEquals(code);
        if (equals < 0)
            throw new Error('Codex config contains unsupported TOML syntax; no changes were made');
        var keys = parseTomlKeyPath(code.slice(0, equals).trim());
        if (keys.length === 0)
            throw new Error('Codex config contains an empty TOML key; no changes were made');
        var fullPath = __spreadArray(__spreadArray([], currentTable, true), keys, true);
        if (fullPath[0] === 'mcp_servers' && fullPath[1] === serverName)
            return true;
        if (currentTable.length === 0 && keys[0] === 'mcp_servers' && keys.length === 1) {
            throw new Error('Codex config uses an inline mcp_servers value; merge Threadforge manually');
        }
        valueDepth = updateTomlValueDepth(code.slice(equals + 1), 0);
    }
    if (stringState.multiline)
        throw new Error('Codex config contains an unterminated TOML multiline string; no changes were made');
    if (valueDepth !== 0)
        throw new Error('Codex config contains an unterminated TOML array or inline table; no changes were made');
    return false;
}
function parseTomlTableHeader(line) {
    var arrayTable = line.startsWith('[[');
    var opening = arrayTable ? 2 : 1;
    var closing = arrayTable ? ']]' : ']';
    if (!line.endsWith(closing))
        throw new Error('Codex config has an unsupported TOML table header; no changes were made');
    var keys = parseTomlKeyPath(line.slice(opening, -closing.length).trim());
    if (keys.length === 0)
        throw new Error('Codex config has an empty TOML table header; no changes were made');
    return keys;
}
function parseTomlKeyPath(value) {
    var _a, _b;
    var keys = [];
    var index = 0;
    while (index < value.length) {
        while (/\s/.test((_a = value[index]) !== null && _a !== void 0 ? _a : ''))
            index++;
        if (index >= value.length)
            break;
        var quote = value[index];
        var key = '';
        if (quote === '"' || quote === "'") {
            var start = index++;
            var escaped = false;
            for (; index < value.length; index++) {
                var char = value[index];
                if (quote === '"' && char === '\\' && !escaped) {
                    escaped = true;
                    continue;
                }
                if (char === quote && !escaped)
                    break;
                escaped = false;
            }
            if (index >= value.length)
                throw new Error('Codex config has an unterminated quoted TOML key; no changes were made');
            var raw = value.slice(start, index + 1);
            try {
                key = quote === '"' ? JSON.parse(raw) : raw.slice(1, -1);
            }
            catch (_c) {
                throw new Error('Codex config has an unsupported quoted TOML key; no changes were made');
            }
            index++;
        }
        else {
            var start = index;
            while (index < value.length && !/[.\s]/.test(value[index]))
                index++;
            key = value.slice(start, index);
            if (!/^[A-Za-z0-9_-]+$/.test(key))
                throw new Error('Codex config has an unsupported bare TOML key; no changes were made');
        }
        keys.push(key);
        while (/\s/.test((_b = value[index]) !== null && _b !== void 0 ? _b : ''))
            index++;
        if (index === value.length)
            break;
        if (value[index] !== '.')
            throw new Error('Codex config has an unsupported dotted TOML key; no changes were made');
        index++;
    }
    return keys;
}
function findUnquotedEquals(value) {
    var quote = '';
    var escaped = false;
    for (var index = 0; index < value.length; index++) {
        var char = value[index];
        if (quote) {
            if (quote === '"' && char === '\\' && !escaped) {
                escaped = true;
                continue;
            }
            if (char === quote && !escaped)
                quote = '';
            escaped = false;
            continue;
        }
        if (char === '"' || char === "'") {
            quote = char;
            continue;
        }
        if (char === '=')
            return index;
    }
    return -1;
}
function stripTomlComment(line, state) {
    var output = '';
    var index = 0;
    if (state.multiline) {
        var close_1 = findMultilineEnd(line, state.multiline, 0);
        if (close_1 < 0)
            return '';
        state.multiline = undefined;
        index = close_1 + 3;
    }
    for (; index < line.length;) {
        var char = line[index];
        if (char === '#')
            break;
        if ((char === '"' || char === "'") && line.startsWith(char.repeat(3), index)) {
            var marker = char.repeat(3);
            var close_2 = findMultilineEnd(line, marker, index + 3);
            if (close_2 < 0) {
                state.multiline = marker;
                break;
            }
            output += '""';
            index = close_2 + 3;
            continue;
        }
        if (char === '"' || char === "'") {
            var start = index++;
            var escaped = false;
            for (; index < line.length; index++) {
                var current = line[index];
                if (char === '"' && current === '\\' && !escaped) {
                    escaped = true;
                    continue;
                }
                if (current === char && !escaped)
                    break;
                escaped = false;
            }
            if (index >= line.length)
                throw new Error('Codex config has an unterminated TOML string; no changes were made');
            output += line.slice(start, index + 1);
            index++;
            continue;
        }
        output += char;
        index++;
    }
    return output;
}
function findMultilineEnd(line, marker, from) {
    for (var index = line.indexOf(marker, from); index >= 0; index = line.indexOf(marker, index + marker.length)) {
        if (marker === "'''" || !isEscaped(line, index))
            return index;
    }
    return -1;
}
function isEscaped(value, index) {
    var slashes = 0;
    for (var cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor--)
        slashes++;
    return slashes % 2 === 1;
}
function updateTomlValueDepth(value, initial) {
    var depth = initial;
    var quote = '';
    var escaped = false;
    for (var index = 0; index < value.length; index++) {
        var char = value[index];
        if (quote) {
            if (quote === '"' && char === '\\' && !escaped) {
                escaped = true;
                continue;
            }
            if (char === quote && !escaped)
                quote = '';
            escaped = false;
            continue;
        }
        if ((char === '"' || char === "'") && value.startsWith(char.repeat(3), index)) {
            var marker = char.repeat(3);
            var close_3 = findMultilineEnd(value, marker, index + 3);
            if (close_3 < 0)
                return depth;
            index = close_3 + 2;
            continue;
        }
        if (char === '"' || char === "'") {
            quote = char;
            continue;
        }
        if (char === '[' || char === '{')
            depth++;
        else if (char === ']' || char === '}')
            depth--;
        if (depth < 0)
            throw new Error('Codex config has an invalid TOML array or inline table; no changes were made');
    }
    return depth;
}
function isWithin(root, value) {
    var relative = node_path_1.default.relative(root, value);
    return relative === '' || (!node_path_1.default.isAbsolute(relative) && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)));
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
