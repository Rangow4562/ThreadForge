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
exports.runAdapterCLI = runAdapterCLI;
var node_path_1 = require("node:path");
var promises_1 = require("node:fs/promises");
var node_url_1 = require("node:url");
var claude_hooks_config_ts_1 = require("./claude-hooks-config.ts");
var opencode_plugin_config_ts_1 = require("./opencode-plugin-config.ts");
var cursor_hooks_config_ts_1 = require("./cursor-hooks-config.ts");
var codex_hooks_config_ts_1 = require("./codex-hooks-config.ts");
var antigravity_hooks_config_ts_1 = require("./antigravity-hooks-config.ts");
var qwen_hooks_config_ts_1 = require("./qwen-hooks-config.ts");
var kimi_code_hooks_config_ts_1 = require("./kimi-code-hooks-config.ts");
var kiro_hooks_config_ts_1 = require("./kiro-hooks-config.ts");
var muse_hooks_config_ts_1 = require("./muse-hooks-config.ts");
var omp_hooks_config_ts_1 = require("./omp-hooks-config.ts");
var openclaw_plugin_install_ts_1 = require("./openclaw-plugin-install.ts");
var openclaw_otel_ts_1 = require("./openclaw-otel.ts");
var gemini_telemetry_config_ts_1 = require("./gemini-telemetry-config.ts");
var claude_telemetry_config_ts_1 = require("./claude-telemetry-config.ts");
var codex_telemetry_config_ts_1 = require("./codex-telemetry-config.ts");
var codex_rollouts_ts_1 = require("./codex-rollouts.ts");
var openclaw_history_ts_1 = require("./openclaw-history.ts");
var opencode_history_ts_1 = require("./opencode-history.ts");
var claude_history_ts_1 = require("./claude-history.ts");
var copilot_history_ts_1 = require("./copilot-history.ts");
var cursor_history_ts_1 = require("./cursor-history.ts");
var factory_history_ts_1 = require("./factory-history.ts");
var fx_history_ts_1 = require("./fx-history.ts");
var pi_history_ts_1 = require("./pi-history.ts");
var prime_history_ts_1 = require("./prime-history.ts");
var openhands_hooks_config_ts_1 = require("./openhands-hooks-config.ts");
var openhands_hook_ts_1 = require("./openhands-hook.ts");
var hermes_hook_ts_1 = require("./hermes-hook.ts");
var deepseek_hook_ts_1 = require("./deepseek-hook.ts");
var goose_otel_ts_1 = require("./goose-otel.ts");
var vscode_otel_ts_1 = require("./vscode-otel.ts");
var claude_cowork_otel_ts_1 = require("./claude-cowork-otel.ts");
var cline_skill_config_ts_1 = require("./cline-skill-config.ts");
var codex_mcp_config_ts_1 = require("./codex-mcp-config.ts");
var opencode_mcp_config_ts_1 = require("./opencode-mcp-config.ts");
var mcp_config_ts_1 = require("./mcp-config.ts");
function runAdapterCLI(args, stdout, stderr) {
    return __awaiter(this, void 0, void 0, function () {
        var family, action, rest, hermesSubsystem, openClawSubsystem, gooseSubsystem, vscodeSubsystem, coworkSubsystem, openCodeSubsystem, claudeSubsystem, copilotSubsystem, cursorSubsystem, factorySubsystem, fxSubsystem, piSubsystem, primeSubsystem, clineSubsystem, eventName, marker, unexpected, eventName, marker, unexpected, eventType, marker, unexpected, options, providedMuseScope, providedOmpScope, providedOpenClawScope, isOpenClawPluginAction, antigravityScope, qwenScope, kiroScope, museScope, ompScope, projectRoot, result, warning, skillOptions, _a, status_1, sinceMs, validation, status_2, sinceMs, validation, status_3, sinceMs, validation, _b, _c, _d, _e, _f, syncResult, _g, _h, _j, _k, _l, syncResult, status_4, syncResult, status_5, syncResult, status_6, syncResult, status_7, syncResult, status_8, syncResult, status_9, syncResult, status_10, syncResult, configOptions, _m, configOptions, _o, configOptions, _p, configOptions, _q, pluginAction, pluginOptions, commands, status_11, syncResult, host, host, command, hostName, name_1, _r, hostName, command, args_1, cwd, name_2, _s, _t, host, server, _u, error_1, message;
        var _v, _w, _x, _y, _z, _0, _1, _2;
        return __generator(this, function (_3) {
            switch (_3.label) {
                case 0:
                    family = args[0];
                    if (family === 'prime-agent')
                        family = 'prime';
                    if (family === 'open-hands')
                        family = 'openhands';
                    if (family === 'deepseek-harness' || family === 'deepseek_harness')
                        family = 'dsh';
                    if (family === 'claude-cowork')
                        family = 'cowork';
                    action = args[1];
                    rest = args.slice(2);
                    if (!family || !action) {
                        stderr.write("".concat(usage(), "\n"));
                        return [2 /*return*/, 2];
                    }
                    if (family === 'cline' && action === 'skills') {
                        clineSubsystem = 'skills';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'cowork' && action === 'otel') {
                        coworkSubsystem = 'otel';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'vscode' && action === 'otel') {
                        vscodeSubsystem = 'otel';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'goose' && action === 'otel') {
                        gooseSubsystem = 'otel';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (!(family === 'dsh' && action === 'hook')) return [3 /*break*/, 2];
                    eventName = rest[0], marker = rest[1], unexpected = rest.slice(2);
                    if (!deepseek_hook_ts_1.deepseekHookEvents.includes(eventName) ||
                        marker !== deepseek_hook_ts_1.deepseekManagedMarker || unexpected.length > 0) {
                        stderr.write('DeepSeek Harness hook invocation is invalid or unowned\n');
                        return [2 /*return*/, 2];
                    }
                    return [4 /*yield*/, (0, deepseek_hook_ts_1.runDeepSeekHook)(eventName, process.stdin, stdout, stderr, {
                            endpoint: process.env.THREADFORGE_EVENT_ENDPOINT,
                            timeoutMs: 250,
                        })];
                case 1:
                    _3.sent();
                    return [2 /*return*/, 0];
                case 2:
                    if (!(family === 'hermes' && action === 'hook')) return [3 /*break*/, 4];
                    eventName = rest[0], marker = rest[1], unexpected = rest.slice(2);
                    if (!hermes_hook_ts_1.hermesHookEvents.includes(eventName) || marker !== hermes_hook_ts_1.hermesManagedMarker || unexpected.length > 0) {
                        stderr.write('Hermes hook invocation is invalid or unowned\n');
                        return [2 /*return*/, 2];
                    }
                    return [4 /*yield*/, (0, hermes_hook_ts_1.runHermesHook)(eventName, process.stdin, process.env.THREADFORGE_EVENT_ENDPOINT)];
                case 3:
                    _3.sent();
                    return [2 /*return*/, 0];
                case 4:
                    if (family === 'hermes' && action === 'hooks') {
                        hermesSubsystem = 'hooks';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (!(family === 'openhands' && action === 'hook')) return [3 /*break*/, 6];
                    eventType = rest[0], marker = rest[1], unexpected = rest.slice(2);
                    if (!['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end'].includes(eventType !== null && eventType !== void 0 ? eventType : '') ||
                        marker !== openhands_hooks_config_ts_1.openHandsManagedMarker || unexpected.length > 0) {
                        stderr.write('OpenHands hook invocation is invalid or unowned\n');
                        return [2 /*return*/, 2];
                    }
                    return [4 /*yield*/, (0, openhands_hook_ts_1.runOpenHandsHook)(eventType, process.stdin, process.env.THREADFORGE_EVENT_ENDPOINT)];
                case 5:
                    _3.sent();
                    return [2 /*return*/, 0];
                case 6:
                    if (family === 'openclaw' && (action === 'otel' || action === 'sessions')) {
                        openClawSubsystem = action;
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'opencode' && action === 'sessions') {
                        openCodeSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'claude' && action === 'sessions') {
                        claudeSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'copilot' && action === 'sessions') {
                        copilotSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'cursor' && action === 'sessions') {
                        cursorSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'factory' && action === 'sessions') {
                        factorySubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'fx' && action === 'sessions') {
                        fxSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'pi' && action === 'sessions') {
                        piSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    if (family === 'prime' && action === 'sessions') {
                        primeSubsystem = 'sessions';
                        action = rest[0];
                        rest = rest.slice(1);
                        if (!action) {
                            stderr.write("".concat(usage(), "\n"));
                            return [2 /*return*/, 2];
                        }
                    }
                    _3.label = 7;
                case 7:
                    _3.trys.push([7, 162, , 163]);
                    options = parseOptions(rest);
                    if (family === 'hermes' && hermesSubsystem === 'hooks' && action === 'print-config') {
                        rejectUnexpected(options, ['command-template']);
                        stdout.write((0, hermes_hook_ts_1.renderHermesHookConfig)(options.values.get('command-template')));
                        return [2 /*return*/, 0];
                    }
                    if (family === 'cowork' && coworkSubsystem === 'otel' && action === 'print-config') {
                        rejectUnexpected(options, ['endpoint']);
                        stdout.write("".concat((0, claude_cowork_otel_ts_1.renderClaudeCoworkOtelGuidance)(required(options, 'endpoint')), "\n"));
                        return [2 /*return*/, 0];
                    }
                    providedMuseScope = options.values.get('scope');
                    if (family === 'muse' && !providedMuseScope)
                        throw new Error('Muse requires an explicit --scope user|project choice');
                    providedOmpScope = options.values.get('scope');
                    if (family === 'omp' && !providedOmpScope)
                        throw new Error('Oh My Pi requires an explicit --scope user|project choice');
                    providedOpenClawScope = options.values.get('scope');
                    isOpenClawPluginAction = family === 'openclaw' && ['install', 'update', 'uninstall'].includes(action);
                    if (isOpenClawPluginAction && !providedOpenClawScope)
                        throw new Error('OpenClaw requires an explicit --scope active-gateway choice');
                    antigravityScope = (_v = options.values.get('scope')) !== null && _v !== void 0 ? _v : 'user';
                    qwenScope = (_w = options.values.get('scope')) !== null && _w !== void 0 ? _w : 'user';
                    kiroScope = (_x = options.values.get('scope')) !== null && _x !== void 0 ? _x : 'user';
                    museScope = providedMuseScope !== null && providedMuseScope !== void 0 ? providedMuseScope : 'user';
                    ompScope = providedOmpScope !== null && providedOmpScope !== void 0 ? providedOmpScope : 'user';
                    projectRoot = family === 'gemini' || family === 'telemetry' || family === 'openclaw' || family === 'cowork' ||
                        (family === 'goose' && gooseSubsystem === 'otel') ||
                        (family === 'vscode' && vscodeSubsystem === 'otel') ||
                        (family === 'claude' && claudeSubsystem === 'sessions') ||
                        (family === 'copilot' && copilotSubsystem === 'sessions') ||
                        (family === 'cursor' && cursorSubsystem === 'sessions') ||
                        (family === 'factory' && factorySubsystem === 'sessions') ||
                        (family === 'fx' && fxSubsystem === 'sessions') ||
                        (family === 'pi' && piSubsystem === 'sessions') ||
                        (family === 'prime' && primeSubsystem === 'sessions') ||
                        (family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'status') ||
                        (family === 'codex' && (action === 'sync' || action === 'status')) ||
                        ((family === 'antigravity' && antigravityScope === 'user') || (family === 'qwen' && qwenScope === 'user') ||
                            (family === 'kiro' && kiroScope === 'user') || (family === 'muse' && museScope === 'user') ||
                            (family === 'omp' && ompScope === 'user') || family === 'kimi')
                        ? ''
                        : required(options, 'project-root');
                    result = void 0;
                    warning = void 0;
                    if (!(family === 'cline' && clineSubsystem === 'skills' && (action === 'install' || action === 'uninstall'))) return [3 /*break*/, 12];
                    rejectUnexpected(options, ['project-root', 'skill-file', 'sha256']);
                    skillOptions = {
                        projectRoot: projectRoot,
                        skillFile: required(options, 'skill-file'),
                        sha256: required(options, 'sha256'),
                    };
                    if (!(action === 'install')) return [3 /*break*/, 9];
                    return [4 /*yield*/, (0, cline_skill_config_ts_1.installClineProjectSkill)(skillOptions)];
                case 8:
                    _a = _3.sent();
                    return [3 /*break*/, 11];
                case 9: return [4 /*yield*/, (0, cline_skill_config_ts_1.uninstallClineProjectSkill)(skillOptions)];
                case 10:
                    _a = _3.sent();
                    _3.label = 11;
                case 11:
                    result = _a;
                    return [3 /*break*/, 161];
                case 12:
                    if (!(family === 'vscode' && vscodeSubsystem === 'otel' && action === 'print-config')) return [3 /*break*/, 13];
                    rejectUnexpected(options, ['endpoint']);
                    stdout.write("".concat((0, vscode_otel_ts_1.renderVSCodeOtelConfig)(options.values.has('endpoint') ? required(options, 'endpoint') : undefined), "\n"));
                    return [2 /*return*/, 0];
                case 13:
                    if (!(family === 'vscode' && vscodeSubsystem === 'otel' && (action === 'status' || action === 'validate'))) return [3 /*break*/, 15];
                    rejectUnexpected(options, ['settings-path', 'log-path', 'since'], [], ['json']);
                    return [4 /*yield*/, (0, vscode_otel_ts_1.getVSCodeOtelStatus)(__assign(__assign({}, (options.values.has('settings-path') ? { settingsPath: required(options, 'settings-path') } : {})), (options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {})))];
                case 14:
                    status_1 = _3.sent();
                    if (action === 'status') {
                        stdout.write(options.switches.has('json')
                            ? "".concat(JSON.stringify(status_1), "\n")
                            : "VS Code Copilot OTLP status: enabled=".concat(status_1.telemetry_enabled, ", endpoint=").concat(status_1.configured_endpoint, ", events=").concat(status_1.telemetry.observed_events, ", issues=").concat(status_1.issues.length, "\n"));
                        return [2 /*return*/, 0];
                    }
                    sinceMs = options.values.has('since') ? (0, vscode_otel_ts_1.parseVSCodeOtelDuration)(required(options, 'since')) : (0, vscode_otel_ts_1.parseVSCodeOtelDuration)('10m');
                    validation = (0, vscode_otel_ts_1.validateVSCodeOtelStatus)(status_1, sinceMs);
                    stdout.write(options.switches.has('json')
                        ? "".concat(JSON.stringify(validation), "\n")
                        : "VS Code Copilot OTLP validation ".concat(validation.valid ? 'passed' : 'failed', ": ").concat(validation.issues.length, " issue(s)\n"));
                    return [2 /*return*/, validation.valid ? 0 : 1];
                case 15:
                    if (!(family === 'goose' && gooseSubsystem === 'otel' && action === 'print-config')) return [3 /*break*/, 16];
                    rejectUnexpected(options, ['endpoint']);
                    stdout.write("".concat((0, goose_otel_ts_1.renderGooseOtelConfig)(options.values.has('endpoint') ? required(options, 'endpoint') : undefined), "\n"));
                    return [2 /*return*/, 0];
                case 16:
                    if (!(family === 'goose' && gooseSubsystem === 'otel' && (action === 'status' || action === 'validate'))) return [3 /*break*/, 18];
                    rejectUnexpected(options, ['config-path', 'log-path', 'since'], [], ['json']);
                    return [4 /*yield*/, (0, goose_otel_ts_1.getGooseOtelStatus)(__assign(__assign({}, (options.values.has('config-path') ? { configPath: required(options, 'config-path') } : {})), (options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {})))];
                case 17:
                    status_2 = _3.sent();
                    if (action === 'status') {
                        stdout.write(options.switches.has('json')
                            ? "".concat(JSON.stringify(status_2), "\n")
                            : "Goose OTLP status: endpoint=".concat(status_2.configured_endpoint, ", observed_events=").concat(status_2.telemetry.observed_events, ", issues=").concat(status_2.issues.length, "\n"));
                        return [2 /*return*/, 0];
                    }
                    sinceMs = options.values.has('since') ? (0, goose_otel_ts_1.parseGooseOtelDuration)(required(options, 'since')) : (0, goose_otel_ts_1.parseGooseOtelDuration)('10m');
                    validation = (0, goose_otel_ts_1.validateGooseOtelStatus)(status_2, sinceMs);
                    stdout.write(options.switches.has('json')
                        ? "".concat(JSON.stringify(validation), "\n")
                        : "Goose OTLP validation ".concat(validation.valid ? 'passed' : 'failed', ": ").concat(validation.issues.length, " issue(s)\n"));
                    return [2 /*return*/, validation.valid ? 0 : 1];
                case 18:
                    if (!(family === 'openclaw' && action === 'print-config')) return [3 /*break*/, 19];
                    rejectUnexpected(options, ['endpoint'], [], ['capture-content', 'allow-remote']);
                    stdout.write("".concat((0, openclaw_otel_ts_1.renderOpenClawOtelConfig)(__assign(__assign(__assign({}, (options.values.has('endpoint') ? { endpoint: required(options, 'endpoint') } : {})), (options.switches.has('capture-content') ? { captureContent: true } : {})), (options.switches.has('allow-remote') ? { allowRemote: true } : {}))), "\n"));
                    return [2 /*return*/, 0];
                case 19:
                    if (!(family === 'openclaw' && openClawSubsystem === 'otel' && (action === 'status' || action === 'validate'))) return [3 /*break*/, 21];
                    rejectUnexpected(options, ['log-path', 'since'], [], ['json']);
                    return [4 /*yield*/, (0, openclaw_otel_ts_1.getOpenClawOtelStatus)(options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {})];
                case 20:
                    status_3 = _3.sent();
                    if (action === 'status') {
                        if (options.switches.has('json'))
                            stdout.write("".concat(JSON.stringify(status_3), "\n"));
                        else
                            stdout.write(status_3.last_event_observed
                                ? "OpenClaw OTLP events observed: ".concat(status_3.observed_events, "; last at ").concat(status_3.last_event_observed_at).concat(status_3.complete ? '' : ' (local log scan incomplete)', "\n")
                                : "No OpenClaw OTLP events observed in the local Threadforge event log".concat(status_3.present ? '' : ' (log not present)', "\n"));
                        return [2 /*return*/, 0];
                    }
                    sinceMs = options.values.has('since') ? (0, openclaw_otel_ts_1.parseOpenClawDuration)(required(options, 'since')) : (0, openclaw_otel_ts_1.parseOpenClawDuration)('10m');
                    validation = (0, openclaw_otel_ts_1.validateOpenClawOtelStatus)(status_3, sinceMs);
                    if (options.switches.has('json'))
                        stdout.write("".concat(JSON.stringify(validation), "\n"));
                    else
                        stdout.write(validation.valid
                            ? "OpenClaw OTLP validation passed: recent event observed within ".concat((_y = options.values.get('since')) !== null && _y !== void 0 ? _y : '10m', "\n")
                            : "OpenClaw OTLP validation failed: no recent OpenClaw event observed within ".concat((_z = options.values.get('since')) !== null && _z !== void 0 ? _z : '10m', "\n"));
                    return [2 /*return*/, validation.valid ? 0 : 1];
                case 21:
                    if (!(family === 'openclaw' && openClawSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 23];
                    rejectUnexpected(options, ['state-path']);
                    _c = (_b = stdout).write;
                    _d = "".concat;
                    _f = (_e = JSON).stringify;
                    return [4 /*yield*/, (0, openclaw_history_ts_1.getOpenClawHistoryStatus)({ statePath: options.values.get('state-path') })];
                case 22:
                    _c.apply(_b, [_d.apply("", [_f.apply(_e, [_3.sent()]), "\n"])]);
                    return [2 /*return*/, 0];
                case 23:
                    if (!(family === 'openclaw' && openClawSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 25];
                    rejectUnexpected(options, ['openclaw-cli', 'state-path', 'endpoint', 'max-sessions', 'max-messages'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, openclaw_history_ts_1.syncOpenClawHistory)({
                            openclawCli: options.values.get('openclaw-cli'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                            maxMessages: options.values.has('max-messages') ? Number(required(options, 'max-messages')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 24:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 25:
                    if (!(family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 27];
                    rejectUnexpected(options, ['state-path']);
                    _h = (_g = stdout).write;
                    _j = "".concat;
                    _l = (_k = JSON).stringify;
                    return [4 /*yield*/, (0, opencode_history_ts_1.getOpenCodeHistoryStatus)({ statePath: options.values.get('state-path') })];
                case 26:
                    _h.apply(_g, [_j.apply("", [_l.apply(_k, [_3.sent()]), "\n"])]);
                    return [2 /*return*/, 0];
                case 27:
                    if (!(family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 29];
                    rejectUnexpected(options, ['project-root', 'opencode-cli', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, opencode_history_ts_1.syncOpenCodeHistory)({
                            projectRoot: projectRoot,
                            opencodeCli: options.values.get('opencode-cli'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 28:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 29:
                    if (!(family === 'claude' && claudeSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 31];
                    rejectUnexpected(options, ['projects-dir', 'state-path', 'max-files']);
                    return [4 /*yield*/, (0, claude_history_ts_1.getClaudeHistoryStatus)({
                            projectsDir: options.values.get('projects-dir'),
                            statePath: options.values.get('state-path'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                        })];
                case 30:
                    status_4 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_4), "\n"));
                    return [2 /*return*/, 0];
                case 31:
                    if (!(family === 'claude' && claudeSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 33];
                    rejectUnexpected(options, ['projects-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, claude_history_ts_1.syncClaudeHistory)({
                            projectsDir: options.values.get('projects-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 32:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 33:
                    if (!(family === 'copilot' && copilotSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 35];
                    rejectUnexpected(options, ['copilot-dir', 'state-path', 'max-sessions']);
                    return [4 /*yield*/, (0, copilot_history_ts_1.getCopilotHistoryStatus)({
                            copilotDir: options.values.get('copilot-dir'),
                            statePath: options.values.get('state-path'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                        })];
                case 34:
                    status_5 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_5), "\n"));
                    return [2 /*return*/, 0];
                case 35:
                    if (!(family === 'copilot' && copilotSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 37];
                    rejectUnexpected(options, ['copilot-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, copilot_history_ts_1.syncCopilotHistory)({
                            copilotDir: options.values.get('copilot-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 36:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 37:
                    if (!(family === 'cursor' && cursorSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 39];
                    rejectUnexpected(options, ['projects-dir', 'state-path', 'max-files']);
                    return [4 /*yield*/, (0, cursor_history_ts_1.getCursorHistoryStatus)({
                            projectsDir: options.values.get('projects-dir'),
                            statePath: options.values.get('state-path'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                        })];
                case 38:
                    status_6 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_6), "\n"));
                    return [2 /*return*/, 0];
                case 39:
                    if (!(family === 'cursor' && cursorSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 41];
                    rejectUnexpected(options, ['projects-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, cursor_history_ts_1.syncCursorHistory)({
                            projectsDir: options.values.get('projects-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 40:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 41:
                    if (!(family === 'factory' && factorySubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 43];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-sessions']);
                    return [4 /*yield*/, (0, factory_history_ts_1.getFactoryHistoryStatus)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                        })];
                case 42:
                    status_7 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_7), "\n"));
                    return [2 /*return*/, 0];
                case 43:
                    if (!(family === 'factory' && factorySubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 45];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, factory_history_ts_1.syncFactoryHistory)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 44:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 45:
                    if (!(family === 'fx' && fxSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 47];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-sessions']);
                    return [4 /*yield*/, (0, fx_history_ts_1.getFxHistoryStatus)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                        })];
                case 46:
                    status_8 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_8), "\n"));
                    return [2 /*return*/, 0];
                case 47:
                    if (!(family === 'fx' && fxSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 49];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, fx_history_ts_1.syncFxHistory)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 48:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 49:
                    if (!(family === 'pi' && piSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 51];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-files']);
                    return [4 /*yield*/, (0, pi_history_ts_1.getPiHistoryStatus)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                        })];
                case 50:
                    status_9 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_9), "\n"));
                    return [2 /*return*/, 0];
                case 51:
                    if (!(family === 'pi' && piSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 53];
                    rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, pi_history_ts_1.syncPiHistory)({
                            sessionsDir: options.values.get('sessions-dir'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 52:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 53:
                    if (!(family === 'prime' && primeSubsystem === 'sessions' && action === 'status')) return [3 /*break*/, 55];
                    rejectUnexpected(options, ['sessions-dir', 'artifacts-dir', 'state-path', 'max-files']);
                    return [4 /*yield*/, (0, prime_history_ts_1.getPrimeHistoryStatus)({
                            sessionsDir: options.values.get('sessions-dir'), artifactsDir: options.values.get('artifacts-dir'),
                            statePath: options.values.get('state-path'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                        })];
                case 54:
                    status_10 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_10), "\n"));
                    return [2 /*return*/, 0];
                case 55:
                    if (!(family === 'prime' && primeSubsystem === 'sessions' && action === 'sync')) return [3 /*break*/, 57];
                    rejectUnexpected(options, ['sessions-dir', 'artifacts-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, prime_history_ts_1.syncPrimeHistory)({
                            sessionsDir: options.values.get('sessions-dir'), artifactsDir: options.values.get('artifacts-dir'),
                            statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
                            maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
                            preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
                        })];
                case 56:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(syncResult), "\n"));
                    return [2 /*return*/, syncResult.complete ? 0 : 1];
                case 57:
                    if (!(family === 'kimi' && (action === 'install' || action === 'update'))) return [3 /*break*/, 62];
                    rejectUnexpected(options, ['command', 'kimi-code-home']);
                    configOptions = __assign({ command: required(options, 'command') }, (options.values.has('kimi-code-home') ? { kimiCodeHome: required(options, 'kimi-code-home') } : {}));
                    if (!(action === 'install')) return [3 /*break*/, 59];
                    return [4 /*yield*/, (0, kimi_code_hooks_config_ts_1.installKimiCodeHooks)(configOptions)];
                case 58:
                    _m = _3.sent();
                    return [3 /*break*/, 61];
                case 59: return [4 /*yield*/, (0, kimi_code_hooks_config_ts_1.updateKimiCodeHooks)(configOptions)];
                case 60:
                    _m = _3.sent();
                    _3.label = 61;
                case 61:
                    result = _m;
                    return [3 /*break*/, 161];
                case 62:
                    if (!(family === 'kimi' && action === 'uninstall')) return [3 /*break*/, 64];
                    rejectUnexpected(options, ['kimi-code-home']);
                    return [4 /*yield*/, (0, kimi_code_hooks_config_ts_1.removeKimiCodeHooks)(options.values.has('kimi-code-home')
                            ? { kimiCodeHome: required(options, 'kimi-code-home') }
                            : {})];
                case 63:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 64:
                    if (!(family === 'kiro' && (action === 'install' || action === 'update'))) return [3 /*break*/, 69];
                    rejectUnexpected(options, ['command', 'scope', 'project-root', 'kiro-home']);
                    if (kiroScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (kiroScope === 'project' && options.values.has('kiro-home'))
                        throw new Error('--kiro-home is only valid with --scope user');
                    configOptions = __assign(__assign({ command: required(options, 'command'), scope: kiroScope }, (kiroScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('kiro-home') ? { kiroHome: required(options, 'kiro-home') } : {}));
                    if (!(action === 'install')) return [3 /*break*/, 66];
                    return [4 /*yield*/, (0, kiro_hooks_config_ts_1.installKiroHooks)(configOptions)];
                case 65:
                    _o = _3.sent();
                    return [3 /*break*/, 68];
                case 66: return [4 /*yield*/, (0, kiro_hooks_config_ts_1.updateKiroHooks)(configOptions)];
                case 67:
                    _o = _3.sent();
                    _3.label = 68;
                case 68:
                    result = _o;
                    return [3 /*break*/, 161];
                case 69:
                    if (!(family === 'kiro' && action === 'uninstall')) return [3 /*break*/, 71];
                    rejectUnexpected(options, ['scope', 'project-root', 'kiro-home']);
                    if (kiroScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (kiroScope === 'project' && options.values.has('kiro-home'))
                        throw new Error('--kiro-home is only valid with --scope user');
                    return [4 /*yield*/, (0, kiro_hooks_config_ts_1.removeKiroHooks)(__assign(__assign({ scope: kiroScope }, (kiroScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('kiro-home') ? { kiroHome: required(options, 'kiro-home') } : {})))];
                case 70:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 71:
                    if (!(family === 'muse' && (action === 'install' || action === 'update'))) return [3 /*break*/, 76];
                    rejectUnexpected(options, ['command', 'scope', 'project-root', 'xdg-config-home']);
                    if (museScope !== 'user' && museScope !== 'project')
                        throw new Error('Muse scope must be user or project');
                    if (museScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (museScope === 'project' && options.values.has('xdg-config-home'))
                        throw new Error('--xdg-config-home is only valid with --scope user');
                    configOptions = __assign(__assign({ command: required(options, 'command'), scope: museScope }, (museScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('xdg-config-home') ? { xdgConfigHome: required(options, 'xdg-config-home') } : {}));
                    if (!(action === 'install')) return [3 /*break*/, 73];
                    return [4 /*yield*/, (0, muse_hooks_config_ts_1.installMuseHooks)(configOptions)];
                case 72:
                    _p = _3.sent();
                    return [3 /*break*/, 75];
                case 73: return [4 /*yield*/, (0, muse_hooks_config_ts_1.updateMuseHooks)(configOptions)];
                case 74:
                    _p = _3.sent();
                    _3.label = 75;
                case 75:
                    result = _p;
                    return [3 /*break*/, 161];
                case 76:
                    if (!(family === 'muse' && action === 'uninstall')) return [3 /*break*/, 78];
                    rejectUnexpected(options, ['scope', 'project-root', 'xdg-config-home']);
                    if (museScope !== 'user' && museScope !== 'project')
                        throw new Error('Muse scope must be user or project');
                    if (museScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (museScope === 'project' && options.values.has('xdg-config-home'))
                        throw new Error('--xdg-config-home is only valid with --scope user');
                    return [4 /*yield*/, (0, muse_hooks_config_ts_1.removeMuseHooks)(__assign(__assign({ scope: museScope }, (museScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('xdg-config-home') ? { xdgConfigHome: required(options, 'xdg-config-home') } : {})))];
                case 77:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 78:
                    if (!(family === 'omp' && (action === 'install' || action === 'update'))) return [3 /*break*/, 83];
                    rejectUnexpected(options, ['scope', 'project-root', 'omp-agent-dir']);
                    if (ompScope !== 'user' && ompScope !== 'project')
                        throw new Error('Oh My Pi scope must be user or project');
                    if (ompScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (ompScope === 'project' && options.values.has('omp-agent-dir'))
                        throw new Error('--omp-agent-dir is only valid with --scope user');
                    configOptions = __assign(__assign({ scope: ompScope }, (ompScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('omp-agent-dir') ? { ompAgentDir: required(options, 'omp-agent-dir') } : {}));
                    if (!(action === 'install')) return [3 /*break*/, 80];
                    return [4 /*yield*/, (0, omp_hooks_config_ts_1.installOmpExtension)(configOptions)];
                case 79:
                    _q = _3.sent();
                    return [3 /*break*/, 82];
                case 80: return [4 /*yield*/, (0, omp_hooks_config_ts_1.updateOmpExtension)(configOptions)];
                case 81:
                    _q = _3.sent();
                    _3.label = 82;
                case 82:
                    result = _q;
                    return [3 /*break*/, 161];
                case 83:
                    if (!(family === 'omp' && action === 'uninstall')) return [3 /*break*/, 85];
                    rejectUnexpected(options, ['scope', 'project-root', 'omp-agent-dir']);
                    if (ompScope !== 'user' && ompScope !== 'project')
                        throw new Error('Oh My Pi scope must be user or project');
                    if (ompScope === 'user' && options.values.has('project-root'))
                        throw new Error('--project-root is only valid with --scope project');
                    if (ompScope === 'project' && options.values.has('omp-agent-dir'))
                        throw new Error('--omp-agent-dir is only valid with --scope user');
                    return [4 /*yield*/, (0, omp_hooks_config_ts_1.removeOmpExtension)(__assign(__assign({ scope: ompScope }, (ompScope === 'project' ? { projectRoot: projectRoot } : {})), (options.values.has('omp-agent-dir') ? { ompAgentDir: required(options, 'omp-agent-dir') } : {})))];
                case 84:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 85:
                    if (!(family === 'openhands' && (action === 'install' || action === 'update'))) return [3 /*break*/, 87];
                    rejectUnexpected(options, ['project-root', 'command-template']);
                    return [4 /*yield*/, (0, openhands_hooks_config_ts_1.installOpenHandsProjectHooks)(projectRoot, __assign({}, (options.values.has('command-template') ? { commandTemplate: required(options, 'command-template') } : {})))];
                case 86:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 87:
                    if (!(family === 'openhands' && action === 'uninstall')) return [3 /*break*/, 89];
                    rejectUnexpected(options, ['project-root']);
                    return [4 /*yield*/, (0, openhands_hooks_config_ts_1.removeOpenHandsProjectHooks)(projectRoot)];
                case 88:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 89:
                    if (!(family === 'openclaw' && (action === 'install' || action === 'update' || action === 'uninstall'))) return [3 /*break*/, 91];
                    rejectUnexpected(options, ['scope', 'openclaw-cli'], [], ['print', 'acknowledge-local-source']);
                    if (providedOpenClawScope !== 'active-gateway')
                        throw new Error('OpenClaw scope must be active-gateway; run the command under the profile used by the Gateway');
                    if (action !== 'install' && options.switches.has('acknowledge-local-source')) {
                        throw new Error('--acknowledge-local-source is valid only with OpenClaw install');
                    }
                    pluginAction = action;
                    pluginOptions = __assign(__assign({ scope: 'active-gateway' }, (options.values.has('openclaw-cli') ? { openclawCli: required(options, 'openclaw-cli') } : {})), (options.switches.has('acknowledge-local-source') ? { acknowledgeLocalSource: true } : {}));
                    if (options.switches.has('print')) {
                        commands = (0, openclaw_plugin_install_ts_1.buildOpenClawPluginCommands)(pluginAction, pluginOptions);
                        stdout.write("".concat(JSON.stringify({ schema_version: 'threadforge.adapter-install.v1', family: family, action: action, result: 'planned', commands: commands }), "\n"));
                        return [2 /*return*/, 0];
                    }
                    return [4 /*yield*/, (0, openclaw_plugin_install_ts_1.runOpenClawPluginAction)(pluginAction, pluginOptions)];
                case 90:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 91:
                    if (!(family === 'codex' && action === 'status')) return [3 /*break*/, 93];
                    rejectUnexpected(options, ['codex-home', 'state-path']);
                    return [4 /*yield*/, (0, codex_rollouts_ts_1.getCodexRolloutStatus)({
                            codexHome: options.values.get('codex-home'),
                            statePath: options.values.get('state-path'),
                        })];
                case 92:
                    status_11 = _3.sent();
                    stdout.write("".concat(JSON.stringify(status_11), "\n"));
                    return [2 /*return*/, 0];
                case 93:
                    if (!(family === 'codex' && action === 'sync')) return [3 /*break*/, 95];
                    rejectUnexpected(options, ['codex-home', 'state-path', 'endpoint'], [], ['print', 'include-content']);
                    return [4 /*yield*/, (0, codex_rollouts_ts_1.syncCodexRollouts)({
                            codexHome: options.values.get('codex-home'),
                            statePath: options.values.get('state-path'),
                            endpoint: options.values.get('endpoint'),
                            preview: options.switches.has('print'),
                            includeContent: options.switches.has('include-content'),
                        })];
                case 94:
                    syncResult = _3.sent();
                    stdout.write("".concat(JSON.stringify(__assign({ schema_version: 'threadforge.codex-rollout-sync.v1' }, syncResult)), "\n"));
                    return [2 /*return*/, syncResult.errors.length > 0 ? 1 : 0];
                case 95:
                    if (!(family === 'telemetry' && (action === 'install' || action === 'update'))) return [3 /*break*/, 101];
                    rejectUnexpected(options, ['host']);
                    host = required(options, 'host');
                    if (!(host === 'claude-code')) return [3 /*break*/, 97];
                    return [4 /*yield*/, (0, claude_telemetry_config_ts_1.installClaudeTelemetry)()];
                case 96:
                    result = _3.sent();
                    return [3 /*break*/, 100];
                case 97:
                    if (!(host === 'codex-cli')) return [3 /*break*/, 99];
                    return [4 /*yield*/, (0, codex_telemetry_config_ts_1.installCodexTelemetry)()];
                case 98:
                    result = _3.sent();
                    return [3 /*break*/, 100];
                case 99: throw new Error('unsupported telemetry host');
                case 100: return [3 /*break*/, 161];
                case 101:
                    if (!(family === 'telemetry' && action === 'uninstall')) return [3 /*break*/, 107];
                    rejectUnexpected(options, ['host']);
                    host = required(options, 'host');
                    if (!(host === 'claude-code')) return [3 /*break*/, 103];
                    return [4 /*yield*/, (0, claude_telemetry_config_ts_1.removeClaudeTelemetry)()];
                case 102:
                    result = _3.sent();
                    return [3 /*break*/, 106];
                case 103:
                    if (!(host === 'codex-cli')) return [3 /*break*/, 105];
                    return [4 /*yield*/, (0, codex_telemetry_config_ts_1.removeCodexTelemetry)()];
                case 104:
                    result = _3.sent();
                    return [3 /*break*/, 106];
                case 105: throw new Error('unsupported telemetry host');
                case 106: return [3 /*break*/, 161];
                case 107:
                    if (!(family === 'gemini' && (action === 'install' || action === 'update'))) return [3 /*break*/, 109];
                    rejectUnexpected(options, []);
                    return [4 /*yield*/, (0, gemini_telemetry_config_ts_1.installGeminiTelemetry)()];
                case 108:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 109:
                    if (!(family === 'gemini' && action === 'uninstall')) return [3 /*break*/, 111];
                    rejectUnexpected(options, []);
                    return [4 /*yield*/, (0, gemini_telemetry_config_ts_1.removeGeminiTelemetry)()];
                case 110:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 111:
                    if (!(family === 'claude' && (action === 'install' || action === 'update'))) return [3 /*break*/, 113];
                    rejectUnexpected(options, ['project-root', 'command']);
                    command = required(options, 'command');
                    return [4 /*yield*/, (0, claude_hooks_config_ts_1.installClaudeProjectHooks)(projectRoot, { command: command })];
                case 112:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 113:
                    if (!(family === 'claude' && action === 'uninstall')) return [3 /*break*/, 115];
                    rejectUnexpected(options, ['project-root']);
                    return [4 /*yield*/, (0, claude_hooks_config_ts_1.removeClaudeProjectHooks)(projectRoot)];
                case 114:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 115:
                    if (!(family === 'opencode' && (action === 'install' || action === 'update'))) return [3 /*break*/, 117];
                    rejectUnexpected(options, ['project-root', 'module']);
                    return [4 /*yield*/, (0, opencode_plugin_config_ts_1.installOpenCodeProjectPlugin)(projectRoot, required(options, 'module'))];
                case 116:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 117:
                    if (!(family === 'opencode' && action === 'uninstall')) return [3 /*break*/, 119];
                    rejectUnexpected(options, ['project-root']);
                    return [4 /*yield*/, (0, opencode_plugin_config_ts_1.removeOpenCodeProjectPlugin)(projectRoot)];
                case 118:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 119:
                    if (!(family === 'cursor' && (action === 'install' || action === 'update'))) return [3 /*break*/, 121];
                    rejectUnexpected(options, ['project-root', 'command']);
                    return [4 /*yield*/, (0, cursor_hooks_config_ts_1.installCursorProjectHooks)(projectRoot, required(options, 'command'))];
                case 120:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 121:
                    if (!(family === 'cursor' && action === 'uninstall')) return [3 /*break*/, 123];
                    rejectUnexpected(options, ['project-root']);
                    return [4 /*yield*/, (0, cursor_hooks_config_ts_1.removeCursorProjectHooks)(projectRoot)];
                case 122:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 123:
                    if (!(family === 'codex' && (action === 'install' || action === 'update'))) return [3 /*break*/, 125];
                    rejectUnexpected(options, ['project-root', 'command']);
                    return [4 /*yield*/, (0, codex_hooks_config_ts_1.installCodexProjectHooks)(projectRoot, required(options, 'command'))];
                case 124:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 125:
                    if (!(family === 'codex' && action === 'uninstall')) return [3 /*break*/, 127];
                    rejectUnexpected(options, ['project-root']);
                    return [4 /*yield*/, (0, codex_hooks_config_ts_1.removeCodexProjectHooks)(projectRoot)];
                case 126:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 127:
                    if (!(family === 'antigravity' && (action === 'install' || action === 'update'))) return [3 /*break*/, 129];
                    rejectUnexpected(options, ['scope', 'project-root', 'command']);
                    if (antigravityScope !== 'user' && antigravityScope !== 'project')
                        throw new Error('Antigravity --scope must be user or project');
                    return [4 /*yield*/, (0, antigravity_hooks_config_ts_1.installAntigravityHooks)(__assign(__assign({ scope: antigravityScope }, (antigravityScope === 'project' ? { projectRoot: projectRoot } : {})), { command: required(options, 'command') }))];
                case 128:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 129:
                    if (!(family === 'antigravity' && action === 'uninstall')) return [3 /*break*/, 131];
                    rejectUnexpected(options, ['scope', 'project-root']);
                    if (antigravityScope !== 'user' && antigravityScope !== 'project')
                        throw new Error('Antigravity --scope must be user or project');
                    return [4 /*yield*/, (0, antigravity_hooks_config_ts_1.removeAntigravityHooks)(__assign({ scope: antigravityScope }, (antigravityScope === 'project' ? { projectRoot: projectRoot } : {})))];
                case 130:
                    result = _3.sent();
                    return [3 /*break*/, 161];
                case 131:
                    if (!(family === 'qwen' && (action === 'install' || action === 'update'))) return [3 /*break*/, 133];
                    rejectUnexpected(options, ['scope', 'project-root', 'command']);
                    if (qwenScope !== 'user' && qwenScope !== 'project')
                        throw new Error('Qwen --scope must be user or project');
                    return [4 /*yield*/, (0, qwen_hooks_config_ts_1.installQwenHooks)(__assign(__assign({ scope: qwenScope }, (qwenScope === 'project' ? { projectRoot: projectRoot } : {})), { command: required(options, 'command') }))];
                case 132:
                    result = _3.sent();
                    if (qwenScope === 'project')
                        warning = 'Qwen Code runs project hooks only in a trusted folder; Threadforge did not change project trust';
                    return [3 /*break*/, 161];
                case 133:
                    if (!(family === 'qwen' && action === 'uninstall')) return [3 /*break*/, 135];
                    rejectUnexpected(options, ['scope', 'project-root']);
                    if (qwenScope !== 'user' && qwenScope !== 'project')
                        throw new Error('Qwen --scope must be user or project');
                    return [4 /*yield*/, (0, qwen_hooks_config_ts_1.removeQwenHooks)(__assign({ scope: qwenScope }, (qwenScope === 'project' ? { projectRoot: projectRoot } : {})))];
                case 134:
                    result = _3.sent();
                    if (qwenScope === 'project')
                        warning = 'Qwen Code runs project hooks only in a trusted folder; Threadforge did not change project trust';
                    return [3 /*break*/, 161];
                case 135:
                    if (!(family === 'mcp' && action === 'uninstall')) return [3 /*break*/, 143];
                    hostName = required(options, 'host');
                    name_1 = (_0 = options.values.get('name')) !== null && _0 !== void 0 ? _0 : 'threadforge';
                    rejectUnexpected(options, ['project-root', 'host', 'name']);
                    if (!(hostName === 'codex-cli')) return [3 /*break*/, 137];
                    if (name_1 !== 'threadforge')
                        throw new Error('Codex project MCP currently manages the fixed server name threadforge');
                    return [4 /*yield*/, (0, codex_mcp_config_ts_1.removeCodexProjectMcpServer)(projectRoot)];
                case 136:
                    result = _3.sent();
                    return [3 /*break*/, 142];
                case 137:
                    if (!(hostName === 'opencode')) return [3 /*break*/, 139];
                    return [4 /*yield*/, (0, opencode_mcp_config_ts_1.removeManagedOpenCodeProjectMcpServer)(projectRoot, name_1)];
                case 138:
                    _r = _3.sent();
                    return [3 /*break*/, 141];
                case 139: return [4 /*yield*/, (0, mcp_config_ts_1.removeManagedProjectMcpServer)(projectRoot, hostName, name_1)];
                case 140:
                    _r = _3.sent();
                    _3.label = 141;
                case 141:
                    result = _r;
                    _3.label = 142;
                case 142: return [3 /*break*/, 161];
                case 143:
                    if (!(family === 'mcp' && (action === 'install' || action === 'update'))) return [3 /*break*/, 160];
                    hostName = required(options, 'host');
                    command = required(options, 'command');
                    return [4 /*yield*/, buildManagedMcpArgs(projectRoot, (_1 = options.repeated.get('arg')) !== null && _1 !== void 0 ? _1 : [])];
                case 144:
                    args_1 = _3.sent();
                    cwd = options.values.get('cwd');
                    rejectUnexpected(options, ['project-root', 'host', 'command', 'cwd', 'name'], ['arg']);
                    name_2 = (_2 = options.values.get('name')) !== null && _2 !== void 0 ? _2 : 'threadforge';
                    if (!(hostName === 'codex-cli')) return [3 /*break*/, 149];
                    if (cwd !== undefined)
                        throw new Error('Codex project MCP config does not accept --cwd; use the project root as its working directory');
                    if (name_2 !== 'threadforge')
                        throw new Error('Codex project MCP currently manages the fixed server name threadforge');
                    if (!(action === 'install')) return [3 /*break*/, 146];
                    return [4 /*yield*/, (0, codex_mcp_config_ts_1.installCodexProjectMcpServer)(projectRoot, command, args_1)];
                case 145:
                    _s = _3.sent();
                    return [3 /*break*/, 148];
                case 146: return [4 /*yield*/, (0, codex_mcp_config_ts_1.updateCodexProjectMcpServer)(projectRoot, command, args_1)];
                case 147:
                    _s = _3.sent();
                    _3.label = 148;
                case 148:
                    result = _s;
                    return [3 /*break*/, 159];
                case 149:
                    if (!(hostName === 'opencode')) return [3 /*break*/, 154];
                    if (cwd !== undefined)
                        throw new Error('OpenCode project MCP config does not accept --cwd; use the project root as its working directory');
                    if (!(action === 'install')) return [3 /*break*/, 151];
                    return [4 /*yield*/, (0, opencode_mcp_config_ts_1.installOpenCodeProjectMcpServer)(projectRoot, name_2, command, args_1)];
                case 150:
                    _t = _3.sent();
                    return [3 /*break*/, 153];
                case 151: return [4 /*yield*/, (0, opencode_mcp_config_ts_1.updateManagedOpenCodeProjectMcpServer)(projectRoot, name_2, command, args_1)];
                case 152:
                    _t = _3.sent();
                    _3.label = 153;
                case 153:
                    result = _t;
                    return [3 /*break*/, 159];
                case 154:
                    host = hostName;
                    server = __assign(__assign({ command: command, args: args_1 }, (cwd === undefined ? {} : { cwd: cwd })), (host === 'gemini-cli' ? { trust: false } : {}));
                    if (!(action === 'install')) return [3 /*break*/, 156];
                    return [4 /*yield*/, (0, mcp_config_ts_1.installProjectMcpServer)(projectRoot, host, name_2, server)];
                case 155:
                    _u = _3.sent();
                    return [3 /*break*/, 158];
                case 156: return [4 /*yield*/, (0, mcp_config_ts_1.updateManagedProjectMcpServer)(projectRoot, host, name_2, server)];
                case 157:
                    _u = _3.sent();
                    _3.label = 158;
                case 158:
                    result = _u;
                    _3.label = 159;
                case 159: return [3 /*break*/, 161];
                case 160: throw new Error('unsupported adapter command');
                case 161:
                    stdout.write("".concat(JSON.stringify(__assign({ schema_version: 'threadforge.adapter-install.v1', family: family, action: action, result: result }, (warning ? { warning: warning } : {}))), "\n"));
                    return [2 /*return*/, 0];
                case 162:
                    error_1 = _3.sent();
                    message = error_1 instanceof Error ? error_1.message : 'adapter command failed';
                    stderr.write("".concat(message, "\n"));
                    return [2 /*return*/, 1];
                case 163: return [2 /*return*/];
            }
        });
    });
}
function parseOptions(args) {
    var _a;
    var values = new Map();
    var repeated = new Map();
    var switches = new Set();
    for (var index = 0; index < args.length; index++) {
        var key = args[index];
        if (!(key === null || key === void 0 ? void 0 : key.startsWith('--')) || key.length < 3)
            throw new Error('adapter options must use --name value form');
        var name_3 = key.slice(2);
        if (name_3 === 'print' || name_3 === 'include-content' || name_3 === 'acknowledge-local-source' ||
            name_3 === 'json' || name_3 === 'capture-content' || name_3 === 'allow-remote') {
            if (switches.has(name_3))
                throw new Error("adapter option --".concat(name_3, " was repeated"));
            switches.add(name_3);
            continue;
        }
        var value = args[++index];
        if (name_3 === 'arg') {
            if (value === undefined)
                throw new Error("adapter option --".concat(name_3, " requires a value"));
            repeated.set(name_3, __spreadArray(__spreadArray([], ((_a = repeated.get(name_3)) !== null && _a !== void 0 ? _a : []), true), [value], false));
        }
        else {
            if (value === undefined || value.startsWith('--'))
                throw new Error("adapter option --".concat(name_3, " requires a value"));
            if (values.has(name_3))
                throw new Error("adapter option --".concat(name_3, " was repeated"));
            values.set(name_3, value);
        }
    }
    return { values: values, repeated: repeated, switches: switches };
}
function required(options, key) {
    var _a;
    var value = (_a = options.values.get(key)) === null || _a === void 0 ? void 0 : _a.trim();
    if (!value)
        throw new Error("adapter option --".concat(key, " is required"));
    return value;
}
function rejectUnexpected(options, allowed, repeatedAllowed, switchesAllowed) {
    var _a, _b;
    if (repeatedAllowed === void 0) { repeatedAllowed = []; }
    if (switchesAllowed === void 0) { switchesAllowed = []; }
    var unexpected = (_b = (_a = __spreadArray([], options.values.keys(), true).find(function (key) { return !allowed.includes(key); })) !== null && _a !== void 0 ? _a : __spreadArray([], options.repeated.keys(), true).find(function (key) { return !repeatedAllowed.includes(key); })) !== null && _b !== void 0 ? _b : __spreadArray([], options.switches, true).find(function (key) { return !switchesAllowed.includes(key); });
    if (unexpected)
        throw new Error("unsupported adapter option --".concat(unexpected));
}
function buildManagedMcpArgs(projectRoot, args) {
    return __awaiter(this, void 0, void 0, function () {
        var rootIndexes, rootIndex, canonicalRoot, configuredRoot, normalize, same;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    if (args.length < 4 || args[0] !== 'mcp' || args[1] !== 'serve') {
                        throw new Error('MCP args must begin with mcp serve and include --project-root <same-project-root>');
                    }
                    rootIndexes = args.flatMap(function (value, index) { return value === '--project-root' ? [index] : []; });
                    if (rootIndexes.length !== 1 || args.some(function (value) { return value.startsWith('--project-root='); })) {
                        throw new Error('MCP args must contain exactly one --project-root setting');
                    }
                    rootIndex = rootIndexes[0];
                    if (!args[rootIndex + 1] || args[rootIndex + 1].startsWith('--')) {
                        throw new Error('MCP args must include --project-root <same-project-root>');
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(projectRoot).catch(function () { throw new Error('project root must exist and be accessible'); })];
                case 1:
                    canonicalRoot = _a.sent();
                    return [4 /*yield*/, (0, promises_1.realpath)(args[rootIndex + 1]).catch(function () { throw new Error('MCP --project-root must exist and match the selected project root'); })];
                case 2:
                    configuredRoot = _a.sent();
                    normalize = function (value) { return node_path_1.default.resolve(value).replace(/[\\/]+$/, ''); };
                    same = process.platform === 'win32'
                        ? normalize(canonicalRoot).toLowerCase() === normalize(configuredRoot).toLowerCase()
                        : normalize(canonicalRoot) === normalize(configuredRoot);
                    if (!same)
                        throw new Error('MCP --project-root must match the selected project root');
                    return [2 /*return*/, __spreadArray(__spreadArray([], args.filter(function (value) { return value !== mcp_config_ts_1.managedMcpArgument; }), true), [mcp_config_ts_1.managedMcpArgument], false)];
            }
        });
    });
}
function usage() {
    return [
        'Usage:',
        '  npm run adapter -- claude install|update --project-root <absolute-path> --command <shell-command-with-marker>',
        '  npm run adapter -- claude uninstall --project-root <absolute-path>',
        '  npm run adapter -- cursor install|update --project-root <absolute-path> --command <shell-command-with-marker>',
        '  npm run adapter -- cursor uninstall --project-root <absolute-path>',
        '  npm run adapter -- codex install|update --project-root <absolute-path> --command <shell-command-with-marker>',
        '  npm run adapter -- codex uninstall --project-root <absolute-path>',
        '  npm run adapter -- antigravity install|update [--scope user|project] [--project-root <absolute-path>] --command <shell-command-with-marker>',
        '  npm run adapter -- antigravity uninstall [--scope user|project] [--project-root <absolute-path>]',
        '  npm run adapter -- qwen install|update [--scope user|project] [--project-root <absolute-path>] --command <bash-command-with-marker>',
        '  npm run adapter -- qwen uninstall [--scope user|project] [--project-root <absolute-path>]',
        '  npm run adapter -- kimi install|update --command <shell-command-with-marker> [--kimi-code-home <absolute-path>]',
        '  npm run adapter -- kimi uninstall [--kimi-code-home <absolute-path>]',
        '  npm run adapter -- kiro install|update --scope user|project --command <shell-command-with-marker> [--project-root <absolute-path>] [--kiro-home <absolute-path>]',
        '  npm run adapter -- kiro uninstall [--scope user|project] [--project-root <absolute-path>] [--kiro-home <absolute-path>]',
        '  npm run adapter -- muse install|update --scope user|project --command <shell-command-with-marker> [--project-root <absolute-path>] [--xdg-config-home <absolute-path>]',
        '  npm run adapter -- muse uninstall --scope user|project [--project-root <absolute-path>] [--xdg-config-home <absolute-path>]',
        '  npm run adapter -- omp install|update --scope user|project [--project-root <absolute-path>] [--omp-agent-dir <absolute-path>]',
        '  npm run adapter -- omp uninstall --scope user|project [--project-root <absolute-path>] [--omp-agent-dir <absolute-path>]',
        '  npm run adapter -- openclaw install|update|uninstall --scope active-gateway [--openclaw-cli <command>] [--print] [--acknowledge-local-source]',
        '  npm run adapter -- openhands install|update --project-root <absolute-path> [--command-template <command-with-{event}-and-marker>]',
        '  npm run adapter -- openhands uninstall --project-root <absolute-path>',
        '  npm run adapter -- openhands hook <session_start|user_prompt_submit|pre_tool_use|post_tool_use|stop|session_end> --threadforge-openhands-hook=v1',
        '  npm run adapter -- hermes hooks print-config [--command-template <command-with-{event}-and-marker>]',
        '  npm run adapter -- hermes hook <event> --threadforge-hermes-hook=v1',
        '  npm run adapter -- dsh hook <SessionStart|UserPromptSubmit|PreToolUse|PostToolUse|Stop|SubagentStart|SubagentStop> --threadforge-dsh-hook=v1',
        '  npm run adapter -- claude-cowork otel print-config --endpoint <public-https-collector-url>',
        '  npm run adapter -- goose otel print-config [--endpoint <loopback-http-base-url>]',
        '  npm run adapter -- goose otel status [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- goose otel validate [--since <duration>] [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- vscode otel print-config [--endpoint <loopback-http-base-url>]',
        '  npm run adapter -- vscode otel status [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- vscode otel validate [--since <duration>] [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- openclaw otel print-config [--endpoint <url>] [--capture-content] [--allow-remote]',
        '  npm run adapter -- openclaw otel status [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- openclaw otel validate [--since <duration>] [--log-path <events.jsonl>] [--json]',
        '  npm run adapter -- openclaw sessions status [--state-path <absolute-path>]',
        '  npm run adapter -- openclaw sessions sync [--openclaw-cli <command>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-200>] [--max-messages <1-2000>] [--print] [--include-content]',
        '  npm run adapter -- claude sessions status [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-5000>]',
        '  npm run adapter -- claude sessions sync [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-5000>] [--print] [--include-content]',
        '  npm run adapter -- copilot sessions status [--copilot-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
        '  npm run adapter -- copilot sessions sync [--copilot-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
        '  npm run adapter -- cursor sessions status [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-5000>]',
        '  npm run adapter -- cursor sessions sync [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-5000>] [--print] [--include-content]',
        '  npm run adapter -- factory sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
        '  npm run adapter -- factory sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
        '  npm run adapter -- fx sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
        '  npm run adapter -- fx sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
        '  npm run adapter -- pi sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]',
        '  npm run adapter -- pi sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]',
        '  npm run adapter -- prime-agent sessions status [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]',
        '  npm run adapter -- prime-agent sessions sync [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]',
        '  npm run adapter -- opencode sessions status [--state-path <absolute-path>]',
        '  npm run adapter -- opencode sessions sync --project-root <absolute-path> [--opencode-cli <command>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-200>] [--print] [--include-content]',
        '  npm run adapter -- codex status [--codex-home <absolute-path>] [--state-path <absolute-path>]',
        '  npm run adapter -- codex sync [--codex-home <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--print] [--include-content]',
        '  npm run adapter -- gemini install|update|uninstall',
        '  npm run adapter -- telemetry install|update|uninstall --host <claude-code|codex-cli>',
        '  npm run adapter -- opencode install|update --project-root <absolute-path> --module <Threadforge-package-export-or-file-url>',
        '  npm run adapter -- opencode uninstall --project-root <absolute-path>',
        '  npm run adapter -- mcp install --host <claude-code|cursor|antigravity|gemini-cli|opencode|codex-cli> --project-root <absolute-path> --command <executable> [--arg <value> ...] [--cwd <path>] [--name threadforge]',
        '  npm run adapter -- mcp update --host <claude-code|cursor|antigravity|gemini-cli|opencode|codex-cli> --project-root <absolute-path> --command <executable> [--arg <value> ...] [--cwd <path>] [--name threadforge]',
        '  npm run adapter -- mcp uninstall --host <claude-code|cursor|antigravity|gemini-cli|opencode|codex-cli> --project-root <absolute-path> [--name threadforge]',
        '  npm run adapter -- cline skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    ].join('\n');
}
if (process.argv[1] && import.meta.url === (0, node_url_1.pathToFileURL)(node_path_1.default.resolve(process.argv[1])).href) {
    process.exitCode = await runAdapterCLI(process.argv.slice(2), process.stdout, process.stderr);
}
