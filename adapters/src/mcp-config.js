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
exports.managedMcpArgument = exports.projectMcpHosts = void 0;
exports.projectMcpConfigPath = projectMcpConfigPath;
exports.installProjectMcpServer = installProjectMcpServer;
exports.updateProjectMcpServer = updateProjectMcpServer;
exports.updateManagedProjectMcpServer = updateManagedProjectMcpServer;
exports.removeProjectMcpServer = removeProjectMcpServer;
exports.removeManagedProjectMcpServer = removeManagedProjectMcpServer;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_path_1 = require("node:path");
exports.projectMcpHosts = {
    'claude-code': '.mcp.json',
    cursor: node_path_1.default.join('.cursor', 'mcp.json'),
    antigravity: node_path_1.default.join('.agents', 'mcp_config.json'),
    'gemini-cli': node_path_1.default.join('.gemini', 'settings.json'),
};
exports.managedMcpArgument = '--threadforge-managed-mcp=v1';
var maxConfigBytes = 8 << 20;
function projectMcpConfigPath(projectRoot, host) {
    if (!Object.hasOwn(exports.projectMcpHosts, host))
        throw new Error('unsupported project MCP host');
    if (!node_path_1.default.isAbsolute(projectRoot))
        throw new Error('project root must be absolute');
    return node_path_1.default.join(node_path_1.default.resolve(projectRoot), exports.projectMcpHosts[host]);
}
function installProjectMcpServer(projectRoot, host, serverName, server) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateServerName(serverName);
            validateServerForHost(host, server);
            if (!isManagedServer(server))
                throw new Error('MCP server entry must include the Threadforge ownership argument');
            return [2 /*return*/, mutateProjectConfig(projectRoot, host, function (config) {
                    var servers = getServers(config);
                    if (Object.hasOwn(servers, serverName)) {
                        if (isManagedServer(servers[serverName]) && deepEqual(servers[serverName], server))
                            return { result: 'unchanged', changed: false };
                        throw new Error("MCP server '".concat(serverName, "' already exists with different settings; refusing to overwrite it"));
                    }
                    servers[serverName] = server;
                    config.mcpServers = servers;
                    return { result: 'installed', changed: true };
                })];
        });
    });
}
function updateProjectMcpServer(projectRoot, host, serverName, expectedCurrent, next) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateServerName(serverName);
            validateServerForHost(host, next);
            if (!isManagedServer(next))
                throw new Error('updated MCP server entry must include the Threadforge ownership argument');
            return [2 /*return*/, mutateProjectConfig(projectRoot, host, function (config) {
                    var servers = getServers(config);
                    if (!Object.hasOwn(servers, serverName)) {
                        throw new Error("MCP server '".concat(serverName, "' is absent; refusing to create it during update"));
                    }
                    if (!isManagedServer(servers[serverName]) || !isManagedServer(expectedCurrent) || !deepEqual(servers[serverName], expectedCurrent)) {
                        throw new Error("MCP server '".concat(serverName, "' changed since it was read; refusing to overwrite it"));
                    }
                    requireGeminiTrustOff(host, servers[serverName]);
                    if (deepEqual(servers[serverName], next))
                        return { result: 'unchanged', changed: false };
                    servers[serverName] = next;
                    config.mcpServers = servers;
                    return { result: 'updated', changed: true };
                })];
        });
    });
}
function updateManagedProjectMcpServer(projectRoot, host, serverName, next) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateServerName(serverName);
            validateServerForHost(host, next);
            if (!isManagedServer(next))
                throw new Error('updated MCP server entry must include the Threadforge ownership argument');
            return [2 /*return*/, mutateProjectConfig(projectRoot, host, function (config) {
                    var servers = getServers(config);
                    if (!Object.hasOwn(servers, serverName))
                        throw new Error("MCP server '".concat(serverName, "' is absent; refusing to update it"));
                    if (!isManagedServer(servers[serverName]))
                        throw new Error("MCP server '".concat(serverName, "' is not marked as Threadforge-managed"));
                    requireGeminiTrustOff(host, servers[serverName]);
                    if (deepEqual(servers[serverName], next))
                        return { result: 'unchanged', changed: false };
                    servers[serverName] = next;
                    config.mcpServers = servers;
                    return { result: 'updated', changed: true };
                })];
        });
    });
}
function removeProjectMcpServer(projectRoot, host, serverName, expectedCurrent) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateServerName(serverName);
            return [2 /*return*/, mutateProjectConfig(projectRoot, host, function (config) {
                    var servers = getServers(config);
                    if (!Object.hasOwn(servers, serverName))
                        return { result: 'absent', changed: false };
                    if (!isManagedServer(servers[serverName]) || !isManagedServer(expectedCurrent) || !deepEqual(servers[serverName], expectedCurrent)) {
                        throw new Error("MCP server '".concat(serverName, "' changed since it was read; refusing to remove it"));
                    }
                    requireGeminiTrustOff(host, servers[serverName]);
                    delete servers[serverName];
                    if (Object.keys(servers).length === 0)
                        delete config.mcpServers;
                    else
                        config.mcpServers = servers;
                    return { result: 'removed', changed: true };
                })];
        });
    });
}
function removeManagedProjectMcpServer(projectRoot, host, serverName) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            validateServerName(serverName);
            return [2 /*return*/, mutateProjectConfig(projectRoot, host, function (config) {
                    var servers = getServers(config);
                    if (!Object.hasOwn(servers, serverName))
                        return { result: 'absent', changed: false };
                    if (!isManagedServer(servers[serverName]))
                        throw new Error("MCP server '".concat(serverName, "' is not marked as Threadforge-managed"));
                    requireGeminiTrustOff(host, servers[serverName]);
                    delete servers[serverName];
                    if (Object.keys(servers).length === 0)
                        delete config.mcpServers;
                    else
                        config.mcpServers = servers;
                    return { result: 'removed', changed: true };
                })];
        });
    });
}
function mutateProjectConfig(projectRoot, host, mutate) {
    return __awaiter(this, void 0, void 0, function () {
        var requestedPath, root, configPath, parent, lockPath, lock, error_1, config, outcome;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    requestedPath = projectMcpConfigPath(projectRoot, host);
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () {
                            throw new Error('project root must exist and be accessible');
                        })];
                case 1:
                    root = _a.sent();
                    configPath = node_path_1.default.join(root, node_path_1.default.relative(node_path_1.default.resolve(projectRoot), requestedPath));
                    parent = node_path_1.default.dirname(configPath);
                    return [4 /*yield*/, ensureSafeDirectory(root, parent)];
                case 2:
                    _a.sent();
                    lockPath = node_path_1.default.join(parent, '.threadforge-mcp-config.lock');
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 4:
                    lock = _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'EEXIST')) {
                        throw new Error('project MCP config is locked by another installer or a stale lock; inspect it before retrying');
                    }
                    throw new Error('unable to lock project MCP configuration');
                case 6:
                    _a.trys.push([6, , 9, 12]);
                    return [4 /*yield*/, readConfig(configPath)];
                case 7:
                    config = _a.sent();
                    outcome = mutate(config);
                    if (!outcome.changed)
                        return [2 /*return*/, outcome.result];
                    return [4 /*yield*/, atomicWriteJson(configPath, config)];
                case 8:
                    _a.sent();
                    return [2 /*return*/, outcome.result];
                case 9: return [4 /*yield*/, lock.close().catch(function () { })];
                case 10:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 11:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 12: return [2 /*return*/];
            }
        });
    });
}
function ensureSafeDirectory(root, target) {
    return __awaiter(this, void 0, void 0, function () {
        var relative, current, rootStat, _i, _a, segment, error_2, stat;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    relative = node_path_1.default.relative(root, target);
                    if (relative === '..' || relative.startsWith("..".concat(node_path_1.default.sep)) || node_path_1.default.isAbsolute(relative)) {
                        throw new Error('project MCP config path escapes the project root');
                    }
                    current = root;
                    return [4 /*yield*/, (0, promises_1.lstat)(root).catch(function () { return null; })];
                case 1:
                    rootStat = _b.sent();
                    if (!(rootStat === null || rootStat === void 0 ? void 0 : rootStat.isDirectory()) || rootStat.isSymbolicLink()) {
                        throw new Error('project root must be a real directory, not a symbolic link');
                    }
                    _i = 0, _a = relative.split(node_path_1.default.sep).filter(Boolean);
                    _b.label = 2;
                case 2:
                    if (!(_i < _a.length)) return [3 /*break*/, 9];
                    segment = _a[_i];
                    current = node_path_1.default.join(current, segment);
                    _b.label = 3;
                case 3:
                    _b.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(current)];
                case 4:
                    _b.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_2 = _b.sent();
                    if (!isCode(error_2, 'EEXIST'))
                        throw new Error('unable to create project MCP config directory');
                    return [3 /*break*/, 6];
                case 6: return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { return null; })];
                case 7:
                    stat = _b.sent();
                    if (!(stat === null || stat === void 0 ? void 0 : stat.isDirectory()) || stat.isSymbolicLink()) {
                        throw new Error('project MCP config path contains a non-directory or symbolic link');
                    }
                    _b.label = 8;
                case 8:
                    _i++;
                    return [3 /*break*/, 2];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function readConfig(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, stat, error_3, text, value;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _a.sent();
                    if (!stat.isFile() || stat.isSymbolicLink()) {
                        throw new Error('project MCP config must be a regular file, not a symbolic link');
                    }
                    if (stat.size > maxConfigBytes)
                        throw new Error('project MCP config exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath)];
                case 2:
                    bytes = _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, {}];
                    if (error_3 instanceof Error && error_3.message.startsWith('project MCP config'))
                        throw error_3;
                    throw new Error('unable to read project MCP config');
                case 4:
                    try {
                        text = bytes.toString('utf8').replace(/^\uFEFF/, '');
                        value = JSON.parse(text);
                        if (!isRecord(value))
                            throw new Error('root must be an object');
                        return [2 /*return*/, value];
                    }
                    catch (_b) {
                        throw new Error('project MCP config is not valid strict JSON; no changes were made');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function getServers(config) {
    if (config.mcpServers === undefined)
        return {};
    if (!isRecord(config.mcpServers))
        throw new Error('project MCP config mcpServers must be an object; no changes were made');
    return config.mcpServers;
}
function atomicWriteJson(filePath, value) {
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
                    throw new Error('unable to atomically update project MCP config');
                case 14: return [2 /*return*/];
            }
        });
    });
}
function validateServerName(value) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value))
        throw new Error('MCP server name is invalid');
}
function validateServer(value) {
    if (!isRecord(value) || typeof value.command !== 'string' || value.command.trim() === '' || value.command.length > 2048) {
        throw new Error('MCP server command is required and must be bounded');
    }
    if (!Array.isArray(value.args) || value.args.length > 128 || value.args.some(function (arg) { return typeof arg !== 'string' || arg.length > 4096; })) {
        throw new Error('MCP server args must be a bounded string array');
    }
    if (value.cwd !== undefined && (typeof value.cwd !== 'string' || value.cwd.trim() === '' || value.cwd.length > 4096)) {
        throw new Error('MCP server cwd must be a bounded non-empty path');
    }
    if (value.trust !== undefined && value.trust !== false) {
        throw new Error('MCP server trust must remain false; Threadforge never disables host confirmations');
    }
    var extraKeys = Object.keys(value).filter(function (key) { return key !== 'command' && key !== 'args' && key !== 'cwd' && key !== 'trust'; });
    if (extraKeys.length > 0)
        throw new Error('MCP server config only accepts command, args, and cwd; environment secrets are not accepted');
}
function validateServerForHost(host, value) {
    validateServer(value);
    if (host === 'gemini-cli' && value.trust !== false) {
        throw new Error('Gemini MCP config must explicitly set trust to false');
    }
}
function requireGeminiTrustOff(host, value) {
    if (host === 'gemini-cli' && (!isRecord(value) || value.trust !== false)) {
        throw new Error('Gemini MCP trust changed; refusing to replace or remove the user-edited entry');
    }
}
function isManagedServer(value) {
    return isRecord(value) && Array.isArray(value.args) && value.args.includes(exports.managedMcpArgument);
}
function deepEqual(left, right) {
    return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}
function canonicalize(value) {
    if (Array.isArray(value))
        return value.map(canonicalize);
    if (isRecord(value)) {
        return Object.fromEntries(Object.keys(value).sort().map(function (key) { return [key, canonicalize(value[key])]; }));
    }
    return value;
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return isRecord(error) && error.code === code;
}
