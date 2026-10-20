package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mancodeProgressHTMLFixture = `<!doctype html><html><body>
<h1>private page title is not migration data</h1>
<script type="application/json" id="mancode-progress-data">
{"schemaVersion":1,"tasks":[{"taskId":"module-export","status":"待审核","reason":null}]}
</script>
</body></html>`

func TestNormalizeMancodeProgressHTMLIsBoundedAndReviewOnly(t *testing.T) {
	payload, err := normalizeMancodeProgressHTML([]byte(mancodeProgressHTMLFixture))
	if err != nil {
		t.Fatal(err)
	}
	var projection map[string]any
	if err := json.Unmarshal(payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection["projection_status"] != "present" || projection["authority"] != "local_pending_review" ||
		projection["task_ids_mapped"] != false || projection["human_review_required"] != true ||
		projection["execution_authorized"] != false {
		t.Fatalf("projection authority was not constrained: %#v", projection)
	}
	tasks, ok := projection["tasks"].([]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("progress task was not preserved: %#v", projection["tasks"])
	}
	task, ok := tasks[0].(map[string]any)
	if !ok || task["task_id"] != "module-export" || task["status"] != "待审核" || task["reason"] != nil {
		t.Fatalf("progress task changed during normalization: %#v", tasks[0])
	}
	if strings.Contains(string(payload), "private page title") || strings.Contains(string(payload), "<script") {
		t.Fatalf("HTML outside the documented JSON block was retained: %s", payload)
	}
}

func TestNormalizeMancodeProgressHTMLReportsMissingProjectionWithoutGuessing(t *testing.T) {
	payload, err := normalizeMancodeProgressHTML([]byte("<html><body><h1>custom progress page</h1></body></html>"))
	if err != nil {
		t.Fatal(err)
	}
	var projection mancodeProgressProjection
	if err := json.Unmarshal(payload, &projection); err != nil {
		t.Fatal(err)
	}
	if projection.ProjectionStatus != "absent_manual_sync" || len(projection.Tasks) != 0 ||
		projection.TaskIDsMapped || projection.ExecutionAuthorized {
		t.Fatalf("missing optional contract was inferred as task state: %#v", projection)
	}
}

func TestNormalizeMancodeProgressHTMLRejectsAmbiguousOrInvalidContracts(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{
			name: "wrong type",
			html: `<script type="text/javascript" id="mancode-progress-data">{"schemaVersion":1,"tasks":[]}</script>`,
		},
		{
			name: "duplicate data blocks",
			html: `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[]}</script><script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[]}</script>`,
		},
		{
			name: "duplicate JSON keys",
			html: `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"schemaVersion":1,"tasks":[]}</script>`,
		},
		{
			name: "duplicate task ID",
			html: `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[{"taskId":"same","status":"未完成","reason":null},{"taskId":"same","status":"已完成","reason":null}]}</script>`,
		},
		{
			name: "unsupported status",
			html: `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[{"taskId":"one","status":"released","reason":null}]}</script>`,
		},
		{
			name: "unknown task field",
			html: `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[{"taskId":"one","status":"未完成","reason":null,"title":"do not infer"}]}</script>`,
		},
		{
			name: "invalid JSON",
			html: `<script type="application/json" id="mancode-progress-data">not-json</script>`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeMancodeProgressHTML([]byte(test.html)); err == nil {
				t.Fatal("invalid or ambiguous projection was accepted")
			}
		})
	}
}

func TestMancodeProgressProjectionSnapshotAndCoverage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "项目进度.html")
	if err := os.WriteFile(path, []byte(mancodeProgressHTMLFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := digest(beforeBytes)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsFound != 1 || manifest.Summary.RecordsImported != 1 || manifest.Summary.RecordsRejected != 0 {
		t.Fatalf("Mancode progress projection did not reconcile: %#v", manifest.Summary)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "mancode_progress_projection" || records[0].Scope != "local_pending_review" {
		t.Fatalf("progress projection was not staged as inert review data: %#v", records)
	}
	if strings.Contains(string(records[0].Payload), "private page title") || strings.Contains(string(records[0].Payload), "<script") {
		t.Fatalf("snapshot retained unrelated page content: %s", records[0].Payload)
	}
	afterBytes, err := os.ReadFile(path)
	after := digest(afterBytes)
	if err != nil || after != before {
		t.Fatalf("source page changed during read-only migration: before=%s after=%s err=%v", before, after, err)
	}
	report, err := BuildFormatCoverage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range report.Formats {
		if format.ID == "mancode.progress-projection" {
			if format.Files != 1 || format.RecordsImported != 1 {
				t.Fatalf("progress projection coverage did not reconcile: %#v", format)
			}
			return
		}
	}
	t.Fatal("Mancode progress projection is absent from the fixed format ledger")
}

func TestMancodeProgressProjectionCredentialReasonIsExcluded(t *testing.T) {
	root := t.TempDir()
	html := `<script type="application/json" id="mancode-progress-data">{"schemaVersion":1,"tasks":[{"taskId":"one","status":"阻塞","reason":"api_key=secret-value"}]}</script>`
	writeMancodeFixture(t, root, "项目进度.html", []byte(html))
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.RecordsCredentialBlocked != 1 || manifest.Summary.RecordsImported != 0 {
		t.Fatalf("credential-bearing progress reason was not excluded: %#v", manifest.Summary)
	}
}
