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


def _inputs(tmp_path: Path):
    binary = tmp_path / "threadforge.exe"
    license_path = tmp_path / "source-LICENSE"
    notice_path = tmp_path / "source-NOTICE"
    third_party_path = tmp_path / "source-THIRD_PARTY.md"
    third_party_licenses_path = tmp_path / "source-go-licenses.tar.gz"
    binary.write_bytes(b"synthetic release binary\x00\x01")
    license_path.write_bytes(b"Apache License 2.0 fixture\n")
    notice_path.write_bytes(b"Threadforge notice fixture\n")
    third_party_path.write_bytes(b"Third-party ledger fixture\n")
    third_party_licenses_path.write_bytes(b"nested target-specific license evidence fixture\n")
    return binary, license_path, notice_path, third_party_path, third_party_licenses_path


def _package(archive_path, inputs, member_name="threadforge.exe"):
    binary, license_path, notice_path, third_party_path, third_party_licenses_path = inputs
    PACKAGER.package(
        archive_path,
        binary,
        member_name,
        license_path,
        notice_path,
        third_party_path,
        third_party_licenses_path,
        1_798_358_401,
    )


def test_release_zip_has_complete_legal_payload_and_repeats_identically(tmp_path):
    inputs = _inputs(tmp_path)
    first = tmp_path / "first.zip"
    second = tmp_path / "second.zip"

    _package(first, inputs)
    _package(second, inputs)

    assert first.read_bytes() == second.read_bytes()
    with zipfile.ZipFile(first) as archive:
        entries = archive.infolist()
        expected_names = [
            "LICENSE",
            "NOTICE",
            "THIRD_PARTY.md",
            "THIRD_PARTY_LICENSES.tar.gz",
            "threadforge.exe",
        ]
        assert [entry.filename for entry in entries] == expected_names
        expected_time = datetime.fromtimestamp(1_798_358_401, timezone.utc)
        for entry in entries:
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
            assert stat.S_IMODE(entry.external_attr >> 16) == (0o755 if entry.filename == "threadforge.exe" else 0o644)
        for name, source in zip(expected_names, (*inputs[1:], inputs[0]), strict=True):
            assert archive.read(name) == source.read_bytes()
        assert archive.testzip() is None


def test_release_zip_refuses_unexpected_binary_member_without_replacing_existing_file(tmp_path):
    inputs = _inputs(tmp_path)
    archive = tmp_path / "existing.zip"
    archive.write_bytes(b"preserve prior artifact")

    try:
        _package(archive, inputs, "unexpected.exe")
    except ValueError as exc:
        assert "must be threadforge.exe" in str(exc)
    else:
        raise AssertionError("packager accepted an unexpected binary member")

    assert archive.read_bytes() == b"preserve prior artifact"


def test_release_zip_requires_every_notice_input_without_replacing_existing_file(tmp_path):
    inputs = list(_inputs(tmp_path))
    inputs[3].unlink()
    archive = tmp_path / "existing.zip"
    archive.write_bytes(b"preserve prior artifact")

    try:
        _package(archive, tuple(inputs))
    except ValueError as exc:
        assert "third-party ledger is unavailable" in str(exc)
    else:
        raise AssertionError("packager accepted a release without THIRD_PARTY.md")

    assert archive.read_bytes() == b"preserve prior artifact"
