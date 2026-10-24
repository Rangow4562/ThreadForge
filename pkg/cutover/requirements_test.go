package cutover

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var documentedSourceRow = regexp.MustCompile("^\\|\\s*`([^`]+\\.mdx)`\\s*\\|")
var documentedMancodeTargetRow = regexp.MustCompile("^\\|\\s*`(mancode/[^`]+)`\\s*\\|")

var parityRowRequirements = map[string][]string{
	"Beacon Elastic forwarding":                   {"beacon.forwarding.elasticsearch"},
	"Beacon Datadog forwarding":                   {"beacon.forwarding.datadog"},
	"Beacon Sumo Logic forwarding":                {"beacon.forwarding.sumo"},
	"Beacon Rapid7 InsightIDR forwarding":         {"beacon.forwarding.rapid7"},
	"Beacon Wazuh forwarding":                     {"beacon.forwarding.wazuh"},
	"Beacon Microsoft Sentinel forwarding":        {"beacon.forwarding.sentinel"},
	"Beacon capture":                              {"beacon.capture"},
	"Beacon endpoint":                             {"beacon.endpoint"},
	"Beacon security":                             {"beacon.security"},
	"Beacon memory":                               {"beacon.memory"},
	"GitHub Copilot CLI memory injection":         {"beacon.adapter.github-copilot-cli-memory"},
	"Beacon session handoff":                      {"beacon.session-handoff"},
	"Mancode continuity":                          {"mancode.continuity"},
	"Mancode task plans":                          {"mancode.task-plans"},
	"Mancode governance":                          {"mancode.governance"},
	"Mancode project views":                       {"mancode.project-views"},
	"AnyJev":                                      {"anyjev"},
	"AnyJev serving benchmark":                    {"anyjev.serving-benchmark"},
	"AnyJev artifact lifecycle":                   {"anyjev.artifact-lifecycle"},
	"AnyJev paraphrase and listing-order studies": {"anyjev.paraphrase-order"},
	"AnyJev per-item evaluation dumps":            {"anyjev.per-item-dumps"},
	"AnyJev universal studies":                    {"anyjev.universal-studies"},
	"AnyJev 2048 and Minesweeper demos":           {"anyjev.game-demos"},
	"AnyJev Jev-mode demos":                       {"anyjev.jev-mode-demos"},
	"RRSI":                                        {"rrsi.core"},
	"RRSI domains":                                {"rrsi.domain.coding", "rrsi.domain.agentic-workspace", "rrsi.domain.engineering-design"},
	"DeepSeek Harness":                            {"beacon.adapter.deepseek-harness"},
	"Migration framework":                         {"migration.framework"},
	"Migration acceptance":                        {"migration.acceptance"},
	"AnyJev binary distillation caches":           {"anyjev.binary-distillation"},
	"Unified acceptance":                          {"unified.acceptance"},
}

func TestRequiredSourcesMatchDocumentedSourceMatrices(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", "docs", "source-matrix.md"))
	beacon := matrixIDs(t, data, "## Beacon documented runtime sources", documentedSourceRow, "beacon/", ".mdx")
	mancode := matrixIDs(t, data, "## Mancode documented bootstrap targets", documentedMancodeTargetRow, "", "")

	required := make(map[string]struct{}, len(RequiredSources))
	for _, id := range RequiredSources {
		if _, exists := required[id]; exists {
			t.Errorf("RequiredSources contains duplicate ID %q", id)
		}
		required[id] = struct{}{}
	}
	assertSameIDs(t, "Beacon source matrix", beacon, prefixedIDs(required, "beacon/"))
	assertSameIDs(t, "Mancode bootstrap matrix", mancode, prefixedIDs(required, "mancode/"))
}

func TestFeatureParityRowsMapToCutoverRequirements(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", "docs", "feature-parity.md"))
	rows := featureParityRows(data)
	if len(rows) == 0 {
		t.Fatal("feature-parity.md product-area table was not found")
	}

	required := make(map[string]struct{}, len(RequiredFeatures))
	for _, id := range RequiredFeatures {
		if _, exists := required[id]; exists {
			t.Errorf("RequiredFeatures contains duplicate ID %q", id)
		}
		required[id] = struct{}{}
	}
	covered := make(map[string]struct{}, len(required))
	owners := make(map[string]string, len(required))
	for _, row := range rows {
		ids, ok := parityRowRequirements[row]
		if !ok {
			t.Errorf("feature parity row %q has no cutover requirement mapping", row)
			continue
		}
		for _, id := range ids {
			if _, ok := required[id]; !ok {
				t.Errorf("feature parity row %q maps to non-required ID %q", row, id)
			}
			if previous, ok := owners[id]; ok && previous != row {
				t.Errorf("feature parity rows %q and %q share requirement ID %q; each row must be gated independently", previous, row, id)
			}
			owners[id] = row
			covered[id] = struct{}{}
		}
	}
	for row := range parityRowRequirements {
		if !contains(rows, row) {
			t.Errorf("cutover mapping for feature parity row %q is stale", row)
		}
	}
	for _, id := range RequiredFeatures {
		if id == "release.license-review" { // tracked by docs/license-audit.md and THIRD_PARTY.md, outside the product-area table
			continue
		}
		if _, ok := covered[id]; !ok {
			t.Errorf("required feature %q is not represented in the feature parity table", id)
		}
	}
}

func TestUnifiedAcceptanceDocumentsCurrentEvidenceSchema(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", "docs", "feature-parity.md"))
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "| Unified acceptance |") {
			continue
		}
		if !strings.Contains(line, SchemaVersion) || strings.Contains(line, "cutover evidence v3") {
			t.Fatalf("Unified acceptance row must describe current evidence schema %q: %s", SchemaVersion, line)
		}
		return
	}
	t.Fatal("Unified acceptance row was not found in docs/feature-parity.md")
}

func readRepositoryDocument(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func matrixIDs(t *testing.T, data []byte, heading string, rowPattern *regexp.Regexp, prefix, suffix string) map[string]struct{} {
	t.Helper()
	result := make(map[string]struct{})
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}
			inSection = trimmed == heading
			continue
		}
		if !inSection {
			continue
		}
		match := rowPattern.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		id := prefix + strings.TrimSuffix(match[1], suffix)
		if _, exists := result[id]; exists {
			t.Errorf("%s contains duplicate source ID %q", heading, id)
		}
		result[id] = struct{}{}
	}
	if !inSection || len(result) == 0 {
		t.Fatalf("could not read source rows under %q", heading)
	}
	return result
}

func featureParityRows(data []byte) []string {
	var rows []string
	inTable := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "| Product area |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			break
		}
		parts := strings.Split(trimmed, "|")
		if len(parts) < 3 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		if name == "" || strings.Trim(name, "-:") == "" {
			continue
		}
		rows = append(rows, name)
	}
	return rows
}

func prefixedIDs(source map[string]struct{}, prefix string) map[string]struct{} {
	result := make(map[string]struct{})
	for id := range source {
		if strings.HasPrefix(id, prefix) {
			result[id] = struct{}{}
		}
	}
	return result
}

func assertSameIDs(t *testing.T, label string, documented, required map[string]struct{}) {
	t.Helper()
	for id := range documented {
		if _, ok := required[id]; !ok {
			t.Errorf("%s ID %q is not enforced by the cutover assessor", label, id)
		}
	}
	for id := range required {
		if _, ok := documented[id]; !ok {
			t.Errorf("cutover source ID %q is not documented in %s", id, label)
		}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
