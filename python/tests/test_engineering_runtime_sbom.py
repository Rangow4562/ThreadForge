import hashlib
import importlib.util
import io
import json
import subprocess
import sys
import tarfile
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parents[2]


def load_script(name):
    path = ROOT / "scripts" / name
    spec = importlib.util.spec_from_file_location(f"threadforge_{path.stem}", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"could not load {name}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


BUILDER = load_script("build_engineering_runtime_sbom_binding.py")
VERIFIER = load_script("verify_engineering_runtime_sbom.py")

CANDIDATE_TEST_PATH = ROOT / "python" / "tests" / "test_release_candidate_verification.py"
CANDIDATE_SPEC = importlib.util.spec_from_file_location("threadforge_candidate_test_fixtures", CANDIDATE_TEST_PATH)
if CANDIDATE_SPEC is None or CANDIDATE_SPEC.loader is None:
    raise RuntimeError("could not load SPDX fixture builder")
CANDIDATE_TESTS = importlib.util.module_from_spec(CANDIDATE_SPEC)
sys.modules[CANDIDATE_SPEC.name] = CANDIDATE_TESTS
CANDIDATE_SPEC.loader.exec_module(CANDIDATE_TESTS)


VERSION = "1.2.3"
TAG = f"threadforge-engineering:{VERSION}-candidate"
SOURCE_COMMIT = "a" * 40
SOURCE_REF = f"refs/tags/v{VERSION}"
ARCHIVE_NAME = f"threadforge_{VERSION}_engineering_linux_amd64.docker.tar"
SBOM_NAME = f"threadforge_{VERSION}_engineering_linux_amd64.spdx.json"


def _hash(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _docker_archive(path, *, tag=TAG, unsafe_member=None):
    runtime_hash = _hash(ROOT / "containers" / "engineering" / "requirements-linux-x86_64-py312.lock")
    build_hash = _hash(ROOT / "containers" / "engineering" / "build-requirements.lock")
    base = (ROOT / "containers" / "engineering" / "base-image.txt").read_text(encoding="utf-8").strip()
    layer_data = b"synthetic layer bytes\n"
    config = {
        "architecture": "amd64",
        "os": "linux",
        "config": {
            "User": "65532:65532",
            "Labels": {
                "org.opencontainers.image.base.name": base,
                "org.threadforge.engineering.requirements-sha256": runtime_hash,
                "org.threadforge.engineering.build-requirements-sha256": build_hash,
            },
        },
        "rootfs": {"type": "layers", "diff_ids": [f"sha256:{hashlib.sha256(layer_data).hexdigest()}"]},
    }
    config_bytes = json.dumps(config, separators=(",", ":")).encode()
    image_id = f"sha256:{hashlib.sha256(config_bytes).hexdigest()}"
    config_name = f"{image_id.removeprefix('sha256:')}.json"
    manifest = [{"Config": config_name, "RepoTags": [tag], "Layers": ["layer.tar"]}]
    with tarfile.open(path, mode="w") as archive:
        for name, data in {
            "manifest.json": json.dumps(manifest).encode(),
            config_name: config_bytes,
            "layer.tar": layer_data,
        }.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = 0o644
            archive.addfile(info, io.BytesIO(data))
        if unsafe_member is not None:
            info = tarfile.TarInfo(unsafe_member)
            info.size = 0
            info.mode = 0o644
            archive.addfile(info, io.BytesIO())
    return image_id


def _checksums(bundle):
    names = [ARCHIVE_NAME, SBOM_NAME, f"threadforge_{VERSION}_engineering_linux_amd64.binding.json"]
    lines = [f"{_hash(bundle / name)}  {name}" for name in sorted(names)]
    (bundle / "checksums.txt").write_text("\n".join(lines) + "\n", encoding="ascii")


def _fixture(tmp_path, *, tag=TAG, unsafe_member=None, sbom=None):
    bundle = tmp_path / "artifact"
    bundle.mkdir(parents=True)
    archive = bundle / ARCHIVE_NAME
    image_id = _docker_archive(archive, tag=tag, unsafe_member=unsafe_member)
    sbom_path = bundle / SBOM_NAME
    sbom_path.write_text(json.dumps(sbom if sbom is not None else CANDIDATE_TESTS.python_lock_sbom()), encoding="utf-8")
    receipt = tmp_path / "image-build-receipt.json"
    receipt.write_text(json.dumps({
        "schema_version": "threadforge.engineering-image-build.v1",
        "platform": "linux/amd64",
        "image_id": image_id,
        "tag": TAG,
        "base_image": (ROOT / "containers" / "engineering" / "base-image.txt").read_text(encoding="utf-8").strip(),
        "runtime_requirements_sha256": _hash(ROOT / "containers" / "engineering" / "requirements-linux-x86_64-py312.lock"),
        "build_requirements_sha256": _hash(ROOT / "containers" / "engineering" / "build-requirements.lock"),
        "benchmark_data_included": False,
        "evaluator_included": False,
    }), encoding="utf-8")
    BUILDER.build_binding(
        repo_root=ROOT,
        output_dir=bundle,
        version=VERSION,
        source_commit=SOURCE_COMMIT,
        source_ref=SOURCE_REF,
        image_tag=TAG,
        build_receipt=receipt,
        image_archive=archive,
        sbom=sbom_path,
    )
    return bundle


class FakeGitHub:
    def __init__(self):
        self.commands = []
        self.options = []

    def run(self, command, **kwargs):
        self.commands.append(command)
        self.options.append(kwargs)
        path = Path(command[3])
        predicate = command[command.index("--predicate-type") + 1]
        limit = VERIFIER.MAX_ARCHIVE_BYTES if path.suffix == ".tar" else VERIFIER.MAX_SBOM_BYTES
        digest = VERIFIER._sha256(path, limit=limit)[0]
        statement = {
            "predicateType": predicate,
            "subject": [{"name": path.name, "digest": {"sha256": digest}}],
        }
        raw = json.dumps([{"verificationResult": {"statement": statement}}])
        return subprocess.CompletedProcess(command, 0, stdout=raw, stderr="")


def _verify(bundle, fake=None):
    return VERIFIER.verify_engineering_runtime_artifact(
        bundle_dir=bundle,
        repo_root=ROOT,
        version=VERSION,
        source_commit=SOURCE_COMMIT,
        source_ref=SOURCE_REF,
        repository="example/threadforge",
        run=(fake or FakeGitHub()).run,
    )


def test_builds_and_verifies_source_bound_engineering_image_sbom(tmp_path):
    bundle = _fixture(tmp_path)
    fake = FakeGitHub()

    result = _verify(bundle, fake)

    assert result["schema_version"] == "threadforge.engineering-runtime-sbom-verification.v1"
    assert result["image_archive_verified"] is True
    assert result["spdx_schema_verified"] is True
    assert result["package_record_count"] == 2
    assert result["slsa_attestations_verified"] == 4
    assert result["archive_sbom_attestation_verified"] is True
    assert result["attestation_checks_skipped"] is False
    assert result["license_review_required"] is True
    assert result["release_clearance"] is False
    assert result["release_authorized"] is False
    assert len(fake.commands) == 5
    assert sum(command[command.index("--predicate-type") + 1] == VERIFIER.SPDX_PREDICATE for command in fake.commands) == 1
    assert all(option.get("encoding") == "utf-8" and option.get("errors") == "replace" for option in fake.options)


def test_pre_attestation_verification_reports_skipped_checks(tmp_path):
    bundle = _fixture(tmp_path)
    fake = FakeGitHub()

    result = VERIFIER.verify_engineering_runtime_artifact(
        bundle_dir=bundle,
        repo_root=ROOT,
        version=VERSION,
        source_commit=SOURCE_COMMIT,
        source_ref=SOURCE_REF,
        repository="example/threadforge",
        run=fake.run,
        verify_attestations=False,
    )

    assert result["slsa_attestations_verified"] == 0
    assert result["archive_sbom_attestation_verified"] is False
    assert result["attestation_checks_skipped"] is True
    assert result["release_authorized"] is False
    assert fake.commands == []


def test_binding_builder_refuses_to_overwrite_artifact_metadata(tmp_path):
    bundle = _fixture(tmp_path)
    receipt = tmp_path / "image-build-receipt.json"
    with pytest.raises(BUILDER.EngineeringSBOMError, match="overwrite"):
        BUILDER.build_binding(
            repo_root=ROOT,
            output_dir=bundle,
            version=VERSION,
            source_commit=SOURCE_COMMIT,
            source_ref=SOURCE_REF,
            image_tag=TAG,
            build_receipt=receipt,
            image_archive=bundle / ARCHIVE_NAME,
            sbom=bundle / SBOM_NAME,
        )


def test_rejects_tampered_archive_before_attestation_lookups(tmp_path):
    bundle = _fixture(tmp_path)
    (bundle / ARCHIVE_NAME).write_bytes((bundle / ARCHIVE_NAME).read_bytes() + b"tamper")
    fake = FakeGitHub()

    with pytest.raises(VERIFIER.EngineeringRuntimeSBOMError, match="checksum mismatch"):
        _verify(bundle, fake)
    assert fake.commands == []


def test_rejects_wrong_image_tag_and_unsafe_docker_archive_members(tmp_path):
    wrong_tag_bundle = _fixture(tmp_path / "wrong-tag", tag="other/image:latest")
    with pytest.raises(VERIFIER.EngineeringRuntimeSBOMError, match="RepoTags"):
        _verify(wrong_tag_bundle)

    unsafe_bundle = _fixture(tmp_path / "unsafe", unsafe_member="../escape")
    with pytest.raises(VERIFIER.EngineeringRuntimeSBOMError, match="unsafe member path"):
        _verify(unsafe_bundle)


def test_rejects_source_or_license_clearance_claims_in_binding(tmp_path):
    for mutation in ("source", "clearance"):
        bundle = _fixture(tmp_path / mutation)
        binding_path = bundle / f"threadforge_{VERSION}_engineering_linux_amd64.binding.json"
        binding = json.loads(binding_path.read_text(encoding="utf-8"))
        if mutation == "source":
            binding["source_commit"] = "b" * 40
        else:
            binding["release_clearance"] = True
        binding_path.write_text(json.dumps(binding, sort_keys=True, indent=2) + "\n", encoding="utf-8")
        _checksums(bundle)

        with pytest.raises(VERIFIER.EngineeringRuntimeSBOMError):
            _verify(bundle)


def test_rejects_unpinned_or_unsafe_build_identity(tmp_path):
    bundle = _fixture(tmp_path)
    with pytest.raises(VERIFIER.EngineeringRuntimeSBOMError, match="source ref"):
        VERIFIER.verify_engineering_runtime_artifact(
            bundle_dir=bundle,
            repo_root=ROOT,
            version=VERSION,
            source_commit=SOURCE_COMMIT,
            source_ref="refs/tags/v9.9.9",
            repository="example/threadforge",
            run=FakeGitHub().run,
        )
