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
exports.installClineProjectSkill = installClineProjectSkill;
exports.uninstallClineProjectSkill = uninstallClineProjectSkill;
var node_crypto_1 = require("node:crypto");
var node_fs_1 = require("node:fs");
var promises_1 = require("node:fs/promises");
var node_path_1 = require("node:path");
var maxSkillBytes = 256 << 10;
var approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
var omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';
/**
 * Copy a Threadforge-generated, privacy-reviewed portable procedure into
 * Cline's project skill directory. The caller must pass the SHA-256 printed by
 * `threadforge procedure export-skill`; this detects edits between review and
 * installation. Cline's project MCP/user settings are never modified.
 */
function installClineProjectSkill(options) {
    return __awaiter(this, void 0, void 0, function () {
        var _a, root, skill, skillsRoot, releaseLock, targetDirectory, target, markerPath, existing, marker, targetWritten, error_1;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, resolvePortableSkill(options)];
                case 1:
                    _a = _b.sent(), root = _a.root, skill = _a.skill;
                    return [4 /*yield*/, ensureDirectories(root, ['.cline', 'skills'])];
                case 2:
                    skillsRoot = _b.sent();
                    return [4 /*yield*/, acquireSkillLock(skillsRoot, skill.name)];
                case 3:
                    releaseLock = _b.sent();
                    _b.label = 4;
                case 4:
                    _b.trys.push([4, , 15, 17]);
                    return [4 /*yield*/, ensureDirectories(root, ['.cline', 'skills', skill.name])];
                case 5:
                    targetDirectory = _b.sent();
                    target = node_path_1.default.join(targetDirectory, 'SKILL.md');
                    markerPath = node_path_1.default.join(targetDirectory, '.threadforge-owner.json');
                    return [4 /*yield*/, readOptionalRegularFile(target, maxSkillBytes)];
                case 6:
                    existing = _b.sent();
                    return [4 /*yield*/, readOwnershipMarker(markerPath)];
                case 7:
                    marker = _b.sent();
                    if (existing || marker) {
                        if (!existing || !marker) {
                            throw new Error("Cline skill '".concat(skill.name, "' already exists without a complete Threadforge ownership record; refusing to change it"));
                        }
                        assertOwned(marker, skill, target, root);
                        if (!existing.equals(skill.bytes))
                            throw new Error("Cline skill '".concat(skill.name, "' already exists with different content; refusing to overwrite it"));
                        return [2 /*return*/, result('unchanged', skill, target, root)];
                    }
                    targetWritten = false;
                    _b.label = 8;
                case 8:
                    _b.trys.push([8, 11, , 14]);
                    return [4 /*yield*/, writeExclusive(target, skill.bytes, 420)];
                case 9:
                    _b.sent();
                    targetWritten = true;
                    return [4 /*yield*/, writeExclusive(markerPath, Buffer.from("".concat(JSON.stringify(ownerFor(skill, target, root)), "\n"), 'utf8'), 384)];
                case 10:
                    _b.sent();
                    return [3 /*break*/, 14];
                case 11:
                    error_1 = _b.sent();
                    if (!targetWritten) return [3 /*break*/, 13];
                    return [4 /*yield*/, (0, promises_1.unlink)(target).catch(function () { })];
                case 12:
                    _b.sent();
                    _b.label = 13;
                case 13:
                    if (isCode(error_1, 'EEXIST')) {
                        throw new Error("Cline skill '".concat(skill.name, "' appeared during installation; inspect it before retrying"));
                    }
                    throw new Error('unable to write the Cline project skill');
                case 14: return [2 /*return*/, result('installed', skill, target, root)];
                case 15: return [4 /*yield*/, releaseLock()];
                case 16:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 17: return [2 /*return*/];
            }
        });
    });
}
/** Remove only the exact exported bytes named by the user's expected digest. */
function uninstallClineProjectSkill(options) {
    return __awaiter(this, void 0, void 0, function () {
        var _a, root, skill, skillsRoot, skillsRootStat, releaseLock, skillDirectory, directoryStat, target, markerPath, existing, marker;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0: return [4 /*yield*/, resolvePortableSkill(options)];
                case 1:
                    _a = _b.sent(), root = _a.root, skill = _a.skill;
                    skillsRoot = node_path_1.default.join(root, '.cline', 'skills');
                    return [4 /*yield*/, (0, promises_1.lstat)(skillsRoot).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 2:
                    skillsRootStat = _b.sent();
                    if (!skillsRootStat)
                        return [2 /*return*/, result('absent', skill, node_path_1.default.join(skillsRoot, skill.name, 'SKILL.md'), root)];
                    if (!skillsRootStat.isDirectory() || skillsRootStat.isSymbolicLink()) {
                        throw new Error('Cline project skill directory must be a real directory, not a symbolic link');
                    }
                    return [4 /*yield*/, acquireSkillLock(skillsRoot, skill.name)];
                case 3:
                    releaseLock = _b.sent();
                    _b.label = 4;
                case 4:
                    _b.trys.push([4, , 13, 15]);
                    skillDirectory = node_path_1.default.join(skillsRoot, skill.name);
                    return [4 /*yield*/, (0, promises_1.lstat)(skillDirectory).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 5:
                    directoryStat = _b.sent();
                    if (!directoryStat)
                        return [2 /*return*/, result('absent', skill, node_path_1.default.join(skillDirectory, 'SKILL.md'), root)];
                    if (!directoryStat.isDirectory() || directoryStat.isSymbolicLink()) {
                        throw new Error("Cline skill path '".concat(skill.name, "' is not a safe directory"));
                    }
                    target = node_path_1.default.join(skillDirectory, 'SKILL.md');
                    markerPath = node_path_1.default.join(skillDirectory, '.threadforge-owner.json');
                    return [4 /*yield*/, readOptionalRegularFile(target, maxSkillBytes)];
                case 6:
                    existing = _b.sent();
                    return [4 /*yield*/, readOwnershipMarker(markerPath)];
                case 7:
                    marker = _b.sent();
                    if (!existing && !marker)
                        return [2 /*return*/, result('absent', skill, target, root)];
                    if (!marker)
                        throw new Error("Cline skill '".concat(skill.name, "' has no Threadforge ownership record; refusing to remove it"));
                    assertOwned(marker, skill, target, root);
                    if (!!existing) return [3 /*break*/, 9];
                    return [4 /*yield*/, (0, promises_1.unlink)(markerPath)];
                case 8:
                    _b.sent();
                    return [2 /*return*/, result('removed', skill, target, root)];
                case 9:
                    if (!existing.equals(skill.bytes)) {
                        throw new Error("Cline skill '".concat(skill.name, "' was modified; refusing to remove user-edited content"));
                    }
                    return [4 /*yield*/, (0, promises_1.unlink)(target)];
                case 10:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.unlink)(markerPath)];
                case 11:
                    _b.sent();
                    // Preserve any user-added supporting files and directories.
                    return [4 /*yield*/, (0, promises_1.rmdir)(skillDirectory).catch(function () { })];
                case 12:
                    // Preserve any user-added supporting files and directories.
                    _b.sent();
                    return [2 /*return*/, result('removed', skill, target, root)];
                case 13: return [4 /*yield*/, releaseLock()];
                case 14:
                    _b.sent();
                    return [7 /*endfinally*/];
                case 15: return [2 /*return*/];
            }
        });
    });
}
function resolvePortableSkill(options) {
    return __awaiter(this, void 0, void 0, function () {
        var rootInput, rootInfo, root, sourceInput, relativeSourcePath, parts, fileInfo, sourceRealPath, bytes, afterRead, digest, text, name;
        var _a;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    if (!node_path_1.default.isAbsolute(options.projectRoot))
                        throw new Error('Cline project root must be an absolute path');
                    if (!options.skillFile.trim())
                        throw new Error('portable skill file path is required');
                    if (!/^[a-f0-9]{64}$/.test(options.sha256))
                        throw new Error('--sha256 must be a lowercase SHA-256 digest');
                    rootInput = node_path_1.default.resolve(options.projectRoot);
                    return [4 /*yield*/, (0, promises_1.lstat)(rootInput).catch(function () { return null; })];
                case 1:
                    rootInfo = _b.sent();
                    if (!(rootInfo === null || rootInfo === void 0 ? void 0 : rootInfo.isDirectory()) || rootInfo.isSymbolicLink()) {
                        throw new Error('Cline project root must be a real directory, not a symbolic link');
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(rootInput).catch(function () { throw new Error('Cline project root could not be resolved'); })];
                case 2:
                    root = _b.sent();
                    sourceInput = node_path_1.default.isAbsolute(options.skillFile)
                        ? node_path_1.default.resolve(options.skillFile)
                        : node_path_1.default.resolve(root, options.skillFile);
                    relativeSourcePath = node_path_1.default.relative(root, sourceInput);
                    parts = relativeSourcePath.split(node_path_1.default.sep);
                    if (!isWithin(root, sourceInput) || parts.length !== 5 || parts[0] !== '.threadforge' ||
                        parts[1] !== 'skills' || !parts[2] || !/^rev-\d{6}$/.test((_a = parts[3]) !== null && _a !== void 0 ? _a : '') || parts[4] !== 'SKILL.md') {
                        throw new Error('skill file must be an exported .threadforge/skills/<id>/rev-NNNNNN/SKILL.md artifact in this project');
                    }
                    return [4 /*yield*/, assertNoSymlinkPath(root, sourceInput)];
                case 3:
                    _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(sourceInput).catch(function () { return null; })];
                case 4:
                    fileInfo = _b.sent();
                    if (!(fileInfo === null || fileInfo === void 0 ? void 0 : fileInfo.isFile()) || fileInfo.isSymbolicLink())
                        throw new Error('portable skill must be a regular file, not a symbolic link');
                    if (fileInfo.size > maxSkillBytes)
                        throw new Error('portable skill exceeds the 256 KiB local limit');
                    return [4 /*yield*/, (0, promises_1.realpath)(sourceInput).catch(function () { return ''; })];
                case 5:
                    sourceRealPath = _b.sent();
                    if (sourceRealPath !== sourceInput)
                        throw new Error('portable skill path changed during validation');
                    return [4 /*yield*/, (0, promises_1.readFile)(sourceInput)];
                case 6:
                    bytes = _b.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(sourceInput).catch(function () { return null; })];
                case 7:
                    afterRead = _b.sent();
                    if (!(afterRead === null || afterRead === void 0 ? void 0 : afterRead.isFile()) || afterRead.isSymbolicLink() || afterRead.size !== fileInfo.size || afterRead.mtimeMs !== fileInfo.mtimeMs) {
                        throw new Error('portable skill changed while it was being read');
                    }
                    digest = (0, node_crypto_1.createHash)('sha256').update(bytes).digest('hex');
                    if (digest !== options.sha256)
                        throw new Error('portable skill SHA-256 does not match the reviewed export digest');
                    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
                    name = validateGeneratedSkill(text, parts[2], Number(parts[3].slice(4)));
                    return [2 /*return*/, { root: root, skill: { name: name, bytes: bytes, sha256: digest } }];
            }
        });
    });
}
function validateGeneratedSkill(text, graphID, revision) {
    var frontmatter = /^---\r?\nname: ("(?:\\.|[^"\\])*")\r?\ndescription: ("(?:\\.|[^"\\])*")\r?\n---\r?\n/.exec(text);
    if (!frontmatter)
        throw new Error('file is not a generated Threadforge portable SKILL.md');
    var name;
    var description;
    try {
        name = JSON.parse(frontmatter[1]);
        description = JSON.parse(frontmatter[2]);
    }
    catch (_a) {
        throw new Error('portable skill frontmatter is malformed');
    }
    if (typeof name !== 'string' || name.length > 64 || !/^threadforge-[a-z0-9]+(?:-[a-z0-9]+)*-r\d+$/.test(name)) {
        throw new Error('portable skill name is invalid for Cline project skills');
    }
    if (typeof description !== 'string' || description.length === 0 || description.length > 1024) {
        throw new Error('portable skill description is invalid for Cline project skills');
    }
    var identity = /^> Source procedure: (\S+) revision (\d+)$/m.exec(text);
    if (!identity || identity[1] !== graphID || Number(identity[2]) !== revision || !name.endsWith("-r".concat(revision))) {
        throw new Error('portable skill identity does not match its exported project path');
    }
    if (!text.includes(approvalNotice) || !text.includes(omissionNotice)) {
        throw new Error('portable skill is missing the Threadforge advisory/privacy notices');
    }
    return name;
}
function ensureDirectories(root, segments) {
    return __awaiter(this, void 0, void 0, function () {
        var current, _i, segments_1, segment, error_2, info;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    current = root;
                    _i = 0, segments_1 = segments;
                    _a.label = 1;
                case 1:
                    if (!(_i < segments_1.length)) return [3 /*break*/, 9];
                    segment = segments_1[_i];
                    current = node_path_1.default.join(current, segment);
                    _a.label = 2;
                case 2:
                    _a.trys.push([2, 4, , 5]);
                    return [4 /*yield*/, (0, promises_1.mkdir)(current, { mode: 493 })];
                case 3:
                    _a.sent();
                    return [3 /*break*/, 5];
                case 4:
                    error_2 = _a.sent();
                    if (!isCode(error_2, 'EEXIST'))
                        throw new Error('unable to create the Cline project skills directory');
                    return [3 /*break*/, 5];
                case 5: return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { return null; })];
                case 6:
                    info = _a.sent();
                    if (!(info === null || info === void 0 ? void 0 : info.isDirectory()) || info.isSymbolicLink()) {
                        throw new Error('Cline project skill path contains a non-directory or symbolic link');
                    }
                    return [4 /*yield*/, (0, promises_1.realpath)(current).catch(function () { return ''; })];
                case 7:
                    if ((_a.sent()) !== current) {
                        throw new Error('Cline project skill path changed during validation');
                    }
                    _a.label = 8;
                case 8:
                    _i++;
                    return [3 /*break*/, 1];
                case 9: return [2 /*return*/, current];
            }
        });
    });
}
function assertNoSymlinkPath(root, target) {
    return __awaiter(this, void 0, void 0, function () {
        var relative, current, _i, _a, segment, info;
        return __generator(this, function (_b) {
            switch (_b.label) {
                case 0:
                    relative = node_path_1.default.relative(root, target);
                    if (!isWithin(root, target))
                        throw new Error('portable skill path escapes the project root');
                    current = root;
                    _i = 0, _a = relative.split(node_path_1.default.sep).filter(Boolean);
                    _b.label = 1;
                case 1:
                    if (!(_i < _a.length)) return [3 /*break*/, 4];
                    segment = _a[_i];
                    current = node_path_1.default.join(current, segment);
                    return [4 /*yield*/, (0, promises_1.lstat)(current).catch(function () { return null; })];
                case 2:
                    info = _b.sent();
                    if (!info || info.isSymbolicLink())
                        throw new Error('portable skill path contains a missing or symbolic-link component');
                    _b.label = 3;
                case 3:
                    _i++;
                    return [3 /*break*/, 1];
                case 4: return [2 /*return*/];
            }
        });
    });
}
function readOptionalRegularFile(filePath, byteLimit) {
    return __awaiter(this, void 0, void 0, function () {
        var info, bytes, afterRead;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function (error) { return isCode(error, 'ENOENT') ? null : Promise.reject(error); })];
                case 1:
                    info = _a.sent();
                    if (!info)
                        return [2 /*return*/, undefined];
                    if (!info.isFile() || info.isSymbolicLink())
                        throw new Error('Cline skill target must be a regular file, not a symbolic link');
                    if (info.size > byteLimit)
                        throw new Error('existing Cline skill exceeds the local size limit');
                    return [4 /*yield*/, (0, promises_1.readFile)(filePath)];
                case 2:
                    bytes = _a.sent();
                    return [4 /*yield*/, (0, promises_1.lstat)(filePath).catch(function () { return null; })];
                case 3:
                    afterRead = _a.sent();
                    if (!(afterRead === null || afterRead === void 0 ? void 0 : afterRead.isFile()) || afterRead.isSymbolicLink() || afterRead.size !== info.size || afterRead.mtimeMs !== info.mtimeMs) {
                        throw new Error('Cline skill target changed while it was being read');
                    }
                    return [2 /*return*/, bytes];
            }
        });
    });
}
function readOwnershipMarker(filePath) {
    return __awaiter(this, void 0, void 0, function () {
        var bytes, value, marker;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0: return [4 /*yield*/, readOptionalRegularFile(filePath, 4096)];
                case 1:
                    bytes = _a.sent();
                    if (!bytes)
                        return [2 /*return*/, undefined];
                    try {
                        value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
                    }
                    catch (_b) {
                        throw new Error('Cline Threadforge ownership record is malformed');
                    }
                    if (!value || typeof value !== 'object' || Array.isArray(value))
                        throw new Error('Cline Threadforge ownership record is malformed');
                    marker = value;
                    if (Object.keys(marker).sort().join(',') !== 'path,schema_version,sha256,skill_name' ||
                        marker.schema_version !== 'threadforge.cline-skill-owner.v1' || typeof marker.skill_name !== 'string' ||
                        typeof marker.path !== 'string' || typeof marker.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(marker.sha256)) {
                        throw new Error('Cline Threadforge ownership record is malformed');
                    }
                    return [2 /*return*/, marker];
            }
        });
    });
}
function ownerFor(skill, target, root) {
    return {
        schema_version: 'threadforge.cline-skill-owner.v1',
        skill_name: skill.name,
        path: node_path_1.default.relative(root, target).split(node_path_1.default.sep).join('/'),
        sha256: skill.sha256,
    };
}
function assertOwned(marker, skill, target, root) {
    var expected = ownerFor(skill, target, root);
    if (marker.skill_name !== expected.skill_name || marker.path !== expected.path || marker.sha256 !== expected.sha256) {
        throw new Error("Cline skill '".concat(skill.name, "' ownership record does not match the reviewed export"));
    }
}
function writeExclusive(filePath, bytes, mode) {
    return __awaiter(this, void 0, void 0, function () {
        var handle, created, error_3;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    created = false;
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 6, , 10]);
                    return [4 /*yield*/, (0, promises_1.open)(filePath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, mode)];
                case 2:
                    handle = _a.sent();
                    created = true;
                    return [4 /*yield*/, handle.writeFile(bytes)];
                case 3:
                    _a.sent();
                    return [4 /*yield*/, handle.sync()];
                case 4:
                    _a.sent();
                    return [4 /*yield*/, handle.close()];
                case 5:
                    _a.sent();
                    return [3 /*break*/, 10];
                case 6:
                    error_3 = _a.sent();
                    return [4 /*yield*/, (handle === null || handle === void 0 ? void 0 : handle.close().catch(function () { }))];
                case 7:
                    _a.sent();
                    if (!created) return [3 /*break*/, 9];
                    return [4 /*yield*/, (0, promises_1.unlink)(filePath).catch(function () { })];
                case 8:
                    _a.sent();
                    _a.label = 9;
                case 9: throw error_3;
                case 10: return [2 /*return*/];
            }
        });
    });
}
function acquireSkillLock(skillsRoot, name) {
    return __awaiter(this, void 0, void 0, function () {
        var lockPath, handle, error_4;
        var _this = this;
        return __generator(this, function (_a) {
            switch (_a.label) {
                case 0:
                    lockPath = node_path_1.default.join(skillsRoot, ".threadforge-".concat(name, ".lock"));
                    _a.label = 1;
                case 1:
                    _a.trys.push([1, 3, , 4]);
                    return [4 /*yield*/, (0, promises_1.open)(lockPath, node_fs_1.constants.O_CREAT | node_fs_1.constants.O_EXCL | node_fs_1.constants.O_WRONLY, 384)];
                case 2:
                    handle = _a.sent();
                    return [3 /*break*/, 4];
                case 3:
                    error_4 = _a.sent();
                    if (isCode(error_4, 'EEXIST'))
                        throw new Error('Cline skill is locked by another installer or a stale lock; inspect it before retrying');
                    throw new Error('unable to lock the Cline project skill');
                case 4: return [2 /*return*/, function () { return __awaiter(_this, void 0, void 0, function () {
                        return __generator(this, function (_a) {
                            switch (_a.label) {
                                case 0: return [4 /*yield*/, handle.close().catch(function () { })];
                                case 1:
                                    _a.sent();
                                    return [4 /*yield*/, (0, promises_1.unlink)(lockPath).catch(function () { })];
                                case 2:
                                    _a.sent();
                                    return [2 /*return*/];
                            }
                        });
                    }); }];
            }
        });
    });
}
function result(action, skill, target, root) {
    return {
        schema_version: 'threadforge.cline-project-skill.v1',
        action: action,
        skill_name: skill.name,
        path: node_path_1.default.relative(root, target).split(node_path_1.default.sep).join('/'),
        sha256: skill.sha256,
    };
}
function isWithin(parent, child) {
    var relative = node_path_1.default.relative(parent, child);
    return relative !== '' && relative !== '..' && !relative.startsWith("..".concat(node_path_1.default.sep)) && !node_path_1.default.isAbsolute(relative);
}
function isCode(error, code) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
