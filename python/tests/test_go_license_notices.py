from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
UPSTREAM_LICENSE = ROOT / "third_party" / "agent-beacon" / "LICENSE"
NESTED_MODULE_LICENSE = (
    ROOT / "third_party" / "agent-beacon" / "pkg" / "asymptoteobserve" / "LICENSE"
)


def test_nested_beacon_module_keeps_upstream_license_text():
    assert NESTED_MODULE_LICENSE.is_file()
    assert NESTED_MODULE_LICENSE.read_text(encoding="utf-8") == UPSTREAM_LICENSE.read_text(
        encoding="utf-8"
    )
