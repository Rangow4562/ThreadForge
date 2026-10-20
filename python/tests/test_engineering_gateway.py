from __future__ import annotations

import asyncio
import csv
import importlib
import json
import re
import sys
import types
import xml.etree.ElementTree as ET
from pathlib import Path

import pytest

from threadforge_domains.engineering import gateway
from threadforge_domains.engineering.security import ContainerResult


def _workspace(tmp_path, monkeypatch):
    root = tmp_path / "task-workspace"
    (root / "documents").mkdir(parents=True)
    (root / "output").mkdir()
    monkeypatch.setenv("THREADFORGE_ACTIVE_WORKSPACE", str(root))
    return root


def test_gateway_reads_bounded_text_and_paginates_task_directory(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    (root / "documents" / "notes.txt").write_text("abcdefghij", encoding="utf-8")
    (root / "documents" / "a.csv").write_text("x,y\n1,2\n", encoding="utf-8")
    (root / "documents" / "b.csv").write_text("x,y\n3,4\n", encoding="utf-8")

    page = json.loads(gateway.read_text_file("documents/notes.txt", offset=3, limit=4))
    listing = json.loads(gateway.list_files("documents", limit=1, offset=1))

    assert page == {"text": "defg", "offset": 3, "next_offset": 7, "has_more": True}
    assert listing["items"] == [{"name": "b.csv", "type": "file"}]
    assert listing["total_count"] == 3
    assert listing["has_more"] is True
    assert listing["next_offset"] == 2
    assert "task-workspace" not in listing["items"][0]["name"]


def test_gateway_text_paging_supports_offsets_past_the_list_limit(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    content = "a" * 12_345 + "tail"
    (root / "documents" / "large.txt").write_text(content, encoding="utf-8")

    page = json.loads(gateway.read_text_file("documents/large.txt", offset=12_345, limit=20))

    assert page["text"] == "tail"
    assert page["next_offset"] is None
    assert page["has_more"] is False


def test_gateway_tree_and_search_skip_linked_entries(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    folder = root / "documents" / "design"
    folder.mkdir()
    (folder / "summary.md").write_text("summary", encoding="utf-8")
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "secret-reference.txt").write_text("must not leak", encoding="utf-8")
    link = root / "documents" / "leak"
    try:
        link.symlink_to(outside, target_is_directory=True)
    except (OSError, NotImplementedError):
        pass

    tree = gateway.get_directory_tree("documents", max_entries=20)
    search = json.loads(gateway.search_files("documents", "summary", limit=10, offset=0))
    outside_search = gateway.search_files("documents", "secret-reference")

    assert "design/" in tree
    assert "summary.md" in tree
    assert "secret-reference.txt" not in tree
    assert search["items"] == ["documents/design/summary.md"]
    assert "secret-reference.txt" not in outside_search


def test_gateway_write_is_confined_to_output_and_bounds_content(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    source = root / "documents" / "input.txt"
    source.write_text("keep", encoding="utf-8")

    written = gateway.write_file("output/submission/payload.py", "PAYLOAD = {'valid': True}\n")
    denied = gateway.write_file("documents/input.txt", "overwrite")
    oversized = gateway.write_file("output/large.txt", "x" * (gateway.MAX_WRITE_BYTES + 1))

    assert written == "Wrote 26 characters to output/submission/payload.py"
    payload = root / "output/submission/payload.py"
    assert payload.is_file()
    if sys.platform != "win32":
        payload.chmod(0o444)  # mimic a read-only/container-owned prior deliverable
    replaced = gateway.write_file("output/submission/payload.py", "PAYLOAD = {'valid': False}\n")
    assert replaced == "Wrote 27 characters to output/submission/payload.py"
    assert payload.read_text(encoding="utf-8") == "PAYLOAD = {'valid': False}\n"
    assert source.read_text(encoding="utf-8") == "keep"
    assert "restricted" in denied
    assert "256 KiB" in oversized


def test_gateway_code_exec_uses_one_task_only_container_and_validates_workdir(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    (root / "output" / "work").mkdir()
    benchmark = tmp_path / "benchmark"
    evaluator = tmp_path / "evaluator"
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/eng@sha256:{'a' * 64}"
    monkeypatch.setenv("THREADFORGE_ENGINEERING_BENCHMARK_ROOT", str(benchmark))
    monkeypatch.setenv("THREADFORGE_ENGINEERING_EVALUATOR_ROOT", str(evaluator))
    monkeypatch.setenv("THREADFORGE_ENGINEERING_EXECUTION_IMAGE", image)
    calls = []

    def fake_run(image_arg, workspace, command, **kwargs):
        calls.append((image_arg, workspace, command, kwargs))
        return ContainerResult(0, "inside container", False, False)

    monkeypatch.setattr(gateway, "run_sandboxed", fake_run)
    result = gateway.code_exec("python3 --version", "output/work", 30)
    denied = gateway.code_exec("echo no", str(tmp_path / "outside"), 30)

    assert result == {"success": True, "output": "inside container", "returncode": 0, "timed_out": False}
    assert len(calls) == 1
    image_arg, workspace_arg, command, options = calls[0]
    assert image_arg == image
    assert workspace_arg == root.resolve()
    assert command.startswith("cd -- /task/output/work && ")
    assert options["benchmark_root"] == benchmark.resolve()
    assert options["evaluator_root"] == evaluator.resolve()
    assert "active task workspace" in denied["output"]


def test_gateway_refuses_execution_without_explicit_local_runtime(tmp_path, monkeypatch):
    _workspace(tmp_path, monkeypatch)
    calls = []
    monkeypatch.setattr(gateway, "run_sandboxed", lambda *args, **kwargs: calls.append(args))

    result = gateway.code_exec("echo no")

    assert result["success"] is False
    assert "benchmark and evaluator roots" in result["output"]
    assert calls == []


def test_gateway_read_pdf_parses_only_inside_the_sandbox(tmp_path, monkeypatch):
    root = _workspace(tmp_path, monkeypatch)
    pdf = root / "documents" / "design.pdf"
    pdf.write_bytes(b"fixture pdf bytes")
    benchmark = tmp_path / "benchmark"
    evaluator = tmp_path / "evaluator"
    benchmark.mkdir()
    evaluator.mkdir()
    image = f"threadforge/eng@sha256:{'b' * 64}"
    monkeypatch.setenv("THREADFORGE_ENGINEERING_BENCHMARK_ROOT", str(benchmark))
    monkeypatch.setenv("THREADFORGE_ENGINEERING_EVALUATOR_ROOT", str(evaluator))
    monkeypatch.setenv("THREADFORGE_ENGINEERING_EXECUTION_IMAGE", image)
    calls = []

    def fake_run(image_arg, workspace, command, **kwargs):
        calls.append((image_arg, workspace, command, kwargs))
        return ContainerResult(0, "--- page 1 ---\nfixture text", False, False)

    monkeypatch.setattr(gateway, "run_sandboxed", fake_run)
    result = gateway.read_pdf("documents/design.pdf", max_pages=2)

    assert result == "--- page 1 ---\nfixture text"
    assert len(calls) == 1
    assert "python3 -c" in calls[0][2]
    assert "/task/documents/design.pdf" in calls[0][2]
    assert calls[0][3]["timeout_seconds"] == 120


@pytest.mark.parametrize("host,origin,allowed", [
    ("127.0.0.1:8996", None, True),
    ("localhost:8996", "http://localhost:8996", True),
    ("evil.example:8996", None, False),
    ("127.0.0.1:8996", "https://evil.example", False),
    ("127.0.0.1:8996", "http://127.0.0.1:9000", False),
])
def test_loopback_origin_guard(host, origin, allowed):
    async def exercise():
        forwarded = []

        async def app(scope, receive, send):
            forwarded.append(True)
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ok"})

        guard = gateway.LoopbackOriginGuard(app, 8996)
        scope = {"type": "http", "headers": [(b"host", host.encode("ascii"))]}
        if origin is not None:
            scope["headers"].append((b"origin", origin.encode("ascii")))
        responses = []

        async def send(message):
            responses.append(message)

        await guard(scope, None, send)
        assert bool(forwarded) is allowed
        assert responses[0]["status"] == (200 if allowed else 403)

    asyncio.run(exercise())


def test_loopback_origin_guard_rejects_duplicate_host_and_untrusted_websocket_origin():
    async def exercise():
        async def app(scope, receive, send):
            raise AssertionError("untrusted request must not reach the MCP app")

        guard = gateway.LoopbackOriginGuard(app, 8996)
        sent = []

        async def send(message):
            sent.append(message)

        await guard({"type": "http", "headers": [
            (b"host", b"127.0.0.1:8996"), (b"host", b"evil.example:8996"),
        ]}, None, send)
        assert sent[0]["status"] == 403

        sent.clear()
        await guard({"type": "websocket", "headers": [
            (b"host", b"127.0.0.1:8996"), (b"origin", b"https://evil.example"),
        ]}, None, send)
        assert sent == [{"type": "websocket.close", "code": 1008}]

    asyncio.run(exercise())


def test_gateway_port_is_bounded_loopback_configuration():
    assert gateway._gateway_port("8996") == 8996
    for invalid in ("0", "80", "65536", "invalid"):
        with pytest.raises(ValueError, match="GATEWAY_PORT"):
            gateway._gateway_port(invalid)


def test_gateway_main_fails_actionably_when_fastmcp_is_not_installed(monkeypatch):
    monkeypatch.setattr(gateway, "mcp", None)
    with pytest.raises(RuntimeError, match=r"optional \[workspace\] dependencies"):
        gateway.main()


def test_gateway_registers_narrow_tools_with_behavior_annotations(monkeypatch):
    registered = {}

    class FakeFastMCP:
        def __init__(self, name, *, instructions):
            assert name == "threadforge_engineering_mcp"
            assert "task" in instructions

        def tool(self, *, name, annotations):
            def register(function):
                registered[name] = {"function": function, "annotations": annotations}
                return function
            return register

    fake_package = types.ModuleType("fastmcp")
    fake_package.FastMCP = FakeFastMCP
    with monkeypatch.context() as patch:
        patch.setitem(sys.modules, "fastmcp", fake_package)
        importlib.reload(gateway)
        assert set(registered) == {
            "read_text_file", "list_files", "get_directory_tree", "search_files",
            "write_file", "read_pdf", "code_exec",
        }
        assert registered["read_text_file"]["annotations"]["readOnlyHint"] is True
        assert registered["write_file"]["annotations"]["destructiveHint"] is True
        assert registered["code_exec"]["annotations"]["openWorldHint"] is False
    importlib.reload(gateway)


def test_gateway_main_builds_streamable_http_app_behind_local_host_guard(monkeypatch):
    captured = {}

    class FakeFastMCP:
        def __init__(self, name, *, instructions):
            self.name = name

        def tool(self, *, name, annotations):
            return lambda function: function

        def http_app(self, *, path, transport):
            captured["path"] = path
            captured["transport"] = transport
            return object()

    fake_package = types.ModuleType("fastmcp")
    fake_package.FastMCP = FakeFastMCP
    fake_uvicorn = types.ModuleType("uvicorn")
    fake_uvicorn.run = lambda app, **options: captured.update(app=app, options=options)
    with monkeypatch.context() as patch:
        patch.setitem(sys.modules, "fastmcp", fake_package)
        patch.setitem(sys.modules, "uvicorn", fake_uvicorn)
        patch.setenv("GATEWAY_PORT", "8997")
        importlib.reload(gateway)
        gateway.main()
        assert captured["path"] == "/mcp/"
        assert captured["transport"] == "streamable-http"
        assert isinstance(captured["app"], gateway.LoopbackOriginGuard)
        assert captured["options"]["host"] == "127.0.0.1"
        assert captured["options"]["port"] == 8997
    importlib.reload(gateway)


def test_gateway_read_only_evaluation_has_ten_verified_fixture_answers(monkeypatch):
    root = Path(__file__).parent / "fixtures" / "engineering_gateway_workspace"
    monkeypatch.setenv("THREADFORGE_ACTIVE_WORKSPACE", str(root.resolve()))

    def read(relative):
        return json.loads(gateway.read_text_file(relative))["text"]

    tree = gateway.get_directory_tree(".", max_entries=100)
    catalog_search = json.loads(gateway.search_files(".", "catalog", limit=10, offset=0))
    assert "requirements/" in tree
    assert catalog_search["items"] == ["parts/module_catalog.csv"]

    overview = read("assembly/overview.md")
    revision = read("design/revision_log.md")
    requirements = {
        row["parameter"]: (float(row["value"]), row["unit"])
        for row in csv.DictReader(read("requirements/electrical_limits.csv").splitlines())
    }
    parts = {
        row["module_id"]: row
        for row in csv.DictReader(read("parts/module_catalog.csv").splitlines())
    }
    selected_id = re.search(r"E4 selects (PWR-\d+)", revision).group(1)
    selected = parts[selected_id]
    thermal = read("environment/thermal_policy.md")
    ambient = int(re.search(r"ambient is (\d+) C", thermal).group(1))
    reference = int(re.search(r"(\d+) C reference point", thermal).group(1))
    derate = int(re.search(r"subtract (\d+) C", thermal).group(1))
    derated_limit = int(selected["max_case_c"]) - derate * (ambient - reference)
    acceptance = {}
    for row in csv.DictReader(read("verification/acceptance_matrix.csv").splitlines()):
        try:
            value = float(row["minimum_or_maximum"])
        except ValueError:
            value = row["minimum_or_maximum"]
        acceptance[row["metric"]] = (value, row["unit"])
    runs = list(csv.DictReader(read("tests/repeat_runs.csv").splitlines()))
    max_ripple = max(float(row["ripple_mv_pp"]) for row in runs)
    connectors = list(csv.DictReader(read("assembly/connectors.csv").splitlines()))
    remote_enable = next(row for row in connectors if row["signal"] == "REMOTE_ENABLE")
    bill = list(csv.DictReader(read("assembly/bill_of_materials.csv").splitlines()))

    verified = [
        selected_id,
        f"{requirements['input_minimum'][0] - float(selected['input_min_v']):g} V",
        f"{derated_limit} C",
        f"{acceptance['output_ripple'][0]:g} mV",
        f"{max_ripple:g} mV, {'PASS' if max_ripple <= acceptance['output_ripple'][0] else 'FAIL'}",
        f"{float(selected['continuous_a']) - requirements['continuous_load_current'][0]:.1f} A",
        re.search(r"Power rail: ([0-9.]+ V DC)", overview).group(1),
        f"{remote_enable['connector']}-{remote_enable['pin']}",
        str(len(bill)),
        f"{acceptance['efficiency'][0]:g}%",
    ]
    evaluation = Path(__file__).parent / "fixtures" / "engineering_gateway_evaluation.xml"
    answers = [item.text for item in ET.parse(evaluation).getroot().findall("./qa_pair/answer")]

    assert len(answers) == 10
    assert answers == verified
