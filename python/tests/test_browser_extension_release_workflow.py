from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW_PATH = ROOT / ".github" / "workflows" / "threadforge-tests.yml"


def test_browser_extension_candidate_is_source_bound_separate_and_required():
    workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
    marker = "  browser-extension-candidate:\n"
    assert marker in workflow
    job = workflow.split(marker, 1)[1].split("\n  release-candidate-bundle:\n", 1)[0]

    assert "needs: [test, release-source-policy]" in job
    assert "github.ref == 'refs/heads/main'" in job
    assert "ref: ${{ github.sha }}" in job
    assert "persist-credentials: false" in job
    assert 'node-version: \'22.19.0\'' in job
    assert 'npm run package:chromium' in job
    assert 'npm run package:firefox' in job
    assert "syft-version: v1.56.0" in job
    assert "path: adapters/browser-extension/dist" in job
    assert "path: adapters/browser-extension/dist-firefox" in job
    assert "npm sbom --package-lock-only --sbom-format spdx" in job
    assert "checksums.txt" in job
    assert "sbom-path:" in job
    assert "threadforge-browser-extension-${{ steps.release.outputs.version }}" in job
    assert "retention-days: 14" in job
    assert "contents: write" not in job
    assert "upload-release-assets: false" in job

    bundle_marker = "  release-candidate-bundle:\n"
    bundle = workflow.split(bundle_marker, 1)[1].split("\n  license-review-index:\n", 1)[0]
    assert "browser-extension-candidate" in bundle.split("steps:", 1)[0]
    assert 'Expected exactly 27 versioned candidate payload files' in bundle


def test_browser_candidate_job_is_not_part_of_the_27_payload_list():
    workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
    expected = (
        'candidate_payloads=(dist/threadforge_"$RELEASE_VERSION"_* '
        '"dist/threadforge_python-${RELEASE_VERSION}.tar.gz" '
        'dist/threadforge_python-"$RELEASE_VERSION"-*.whl)'
    )
    assert expected in workflow
    assert "Expected exactly 27 versioned candidate payload files" in workflow
