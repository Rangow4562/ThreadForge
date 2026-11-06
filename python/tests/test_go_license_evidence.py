import csv
import hashlib
import importlib.util
import io
import json
import subprocess
import tarfile
from pathlib import Path

import pytest


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "build_go_license_evidence.py"
WORKFLOW_PATH = Path(__file__).resolve().parents[2] / ".github" / "workflows" / "threadforge-tests.yml"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_go_license_evidence", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the Go license evidence builder")
EVIDENCE = importlib.util.module_from_spec(SCRIPT_SPEC)
SCRIPT_SPEC.loader.exec_module(EVIDENCE)


def test_release_workflow_creates_go_license_evidence_output_directory():
    workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
    marker = "      - name: Collect target-specific Go license evidence\n"
    assert marker in workflow
    step = workflow.split(marker, 1)[1].split("      - name:", 1)[0]
    assert "LICENSE_EVIDENCE_PATH: dist/" in step
    assert "run: >-\n          mkdir -p dist &&\n" in step


REPORT = (
    "example.com/zeta,Unknown,MIT\n"
    "example.com/alpha,https://example.test/license,Apache-2.0\n"
)
LICENSE_TEXT = "MIT License\nCopyright Example\n"
SOURCE_COMMIT = "a" * 40
SOURCE_DATE_EPOCH = 1_798_358_401


def fake_runner(report=REPORT, *, save_exit=0):
    calls = []

    def run(argv, **kwargs):
        calls.append(argv)
        if argv[1] == "report":
            return subprocess.CompletedProcess(
                args=argv,
                returncode=0,
                stdout=report,
                stderr=(
                    "W: package contains non-Go code that can't be inspected for further "
                    "dependencies at C:\\private\\source.s"
                ),
            )
        if argv[1] == "save":
            save_path = Path(argv[argv.index("--save_path") + 1])
            save_path.mkdir(parents=True)
            (save_path / "LICENSE").write_text(LICENSE_TEXT, encoding="utf-8")
            return subprocess.CompletedProcess(
                args=argv, returncode=save_exit, stdout="", stderr="save diagnostic"
            )
        raise AssertionError(f"unexpected go-licenses command: {argv[1]}")

    return run, calls


def build(tmp_path, output_name, *, report=REPORT, save_exit=0):
    tool = tmp_path / "go-licenses"
    tool.write_bytes(b"pinned synthetic tool")
    output = tmp_path / output_name
    runner, calls = fake_runner(report, save_exit=save_exit)
    summary = EVIDENCE.build_license_evidence(
        go_licenses=tool,
        package="./cmd/threadforge",
        goos="linux",
        goarch="amd64",
        source_commit=SOURCE_COMMIT,
        source_date_epoch=SOURCE_DATE_EPOCH,
        output=output,
        run=runner,
    )
    return output, summary, calls, tool


def test_packages_deterministic_notice_archive_and_explicit_review_status(tmp_path):
    first, summary, _, tool = build(tmp_path, "first.tar.gz")
    second, second_summary, _, _ = build(tmp_path, "second.tar.gz")

    assert first.read_bytes() == second.read_bytes()
    assert summary == second_summary
    assert summary["schema"] == "threadforge.go-license-evidence.v1"
    assert summary["scanner"]["module"] == "github.com/google/go-licenses/v2"
    assert summary["scanner"]["version"] == "v2.0.1"
    assert summary["scanner"]["binary_sha256"] == hashlib.sha256(tool.read_bytes()).hexdigest()
    assert len(summary["go_executable_sha256"]) == 64
    assert summary["license_record_count"] == 2
    assert summary["license_url_unknown_count"] == 1
    assert summary["unknown_license_expression_count"] == 0
    assert summary["license_review_status"] == "unreviewed"
    assert summary["license_review_required"] is True
    assert summary["release_clearance"] is False
    assert summary["diagnostics"]["report"]["non_go_native_source_warnings"] == 1
    with tarfile.open(first, "r:gz") as archive:
        members = {item.name: archive.extractfile(item).read() for item in archive.getmembers()}
    prefix = "go-license-evidence/"
    assert str(tmp_path).encode() not in b"".join(members.values())
    assert members[prefix + "licenses/LICENSE"].decode().replace("\r\n", "\n") == LICENSE_TEXT
    assert members[prefix + "README.md"].decode().find("does not grant redistribution clearance") >= 0
    indexed = json.loads(members[prefix + "evidence.json"])
    assert indexed["release_clearance"] is False
    rows = list(csv.DictReader(io.StringIO(members[prefix + "report.csv"].decode())))
    assert [row["package"] for row in rows] == ["example.com/alpha", "example.com/zeta"]


def test_rejects_unknown_license_expression_before_saving(tmp_path):
    tool = tmp_path / "go-licenses"
    tool.write_bytes(b"pinned synthetic tool")
    output = tmp_path / "blocked.tar.gz"
    runner, calls = fake_runner("example.com/unknown,Unknown,Unknown\n")

    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="could not classify"):
        EVIDENCE.build_license_evidence(
            go_licenses=tool,
            package="./cmd/threadforge",
            goos="linux",
            goarch="amd64",
            source_commit=SOURCE_COMMIT,
            source_date_epoch=SOURCE_DATE_EPOCH,
            output=output,
            run=runner,
        )

    assert len(calls) == 1
    assert not output.exists()


def test_rejects_invalid_urls_targets_commits_and_preexisting_outputs(tmp_path):
    output, _, _, tool = build(tmp_path, "existing.tar.gz")
    cases = [
        ("linux", "amd64", "bad", tmp_path / "other.tar.gz", REPORT),
        ("linux", "arm64", SOURCE_COMMIT, tmp_path / "other.tar.gz", REPORT),
        ("linux", "amd64", SOURCE_COMMIT, output, REPORT),
        (
            "linux",
            "amd64",
            SOURCE_COMMIT,
            tmp_path / "other.tar.gz",
            "example.com/dep,https://user:secret@example.test/LICENSE,MIT\n",
        ),
    ]
    for goos, goarch, commit, target, report in cases:
        runner, _ = fake_runner(report)
        with pytest.raises(EVIDENCE.LicenseEvidenceError):
            EVIDENCE.build_license_evidence(
                go_licenses=tool,
                package="./cmd/threadforge",
                goos=goos,
                goarch=goarch,
                source_commit=commit,
                source_date_epoch=SOURCE_DATE_EPOCH,
                output=target,
                run=runner,
            )


def test_rejects_scanner_save_failure_and_malformed_report(tmp_path):
    tool = tmp_path / "go-licenses"
    tool.write_bytes(b"pinned synthetic tool")
    runner, _ = fake_runner(save_exit=1)
    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="save failed"):
        EVIDENCE.build_license_evidence(
            go_licenses=tool,
            package="./cmd/threadforge",
            goos="windows",
            goarch="amd64",
            source_commit=SOURCE_COMMIT,
            source_date_epoch=SOURCE_DATE_EPOCH,
            output=tmp_path / "failed.tar.gz",
            run=runner,
        )

    runner, _ = fake_runner("only-two-columns,MIT\n")
    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="three CSV columns"):
        EVIDENCE.build_license_evidence(
            go_licenses=tool,
            package="./cmd/threadforge",
            goos="windows",
            goarch="amd64",
            source_commit=SOURCE_COMMIT,
            source_date_epoch=SOURCE_DATE_EPOCH,
            output=tmp_path / "malformed.tar.gz",
            run=runner,
        )
