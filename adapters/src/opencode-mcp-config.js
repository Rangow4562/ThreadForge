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
exports.openCodeMcpConfigPath = openCodeMcpConfigPath;
exports.installOpenCodeProjectMcpServer = installOpenCodeProjectMcpServer;
exports.updateManagedOpenCodeProjectMcpServer = updateManagedOpenCodeProjectMcpServer;
exports.removeManagedOpenCodeProjectMcpServer = removeManagedOpenCodeProjectMcpServer;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_path_1 = require("node:path");
var mcp_config_ts_1 = require("./mcp-config.ts");
var maxConfigBytes = 8 << 20;
function openCodeMcpConfigPath(projectRoot) {
    if (!node_path_1.default.isAbsolute(projectRoot))
        throw new Error('project root must be absolute');
    return node_path_1.default.join(node_path_1.default.resolve(projectRoot), 'opencode.json');
}
function installOpenCodeProjectMcpServer(projectRoot, serverName, command, args) {
    return __awaiter(this, void 0, void 0, function () {
        var entry;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateName(serverName);
                    return [4 /*yield*/, buildEntry(projectRoot, command, args)];
                case 1:
                    entry = _a.sent();
                    return [2 /*return*/, mutateOpenCodeConfig(projectRoot, function (config) {
                            var servers = readMcpServers(config);
                            if (Object.hasOwn(servers, serverName)) {
                                if (isOwnedEntry(servers[serverName]) && deepEqual(servers[serverName], entry))
                                    return { result: 'unchanged', changed: false };
                                throw new Error("OpenCode MCP server '".concat(serverName, "' already exists with different settings; refusing to overwrite it"));
                            }
                            servers[serverName] = entry;
                            config.mcp = servers;
                            return { result: 'installed', changed: true };
                        })];
            }
        });
    });
}
function updateManagedOpenCodeProjectMcpServer(projectRoot, serverName, command, args) {
    return __awaiter(this, void 0, void 0, function () {
        var entry;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateName(serverName);
                    return [4 /*yield*/, buildEntry(projectRoot, command, args)];
                case 1:
                    entry = _a.sent();
                    return [2 /*return*/, mutateOpenCodeConfig(projectRoot, function (config) {
                            var servers = readMcpServers(config);
                            var current = servers[serverName];
                            if (current === undefined)
                                throw new Error("OpenCode MCP server '".concat(serverName, "' is absent; refusing to update it"));
                            if (!isOwnedEntry(current))
                                throw new Error("OpenCode MCP server '".concat(serverName, "' is not an unchanged Threadforge entry; refusing to replace it"));
                            if (deepEqual(current, entry))
                                return { result: 'unchanged', changed: false };
                            servers[serverName] = entry;
                            config.mcp = servers;
                            return { result: 'updated', changed: true };
                        })];
            }
        });
    });
}
function removeManagedOpenCodeProjectMcpServer(projectRoot, serverName) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateName(serverName);
            return [2 /*return*/, mutateOpenCodeConfig(projectRoot, function (config) {
                    var servers = readMcpServers(config);
                    if (!Object.hasOwn(servers, serverName))
                        return { result: 'absent', changed: false };
                    if (!isOwnedEntry(servers[serverName]))
                        throw new Error("OpenCode MCP server '".concat(serverName, "' is not an unchanged Threadforge entry; refusing to remove it"));
                    delete servers[serverName];
                    if (Object.keys(servers).length === 0)
                        delete config.mcp;
                    else
                        config.mcp = servers;
                    return { result: 'removed', changed: true };
                })];
        });
    });
}
function buildEntry(projectRoot, command, args) {
    return __awaiter(this, void 0, void 0, function () {
        var rootIndexes, configuredRoot, expectedRoot, actualRoot, normalize, same;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (typeof command !== 'string' || command.trim() === '' || command.length > 2048 || /[\r\n\0]/.test(command)) {
                        throw new Error('OpenCode MCP executable is invalid or too long');
                    }
                    if (!Array.isArray(args) || args.length > 128 || args.some(function (arg) { return typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg); })) {
                        throw new Error('OpenCode MCP arguments must be bounded strings without control characters');
                    }
                    if (args[0] !== 'mcp' || args[1] !== 'serve' || !args.includes(mcp_config_ts_1.managedMcpArgument)) {
                        throw new Error("OpenCode MCP command must begin with mcp serve and include ".concat(mcp_config_ts_1.managedMcpArgument));
                    }
                    rootIndexes = args.flatMap(function (value, index) { return value === '--project-root' ? [index] : []; });
                    if (rootIndexes.length !== 1 || args.some(function (value) { return value.startsWith('--project-root='); })) {
                        throw new Error('OpenCode MCP command must contain exactly one --project-root <same-root> argument');
                    }
                    configuredRoot = args[rootIndexes[0] + 1];
                    if (!configuredRoot || configuredRoot.startsWith('--'))
                        throw new Error('OpenCode MCP command is missing --project-root <same-root>');
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    expectedRoot = _a.sent();
                    return [4 /*yield*/, (0, promises_1.realpath)(configuredRoot).catch(function () { throw new Error('MCP --project-root must exist and match the selected project root'); })];
                case 2:
                    actualRoot = _a.sent();
                    normalize = function (value) { return node_path_1.default.resolve(value).replace(/[\\/]+$/, ''); };
                    same = process.platform === 'win32'
                        ? normalize(expectedRoot).toLowerCase() === normalize(actualRoot).toLowerCase()
                        : normalize(expectedRoot) === normalize(actualRoot);
                    if (!same)
                        throw new Error('MCP --project-root must match the selected project root');
                    return [2 /*return*/, { type: 'local', command: __spreadArray([command], args, true), enabled: true }];
            }
        });
    });
}
function mutateOpenCodeConfig(projectRoot, mutate) {
    return __awaiter(this, void 0, void 0, function () {
        var root, rootStat, configPath, jsoncPath, lockPath, lock, error_1, config, outcome;
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
                        throw new Error('project root must be a real directory, not a symbolic link');
                    configPath = node_path_1.default.join(root, 'opencode.json');
                    jsoncPath = node_path_1.default.join(root, 'opencode.jsonc');
                    return [4 /*yield*/, pathExists(jsoncPath)];
                case 3:
                    if (_a.sent())
                        throw new Error('OpenCode opencode.jsonc is present; no changes made because comments will not be rewritten; merge the Threadforge MCP entry manually');
                    lockPath = node_path_1.default.join(root, '.threadforge-opencode-mcp.lock');
                    _a.label = 4;
                case 4:
                    _a.trys.push([4, 6, , 7]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 5:
                    lock = _a.sent();
                    return [3 /*break*/, 7];
                case 6:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'EEXIST'))
                        throw new Error('OpenCode MCP config is locked by another writer or a stale lock');
                    throw new Error('unable to lock OpenCode MCP configuration');
                case 7:
                    _a.trys.push([7, , 10, 13]);
                    return [4 /*yield*/, readConfig(configPath)];
                case 8:
                    config = _a.sent();
                    outcome = mutate(config);
                    if (!outcome.changed)
                        return [2 /*return*/, outcome.result];
                    return [4 /*yield*/, atomicWriteJson(configPath, config)];
                case 9:
                    _a.sent();
                    return [2 /*return*/, outcome.result];
                case 10: return [4 /*yield*/, lock.close().catch(function () { })];
                case 11:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 12:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 13: return [2 /*return*/];
            }
        });
    });
}
function readConfig(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, value, _a, _b, error_2;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    _c.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _c.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('OpenCode config must be a regular file, not a symbolic link');
                    if (stat.size > maxConfigBytes)
                        throw new Error('OpenCode config exceeds the local size limit');
                    _b = (_a = JSON).parse;
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                case 2:
                    value = _b.apply(_a, [(_c.sent()).replace(/^\uFEFF/, '')]);
                    if (!isRecord(value))
                        throw new Error('OpenCode config root must be an object');
                    return [2 /*return*/, value];
                case 3:
                    error_2 = _c.sent();
                    if (isCode(error_2, 'ENOENT'))
                        return [2 /*return*/, {}];
                    if (error_2 instanceof Error && error_2.message.startsWith('OpenCode config'))
                        throw error_2;
                    throw new Error('OpenCode config is not valid strict JSON; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readMcpServers(config) {
    if (config.mcp === undefined || config.mcp === null)
        return {};
    if (!isRecord(config.mcp))
        throw new Error('OpenCode config mcp must be an object; no changes were made');
    return config.mcp;
}
function isOwnedEntry(value) {
    if (!isRecord(value) || value.type !== 'local' || value.enabled !== true || !Array.isArray(value.command))
        return false;
    if (Object.keys(value).some(function (key) { return !['type', 'enabled', 'command'].includes(key); }))
        return false;
    return value.command.every(function (part) { return typeof part === 'string'; }) && value.command.includes(mcp_config_ts_1.managedMcpArgument);
}
function atomicWriteJson(filePath, value) {
    return __awaiter(this, void 0, void 0, function () {
        var tempPath, handle, mode, existing, error_3, _a;
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
                    existing = _b.sent();
                    if (!existing.isFile() || existing.isSymbolicLink())
                        throw new Error('OpenCode config target must be a regular file');
                    mode = existing.mode & 511;
                    return [3 /*break*/, 5];
                case 4:
                    error_3 = _b.sent();
                    if (!isCode(error_3, 'ENOENT'))
                        throw error_3;
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
                    throw new Error('unable to atomically update OpenCode config');
                case 14: return [2 /*return*/];
            }
        });
    });
}
function pathExists(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var error_4;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 2, , 3]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    _a.sent();
                    return [2 /*return*/, true];
                case 2:
                    error_4 = _a.sent();
                    if (isCode(error_4, 'ENOENT'))
                        return [2 /*return*/, false];
                    throw new Error('unable to inspect OpenCode JSONC config');
                case 3: return [2 /*return*/];
            }
        });
    });
}
function validateName(value) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value))
        throw new Error('MCP server name is invalid');
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
