import importlib.util
import stat
import zipfile
from datetime import datetime, timezone
from pathlib import Path


PACKAGER_PATH = Path(__file__).resolve().parents[2] / "scripts" / "package_release_zip.py"
PACKAGER_SPEC = importlib.util.spec_from_file_location("threadforge_release_zip_packager", PACKAGER_PATH)
if PACKAGER_SPEC is None or PACKAGER_SPEC.loader is None:
    raise RuntimeError("could not load the release ZIP packager")
PACKAGER = importlib.util.module_from_spec(PACKAGER_SPEC)
PACKAGER_SPEC.loader.exec_module(PACKAGER)


def test_release_zip_has_one_normalized_regular_binary_and_repeats_identically(tmp_path):
    binary = tmp_path / "threadforge.exe"
    binary.write_bytes(b"synthetic release binary\x00\x01")
    first = tmp_path / "first.zip"
    second = tmp_path / "second.zip"
    source_date_epoch = 1_798_358_401

    PACKAGER.package(first, binary, "threadforge.exe", source_date_epoch)
    PACKAGER.package(second, binary, "threadforge.exe", source_date_epoch)

    assert first.read_bytes() == second.read_bytes()
    with zipfile.ZipFile(first) as archive:
        entries = archive.infolist()
        assert len(entries) == 1
        entry = entries[0]
        expected_time = datetime.fromtimestamp(source_date_epoch, timezone.utc)
        assert entry.filename == "threadforge.exe"
        assert entry.date_time == (
            expected_time.year,
            expected_time.month,
            expected_time.day,
            expected_time.hour,
            expected_time.minute,
            expected_time.second // 2 * 2,
        )
        assert entry.compress_type == zipfile.ZIP_STORED
        assert stat.S_IFMT(entry.external_attr >> 16) == stat.S_IFREG
        assert stat.S_IMODE(entry.external_attr >> 16) == 0o755
        assert archive.read(entry) == binary.read_bytes()
        assert archive.testzip() is None


def test_release_zip_refuses_unexpected_member_without_replacing_existing_file(tmp_path):
    binary = tmp_path / "threadforge.exe"
    binary.write_bytes(b"synthetic release binary")
    archive = tmp_path / "existing.zip"
    archive.write_bytes(b"preserve prior artifact")

    try:
        PACKAGER.package(archive, binary, "unexpected.exe", 1_798_358_401)
    except ValueError as exc:
        assert "must be threadforge.exe" in str(exc)
    else:
        raise AssertionError("packager accepted an unexpected archive member")

    assert archive.read_bytes() == b"preserve prior artifact"
