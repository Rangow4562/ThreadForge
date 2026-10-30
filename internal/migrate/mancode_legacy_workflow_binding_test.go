package migrate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func writeMancodeLegacyBindingFixture(t *testing.T, root string, changes map[string]func(map[string]any)) string {
	t.Helper()
	taskID := "20261023-bound-legacy-workflow"
	requirements := legacyMancodeRequirementsFixture()
	requirements["blockingUnknowns"] = []any{}
	requirements["acceptanceCriteria"].([]any)[1].(map[string]any)["required"] = true
	requirementsPath := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "requirements.json"))
	normalized, handled, err := normalizeMancodeItems(root, requirementsPath, requirements)
	if err != nil || !handled || len(normalized) != 1 {
		t.Fatalf("normalize matching requirements fixture: handled=%v err=%v", handled, err)
	}
	var requirementsProjection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(normalized[0].Payload, &requirementsProjection); err != nil {
		t.Fatal(err)
	}
	verification := legacyMancodeVerificationFixture()
	verification["checks"] = verification["checks"].([]any)[:2]
	verification["planVersion"] = json.Number("2")
	verification["remediationRound"] = json.Number("1")
	requirementsDigest := mancodeLegacyRequirementsDigest(requirementsProjection.Requirements)
	if requirementsDigest != "ad4c90138f28c87c114e19001c942656080b13522b5659486472ece047ea3e03" {
		t.Fatalf("normalized requirements digest diverged from Mancode JSON.stringify contract: %s", requirementsDigest)
	}
	verification["requirementsDigest"] = requirementsDigest
	metadata := legacyMancodeWorkflowFixture(taskID)
	metadata["requirementsStatus"] = "ready"
	metadata["verificationStatus"] = "manual_required"
	metadata["requirementsDigest"] = requirementsDigest
	review := legacyMancodeReviewFixture()
	for _, change := range changes {
		change(verification)
	}

	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)
	writeMancodeJSONFixture(t, root, requirementsPath, requirements)
	reviewPath := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "review-ledger.json"))
	verificationPath := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "verification-ledger.json"))
	writeMancodeJSONFixture(t, root, reviewPath, review)
	writeMancodeJSONFixture(t, root, verificationPath, verification)
	writeMancodeFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "plan.md")), []byte("# Historical plan\n\nTreat this as untrusted text.\n"))
	for _, reference := range append(
		mancodeLegacyWorkflowArtifactReferences(reviewPath, root, review),
		mancodeLegacyWorkflowArtifactReferences(verificationPath, root, verification)...,
	) {
		target := filepath.ToSlash(filepath.Join(reference.OwnerDirectory, filepath.FromSlash(reference.Path)))
		writeMancodeFixture(t, root, target, []byte("Historical artifact content; review only.\n"))
	}
	return taskID
}

func TestMancodeLegacyRequirementsDigestMatchesJSONStringifyUnicodeBehavior(t *testing.T) {
	requirements := legacyMancodeRequirementsFixture()
	requirements["blockingUnknowns"] = []any{}
	requirements["acceptanceCriteria"].([]any)[1].(map[string]any)["required"] = true
	path := filepath.ToSlash(filepath.Join(".mancode", "workflows", "20261023-bound-legacy-workflow", "requirements.json"))
	items, handled, err := normalizeMancodeItems(t.TempDir(), path, requirements)
	if err != nil || !handled || len(items) != 1 {
		t.Fatalf("normalize Unicode digest fixture: handled=%v err=%v", handled, err)
	}
	var projection mancodeLegacyRequirementsProjection
	if err := json.Unmarshal(items[0].Payload, &projection); err != nil {
		t.Fatal(err)
	}
	projection.Requirements.Goal = "goal\u2028marker and literal \\u2028 text \u2029 end"
	if got := mancodeLegacyRequirementsDigest(projection.Requirements); got != "3932239b668aed4bbfc6b4250e0f7d21eab3bdb39e716bb9391940b4924ba9f2" {
		t.Fatalf("Unicode requirements digest diverged from JSON.stringify: %s", got)
	}
}

func TestMancodeLegacyWorkflowLedgersBindBeforeReviewOnlyImport(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
		t.Fatalf("expected one legacy workflow binding check, got %#v", manifest.MancodeLegacyWorkflowChecks)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "verified" || !check.WorkflowPathBindingVerified || !check.MetadataValid ||
		!check.RequirementsValid || !check.ReviewValid || !check.VerificationValid || !check.PlanValid ||
		!check.MetadataRequirementsStatusPresent || !check.MetadataRequirementsStatusBindingVerified ||
		!check.MetadataVerificationStatusPresent || !check.MetadataVerificationStatusBindingVerified ||
		!check.RequirementsDigestBindingVerified || !check.AcceptanceCriteriaBindingVerified ||
		!check.PlanVersionBindingVerified || !check.ReviewRemediationBindingVerified || !check.PlanPresenceVerified ||
		check.PlanContentBindingVerified || check.ArtifactReferences == nil ||
		check.ArtifactReferences.ReferenceCount != 2 || check.ArtifactReferences.FoundCount != 2 ||
		check.ArtifactReferences.ImportedCount != 2 || !check.ArtifactReferences.BindingVerified ||
		len(check.ArtifactReferences.Targets) != 2 {
		t.Fatalf("matching workflow components were not bound conservatively: %#v", check)
	}
	for _, target := range check.ArtifactReferences.Targets {
		if !target.Imported {
			t.Fatalf("matching artifact target was not imported: %#v", check.ArtifactReferences)
		}
	}
	tampered := manifest
	tampered.MancodeLegacyWorkflowChecks = append([]migrationcoverage.MancodeLegacyWorkflowCheck(nil), manifest.MancodeLegacyWorkflowChecks...)
	tampered.MancodeLegacyWorkflowChecks[0].RequirementsSourceSetSHA256 = strings.Repeat("f", 64)
	tampered.SourceFingerprint = sourceFingerprint(tampered.Files, tampered.CrossFileChecks,
		tampered.MancodeLegacyWorkflowChecks, tampered.MancodeGitRef)
	tampered.ID = string(Mancode) + "-" + tampered.SourceFingerprint[:20]
	if VerifyManifest(tampered) == nil {
		t.Fatal("manifest accepted a tampered legacy workflow source-set commitment")
	}
	tamperedArtifact := manifest
	tamperedArtifact.MancodeLegacyWorkflowChecks = append([]migrationcoverage.MancodeLegacyWorkflowCheck(nil), manifest.MancodeLegacyWorkflowChecks...)
	copyOfReferences := *manifest.MancodeLegacyWorkflowChecks[0].ArtifactReferences
	copyOfReferences.Targets = append([]migrationcoverage.MancodeLegacyArtifactTarget(nil), copyOfReferences.Targets...)
	tamperedArtifact.MancodeLegacyWorkflowChecks[0].ArtifactReferences = &copyOfReferences
	tamperedArtifact.MancodeLegacyWorkflowChecks[0].ArtifactReferences.Targets[0].SourceSHA256 = strings.Repeat("f", 64)
	tamperedArtifact.SourceFingerprint = sourceFingerprint(tamperedArtifact.Files, tamperedArtifact.CrossFileChecks,
		tamperedArtifact.MancodeLegacyWorkflowChecks, tamperedArtifact.MancodeGitRef)
	tamperedArtifact.ID = string(Mancode) + "-" + tamperedArtifact.SourceFingerprint[:20]
	if VerifyManifest(tamperedArtifact) == nil {
		t.Fatal("manifest accepted an artifact commitment not backed by the source file inventory")
	}
	encodedCheck, err := json.Marshal(check)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedCheck), taskID) || strings.Contains(string(encodedCheck), "plan.md") {
		t.Fatalf("binding check exposed a task identity or raw path: %s", encodedCheck)
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
	var verification mancodeLegacyVerificationProjection
	for _, record := range records {
		if record.Type == "mancode_legacy_verification_summary" {
			if err := json.Unmarshal(record.Payload, &verification); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !verification.RequirementsBindingVerified || verification.PlanBindingVerified {
		t.Fatalf("verification projection overstated sibling bindings: %#v", verification)
	}
	coverage, err := BuildFormatCoverage(snapshot)
	if err != nil || len(coverage.MancodeLegacyWorkflowChecks) != 1 || coverage.MancodeLegacyWorkflowChecks[0].Status != "verified" {
		t.Fatalf("verified workflow binding did not reconcile into format coverage: checks=%#v err=%v", coverage.MancodeLegacyWorkflowChecks, err)
	}
}

func TestMancodeLegacyWorkflowReferencedArtifactsBlockMissingAndUnimportedTargets(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		writeFile  bool
		wantIssue  string
		wantRefs   int
		wantFound  int
		wantImport int
	}{
		{name: "missing", reference: "reports/missing.txt", wantIssue: "referenced_artifact_missing", wantRefs: 2, wantFound: 1, wantImport: 1},
		{name: "unsupported file type", reference: "reports/result.pdf", writeFile: true, wantIssue: "referenced_artifact_unimported", wantRefs: 2, wantFound: 2, wantImport: 1},
		{name: "credential path", reference: "reports/private-secret.txt", writeFile: true, wantIssue: "referenced_artifact_unimported", wantRefs: 2, wantFound: 2, wantImport: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			taskID := writeMancodeLegacyBindingFixture(t, root, map[string]func(map[string]any){
				"artifact_reference": func(ledger map[string]any) {
					checks := ledger["checks"].([]any)
					automated := checks[0].(map[string]any)["automated"].(map[string]any)
					automated["evidenceFile"] = test.reference
				},
			})
			if test.writeFile {
				relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, filepath.FromSlash(test.reference)))
				writeMancodeFixture(t, root, relative, []byte("Historical artifact content; never execution authorization.\n"))
			}
			if test.name == "missing" {
				relative := filepath.Join(root, ".mancode", "workflows", taskID, filepath.FromSlash(test.reference))
				if err := os.Remove(relative); err != nil {
					t.Fatal(err)
				}
			}
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
				t.Fatalf("expected one workflow reference check: %#v", manifest.MancodeLegacyWorkflowChecks)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.ArtifactReferences == nil || check.ArtifactReferences.ReferenceCount != test.wantRefs ||
				check.ArtifactReferences.FoundCount != test.wantFound || check.ArtifactReferences.ImportedCount != test.wantImport ||
				check.ArtifactReferences.BindingVerified || !containsString(check.Issues, test.wantIssue) || check.Status == "verified" {
				t.Fatalf("missing or non-imported artifact was not surfaced: check=%#v refs=%+v expected=(%d,%d,%d)",
					check, check.ArtifactReferences, test.wantRefs, test.wantFound, test.wantImport)
			}
			encoded, err := json.Marshal(check.ArtifactReferences)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), test.reference) || strings.Contains(string(encoded), taskID) {
				t.Fatalf("artifact check exposed a raw source path or task ID: %s", encoded)
			}
			if test.name == "credential path" {
				relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, filepath.FromSlash(test.reference)))
				for _, entry := range manifest.Files {
					if entry.Path != relative {
						continue
					}
					if entry.Status != "credential_path_excluded" || entry.SourceSHA256 != credentialPathCommitment(relative) ||
						entry.SourceSHA256 == digest([]byte("Historical artifact content; never execution authorization.\n")) {
						t.Fatalf("credential artifact was not represented by a path-only commitment: %#v", entry)
					}
					return
				}
				t.Fatalf("credential artifact exclusion is absent from the source inventory: %s", relative)
			}
		})
	}
}

func TestMancodeLegacySensitiveReferenceDoesNotInvokeContentHasher(t *testing.T) {
	root := t.TempDir()
	taskID := "task-sensitive-artifact"
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "reports", "private-secret.pdf"))
	writeMancodeFixture(t, root, relative, []byte("content that must never be read or hashed"))
	references := []mancodeLegacyWorkflowArtifactReference{{
		OwnerDirectory: filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID)),
		Path:           "reports/private-secret.pdf",
	}}
	var scanned []scannedFile
	var summary Summary
	hasherCalled := false
	bindings, issues, err := reconcileMancodeLegacyArtifactReferencesWithHasher(root, references, &scanned, &summary,
		func(string, string, int64) (FileEntry, error) {
			hasherCalled = true
			return FileEntry{}, errors.New("sensitive artifact content hasher was invoked")
		})
	if err != nil {
		t.Fatal(err)
	}
	if hasherCalled || bindings == nil || bindings.FoundCount != 1 || bindings.ImportedCount != 0 ||
		len(issues) != 1 || issues[0] != "referenced_artifact_unimported" || len(scanned) != 1 ||
		scanned[0].entry.Status != "credential_path_excluded" ||
		scanned[0].entry.SourceSHA256 != credentialPathCommitment(relative) || summary.FilesCredentialBlocked != 1 {
		t.Fatalf("sensitive artifact was read or not safely blocked: hasher=%t bindings=%+v issues=%v scanned=%+v summary=%+v",
			hasherCalled, bindings, issues, scanned, summary)
	}
}

func TestMancodeLegacyWorkflowReferencedTextFormatsImportAsReviewOnlyDocuments(t *testing.T) {
	if eligibleMancodePath(".mancode/memory/project.yaml", t.TempDir()) {
		t.Fatal("workflow-only text extensions broadened Mancode memory-file eligibility")
	}
	if eligibleMancodePath(".mancode/memory/example.py", t.TempDir()) {
		t.Fatal("workflow source-code extensions broadened Mancode memory-file eligibility")
	}
	formats := []struct {
		extension string
		content   string
	}{
		{".yaml", "result: passed\n"},
		{".toml", "result = 'passed'\n"},
		{".csv", "check,result\nunit,passed\n"},
		{".xml", "<result>passed</result>\n"},
		{".html", "<p>result: passed</p>\n"},
		{".sql", "-- verification evidence\nSELECT 1;\n"},
		{".py", "# historical source artifact\nprint('review only')\n"},
		{".ts", "// historical source artifact\nexport const reviewOnly = true;\n"},
		{".go", "// historical source artifact\npackage example\n"},
		{".rs", "// historical source artifact\nfn main() {}\n"},
		{".java", "// historical source artifact\nclass Example {}\n"},
		{".cpp", "// historical source artifact\nint main() { return 0; }\n"},
		{".jsonc", "// historical configuration\n{\"review_only\": true}\n"},
		{".tf", "# historical infrastructure artifact\nresource \"null_resource\" \"example\" {}\n"},
	}
	for _, format := range formats {
		t.Run(format.extension, func(t *testing.T) {
			root := t.TempDir()
			reference := "reports/evidence" + format.extension
			taskID := writeMancodeLegacyBindingFixture(t, root, map[string]func(map[string]any){
				"artifact_reference": func(ledger map[string]any) {
					checks := ledger["checks"].([]any)
					automated := checks[0].(map[string]any)["automated"].(map[string]any)
					automated["evidenceFile"] = reference
				},
			})
			relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, filepath.FromSlash(reference)))
			writeMancodeFixture(t, root, relative, []byte(format.content))

			if !eligibleMancodePath(relative, root) || classifyMancodeRecord(relative) != "workflow_document" {
				t.Fatalf("referenced text extension was not classified as a workflow document: %s", relative)
			}
			items, err := decodeMancodeSourceItems(relative, "", []byte(format.content))
			if err != nil || len(items) != 1 || items[0].(map[string]any)["content"] != format.content {
				t.Fatalf("referenced text content was not preserved: items=%#v err=%v", items, err)
			}
			scanned, _, err := scanFile(Mancode, root, relative, filepath.Join(root, filepath.FromSlash(relative)),
				[]byte(format.content), maxTotalBytes, 0)
			if err != nil || len(scanned.records) != 1 || scanned.records[0].Type != "workflow_document" ||
				scanned.records[0].Scope != "local_pending_review" {
				t.Fatalf("referenced text was not staged as inert review-only content: records=%#v err=%v", scanned.records, err)
			}
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
				t.Fatalf("expected one legacy workflow check, got %#v", manifest.MancodeLegacyWorkflowChecks)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.Status != "verified" || check.ArtifactReferences == nil || !check.ArtifactReferences.BindingVerified {
				t.Fatalf("referenced text document did not bind as imported review-only data: %#v", check)
			}
			for _, entry := range manifest.Files {
				if entry.Path == relative {
					if entry.Status != "ready" || entry.RecordCount != 1 || entry.ImportedCount != 1 {
						t.Fatalf("referenced text document did not produce one staged record: %#v", entry)
					}
					return
				}
			}
			t.Fatalf("referenced text document is absent from the source inventory: %s", relative)
		})
	}
	for _, test := range []struct {
		path string
		data []byte
	}{
		{"evidence.yaml", []byte("unsafe\x00content")},
		{"evidence.py", []byte("unsafe\x00content")},
		{"evidence.ts", []byte{0xff, 0xfe}},
		{"evidence.exe", []byte("not an allowed text suffix")},
	} {
		if _, err := decodeMancodeSourceItems(test.path, "", test.data); err == nil {
			t.Fatalf("unsafe or unsupported Mancode workflow document was accepted: %s", test.path)
		}
	}
}

func TestMancodeLegacyWorkflowReferenceImportsExtensionlessTextAndRejectsUnsafeTargets(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		content    string
		wantImport bool
		wantSecret bool
	}{
		{"extensionless", "reports/evidence", "Historical review-only text.\n", true, false},
		{"unlisted suffix", "reports/evidence.workflow-output", "Historical generated evidence.\n", true, false},
		{"unrecognized JSON content", "reports/evidence.json", "Not valid JSON, but still bounded UTF-8 evidence.\n", true, false},
		{"binary suffix", "reports/evidence.exe", "This is not a text artifact.\n", false, false},
		{"credential content", "reports/evidence.workflow-output", "token=opaque-secret-value\n", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, dataRoot := t.TempDir(), t.TempDir()
			taskID := writeMancodeLegacyBindingFixture(t, root, map[string]func(map[string]any){
				"artifact_reference": func(ledger map[string]any) {
					checks := ledger["checks"].([]any)
					automated := checks[0].(map[string]any)["automated"].(map[string]any)
					automated["evidenceFile"] = test.reference
				},
			})
			relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, filepath.FromSlash(test.reference)))
			writeMancodeFixture(t, root, relative, []byte(test.content))

			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
				t.Fatalf("expected one workflow binding check, got %#v", manifest.MancodeLegacyWorkflowChecks)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.ArtifactReferences == nil {
				t.Fatal("referenced-artifact coverage is missing")
			}
			bound, imported := isMancodeLegacyWorkflowArtifactTarget(relative, root, manifest.MancodeLegacyWorkflowChecks)
			if test.wantImport && (!bound || !imported) {
				t.Fatalf("referenced source path did not bind to its workflow target: bound=%t imported=%t check=%#v",
					bound, imported, check.ArtifactReferences)
			}
			var inspectedFiles []scannedFile
			if _, err := inspect(Mancode, root, &inspectedFiles); err != nil {
				t.Fatal(err)
			}
			for _, scanned := range inspectedFiles {
				if scanned.entry.Path == relative && test.wantImport &&
					(len(scanned.records) != 1 || scanned.records[0].Type != "workflow_document") {
					t.Fatalf("inspect sink lacks the referenced workflow document: %#v", scanned)
				}
			}
			var artifact *FileEntry
			for index := range manifest.Files {
				if manifest.Files[index].Path == relative {
					artifact = &manifest.Files[index]
					break
				}
			}
			if artifact == nil {
				t.Fatal("referenced source file is missing from the migration inventory")
			}
			if test.wantImport {
				if artifact.ImportedCount != 1 || artifact.RecordCount != 1 || artifact.Status != "ready" {
					t.Fatalf("bounded referenced text did not import: %#v", artifact)
				}
			} else if artifact.ImportedCount != 0 {
				t.Fatalf("unsafe/credential-bearing reference was imported: %#v", artifact)
			}
			if test.wantSecret && artifact.ExcludedCount != 1 {
				t.Fatalf("credential-bearing reference was not excluded: %#v", artifact)
			}
			snapshot, err := CreateSnapshot(Mancode, root, dataRoot)
			if err != nil {
				t.Fatal(err)
			}
			coverage, err := BuildFormatCoverage(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if coverage.UnclassifiedFiles != 0 {
				t.Fatalf("bound artifact path became unclassified: %#v", coverage)
			}
			var documentFormat *migrationcoverage.Format
			for index := range coverage.Formats {
				if coverage.Formats[index].ID == "mancode.workflow-document" {
					documentFormat = &coverage.Formats[index]
					break
				}
			}
			if documentFormat == nil || documentFormat.Files < 1 {
				t.Fatalf("referenced artifact was not assigned to the workflow-document family: %#v", coverage.Formats)
			}
			if !test.wantImport {
				return
			}

			manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(Mancode), snapshot.SourceFingerprint, "manifest.json")
			records, err := ReadSnapshotRecords(manifestPath, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if record.SourcePath != relative {
					continue
				}
				var payload map[string]any
				if err := json.Unmarshal(record.Payload, &payload); err != nil || record.Type != "workflow_document" ||
					record.Scope != "local_pending_review" || payload["content"] != test.content {
					t.Fatalf("extension-independent source was not preserved as inert review text: record=%#v payload=%#v err=%v", record, payload, err)
				}
				return
			}
			t.Fatalf("snapshot readback omitted referenced source document %q", relative)
		})
	}
}

func TestMancodeUnreferencedUnlistedWorkflowFileIsNotScanned(t *testing.T) {
	root := t.TempDir()
	relative := filepath.ToSlash(filepath.Join(".mancode", "workflows", "unreferenced-workflow", "reports", "evidence.payload"))
	writeMancodeFixture(t, root, relative, []byte("must not be captured without an explicit ledger reference"))
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 0 || manifest.Summary.FilesScanned != 0 {
		t.Fatalf("unreferenced uncommon-suffix file expanded workflow scan scope: files=%#v summary=%#v", manifest.Files, manifest.Summary)
	}
}

func TestMancodeLegacyWorkflowBindingSurfacesSiblingMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		issue  string
	}{
		{"requirements_digest", func(ledger map[string]any) { ledger["requirementsDigest"] = strings.Repeat("f", 64) }, "requirements_digest_mismatch"},
		{"acceptance_criteria", func(ledger map[string]any) {
			ledger["checks"].([]any)[0].(map[string]any)["acceptanceId"] = "AC-OTHER-1"
		}, "acceptance_criteria_mismatch"},
		{"plan_version", func(ledger map[string]any) { ledger["planVersion"] = json.Number("3") }, "plan_version_mismatch"},
		{"remediation_round", func(ledger map[string]any) { ledger["remediationRound"] = json.Number("0") }, "remediation_round_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeMancodeLegacyBindingFixture(t, root, map[string]func(map[string]any){test.name: test.mutate})
			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.MancodeLegacyWorkflowChecks) != 1 {
				t.Fatalf("expected binding evidence, got %#v", manifest.MancodeLegacyWorkflowChecks)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.Status != "conflicting" || !containsString(check.Issues, test.issue) ||
				check.RequirementsDigestBindingVerified && test.name == "requirements_digest" ||
				check.AcceptanceCriteriaBindingVerified && test.name == "acceptance_criteria" ||
				check.PlanVersionBindingVerified && test.name == "plan_version" ||
				check.ReviewRemediationBindingVerified && test.name == "remediation_round" {
				t.Fatalf("mismatch was hidden or marked verified: %#v", check)
			}
		})
	}
}

func TestMancodeLegacyWorkflowBindingSurfacesMetadataStatusMismatches(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
		issue string
	}{
		{"requirements_status", "requirementsStatus", "needs_clarification", "metadata_requirements_status_mismatch"},
		{"verification_status", "verificationStatus", "pending", "metadata_verification_status_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			taskID := writeMancodeLegacyBindingFixture(t, root, nil)
			metadataPath := filepath.Join(root, ".mancode", "workflows", taskID, "metadata.json")
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatal(err)
			}
			var metadata map[string]any
			if err := json.Unmarshal(data, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata[test.field] = test.value
			writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)

			manifest, err := Inspect(Mancode, root)
			if err != nil {
				t.Fatal(err)
			}
			check := manifest.MancodeLegacyWorkflowChecks[0]
			if check.Status != "conflicting" || !containsString(check.Issues, test.issue) {
				t.Fatalf("metadata status mismatch was hidden: %#v", check)
			}
			if test.field == "requirementsStatus" && (!check.MetadataRequirementsStatusPresent || check.MetadataRequirementsStatusBindingVerified) {
				t.Fatalf("requirements status mismatch flags are inconsistent: %#v", check)
			}
			if test.field == "verificationStatus" && (!check.MetadataVerificationStatusPresent || check.MetadataVerificationStatusBindingVerified) {
				t.Fatalf("verification status mismatch flags are inconsistent: %#v", check)
			}
		})
	}
}

func TestMancodeLegacyWorkflowStatusBindingsRemainOptional(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	metadataPath := filepath.Join(root, ".mancode", "workflows", taskID, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	delete(metadata, "requirementsStatus")
	delete(metadata, "verificationStatus")
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)

	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "verified" || check.MetadataRequirementsStatusPresent || check.MetadataRequirementsStatusBindingVerified ||
		check.MetadataVerificationStatusPresent || check.MetadataVerificationStatusBindingVerified {
		t.Fatalf("absent optional status fields changed legacy workflow verification: %#v", check)
	}
}

func TestMancodeLegacyWorkflowBindingDetectsMetadataRequirementsDigestMismatch(t *testing.T) {
	root := t.TempDir()
	taskID := writeMancodeLegacyBindingFixture(t, root, nil)
	metadata := legacyMancodeWorkflowFixture(taskID)
	metadata["requirementsStatus"] = "ready"
	metadata["verificationStatus"] = "manual_required"
	metadata["requirementsDigest"] = strings.Repeat("f", 64)
	writeMancodeJSONFixture(t, root, filepath.ToSlash(filepath.Join(".mancode", "workflows", taskID, "metadata.json")), metadata)
	manifest, err := Inspect(Mancode, root)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "conflicting" || check.MetadataRequirementsDigestBindingVerified ||
		!containsString(check.Issues, "metadata_requirements_digest_mismatch") {
		t.Fatalf("metadata requirements mismatch was not surfaced: %#v", check)
	}
}

func TestMancodeLegacyWorkflowBindingRequiresPlanPresenceAndSupportsMancodeRoot(t *testing.T) {
	projectRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, projectRoot, nil)
	projectManifest, err := Inspect(Mancode, projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	legacyCheck := projectManifest.MancodeLegacyWorkflowChecks[0]

	legacyRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, legacyRoot, nil)
	legacyManifest, err := Inspect(Mancode, filepath.Join(legacyRoot, ".mancode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyManifest.MancodeLegacyWorkflowChecks) != 1 ||
		legacyManifest.MancodeLegacyWorkflowChecks[0].WorkflowIDSHA256 != legacyCheck.WorkflowIDSHA256 {
		t.Fatalf("project-root and .mancode-root workflow identities differ: %#v versus %#v",
			legacyCheck, legacyManifest.MancodeLegacyWorkflowChecks)
	}

	missingPlanRoot := t.TempDir()
	writeMancodeLegacyBindingFixture(t, missingPlanRoot, nil)
	plan := filepath.Join(missingPlanRoot, ".mancode", "workflows", "20261023-bound-legacy-workflow", "plan.md")
	if err := os.Remove(plan); err != nil {
		t.Fatal(err)
	}
	manifest, err := Inspect(Mancode, missingPlanRoot)
	if err != nil {
		t.Fatal(err)
	}
	check := manifest.MancodeLegacyWorkflowChecks[0]
	if check.Status != "incomplete" || !containsString(check.Issues, "plan_missing") || check.PlanPresenceVerified {
		t.Fatalf("missing plan presence was not surfaced: %#v", check)
	}
}

func TestLegacyManifestWorkflowChecksSortByHashedWorkflowIdentity(t *testing.T) {
	checks := legacyMancodeWorkflowBindingChecks([]FileEntry{
		{Path: ".mancode/workflows/alpha/metadata.json", SourceSHA256: strings.Repeat("a", 64)},
		{Path: ".mancode/workflows/beta/metadata.json", SourceSHA256: strings.Repeat("b", 64)},
	}, t.TempDir())
	if len(checks) != 2 || checks[0].WorkflowIDSHA256 >= checks[1].WorkflowIDSHA256 {
		t.Fatalf("legacy workflow checks are not sorted by their validated hashed identity: %#v", checks)
	}
	if err := migrationcoverage.ValidateMancodeLegacyWorkflowChecks("mancode", checks); err != nil {
		t.Fatalf("sorted legacy workflow checks failed validation: %v", err)
	}
}
