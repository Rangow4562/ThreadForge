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
        "locked_package_count": 1,
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
        "distributions": [{"name": "sample", "version": "1.0", "locked": True}],
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
    assert result["payload_count"] == 24
    assert result["checksum_count"] == 24
    assert result["slsa_attestations_verified"] == 25
    assert result["archive_sbom_attestations_verified"] == 4
    assert result["integrity_verified"] is True
    assert result["provenance_verified"] is True
    assert result["license_review_required"] is True
    assert result["release_clearance"] is False
    assert result["release_authorized"] is False
    assert len(runner.calls) == 29
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
