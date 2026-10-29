package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/threadforge-ai/threadforge/pkg/migrationreview"
)

func TestDecodeUniqueJSONRejectsDuplicateKeysAtEveryObjectDepth(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "top-level", json: `{"value":"first","value":"second"}`},
		{name: "nested", json: `{"nested":{"value":"first","value":"second"}}`},
		{name: "escaped-equivalent", json: `{"value":"first","va\u006cue":"second"}`},
		{name: "inside-array", json: `{"items":[{"value":1,"value":2}]}`},
	}
	type payload struct {
		Value  string `json:"value"`
		Nested struct {
			Value string `json:"value"`
		} `json:"nested"`
		Items []map[string]int `json:"items"`
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value payload
			if err := decodeUniqueJSON([]byte(test.json), &value); err == nil {
				t.Fatal("duplicate object key was accepted")
			}
		})
	}
}

func TestDecodeUniqueJSONAllowsSameKeyInSeparateObjects(t *testing.T) {
	var value struct {
		Items []struct {
			Value int `json:"value"`
		} `json:"items"`
	}
	if err := decodeUniqueJSON([]byte(`{"items":[{"value":1},{"value":2}]}`), &value); err != nil {
		t.Fatalf("same key in distinct objects should be valid: %v", err)
	}
}

func TestCutoverEvaluationReadersRejectDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	content := []byte(`{"schema_version":"one","schema_version":"two"}`)
	digest := sha256.Sum256(content)
	evidence := Evidence{Path: "duplicate.json", SHA256: hex.EncodeToString(digest[:])}
	if err := os.WriteFile(filepath.Join(root, evidence.Path), content, 0o600); err != nil {
		t.Fatal(err)
	}
	verifier := &artifactVerifier{root: root, verified: make(map[string]string)}
	if _, err := verifier.readEvaluationTrials(evidence); err == nil {
		t.Fatal("evaluation reader accepted duplicate JSON keys")
	}
	verifier = &artifactVerifier{root: root, verified: make(map[string]string)}
	if _, err := verifier.readEvaluationAttestation(evidence); err == nil {
		t.Fatal("attestation reader accepted duplicate JSON keys")
	}

	trustPath := filepath.Join(root, "trust.json")
	if err := os.WriteFile(trustPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadEvaluatorTrustStore(trustPath); err == nil {
		t.Fatal("trusted evaluator reader accepted duplicate JSON keys")
	}
}

func TestCutoverMigrationReadersRejectDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	content := []byte(`{"schema_version":"one","schema_version":"two"}`)
	digest := sha256.Sum256(content)
	backupEvidence := Evidence{Path: "backup.json", SHA256: hex.EncodeToString(digest[:])}
	coverageEvidence := Evidence{Path: "coverage.json", SHA256: hex.EncodeToString(digest[:])}
	if err := os.WriteFile(filepath.Join(root, backupEvidence.Path), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, coverageEvidence.Path), content, 0o600); err != nil {
		t.Fatal(err)
	}

	assessment := Assessment{}
	validateMigrationSourceBackup(
		&assessment,
		&artifactVerifier{root: root, verified: make(map[string]string)},
		MigrationEvidence{Product: "fixture", SourceBackupVerification: backupEvidence},
	)
	if !hasBlocker(assessment.Blockers, "source backup verification is invalid JSON") {
		t.Fatalf("duplicate backup verification JSON was not blocked: %#v", assessment.Blockers)
	}

	assessment = Assessment{}
	validateMigrationFormatCoverage(
		&assessment,
		&artifactVerifier{root: root, verified: make(map[string]string)},
		MigrationEvidence{Product: "fixture", FormatCoverage: coverageEvidence},
		migrationreview.TrustStore{},
	)
	if !hasBlocker(assessment.Blockers, "format coverage is invalid JSON") {
		t.Fatalf("duplicate format coverage JSON was not blocked: %#v", assessment.Blockers)
	}
}

func TestDecodeUniqueJSONEnforcesStructuralLimits(t *testing.T) {
	var value any
	tooDeep := "0"
	for range maxCutoverJSONDepth + 2 {
		tooDeep = "[" + tooDeep + "]"
	}
	if err := decodeUniqueJSON([]byte(tooDeep), &value); err == nil {
		t.Fatal("excessively nested JSON was accepted")
	}
	if err := decodeUniqueJSON([]byte(`{} {}`), &value); err == nil {
		t.Fatal("multiple top-level JSON values were accepted")
	}
}
