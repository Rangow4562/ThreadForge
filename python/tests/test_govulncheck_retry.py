import importlib.util
import io
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = ROOT / "scripts" / "run_govulncheck.py"
SPEC = importlib.util.spec_from_file_location("threadforge_govulncheck_retry", SCRIPT_PATH)
assert SPEC is not None and SPEC.loader is not None
govulncheck = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = govulncheck
SPEC.loader.exec_module(govulncheck)


NETWORK_ERROR = (
    'govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/modules.json.gz": '
    'dial tcp: lookup vuln.go.dev: i/o timeout\nexit status 1\n'
)


def test_retries_only_transient_vulnerability_database_fetch_then_succeeds():
    responses = iter(
        [
            subprocess.CompletedProcess([], 1, "", NETWORK_ERROR),
            subprocess.CompletedProcess([], 0, "No vulnerabilities found.\n", ""),
        ]
    )
    calls = []
    delays = []
    stdout = io.StringIO()
    stderr = io.StringIO()

    result = govulncheck.run_govulncheck(
        run=lambda command, **kwargs: (calls.append((command, kwargs)) or next(responses)),
        sleep=delays.append,
        stdout=stdout,
        stderr=stderr,
    )

    assert result == 0
    assert len(calls) == 2
    assert calls[0][0] == govulncheck.COMMAND
    assert calls[0][1]["cwd"] == ROOT
    assert delays == [5]
    assert "No vulnerabilities found." in stdout.getvalue()
    assert "retrying govulncheck (attempt 2/3) in 5s" in stderr.getvalue()


def test_vulnerability_finding_is_never_retried():
    findings = "Vulnerability #1: GO-2026-0001\nVulnerability IDs: GO-2026-0001\n"
    calls = []
    delays = []

    result = govulncheck.run_govulncheck(
        run=lambda command, **kwargs: (
            calls.append(command) or subprocess.CompletedProcess(command, 1, findings, "")
        ),
        sleep=delays.append,
        stdout=io.StringIO(),
        stderr=io.StringIO(),
    )

    assert result == 1
    assert len(calls) == 1
    assert delays == []


def test_unrecognized_scan_error_is_never_retried():
    calls = []
    delays = []
    error = "govulncheck: invalid package pattern\nexit status 1\n"

    result = govulncheck.run_govulncheck(
        run=lambda command, **kwargs: (
            calls.append(command) or subprocess.CompletedProcess(command, 2, "", error)
        ),
        sleep=delays.append,
        stdout=io.StringIO(),
        stderr=io.StringIO(),
    )

    assert result == 2
    assert len(calls) == 1
    assert delays == []


def test_network_failure_exhausts_three_attempts_without_becoming_a_pass():
    calls = []
    delays = []
    stderr = io.StringIO()

    result = govulncheck.run_govulncheck(
        run=lambda command, **kwargs: (
            calls.append(command) or subprocess.CompletedProcess(command, 1, "", NETWORK_ERROR)
        ),
        sleep=delays.append,
        stdout=io.StringIO(),
        stderr=stderr,
    )

    assert result == 1
    assert len(calls) == 3
    assert delays == [5, 10]
    assert "attempt 3/3" in stderr.getvalue()


def test_other_hosts_and_non_network_database_errors_are_not_retryable():
    assert not govulncheck.is_retryable_database_fetch_failure(
        "govulncheck: fetching vulnerabilities: Get \"https://example.test/index.json\": i/o timeout"
    )
    assert not govulncheck.is_retryable_database_fetch_failure(
        'govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/modules.json.gz": HTTP 500'
    )


def test_transient_dns_server_failure_is_retryable_only_for_vuln_database():
    error = (
        'govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/modules.json.gz": '
        'dial tcp: lookup vuln.go.dev on 192.0.2.53:53: server misbehaving\nexit status 1'
    )
    assert govulncheck.is_retryable_database_fetch_failure(error)
