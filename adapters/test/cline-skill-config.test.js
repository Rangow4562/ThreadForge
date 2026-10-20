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
var strict_1 = require("node:assert/strict");
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var node_os_1 = require("node:os");
var node_path_1 = require("node:path");
var node_stream_1 = require("node:stream");
var node_test_1 = require("node:test");
var cli_ts_1 = require("../src/cli.ts");
var cline_skill_config_ts_1 = require("../src/cline-skill-config.ts");
var approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
var omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';
function fixture() {
    var root = (0, node_fs_1.mkdtempSync)(node_path_1.default.join((0, node_os_1.tmpdir)(), 'threadforge-cline-skill-'));
    var graphID = 'approved-procedure';
    var revision = 2;
    var file = node_path_1.default.join(root, '.threadforge', 'skills', graphID, 'rev-000002', 'SKILL.md');
    (0, node_fs_1.mkdirSync)(node_path_1.default.dirname(file), { recursive: true });
    var body = [
        '---',
        'name: "threadforge-approved-procedure-a1b2c3d4-r2"',
        'description: "Approved Threadforge procedure: review the change."',
        '---',
        '',
        '# Threadforge procedure',
        '',
        "> Source procedure: ".concat(graphID, " revision ").concat(revision),
        '',
        approvalNotice,
        '',
        '## Procedure',
        '',
        '### check: inspect',
        '',
        '> Inspect the current project state and report uncertainty.',
        '',
        omissionNotice,
        '',
    ].join('\n');
    var bytes = Buffer.from(body, 'utf8');
    (0, node_fs_1.writeFileSync)(file, bytes);
    return { root: root, file: file, bytes: bytes, sha256: (0, node_crypto_1.createHash)('sha256').update(bytes).digest('hex') };
}
function writableText() {
    var value = '';
    var stream = new node_stream_1.Writable({ write: function (chunk, _encoding, callback) { value += chunk.toString(); callback(); } });
    return { stream: stream, text: function () { return value; } };
}
(0, node_test_1.test)('installs only a digest-matched Threadforge export and preserves neighboring Cline skills', function () { return __awaiter(void 0, void 0, void 0, function () {
    var f, neighborDir, first, repeated, removed;
    return __generator(this, function (_a) {
        switch (_a.label) {
            case 0:
                f = fixture();
                _a.label = 1;
            case 1:
                _a.trys.push([1, , 5, 6]);
                neighborDir = node_path_1.default.join(f.root, '.cline', 'skills', 'user-skill');
                (0, node_fs_1.mkdirSync)(neighborDir, { recursive: true });
                (0, node_fs_1.writeFileSync)(node_path_1.default.join(neighborDir, 'SKILL.md'), 'user content\n');
                return [4 /*yield*/, (0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 })];
            case 2:
                first = _a.sent();
                strict_1.default.equal(first.action, 'installed');
                strict_1.default.equal(first.path, '.cline/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
                strict_1.default.deepEqual((0, node_fs_1.readFileSync)(node_path_1.default.join(f.root, first.path)), f.bytes);
                return [4 /*yield*/, (0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 })];
            case 3:
                repeated = _a.sent();
                strict_1.default.equal(repeated.action, 'unchanged');
                return [4 /*yield*/, (0, cline_skill_config_ts_1.uninstallClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 })];
            case 4:
                removed = _a.sent();
                strict_1.default.equal(removed.action, 'removed');
                strict_1.default.equal((0, node_fs_1.readFileSync)(node_path_1.default.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
                return [3 /*break*/, 6];
            case 5:
                (0, node_fs_1.rmSync)(f.root, { recursive: true, force: true });
                return [7 /*endfinally*/];
            case 6: return [2 /*return*/];
        }
    });
}); });
(0, node_test_1.test)('refuses digest mismatch, conflicting skills, edited installs, and non-export paths', function () { return __awaiter(void 0, void 0, void 0, function () {
    var f, destination, outside;
    return __generator(this, function (_a) {
        switch (_a.label) {
            case 0:
                f = fixture();
                _a.label = 1;
            case 1:
                _a.trys.push([1, , 8, 9]);
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: '0'.repeat(64) }), /SHA-256 does not match/)];
            case 2:
                _a.sent();
                destination = node_path_1.default.join(f.root, '.cline', 'skills', 'threadforge-approved-procedure-a1b2c3d4-r2');
                (0, node_fs_1.mkdirSync)(destination, { recursive: true });
                (0, node_fs_1.writeFileSync)(node_path_1.default.join(destination, 'SKILL.md'), 'user-owned skill\n');
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }), /without a complete Threadforge ownership record/)];
            case 3:
                _a.sent();
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.uninstallClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }), /no Threadforge ownership record/)];
            case 4:
                _a.sent();
                (0, node_fs_1.rmSync)(destination, { recursive: true, force: true });
                return [4 /*yield*/, (0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 })];
            case 5:
                _a.sent();
                (0, node_fs_1.writeFileSync)(node_path_1.default.join(destination, 'SKILL.md'), Buffer.concat([f.bytes, Buffer.from('\nuser edit\n')]));
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.uninstallClineProjectSkill)({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }), /modified; refusing to remove/)];
            case 6:
                _a.sent();
                outside = node_path_1.default.join(f.root, 'SKILL.md');
                (0, node_fs_1.writeFileSync)(outside, f.bytes);
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.installClineProjectSkill)({ projectRoot: f.root, skillFile: outside, sha256: f.sha256 }), /must be an exported/)];
            case 7:
                _a.sent();
                return [3 /*break*/, 9];
            case 8:
                (0, node_fs_1.rmSync)(f.root, { recursive: true, force: true });
                return [7 /*endfinally*/];
            case 9: return [2 /*return*/];
        }
    });
}); });
(0, node_test_1.test)('Cline CLI installs and removes a reviewed exported procedure using its printed digest', function () { return __awaiter(void 0, void 0, void 0, function () {
    var f, stdout, stderr, base, _a, _b, installed, removeOut, _c, _d, removed;
    return __generator(this, function (_e) {
        switch (_e.label) {
            case 0:
                f = fixture();
                stdout = writableText();
                stderr = writableText();
                base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
                _e.label = 1;
            case 1:
                _e.trys.push([1, , 4, 5]);
                _b = (_a = strict_1.default).equal;
                return [4 /*yield*/, (0, cli_ts_1.runAdapterCLI)(__spreadArray(['cline', 'skills', 'install'], base, true), stdout.stream, stderr.stream)];
            case 2:
                _b.apply(_a, [_e.sent(), 0, stderr.text()]);
                installed = JSON.parse(stdout.text());
                strict_1.default.equal(installed.family, 'cline');
                strict_1.default.equal(installed.action, 'install');
                strict_1.default.equal(installed.result.action, 'installed');
                strict_1.default.equal(installed.result.sha256, f.sha256);
                removeOut = writableText();
                _d = (_c = strict_1.default).equal;
                return [4 /*yield*/, (0, cli_ts_1.runAdapterCLI)(__spreadArray(['cline', 'skills', 'uninstall'], base, true), removeOut.stream, stderr.stream)];
            case 3:
                _d.apply(_c, [_e.sent(), 0, stderr.text()]);
                removed = JSON.parse(removeOut.text());
                strict_1.default.equal(removed.result.action, 'removed');
                return [3 /*break*/, 5];
            case 4:
                (0, node_fs_1.rmSync)(f.root, { recursive: true, force: true });
                return [7 /*endfinally*/];
            case 5: return [2 /*return*/];
        }
    });
}); });
(0, node_test_1.test)('rejects symlinked portable-skill inputs without following them', function (t) { return __awaiter(void 0, void 0, void 0, function () {
    var f, link;
    return __generator(this, function (_a) {
        switch (_a.label) {
            case 0:
                f = fixture();
                link = node_path_1.default.join(f.root, '.threadforge', 'skills', 'linked');
                _a.label = 1;
            case 1:
                _a.trys.push([1, , 3, 4]);
                try {
                    (0, node_fs_1.symlinkSync)(node_path_1.default.join(f.root, '.threadforge', 'skills', 'approved-procedure'), link, 'dir');
                }
                catch (_b) {
                    t.skip('directory symlink creation is unavailable');
                    return [2 /*return*/];
                }
                strict_1.default.equal((0, node_fs_1.lstatSync)(link).isSymbolicLink(), true);
                return [4 /*yield*/, strict_1.default.rejects((0, cline_skill_config_ts_1.installClineProjectSkill)({
                        projectRoot: f.root,
                        skillFile: node_path_1.default.join(link, 'rev-000002', 'SKILL.md'),
                        sha256: f.sha256,
                    }), /symbolic-link component/)];
            case 2:
                _a.sent();
                return [3 /*break*/, 4];
            case 3:
                (0, node_fs_1.rmSync)(f.root, { recursive: true, force: true });
                return [7 /*endfinally*/];
            case 4: return [2 /*return*/];
        }
    });
}); });
