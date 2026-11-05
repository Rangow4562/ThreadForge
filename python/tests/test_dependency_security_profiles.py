from __future__ import annotations

import tomllib
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_optional_inference_profiles_keep_setuptools_on_patched_versions():
    pyproject = tomllib.loads((ROOT / "pyproject.toml").read_text(encoding="utf-8"))
    lock = tomllib.loads((ROOT / "uv.lock").read_text(encoding="utf-8"))

    assert "setuptools>=84.1.0" in pyproject["build-system"]["requires"]
    assert "setuptools>=84.1.0" in pyproject["project"]["optional-dependencies"]["test"]

    hf_requirements = pyproject["project"]["optional-dependencies"]["hf"]
    assert "torch>=2.3; python_version < '3.12'" in hf_requirements
    assert "torch>=2.15; python_version >= '3.12'" in hf_requirements
    serving_requirements = pyproject["project"]["optional-dependencies"]["serving"]
    assert (
        "vllm>=0.32,<1; python_version < '3.12' and sys_platform == 'linux'"
        in serving_requirements
    )

    setuptools_versions = {
        package["version"] for package in lock["package"] if package["name"] == "setuptools"
    }
    assert setuptools_versions
    assert all(
        tuple(int(part) for part in version.split(".")[:3]) >= (84, 1, 0)
        for version in setuptools_versions
    )

    project = next(package for package in lock["package"] if package["name"] == "threadforge-python")
    hf_edges = project["optional-dependencies"]["hf"]
    torch_profiles = {
        edge["marker"]: edge["version"]
        for edge in hf_edges
        if edge["name"] == "torch"
    }
    assert torch_profiles == {
        "python_full_version < '3.12'": "2.13.0",
        "python_full_version >= '3.12'": "2.15.0",
    }

    serving_edges = project["optional-dependencies"]["serving"]
    vllm_profiles = [edge.get("marker") for edge in serving_edges if edge["name"] == "vllm"]
    assert vllm_profiles == ["python_full_version < '3.12' and sys_platform == 'linux'"]
