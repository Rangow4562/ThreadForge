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
exports.openHandsHookEvents = exports.openHandsManagedMarker = void 0;
exports.installOpenHandsProjectHooks = installOpenHandsProjectHooks;
exports.removeOpenHandsProjectHooks = removeOpenHandsProjectHooks;
var node_fs_1 = require("node:fs");
var node_crypto_1 = require("node:crypto");
var node_url_1 = require("node:url");
var promises_1 = require("node:fs/promises");
var node_path_1 = require("node:path");
exports.openHandsManagedMarker = '--threadforge-openhands-hook=v1';
exports.openHandsHookEvents = ['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end'];
var maxConfigBytes = 8 << 20;
/** Merge asynchronous, decision-free command hooks into a project-owned OpenHands hooks.json. */
function installOpenHandsProjectHooks(projectRoot_1) {
    return __awaiter(this, arguments, void 0, function (projectRoot, options) {
        var commands, _i, openHandsHookEvents_1, event_1;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_a) {
            commands = new Map();
            for (_i = 0, openHandsHookEvents_1 = exports.openHandsHookEvents; _i < openHandsHookEvents_1.length; _i++) {
                event_1 = openHandsHookEvents_1[_i];
                commands.set(event_1, commandFor(event_1, options.commandTemplate));
            }
            return [2 /*return*/, mutateOpenHandsConfig(projectRoot, true, function (root) {
                    var _a;
                    var located = locateHookMap(root);
                    var cleaned = removeManagedReferences(located.map, located.keys);
                    var nextMap = __assign({}, cleaned.map);
                    var nextKeys = new Map(located.keys);
                    for (var _i = 0, openHandsHookEvents_2 = exports.openHandsHookEvents; _i < openHandsHookEvents_2.length; _i++) {
                        var event_2 = openHandsHookEvents_2[_i];
                        var key = (_a = nextKeys.get(event_2)) !== null && _a !== void 0 ? _a : event_2;
                        var current = readGroups(nextMap[key], key);
                        nextMap[key] = __spreadArray(__spreadArray([], current, true), [{ matcher: '*', hooks: [{ type: 'command', command: commands.get(event_2), async: true, timeout: 10 }] }], false);
                        nextKeys.set(event_2, key);
                    }
                    var nextRoot = applyHookMap(root, nextMap, located.wrapped);
                    var changed = !deepEqual(root, nextRoot);
                    if (changed)
                        replaceObject(root, nextRoot);
                    return { result: !changed ? 'unchanged' : cleaned.found ? 'updated' : 'installed', changed: changed };
                })];
        });
    });
}
/** Remove only hook commands containing Threadforge's ownership marker. */
function removeOpenHandsProjectHooks(projectRoot) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            return [2 /*return*/, mutateOpenHandsConfig(projectRoot, false, function (root) {
                    var located = locateHookMap(root);
                    var cleaned = removeManagedReferences(located.map, located.keys);
                    if (!cleaned.found)
                        return { result: 'absent', changed: false };
                    var nextRoot = applyHookMap(root, cleaned.map, located.wrapped);
                    var empty = Object.keys(nextRoot).length === 0;
                    if (empty)
                        return { result: 'removed', changed: true, deleteFile: true };
                    replaceObject(root, nextRoot);
                    return { result: 'removed', changed: true };
                })];
        });
    });
}
function commandFor(event, template) {
    var command;
    if (template !== undefined) {
        validateCommandTemplate(template);
        command = template.replace('{event}', event);
    }
    else {
        var cliPath = (0, node_url_1.fileURLToPath)(new URL('./cli.ts', import.meta.url));
        command = "".concat(quoteShellArgument(process.execPath), " --experimental-strip-types ").concat(quoteShellArgument(cliPath), " openhands hook ").concat(event, " ").concat(exports.openHandsManagedMarker);
    }
    if (command.length > 8192 || /[\r\n\0]/.test(command) || !command.includes(exports.openHandsManagedMarker) || looksCredentialLike(command)) {
        throw new Error('OpenHands hook command is invalid or too long');
    }
    return command;
}
function validateCommandTemplate(template) {
    if (typeof template !== 'string' || template.trim() === '' || template.length > 8000 || /[\r\n\0]/.test(template) ||
        template.split('{event}').length !== 2 || !template.includes(exports.openHandsManagedMarker) || looksCredentialLike(template)) {
        throw new Error("--command-template must contain exactly one {event} placeholder and ".concat(exports.openHandsManagedMarker));
    }
}
function quoteShellArgument(value) {
    if (process.platform !== 'win32')
        return "'".concat(value.replaceAll("'", "'\\''"), "'");
    var result = '"';
    var slashes = 0;
    for (var _i = 0, value_1 = value; _i < value_1.length; _i++) {
        var character = value_1[_i];
        if (character === '\\') {
            slashes++;
            continue;
        }
        if (character === '"') {
            result += '\\'.repeat(slashes * 2 + 1) + '"';
            slashes = 0;
            continue;
        }
        result += '\\'.repeat(slashes) + character;
        slashes = 0;
    }
    return result + '\\'.repeat(slashes * 2) + '"';
}
function locateHookMap(root) {
    var wrapped = Object.hasOwn(root, 'hooks');
    if (wrapped && Object.keys(root).some(function (key) { return key !== 'hooks'; })) {
        throw new Error('OpenHands hooks wrapper contains unsupported top-level fields; no changes were made');
    }
    var map = wrapped ? root.hooks : root;
    if (!isRecord(map))
        throw new Error('OpenHands hooks must be an object; no changes were made');
    var keys = new Map();
    for (var _i = 0, _a = Object.keys(map); _i < _a.length; _i++) {
        var key = _a[_i];
        var normalized = normalizeEvent(key);
        if (!normalized)
            throw new Error("OpenHands hooks.json contains unsupported event ".concat(key, "; no changes were made"));
        if (keys.has(normalized))
            throw new Error("OpenHands hooks.json declares ".concat(normalized, " more than once; no changes were made"));
        keys.set(normalized, key);
        if (map[key] !== undefined && !Array.isArray(map[key]))
            throw new Error("OpenHands hooks.".concat(key, " must be an array; no changes were made"));
    }
    return { map: map, wrapped: wrapped, keys: keys };
}
function normalizeEvent(value) {
    var normalized = value.replace(/[^A-Za-z0-9]/g, '').toLowerCase();
    return exports.openHandsHookEvents.find(function (event) { return event.replaceAll('_', '') === normalized; });
}
function readGroups(value, key) {
    if (value === undefined || value === null)
        return [];
    if (!Array.isArray(value))
        throw new Error("OpenHands hooks.".concat(key, " must be an array; no changes were made"));
    return value;
}
function removeManagedReferences(map, keys) {
    var result = __assign({}, map);
    var found = false;
    var _loop_1 = function (event_3, key) {
        var groups = readGroups(map[key], key);
        var retainedGroups = [];
        for (var _b = 0, groups_1 = groups; _b < groups_1.length; _b++) {
            var rawGroup = groups_1[_b];
            if (!isRecord(rawGroup) || !Array.isArray(rawGroup.hooks)) {
                throw new Error("OpenHands hooks.".concat(key, " contains an unsupported hook group; no changes were made"));
            }
            var refs = rawGroup.hooks.filter(function (rawRef) {
                if (!isRecord(rawRef))
                    throw new Error("OpenHands hooks.".concat(key, " contains an unsupported hook; no changes were made"));
                var managed = typeof rawRef.command === 'string' && rawRef.command.includes(exports.openHandsManagedMarker);
                if (managed)
                    found = true;
                return !managed;
            });
            if (refs.length === rawGroup.hooks.length)
                retainedGroups.push(rawGroup);
            else if (refs.length > 0)
                retainedGroups.push(__assign(__assign({}, rawGroup), { hooks: refs }));
        }
        if (retainedGroups.length === 0)
            delete result[key];
        else if (retainedGroups.length !== groups.length || retainedGroups.some(function (group, index) { return group !== groups[index]; }))
            result[key] = retainedGroups;
    };
    for (var _i = 0, keys_1 = keys; _i < keys_1.length; _i++) {
        var _a = keys_1[_i], event_3 = _a[0], key = _a[1];
        _loop_1(event_3, key);
    }
    return { map: result, found: found };
}
function applyHookMap(root, map, wrapped) {
    return wrapped ? __assign(__assign({}, root), { hooks: map }) : __assign({}, map);
}
function replaceObject(target, source) {
    for (var _i = 0, _a = Object.keys(target); _i < _a.length; _i++) {
        var key = _a[_i];
        delete target[key];
    }
    Object.assign(target, source);
}
function mutateOpenHandsConfig(projectRoot, create, mutate) {
    return __awaiter(this, void 0, void 0, function () {
        var resolved, rootInfo, canonical, configDir, dirInfo, filePath, lockPath, lock, error_1, root, before, outcome;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(projectRoot))
                        throw new Error('--project-root must be an absolute path');
                    resolved = node_path_1.default.resolve(projectRoot);
                    return [4 /*yield*/, (0, promises_1.lstat)(resolved).catch(function () { return null; })];
                case 1:
                    rootInfo = _a.sent();
                    if (!rootInfo || !rootInfo.isDirectory() || rootInfo.isSymbolicLink())
                        throw new Error('project root must exist and be a real directory');
                    return [4 /*yield*/, (0, promises_1.realpath)(resolved).catch(function () { return ''; })];
                case 2:
                    canonical = _a.sent();
                    if (!canonical || !samePath(canonical, resolved))
                        throw new Error('project root must not traverse symbolic links');
                    configDir = node_path_1.default.join(canonical, '.openhands');
                    return [4 /*yield*/, (0, promises_1.lstat)(configDir).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 3:
                    dirInfo = _a.sent();
                    if (!(!dirInfo && create)) return [3 /*break*/, 6];
                    return [4 /*yield*/, (0, promises_1.mkdir)(configDir).catch(function (error) { if (!isCode(error, 'EEXIST'))
                            throw new Error('unable to create OpenHands project config directory'); })];
                case 4:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(configDir).catch(function () { return null; })];
                case 5:
                    dirInfo = _a.sent();
                    _a.label = 6;
                case 6:
                    if (!dirInfo)
                        return [2 /*return*/, 'absent'];
                    if (!dirInfo.isDirectory() || dirInfo.isSymbolicLink())
                        throw new Error('OpenHands project config path contains a symbolic link or non-directory');
                    filePath = node_path_1.default.join(configDir, 'hooks.json');
                    lockPath = node_path_1.default.join(configDir, '.threadforge-hooks.lock');
                    _a.label = 7;
                case 7:
                    _a.trys.push([7, 9, , 10]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 8:
                    lock = _a.sent();
                    return [3 /*break*/, 10];
                case 9:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'EEXIST'))
                        throw new Error('OpenHands hooks are locked by another writer or a stale lock');
                    throw new Error('unable to lock OpenHands hooks');
                case 10:
                    _a.trys.push([10, , 16, 19]);
                    return [4 /*yield*/, readConfig(filePath)];
                case 11:
                    root = _a.sent();
                    before = JSON.stringify(root);
                    outcome = mutate(root);
                    if (!outcome.changed)
                        return [2 /*return*/, outcome.result];
                    if (!outcome.deleteFile) return [3 /*break*/, 13];
                    return [4 /*yield*/, (0, promises_1.unlink)(filePath).catch(function (error) { if (!isCode(error, 'ENOENT'))
                            throw new Error('unable to remove empty OpenHands hooks file'); })];
                case 12:
                    _a.sent();
                    return [3 /*break*/, 15];
                case 13:
                    if (!(before !== JSON.stringify(root))) return [3 /*break*/, 15];
                    return [4 /*yield*/, writeConfig(filePath, root)];
                case 14:
                    _a.sent();
                    _a.label = 15;
                case 15: return [2 /*return*/, outcome.result];
                case 16: return [4 /*yield*/, lock.close().catch(function () { })];
                case 17:
                    _a.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                case 18:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 19: return [2 /*return*/];
            }
        });
    });
}
function readConfig(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var info, handle, opened, bytes, value, error_2;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _b.sent();
                    if (!info)
                        return [2 /*return*/, {}];
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('OpenHands hooks.json must be a regular file');
                    if (info.size > maxConfigBytes)
                        throw new Error('OpenHands hooks.json exceeds the local size limit');
                    _b.label = 2;
                case 2:
                    _b.trys.push([2, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_RDONLY | ((_a = node_fs_1.constants.O_NOFOLLOW) !== null && _a !== void 0 ? _a : 0))];
                case 3:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.stat()];
                case 4:
                    opened = _b.sent();
                    if (!sameFile(info, opened))
                        throw new Error('OpenHands hooks.json changed before read');
                    return [4 /*yield*/, handle.readFile()];
                case 5:
                    bytes = _b.sent();
                    if (bytes.byteLength > maxConfigBytes)
                        throw new Error('OpenHands hooks.json exceeds the local size limit');
                    value = JSON.parse(bytes.toString('utf8').replace(/^\uFEFF/, ''));
                    if (!isRecord(value))
                        throw new Error('OpenHands hooks.json root must be an object');
                    return [2 /*return*/, value];
                case 6:
                    error_2 = _b.sent();
                    if (error_2 instanceof Error && error_2.message.startsWith('OpenHands hooks.json'))
                        throw error_2;
                    throw new Error('OpenHands hooks.json is invalid strict JSON; no changes were made');
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 8:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function writeConfig(filePath, value) {
    return __awaiter(this, void 0, void 0, function () {
        var tempPath, serialized, handle, mode, error_3, _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    tempPath = "".concat(filePath, ".threadforge-").concat((0, node_crypto_1.randomUUID)(), ".tmp");
                    serialized = "".concat(JSON.stringify(value, null, 2), "\n");
                    if (Buffer.byteLength(serialized, 'utf8') > maxConfigBytes)
                        throw new Error('OpenHands hooks.json exceeds the local size limit after update');
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
                    error_3 = _b.sent();
                    if (!isCode(error_3, 'ENOENT'))
                        throw error_3;
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.open)(tempPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
                case 6:
                    handle = _b.sent();
                    return [4 /*yield*/, handle.writeFile(serialized, 'utf8')];
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
                    throw new Error('unable to atomically update OpenHands hooks.json');
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
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
function sameFile(left, right) { return left.dev === right.dev && left.ino === right.ino; }
function samePath(left, right) { return process.platform === 'win32' ? left.toLowerCase() === right.toLowerCase() : left === right; }
function looksCredentialLike(value) {
    return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
