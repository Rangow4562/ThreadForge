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
exports.installGeminiTelemetry = installGeminiTelemetry;
exports.removeGeminiTelemetry = removeGeminiTelemetry;
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_crypto_1 = require("node:crypto");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var maxSettingsBytes = 8 << 20;
var stateSchema = 'threadforge.gemini-telemetry-state.v1';
var managedSettings = {
    enabled: true,
    target: 'local',
    otlpEndpoint: 'http://127.0.0.1:4318',
    otlpProtocol: 'http',
    useCollector: true,
    logPrompts: false,
    traces: false,
};
/** Installs only compatible local OTLP/HTTP settings, and refuses conflicting user telemetry config. */
function installGeminiTelemetry() {
    return __awaiter(this, arguments, void 0, function (options) {
        var paths;
        var _this = this;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, configPaths((_a = options.homeDir) !== null && _a !== void 0 ? _a : node_os_1.default.homedir(), true)];
                case 1:
                    paths = _b.sent();
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var settings, settingsExisted, telemetry, state, before, owned, _i, _a, _b, key, expected, changed, nextState, error_1;
                            var _c;
                            return __generator(this, function (_d) {
                                switch (_d.label) {
                                    case 0: return [4 /*yield*/, readJson(paths.settings, 'Gemini settings')];
                                    case 1:
                                        settings = _d.sent();
                                        return [4 /*yield*/, pathExists(paths.settings)];
                                    case 2:
                                        settingsExisted = _d.sent();
                                        telemetry = readTelemetry(settings);
                                        if (hasConfiguredOutfile(telemetry)) {
                                            throw new Error('Gemini telemetry.outfile overrides OTLP; remove or migrate it manually before installing Threadforge');
                                        }
                                        return [4 /*yield*/, readOwnership(paths.state)];
                                    case 3:
                                        state = _d.sent();
                                        before = JSON.stringify(settings);
                                        owned = new Set((_c = state === null || state === void 0 ? void 0 : state.owned_keys) !== null && _c !== void 0 ? _c : []);
                                        for (_i = 0, _a = Object.entries(managedSettings); _i < _a.length; _i++) {
                                            _b = _a[_i], key = _b[0], expected = _b[1];
                                            if (Object.hasOwn(telemetry, key)) {
                                                if (!deepEqual(telemetry[key], expected)) {
                                                    throw new Error("Gemini telemetry.".concat(key, " has a conflicting user value; no settings were changed"));
                                                }
                                                continue;
                                            }
                                            telemetry[key] = expected;
                                            owned.add(key);
                                        }
                                        settings.telemetry = telemetry;
                                        changed = before !== JSON.stringify(settings);
                                        nextState = { schema_version: stateSchema, owned_keys: __spreadArray([], owned, true).sort() };
                                        if (!changed && state && deepEqual(nextState, state))
                                            return [2 /*return*/, 'unchanged'];
                                        if (!!state) return [3 /*break*/, 13];
                                        return [4 /*yield*/, atomicWrite(paths.settings, settings)];
                                    case 4:
                                        _d.sent();
                                        _d.label = 5;
                                    case 5:
                                        _d.trys.push([5, 7, , 12]);
                                        return [4 /*yield*/, atomicWrite(paths.state, nextState)];
                                    case 6:
                                        _d.sent();
                                        return [3 /*break*/, 12];
                                    case 7:
                                        error_1 = _d.sent();
                                        if (!settingsExisted) return [3 /*break*/, 9];
                                        return [4 /*yield*/, atomicWrite(paths.settings, JSON.parse(before)).catch(function () { })];
                                    case 8:
                                        _d.sent();
                                        return [3 /*break*/, 11];
                                    case 9: return [4 /*yield*/, (0, promises_1.unlink)(paths.settings).catch(function () { })];
                                    case 10:
                                        _d.sent();
                                        _d.label = 11;
                                    case 11: throw error_1;
                                    case 12: return [2 /*return*/, 'installed'];
                                    case 13: return [4 /*yield*/, atomicWrite(paths.settings, settings)];
                                    case 14:
                                        _d.sent();
                                        if (!!deepEqual(nextState, state)) return [3 /*break*/, 16];
                                        return [4 /*yield*/, atomicWrite(paths.state, nextState)];
                                    case 15:
                                        _d.sent();
                                        _d.label = 16;
                                    case 16: return [2 /*return*/, 'updated'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function removeGeminiTelemetry() {
    return __awaiter(this, arguments, void 0, function (options) {
        var paths;
        var _this = this;
        var _a;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, configPaths((_a = options.homeDir) !== null && _a !== void 0 ? _a : node_os_1.default.homedir(), false)];
                case 1:
                    paths = _b.sent();
                    if (!paths)
                        return [2 /*return*/, 'absent'];
                    return [2 /*return*/, withLock(paths.lock, function () { return __awaiter(_this, void 0, void 0, function () {
                            var state, settings, telemetry, changed, _i, _a, key;
                            return __generator(this, function (_b) {
                                switch (_b.label) {
                                    case 0: return [4 /*yield*/, readOwnership(paths.state)];
                                    case 1:
                                        state = _b.sent();
                                        if (!state)
                                            return [2 /*return*/, 'absent'];
                                        return [4 /*yield*/, readJson(paths.settings, 'Gemini settings')];
                                    case 2:
                                        settings = _b.sent();
                                        telemetry = readTelemetry(settings);
                                        changed = false;
                                        for (_i = 0, _a = state.owned_keys; _i < _a.length; _i++) {
                                            key = _a[_i];
                                            if (Object.hasOwn(telemetry, key) && deepEqual(telemetry[key], managedSettings[key])) {
                                                delete telemetry[key];
                                                changed = true;
                                            }
                                        }
                                        if (!changed) return [3 /*break*/, 4];
                                        if (Object.keys(telemetry).length === 0)
                                            delete settings.telemetry;
                                        else
                                            settings.telemetry = telemetry;
                                        return [4 /*yield*/, atomicWrite(paths.settings, settings)];
                                    case 3:
                                        _b.sent();
                                        _b.label = 4;
                                    case 4: return [4 /*yield*/, (0, promises_1.unlink)(paths.state)];
                                    case 5:
                                        _b.sent();
                                        return [2 /*return*/, 'removed'];
                                }
                            });
                        }); })];
            }
        });
    });
}
function configPaths(homeDir, create) {
    return __awaiter(this, void 0, void 0, function () {
        var home, geminiDir, error_2, dirStat;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(homeDir))
                        throw new Error('Gemini home directory must be absolute');
                    return [4 /*yield*/, (0, promises_1.realpath)(homeDir).catch(function () { throw new Error('Gemini home directory must exist and be accessible'); })];
                case 1:
                    home = _a.sent();
                    geminiDir = node_path_1.default.join(home, '.gemini');
                    if (!create) return [3 /*break*/, 5];
                    _a.label = 2;
                case 2:
                    _a.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(geminiDir, { mode: 448 })];
                case 3:
                    _a.sent();
                    return [3 /*break*/, 5];
                case 4:
                    error_2 = _a.sent();
                    if (!isCode(error_2, 'EEXIST'))
                        throw new Error('unable to create Gemini settings directory');
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.lstat)(geminiDir).catch(function () { return null; })];
                case 6:
                    dirStat = _a.sent();
                    if (!dirStat)
                        return [2 /*return*/, undefined];
                    if (!dirStat.isDirectory() || dirStat.isSymbolicLink())
                        throw new Error('Gemini settings directory must be a real directory, not a symbolic link');
                    return [2 /*return*/, {
                            settings: node_path_1.default.join(geminiDir, 'settings.json'),
                            state: node_path_1.default.join(geminiDir, '.threadforge-telemetry.json'),
                            lock: node_path_1.default.join(geminiDir, '.threadforge-telemetry.lock'),
                        }];
            }
        });
    });
}
function withLock(lockPath, action) {
    return __awaiter(this, void 0, void 0, function () {
        var lock, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 2, , 3]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 1:
                    lock = _a.sent();
                    return [3 /*break*/, 3];
                case 2:
                    error_3 = _a.sent();
                    if (isCode(error_3, 'EEXIST'))
                        throw new Error('Gemini telemetry settings are locked by another writer or a stale lock');
                    throw new Error('unable to lock Gemini telemetry settings');
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
function readJson(filePath, label) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, value, _a, _b, error_4;
        return __generator(this, function (_c) {
            switch (_c.label) {
                case 0:
                    _c.trys.push([0, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    stat = _c.sent();
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error("".concat(label, " must be a regular file"));
                    if (stat.size > maxSettingsBytes)
                        throw new Error("".concat(label, " exceeds the local size limit"));
                    _b = (_a = JSON).parse;
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath, 'utf8')];
                case 2:
                    value = _b.apply(_a, [(_c.sent()).replace(/^\uFEFF/, '')]);
                    if (!isRecord(value))
                        throw new Error("".concat(label, " root must be an object"));
                    return [2 /*return*/, value];
                case 3:
                    error_4 = _c.sent();
                    if (isCode(error_4, 'ENOENT'))
                        return [2 /*return*/, {}];
                    if (error_4 instanceof Error && error_4.message.startsWith(label))
                        throw error_4;
                    throw new Error("".concat(label, " is invalid strict JSON; no changes were made"));
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readOwnership(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var stat, value;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) {
                        if (isCode(error, 'ENOENT'))
                            return null;
                        throw new Error('unable to inspect Threadforge Gemini ownership state');
                    })];
                case 1:
                    stat = _a.sent();
                    if (!stat)
                        return [2 /*return*/, undefined];
                    if (!stat.isFile() || stat.isSymbolicLink())
                        throw new Error('Threadforge Gemini ownership state must be a regular file');
                    return [4 /*yield*/, readJson(filePath, 'Threadforge Gemini ownership state')];
                case 2:
                    value = _a.sent();
                    if (value.schema_version !== stateSchema || !Array.isArray(value.owned_keys) ||
                        value.owned_keys.some(function (key) { return typeof key !== 'string' || !Object.hasOwn(managedSettings, key); }) ||
                        new Set(value.owned_keys).size !== value.owned_keys.length) {
                        throw new Error('Threadforge Gemini ownership state is invalid; inspect it before retrying');
                    }
                    return [2 /*return*/, value];
            }
        });
    });
}
function pathExists(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var error_5;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 2, , 3]);
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath)];
                case 1:
                    _a.sent();
                    return [2 /*return*/, true];
                case 2:
                    error_5 = _a.sent();
                    if (isCode(error_5, 'ENOENT'))
                        return [2 /*return*/, false];
                    throw error_5;
                case 3: return [2 /*return*/];
            }
        });
    });
}
function readTelemetry(settings) {
    if (settings.telemetry === undefined || settings.telemetry === null)
        return {};
    if (!isRecord(settings.telemetry))
        throw new Error('Gemini settings telemetry must be an object; no changes were made');
    return settings.telemetry;
}
function hasConfiguredOutfile(telemetry) {
    return typeof telemetry.outfile === 'string' && telemetry.outfile.trim() !== '';
}
function atomicWrite(filePath, value) {
    return __awaiter(this, void 0, void 0, function () {
        var tempPath, handle, mode, existing, error_6, _a;
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
                        throw new Error('Gemini settings target must be a regular file');
                    mode = existing.mode & 511;
                    return [3 /*break*/, 5];
                case 4:
                    error_6 = _b.sent();
                    if (!isCode(error_6, 'ENOENT'))
                        throw error_6;
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
                    throw new Error('unable to atomically update Gemini telemetry config');
                case 14: return [2 /*return*/];
            }
        });
    });
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
