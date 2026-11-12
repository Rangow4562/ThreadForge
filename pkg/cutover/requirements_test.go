package cutover

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	"GitHub Copilot CLI memory access":            {"beacon.adapter.github-copilot-cli-memory"},
	"Aider approved procedure access":             {"harness.aider.approved-procedure"},
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

func TestReleaseCandidateWorkflowCannotPublishPublicReleases(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", ".github", "workflows", "threadforge-tests.yml"))
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse release-candidate workflow: %v", err)
	}
	if len(document.Content) != 1 {
		t.Fatal("release-candidate workflow must contain one YAML document")
	}
	root := document.Content[0]
	triggers := workflowYAMLValue(root, "on")
	if triggers == nil || triggers.Kind != yaml.MappingNode {
		t.Fatal("release-candidate workflow trigger map is missing")
	}
	for index := 0; index+1 < len(triggers.Content); index += 2 {
		trigger := strings.ToLower(triggers.Content[index].Value)
		if trigger == "release" {
			t.Fatal("release-candidate workflow must not run as a public-release publisher")
		}
	}

	permissions := workflowYAMLValue(root, "permissions")
	if permissions == nil || workflowYAMLScalar(permissions, "contents") != "read" {
		t.Fatal("workflow-wide GITHUB_TOKEN contents permission must remain read-only")
	}
	jobs := workflowYAMLValue(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		t.Fatal("release-candidate workflow jobs are missing")
	}
	for _, jobName := range []string{"python-license-evidence", "release-candidate", "release-candidate-bundle"} {
		job := workflowYAMLValue(jobs, jobName)
		jobPermissions := workflowYAMLValue(job, "permissions")
		if job == nil || jobPermissions == nil || workflowYAMLScalar(jobPermissions, "contents") != "read" {
			t.Errorf("candidate job %q must explicitly keep repository contents read-only", jobName)
		}
	}

	sbomActions := 0
	uploadActions := 0
	var inspect func(*yaml.Node)
	inspect = func(node *yaml.Node) {
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index].Value, node.Content[index+1]
				switch key {
				case "permissions":
					inspectWorkflowPermissions(t, value)
				case "uses":
					action := strings.ToLower(value.Value)
					if strings.Contains(action, "release") {
						t.Errorf("workflow must not use a public release-publishing action: %s", value.Value)
					}
					if strings.HasPrefix(action, "anchore/sbom-action@") {
						sbomActions++
						configuration := workflowYAMLValue(node, "with")
						if workflowYAMLScalar(configuration, "upload-release-assets") != "false" ||
							workflowYAMLScalar(configuration, "upload-artifact") != "false" {
							t.Error("candidate SBOM generation must not upload to a public release or secondary artifact")
						}
					}
					if strings.HasPrefix(action, "actions/upload-artifact@") {
						uploadActions++
					}
				case "run":
					script := strings.ToLower(value.Value)
					for _, forbidden := range []string{
						"gh release ", "gh api", "createrelease", "uploadreleaseasset", "/releases/tags/",
					} {
						if strings.Contains(script, forbidden) {
							t.Errorf("workflow shell steps must not publish GitHub releases (found %q)", forbidden)
						}
					}
				}
				if strings.Contains(strings.ToLower(value.Value), "${{ secrets.") {
					t.Error("release-candidate workflow must not consume publisher secrets")
				}
				inspect(value)
			}
			return
		}
		for _, child := range node.Content {
			inspect(child)
		}
	}
	inspect(root)
	if sbomActions != 3 {
		t.Errorf("expected three non-publishing SBOM action configurations, got %d", sbomActions)
	}
	if uploadActions == 0 {
		t.Error("release candidates must remain available as temporary workflow artifacts")
	}
}

func TestEngineeringRuntimeSBOMJobIsNonPublishingAndGated(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", ".github", "workflows", "threadforge-tests.yml"))
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse Engineering runtime SBOM workflow: %v", err)
	}
	root := document.Content[0]
	jobs := workflowYAMLValue(root, "jobs")
	job := workflowYAMLValue(jobs, "engineering-runtime-sbom")
	if job == nil {
		t.Fatal("Engineering runtime SBOM job is missing")
	}
	needs := workflowYAMLValue(job, "needs")
	if needs == nil || needs.Kind != yaml.SequenceNode {
		t.Fatal("Engineering runtime SBOM job must depend on test and release-evidence jobs")
	}
	needValues := map[string]bool{}
	for _, item := range needs.Content {
		needValues[item.Value] = true
	}
	for _, required := range []string{"test", "elasticsearch-local-stack", "python-license-evidence"} {
		if !needValues[required] {
			t.Errorf("Engineering runtime SBOM job must depend on %q", required)
		}
	}
	condition := strings.ToLower(workflowYAMLScalar(job, "if"))
	if !strings.Contains(condition, "github.event_name == 'push'") || !strings.Contains(condition, "github.event_name == 'workflow_dispatch'") {
		t.Error("Engineering runtime image evidence must run only for tagged candidates or explicit release rehearsals")
	}
	permissions := workflowYAMLValue(job, "permissions")
	permissionValues := map[string]string{}
	if permissions != nil && permissions.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(permissions.Content); index += 2 {
			permissionValues[permissions.Content[index].Value] = permissions.Content[index+1].Value
		}
	}
	if len(permissionValues) != 3 || permissionValues["contents"] != "read" || permissionValues["id-token"] != "write" || permissionValues["attestations"] != "write" {
		t.Errorf("Engineering image evidence must have only read contents and attestation permissions, got %#v", permissionValues)
	}
	steps := workflowYAMLValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		t.Fatal("Engineering runtime SBOM steps are missing")
	}
	buildFound, scanFound, bindingFound, verificationFound, uploadFound := false, false, false, false, false
	attestationActions := 0
	for _, step := range steps.Content {
		uses := workflowYAMLScalar(step, "uses")
		if strings.HasPrefix(uses, "actions/attest@") {
			attestationActions++
			with := workflowYAMLValue(step, "with")
			subjectPath := workflowYAMLValue(with, "subject-path")
			if subjectPath == nil || subjectPath.Kind != yaml.ScalarNode || strings.Contains(subjectPath.Value, "\n") {
				t.Error("each Engineering runtime provenance statement must have a single file subject")
			}
		}
		if strings.HasPrefix(uses, "anchore/sbom-action@") {
			with := workflowYAMLValue(step, "with")
			if workflowYAMLScalar(with, "upload-release-assets") != "false" || workflowYAMLScalar(with, "upload-artifact") != "false" {
				t.Error("Engineering image SBOM action must not publish or upload its own artifact")
			}
			scanFound = true
		}
		if strings.HasPrefix(uses, "actions/upload-artifact@") {
			with := workflowYAMLValue(step, "with")
			if workflowYAMLScalar(with, "retention-days") != "14" || workflowYAMLScalar(with, "compression-level") != "0" {
				t.Error("Engineering image bundle must be a short-lived, uncompressed workflow artifact")
			}
			uploadFound = true
		}
		run := strings.ToLower(workflowYAMLScalar(step, "run"))
		buildFound = buildFound || strings.Contains(run, "containers/engineering/build.ps1")
		bindingFound = bindingFound || strings.Contains(run, "scripts/build_engineering_runtime_sbom_binding.py")
		verificationFound = verificationFound || strings.Contains(run, "scripts/verify_engineering_runtime_sbom.py")
		if strings.Contains(run, "gh release create") || strings.Contains(run, "gh release upload") || strings.Contains(run, "gh api -x post") {
			t.Error("Engineering runtime SBOM job must not publish a GitHub release")
		}
	}
	if !buildFound || !scanFound || !bindingFound || !verificationFound || !uploadFound {
		t.Errorf("Engineering runtime SBOM job is missing required build/scan/binding/verification/upload steps: build=%t scan=%t bind=%t verify=%t upload=%t", buildFound, scanFound, bindingFound, verificationFound, uploadFound)
	}
	if attestationActions != 5 {
		t.Errorf("Engineering runtime evidence must attest four payloads individually and bind one archive to its SBOM; got %d attestations", attestationActions)
	}
}

func TestBrowserReplayProbesChromiumBeforeInstallingLinuxPackages(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", ".github", "workflows", "threadforge-tests.yml"))
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse browser replay workflow: %v", err)
	}
	job := workflowYAMLValue(workflowYAMLValue(document.Content[0], "jobs"), "test")
	steps := workflowYAMLValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		t.Fatal("cross-platform test steps are missing")
	}
	for _, step := range steps.Content {
		if workflowYAMLScalar(step, "name") != "install playwright chromium and linux dependencies" {
			continue
		}
		run := workflowYAMLScalar(step, "run")
		installBrowser := strings.Index(run, "npx playwright install chromium")
		probeRuntime := strings.Index(run, "chromium.launch")
		installSystemDeps := strings.Index(run, "npx playwright install-deps chromium")
		if installBrowser < 0 || probeRuntime < 0 || installSystemDeps < 0 || installBrowser > probeRuntime || probeRuntime > installSystemDeps {
			t.Fatal("Linux browser setup must install Chromium, probe a real launch, then conditionally install system dependencies")
		}
		return
	}
	t.Fatal("Linux Chromium setup step is missing")
}

func TestPublishedReleaseVerificationWorkflowIsReadOnly(t *testing.T) {
	data := readRepositoryDocument(t, filepath.Join("..", "..", ".github", "workflows", "threadforge-published-release-verification.yml"))
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse published-release verification workflow: %v", err)
	}
	if len(document.Content) != 1 {
		t.Fatal("published-release verification workflow must contain one YAML document")
	}
	root := document.Content[0]
	triggers := workflowYAMLValue(root, "on")
	releaseEvent := workflowYAMLValue(triggers, "release")
	types := workflowYAMLValue(releaseEvent, "types")
	if types == nil || types.Kind != yaml.SequenceNode || len(types.Content) != 1 || types.Content[0].Value != "published" {
		t.Fatal("published-release verifier must run only after a release is published")
	}
	dispatch := workflowYAMLValue(triggers, "workflow_dispatch")
	versionInput := workflowYAMLValue(workflowYAMLValue(dispatch, "inputs"), "version")
	if versionInput == nil || workflowYAMLScalar(versionInput, "required") != "true" {
		t.Fatal("manual verification must require a release version")
	}

	assertReadOnlyReleasePermissions(t, workflowYAMLValue(root, "permissions"))
	jobs := workflowYAMLValue(root, "jobs")
	job := workflowYAMLValue(jobs, "verify")
	if job == nil {
		t.Fatal("published-release verification job is missing")
	}
	assertReadOnlyReleasePermissions(t, workflowYAMLValue(job, "permissions"))
	if workflowYAMLScalar(job, "timeout-minutes") != "120" {
		t.Error("published-release verification must have a bounded 120-minute timeout")
	}
	env := workflowYAMLValue(job, "env")
	if workflowYAMLScalar(env, "GH_TOKEN") != "${{ github.token }}" {
		t.Error("published-release verifier must use only the read-scoped GitHub token")
	}

	steps := workflowYAMLValue(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		t.Fatal("published-release verification steps are missing")
	}
	usesCount := 0
	verifyCommandFound := false
	candidateLookupFound := false
	candidateDownloadFound := false
	verifierUsesCandidateBundle := false
	var inspect func(*yaml.Node)
	inspect = func(node *yaml.Node) {
		if node == nil {
			return
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index].Value, node.Content[index+1]
				switch key {
				case "uses":
					usesCount++
					if !regexp.MustCompile(`^actions/(checkout|setup-python)@[0-9a-f]{40}$`).MatchString(value.Value) {
						t.Errorf("published-release verifier must use only pinned read-only setup actions, got %q", value.Value)
					}
					if strings.HasPrefix(value.Value, "actions/checkout@") {
						checkoutWith := workflowYAMLValue(node, "with")
						if workflowYAMLScalar(checkoutWith, "ref") != "main" || workflowYAMLScalar(checkoutWith, "persist-credentials") != "false" {
							t.Error("published-release verifier must check out main tooling without persisting credentials")
						}
					}
				case "run":
					script := strings.ToLower(value.Value)
					if strings.Contains(script, "gh run list") &&
						strings.Contains(script, "--workflow threadforge-tests.yml") &&
						strings.Contains(script, "--commit \"$source_commit\"") &&
						strings.Contains(script, "--event push") &&
						strings.Contains(script, ".event == \"push\"") &&
						strings.Contains(script, ".status == \"completed\"") &&
						strings.Contains(script, ".conclusion == \"success\"") &&
						strings.Contains(script, ".headbranch == $tag") &&
						strings.Contains(script, ".headsha == $sha") {
						candidateLookupFound = true
					}
					if strings.Contains(script, "gh run download \"$candidate_run_id\"") &&
						strings.Contains(script, "threadforge-release-candidate-$release_version") {
						candidateDownloadFound = true
					}
					if strings.Contains(script, "scripts/verify_published_release.py") {
						verifyCommandFound = true
						verifierUsesCandidateBundle = strings.Contains(script, "--bundle-dir \"$candidate_dir\"")
					}
					for _, forbidden := range []string{
						"gh release create", "gh release upload", "gh release edit", "gh release delete",
						"--method post", "--method patch", "--method put", "--method delete",
						"gh api -x post", "gh api -x patch", "gh api -x put", "gh api -x delete",
					} {
						if strings.Contains(script, forbidden) {
							t.Errorf("post-publication verifier workflow must not mutate GitHub state (found %q)", forbidden)
						}
					}
				}
				if strings.Contains(strings.ToLower(value.Value), "${{ secrets.") {
					t.Error("published-release verifier must not consume publisher secrets")
				}
				inspect(value)
			}
			return
		}
		for _, child := range node.Content {
			inspect(child)
		}
	}
	inspect(root)
	if usesCount != 2 {
		t.Errorf("expected only checkout and Python setup actions, got %d actions", usesCount)
	}
	if !candidateLookupFound || !candidateDownloadFound || !verifierUsesCandidateBundle || !verifyCommandFound {
		t.Error("workflow must select the exact successful tag candidate run, download its complete bundle, and verify it against the published assets")
	}
}

func assertReadOnlyReleasePermissions(t *testing.T, node *yaml.Node) {
	t.Helper()
	if node == nil || node.Kind != yaml.MappingNode {
		t.Error("published-release verifier permissions must be an explicit map")
		return
	}
	permissions := map[string]string{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		permissions[node.Content[index].Value] = strings.ToLower(node.Content[index+1].Value)
	}
	if len(permissions) != 3 || permissions["contents"] != "read" || permissions["attestations"] != "read" || permissions["actions"] != "read" {
		t.Errorf("published-release verifier must request only contents:read, attestations:read, and actions:read, got %#v", permissions)
	}
}

func inspectWorkflowPermissions(t *testing.T, node *yaml.Node) {
	t.Helper()
	if node == nil || node.Kind != yaml.MappingNode {
		t.Error("workflow permissions must be an explicit map")
		return
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		name, value := node.Content[index].Value, node.Content[index+1].Value
		if name == "contents" && value != "read" {
			t.Errorf("workflow permissions must not grant contents:%s", value)
		}
		if value == "write" && name != "id-token" && name != "attestations" {
			t.Errorf("unexpected write permission %s:%s in candidate workflow", name, value)
		}
	}
}

func workflowYAMLValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func workflowYAMLScalar(node *yaml.Node, key string) string {
	value := workflowYAMLValue(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.ToLower(value.Value)
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
