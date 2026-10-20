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
exports.defaultEventEndpoint = void 0;
exports.emitEvent = emitEvent;
exports.emitEvents = emitEvents;
exports.assertLoopbackEndpoint = assertLoopbackEndpoint;
exports.defaultEventEndpoint = 'http://127.0.0.1:4318/v1/events';
var maxBatch = 100;
var maxRequestBytes = 8 << 20;
function emitEvent(event_1) {
    return __awaiter(this, arguments, void 0, function (event, options) {
        if (options === void 0) { options = {}; }
        return __generator(this, function (_a) {
            return [2 /*return*/, emitEvents([event], options)];
        });
    });
}
function emitEvents(events_1) {
    return __awaiter(this, arguments, void 0, function (events, options) {
        var _i, _a, _b, index, event_1, endpoint, timeoutMs, fetchImpl, body, response;
        var _c, _d, _e;
        if (options === void 0) { options = {}; }
        return __generator(this, function (_f) {
            switch (_f.label) {
                case 0:
                    if (!Array.isArray(events) || events.length < 1 || events.length > maxBatch) {
                        throw new Error("event batch must contain 1 to ".concat(maxBatch, " events"));
                    }
                    for (_i = 0, _a = events.entries(); _i < _a.length; _i++) {
                        _b = _a[_i], index = _b[0], event_1 = _b[1];
                        validateEvent(event_1, index);
                    }
                    endpoint = (_c = options.endpoint) !== null && _c !== void 0 ? _c : exports.defaultEventEndpoint;
                    assertLoopbackEndpoint(endpoint);
                    timeoutMs = (_d = options.timeoutMs) !== null && _d !== void 0 ? _d : 1500;
                    if (!Number.isInteger(timeoutMs) || timeoutMs < 100 || timeoutMs > 10000) {
                        throw new Error('timeoutMs must be an integer between 100 and 10000');
                    }
                    fetchImpl = (_e = options.fetchImpl) !== null && _e !== void 0 ? _e : fetch;
                    body = JSON.stringify(events.length === 1 ? events[0] : events);
                    if (new TextEncoder().encode(body).byteLength > maxRequestBytes) {
                        throw new Error("event batch exceeds the ".concat(maxRequestBytes, "-byte local intake limit"));
                    }
                    return [4 /*yield*/, fetchImpl(endpoint, {
                            method: 'POST',
                            headers: { 'content-type': 'application/json', accept: 'application/json' },
                            body: body,
                            signal: AbortSignal.timeout(timeoutMs),
                            redirect: 'error',
                        })];
                case 1:
                    response = _f.sent();
                    if (response.status !== 202) {
                        throw new Error("Threadforge local intake rejected event batch (".concat(response.status, ")"));
                    }
                    return [2 /*return*/, { accepted: events.length, status: response.status }];
            }
        });
    });
}
function validateEvent(value, index) {
    if (!isRecord(value) || value.vendor !== 'beacon' || value.product !== 'endpoint-agent') {
        throw new Error("events[".concat(index, "] must use the Beacon event contract"));
    }
    if (typeof value.timestamp !== 'string' || Number.isNaN(Date.parse(value.timestamp))) {
        throw new Error("events[".concat(index, "].timestamp must be an ISO date-time"));
    }
    if (typeof value.schema_version !== 'string' || value.schema_version.length < 1 || value.schema_version.length > 32) {
        throw new Error("events[".concat(index, "].schema_version is required"));
    }
    if (!isRecord(value.event) || !nonEmpty(value.event.kind, 128) || !nonEmpty(value.event.action, 256)) {
        throw new Error("events[".concat(index, "] requires bounded event.kind and event.action"));
    }
    if (!['info', 'low', 'medium', 'high', 'critical'].includes(String(value.severity))) {
        throw new Error("events[".concat(index, "].severity is invalid"));
    }
    if (!isRecord(value.endpoint) || !nonEmpty(value.endpoint.os, 64)) {
        throw new Error("events[".concat(index, "].endpoint.os is required"));
    }
    if (!isRecord(value.harness) || !nonEmpty(value.harness.name, 128)) {
        throw new Error("events[".concat(index, "].harness.name is required"));
    }
}
function assertLoopbackEndpoint(value) {
    var parsed;
    try {
        parsed = new URL(value);
    }
    catch (_a) {
        throw new Error('event endpoint must be a valid loopback HTTP URL');
    }
    var hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    var isIPv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(hostname) &&
        hostname.split('.').every(function (segment) { return Number(segment) <= 255; });
    if (parsed.protocol !== 'http:' || parsed.username || parsed.password || parsed.search || parsed.hash ||
        !(hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1' || isIPv4Loopback) ||
        parsed.pathname !== '/v1/events') {
        throw new Error('event endpoint must be loopback HTTP at /v1/events; remote forwarding is not enabled by this adapter');
    }
}
function nonEmpty(value, max) {
    return typeof value === 'string' && value.trim().length > 0 && value.length <= max;
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
