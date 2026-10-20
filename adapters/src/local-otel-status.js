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
exports.scanLocalOtelEventLog = scanLocalOtelEventLog;
var promises_1 = require("node:fs/promises");
var maxStatusTailBytes = 16 << 20;
var maxStatusLineBytes = 64 << 10;
/** Scan a bounded JSONL tail for one OTLP harness, returning aggregate evidence only. */
function scanLocalOtelEventLog(logPath_1, harnessName_1) {
    return __awaiter(this, arguments, void 0, function (logPath, harnessName, now) {
        var handle, pathInfo, openInfo, latestPathInfo, size, offset, bytes, bytesRead, content, olderEventsOmitted, firstBreak, status_1, latest, lines, _i, lines_1, line, event_1, timestamp, error_1;
        if (now === void 0) { now = new Date(); }
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    _a.trys.push([0, 6, 7, 9]);
                    return [4 /*yield*/, (0, promises_1.lstat)(logPath)];
                case 1:
                    pathInfo = _a.sent();
                    if (pathInfo.isSymbolicLink() || !pathInfo.isFile())
                        throw new Error('Threadforge local event log is not a regular file');
                    return [4 /*yield*/, (0, promises_1.open)(logPath, 'r')];
                case 2:
                    handle = _a.sent();
                    return [4 /*yield*/, handle.stat()];
                case 3:
                    openInfo = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(logPath)];
                case 4:
                    latestPathInfo = _a.sent();
                    if (latestPathInfo.isSymbolicLink() || !latestPathInfo.isFile())
                        throw new Error('Threadforge local event log changed to a symlink');
                    size = Math.min(openInfo.size, maxStatusTailBytes);
                    offset = Math.max(0, openInfo.size - size);
                    bytes = Buffer.alloc(size);
                    return [4 /*yield*/, handle.read(bytes, 0, size, offset)];
                case 5:
                    bytesRead = (_a.sent()).bytesRead;
                    content = bytes.subarray(0, bytesRead).toString('utf8');
                    olderEventsOmitted = offset > 0;
                    if (olderEventsOmitted) {
                        firstBreak = content.indexOf('\n');
                        content = firstBreak < 0 ? '' : content.slice(firstBreak + 1);
                    }
                    status_1 = {
                        present: true, complete: !olderEventsOmitted, older_events_omitted: olderEventsOmitted,
                        malformed_lines: 0, observed_events: 0, last_event_observed: false,
                    };
                    latest = 0;
                    lines = content.split(/\r?\n/);
                    if (lines.at(-1) === '')
                        lines.pop();
                    for (_i = 0, lines_1 = lines; _i < lines_1.length; _i++) {
                        line = lines_1[_i];
                        if (!line)
                            continue;
                        if (Buffer.byteLength(line, 'utf8') > maxStatusLineBytes) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        event_1 = void 0;
                        try {
                            event_1 = JSON.parse(line);
                        }
                        catch (_b) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        if (!isRecord(event_1) || !isOtelEvent(event_1, harnessName))
                            continue;
                        timestamp = typeof event_1.timestamp === 'string' ? Date.parse(event_1.timestamp) : Number.NaN;
                        if (!Number.isFinite(timestamp) || timestamp > now.getTime() + 60000) {
                            status_1.malformed_lines++;
                            continue;
                        }
                        status_1.observed_events++;
                        if (timestamp > latest)
                            latest = timestamp;
                    }
                    status_1.complete = status_1.complete && status_1.malformed_lines === 0;
                    if (status_1.observed_events) {
                        status_1.last_event_observed = true;
                        status_1.last_event_observed_at = new Date(latest).toISOString();
                    }
                    return [2 /*return*/, status_1];
                case 6:
                    error_1 = _a.sent();
                    if (isCode(error_1, 'ENOENT')) {
                        return [2 /*return*/, { present: false, complete: false, older_events_omitted: false, malformed_lines: 0, observed_events: 0, last_event_observed: false }];
                    }
                    throw new Error('Threadforge local event log is inaccessible');
                case 7: return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close())];
                case 8:
                    _a.sent();
                    return [7 /*endfinally*/];
                case 9: return [2 /*return*/];
            }
        });
    });
}
function isOtelEvent(value, name) {
    var harness = isRecord(value.harness) ? value.harness : {};
    return harness.name === name && harness.collection_method === 'otlp';
}
function isRecord(value) { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error, code) { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
