package migrate

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func anyJevNPYFixture(t *testing.T, descriptor, shape string, payload []byte) []byte {
	t.Helper()
	header := "{'descr': '" + descriptor + "', 'fortran_order': False, 'shape': " + shape + ", }"
	padding := (16 - ((10 + len(header) + 1) % 16)) % 16
	header += strings.Repeat(" ", padding) + "\n"
	if len(header) > math.MaxUint16 {
		t.Fatal("test NPY fixture header too large")
	}
	var encoded bytes.Buffer
	encoded.Write([]byte{0x93, 'N', 'U', 'M', 'P', 'Y', 1, 0})
	if err := binary.Write(&encoded, binary.LittleEndian, uint16(len(header))); err != nil {
		t.Fatal(err)
	}
	encoded.WriteString(header)
	encoded.Write(payload)
	return encoded.Bytes()
}

func anyJevNPZFixture(t *testing.T, arrays map[string][]byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := zip.NewWriter(&encoded)
	for name, array := range arrays {
		member, err := writer.Create(name + ".npy")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(array); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func anyJevNPYInt64Fixture(t *testing.T, value int64) []byte {
	t.Helper()
	var payload bytes.Buffer
	if err := binary.Write(&payload, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
	return anyJevNPYFixture(t, "<i8", "()", payload.Bytes())
}

func anyJevNPYInt64VectorFixture(t *testing.T, values ...int64) []byte {
	t.Helper()
	var payload bytes.Buffer
	for _, value := range values {
		if err := binary.Write(&payload, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	return anyJevNPYFixture(t, "<i8", "("+strconv.Itoa(len(values))+",)", payload.Bytes())
}

func anyJevNPYFloat32Fixture(t *testing.T, shape string, values ...float32) []byte {
	t.Helper()
	var payload bytes.Buffer
	for _, value := range values {
		if err := binary.Write(&payload, binary.LittleEndian, math.Float32bits(value)); err != nil {
			t.Fatal(err)
		}
	}
	return anyJevNPYFixture(t, "<f4", shape, payload.Bytes())
}

func anyJevNPYFloat16Fixture(t *testing.T, shape string, values ...uint16) []byte {
	t.Helper()
	var payload bytes.Buffer
	for _, value := range values {
		if err := binary.Write(&payload, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	return anyJevNPYFixture(t, "<f2", shape, payload.Bytes())
}

func TestAnyJevDistillationNPZLabelProjectionOmitsPerExampleData(t *testing.T) {
	arrays := map[string][]byte{
		"K":      anyJevNPYInt64Fixture(t, 2),
		"block":  anyJevNPYInt64Fixture(t, 52),
		"probs":  anyJevNPYFloat32Fixture(t, "(2, 2)", 0.9, 0.1, 0.2, 0.8),
		"y_gold": anyJevNPYInt64VectorFixture(t, 0, 1),
	}
	path := "bench/results_distill/synth_labels/private-teacher/security_incidents.urgency.test.npz"
	item, err := normalizeAnyJevDistillationNPZ(path, anyJevNPZFixture(t, arrays))
	if err != nil {
		t.Fatal(err)
	}
	var result anyJevDistillationCacheImport
	if err := json.Unmarshal(item.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if item.Type != "decision_label_or_evaluation" || result.SchemaVersion != anyJevDistillationCacheSchema ||
		result.CacheKind != "teacher_test_labels" || result.ModelSHA256 != anyJevStringSHA256("private-teacher") ||
		result.QuestionSHA256 != anyJevStringSHA256("security_incidents.urgency") || result.Split != "test" ||
		result.Examples != 2 || result.ClassCount != 2 || result.Block != 52 ||
		result.Top1Accuracy == nil || *result.Top1Accuracy != 1 || result.ExpectedCalibration == nil ||
		math.Abs(*result.ExpectedCalibration-0.15) > 1e-6 || result.NegativeLogLoss == nil ||
		result.ArgmaxCounts[0] != 1 || result.ArgmaxCounts[1] != 1 ||
		!result.HumanReviewRequired || result.ExecutionVerified || result.CutoverEligible {
		t.Fatalf("unexpected label cache projection: %#v", result)
	}
	for _, privateValue := range []string{"private-teacher", "security_incidents.urgency", "0.9,0.1"} {
		if strings.Contains(string(item.Payload), privateValue) {
			t.Errorf("label cache projection retained %q", privateValue)
		}
	}
	if !strings.Contains(string(item.Payload), `"omitted_fields"`) || !strings.Contains(string(item.Payload), `"gold_labels"`) {
		t.Fatal("projection does not record omitted per-example label data")
	}
}

func TestAnyJevDistillationNPZFeatureProjectionAndMalformedInputs(t *testing.T) {
	featurePath := "bench/results_distill/synth_features/private-student/security_incidents.urgency.synth.npz"
	featureArrays := map[string][]byte{
		"K":     anyJevNPYInt64Fixture(t, 4),
		"block": anyJevNPYInt64Fixture(t, 24),
		"H":     anyJevNPYFloat16Fixture(t, "(2, 3)", 0x0000, 0x3c00, 0xc000, 0x3800, 0xbc00, 0x4000),
	}
	item, err := normalizeAnyJevDistillationNPZ(featurePath, anyJevNPZFixture(t, featureArrays))
	if err != nil {
		t.Fatal(err)
	}
	var feature anyJevDistillationCacheImport
	if err := json.Unmarshal(item.Payload, &feature); err != nil {
		t.Fatal(err)
	}
	if feature.CacheKind != "student_features" || feature.Examples != 2 || feature.FeatureWidth != 3 ||
		feature.ClassCount != 4 || feature.Block != 24 || strings.Contains(string(item.Payload), "feature_vectors") == false {
		t.Fatalf("unexpected feature projection: %#v", feature)
	}

	validLabels := map[string][]byte{
		"K":     anyJevNPYInt64Fixture(t, 2),
		"block": anyJevNPYInt64Fixture(t, 52),
		"probs": anyJevNPYFloat32Fixture(t, "(2, 2)", 0.9, 0.1, 0.2, 0.8),
	}
	tests := []struct {
		name string
		path string
		data []byte
	}{
		{"unsupported split", "bench/results_distill/synth_labels/model/question.ood.npz", anyJevNPZFixture(t, validLabels)},
		{"gold labels missing in test split", "bench/results_distill/synth_labels/model/question.test.npz", anyJevNPZFixture(t, validLabels)},
		{"unknown array", "bench/results_distill/synth_labels/model/question.synth.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": validLabels["probs"], "unexpected": anyJevNPYInt64Fixture(t, 1),
		})},
		{"split mismatch", "bench/results_distill/synth_labels/model/question.synth.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": validLabels["probs"], "y_gold": anyJevNPYInt64VectorFixture(t, 0, 1),
		})},
		{"non-normalized probabilities", "bench/results_distill/synth_labels/model/question.synth.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": anyJevNPYFloat32Fixture(t, "(2, 2)", 0.9, 0.2, 0.2, 0.8),
		})},
		{"non-finite probabilities", "bench/results_distill/synth_labels/model/question.synth.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": anyJevNPYFloat32Fixture(t, "(2, 2)", float32(math.NaN()), 0.1, 0.2, 0.8),
		})},
		{"pickle object dtype", "bench/results_distill/synth_labels/model/question.synth.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": anyJevNPYFixture(t, "|O8", "(2, 2)", make([]byte, 32)),
		})},
		{"out-of-range gold label", "bench/results_distill/synth_labels/model/question.test.npz", anyJevNPZFixture(t, map[string][]byte{
			"K": validLabels["K"], "block": validLabels["block"], "probs": validLabels["probs"], "y_gold": anyJevNPYInt64VectorFixture(t, 0, 2),
		})},
		{"unsafe filename", "bench/results_distill/synth_labels/token=secret/question.synth.npz", anyJevNPZFixture(t, validLabels)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeAnyJevDistillationNPZ(test.path, test.data); err == nil {
				t.Fatal("malformed or privacy-sensitive cache was accepted")
			}
		})
	}
	if isAnyJevDistillationNPZPath("bench/results_distill/synth_labels/model/question.test.json") ||
		isAnyJevDistillationNPZPath("bench/results_distill_extra/synth_labels/model/question.test.npz") {
		t.Fatal("NPZ importer accepted an out-of-scope path")
	}
}

func TestAnyJevDistillationNPZSnapshotRetainsPrivateBytesAndUpgradesMetadataSnapshot(t *testing.T) {
	sourceRoot := t.TempDir()
	relative := "bench/results_distill/synth_labels/private-teacher/security_incidents.urgency.synth.npz"
	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	data := anyJevNPZFixture(t, map[string][]byte{
		"K":     anyJevNPYInt64Fixture(t, 2),
		"block": anyJevNPYInt64Fixture(t, 52),
		"probs": anyJevNPYFloat32Fixture(t, "(2, 2)", 0.9, 0.1, 0.2, 0.8),
	})
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, sourceRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), snapshot.SourceFingerprint, "manifest.json")
	loaded, err := LoadSnapshot(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	copyBytes, artifact, err := ReadSnapshotArtifact(manifestPath, loaded, relative)
	if err != nil || !bytes.Equal(copyBytes, data) || artifact.SHA256 != digest(data) || artifact.Bytes != int64(len(data)) {
		t.Fatalf("private NPZ readback differs from source: bytes=%d artifact=%#v err=%v", len(copyBytes), artifact, err)
	}
	if !strings.HasPrefix(artifact.LocalPath, "artifacts/sha256/") {
		t.Fatalf("private artifact path is not content-addressed: %s", artifact.LocalPath)
	}

	root := filepath.Dir(manifestPath)
	if err := os.Remove(filepath.Join(root, privateArtifactManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(artifact.LocalPath))); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(manifestPath); err == nil {
		t.Fatal("snapshot missing its private raw cache artifacts was accepted")
	}
	upgraded, err := CreateSnapshot(AnyJev, sourceRoot, dataRoot)
	if err != nil || upgraded.SourceFingerprint != snapshot.SourceFingerprint {
		t.Fatalf("metadata-only snapshot did not upgrade additively: %#v err=%v", upgraded, err)
	}
	copyBytes, _, err = ReadSnapshotArtifact(manifestPath, upgraded, relative)
	if err != nil || !bytes.Equal(copyBytes, data) {
		t.Fatalf("upgraded snapshot did not preserve the exact source bytes: err=%v", err)
	}

	artifactPath := filepath.Join(root, filepath.FromSlash(artifact.LocalPath))
	if err := os.WriteFile(artifactPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(manifestPath); err == nil {
		t.Fatal("tampered private artifact passed snapshot verification")
	}
}

func TestAnyJevDistillationNPZSourceFilesNormalizeSemantically(t *testing.T) {
	sourceRoot := os.Getenv("THREADFORGE_ANYJEV_SOURCE")
	if sourceRoot == "" {
		t.Skip("set THREADFORGE_ANYJEV_SOURCE to run the source-backed NPZ migration check")
	}
	dataRoot := t.TempDir()
	snapshot, err := CreateSnapshot(AnyJev, sourceRoot, dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]struct{})
	excluded := make(map[string]struct{})
	for _, file := range snapshot.Files {
		if isAnyJevDistillationNPZPath(file.Path) {
			if file.Status == "credential_path_excluded" && file.ImportedCount == 0 {
				excluded[file.Path] = struct{}{}
				continue
			}
			if file.Status != "ready" || file.ImportedCount != 1 || file.RejectedCount != 0 {
				t.Fatalf("source NPZ was not strictly importable: path=%s status=%s issue=%s", file.Path, file.Status, file.Issue)
			}
			expected[file.Path] = struct{}{}
		}
	}
	if len(expected)+len(excluded) == 0 {
		t.Fatal("source contains no distillation label or feature caches")
	}
	if snapshot.Summary.FilesCredentialBlocked < len(excluded) {
		t.Fatalf("snapshot did not preserve the source credential-path exclusions: excluded=%d blocked=%d", len(excluded), snapshot.Summary.FilesCredentialBlocked)
	}
	manifestPath := filepath.Join(dataRoot, "migrations", "snapshots", string(AnyJev), snapshot.SourceFingerprint, "manifest.json")
	records, err := ReadSnapshotRecords(manifestPath, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]int, len(expected))
	for _, record := range records {
		if _, ok := expected[record.SourcePath]; !ok {
			continue
		}
		var payload anyJevDistillationCacheImport
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.SchemaVersion != anyJevDistillationCacheSchema {
			t.Fatalf("source cache did not read back as a semantic projection: %s", record.Key)
		}
		if record.Scope != "local_pending_review" || record.Type != "decision_label_or_evaluation" ||
			payload.ExecutionVerified || payload.CutoverEligible {
			t.Fatalf("source cache trust status changed: %s", record.Key)
		}
		seen[record.SourcePath]++
	}
	if len(seen) != len(expected) {
		t.Fatalf("NPZ cache count mismatch: expected=%d readback=%d", len(expected), len(seen))
	}
	if len(expected) > 0 {
		var sourcePath string
		for candidate := range expected {
			sourcePath = candidate
			break
		}
		original, err := os.ReadFile(filepath.Join(sourceRoot, filepath.FromSlash(sourcePath)))
		if err != nil {
			t.Fatal(err)
		}
		copyBytes, reference, err := ReadSnapshotArtifact(manifestPath, snapshot, sourcePath)
		if err != nil || !bytes.Equal(copyBytes, original) || reference.SHA256 != digest(original) || reference.Bytes != int64(len(original)) {
			t.Fatalf("source-backed raw cache artifact failed exact readback: path=%s bytes=%d err=%v", sourcePath, len(copyBytes), err)
		}
	}
	for sourcePath := range expected {
		if seen[sourcePath] != 1 {
			t.Fatalf("source cache did not produce exactly one record (count=%d): %s", seen[sourcePath], sourcePath)
		}
	}
}
