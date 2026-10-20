"use strict";
// THREADFORGE_OMP_EXTENSION_TEMPLATE:v1
// Threadforge observational extension template for Oh My Pi (omp).
// The installer replaces the first line with a SHA-256 ownership marker.
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
exports.ompManagedMarker = exports.ompExtensionTemplateMarker = void 0;
exports.mapOmpEvent = mapOmpEvent;
exports.createThreadforgeOmpExtension = createThreadforgeOmpExtension;
exports.default = threadforgeOmpExtension;
var node_crypto_1 = require("node:crypto");
exports.ompExtensionTemplateMarker = '// THREADFORGE_OMP_EXTENSION_TEMPLATE:v1';
exports.ompManagedMarker = '// threadforge-managed-omp-extension:v1';
var maxContentBytes = 64 << 10;
var endpoint = 'http://127.0.0.1:4318/v1/events';
var subscribedEvents = [
    'session_start', 'session_shutdown', 'before_agent_start', 'tool_call', 'tool_result',
    'tool_approval_requested', 'tool_approval_resolved', 'user_bash', 'user_python', 'message_end',
];
/** Pure mapper for host fixtures; the installed extension registers the same event set below. */
function mapOmpEvent(value, context, capturedAt) {
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isRecord(value) || typeof value.type !== 'string')
        return [];
    var eventName = value.type;
    if (!subscribedEvents.includes(eventName))
        return [];
    var ctx = isRecord(context) ? context : {};
    var eventFields = baseEvent(value, ctx, capturedAt);
    var action = actionFor(eventName);
    if (eventName === 'message_end') {
        var message = isRecord(value.message) ? value.message : undefined;
        if ((message === null || message === void 0 ? void 0 : message.role) !== 'assistant')
            return [];
        var usage = normalizedUsage(message.usage);
        if (Object.keys(usage).length === 0)
            return [];
        eventFields.event = { kind: 'metric', action: 'token.usage', fidelity: 'observed' };
        eventFields.gen_ai = { usage: usage };
        var model = modelVersion(message.model);
        if (model)
            eventFields.harness = __assign(__assign({}, eventFields.harness), { version: model });
        return [eventFields];
    }
    if (!action)
        return [];
    eventFields.event = { kind: 'agent_runtime', action: action, fidelity: 'observed' };
    if (eventName === 'before_agent_start') {
        var prompt_1 = digest(value.prompt);
        if (prompt_1)
            eventFields.content = contentMetadata(digestFields('prompt', prompt_1));
    }
    if (eventName === 'tool_call' || eventName === 'tool_result') {
        var toolName = safeLabel(value.toolName, 128);
        if (toolName)
            eventFields.tool = { name: toolName };
        var callId = boundedString(value.toolCallId, 512);
        if (callId)
            eventFields.raw = __assign(__assign({}, (isRecord(eventFields.raw) ? eventFields.raw : {})), { tool_call_id_sha256: sha256(callId) });
        var input = digest(value.input);
        var hasOutput = Object.hasOwn(value, 'content') || Object.hasOwn(value, 'details');
        var output = eventName === 'tool_result' && hasOutput
            ? digest(__assign(__assign({}, (Object.hasOwn(value, 'content') ? { content: value.content } : {})), (Object.hasOwn(value, 'details') ? { details: value.details } : {})))
            : undefined;
        if (input || output)
            eventFields.content = contentMetadata(__assign(__assign({}, (input ? digestFields('input', input) : {})), (output ? digestFields('output', output) : {})));
        if (eventName === 'tool_result') {
            if (value.isError === true) {
                eventFields.event = { kind: 'agent_runtime', action: 'tool.failed', fidelity: 'observed' };
                eventFields.severity = 'medium';
            }
            else {
                var classified = classifyTool(toolName !== null && toolName !== void 0 ? toolName : '');
                if (classified.action)
                    eventFields.event = { kind: 'agent_runtime', action: classified.action, fidelity: 'observed' };
                if (classified.operation)
                    eventFields.file = { operation: classified.operation };
                if (classified.mcp)
                    eventFields.mcp = classified.mcp;
            }
        }
    }
    if (eventName === 'tool_approval_requested' || eventName === 'tool_approval_resolved') {
        var toolName = safeLabel(value.toolName, 128);
        if (toolName)
            eventFields.tool = { name: toolName };
        var callId = boundedString(value.toolCallId, 512);
        if (callId)
            eventFields.raw = __assign(__assign({}, (isRecord(eventFields.raw) ? eventFields.raw : {})), { tool_call_id_sha256: sha256(callId) });
        var mode = enumLabel(value.approvalMode, ['always-ask', 'write', 'yolo']);
        if (mode)
            eventFields.raw = __assign(__assign({}, (isRecord(eventFields.raw) ? eventFields.raw : {})), { approval_mode: mode });
        var reason = digest(value.reason);
        if (reason)
            eventFields.content = contentMetadata(digestFields('reason', reason));
        if (eventName === 'tool_approval_requested') {
            eventFields.approval = { required: true, reason: 'runtime approval request observed' };
        }
        else if (typeof value.approved === 'boolean') {
            eventFields.event = {
                kind: 'agent_runtime', action: value.approved ? 'approval.allowed' : 'approval.denied', fidelity: 'observed',
            };
            eventFields.approval = { required: true, decision: value.approved ? 'approve' : 'deny' };
        }
        else
            return [];
    }
    if (eventName === 'user_bash' || eventName === 'user_python') {
        var source = eventName === 'user_bash' ? value.command : value.code;
        var codeDigest = digest(source);
        if (codeDigest)
            eventFields.content = contentMetadata(digestFields('operator_input', codeDigest));
        eventFields.tool = { name: eventName };
        eventFields.raw = __assign(__assign(__assign(__assign({}, (isRecord(eventFields.raw) ? eventFields.raw : {})), { user_initiated: true }), (eventName === 'user_python' ? { user_python: true } : {})), (typeof value.excludeFromContext === 'boolean' ? { excluded_from_model_context: value.excludeFromContext } : {}));
    }
    return [eventFields];
}
function actionFor(eventName) {
    var actions = {
        session_start: 'session.started',
        session_shutdown: 'session.ended',
        before_agent_start: 'prompt.submitted',
        tool_call: 'tool.invoked',
        tool_result: 'tool.completed',
        tool_approval_requested: 'approval.requested',
        tool_approval_resolved: 'approval.result_observed',
        user_bash: 'command.executed',
        user_python: 'command.executed',
    };
    return actions[eventName];
}
function baseEvent(value, context, capturedAt) {
    var manager = isRecord(context.sessionManager) ? context.sessionManager : {};
    var sessionId = boundedString(value.sessionId, 512);
    if (!sessionId && typeof manager.getSessionId === 'function') {
        try {
            sessionId = boundedString(manager.getSessionId(), 512);
        }
        catch ( /* omit */_a) { /* omit */ }
    }
    var model = modelVersion(context.model);
    var event = {
        timestamp: capturedAt.toISOString(),
        vendor: 'beacon',
        product: 'endpoint-agent',
        schema_version: '1.0',
        event: { kind: 'agent_runtime', action: 'tool.invoked', fidelity: 'observed' },
        severity: 'info',
        endpoint: { os: normalizedOS(process.platform) },
        harness: __assign({ name: 'omp', collection_method: 'plugin' }, (model ? { version: model } : {})),
        hook: { name: String(value.type).slice(0, 64) },
    };
    if (sessionId)
        event.session = { id: "omp:".concat(sha256(sessionId)) };
    var mode = enumLabel(context.mode, ['tui', 'rpc', 'json', 'print']);
    if (mode)
        event.raw = { runtime_mode: mode };
    return event;
}
function classifyTool(name) {
    switch (name.toLowerCase()) {
        case 'bash': return { action: 'command.executed' };
        case 'read': return { action: 'file.read', operation: 'read' };
        case 'write': return { action: 'file.modified', operation: 'create' };
        case 'edit': return { action: 'file.modified', operation: 'modify' };
    }
    var mcp = /^mcp__([A-Za-z0-9.-]+)_([A-Za-z0-9_.-]+)$/.exec(name);
    if (mcp)
        return { action: 'mcp.tool_invoked', mcp: { server: mcp[1], tool: mcp[2] } };
    return {};
}
function normalizedUsage(value) {
    if (!isRecord(value))
        return {};
    var usage = {};
    for (var _i = 0, _a = [['input', 'input_tokens'], ['output', 'output_tokens']]; _i < _a.length; _i++) {
        var _b = _a[_i], source = _b[0], target = _b[1];
        var count = boundedCount(value[source]);
        if (count !== undefined)
            usage[target] = count;
    }
    var cacheRead = boundedCount(value.cacheRead);
    if (cacheRead !== undefined)
        usage.cache_read = { input_tokens: cacheRead };
    var cacheWrite = boundedCount(value.cacheWrite);
    if (cacheWrite !== undefined)
        usage.cache_creation = { input_tokens: cacheWrite };
    var reasoning = boundedCount(value.reasoning);
    if (reasoning !== undefined)
        usage.reasoning = { output_tokens: reasoning };
    var cost = isRecord(value.cost) ? boundedMoney(value.cost.total) : undefined;
    if (cost !== undefined)
        usage.cost_usd = cost;
    return usage;
}
function modelVersion(value) {
    var _a;
    if (!isRecord(value))
        return safeLabel(value, 128);
    var id = safeLabel((_a = value.id) !== null && _a !== void 0 ? _a : value.name, 96);
    var provider = safeLabel(value.provider, 64);
    return id ? "".concat(provider ? "".concat(provider, "_") : '').concat(id).slice(0, 128) : undefined;
}
function digest(value) {
    if (value === undefined || value === null)
        return undefined;
    if (typeof value === 'string') {
        var bytes_1 = Buffer.byteLength(value, 'utf8');
        return bytes_1 > maxContentBytes ? { bytes: bytes_1, omitted: true } : { bytes: bytes_1, sha256: sha256(value) };
    }
    var canonical = boundedCanonicalJSON(value);
    if (canonical === undefined)
        return undefined;
    var bytes = Buffer.byteLength(canonical, 'utf8');
    return { bytes: bytes, sha256: sha256(canonical) };
}
function boundedCanonicalJSON(value) {
    var pieces = [];
    var bytes = 0;
    var failed = false;
    var active = new WeakSet();
    var append = function (part) {
        bytes += Buffer.byteLength(part, 'utf8');
        if (bytes > maxContentBytes)
            failed = true;
        else
            pieces.push(part);
    };
    var visit = function (item, depth) {
        if (failed)
            return;
        if (depth > 8) {
            failed = true;
            return;
        }
        if (item === null) {
            append('null');
            return;
        }
        if (typeof item === 'string') {
            append(JSON.stringify(item));
            return;
        }
        if (typeof item === 'boolean' || typeof item === 'number') {
            append(Number.isFinite(item) || typeof item === 'boolean' ? JSON.stringify(item) : 'null');
            return;
        }
        if (Array.isArray(item)) {
            if (item.length > 4096) {
                failed = true;
                return;
            }
            var object = item;
            if (active.has(object)) {
                failed = true;
                return;
            }
            active.add(object);
            append('[');
            item.forEach(function (child, index) { if (index)
                append(','); visit(child, depth + 1); });
            append(']');
            active.delete(object);
            return;
        }
        if (isRecord(item)) {
            var keys = Object.keys(item).sort();
            if (keys.length > 4096 || active.has(item)) {
                failed = true;
                return;
            }
            active.add(item);
            append('{');
            keys.forEach(function (key, index) { if (index)
                append(','); append(JSON.stringify(key)); append(':'); visit(item[key], depth + 1); });
            append('}');
            active.delete(item);
            return;
        }
        append('null');
    };
    try {
        visit(value, 0);
    }
    catch (_a) {
        return undefined;
    }
    return failed ? undefined : pieces.join('');
}
function contentMetadata(fields) {
    return __assign({ retention: 'metadata', included: false }, fields);
}
function digestFields(prefix, value) {
    var _a, _b, _c;
    return __assign(__assign((_a = {}, _a["".concat(prefix, "_bytes")] = value.bytes, _a), (value.sha256 ? (_b = {}, _b["".concat(prefix, "_sha256")] = value.sha256, _b) : {})), (value.omitted ? (_c = {}, _c["".concat(prefix, "_omitted")] = true, _c) : {}));
}
function boundedString(value, max) {
    if (typeof value !== 'string')
        return undefined;
    var text = value.trim();
    return text.length > 0 && text.length <= max ? text : undefined;
}
function safeLabel(value, max) {
    var text = boundedString(value, max);
    return text && /^[A-Za-z0-9][A-Za-z0-9_.-]*$/.test(text) && !looksSecretLike(text) ? text : undefined;
}
function enumLabel(value, allowed) {
    var text = boundedString(value, 64);
    return text && allowed.includes(text) ? text : undefined;
}
function boundedCount(value) {
    return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 1000000000000 ? value : undefined;
}
function boundedMoney(value) {
    return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1000000 ? value : undefined;
}
function looksSecretLike(value) {
    return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
        /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
        /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function sha256(value) {
    return (0, node_crypto_1.createHash)('sha256').update(value, 'utf8').digest('hex');
}
function normalizedOS(platform) {
    if (platform === 'win32')
        return 'windows';
    if (platform === 'darwin')
        return 'macos';
    if (platform === 'linux')
        return 'linux';
    return 'other';
}
function mapOmpEventWithPending(value, context, pending, capturedAt) {
    if (capturedAt === void 0) { capturedAt = new Date(); }
    if (!isRecord(value) || typeof value.type !== 'string')
        return [];
    var id = boundedString(value.toolCallId, 512);
    if (value.type === 'tool_call' && id) {
        var name_1 = safeLabel(value.toolName, 128);
        var input = digest(value.input);
        pending.delete(id);
        pending.set(id, __assign(__assign({}, (name_1 ? { tool_name: name_1 } : {})), (input ? { input: input } : {})));
        while (pending.size > 64)
            pending.delete(pending.keys().next().value);
    }
    var payload = value;
    if ((value.type === 'tool_approval_requested' || value.type === 'tool_approval_resolved') && id) {
        var memo = pending.get(id);
        if (memo)
            payload = __assign(__assign({}, value), (memo.tool_name && !value.toolName ? { toolName: memo.tool_name } : {}));
    }
    var result = mapOmpEvent(payload, context, capturedAt);
    var event = result[0];
    if (event && isRecord(value)) {
        var memo = id ? pending.get(id) : undefined;
        if ((memo === null || memo === void 0 ? void 0 : memo.input) && (value.type === 'tool_approval_requested' || value.type === 'tool_approval_resolved')) {
            event.content = contentMetadata(__assign(__assign({}, (isRecord(event.content) ? event.content : {})), digestFields('decided_tool_input', memo.input)));
        }
    }
    if ((value.type === 'tool_result' || value.type === 'tool_approval_resolved') && id)
        pending.delete(id);
    if (value.type === 'session_start' || value.type === 'session_shutdown')
        pending.clear();
    return result;
}
function sendLocal(event) {
    try {
        void fetch(endpoint, {
            method: 'POST',
            headers: { 'content-type': 'application/json', accept: 'application/json' },
            body: JSON.stringify(event),
            signal: AbortSignal.timeout(250),
            redirect: 'error',
        }).then(function (response) {
            if (response.status !== 202)
                return;
        }).catch(function () { });
    }
    catch ( /* telemetry never changes an OMP callback result */_a) { /* telemetry never changes an OMP callback result */ }
}
function createThreadforgeOmpExtension() {
    var pending = new Map();
    var handler = function (event, context) {
        try {
            for (var _i = 0, _a = mapOmpEventWithPending(event, context, pending); _i < _a.length; _i++) {
                var payload = _a[_i];
                sendLocal(payload);
            }
        }
        catch ( /* observer callbacks stay fail-open and non-blocking */_b) { /* observer callbacks stay fail-open and non-blocking */ }
        // Never return a result: OMP interprets values from several extension events as behavior changes.
    };
    return {
        register: function (pi) {
            for (var _i = 0, subscribedEvents_1 = subscribedEvents; _i < subscribedEvents_1.length; _i++) {
                var name_2 = subscribedEvents_1[_i];
                pi.on(name_2, handler);
            }
        },
        subscribedEvents: __spreadArray([], subscribedEvents, true),
    };
}
function threadforgeOmpExtension(pi) {
    createThreadforgeOmpExtension().register(pi);
}
function isRecord(value) {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
