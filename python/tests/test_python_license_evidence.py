import csv
import hashlib
import importlib.util
import io
import json
import platform
import re
import tarfile
import tomllib
from email.message import Message
from pathlib import Path

import pytest
from jsonschema import Draft202012Validator


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "build_python_license_evidence.py"
REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
PROJECT_PATH = REPOSITORY_ROOT / "python" / "pyproject.toml"
WORKFLOW_PATH = REPOSITORY_ROOT / ".github" / "workflows" / "threadforge-tests.yml"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_python_license_evidence", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the Python license evidence builder")
EVIDENCE = importlib.util.module_from_spec(SCRIPT_SPEC)
SCRIPT_SPEC.loader.exec_module(EVIDENCE)


def test_release_evidence_installs_and_collects_every_declared_extra():
    extras = set(tomllib.loads(PROJECT_PATH.read_text(encoding="utf-8"))["project"]["optional-dependencies"])
    workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
    marker = "  python-license-evidence:\n"
    assert marker in workflow
    job = workflow.split(marker, 1)[1].split("\n  release-candidate:\n", 1)[0]
    sync_command = next(
        line.strip() for line in job.splitlines() if line.strip().startswith("uv sync --locked")
    )
    assert set(re.findall(r"--extra\s+([^\s]+)", sync_command)) == extras
    for version in EVIDENCE.SUPPORTED_PYTHON_EVIDENCE_VERSIONS:
        assert job.count(f"python: '{version}'") == 3

    marker = "      - name: Collect locked Python license and notice evidence\n"
    assert marker in job
    collector_step = job.split(marker, 1)[1].split("      - name: Attest Python license evidence provenance\n", 1)[0]
    run_command = next(
        line.strip() for line in collector_step.splitlines() if line.strip().startswith("uv run --locked")
    )
    assert set(re.findall(r"--extra\s+([^\s]+)", run_command)) == extras
    collector_args = collector_step.split("python ../scripts/build_python_license_evidence.py", 1)[1]
    collector_args = collector_args.split("--source-date-epoch", 1)[0]
    assert set(re.findall(r"--extra\s+([^\s]+)", collector_args)) == extras

    test_marker = "      - name: Test full Python suite with all declared extras\n"
    assert test_marker in job
    test_step = job.split(test_marker, 1)[1].split("      - name: Collect locked Python license and notice evidence\n", 1)[0]
    test_command = next(line.strip() for line in test_step.splitlines() if line.strip().startswith("uv run --locked"))
    assert set(re.findall(r"--extra\s+([^\s]+)", test_command)) == extras
    assert "python -m pytest -q" in test_step


SOURCE_COMMIT = "a" * 40
SOURCE_DATE_EPOCH = 1_798_358_401


class FakeDistribution:
    def __init__(self, root: Path, name: str, version: str, *, expression=None, classifier=None, legacy=None, files=None):
        self.root = root
        self.metadata = Message()
        self.metadata["Name"] = name
        self.metadata["Version"] = version
        if expression is not None:
            self.metadata["License-Expression"] = expression
        if classifier is not None:
            self.metadata["Classifier"] = classifier
        if legacy is not None:
            self.metadata["License"] = legacy
        self.files = files or []

    def locate_file(self, path):
        return self.root / Path(path)


def make_distribution(tmp_path, name, version, *, expression=None, classifier=None, legacy=None, license_text=None, relative_license="example.dist-info/licenses/LICENSE"):
    root = tmp_path / f"{name}-{version}"
    metadata_root = root / "example.dist-info"
    metadata_root.mkdir(parents=True, exist_ok=True)
    metadata_file = metadata_root / "METADATA"
    metadata_file.write_bytes(f"Name: {name}\nVersion: {version}\n".encode("utf-8"))
    files = [Path("example.dist-info/METADATA")]
    if license_text is not None:
        license_path = root / relative_license
        license_path.parent.mkdir(parents=True, exist_ok=True)
        license_path.write_bytes(license_text.encode("utf-8"))
        files.append(Path(relative_license))
    return FakeDistribution(
        root,
        name,
        version,
        expression=expression,
        classifier=classifier,
        legacy=legacy,
        files=files,
    )


def write_inputs(tmp_path, *, lock_packages=None, extras=("decisions", "test", "workspace")):
    lock = tmp_path / "uv.lock"
    packages = lock_packages or [("example-package", "1.2.0"), ("threadforge-python", "0.4.6")]
    lock_text = "version = 1\n"
    for name, version in packages:
        lock_text += f'\n[[package]]\nname = "{name}"\nversion = "{version}"\n'
    lock.write_text(lock_text, encoding="utf-8")
    project = tmp_path / "pyproject.toml"
    extra_values = "\n".join(f'{name} = []' for name in extras)
    project.write_text(
        '[project]\nname = "threadforge-python"\nversion = "0.4.6"\nlicense = "Apache-2.0"\n'
        f"[project.optional-dependencies]\n{extra_values}\n",
        encoding="utf-8",
    )
    return lock, project


def build(
    tmp_path,
    output_name,
    distributions,
    *,
    target_os=None,
    python_version="3.12",
    extras=None,
    lock_packages=None,
):
    lock, project = write_inputs(tmp_path, lock_packages=lock_packages)
    output = tmp_path / output_name
    os_name = target_os or {"Linux": "linux", "Windows": "windows", "Darwin": "darwin"}[platform.system()]
    evidence = EVIDENCE.build_python_license_evidence(
        source_commit=SOURCE_COMMIT,
        release_version="0.4.7",
        target_os=os_name,
        python_version=python_version,
        extras=extras or ["test", "decisions", "workspace"],
        lock_path=lock,
        project_path=project,
        output_path=output,
        source_date_epoch=SOURCE_DATE_EPOCH,
        distributions=distributions,
        host_system=platform.system(),
        host_python_version=python_version,
        host_runtime_version=f"{python_version}.0",
    )
    return output, evidence


def test_archive_is_deterministic_path_free_and_never_clears_review(tmp_path):
    example = make_distribution(
        tmp_path,
        "example-package",
        "1.2.0",
        expression="MIT OR Apache-2.0",
        classifier="License :: OSI Approved :: MIT License",
        legacy="contact=private@example.invalid; license text is not a decision",
        license_text="MIT License\nCopyright Example\n",
    )
    project = make_distribution(
        tmp_path,
        "threadforge-python",
        "0.4.6",
        expression="Apache-2.0",
        license_text="Apache License\n",
    )
    first, evidence = build(tmp_path, "first.tar.gz", [project, example])
    second, second_evidence = build(tmp_path, "second.tar.gz", [example, project])

    assert first.read_bytes() == second.read_bytes()
    assert evidence == second_evidence
    assert evidence["schema"] == "threadforge.python-license-evidence.v2"
    assert evidence["source_commit"] == SOURCE_COMMIT
    assert evidence["collector_sha256"] == EVIDENCE._sha256_source_file(SCRIPT_PATH)
    assert evidence["input_hashes"]["uv_lock_sha256"] == EVIDENCE._sha256_lf_normalized_bytes(
        (tmp_path / "uv.lock").read_bytes()
    )
    assert evidence["installed_distribution_count"] == 2
    assert evidence["unlocked_distribution_count"] == 0
    assert evidence["license_review_status"] == "unreviewed"
    assert evidence["license_review_required"] is True
    assert evidence["release_clearance"] is False
    schema = json.loads((SCRIPT_PATH.parents[1] / "schema" / "python-license-evidence-v2.schema.json").read_text())
    Draft202012Validator.check_schema(schema)
    Draft202012Validator(schema).validate(evidence)

    with tarfile.open(first, "r:gz") as archive:
        members = {member.name: archive.extractfile(member).read() for member in archive.getmembers()}
    assert str(tmp_path).encode() not in b"".join(members.values())
    assert b"private@example.invalid" not in b"".join(members.values())
    assert b"license text is not a decision" not in b"".join(members.values())
    manifest = json.loads(members["python-license-evidence/evidence.json"])
    assert manifest["license_review_status"] == "unreviewed"
    report = list(csv.DictReader(io.StringIO(members["python-license-evidence/report.csv"].decode())))
    assert [row["name"] for row in report] == ["example-package", "threadforge-python"]
    example_row = next(row for row in manifest["distributions"] if row["name"] == "example-package")
    assert example_row["license_expression"] == "MIT OR Apache-2.0"
    assert example_row["license_files"][0]["sha256"] == hashlib.sha256(b"MIT License\nCopyright Example\n").hexdigest()
    assert (
        "python-license-evidence/licenses/example-package/1.2.0/example.dist-info/licenses/LICENSE"
        in members
    )


@pytest.mark.parametrize("python_version", EVIDENCE.SUPPORTED_PYTHON_EVIDENCE_VERSIONS)
def test_all_supported_python_profiles_are_bound_into_v2_schema(tmp_path, python_version):
    example = make_distribution(tmp_path, "example-package", "1.2.0", expression="MIT")
    project = make_distribution(tmp_path, "threadforge-python", "0.4.6", expression="Apache-2.0")
    _, evidence = build(
        tmp_path,
        f"python-{python_version.replace('.', '')}.tar.gz",
        [example, project],
        python_version=python_version,
    )

    schema = json.loads((SCRIPT_PATH.parents[1] / "schema" / "python-license-evidence-v2.schema.json").read_text())
    Draft202012Validator(schema).validate(evidence)
    assert evidence["target"]["python_version"] == python_version


def test_missing_or_unsafe_metadata_is_recorded_without_inference(tmp_path):
    unknown = make_distribution(
        tmp_path,
        "example-package",
        "1.2.0",
        legacy=None,
        license_text=None,
    )
    project = make_distribution(
        tmp_path,
        "threadforge-python",
        "0.4.6",
        expression="Apache-2.0",
        license_text="Apache License\n",
    )
    output, evidence = build(tmp_path, "unknown.tar.gz", [unknown, project])

    unknown_record = next(item for item in evidence["distributions"] if item["name"] == "example-package")
    assert unknown_record["license_expression"] is None
    assert unknown_record["license_expression_status"] == "missing"
    assert unknown_record["license_classifiers"] == []
    assert unknown_record["legacy_license_field_sha256"] is None
    assert evidence["license_summary"]["distributions_without_license_metadata"] == 1
    assert evidence["license_summary"]["distributions_without_license_files"] == 1
    with tarfile.open(output, "r:gz") as archive:
        all_bytes = b"".join(archive.extractfile(member).read() for member in archive.getmembers())
    assert b"secret=token-value" not in all_bytes

    unsafe = make_distribution(
        tmp_path,
        "unsafe-expression",
        "1.0",
        expression="MIT; contact=private@example.invalid",
    )
    unsafe_output, unsafe_evidence = build(
        tmp_path,
        "unsafe-expression.tar.gz",
        [unsafe, project],
        lock_packages=[("unsafe-expression", "1.0"), ("threadforge-python", "0.4.6")],
    )
    unsafe_record = next(item for item in unsafe_evidence["distributions"] if item["name"] == "unsafe-expression")
    assert unsafe_record["license_expression"] is None
    assert unsafe_record["license_expression_status"] == "omitted_unsafe_metadata"
    with tarfile.open(unsafe_output, "r:gz") as archive:
        unsafe_bytes = b"".join(archive.extractfile(member).read() for member in archive.getmembers())
    assert b"private@example.invalid" not in unsafe_bytes


def test_rejects_unsafe_paths_and_oversized_license_files(tmp_path, monkeypatch):
    traversal = make_distribution(tmp_path, "example-package", "1.2.0")
    traversal.files.append(Path("../outside/LICENSE"))
    project = make_distribution(tmp_path, "threadforge-python", "0.4.6", expression="Apache-2.0")
    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="unsafe file path"):
        build(tmp_path, "unsafe.tar.gz", [traversal, project])

    oversized = make_distribution(
        tmp_path,
        "example-package",
        "1.2.0",
        license_text="x" * 33,
    )
    monkeypatch.setattr(EVIDENCE, "MAX_SINGLE_FILE_BYTES", 32)
    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="size limit"):
        build(tmp_path, "oversized.tar.gz", [oversized, project])


def test_deterministic_archive_writer_accepts_relative_output_in_existing_directory(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    output = Path("relative-evidence.tar.gz")
    EVIDENCE._write_deterministic_archive(output, {"evidence.txt": b"bounded\n"}, SOURCE_DATE_EPOCH)
    assert output.is_file()
    with tarfile.open(output, "r:gz") as archive:
        assert archive.extractfile("evidence.txt").read() == b"bounded\n"


def test_collector_digest_is_stable_across_lf_and_crlf_checkouts(tmp_path):
    source = tmp_path / "collector.py"
    prefix = b"a" * ((1024 * 1024) - 1)
    source.write_bytes(prefix + b"\nsecond line\n")
    lf_digest = EVIDENCE._sha256_source_file(source)
    source.write_bytes(prefix + b"\r\nsecond line\r\n")
    crlf_digest = EVIDENCE._sha256_source_file(source)

    assert lf_digest == crlf_digest


def test_project_and_lock_input_hashes_are_stable_across_lf_and_crlf_checkouts(tmp_path):
    lock, project = write_inputs(tmp_path)
    lock.write_bytes(lock.read_bytes().replace(b"\r\n", b"\n"))
    project.write_bytes(project.read_bytes().replace(b"\r\n", b"\n"))
    extras = ["decisions", "test", "workspace"]
    lf_lock_digest = EVIDENCE._load_lock_versions(lock)[1]
    lf_project_digest = EVIDENCE._load_project(project, extras)[2]

    lock.write_bytes(lock.read_bytes().replace(b"\n", b"\r\n"))
    project.write_bytes(project.read_bytes().replace(b"\n", b"\r\n"))
    crlf_lock_digest = EVIDENCE._load_lock_versions(lock)[1]
    crlf_project_digest = EVIDENCE._load_project(project, extras)[2]

    assert lf_lock_digest == crlf_lock_digest
    assert lf_project_digest == crlf_project_digest
    assert lf_lock_digest == hashlib.sha256(lock.read_bytes().replace(b"\r\n", b"\n")).hexdigest()
    assert lf_project_digest == hashlib.sha256(project.read_bytes().replace(b"\r\n", b"\n")).hexdigest()


def test_rejects_target_mismatch_invalid_scope_and_existing_output(tmp_path):
    example = make_distribution(tmp_path, "example-package", "1.2.0", expression="MIT")
    project = make_distribution(tmp_path, "threadforge-python", "0.4.6", expression="Apache-2.0")
    output, _ = build(tmp_path, "existing.tar.gz", [example, project])
    lock, project_file = write_inputs(tmp_path)

    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="target does not match"):
        EVIDENCE.build_python_license_evidence(
            source_commit=SOURCE_COMMIT,
            release_version="0.4.7",
            target_os="windows" if platform.system() != "Windows" else "linux",
            python_version="3.12",
            extras=["test", "decisions", "workspace"],
            lock_path=lock,
            project_path=project_file,
            output_path=tmp_path / "mismatch.tar.gz",
            distributions=[example, project],
            host_system=platform.system(),
            host_python_version="3.12",
            host_runtime_version="3.12.0",
        )

    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="undeclared"):
        EVIDENCE.build_python_license_evidence(
            source_commit=SOURCE_COMMIT,
            release_version="0.4.7",
            target_os={"Linux": "linux", "Windows": "windows", "Darwin": "darwin"}[platform.system()],
            python_version="3.12",
            extras=["test", "missing-extra"],
            lock_path=lock,
            project_path=project_file,
            output_path=tmp_path / "extra.tar.gz",
            distributions=[example, project],
            host_system=platform.system(),
            host_python_version="3.12",
            host_runtime_version="3.12.0",
        )

    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="overwrite"):
        build(tmp_path, "existing.tar.gz", [example, project])
    assert output.is_file()


def test_rejects_installed_distributions_outside_the_locked_environment(tmp_path):
    extra = make_distribution(tmp_path, "not-in-lock", "1.0")
    project = make_distribution(tmp_path, "threadforge-python", "0.4.6", expression="Apache-2.0")
    with pytest.raises(EVIDENCE.LicenseEvidenceError, match="not fully represented in uv.lock"):
        build(
            tmp_path,
            "unlocked.tar.gz",
            [extra, project],
            lock_packages=[("threadforge-python", "0.4.6")],
        )
