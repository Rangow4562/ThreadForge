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
exports.installKimiCodeHooks = installKimiCodeHooks;
exports.updateKimiCodeHooks = updateKimiCodeHooks;
exports.removeKimiCodeHooks = removeKimiCodeHooks;
exports.kimiCodeConfigPath = kimiCodeConfigPath;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var kimi_code_hook_ts_1 = require("./kimi-code-hook.ts");
var maxConfigBytes = 8 << 20;
var lockName = '.threadforge-kimi-code-hooks.lock';
var hookDefinitions = [
    { event: 'SessionStart', timeout: 10 },
    { event: 'UserPromptSubmit', timeout: 30 },
    { event: 'PreToolUse', timeout: 10 },
    { event: 'PostToolUse', timeout: 10 },
    { event: 'PostToolUseFailure', timeout: 10 },
    { event: 'PermissionRequest', timeout: 10 },
    { event: 'PermissionResult', timeout: 10 },
    { event: 'Stop', timeout: 45 },
    { event: 'SessionEnd', timeout: 45 },
    { event: 'SubagentStart', timeout: 10 },
    { event: 'SubagentStop', timeout: 10 },
    { event: 'PreCompact', timeout: 10 },
    { event: 'PostCompact', timeout: 10 },
    { event: 'StopFailure', timeout: 10 },
    { event: 'Interrupt', timeout: 10 },
    { event: 'TaskStarted', timeout: 10 },
    { event: 'Notification', timeout: 10 },
];
/** Installs user-scope Kimi Code hooks by appending validated TOML array tables. */
function installKimiCodeHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var config;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateCommand(options.command);
                    return [4 /*yield*/, resolveConfig(options.kimiCodeHome, true)];
                case 1:
                    config = _a.sent();
                    return [2 /*return*/, withLock(config, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, scan, owned, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(config)];
                                    case 1:
                                        read = _a.sent();
                                        scan = scanKimiToml(read.text);
                                        if (scan.inlineHooks)
                                            throw new Error('Kimi Code uses [[hooks]] array tables; inline hooks configuration cannot be safely extended');
                                        owned = scan.hooks.filter(function (entry) { return entry.managed; });
                                        if (owned.length > 0) {
                                            if (hasExpectedHooks(owned, options.command))
                                                return [2 /*return*/, 'unchanged'];
                                            throw new Error('Threadforge Kimi Code hooks already exist; use update to replace the marked entries');
                                        }
                                        next = appendHooks(read.text, options.command);
                                        return [4 /*yield*/, atomicWrite(config, read.snapshot, next)];
                                    case 2:
                                        _a.sent();
                                        return [2 /*return*/, 'installed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
/** Updates only marked Kimi Code hook entries; it never creates an install during update. */
function updateKimiCodeHooks(options) {
    return __awaiter(this, void 0, void 0, function () {
        var config;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    validateCommand(options.command);
                    return [4 /*yield*/, resolveConfig(options.kimiCodeHome, false)];
                case 1:
                    config = _a.sent();
                    if (!config)
                        throw new Error('Kimi Code config.toml is absent; install Threadforge hooks before updating');
                    return [2 /*return*/, withLock(config, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, scan, owned, next;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(config)];
                                    case 1:
                                        read = _a.sent();
                                        scan = scanKimiToml(read.text);
                                        if (scan.inlineHooks)
                                            throw new Error('Kimi Code inline hooks configuration cannot be safely updated');
                                        owned = scan.hooks.filter(function (entry) { return entry.managed; });
                                        if (owned.length === 0)
                                            throw new Error('Threadforge Kimi Code hooks are absent; refusing to create them during update');
                                        if (hasExpectedHooks(owned, options.command))
                                            return [2 /*return*/, 'unchanged'];
                                        next = appendHooks(removeHookTables(read.text, owned), options.command);
                                        return [4 /*yield*/, atomicWrite(config, read.snapshot, next)];
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
function removeKimiCodeHooks() {
    return __awaiter(this, arguments, void 0, function (options) {
        var config;
        var _this = this;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, resolveConfig(options.kimiCodeHome, false)];
                case 1:
                    config = _a.sent();
                    if (!config)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(config, function () { return __awaiter(_this, void 0, void 0, function () {
                            var read, scan, owned;
                            return __generator(this, function (_a) {
                                switch (_a.label) {
                                    case 0: return [4 /*yield*/, readConfig(config)];
                                    case 1:
                                        read = _a.sent();
                                        scan = scanKimiToml(read.text);
                                        owned = scan.hooks.filter(function (entry) { return entry.managed; });
                                        if (owned.length === 0)
                                            return [2 /*return*/, 'absent'];
                                        if (scan.inlineHooks)
                                            throw new Error('Kimi Code inline hooks configuration cannot be safely uninstalled');
                                        return [4 /*yield*/, atomicWrite(config, read.snapshot, removeHookTables(read.text, owned))];
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
function kimiCodeConfigPath(kimiCodeHome) {
    if (!node_path_1.default.isAbsolute(kimiCodeHome))
        throw new Error('KIMI_CODE_HOME must be an absolute path');
    return node_path_1.default.join(kimiCodeHome, 'config.toml');
}
function resolveConfig(kimiCodeHome, createDefaultHome) {
    return __awaiter(this, void 0, void 0, function () {
        var configuredHome, root, inputStat, home, homeInfo, defaultRoot, error_1, info_1, rootStat, config, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    configuredHome = kimiCodeHome !== null && kimiCodeHome !== void 0 ? kimiCodeHome : process.env.KIMI_CODE_HOME;
                    if (!configuredHome) return [3 /*break*/, 3];
                    if (!node_path_1.default.isAbsolute(configuredHome))
                        throw new Error('KIMI_CODE_HOME must be an absolute path');
                    return [4 /*yield*/, (0, promises_1.lstat)(configuredHome).catch(function (error) {
                            if (!createDefaultHome && isCode(error, 'ENOENT'))
                                return null;
                            throw new Error('KIMI_CODE_HOME must exist and be accessible');
                        })];
                case 1:
                    inputStat = _a.sent();
                    if (!inputStat)
                        return [2 /*return*/, undefined];
                    if (!inputStat.isDirectory() || inputStat.isSymbolicLink())
                        throw new Error('KIMI_CODE_HOME must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(configuredHome).catch(function () { throw new Error('KIMI_CODE_HOME must exist and be accessible'); })];
                case 2:
                    root = _a.sent();
                    return [3 /*break*/, 12];
                case 3: return [4 /*yield*/, (0, promises_1.realpath)(node_os_1.default.homedir()).catch(function () { throw new Error('user home must exist and be accessible'); })];
                case 4:
                    home = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(home)];
                case 5:
                    homeInfo = _a.sent();
                    if (!homeInfo.isDirectory() || homeInfo.isSymbolicLink())
                        throw new Error('user home must be a real directory');
                    defaultRoot = node_path_1.default.join(home, '.kimi-code');
                    if (!createDefaultHome) return [3 /*break*/, 9];
                    _a.label = 6;
                case 6:
                    _a.trys.push([6, 8, , 9]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(defaultRoot, { mode: 448 })];
                case 7:
                    _a.sent();
                    return [3 /*break*/, 9];
                case 8:
                    error_1 = _a.sent();
                    if (!isCode(error_1, 'EEXIST'))
                        throw new Error('unable to create Kimi Code home');
                    return [3 /*break*/, 9];
                case 9: return [4 /*yield*/, (0, promises_1.lstat)(defaultRoot).catch(function (error) {
                        if (!createDefaultHome && isCode(error, 'ENOENT'))
                            return null;
                        throw new Error('Kimi Code home is inaccessible');
                    })];
                case 10:
                    info_1 = _a.sent();
                    if (!info_1)
                        return [2 /*return*/, undefined];
                    if (!info_1.isDirectory() || info_1.isSymbolicLink())
                        throw new Error('Kimi Code home must be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(defaultRoot).catch(function () { throw new Error('Kimi Code home is inaccessible'); })];
                case 11:
                    root = _a.sent();
                    _a.label = 12;
                case 12: return [4 /*yield*/, (0, promises_1.lstat)(root)];
                case 13:
                    rootStat = _a.sent();
                    if (!rootStat.isDirectory() || rootStat.isSymbolicLink())
                        throw new Error('Kimi Code home must be a real directory');
                    config = node_path_1.default.join(root, 'config.toml');
                    return [4 /*yield*/, (0, promises_1.lstat)(config).catch(function (error) {
                            if (isCode(error, 'ENOENT'))
                                return null;
                            throw error;
                        })];
                case 14:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, createDefaultHome ? config : undefined];
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('Kimi Code config.toml must be a regular file');
                    if (info.size > maxConfigBytes)
                        throw new Error('Kimi Code config.toml exceeds the local size limit');
                    return [2 /*return*/, config];
            }
        });
    });
}
function withLock(configPath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lockPath, lock, error_2;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    lockPath = node_path_1.default.join(node_path_1.default.dirname(configPath), lockName);
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
                        throw new Error('Kimi Code hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock Kimi Code config');
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
function readConfig(configPath) {
    return __awaiter(this, void 0, void 0, function () {
        var info, bytes, text, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(configPath)];
                case 1:
                    info = _a.sent();
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('Kimi Code config.toml must be a regular file');
                    if (info.size > maxConfigBytes)
                        throw new Error('Kimi Code config.toml exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(configPath)];
                case 2:
                    bytes = _a.sent();
                    text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
                    return [2 /*return*/, { text: text, snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } } }];
                case 3:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'ENOENT'))
                        return [2 /*return*/, { text: '', snapshot: {} }];
                    if (error_3 instanceof Error && error_3.message.startsWith('Kimi Code config.toml'))
                        throw error_3;
                    throw new Error('Kimi Code config.toml is not valid UTF-8; no changes were made');
                case 4: return [2 /*return*/];
            }
        });
    });
}
function scanKimiToml(text) {
    var _a;
    var hooks = [];
    var currentTable = [];
    var currentArrayTable = false;
    var currentHook;
    var depth = 0;
    var inlineHooks = false;
    var strings = {};
    for (var _i = 0, _b = linesWithOffsets(text); _i < _b.length; _i++) {
        var line = _b[_i];
        var wasMultiline = Boolean(strings.multiline);
        var sourceLine = line.start === 0 ? line.text.replace(/^\uFEFF/, '') : line.text;
        var code = stripTomlComment(sourceLine, strings);
        if (wasMultiline || strings.multiline) {
            if (currentHook)
                currentHook.code += code;
            continue;
        }
        var trimmed = code.trim();
        if (!trimmed)
            continue;
        if (depth > 0) {
            if (currentHook)
                currentHook.code += code;
            depth = updateTomlValueDepth(code, depth);
            continue;
        }
        if (trimmed.startsWith('[')) {
            var header = parseTableHeader(trimmed);
            currentTable = header.path;
            currentArrayTable = header.array;
            currentHook = undefined;
            if (header.path[0] === 'hooks') {
                if (header.path.length === 1 && header.array) {
                    currentHook = { start: line.start, dataEnd: line.end, fields: new Map(), code: '' };
                    hooks.push(currentHook);
                }
                else {
                    inlineHooks = true;
                }
            }
            continue;
        }
        var equals = findUnquotedEquals(trimmed);
        if (equals < 0)
            throw new Error('Kimi Code config contains unsupported TOML syntax; no changes were made');
        var keyText = trimmed.slice(0, equals).trim();
        var valueText = trimmed.slice(equals + 1).trim();
        if (currentTable.length === 0) {
            var keys = parseKeyPath(keyText);
            if (keys[0] === 'hooks')
                inlineHooks = true;
        }
        if (currentHook && currentTable.length === 1 && currentTable[0] === 'hooks' && currentArrayTable) {
            var keys = parseKeyPath(keyText);
            if (keys.length !== 1)
                currentHook.invalid = 'dotted hook keys are unsupported';
            var key = (_a = keys[0]) !== null && _a !== void 0 ? _a : '';
            if (!['event', 'matcher', 'command', 'timeout'].includes(key))
                currentHook.invalid = "unsupported hook field ".concat(key);
            if (currentHook.fields.has(key))
                currentHook.invalid = "duplicate hook field ".concat(key);
            var value = parseHookFieldValue(key, valueText);
            if (value === undefined)
                currentHook.invalid = "unsupported value for hook field ".concat(key);
            else
                currentHook.fields.set(key, value);
            currentHook.code += code;
            currentHook.dataEnd = line.end;
        }
        depth = updateTomlValueDepth(valueText, 0);
    }
    if (strings.multiline)
        throw new Error('Kimi Code config has an unterminated TOML multiline string; no changes were made');
    if (depth !== 0)
        throw new Error('Kimi Code config has an unterminated TOML array or inline table; no changes were made');
    var _loop_1 = function (hook) {
        if (hook.invalid)
            throw new Error("Kimi Code hooks array table ".concat(hook.invalid, "; no changes were made"));
        var event_1 = hook.fields.get('event');
        var command = hook.fields.get('command');
        var matcher = hook.fields.get('matcher');
        var timeout = hook.fields.get('timeout');
        if (typeof event_1 !== 'string' || !event_1 || typeof command !== 'string' || !command ||
            matcher !== undefined && typeof matcher !== 'string' ||
            timeout !== undefined && (typeof timeout !== 'number' || !Number.isInteger(timeout) || timeout < 1 || timeout > 600)) {
            throw new Error('Kimi Code hooks array table is incomplete or malformed; no changes were made');
        }
        var markerAppears = hook.code.includes(kimi_code_hook_ts_1.kimiCodeManagedMarker);
        var managed = command.includes(kimi_code_hook_ts_1.kimiCodeManagedMarker);
        if (markerAppears && !managed)
            throw new Error('Kimi Code Threadforge marker appears in unsupported hook syntax; inspect it before retrying');
        hook.managed = managed;
        if (managed && !hookDefinitions.some(function (definition) { return definition.event === event_1; })) {
            throw new Error('Threadforge Kimi Code hook uses an unsupported event; inspect it before retrying');
        }
    };
    for (var _c = 0, hooks_1 = hooks; _c < hooks_1.length; _c++) {
        var hook = hooks_1[_c];
        _loop_1(hook);
    }
    return { hooks: hooks, inlineHooks: inlineHooks };
}
function parseHookFieldValue(key, raw) {
    var value = raw.trim();
    if (key === 'timeout') {
        if (!/^[+-]?(?:0|[1-9](?:_?[0-9])*)$/.test(value))
            return undefined;
        var number = Number(value.replaceAll('_', ''));
        return Number.isSafeInteger(number) ? number : undefined;
    }
    return parseTomlString(value);
}
function parseTomlString(value) {
    if (value.length < 2)
        return undefined;
    if (value.startsWith('"') && value.endsWith('"')) {
        try {
            var parsed = JSON.parse(value);
            return typeof parsed === 'string' ? parsed : undefined;
        }
        catch (_a) {
            return undefined;
        }
    }
    if (value.startsWith("'") && value.endsWith("'") && !value.slice(1, -1).includes("'"))
        return value.slice(1, -1);
    return undefined;
}
function appendHooks(content, command) {
    var eol = content.includes('\r\n') ? '\r\n' : '\n';
    var prefix = content;
    if (prefix && !prefix.endsWith('\n'))
        prefix += eol;
    var blocks = hookDefinitions.map(function (_a) {
        var event = _a.event, timeout = _a.timeout;
        return [
            '[[hooks]]',
            "event = ".concat(JSON.stringify(event)),
            "command = ".concat(JSON.stringify(command)),
            "timeout = ".concat(timeout),
        ].join(eol);
    });
    return "".concat(prefix).concat(blocks.join(eol)).concat(eol);
}
function hasExpectedHooks(entries, command) {
    if (entries.length !== hookDefinitions.length)
        return false;
    var actual = entries.map(function (entry) { return ({
        event: entry.fields.get('event'),
        command: entry.fields.get('command'),
        matcher: entry.fields.get('matcher'),
        timeout: entry.fields.get('timeout'),
    }); }).sort(function (a, b) { return String(a.event).localeCompare(String(b.event)); });
    var expected = __spreadArray([], hookDefinitions, true).sort(function (a, b) { return a.event.localeCompare(b.event); }).map(function (_a) {
        var event = _a.event, timeout = _a.timeout;
        return ({ event: event, command: command, matcher: undefined, timeout: timeout });
    });
    return actual.every(function (entry, index) {
        var _a, _b;
        return entry.event === ((_a = expected[index]) === null || _a === void 0 ? void 0 : _a.event) && entry.command === command &&
            entry.matcher === undefined && entry.timeout === ((_b = expected[index]) === null || _b === void 0 ? void 0 : _b.timeout);
    });
}
function removeHookTables(content, entries) {
    var ranges = entries.map(function (_a) {
        var start = _a.start, dataEnd = _a.dataEnd;
        return ({ start: start, end: dataEnd });
    }).sort(function (a, b) { return a.start - b.start; });
    var result = '';
    var offset = 0;
    for (var _i = 0, ranges_1 = ranges; _i < ranges_1.length; _i++) {
        var range = ranges_1[_i];
        if (range.start < offset || range.end < range.start)
            throw new Error('Kimi Code owned hook ranges overlap; no changes were made');
        result += content.slice(offset, range.start);
        offset = range.end;
    }
    return result + content.slice(offset);
}
function linesWithOffsets(text) {
    var start, newline, end, line;
    return __generator(this, function (_a) {
        switch (_a.label) {
            case 0:
                start = 0;
                _a.label = 1;
            case 1:
                if (!(start < text.length)) return [3 /*break*/, 4];
                newline = text.indexOf('\n', start);
                end = newline < 0 ? text.length : newline + 1;
                line = text.slice(start, newline < 0 ? text.length : newline);
                if (line.endsWith('\r'))
                    line = line.slice(0, -1);
                return [4 /*yield*/, { text: line, start: start, end: end }];
            case 2:
                _a.sent();
                start = end;
                _a.label = 3;
            case 3: return [3 /*break*/, 1];
            case 4: return [2 /*return*/];
        }
    });
}
function parseTableHeader(value) {
    var array = value.startsWith('[[');
    var opening = array ? 2 : 1;
    var closing = array ? ']]' : ']';
    if (!value.endsWith(closing))
        throw new Error('Kimi Code config has an unsupported TOML table header; no changes were made');
    var pathParts = parseKeyPath(value.slice(opening, -closing.length).trim());
    if (pathParts.length === 0)
        throw new Error('Kimi Code config has an empty TOML table header; no changes were made');
    return { path: pathParts, array: array };
}
function parseKeyPath(value) {
    var _a, _b;
    var keys = [];
    var index = 0;
    while (index < value.length) {
        while (/\s/.test((_a = value[index]) !== null && _a !== void 0 ? _a : ''))
            index++;
        if (index >= value.length)
            break;
        var quote = value[index];
        var key = void 0;
        if (quote === '"' || quote === "'") {
            var start = index++;
            var escaped = false;
            for (; index < value.length; index++) {
                var character = value[index];
                if (quote === '"' && character === '\\' && !escaped) {
                    escaped = true;
                    continue;
                }
                if (character === quote && !escaped)
                    break;
                escaped = false;
            }
            if (index >= value.length)
                throw new Error('Kimi Code config has an unterminated quoted TOML key; no changes were made');
            var raw = value.slice(start, index + 1);
            try {
                key = quote === '"' ? JSON.parse(raw) : raw.slice(1, -1);
            }
            catch (_c) {
                throw new Error('Kimi Code config has an unsupported quoted TOML key; no changes were made');
            }
            index++;
        }
        else {
            var start = index;
            while (index < value.length && !/[.\s]/.test(value[index]))
                index++;
            key = value.slice(start, index);
            if (!/^[A-Za-z0-9_-]+$/.test(key))
                throw new Error('Kimi Code config has an unsupported bare TOML key; no changes were made');
        }
        keys.push(key);
        while (/\s/.test((_b = value[index]) !== null && _b !== void 0 ? _b : ''))
            index++;
        if (index === value.length)
            break;
        if (value[index] !== '.')
            throw new Error('Kimi Code config has an unsupported dotted TOML key; no changes were made');
        index++;
    }
    return keys;
}
function findUnquotedEquals(value) {
    var quote = '';
    var escaped = false;
    for (var index = 0; index < value.length; index++) {
        var character = value[index];
        if (quote) {
            if (quote === '"' && character === '\\' && !escaped) {
                escaped = true;
                continue;
            }
            if (character === quote && !escaped)
                quote = '';
            escaped = false;
            continue;
        }
        if (character === '"' || character === "'") {
            quote = character;
            continue;
        }
        if (character === '=')
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
        var character = line[index];
        if (character === '#')
            break;
        if ((character === '"' || character === "'") && line.startsWith(character.repeat(3), index)) {
            var marker = character.repeat(3);
            var close_2 = findMultilineEnd(line, marker, index + 3);
            if (close_2 < 0) {
                state.multiline = marker;
                break;
            }
            output += '""';
            index = close_2 + 3;
            continue;
        }
        if (character === '"' || character === "'") {
            var start = index++;
            var escaped = false;
            for (; index < line.length; index++) {
                var current = line[index];
                if (character === '"' && current === '\\' && !escaped) {
                    escaped = true;
                    continue;
                }
                if (current === character && !escaped)
                    break;
                escaped = false;
            }
            if (index >= line.length)
                throw new Error('Kimi Code config has an unterminated TOML string; no changes were made');
            output += line.slice(start, index + 1);
            index++;
            continue;
        }
        output += character;
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
        var character = value[index];
        if (quote) {
            if (quote === '"' && character === '\\' && !escaped) {
                escaped = true;
                continue;
            }
            if (character === quote && !escaped)
                quote = '';
            escaped = false;
            continue;
        }
        if ((character === '"' || character === "'") && value.startsWith(character.repeat(3), index)) {
            var marker = character.repeat(3);
            var close_3 = findMultilineEnd(value, marker, index + 3);
            if (close_3 < 0)
                return depth;
            index = close_3 + 2;
            continue;
        }
        if (character === '"' || character === "'") {
            quote = character;
            continue;
        }
        if (character === '[' || character === '{')
            depth++;
        else if (character === ']' || character === '}')
            depth--;
        if (depth < 0)
            throw new Error('Kimi Code config has an invalid TOML array or inline table; no changes were made');
    }
    return depth;
}
function atomicWrite(config, snapshot, content) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, tempPath, mode, handle, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    bytes = Buffer.from(content, 'utf8');
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('Kimi Code config.toml would exceed the local size limit');
                    tempPath = "".concat(config, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    mode = snapshot.identity ? snapshot.identity.mode & 511 : 384;
                    _b.label = 1;
                case 1:
                    _b.trys.push([1, 10, , 13]);
                    return [4 /*yield*/, assertUnchanged(config, snapshot)];
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
                    return [4 /*yield*/, assertUnchanged(config, snapshot)];
                case 8:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.rename)(tempPath, config)];
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
                    throw new Error('unable to atomically update Kimi Code config.toml');
                case 13: return [2 /*return*/];
            }
        });
    });
}
function assertUnchanged(config, snapshot) {
    return __awaiter(this, void 0, void 0, function () {
        var info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(config).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw error;
                    })];
                case 1:
                    info = _a.sent();
                    if (!snapshot.identity) {
                        if (info)
                            throw new Error('Kimi Code config.toml appeared during update');
                        return [2 /*return*/];
                    }
                    if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
                        info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
                        throw new Error('Kimi Code config.toml changed during update');
                    }
                    return [2 /*return*/];
            }
        });
    });
}
function validateCommand(command) {
    if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
        throw new Error('Kimi Code hook command is invalid or too long');
    }
    if (!command.includes(kimi_code_hook_ts_1.kimiCodeManagedMarker))
        throw new Error("Kimi Code hook command must include ".concat(kimi_code_hook_ts_1.kimiCodeManagedMarker));
    if (looksSecretLike(command))
        throw new Error('Kimi Code hook command must not contain credential-like values');
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
