import importlib.util
import json
import subprocess
import sys
from pathlib import Path

import pytest


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "verify_published_release.py"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_published_release", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the published-release verifier")
VERIFIER = importlib.util.module_from_spec(SCRIPT_SPEC)
sys.modules[SCRIPT_SPEC.name] = VERIFIER
SCRIPT_SPEC.loader.exec_module(VERIFIER)


VERSION = "1.2.3"
TAG = f"v{VERSION}"
SOURCE_COMMIT = "a" * 40


def candidate_bundle(root: Path) -> dict[str, bytes]:
    root.mkdir()
    files = {"checksums.txt": b"synthetic checksums\n"}
    files.update({f"candidate-payload-{index:02}.bin": f"payload {index}\n".encode() for index in range(27)})
    for name, content in files.items():
        (root / name).write_bytes(content)
    return files


class FakeGitHub:
    def __init__(self, bundle: Path, files: dict[str, bytes]):
        self.bundle = bundle
        self.files = files
        self.assets = list(files)
        self.download_files = dict(files)
        self.download_extras: dict[str, bytes] = {}
        self.download_overrides: dict[str, bytes] = {}
        self.release_commit = SOURCE_COMMIT
        self.is_draft = False
        self.is_prerelease = False
        self.candidate_succeeds = True
        self.candidate_stderr = (
            "release-candidate verification failed: candidate bundle does not contain exactly the documented payload set"
        )
        self.commands: list[list[str]] = []

    def run(self, command, **kwargs):
        assert kwargs["capture_output"] is True
        assert kwargs["text"] is True
        assert kwargs["check"] is False
        self.commands.append(command)
        completed = subprocess.CompletedProcess(command, 0, stdout="", stderr="")
        if len(command) > 1 and Path(command[1]).name == "verify_release_candidate.py":
            completed.returncode = 0 if self.candidate_succeeds else 1
            if not self.candidate_succeeds:
                completed.stderr = self.candidate_stderr
            return completed
        if command[1:3] == ["release", "view"]:
            completed.stdout = json.dumps({
                "tagName": TAG,
                "isDraft": self.is_draft,
                "isPrerelease": self.is_prerelease,
                "assets": [{"name": name} for name in self.assets],
            })
            return completed
        if command[1] == "api":
            completed.stdout = json.dumps({"sha": self.release_commit})
            return completed
        if command[1:3] == ["release", "download"]:
            destination = Path(command[command.index("--dir") + 1])
            for name, content in (self.download_files | self.download_extras).items():
                (destination / name).write_bytes(self.download_overrides.get(name, content))
            return completed
        raise AssertionError(f"unexpected command: {command}")


def verify(bundle: Path, fake: FakeGitHub):
    return VERIFIER.verify_published_release(
        bundle_dir=bundle,
        version=VERSION,
        source_commit=SOURCE_COMMIT,
        repository="example/threadforge",
        gh="gh-test",
        run=fake.run,
    )


def test_published_release_requires_provenance_tag_binding_and_exact_asset_bytes(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)

    result = verify(bundle, fake)

    assert result == {
        "schema": "threadforge.published-release-verification.v1",
        "repository": "example/threadforge",
        "tag": TAG,
        "source_commit": SOURCE_COMMIT,
        "candidate_provenance_verified": True,
        "tag_commit_matches_candidate": True,
        "public_assets_match_candidate": True,
        "asset_count": 28,
        "payload_count": 27,
        "license_review_required": True,
        "release_clearance": False,
        "release_authorized": False,
    }
    candidate_command = fake.commands[0]
    assert candidate_command[candidate_command.index("--source-ref") + 1] == f"refs/tags/{TAG}"
    assert all("shell" not in str(command).lower() for command in fake.commands)


def test_rejects_public_asset_with_different_bytes(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.download_overrides["candidate-payload-03.bin"] = b"tampered\n"

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError, match="byte differences=1"):
        verify(bundle, fake)


def test_reports_metadata_and_download_inventory_differences_with_common_asset_hashes(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.assets.pop()
    fake.assets.append("unverified-extra.zip")
    fake.download_files.pop("candidate-payload-02.bin")
    fake.download_extras["unverified-extra.zip"] = b"unverified\n"
    fake.download_overrides["candidate-payload-03.bin"] = b"tampered\n"

    with pytest.raises(
        VERIFIER.PublishedReleaseVerificationError,
        match=r"metadata missing=1, metadata extra=1, download missing=1, download extra=1, byte differences=1",
    ):
        verify(bundle, fake)
    assert any(command[1:3] == ["release", "download"] for command in fake.commands)


def test_rejects_duplicate_public_asset_names_before_download(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.assets.append(fake.assets[0])

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError, match="duplicate asset names"):
        verify(bundle, fake)
    assert not any(command[1:3] == ["release", "download"] for command in fake.commands)


def test_rejects_tag_pointing_at_a_different_commit(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.release_commit = "b" * 40

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError, match="source commit"):
        verify(bundle, fake)
    assert not any(command[1:3] == ["release", "download"] for command in fake.commands)


@pytest.mark.parametrize("state", ["draft", "prerelease"])
def test_rejects_unpublished_or_prerelease_release(tmp_path, state):
    bundle = tmp_path / state
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    setattr(fake, "is_draft" if state == "draft" else "is_prerelease", True)

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError, match="draft, prerelease"):
        verify(bundle, fake)


def test_candidate_provenance_failure_stops_before_public_release_queries(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.candidate_succeeds = False

    with pytest.raises(
        VERIFIER.PublishedReleaseVerificationError,
        match="candidate bundle does not contain exactly the documented payload set",
    ):
        verify(bundle, fake)
    assert len(fake.commands) == 1


def test_candidate_provenance_failure_does_not_echo_unsafe_subprocess_output(tmp_path):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    fake.candidate_succeeds = False
    fake.candidate_stderr = "release-candidate verification failed: token gho_abc123 at C:/private/path"

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError) as error:
        verify(bundle, fake)

    assert str(error.value) == "release-candidate provenance verification command failed"
    assert "gho_abc123" not in str(error.value)


@pytest.mark.parametrize(
    ("version", "source_commit", "repository"),
    [("v1.2.3", SOURCE_COMMIT, "example/threadforge"), (VERSION, "A" * 40, "example/threadforge"), (VERSION, SOURCE_COMMIT, "../threadforge")],
)
def test_rejects_unsafe_release_identity_before_running_commands(tmp_path, version, source_commit, repository):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError):
        VERIFIER.verify_published_release(
            bundle_dir=bundle,
            version=version,
            source_commit=source_commit,
            repository=repository,
            run=fake.run,
        )
    assert fake.commands == []


def test_rejects_link_in_downloaded_asset_directory(tmp_path, monkeypatch):
    bundle = tmp_path / "candidate"
    files = candidate_bundle(bundle)
    fake = FakeGitHub(bundle, files)
    original_run = fake.run

    def run_with_link(command, **kwargs):
        result = original_run(command, **kwargs)
        if command[1:3] == ["release", "download"]:
            destination = Path(command[command.index("--dir") + 1])
            linked = destination / "linked.bin"
            try:
                linked.symlink_to(destination / "checksums.txt")
            except OSError:
                pytest.skip("Windows policy does not allow creating a file symlink")
        return result

    with pytest.raises(VERIFIER.PublishedReleaseVerificationError, match="asset names|contains a link"):
        VERIFIER.verify_published_release(
            bundle_dir=bundle,
            version=VERSION,
            source_commit=SOURCE_COMMIT,
            repository="example/threadforge",
            run=run_with_link,
        )
