import hashlib
import importlib.util
import io
import json
import sys
import tarfile
import zipfile
from pathlib import Path
import subprocess

import pytest


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "verify_release_candidate.py"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_release_candidate", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the release-candidate verifier")
VERIFIER = importlib.util.module_from_spec(SCRIPT_SPEC)
sys.modules[SCRIPT_SPEC.name] = VERIFIER
SCRIPT_SPEC.loader.exec_module(VERIFIER)


VERSION = "1.2.3"
SOURCE_COMMIT = "a" * 40
SOURCE_REF = "refs/heads/main"


def archive_bytes(files, prefix):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        for name, data in files.items():
            member = tarfile.TarInfo(f"{prefix}/{name}")
            member.size = len(data)
            member.mode = 0o644
            archive.addfile(member, io.BytesIO(data))
    return output.getvalue()


def go_evidence(target):
    goos, goarch = target.split("_")
    notice = b"Synthetic notice\n"
    document = {
        "schema": "threadforge.go-license-evidence.v1",
        "source_commit": SOURCE_COMMIT,
        "target": {"goos": goos, "goarch": goarch},
        "main_package": "./cmd/threadforge",
        "scanner": {
            "module": "github.com/google/go-licenses/v2",
            "version": "v2.0.1",
            "binary_sha256": "b" * 64,
        },
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "license_record_count": 1,
        "license_url_unknown_count": 0,
        "unknown_license_expression_count": 0,
        "saved_notice_file_count": 1,
        "saved_notice_total_bytes": len(notice),
        "saved_notice_files": [
            {"path": "example/LICENSE", "sha256": hashlib.sha256(notice).hexdigest(), "size_bytes": len(notice)}
        ],
    }
    return archive_bytes(
        {
            "README.md": b"Unreviewed evidence.\n",
            "evidence.json": json.dumps(document).encode(),
            "report.csv": b"package,license_url,license_expression\nexample,unknown,MIT\n",
            "licenses/example/LICENSE": notice,
        },
        "go-license-evidence",
    )


def python_evidence(system, python_version):
    notice = b"Python notice\n"
    document = {
        "schema": "threadforge.python-license-evidence.v2",
        "source_commit": SOURCE_COMMIT,
        "release_version": VERSION,
        "target": {"os": system, "python_version": python_version},
        "scope": {"project": "threadforge-python", "project_version": "1.0.0", "extras": VERIFIER.PYTHON_EXTRAS},
        "locked_package_count": 2,
        "installed_distribution_count": 1,
        "unlocked_distribution_count": 0,
        "license_review_status": "unreviewed",
        "license_review_required": True,
        "release_clearance": False,
        "license_summary": {
            "license_file_count": 1,
            "license_file_bytes": len(notice),
            "distributions_without_license_files": 0,
            "distributions_without_license_metadata": 0,
            "malformed_license_metadata_count": 0,
        },
        "input_hashes": {"uv_lock_sha256": "d" * 64, "pyproject_sha256": "e" * 64},
        "distributions": [{"name": "sample", "normalized_name": "sample", "version": "1.0", "locked": True}],
    }
    return archive_bytes(
        {
            "README.md": b"Unreviewed evidence.\n",
            "evidence.json": json.dumps(document).encode(),
            "report.csv": b"distribution,version\nsample,1.0\n",
            "licenses/sample/LICENSE": notice,
        },
        "python-license-evidence",
    )


def python_release_distribution(*, metadata_version=VERSION, omit_wheel_license=None, sdist_traversal=False):
    metadata = f"Metadata-Version: 2.1\nName: threadforge-python\nVersion: {metadata_version}\n\n".encode()
    source_files = {name: b"verified package fixture\n" for name in VERIFIER.PYTHON_DISTRIBUTION_FILES}
    source_files.update({
        "LICENSE.threadforge": b"Apache-2.0 project license\n",
        "NOTICE.threadforge": b"Threadforge notice\n",
        "THIRD_PARTY.md": b"Third-party notices\n",
        "PKG-INFO": metadata,
        "LICENSE.anyjev": b"AnyJev license notice\n",
        "LICENSE.rrsi": b"RRSI license notice\n",
        "CREDITS.anyjev.md": b"AnyJev credits\n",
        "UPSTREAM_MODIFICATIONS.md": b"Upstream modifications\n",
    })
    if sdist_traversal:
        source_files["../escape"] = b"unsafe"
    sdist = archive_bytes(source_files, f"threadforge_python-{VERSION}")

    dist_info = f"threadforge_python-{VERSION}.dist-info"
    wheel_files = {name: b"verified package fixture\n" for name in VERIFIER.PYTHON_DISTRIBUTION_FILES}
    wheel_files.update({
        f"{dist_info}/METADATA": metadata,
        f"{dist_info}/RECORD": b"",
    })
    if omit_wheel_license != "LICENSE.anyjev":
        wheel_files[f"{dist_info}/licenses/LICENSE.anyjev"] = b"AnyJev license notice\n"
    if omit_wheel_license != "LICENSE.rrsi":
        wheel_files[f"{dist_info}/licenses/LICENSE.rrsi"] = b"RRSI license notice\n"
    if omit_wheel_license != "LICENSE.threadforge":
        wheel_files[f"{dist_info}/licenses/LICENSE.threadforge"] = b"Apache-2.0 project license\n"
    wheel_output = io.BytesIO()
    with zipfile.ZipFile(wheel_output, "w", compression=zipfile.ZIP_STORED) as archive:
        for name, data in wheel_files.items():
            archive.writestr(name, data)
    return sdist, wheel_output.getvalue()


def python_lock_sbom():
    binding = {
        "schema": "threadforge.python-lock-sbom-binding.v1",
        "source_commit": SOURCE_COMMIT,
        "release_version": VERSION,
        "uv_lock_sha256": "d" * 64,
        "pyproject_sha256": "e" * 64,
        "package_record_count": 2,
        "extras": VERIFIER.PYTHON_EXTRAS,
    }
    binding_text = json.dumps(binding, sort_keys=True, separators=(",", ":"))
    digest = hashlib.sha256(binding_text.encode()).hexdigest()
    root_id = "SPDXRef-Package-000001"
    dependency_id = "SPDXRef-Package-000002"

    def package(package_id, name, version, purpose, *, source_info=None):
        result = {
            "SPDXID": package_id,
            "name": name,
            "versionInfo": version,
            "downloadLocation": "NOASSERTION",
            "filesAnalyzed": False,
            "licenseConcluded": "NOASSERTION",
            "licenseDeclared": "NOASSERTION",
            "copyrightText": "NOASSERTION",
            "externalRefs": [{
                "referenceCategory": "PACKAGE-MANAGER",
                "referenceType": "purl",
                "referenceLocator": f"pkg:pypi/{name}@{version}",
            }],
        }
        if purpose is not None:
            result["primaryPackagePurpose"] = purpose
        if source_info is not None:
            result["sourceInfo"] = source_info
        return result

    return {
        "spdxVersion": "SPDX-2.3",
        "dataLicense": "CC0-1.0",
        "SPDXID": "SPDXRef-DOCUMENT",
        "name": f"threadforge-python-{VERSION}-all-extras-lock",
        "documentNamespace": f"https://threadforge.dev/spdx/python-lock/{VERSION}/{digest}",
        "creationInfo": {
            "creators": ["Tool: Threadforge Python lock SBOM builder"],
            "created": "2026-11-11T00:00:00Z",
            "comment": "threadforge.python-lock-sbom-binding.v1 " + binding_text,
        },
        "documentDescribes": [root_id],
        "packages": [
            package(
                root_id,
                "threadforge-python",
                VERSION,
                "APPLICATION",
                source_info=(
                    f"Candidate version {VERSION}; all declared extras resolved from uv.lock SHA-256 {'d' * 64} "
                    f"and pyproject.toml SHA-256 {'e' * 64}."
                ),
            ),
            package(dependency_id, "sample", "1.0", None),
        ],
        "relationships": [
            {
                "spdxElementId": "SPDXRef-DOCUMENT",
                "relationshipType": "DESCRIBES",
                "relatedSpdxElement": root_id,
            },
            {"spdxElementId": root_id, "relationshipType": "DEPENDS_ON", "relatedSpdxElement": dependency_id},
        ],
    }


def write_candidate(root):
    root.mkdir()
    for target in VERIFIER.GO_TARGETS:
        evidence_name = f"threadforge_{VERSION}_{target}.go-licenses.tar.gz"
        evidence = go_evidence(target)
        (root / evidence_name).write_bytes(evidence)
        archive_name = f"threadforge_{VERSION}_{target}{'.zip' if target == 'windows_amd64' else '.tar.gz'}"
        members = {
            "LICENSE": b"Apache License\n",
            "NOTICE": b"Notice\n",
            "THIRD_PARTY.md": b"Third-party notices\n",
            "THIRD_PARTY_LICENSES.tar.gz": evidence,
            "threadforge.exe" if target == "windows_amd64" else "threadforge": b"synthetic executable",
        }
        if target == "windows_amd64":
            with zipfile.ZipFile(root / archive_name, "w", compression=zipfile.ZIP_STORED) as archive:
                for name, data in members.items():
                    archive.writestr(name, data)
        else:
            with tarfile.open(root / archive_name, "w:gz") as archive:
                for name, data in members.items():
                    member = tarfile.TarInfo(name)
                    member.size = len(data)
                    archive.addfile(member, io.BytesIO(data))
        sbom = {
            "spdxVersion": "SPDX-2.3",
            "name": "threadforge.exe" if target == "windows_amd64" else "threadforge",
            "packages": [],
            "files": [],
            "relationships": [],
        }
        (root / f"threadforge_{VERSION}_{target}.spdx.json").write_text(json.dumps(sbom), encoding="utf-8")
    for system, python_version in VERIFIER.PYTHON_TARGETS:
        name = f"threadforge_{VERSION}_python_{system}_py{python_version}.license-evidence.tar.gz"
        (root / name).write_bytes(python_evidence(system, python_version))
    sdist, wheel = python_release_distribution()
    (root / f"threadforge_python-{VERSION}.tar.gz").write_bytes(sdist)
    (root / f"threadforge_python-{VERSION}-py3-none-any.whl").write_bytes(wheel)
    (root / f"threadforge_{VERSION}_python.spdx.json").write_text(json.dumps(python_lock_sbom()), encoding="utf-8")
    payloads = sorted(VERIFIER.expected_payload_names(VERSION))
    lines = [f"{hashlib.sha256((root / name).read_bytes()).hexdigest()}  {name}" for name in payloads]
    (root / "checksums.txt").write_text("\n".join(lines) + "\n", encoding="ascii")
    return root


class SuccessfulGH:
    def __init__(self, *, wrong_commit=False, wrong_spdx=False):
        self.calls = []
        self.wrong_commit = wrong_commit
        self.wrong_spdx = wrong_spdx

    def __call__(self, command, **kwargs):
        self.calls.append((command, kwargs))
        artifact = Path(command[3])
        predicate_type = command[command.index("--predicate-type") + 1]
        source_ref = command[command.index("--source-ref") + 1]
        source_commit = command[command.index("--source-digest") + 1]
        digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
        subject_name = artifact.name
        if predicate_type == VERIFIER.SPDX_PREDICATE:
            target = next(target for target in VERIFIER.GO_TARGETS if f"_{target}." in artifact.name)
            subject_name = f"threadforge_{VERSION}_{target}{'.zip' if target == 'windows_amd64' else '.tar.gz'}"
            subject_path = artifact.parent / subject_name
            digest = hashlib.sha256(subject_path.read_bytes()).hexdigest()
            spdx = json.loads((artifact.parent / f"threadforge_{VERSION}_{target}.spdx.json").read_text())
            if self.wrong_spdx:
                spdx["name"] = "other"
            predicate = spdx
        else:
            predicate = {
                "buildDefinition": {
                    "buildType": "https://actions.github.io/buildtypes/workflow/v1",
                    "externalParameters": {
                        "workflow": {
                            "path": VERIFIER.WORKFLOW,
                            "ref": source_ref,
                            "repository": f"https://github.com/{VERIFIER.REPOSITORY}",
                        }
                    },
                    "resolvedDependencies": [
                        {
                            "uri": f"git+https://github.com/{VERIFIER.REPOSITORY}@{source_ref}",
                            "digest": {"gitCommit": "c" * 40 if self.wrong_commit else source_commit},
                        }
                    ],
                }
            }
        statement = {
            "_type": "https://in-toto.io/Statement/v1",
            "predicateType": predicate_type,
            "subject": [{"name": subject_name, "digest": {"sha256": digest}}],
            "predicate": predicate,
        }
        payload = json.dumps([{"verificationResult": {"statement": statement}}])
        return subprocess.CompletedProcess(command, 0, stdout=payload, stderr="")


def run_verification(root, runner):
    return VERIFIER.verify_release_candidate(
        bundle_dir=root,
        version=VERSION,
        source_commit=SOURCE_COMMIT,
        source_ref=SOURCE_REF,
        run=runner,
    )


def test_verifies_exact_bundle_and_all_signed_claims(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    runner = SuccessfulGH()

    result = run_verification(root, runner)

    assert result["schema"] == "threadforge.release-candidate-verification.v1"
    assert result["payload_count"] == 27
    assert result["checksum_count"] == 27
    assert result["slsa_attestations_verified"] == 28
    assert result["archive_sbom_attestations_verified"] == 4
    assert result["python_lock_sboms_verified"] == 1
    assert result["python_distribution_artifacts_verified"] == 2
    assert result["integrity_verified"] is True
    assert result["provenance_verified"] is True
    assert result["license_review_required"] is True
    assert result["release_clearance"] is False
    assert result["release_authorized"] is False
    assert len(runner.calls) == 32
    for command, options in runner.calls:
        assert command[command.index("--repo") + 1] == VERIFIER.REPOSITORY
        assert command[command.index("--signer-workflow") + 1] == f"{VERIFIER.REPOSITORY}/{VERIFIER.WORKFLOW}"
        assert command[command.index("--source-ref") + 1] == SOURCE_REF
        assert command[command.index("--source-digest") + 1] == SOURCE_COMMIT
        assert options["timeout"] == 120


def test_rejects_checksum_mismatch_before_network_calls(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    archive = next(root.glob("threadforge_1.2.3_linux_amd64.tar.gz"))
    archive.write_bytes(archive.read_bytes() + b"tamper")
    runner = SuccessfulGH()

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="checksum mismatch"):
        run_verification(root, runner)
    assert runner.calls == []


def test_rejects_dot_segments_in_repository_before_network_calls(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    runner = SuccessfulGH()

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="repository must use owner/name"):
        VERIFIER.verify_release_candidate(
            bundle_dir=root,
            version=VERSION,
            source_commit=SOURCE_COMMIT,
            source_ref=SOURCE_REF,
            repository="../threadforge",
            run=runner,
        )
    assert runner.calls == []


def test_rejects_extra_files_and_duplicate_checksums(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    (root / "unexpected.txt").write_text("extra", encoding="utf-8")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="exactly the documented payload set"):
        run_verification(root, SuccessfulGH())

    (root / "unexpected.txt").unlink()
    checksums = root / "checksums.txt"
    checksums.write_text(checksums.read_text() + checksums.read_text().splitlines()[0] + "\n", encoding="ascii")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="malformed|duplicate"):
        run_verification(root, SuccessfulGH())


def test_rejects_python_lock_sbom_not_bound_to_all_profile_lock_hashes(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    name = f"threadforge_{VERSION}_python.spdx.json"
    sbom = json.loads((root / name).read_text(encoding="utf-8"))
    prefix = "threadforge.python-lock-sbom-binding.v1 "
    binding = json.loads(sbom["creationInfo"]["comment"][len(prefix):])
    binding["uv_lock_sha256"] = "f" * 64
    binding_text = json.dumps(binding, sort_keys=True, separators=(",", ":"))
    sbom["creationInfo"]["comment"] = prefix + binding_text
    digest = hashlib.sha256(binding_text.encode()).hexdigest()
    sbom["documentNamespace"] = f"https://threadforge.dev/spdx/python-lock/{VERSION}/{digest}"
    (root / name).write_text(json.dumps(sbom), encoding="utf-8")
    _rewrite_checksums(root)
    runner = SuccessfulGH()

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="does not match the candidate evidence"):
        run_verification(root, runner)
    assert runner.calls == []


def test_rejects_python_lock_sbom_not_bound_to_the_project_manifest_hash(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    name = f"threadforge_{VERSION}_python.spdx.json"
    sbom = json.loads((root / name).read_text(encoding="utf-8"))
    prefix = "threadforge.python-lock-sbom-binding.v1 "
    binding = json.loads(sbom["creationInfo"]["comment"][len(prefix):])
    binding["pyproject_sha256"] = "f" * 64
    binding_text = json.dumps(binding, sort_keys=True, separators=(",", ":"))
    sbom["creationInfo"]["comment"] = prefix + binding_text
    digest = hashlib.sha256(binding_text.encode()).hexdigest()
    sbom["documentNamespace"] = f"https://threadforge.dev/spdx/python-lock/{VERSION}/{digest}"
    (root / name).write_text(json.dumps(sbom), encoding="utf-8")
    _rewrite_checksums(root)

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="does not match the candidate evidence"):
        run_verification(root, SuccessfulGH())


def test_rejects_python_lock_sbom_that_asserts_unreviewed_licenses(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    name = f"threadforge_{VERSION}_python.spdx.json"
    sbom = json.loads((root / name).read_text(encoding="utf-8"))
    sbom["packages"][1]["licenseDeclared"] = "MIT"
    (root / name).write_text(json.dumps(sbom), encoding="utf-8")
    _rewrite_checksums(root)

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="makes license claims"):
        run_verification(root, SuccessfulGH())


def test_rejects_unreviewed_evidence_that_claims_clearance(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    name = f"threadforge_{VERSION}_linux_amd64.go-licenses.tar.gz"
    evidence = go_evidence("linux_amd64")
    with tarfile.open(fileobj=io.BytesIO(evidence), mode="r:gz") as archive:
        members = {member.name.removeprefix("go-license-evidence/"): archive.extractfile(member).read() for member in archive}
    manifest = json.loads(members["evidence.json"])
    manifest["release_clearance"] = True
    members["evidence.json"] = json.dumps(manifest).encode()
    tampered = archive_bytes(members, "go-license-evidence")
    (root / name).write_bytes(tampered)
    cli_archive = root / f"threadforge_{VERSION}_linux_amd64.tar.gz"
    cli_members = VERIFIER._read_tar(cli_archive)
    cli_members["THIRD_PARTY_LICENSES.tar.gz"] = tampered
    with tarfile.open(cli_archive, "w:gz") as archive:
        for member_name, data in cli_members.items():
            member = tarfile.TarInfo(member_name)
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
    _rewrite_checksums(root)

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="unreviewed and uncleared"):
        run_verification(root, SuccessfulGH())


def test_rejects_wrong_source_commit_in_signed_provenance(tmp_path):
    root = write_candidate(tmp_path / "candidate")

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="requested source commit"):
        run_verification(root, SuccessfulGH(wrong_commit=True))


def test_rejects_spdx_attestation_that_differs_from_bundled_document(tmp_path):
    root = write_candidate(tmp_path / "candidate")

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="differs from the archive-bound"):
        run_verification(root, SuccessfulGH(wrong_spdx=True))


def test_rejects_python_sdist_with_a_different_project_version(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    sdist, _ = python_release_distribution(metadata_version="9.9.9")
    (root / f"threadforge_python-{VERSION}.tar.gz").write_bytes(sdist)
    _rewrite_checksums(root)
    runner = SuccessfulGH()

    with pytest.raises(VERIFIER.ReleaseCandidateError, match="name or version"):
        run_verification(root, runner)
    assert runner.calls == []


def test_rejects_python_distribution_path_traversal_and_missing_wheel_notice(tmp_path):
    root = write_candidate(tmp_path / "candidate")
    sdist, _ = python_release_distribution(sdist_traversal=True)
    (root / f"threadforge_python-{VERSION}.tar.gz").write_bytes(sdist)
    _rewrite_checksums(root)
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="unsafe member path"):
        run_verification(root, SuccessfulGH())

    sdist, wheel = python_release_distribution(omit_wheel_license="LICENSE.rrsi")
    (root / f"threadforge_python-{VERSION}.tar.gz").write_bytes(sdist)
    (root / f"threadforge_python-{VERSION}-py3-none-any.whl").write_bytes(wheel)
    _rewrite_checksums(root)
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="upstream notices"):
        run_verification(root, SuccessfulGH())

    sdist, wheel = python_release_distribution(omit_wheel_license="LICENSE.threadforge")
    (root / f"threadforge_python-{VERSION}.tar.gz").write_bytes(sdist)
    (root / f"threadforge_python-{VERSION}-py3-none-any.whl").write_bytes(wheel)
    _rewrite_checksums(root)
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="upstream notices"):
        run_verification(root, SuccessfulGH())


def test_rejects_unsafe_archive_names_and_release_versions():
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="unsafe member path"):
        VERIFIER._safe_member_name("../escape")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="stable MAJOR.MINOR.PATCH"):
        VERIFIER.expected_payload_names("v1.2.3")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="unsafe member path"):
        VERIFIER._safe_member_name("a//b")


def test_rejects_duplicate_keys_and_nonstandard_json_numbers():
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="duplicate object key"):
        VERIFIER._decode_json(b'{"schema":"expected","schema":"overridden"}', "evidence")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="non-standard numeric constant"):
        VERIFIER._decode_json(b'{"count":NaN}', "evidence")


def test_rejects_symlink_members_in_candidate_archives(tmp_path):
    path = tmp_path / "linked.zip"
    with zipfile.ZipFile(path, "w") as archive:
        info = zipfile.ZipInfo("escape")
        info.create_system = 3
        info.external_attr = (0o120777 << 16)
        archive.writestr(info, "../../outside")
    with pytest.raises(VERIFIER.ReleaseCandidateError, match="linked|invalid"):
        VERIFIER._read_zip(path)


def _rewrite_checksums(root):
    names = sorted(VERIFIER.expected_payload_names(VERSION))
    lines = [f"{hashlib.sha256((root / name).read_bytes()).hexdigest()}  {name}" for name in names]
    (root / "checksums.txt").write_text("\n".join(lines) + "\n", encoding="ascii")
