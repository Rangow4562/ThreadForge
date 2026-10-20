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
exports.opencodeManagedMarker = void 0;
exports.installOpenCodeProjectPlugin = installOpenCodeProjectPlugin;
exports.removeOpenCodeProjectPlugin = removeOpenCodeProjectPlugin;
exports.renderOpenCodePlugin = renderOpenCodePlugin;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_path_1 = require("node:path");
exports.opencodeManagedMarker = 'threadforge-managed-opencode-plugin:v1';
var maxPluginBytes = 256 << 10;
/** Installs Threadforge as a separate project plugin file, leaving other plugins untouched. */
function installOpenCodeProjectPlugin(projectRoot, moduleSpecifier) {
    return __awaiter(this, void 0, void 0, function () {
        var filePath;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateModuleSpecifier(moduleSpecifier);
                    return [4 /*yield*/, pluginPath(projectRoot, true)];
                case 1:
                    filePath = _a.sent();
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var existing, stat, error_1, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0:
                                        _a.trys.push([0, 3, , 4]);
                                        return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                                    case 1:
                                        stat = _a.sent();
                                        if (!stat.isFile() || stat.isSymbolicLink())
                                            throw new Error('OpenCode Threadforge plugin target must be a regular file');
                                        if (stat.size > maxPluginBytes)
                                            throw new Error('OpenCode plugin target exceeds the local size limit');
                                        return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                                    case 2:
                                        existing = _a.sent();
                                        return [3 /*break*/, 4];
                                    case 3:
                                        error_1 = _a.sent();
                                        if (!isCode(error_1, 'ENOENT'))
                                            throw error_1;
                                        return [3 /*break*/, 4];
                                    case 4:
                                        if (existing !== undefined && !existing.includes(exports.opencodeManagedMarker)) {
                                            throw new Error('refusing to overwrite unmanaged OpenCode plugin file');
                                        }
                                        next = renderOpenCodePlugin(moduleSpecifier);
                                        if (existing === next)
                                            return [2 /*return*/, 'unchanged'];
                                        return [4 /*yield*/, atomicWrite(filePath, next)];
                                    case 5:
                                        _a.sent();
                                        return [2 /*return*/, existing === undefined ? 'installed' : 'updated'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Removes only the isolated plugin file that contains Threadforge's ownership marker. */
function removeOpenCodeProjectPlugin(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        var filePath;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, pluginPath(projectRoot, false)];
                case 1:
                    filePath = _a.sent();
                    if (filePath === undefined)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(filePath, function () { return __awaiter(_this, void 0, void 0, function () {
                            var text, stat, error_2;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0:
                                        _a.trys.push([0, 3, , 4]);
                                        return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                                    case 1:
                                        stat = _a.sent();
                                        if (!stat.isFile() || stat.isSymbolicLink())
                                            throw new Error('OpenCode Threadforge plugin target must be a regular file');
                                        if (stat.size > maxPluginBytes)
                                            throw new Error('OpenCode plugin target exceeds the local size limit');
                                        return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                                    case 2:
                                        text = _a.sent();
                                        return [3 /*break*/, 4];
                                    case 3:
                                        error_2 = _a.sent();
                                        if (isCode(error_2, 'ENOENT'))
                                            return [2 /*return*/, 'absent'];
                                        throw error_2;
                                    case 4:
                                        if (!text.includes(exports.opencodeManagedMarker))
                                            return [2 /*return*/, 'absent'];
                                        return [4 /*yield*/, (0, promises_1.unlink)(filePath)];
                                    case 5:
                                        _a.sent();
                                        return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function renderOpenCodePlugin(moduleSpecifier) {
    validateModuleSpecifier(moduleSpecifier);
    var quoted = JSON.stringify(moduleSpecifier);
    return [
        "// ".concat(exports.opencodeManagedMarker),
        'import { ThreadforgeOpenCodePlugin } from ' + quoted + ';',
        'export const ThreadforgeOpenCode = ThreadforgeOpenCodePlugin;',
        'export default ThreadforgeOpenCodePlugin;',
        '',
    ].join('\n');
}
function pluginPath(projectRoot, createDirectory) {
    return __awaiter(this, void 0, void 0, function () {
        var root, rootStat, opencodeDir, pluginsDir, opencodeStat, pluginsStat;
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
                    opencodeDir = node_path_1.default.join(root, '.opencode');
                    pluginsDir = node_path_1.default.join(opencodeDir, 'plugins');
                    if (!createDirectory) return [3 /*break*/, 5];
                    return [4 /*yield*/, ensureDirectory(root, opencodeDir)];
                case 3:
                    _a.sent();
                    return [4 /*yield*/, ensureDirectory(root, pluginsDir)];
                case 4:
                    _a.sent();
                    return [3 /*break*/, 8];
                case 5: return [4 /*yield*/, (0, promises_1.lstat)(opencodeDir).catch(function () { return null; })];
                case 6:
                    opencodeStat = _a.sent();
                    if (!opencodeStat)
                        return [2 /*return*/, undefined];
                    if (!opencodeStat.isDirectory() || opencodeStat.isSymbolicLink())
                        throw new Error('OpenCode project config path contains a symbolic link or non-directory');
                    return [4 /*yield*/, (0, promises_1.lstat)(pluginsDir).catch(function () { return null; })];
                case 7:
                    pluginsStat = _a.sent();
                    if (!pluginsStat)
                        return [2 /*return*/, undefined];
                    if (!pluginsStat.isDirectory() || pluginsStat.isSymbolicLink())
                        throw new Error('OpenCode plugin path contains a symbolic link or non-directory');
                    _a.label = 8;
                case 8: return [2 /*return*/, node_path_1.default.join(pluginsDir, 'threadforge.ts')];
            }
        });
    });
}
function ensureDirectory(root, target) {
    return __awaiter(this, void 0, void 0, function () {
        var relative, current, _i, _a, part, error_3, stat;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    relative = node_path_1.default.relative(root, target);
                    if (relative === '..' || relative.startsWith("..".concat(node_path_1.default.sep)) || node_path_1.default.isAbsolute(relative)) {
                        throw new Error('OpenCode plugin path escapes the project root');
                    }
                    current = root;
                    _i = 0, _a = relative.split(node_path_1.default.sep).filter(Boolean);
                    _b.label = 1;
                case 1:
                    if (!(_i < _a.length)) return [3 /*break*/, 8];
                    part = _a[_i];
                    current = node_path_1.default.join(current, part);
                    _b.label = 2;
                case 2:
                    _b.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(current)];
                case 3:
                    _b.sent();
                    return [3 /*break*/, 5];
                case 4:
                    error_3 = _b.sent();
                    if (!isCode(error_3, 'EEXIST'))
                        throw new Error('unable to create OpenCode plugin directory');
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { return null; })];
                case 6:
                    stat = _b.sent();
                    if (!(stat === null || stat === void 0 ? void 0 : stat.isDirectory()) || stat.isSymbolicLink())
                        throw new Error('OpenCode plugin path contains a symbolic link or non-directory');
                    _b.label = 7;
                case 7:
                    _i++;
                    return [3 /*break*/, 1];
                case 8: return [2 /*return*/];
            }
        });
    });
}
function withLock(filePath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lockPath, lock, error_4;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    lockPath = node_path_1.default.join(node_path_1.default.dirname(filePath), '.threadforge-opencode-plugin.lock');
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    lock = _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_4 = _a.sent();
                    if (isCode(error_4, 'EEXIST'))
                        throw new Error('OpenCode plugin is locked by another installer or a stale lock');
                    throw new Error('unable to lock OpenCode plugin directory');
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
function atomicWrite(filePath, text) {
    return __awaiter(this, void 0, void 0, function () {
        var tempPath, handle, mode, error_5, _a;
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
                    error_5 = _b.sent();
                    if (!isCode(error_5, 'ENOENT'))
                        throw error_5;
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
                case 6:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.writeFile(text, 'utf8')];
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
                    throw new Error('unable to atomically update OpenCode plugin file');
                case 14: return [2 /*return*/];
            }
        });
    });
}
function validateModuleSpecifier(value) {
    if (typeof value !== 'string' || value.trim() !== value || value.length < 1 || value.length > 4096 || /[\r\n\0]/.test(value)) {
        throw new Error('OpenCode adapter module specifier is invalid');
    }
    if (value === '@threadforge/adapter-contracts/opencode-plugin')
        return;
    var url;
    try {
        url = new URL(value);
    }
    catch (_a) {
        throw new Error('module specifier must be the Threadforge package export or an absolute file URL');
    }
    if (url.protocol !== 'file:' || url.username || url.password || url.search || url.hash) {
        throw new Error('module specifier must be a local file URL without credentials or query parameters');
    }
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function isCode(error, code) {
    return isRecord(error) && error.code === code;
}
