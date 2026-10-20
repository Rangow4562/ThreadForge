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
exports.buildOpenClawPluginCommands = buildOpenClawPluginCommands;
exports.runOpenClawPluginAction = runOpenClawPluginAction;
exports.openClawPluginPackagePath = openClawPluginPackagePath;
var node_child_process_1 = require("node:child_process");
var node_url_1 = require("node:url");
var pluginId = 'threadforge-observability';
var packageDirectory = (0, node_url_1.fileURLToPath)(new URL('../openclaw-plugin/', import.meta.url));
/** Build the exact OpenClaw CLI operations without editing OpenClaw's JSON5 configuration. */
function buildOpenClawPluginCommands(action, options) {
    var _a;
    validateOptions(action, options);
    var executable = ((_a = options.openclawCli) === null || _a === void 0 ? void 0 : _a.trim()) || 'openclaw';
    if (/[\0\r\n]/.test(executable))
        throw new Error('OpenClaw CLI path contains invalid characters');
    if (action === 'install') {
        return [
            {
                executable: executable,
                args: __spreadArray(__spreadArray(['plugins', 'install', '--link'], (options.acknowledgeLocalSource ? ['--force'] : []), true), [packageDirectory], false),
            },
            { executable: executable, args: ['plugins', 'enable', pluginId] },
        ];
    }
    if (action === 'update')
        return [{ executable: executable, args: ['plugins', 'reload', pluginId] }];
    return [{ executable: executable, args: ['plugins', 'uninstall', pluginId, '--keep-files'] }];
}
/** Executes OpenClaw's own scoped plugin-management commands and preserves its confirmation flow. */
function runOpenClawPluginAction(action_1, options_1) {
    return __awaiter(this, arguments, void 0, function (action, options, runner) {
        var commands, index, error_1, detail;
        if (runner === void 0) { runner = runCommand; }
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    commands = buildOpenClawPluginCommands(action, options);
                    index = 0;
                    _a.label = 1;
                case 1:
                    if (!(index < commands.length)) return [3 /*break*/, 6];
                    _a.label = 2;
                case 2:
                    _a.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, runner(commands[index])];
                case 3:
                    _a.sent();
                    return [3 /*break*/, 5];
                case 4:
                    error_1 = _a.sent();
                    if (action === 'install' && index > 0) {
                        throw new Error("OpenClaw installed the Threadforge plugin but could not enable it; run \"openclaw plugins enable ".concat(pluginId, "\" after resolving the CLI error"));
                    }
                    detail = error_1 instanceof Error ? error_1.message : 'unknown OpenClaw CLI error';
                    throw new Error("OpenClaw plugin ".concat(action, " failed: ").concat(detail));
                case 5:
                    index++;
                    return [3 /*break*/, 1];
                case 6:
                    if (action === 'install')
                        return [2 /*return*/, 'installed'];
                    if (action === 'update')
                        return [2 /*return*/, 'reloaded'];
                    return [2 /*return*/, 'uninstalled'];
            }
        });
    });
}
function openClawPluginPackagePath() {
    return packageDirectory;
}
function validateOptions(action, options) {
    if (!['install', 'update', 'uninstall'].includes(action))
        throw new Error('unsupported OpenClaw plugin action');
    if (!options || options.scope !== 'active-gateway')
        throw new Error('OpenClaw requires the explicit --scope active-gateway choice');
    if (action !== 'install' && options.acknowledgeLocalSource) {
        throw new Error('--acknowledge-local-source is valid only with OpenClaw install');
    }
}
function runCommand(command) {
    return __awaiter(this, void 0, void 0, function () {
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (!(process.platform === 'win32')) return [3 /*break*/, 2];
                    return [4 /*yield*/, runPowerShell(command)];
                case 1:
                    _a.sent();
                    return [2 /*return*/];
                case 2: return [4 /*yield*/, new Promise(function (resolve, reject) {
                        var child = (0, node_child_process_1.spawn)(command.executable, command.args, { stdio: 'inherit', windowsHide: true });
                        child.once('error', function (error) { return reject(new Error("could not start ".concat(command.executable, ": ").concat(error.message))); });
                        child.once('close', function (code, signal) {
                            if (code === 0)
                                resolve();
                            else
                                reject(new Error("".concat(command.executable, " exited ").concat(signal ? "from ".concat(signal) : "with status ".concat(code))));
                        });
                    })];
                case 3:
                    _a.sent();
                    return [2 /*return*/];
            }
        });
    });
}
function runPowerShell(command) {
    return __awaiter(this, void 0, void 0, function () {
        var quote, invocation, script, encoded;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    quote = function (value) {
                        if (/[\0\r\n]/.test(value))
                            throw new Error('OpenClaw CLI argument contains invalid characters');
                        return "'".concat(value.replace(/'/g, "''"), "'");
                    };
                    invocation = __spreadArray([command.executable], command.args, true).map(quote).join(' ');
                    script = "& ".concat(invocation, "\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit");
                    encoded = Buffer.from(script, 'utf16le').toString('base64');
                    return [4 /*yield*/, new Promise(function (resolve, reject) {
                            var child = (0, node_child_process_1.spawn)('powershell.exe', ['-NoProfile', '-EncodedCommand', encoded], { stdio: 'inherit', windowsHide: true });
                            child.once('error', function (error) { return reject(new Error("could not start PowerShell for OpenClaw CLI: ".concat(error.message))); });
                            child.once('close', function (code, signal) {
                                if (code === 0)
                                    resolve();
                                else
                                    reject(new Error("OpenClaw CLI exited ".concat(signal ? "from ".concat(signal) : "with status ".concat(code))));
                            });
                        })];
                case 1:
                    _a.sent();
                    return [2 /*return*/];
            }
        });
    });
}
