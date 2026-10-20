package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeIssueUsesStableCodesAndNeverEchoesParserDetails(t *testing.T) {
	const privateDetail = `api_key="do-not-copy-this-secret" at C:\Users\private\settings.json`
	if got := safeIssue(nil); got != "unsupported_source_format" {
		t.Fatalf("safeIssue(nil) = %q", got)
	}
	if got := safeIssue(errors.New(privateDetail)); got != "source_validation_failed" {
		t.Fatalf("safeIssue returned parser details instead of a code: %q", got)
	}
	if strings.Contains(safeIssue(errors.New(privateDetail)), privateDetail) {
		t.Fatal("safeIssue echoed source-controlled error details")
	}
}

func TestBeaconInspectIssueDoesNotExposeMalformedSourceContents(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, ".beacon", "endpoint", "logs", "runtime.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	privateSource := `{"api_key":"do-not-copy-this-secret","unfinished":` + "\n"
	if err := os.WriteFile(logPath, []byte(privateSource), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Beacon, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 1 || manifest.Files[0].Status != "unsupported" || manifest.Files[0].Issue != "source_validation_failed" {
		t.Fatalf("unexpected sanitized migration issue: %#v", manifest.Files)
	}
	if strings.Contains(manifest.Files[0].Issue, "do-not-copy-this-secret") {
		t.Fatal("migration manifest exposed source-controlled parser details")
	}
}
