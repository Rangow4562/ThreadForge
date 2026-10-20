from __future__ import annotations

from threadforge_domains.workspace.security import (
    active_workspace,
    safe_exec_env,
    scoped_path,
    valid_pinned_image,
)


def test_workspace_paths_confine_reads_and_output_writes(tmp_path):
    workspace = tmp_path / "task"
    documents = workspace / "documents"
    output = workspace / "output"
    documents.mkdir(parents=True)
    output.mkdir()
    source = documents / "source.txt"
    source.write_text("local fixture", encoding="utf-8")
    env = {"THREADFORGE_ACTIVE_WORKSPACE": str(workspace)}

    assert active_workspace(env) == workspace.resolve()
    assert scoped_path(str(source), environ=env) == source.resolve()
    assert scoped_path(str(output / "deliverable.txt"), write=True, environ=env)
    assert scoped_path(str(source), write=True, environ=env) is None
    assert scoped_path("../outside.txt", environ=env) is None
    assert scoped_path(str(tmp_path / "outside.txt"), environ=env) is None


def test_workspace_boundary_rejects_symlinks_to_outside(tmp_path):
    workspace = tmp_path / "task"
    output = workspace / "output"
    output.mkdir(parents=True)
    outside = tmp_path / "outside"
    outside.mkdir()
    target = outside / "secret.txt"
    target.write_text("not available to task", encoding="utf-8")
    link = output / "linked.txt"
    try:
        link.symlink_to(target)
    except (OSError, NotImplementedError):
        return

    env = {"THREADFORGE_ACTIVE_WORKSPACE": str(workspace)}
    assert scoped_path(str(link), environ=env) is None
    assert scoped_path(str(link), write=True, environ=env) is None


def test_code_execution_environment_drops_secrets():
    env = {
        "PATH": "preserved",
        "OPENAI_API_KEY": "not-forwarded",
        "AWS_ACCESS_KEY_ID": "not-forwarded",
        "GOOGLE_APPLICATION_CREDENTIALS": "not-forwarded",
        "DOCKER_AUTH_CONFIG": "not-forwarded",
        "SERVICE_TOKEN": "not-forwarded",
        "USER_NOTE": "preserved",
    }

    assert safe_exec_env(env) == {"PATH": "preserved", "USER_NOTE": "preserved"}


def test_code_execution_requires_an_immutable_container_digest():
    digest = "a" * 64

    assert valid_pinned_image(f"threadforge/workspace@sha256:{digest}")
    assert valid_pinned_image(f"sha256:{digest}")
    assert not valid_pinned_image("threadforge/workspace:latest")
    assert not valid_pinned_image(f"sha256:{digest.upper()}")
    assert not valid_pinned_image(f"threadforge/workspace@sha256:{digest},--network=host")
