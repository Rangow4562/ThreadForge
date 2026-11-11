import importlib.util
import io
import sys
import tarfile
import tomllib
from pathlib import Path

import pytest


SCRIPT_PATH = Path(__file__).resolve().parents[2] / "scripts" / "build_python_release_distribution.py"
SCRIPT_SPEC = importlib.util.spec_from_file_location("threadforge_python_release_distribution", SCRIPT_PATH)
if SCRIPT_SPEC is None or SCRIPT_SPEC.loader is None:
    raise RuntimeError("could not load the Python release distribution builder")
BUILDER = importlib.util.module_from_spec(SCRIPT_SPEC)
sys.modules[SCRIPT_SPEC.name] = BUILDER
SCRIPT_SPEC.loader.exec_module(BUILDER)


def test_release_version_is_only_applied_to_the_staged_pyproject(tmp_path):
    source = Path(__file__).resolve().parents[1] / "pyproject.toml"
    original = source.read_bytes()
    staged = tmp_path / "pyproject.toml"
    staged.write_text(
        "[build-system]\nrequires = []\n\n[project]\nname = \"threadforge-python\"\nversion = \"0.5.0\"\n\n[project.optional-dependencies]\ntest = []\n",
        encoding="utf-8",
    )

    BUILDER._set_staged_version(staged, "9.8.7")

    assert tomllib.loads(staged.read_text(encoding="utf-8"))["project"]["version"] == "9.8.7"
    assert source.read_bytes() == original


def test_source_archive_extraction_accepts_regular_root_and_files(tmp_path):
    archive_path = tmp_path / "source.tar.gz"
    with tarfile.open(archive_path, mode="w:gz") as archive:
        root = tarfile.TarInfo("threadforge_python-9.8.7/")
        root.type = tarfile.DIRTYPE
        archive.addfile(root)
        member = tarfile.TarInfo("threadforge_python-9.8.7/pyproject.toml")
        content = b"[project]\nname='threadforge-python'\nversion='9.8.7'\n"
        member.size = len(content)
        archive.addfile(member, io.BytesIO(content))

    result = BUILDER._safe_sdist_extract(archive_path, tmp_path / "unpacked")

    assert (result / "pyproject.toml").read_bytes() == content


@pytest.mark.parametrize("member_name", ["threadforge_python-9.8.7/../escape", "/absolute/file"])
def test_source_archive_extraction_rejects_traversal_and_absolute_paths(tmp_path, member_name):
    archive_path = tmp_path / "unsafe.tar.gz"
    with tarfile.open(archive_path, mode="w:gz") as archive:
        member = tarfile.TarInfo(member_name)
        content = b"unsafe"
        member.size = len(content)
        archive.addfile(member, io.BytesIO(content))

    with pytest.raises(RuntimeError, match="unsafe member path"):
        BUILDER._safe_sdist_extract(archive_path, tmp_path / "unpacked")


def test_builder_refuses_to_overwrite_an_existing_candidate_asset(tmp_path):
    output = tmp_path / "dist"
    output.mkdir()
    existing = output / "threadforge_python-0.5.6.tar.gz"
    existing.write_bytes(b"preserve me")

    with pytest.raises(RuntimeError, match="already exists"):
        BUILDER.build_release_distribution("0.5.6", output, 1_700_000_000)

    assert existing.read_bytes() == b"preserve me"
