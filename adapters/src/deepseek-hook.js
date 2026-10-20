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
var __asyncValues = (this && this.__asyncValues) || function (o) {
    if (!Symbol.asyncIterator) throw new TypeError("Symbol.asyncIterator is not defined.");
    var m = o[Symbol.asyncIterator], i;
    return m ? m.call(o) : (o = typeof __values === "function" ? __values(o) : o[Symbol.iterator](), i = {}, verb("next"), verb("throw"), verb("return"), i[Symbol.asyncIterator] = function () { return this; }, i);
    function verb(n) { i[n] = o[n] && function (v) { return new Promise(function (resolve, reject) { v = o[n](v), settle(resolve, reject, v.done, v.value); }); }; }
    function settle(resolve, reject, d, v) { Promise.resolve(v).then(function(v) { resolve({ value: v, done: d }); }, reject); }
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.deepseekManagedMarker = exports.deepseekHookEvents = void 0;
exports.mapDeepSeekHookPayload = mapDeepSeekHookPayload;
exports.runDeepSeekHook = runDeepSeekHook;
var claude_hook_ts_1 = require("./claude-hook.ts");
var emit_ts_1 = require("./emit.ts");
exports.deepseekHookEvents = [
    'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop', 'SubagentStart', 'SubagentStop',
];
exports.deepseekManagedMarker = '--threadforge-dsh-hook=v1';
var maxInputBytes = 1 << 20;
/** Maps the seven supported DeepSeek Harness bridge events without retaining payload content. */
function mapDeepSeekHookPayload(value, capturedAt) {
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isRecord(value))
        return undefined;
    var eventName = value.hook_event_name;
    if (!isDeepSeekHookEvent(eventName))
        return undefined;
    var event = (0, claude_hook_ts_1.mapClaudeHookPayload)(value, capturedAt);
    if (!event)
        return undefined;
    var harness = event.harness;
    if (isRecord(harness))
        harness.name = 'deepseek_harness';
    var session = event.session;
    if (isRecord(session) && typeof session.id === 'string' && session.id.startsWith('claude:')) {
        session.id = "dsh:".concat(session.id.slice('claude:'.length));
    }
    if (eventName === 'SubagentStop' && isRecord(event.event))
        event.event.action = 'subagent.stopped';
    return event;
}
/**
 * Emits a digest-first event to the loopback receiver and always answers the Claude-shaped bridge
 * with an empty no-opinion object. Collector and parse failures cannot affect tools or permissions.
 */
function runDeepSeekHook(eventName_1, input_1, stdout_1, stderr_1) {
    return __awaiter(this, arguments, void 0, function (eventName, input, stdout, stderr, options) {
        var bytes, payload, event_1, _a;
        var _b, _c, _d;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    _e.trys.push([0, 4, , 5]);
                    if (!isDeepSeekHookEvent(eventName))
                        throw new Error('unsupported event');
                    return [4 /*yield*/, readBoundedInput(input)];
                case 1:
                    bytes = _e.sent();
                    payload = JSON.parse(bytes.toString('utf8'));
                    if (!isRecord(payload) || payload.hook_event_name !== eventName)
                        throw new Error('event mismatch');
                    event_1 = mapDeepSeekHookPayload(payload, (_c = (_b = options.now) === null || _b === void 0 ? void 0 : _b.call(options)) !== null && _c !== void 0 ? _c : new Date());
                    if (!event_1) return [3 /*break*/, 3];
                    return [4 /*yield*/, (0, emit_ts_1.emitEvent)(event_1, {
                            endpoint: options.endpoint,
                            timeoutMs: (_d = options.timeoutMs) !== null && _d !== void 0 ? _d : 250,
                            fetchImpl: options.fetchImpl,
                        })];
                case 2:
                    _e.sent();
                    _e.label = 3;
                case 3: return [3 /*break*/, 5];
                case 4:
                    _a = _e.sent();
                    stderr.write('threadforge DeepSeek Harness hook capture skipped; no policy decision was made\n');
                    return [3 /*break*/, 5];
                case 5:
                    stdout.write('{}\n');
                    return [2 /*return*/];
            }
        });
    });
}
function readBoundedInput(input) {
    return __awaiter(this, void 0, void 0, function () {
        var chunks, total, chunk, bytes, e_1_1;
        var _a, input_1, input_1_1;
        var _b, e_1, _c, _d;
        return __generator(this, function (_e) {
            switch (_e.label) {
                case 0:
                    chunks = [];
                    total = 0;
                    _e.label = 1;
                case 1:
                    _e.trys.push([1, 6, 7, 12]);
                    _a = true, input_1 = __asyncValues(input);
                    _e.label = 2;
                case 2: return [4 /*yield*/, input_1.next()];
                case 3:
                    if (!(input_1_1 = _e.sent(), _b = input_1_1.done, !_b)) return [3 /*break*/, 5];
                    _d = input_1_1.value;
                    _a = false;
                    chunk = _d;
                    bytes = Buffer.from(chunk);
                    total += bytes.byteLength;
                    if (total > maxInputBytes)
                        throw new Error('oversized input');
                    chunks.push(bytes);
                    _e.label = 4;
                case 4:
                    _a = true;
                    return [3 /*break*/, 2];
                case 5: return [3 /*break*/, 12];
                case 6:
                    e_1_1 = _e.sent();
                    e_1 = { error: e_1_1 };
                    return [3 /*break*/, 12];
                case 7:
                    _e.trys.push([7, , 10, 11]);
                    if (!(!_a && !_b && (_c = input_1.return))) return [3 /*break*/, 9];
                    return [4 /*yield*/, _c.call(input_1)];
                case 8:
                    _e.sent();
                    _e.label = 9;
                case 9: return [3 /*break*/, 11];
                case 10:
                    if (e_1) throw e_1.error;
                    return [7 /*endfinally*/];
                case 11: return [7 /*endfinally*/];
                case 12: return [2 /*return*/, Buffer.concat(chunks, total)];
            }
        });
    });
}
function isDeepSeekHookEvent(value) {
    return typeof value === 'string' && exports.deepseekHookEvents.includes(value);
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
