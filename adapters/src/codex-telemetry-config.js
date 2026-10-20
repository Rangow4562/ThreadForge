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
exports.installCodexTelemetry = installCodexTelemetry;
exports.removeCodexTelemetry = removeCodexTelemetry;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_util_1 = require("node:util");
var maxConfigBytes = 8 << 20;
var beginPrefix = '# >>> THREADFORGE CODEX OTEL BEGIN v1';
var endMarker = '# <<< THREADFORGE CODEX OTEL END v1';
/** Installs only a hash-owned local OTLP block in the selected Codex user home. */
function installCodexTelemetry() {
    return __awaiter(this, arguments, void 0, function (options) {
        var paths;
        var _this = this;
        var _a, _b;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0: return [4 /*yield*/, configPaths((_b = (_a = options.codexHome) !== null && _a !== void 0 ? _a : process.env.CODEX_HOME) !== null && _b !== void 0 ? _b : node_path_1.default.join(node_os_1.default.homedir(), '.codex'), true)];
                case 1:
                    paths = _c.sent();
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var snapshot, managed, outside, eol, body, block_1, next, prefixAdded, separator, block;
                            var _a;
                            return __generator(this, function (_b) {
                                switch (_b.label) {
                                    case 0: return [4 /*yield*/, readConfig(paths.config)];
                                    case 1:
                                        snapshot = _b.sent();
                                        managed = findManagedBlock(snapshot.content);
                                        outside = managed
                                            ? snapshot.content.slice(0, managed.start) + snapshot.content.slice(managed.end)
                                            : snapshot.content;
                                        if (hasUnmanagedOtelConfig(outside)) {
                                            throw new Error('Codex config already contains an unmanaged otel section or key; merge it manually before installing Threadforge');
                                        }
                                        eol = (_a = managed === null || managed === void 0 ? void 0 : managed.eol) !== null && _a !== void 0 ? _a : detectLineEnding(snapshot.content);
                                        body = managedConfigBody(eol);
                                        if ((managed === null || managed === void 0 ? void 0 : managed.body) === body)
                                            return [2 /*return*/, 'unchanged'];
                                        if (!managed) return [3 /*break*/, 3];
                                        block_1 = renderManagedBlock(body, eol, managed.prefixAdded, managed.createdConfig);
                                        next = snapshot.content.slice(0, managed.start) + block_1 + snapshot.content.slice(managed.end);
                                        return [4 /*yield*/, atomicWrite(paths.config, next, snapshot)];
                                    case 2:
                                        _b.sent();
                                        return [2 /*return*/, 'updated'];
                                    case 3:
                                        prefixAdded = snapshot.content.length > 0 && !/(?:\r\n|\n)$/.test(snapshot.content);
                                        separator = prefixAdded ? eol : '';
                                        block = renderManagedBlock(body, eol, prefixAdded, !snapshot.existed);
                                        return [4 /*yield*/, atomicWrite(paths.config, "".concat(snapshot.content).concat(separator).concat(block).concat(eol), snapshot)];
                                    case 4:
                                        _b.sent();
                                        return [2 /*return*/, 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Removes only the unchanged Threadforge-owned TOML block and leaves other bytes untouched. */
function removeCodexTelemetry() {
    return __awaiter(this, arguments, void 0, function (options) {
        var paths;
        var _this = this;
        var _a, _b;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0: return [4 /*yield*/, configPaths((_b = (_a = options.codexHome) !== null && _a !== void 0 ? _a : process.env.CODEX_HOME) !== null && _b !== void 0 ? _b : node_path_1.default.join(node_os_1.default.homedir(), '.codex'), false)];
                case 1:
                    paths = _c.sent();
                    if (!paths)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var snapshot, managed, start, end, suffix, separator, beforeBlock, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(paths.config)];
                                    case 1:
                                        snapshot = _a.sent();
                                        managed = findManagedBlock(snapshot.content);
                                        if (!managed)
                                            return [2 /*return*/, 'absent'];
                                        start = managed.start;
                                        if (managed.prefixAdded && start >= managed.eol.length &&
                                            snapshot.content.slice(start - managed.eol.length, start) === managed.eol) {
                                            start -= managed.eol.length;
                                        }
                                        end = managed.end;
                                        suffix = snapshot.content.slice(end);
                                        separator = suffix.startsWith('\r\n') ? '\r\n' : suffix.startsWith('\n') ? '\n' : '';
                                        beforeBlock = snapshot.content.slice(0, start);
                                        if (separator && (suffix.length === separator.length || (!managed.prefixAdded && beforeBlock.endsWith(separator)))) {
                                            end += separator.length;
                                        }
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
function configPaths(codexHome, create) {
    return __awaiter(this, void 0, void 0, function () {
        var absolute, info, parent_1, error_1, canonical, canonicalInfo;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(codexHome))
                        throw new Error('CODEX_HOME must be an absolute path');
                    absolute = node_path_1.default.resolve(codexHome);
                    return [4 /*yield*/, (0, promises_1.lstat)(absolute).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('unable to inspect CODEX_HOME');
                        })];
                case 1:
                    info = _a.sent();
                    if (!!info) return [3 /*break*/, 8];
                    if (!create)
                        return [2 /*return*/, undefined];
                    return [4 /*yield*/, (0, promises_1.realpath)(node_path_1.default.dirname(absolute)).catch(function () { throw new Error('CODEX_HOME parent must exist and be accessible'); })];
                case 2:
                    parent_1 = _a.sent();
                    if (!node_path_1.default.isAbsolute(parent_1))
                        throw new Error('CODEX_HOME parent is invalid');
                    _a.label = 3;
                case 3:
                    _a.trys.push([3, 5, , 6]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(absolute, { mode: 448 })];
                case 4:
                    _a.sent();
                    return [3 /*break*/, 6];
                case 5:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create CODEX_HOME directory');
                    return [3 /*break*/, 6];
                case 6: return [4 /*yield*/, (0, promises_1.lstat)(absolute).catch(function () { return null; })];
                case 7:
                    info = _a.sent();
                    _a.label = 8;
                case 8:
                    if (!info || !info.isDirectory() || info.isSymbolicLink()) {
                        throw new Error('CODEX_HOME must be a real directory, not a symbolic link');
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(absolute).catch(function () { throw new Error('CODEX_HOME could not be resolved'); })];
                case 9:
                    canonical = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(canonical).catch(function () { return null; })];
                case 10:
                    canonicalInfo = _a.sent();
                    if (!canonicalInfo || !canonicalInfo.isDirectory() || canonicalInfo.isSymbolicLink()) {
                        throw new Error('CODEX_HOME must resolve to a real directory');
                    }
                    return [2 /*return*/, {
                            config: node_path_1.default.join(canonical, 'config.toml'),
                            lock: node_path_1.default.join(canonical, '.threadforge-codex-telemetry.lock'),
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
                        throw new Error('Codex telemetry config is locked by another writer or a stale lock');
                    throw new Error('unable to lock Codex telemetry config');
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
                        throw new Error('unable to inspect Codex config.toml');
                    })];
                case 1:
                    initial = _b.sent();
                    if (!initial)
                        return [2 /*return*/, { content: '', existed: false }];
                    if (!initial.isFile() || initial.isSymbolicLink())
                        throw new Error('Codex config.toml must be a regular, non-symlink file');
                    if (initial.size > maxConfigBytes)
                        throw new Error('Codex config.toml exceeds the local size limit');
                    _b.label = 2;
                case 2:
                    _b.trys.push([2, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 3:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 4:
                    opened = _b.sent();
                    if (!sameFile(initial, opened))
                        throw new Error('Codex config.toml changed before reading');
                    return [4 /*yield*/, handle.readFile()];
                case 5:
                    bytes = _b.sent();
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Codex config.toml exceeds the local size limit');
                    content = void 0;
                    try {
                        content = new node_util_1.TextDecoder('utf-8', { fatal: true }).decode(bytes);
                    }
                    catch (_c) {
                        throw new Error('Codex config.toml is not valid UTF-8; no changes were made');
                    }
                    return [2 /*return*/, { content: content, existed: true, identity: identityOf(initial) }];
                case 6:
                    error_3 = _b.sent();
                    if (error_3 instanceof Error && error_3.message.startsWith('Codex config.toml'))
                        throw error_3;
                    throw new Error('Codex config.toml could not be opened safely');
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
                        throw new Error('Codex config.toml would exceed the local size limit');
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
                    throw new Error('unable to atomically update Codex config.toml');
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
                            throw new Error('Codex config.toml appeared during update');
                        return [2 /*return*/];
                    }
                    if (!current || !current.isFile() || current.isSymbolicLink() || !sameFile(snapshot.identity, current) ||
                        current.size !== snapshot.identity.size || current.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Codex config.toml changed during update');
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
function managedConfigBody(eol) {
    return [
        '[otel]',
        'environment = "local"',
        'metrics_exporter = "none"',
        'log_user_prompt = false',
        '',
        '[otel.exporter."otlp-http"]',
        'endpoint = "http://127.0.0.1:4318/v1/logs"',
        'protocol = "json"',
        '',
        '[otel.trace_exporter."otlp-http"]',
        'endpoint = "http://127.0.0.1:4318/v1/traces"',
        'protocol = "json"',
    ].join(eol);
}
function renderManagedBlock(body, eol, prefixAdded, createdConfig) {
    var digest = (0, node_crypto_1.createHash)('sha256').update(body, 'utf8').digest('hex');
    var marker = "".concat(beginPrefix, " sha256=").concat(digest, " prefix=").concat(prefixAdded ? 1 : 0, " created=").concat(createdConfig ? 1 : 0);
    return "".concat(marker).concat(eol).concat(body).concat(eol).concat(endMarker);
}
function findManagedBlock(content) {
    var _a, _b, _c;
    var begins = __spreadArray([], content.matchAll(/^# >>> THREADFORGE CODEX OTEL BEGIN v1 sha256=([0-9a-f]{64}) prefix=([01]) created=([01])(?=\r?$)/gm), true);
    var ends = __spreadArray([], content.matchAll(/^# <<< THREADFORGE CODEX OTEL END v1(?=\r?$)/gm), true);
    var containsMarkerText = content.includes('THREADFORGE CODEX OTEL BEGIN') || content.includes('THREADFORGE CODEX OTEL END');
    if (begins.length === 0 && ends.length === 0 && !containsMarkerText)
        return undefined;
    if (begins.length !== 1 || ends.length !== 1)
        throw new Error('Threadforge Codex telemetry markers are malformed or duplicated');
    var begin = begins[0];
    var end = ends[0];
    var start = (_a = begin.index) !== null && _a !== void 0 ? _a : -1;
    var markerEnd = start + begin[0].length;
    var eol = lineEndingAt(content, markerEnd);
    if (start < 0 || !eol || ((_b = end.index) !== null && _b !== void 0 ? _b : -1) <= markerEnd)
        throw new Error('Threadforge Codex telemetry block is incomplete');
    var bodyStart = markerEnd + eol.length;
    var bodyEnd = (_c = end.index) !== null && _c !== void 0 ? _c : -1;
    var rawBody = content.slice(bodyStart, bodyEnd);
    if (!rawBody.endsWith(eol))
        throw new Error('Threadforge Codex telemetry block has invalid separators');
    var body = rawBody.slice(0, -eol.length);
    var digest = (0, node_crypto_1.createHash)('sha256').update(body, 'utf8').digest('hex');
    if (digest !== begin[1])
        throw new Error('Threadforge Codex telemetry block was edited; inspect it before retrying');
    return {
        start: start,
        end: bodyEnd + end[0].length,
        body: body,
        eol: eol,
        prefixAdded: begin[2] === '1',
        createdConfig: begin[3] === '1',
    };
}
function hasUnmanagedOtelConfig(content) {
    var inRoot = true;
    var strings = {};
    for (var _i = 0, _a = content.split(/\r?\n/); _i < _a.length; _i++) {
        var line = _a[_i];
        var code = stripTomlComment(line, strings).trim();
        if (!code)
            continue;
        if (code.startsWith('[')) {
            var double = code.startsWith('[[');
            var openLength = double ? 2 : 1;
            var close_1 = double ? ']]' : ']';
            if (!code.endsWith(close_1))
                throw new Error('Codex config has an unsupported TOML table header; no changes were made');
            var tablePath = code.slice(openLength, -openLength).trim();
            if (!tablePath)
                throw new Error('Codex config has an empty TOML table header; no changes were made');
            inRoot = false;
            if (firstKeyIsOtel(tablePath))
                return true;
            continue;
        }
        if (inRoot) {
            var equals = findUnquotedEquals(code);
            if (equals >= 0 && firstKeyIsOtel(code.slice(0, equals).trim()))
                return true;
        }
    }
    if (strings.multiline)
        throw new Error('Codex config has an unterminated TOML multiline string; no changes were made');
    return false;
}
function stripTomlComment(line, state) {
    if (state.multiline) {
        var close_2 = line.indexOf(state.multiline);
        if (close_2 < 0)
            return '';
        state.multiline = undefined;
        return '';
    }
    var result = '';
    for (var index = 0; index < line.length;) {
        var char = line[index];
        if (char === '#')
            return result;
        if ((char === '"' || char === "'") && line.startsWith(char.repeat(3), index)) {
            var marker = char.repeat(3);
            var close_3 = line.indexOf(marker, index + 3);
            if (close_3 < 0) {
                state.multiline = marker;
                return result;
            }
            result += line.slice(index, close_3 + 3);
            index = close_3 + 3;
            continue;
        }
        if (char === '"' || char === "'") {
            var end = index + 1;
            var escaped = false;
            for (; end < line.length; end++) {
                var current = line[end];
                if (char === '"' && current === '\\' && !escaped) {
                    escaped = true;
                    continue;
                }
                if (current === char && !escaped)
                    break;
                escaped = false;
            }
            if (end >= line.length)
                throw new Error('Codex config has an unterminated TOML string; no changes were made');
            result += line.slice(index, end + 1);
            index = end + 1;
            continue;
        }
        result += char;
        index++;
    }
    return result;
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
function firstKeyIsOtel(value) {
    return /^(?:otel|"otel"|'otel')(?=\s*(?:\.|$))/.test(value.trim());
}
function detectLineEnding(content) {
    return content.includes('\r\n') ? '\r\n' : '\n';
}
function lineEndingAt(content, index) {
    if (content.startsWith('\r\n', index))
        return '\r\n';
    if (content.startsWith('\n', index))
        return '\n';
    return undefined;
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
