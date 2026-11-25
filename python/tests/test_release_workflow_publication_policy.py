"""Keep CI workflows from publishing public releases before explicit clearance."""

import re
from pathlib import Path


WORKFLOW_DIRECTORY = Path(__file__).resolve().parents[2] / ".github" / "workflows"
FORBIDDEN_PUBLICATION_PATHS = (
    (
        "write permission for repository publication",
        re.compile(r"(?im)^\s*(?:contents|packages|pages|deployments):\s*write\s*(?:#.*)?$"),
    ),
    (
        "known GitHub release-publishing action",
        re.compile(
            r"(?im)^\s*(?:-\s*)?uses:\s*(?:softprops/action-gh-release|ncipollo/release-action|"
            r"marvinpinto/action-automatic-releases)(?:@|\s)"
        ),
    ),
    (
        "GitHub CLI release mutation",
        re.compile(r"(?im)^\s*(?:-\s*)?(?:run:\s*)?gh\s+release\s+(?:create|upload|edit|delete)\b"),
    ),
    (
        "enabled release-asset upload",
        re.compile(r"(?im)^\s*upload-release-assets:\s*true\s*(?:#.*)?$"),
    ),
    (
        "mutating GitHub API request through gh",
        re.compile(
            r"(?im)^\s*(?:-\s*)?(?:run:\s*)?gh\s+api\b[^\r\n]*(?:--method(?:=|\s+)|-X\s+)"
            r"(?:POST|PATCH|PUT|DELETE)\b"
        ),
    ),
)


def publication_policy_violations(workflow_text: str) -> list[str]:
    return [
        description
        for description, pattern in FORBIDDEN_PUBLICATION_PATHS
        if pattern.search(workflow_text)
    ]


def test_current_github_workflows_cannot_publish_public_releases():
    workflow_files = sorted((*WORKFLOW_DIRECTORY.glob("*.yml"), *WORKFLOW_DIRECTORY.glob("*.yaml")))
    assert workflow_files, "expected repository GitHub Actions workflows"

    violations = {
        path.name: publication_policy_violations(path.read_text(encoding="utf-8"))
        for path in workflow_files
    }
    violations = {name: found for name, found in violations.items() if found}
    assert not violations, f"workflow gained a public-release write path: {violations}"


def test_publication_policy_detects_common_write_paths():
    examples = (
        "permissions:\n  contents: write\n",
        "      - uses: softprops/action-gh-release@v2\n",
        "        run: gh release upload v1.2.3 app.zip\n",
        "        upload-release-assets: true\n",
        "        run: gh api --method POST repos/acme/example/releases\n",
    )
    for example in examples:
        assert publication_policy_violations(example), f"publication path was not detected: {example!r}"
