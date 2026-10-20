package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/decision"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const maxPythonProtocolLine = 18 << 20
const maxPythonProtocolResponse = 18 << 20

type pythonProtocolRequest struct {
	SchemaVersion string          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Params        json.RawMessage `json:"params"`
}

type pythonProtocolResponse struct {
	SchemaVersion string          `json:"schema_version"`
	RequestID     string          `json:"request_id"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         json.RawMessage `json:"error,omitempty"`
}

func runDecision(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "decision command required: calibrate, observe, run, batch, benchmark, distill, artifact, bundle, model fingerprint, or model truncate")
		return 2
	}
	switch args[0] {
	case "calibrate":
		return calibrateDecision(args[1:], stdout, stderr)
	case "observe":
		if len(args) > 1 && args[1] == "history" {
			return decisionObservationHistory(args[2:], stdout, stderr)
		}
		return observeDecision(args[1:], stdout, stderr)
	case "run":
		return runTypedDecision(args[1:], stdout, stderr)
	case "batch":
		return runTypedDecisionBatch(args[1:], stdout, stderr)
	case "benchmark":
		if len(args) < 2 || args[1] != "pipeline" {
			_, _ = fmt.Fprintln(stderr, "usage: threadforge decision benchmark pipeline --file <serving-pipeline-request.json> [--python python]")
			return 2
		}
		return runAnyJevServingPipeline(args[2:], stdout, stderr)
	case "distill":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill <fit|soft|label|extract|extract-gold|pipeline|synth|export-cases> --file <request.json> [--python python]")
			return 2
		}
		switch args[1] {
		case "fit":
			return runAnyJevDistillationFit(args[2:], stdout, stderr)
		case "soft":
			return runAnyJevDistillationSoft(args[2:], stdout, stderr)
		case "label":
			return runAnyJevDistillationLabel(args[2:], stdout, stderr)
		case "extract":
			return runAnyJevDistillationExtract(args[2:], stdout, stderr)
		case "extract-gold":
			return runAnyJevDistillationExtractGold(args[2:], stdout, stderr)
		case "pipeline":
			return runAnyJevDistillationPipeline(args[2:], stdout, stderr)
		case "synth":
			return runAnyJevDistillationSynth(args[2:], stdout, stderr)
		case "export-cases":
			return runAnyJevDistillationExportCases(args[2:], stdout, stderr)
		default:
			_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill <fit|soft|label|extract|extract-gold|pipeline|synth|export-cases> --file <request.json> [--python python]")
			return 2
		}
	case "artifact":
		return runDecisionArtifact(args[1:], stdout, stderr)
	case "bundle":
		return runDecisionBundle(args[1:], stdout, stderr)
	case "model":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "usage: threadforge decision model <fingerprint|truncate> [options]")
			return 2
		}
		switch args[1] {
		case "fingerprint":
			return fingerprintDecisionModel(args[2:], stdout, stderr)
		case "truncate":
			return truncateDecisionModel(args[2:], stdout, stderr)
		default:
			_, _ = fmt.Fprintln(stderr, "usage: threadforge decision model <fingerprint|truncate> [options]")
			return 2
		}
	default:
		_, _ = fmt.Fprintf(stderr, "unknown decision command %q\n", args[0])
		return 2
	}
}

func runAnyJevDistillationFit(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill fit", stderr)
	file := fs.String("file", "", "versioned local AnyJev distillation fit request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill fit --file <distillation-fit-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 4<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "distillation request contains credential material; no cache was read")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 4 || len(fields) > 6 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev distillation fit request envelope")
		return 1
	}
	for key := range fields {
		if key != "schema_version" && key != "cache_root" && key != "teacher_model" && key != "budgets" &&
			key != "bootstrap_resamples" && key != "questions" {
			_, _ = fmt.Fprintln(stderr, "AnyJev distillation request contains unsupported fields")
			return 1
		}
	}
	var version string
	if raw := fields["schema_version"]; len(raw) == 0 || decodeJSONBytes(raw, &version) != nil ||
		version != "threadforge.anyjev-distillation-fit-request.v1" {
		_, _ = fmt.Fprintln(stderr, "unsupported AnyJev distillation fit request version")
		return 1
	}
	var questions []json.RawMessage
	if raw := fields["questions"]; len(raw) == 0 || decodeJSONBytes(raw, &questions) != nil || len(questions) < 1 || len(questions) > 256 {
		_, _ = fmt.Fprintln(stderr, "distillation request must include 1 to 256 question cache groups")
		return 1
	}
	expectedCalibrationExamples := 300
	if raw := fields["calibration_examples"]; len(raw) != 0 && decodeJSONBytes(raw, &expectedCalibrationExamples) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation calibration example count is invalid")
		return 1
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-fit", "decision.distill.fit", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation fit failed safely; source paths and numerical details were withheld")
		return 1
	}
	var result struct {
		SchemaVersion          string            `json:"schema_version"`
		TeacherSHA256          string            `json:"teacher_sha256"`
		CalibrationExamples    int               `json:"calibration_examples"`
		Budgets                []int             `json:"budgets"`
		BootstrapResamples     int               `json:"bootstrap_resamples"`
		QuestionCount          int               `json:"question_count"`
		TotalTestExamples      int               `json:"total_test_examples"`
		Teacher                json.RawMessage   `json:"teacher"`
		Students               []json.RawMessage `json:"students"`
		PerQuestion            []json.RawMessage `json:"per_question"`
		InputDigests           []json.RawMessage `json:"input_digests"`
		HumanReviewRequired    bool              `json:"human_review_required"`
		ModelInferenceVerified bool              `json:"model_inference_verified"`
		ExecutionVerified      bool              `json:"execution_verified"`
		CutoverEligible        bool              `json:"cutover_evidence_eligible"`
	}
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-fit-result.v1" ||
		result.CalibrationExamples != expectedCalibrationExamples || result.QuestionCount != len(questions) ||
		result.TotalTestExamples < 2 || len(result.Students) < 1 || len(result.InputDigests) < 5 ||
		!result.HumanReviewRequired || result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEligible {
		_, _ = fmt.Fprintf(stderr, "Python returned an invalid or unsafe AnyJev distillation result (version=%t calibration=%d/%d questions=%d/%d test_examples=%d students=%d input_digests=%d review_required=%t inference_verified=%t execution_verified=%t cutover_eligible=%t)\n",
			result.SchemaVersion == "threadforge.anyjev-distillation-fit-result.v1", result.CalibrationExamples,
			expectedCalibrationExamples, result.QuestionCount, len(questions), result.TotalTestExamples,
			len(result.Students), len(result.InputDigests), result.HumanReviewRequired,
			result.ModelInferenceVerified, result.ExecutionVerified, result.CutoverEligible)
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev distillation output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationSoft(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill soft", stderr)
	file := fs.String("file", "", "versioned local AnyJev soft-target replay request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill soft --file <distillation-soft-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 4<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "soft distillation request contains credential material; no cache was read")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 6 || len(fields) > 7 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev soft distillation request envelope")
		return 1
	}
	for key := range fields {
		if key != "schema_version" && key != "cache_root" && key != "teacher_model" && key != "teacher_block" &&
			key != "teacher_n_layers" && key != "bootstrap_resamples" && key != "questions" {
			_, _ = fmt.Fprintln(stderr, "soft distillation request contains unsupported fields")
			return 1
		}
	}
	var version string
	if raw := fields["schema_version"]; len(raw) == 0 || decodeJSONBytes(raw, &version) != nil ||
		version != "threadforge.anyjev-distillation-soft-request.v1" {
		_, _ = fmt.Fprintln(stderr, "unsupported AnyJev soft distillation request version")
		return 1
	}
	var questions []json.RawMessage
	if raw := fields["questions"]; len(raw) == 0 || decodeJSONBytes(raw, &questions) != nil || len(questions) < 1 || len(questions) > 256 {
		_, _ = fmt.Fprintln(stderr, "soft distillation request must include 1 to 256 question cache groups")
		return 1
	}
	var expectedTeacherBlock int
	if raw := fields["teacher_block"]; len(raw) == 0 || decodeJSONBytes(raw, &expectedTeacherBlock) != nil {
		_, _ = fmt.Fprintln(stderr, "soft distillation teacher block is invalid")
		return 1
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "soft distillation request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-soft", "decision.distill.soft", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev soft distillation replay failed safely; source paths and numerical details were withheld")
		return 1
	}
	var result struct {
		SchemaVersion          string            `json:"schema_version"`
		TeacherSHA256          string            `json:"teacher_sha256"`
		TeacherBlock           int               `json:"teacher_block"`
		QuestionCount          int               `json:"question_count"`
		TotalTestExamples      int               `json:"total_test_examples"`
		BootstrapResamples     int               `json:"bootstrap_resamples"`
		Teacher                json.RawMessage   `json:"teacher"`
		Students               []json.RawMessage `json:"students"`
		PerQuestion            []json.RawMessage `json:"per_question"`
		InputDigests           []json.RawMessage `json:"input_digests"`
		HumanReviewRequired    bool              `json:"human_review_required"`
		ModelInferenceVerified bool              `json:"model_inference_verified"`
		ExecutionVerified      bool              `json:"execution_verified"`
		CutoverEligible        bool              `json:"cutover_evidence_eligible"`
	}
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-soft-result.v1" ||
		result.TeacherBlock != expectedTeacherBlock || result.QuestionCount != len(questions) ||
		result.TotalTestExamples < 2 || len(result.Students) < 1 || len(result.InputDigests) < 4 ||
		!result.HumanReviewRequired || result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev soft distillation result")
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev soft distillation output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationLabel(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill label", stderr)
	file := fs.String("file", "", "versioned local AnyJev teacher-label request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill label --file <distillation-label-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 16<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "distillation label request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) != 9 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev distillation label request envelope")
		return 1
	}
	for key := range fields {
		if key != "schema_version" && key != "data_root" && key != "cache_root" && key != "teacher_model" &&
			key != "teacher_model_checkpoint_sha256" && key != "backend" && key != "teacher_artifact" &&
			key != "workflows" && key != "questions" {
			_, _ = fmt.Fprintln(stderr, "distillation label request contains unsupported fields")
			return 1
		}
	}
	var version, teacherModel, checkpointSHA string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-label-request.v1" ||
		decodeJSONBytes(fields["teacher_model"], &teacherModel) != nil || strings.TrimSpace(teacherModel) == "" ||
		decodeJSONBytes(fields["teacher_model_checkpoint_sha256"], &checkpointSHA) != nil || !validLowerSHA256(checkpointSHA) {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev distillation label request")
		return 1
	}
	var workflows, questions []json.RawMessage
	if decodeJSONBytes(fields["workflows"], &workflows) != nil || len(workflows) < 1 || len(workflows) > 256 ||
		decodeJSONBytes(fields["questions"], &questions) != nil || len(questions) < 1 || len(questions) > 256 {
		_, _ = fmt.Fprintln(stderr, "distillation label request must contain 1 to 256 workflows and questions")
		return 1
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation label request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-label", "decision.distill.label", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation labeling failed safely; source paths and model details were withheld")
		return 1
	}
	type cacheResult struct {
		Role                    string `json:"role"`
		QuestionSHA256          string `json:"question_sha256"`
		TeacherSHA256           string `json:"teacher_sha256"`
		TeacherCheckpointSHA256 string `json:"teacher_checkpoint_sha256"`
		SourceSHA256            string `json:"source_sha256"`
		Examples                int    `json:"examples"`
		Classes                 int    `json:"classes"`
		Block                   int    `json:"block"`
		RelativePath            string `json:"relative_path"`
		SHA256                  string `json:"sha256"`
	}
	var result struct {
		SchemaVersion           string        `json:"schema_version"`
		TeacherSHA256           string        `json:"teacher_sha256"`
		TeacherCheckpointSHA256 string        `json:"teacher_checkpoint_sha256"`
		QuestionCount           int           `json:"question_count"`
		TotalSyntheticExamples  int           `json:"total_synthetic_examples"`
		TotalTestExamples       int           `json:"total_test_examples"`
		Caches                  []cacheResult `json:"caches"`
		HumanReviewRequired     bool          `json:"human_review_required"`
		ModelInferenceVerified  bool          `json:"model_inference_verified"`
		ExecutionVerified       bool          `json:"execution_verified"`
		CutoverEligible         bool          `json:"cutover_evidence_eligible"`
	}
	teacherDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(teacherModel)))
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-label-result.v1" ||
		result.TeacherSHA256 != teacherDigest || result.TeacherCheckpointSHA256 != checkpointSHA ||
		result.QuestionCount != len(questions) || len(result.Caches) != len(questions)*2 ||
		result.TotalSyntheticExamples < 1 || result.TotalTestExamples < 1 || !result.HumanReviewRequired ||
		!result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev distillation-label result")
		return 1
	}
	seen := make(map[string]map[string]bool)
	for _, cache := range result.Caches {
		if !validLowerSHA256(cache.QuestionSHA256) || cache.TeacherSHA256 != teacherDigest ||
			cache.TeacherCheckpointSHA256 != checkpointSHA || !validLowerSHA256(cache.SourceSHA256) ||
			!validLowerSHA256(cache.SHA256) || cache.Examples < 1 || cache.Examples > 10000 ||
			cache.Classes < 2 || cache.Classes > 26 || cache.Block < 1 || cache.Block > 100000 ||
			(cache.Role != "teacher_synthetic_probabilities" && cache.Role != "teacher_test_probabilities") {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev distillation cache receipt")
			return 1
		}
		wantPrefix := "distillation/synth_labels/" + checkpointSHA + "/" + cache.QuestionSHA256 + "."
		if !strings.HasPrefix(cache.RelativePath, wantPrefix) ||
			!(strings.HasSuffix(cache.RelativePath, ".synth.npz") || strings.HasSuffix(cache.RelativePath, ".test.npz")) {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev cache path")
			return 1
		}
		split := strings.TrimSuffix(strings.TrimPrefix(cache.RelativePath, wantPrefix), ".npz")
		if (cache.Role == "teacher_synthetic_probabilities" && split != "synth") ||
			(cache.Role == "teacher_test_probabilities" && split != "test") {
			_, _ = fmt.Fprintln(stderr, "Python returned inconsistent AnyJev cache role and path")
			return 1
		}
		if seen[cache.QuestionSHA256] == nil {
			seen[cache.QuestionSHA256] = make(map[string]bool)
		}
		if seen[cache.QuestionSHA256][cache.Role] {
			_, _ = fmt.Fprintln(stderr, "Python returned duplicate AnyJev cache receipts")
			return 1
		}
		seen[cache.QuestionSHA256][cache.Role] = true
	}
	if len(seen) != len(questions) {
		_, _ = fmt.Fprintln(stderr, "Python returned an incomplete AnyJev cache set")
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev distillation-label output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationExtract(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill extract", stderr)
	file := fs.String("file", "", "versioned local AnyJev student-feature extraction request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill extract --file <distillation-extract-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 16<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "distillation extract request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 9 || len(fields) > 10 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev distillation extract request envelope")
		return 1
	}
	for key := range fields {
		if key != "schema_version" && key != "data_root" && key != "cache_root" && key != "student_model" &&
			key != "student_model_checkpoint_sha256" && key != "backend" && key != "block" &&
			key != "seed" && key != "workflows" && key != "questions" {
			_, _ = fmt.Fprintln(stderr, "distillation extract request contains unsupported fields")
			return 1
		}
	}
	var version, studentModel, checkpointSHA string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-extract-request.v1" ||
		decodeJSONBytes(fields["student_model"], &studentModel) != nil || strings.TrimSpace(studentModel) == "" ||
		decodeJSONBytes(fields["student_model_checkpoint_sha256"], &checkpointSHA) != nil || !validLowerSHA256(checkpointSHA) {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev distillation extract request")
		return 1
	}
	var block int
	if decodeJSONBytes(fields["block"], &block) != nil || block < 1 || block > 100000 {
		_, _ = fmt.Fprintln(stderr, "distillation extract block is invalid")
		return 1
	}
	var workflows, questions []json.RawMessage
	if decodeJSONBytes(fields["workflows"], &workflows) != nil || len(workflows) < 1 || len(workflows) > 256 ||
		decodeJSONBytes(fields["questions"], &questions) != nil || len(questions) < 1 || len(questions) > 256 {
		_, _ = fmt.Fprintln(stderr, "distillation extract request must contain 1 to 256 workflows and questions")
		return 1
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation extract request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-extract", "decision.distill.extract", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation feature extraction failed safely; source paths and model details were withheld")
		return 1
	}
	type cacheResult struct {
		Role                    string `json:"role"`
		QuestionSHA256          string `json:"question_sha256"`
		StudentSHA256           string `json:"student_sha256"`
		StudentCheckpointSHA256 string `json:"student_checkpoint_sha256"`
		SourceSHA256            string `json:"source_sha256"`
		Examples                int    `json:"examples"`
		Classes                 int    `json:"classes"`
		Block                   int    `json:"block"`
		FeatureWidth            int    `json:"feature_width"`
		RelativePath            string `json:"relative_path"`
		SHA256                  string `json:"sha256"`
	}
	var result struct {
		SchemaVersion           string        `json:"schema_version"`
		StudentSHA256           string        `json:"student_sha256"`
		StudentCheckpointSHA256 string        `json:"student_checkpoint_sha256"`
		QuestionCount           int           `json:"question_count"`
		TotalSyntheticExamples  int           `json:"total_synthetic_examples"`
		Caches                  []cacheResult `json:"caches"`
		HumanReviewRequired     bool          `json:"human_review_required"`
		ModelInferenceVerified  bool          `json:"model_inference_verified"`
		ExecutionVerified       bool          `json:"execution_verified"`
		CutoverEligible         bool          `json:"cutover_evidence_eligible"`
	}
	studentDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(studentModel)))
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-extract-result.v1" ||
		result.StudentSHA256 != studentDigest || result.StudentCheckpointSHA256 != checkpointSHA ||
		result.QuestionCount != len(questions) || len(result.Caches) != len(questions) ||
		result.TotalSyntheticExamples < 1 || !result.HumanReviewRequired || !result.ModelInferenceVerified ||
		result.ExecutionVerified || result.CutoverEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev distillation-extract result")
		return 1
	}
	seen := make(map[string]bool)
	totalExamples := 0
	for _, cache := range result.Caches {
		if cache.Role != "student_synthetic_features" || !validLowerSHA256(cache.QuestionSHA256) ||
			cache.StudentSHA256 != studentDigest || cache.StudentCheckpointSHA256 != checkpointSHA ||
			!validLowerSHA256(cache.SourceSHA256) || !validLowerSHA256(cache.SHA256) ||
			cache.Examples < 1 || cache.Examples > 10000 || cache.Classes < 2 || cache.Classes > 26 ||
			cache.Block != block || cache.FeatureWidth < 1 || cache.FeatureWidth > 65536 {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev feature-cache receipt")
			return 1
		}
		wantPath := "distillation/synth_features/" + checkpointSHA + "/" + cache.QuestionSHA256 + ".synth.npz"
		if cache.RelativePath != wantPath || seen[cache.QuestionSHA256] {
			_, _ = fmt.Fprintln(stderr, "Python returned a duplicate or invalid AnyJev feature-cache path")
			return 1
		}
		seen[cache.QuestionSHA256] = true
		totalExamples += cache.Examples
	}
	if totalExamples != result.TotalSyntheticExamples {
		_, _ = fmt.Fprintln(stderr, "Python returned an inconsistent AnyJev feature-cache count")
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev distillation-extract output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationExtractGold(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill extract-gold", stderr)
	file := fs.String("file", "", "versioned local AnyJev gold-feature extraction request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill extract-gold --file <extract-gold-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 16<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "gold-feature request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 9 || len(fields) > 10 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev gold-feature request envelope")
		return 1
	}
	allowed := map[string]bool{
		"schema_version": true, "data_root": true, "cache_root": true, "model_role": true,
		"model": true, "model_checkpoint_sha256": true, "backend": true, "block": true,
		"seed": true, "questions": true,
	}
	for key := range fields {
		if !allowed[key] {
			_, _ = fmt.Fprintln(stderr, "gold-feature request contains unsupported fields")
			return 1
		}
	}
	var version, dataRoot, cacheRoot, modelRole, model, checkpointSHA string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-extract-gold-request.v1" ||
		decodeJSONBytes(fields["data_root"], &dataRoot) != nil || !filepath.IsAbs(dataRoot) ||
		decodeJSONBytes(fields["cache_root"], &cacheRoot) != nil || !filepath.IsAbs(cacheRoot) ||
		decodeJSONBytes(fields["model_role"], &modelRole) != nil || (modelRole != "teacher" && modelRole != "student") ||
		decodeJSONBytes(fields["model"], &model) != nil || strings.TrimSpace(model) == "" || len(model) > 512 ||
		decodeJSONBytes(fields["model_checkpoint_sha256"], &checkpointSHA) != nil || !validLowerSHA256(checkpointSHA) {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev gold-feature request")
		return 1
	}
	var backend map[string]json.RawMessage
	if decodeJSONBytes(fields["backend"], &backend) != nil || len(backend) < 2 || len(backend) > 5 {
		_, _ = fmt.Fprintln(stderr, "gold-feature request backend is invalid")
		return 1
	}
	for key := range backend {
		if key != "type" && key != "model_path" && key != "device" && key != "dtype" && key != "batch_size" {
			_, _ = fmt.Fprintln(stderr, "gold-feature request backend contains unsupported fields")
			return 1
		}
	}
	var backendType, modelPath string
	if decodeJSONBytes(backend["type"], &backendType) != nil || backendType != "hf" ||
		decodeJSONBytes(backend["model_path"], &modelPath) != nil || !filepath.IsAbs(modelPath) {
		_, _ = fmt.Fprintln(stderr, "gold-feature request requires a local HF checkpoint path")
		return 1
	}
	var block int
	if decodeJSONBytes(fields["block"], &block) != nil || block < 1 || block > 100000 {
		_, _ = fmt.Fprintln(stderr, "gold-feature request block is invalid")
		return 1
	}
	if raw, ok := fields["seed"]; ok {
		var seed uint64
		if decodeJSONBytes(raw, &seed) != nil || seed > 1<<32-1 {
			_, _ = fmt.Fprintln(stderr, "gold-feature request seed is invalid")
			return 1
		}
	}
	type requestedQuestion struct {
		ID             string          `json:"question_id"`
		Workflow       string          `json:"workflow"`
		Question       json.RawMessage `json:"question"`
		TrainCasesPath string          `json:"train_cases_path"`
		TestCasesPath  string          `json:"test_cases_path"`
	}
	var rawQuestions []json.RawMessage
	if decodeJSONBytes(fields["questions"], &rawQuestions) != nil || len(rawQuestions) < 1 || len(rawQuestions) > 256 {
		_, _ = fmt.Fprintln(stderr, "gold-feature request must contain 1 to 256 questions")
		return 1
	}
	questionIDs := make(map[string]bool, len(rawQuestions))
	questionDigests := make(map[string]bool, len(rawQuestions))
	for _, raw := range rawQuestions {
		var questionFields map[string]json.RawMessage
		var question requestedQuestion
		if decodeJSONBytes(raw, &questionFields) != nil || len(questionFields) != 5 ||
			decodeJSONBytes(raw, &question) != nil || strings.TrimSpace(question.ID) == "" || len(question.ID) > 512 ||
			strings.TrimSpace(question.Workflow) == "" || len(question.Workflow) > 128 || len(question.Question) == 0 {
			_, _ = fmt.Fprintln(stderr, "gold-feature request contains an invalid question entry")
			return 1
		}
		for key := range questionFields {
			if key != "question_id" && key != "workflow" && key != "question" &&
				key != "train_cases_path" && key != "test_cases_path" {
				_, _ = fmt.Fprintln(stderr, "gold-feature question contains unsupported fields")
				return 1
			}
		}
		validSourcePath := func(value string) bool {
			if value == "" || len(value) > 4096 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || !strings.HasSuffix(value, ".json") {
				return false
			}
			for _, part := range strings.Split(value, "/") {
				if part == "" || part == "." || part == ".." {
					return false
				}
			}
			return true
		}
		if !validSourcePath(question.TrainCasesPath) || !validSourcePath(question.TestCasesPath) ||
			questionIDs[question.ID] {
			_, _ = fmt.Fprintln(stderr, "gold-feature question identity or source path is invalid or duplicated")
			return 1
		}
		questionIDs[question.ID] = true
		questionDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(question.ID)))
		if questionDigests[questionDigest] {
			_, _ = fmt.Fprintln(stderr, "gold-feature question digest is duplicated")
			return 1
		}
		questionDigests[questionDigest] = true
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev gold-feature request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-extract-gold", "decision.distill.extract_gold", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev gold-feature extraction failed safely; paths, labels, and model details were withheld")
		return 1
	}
	type cacheResult struct {
		Role             string `json:"role"`
		ModelRole        string `json:"model_role"`
		QuestionSHA256   string `json:"question_sha256"`
		ModelSHA256      string `json:"model_sha256"`
		CheckpointSHA256 string `json:"model_checkpoint_sha256"`
		SourceSHA256     string `json:"source_sha256"`
		Examples         int    `json:"examples"`
		Classes          int    `json:"classes"`
		Block            int    `json:"block"`
		FeatureWidth     int    `json:"feature_width"`
		Split            string `json:"split"`
		RelativePath     string `json:"relative_path"`
		SHA256           string `json:"sha256"`
	}
	var result struct {
		SchemaVersion           string        `json:"schema_version"`
		ModelRole               string        `json:"model_role"`
		ModelSHA256             string        `json:"model_sha256"`
		ModelCheckpointSHA256   string        `json:"model_checkpoint_sha256"`
		QuestionCount           int           `json:"question_count"`
		TotalTrainExamples      int           `json:"total_train_examples"`
		TotalTestExamples       int           `json:"total_test_examples"`
		CacheCount              int           `json:"cache_count"`
		Caches                  []cacheResult `json:"caches"`
		HumanReviewRequired     bool          `json:"human_review_required"`
		ModelInferenceVerified  bool          `json:"model_inference_verified"`
		ExecutionVerified       bool          `json:"execution_verified"`
		CutoverEvidenceEligible bool          `json:"cutover_evidence_eligible"`
	}
	modelDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(model)))
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-extract-gold-result.v1" ||
		result.ModelRole != modelRole || result.ModelSHA256 != modelDigest ||
		result.ModelCheckpointSHA256 != checkpointSHA || result.QuestionCount != len(rawQuestions) ||
		result.CacheCount != len(result.Caches) || len(result.Caches) != 2*len(rawQuestions) ||
		result.TotalTrainExamples < 1 || result.TotalTrainExamples > 100000 ||
		result.TotalTestExamples < 1 || result.TotalTestExamples > 100000 ||
		!result.HumanReviewRequired || !result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev gold-feature result")
		return 1
	}
	seen := make(map[string]bool, len(result.Caches))
	totalTrain, totalTest := 0, 0
	totalFeatureElements := int64(0)
	for _, cache := range result.Caches {
		if !questionDigests[cache.QuestionSHA256] || cache.ModelRole != modelRole ||
			cache.ModelSHA256 != modelDigest || cache.CheckpointSHA256 != checkpointSHA ||
			!validLowerSHA256(cache.SourceSHA256) || !validLowerSHA256(cache.SHA256) ||
			cache.Examples < 1 || cache.Examples > 5000 || cache.Classes < 2 || cache.Classes > 26 ||
			cache.Block != block || cache.FeatureWidth < 1 || cache.FeatureWidth > 65536 ||
			(cache.Split != "train" && cache.Split != "test") {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev gold-feature receipt")
			return 1
		}
		key := cache.QuestionSHA256 + "/" + cache.Split
		wantRole := modelRole + "_gold_" + cache.Split + "_features"
		wantPath := "distillation/gold_features/" + checkpointSHA + "/" + cache.QuestionSHA256 + "." + cache.Split + ".npz"
		if cache.Role != wantRole || cache.RelativePath != wantPath || seen[key] {
			_, _ = fmt.Fprintln(stderr, "Python returned a duplicate or invalid AnyJev gold-feature path")
			return 1
		}
		seen[key] = true
		totalFeatureElements += int64(cache.Examples) * int64(cache.FeatureWidth)
		if totalFeatureElements > 50_000_000 {
			_, _ = fmt.Fprintln(stderr, "Python returned gold-feature receipts beyond the aggregate feature bound")
			return 1
		}
		if cache.Split == "train" {
			totalTrain += cache.Examples
		} else {
			totalTest += cache.Examples
		}
	}
	if totalTrain != result.TotalTrainExamples || totalTest != result.TotalTestExamples {
		_, _ = fmt.Fprintln(stderr, "Python returned inconsistent AnyJev gold-feature counts")
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev gold-feature output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationPipeline(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill pipeline", stderr)
	file := fs.String("file", "", "versioned local AnyJev end-to-end distillation request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill pipeline --file <distillation-pipeline-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 16<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 8 || len(fields) > 12 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev distillation pipeline request envelope")
		return 1
	}
	allowedFields := map[string]bool{
		"schema_version": true, "dataset_path": true, "staging_root": true, "cache_root": true,
		"workflows": true, "calibration_examples": true, "budgets": true, "bootstrap_resamples": true,
		"seed": true, "generator": true, "teacher": true, "students": true,
	}
	for key := range fields {
		if !allowedFields[key] {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline request contains unsupported fields")
			return 1
		}
	}
	var version, datasetPath, stagingRoot, cacheRoot string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-pipeline-request.v1" ||
		decodeJSONBytes(fields["dataset_path"], &datasetPath) != nil || !filepath.IsAbs(datasetPath) ||
		decodeJSONBytes(fields["staging_root"], &stagingRoot) != nil || !filepath.IsAbs(stagingRoot) ||
		decodeJSONBytes(fields["cache_root"], &cacheRoot) != nil || !filepath.IsAbs(cacheRoot) {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev distillation pipeline roots/version")
		return 1
	}
	workflows := []string{}
	if decodeJSONBytes(fields["workflows"], &workflows) != nil || len(workflows) < 1 || len(workflows) > 4 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline must select 1 to 4 workflows")
		return 1
	}
	allowedWorkflows := map[string]bool{
		"security_incidents": true, "invoice_processing": true,
		"customer_service": true, "agent_trace_observability": true,
	}
	seenWorkflows := map[string]bool{}
	for _, workflow := range workflows {
		if !allowedWorkflows[workflow] || seenWorkflows[workflow] {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline workflows are unsupported or duplicated")
			return 1
		}
		seenWorkflows[workflow] = true
	}
	decodeObject := func(raw json.RawMessage, minimum, maximum int) map[string]json.RawMessage {
		var object map[string]json.RawMessage
		if decodeJSONBytes(raw, &object) != nil || len(object) < minimum || len(object) > maximum {
			return nil
		}
		return object
	}
	validKeys := func(object map[string]json.RawMessage, allowed map[string]bool) bool {
		for key := range object {
			if !allowed[key] {
				return false
			}
		}
		return true
	}
	readIdentity := func(object map[string]json.RawMessage) (string, string, bool) {
		var model, checkpoint string
		if decodeJSONBytes(object["model"], &model) != nil || strings.TrimSpace(model) == "" || len(model) > 512 ||
			decodeJSONBytes(object["checkpoint_sha256"], &checkpoint) != nil || !validLowerSHA256(checkpoint) {
			return "", "", false
		}
		return model, checkpoint, true
	}
	readBackend := func(raw json.RawMessage) bool {
		backend := decodeObject(raw, 2, 5)
		if backend == nil || !validKeys(backend, map[string]bool{
			"type": true, "model_path": true, "device": true, "dtype": true, "batch_size": true,
		}) {
			return false
		}
		var kind, path string
		return decodeJSONBytes(backend["type"], &kind) == nil && kind == "hf" &&
			decodeJSONBytes(backend["model_path"], &path) == nil && filepath.IsAbs(path)
	}
	generator := decodeObject(fields["generator"], 3, 9)
	if generator == nil || !validKeys(generator, map[string]bool{
		"model": true, "checkpoint_sha256": true, "backend": true, "per_workflow": true,
		"batch_size": true, "max_new_tokens": true, "temperature": true, "top_p": true, "max_attempts": true,
	}) {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline generator request is invalid")
		return 1
	}
	generatorModel, generatorSHA, ok := readIdentity(generator)
	if !ok || !readBackend(generator["backend"]) {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline generator identity/backend is invalid")
		return 1
	}
	perWorkflow := 1200
	if raw, ok := generator["per_workflow"]; ok && decodeJSONBytes(raw, &perWorkflow) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline synthetic count is invalid")
		return 1
	}
	if perWorkflow < 1 || perWorkflow > 5000 || perWorkflow*len(workflows) > 100000 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline synthetic count exceeds its bounds")
		return 1
	}
	teacher := decodeObject(fields["teacher"], 6, 6)
	if teacher == nil || !validKeys(teacher, map[string]bool{
		"model": true, "checkpoint_sha256": true, "backend": true, "artifact": true, "block": true, "n_layers": true,
	}) {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline teacher request is invalid")
		return 1
	}
	teacherModel, teacherSHA, ok := readIdentity(teacher)
	var teacherBlock, teacherLayers int
	if !ok || !readBackend(teacher["backend"]) || decodeJSONBytes(teacher["block"], &teacherBlock) != nil ||
		decodeJSONBytes(teacher["n_layers"], &teacherLayers) != nil || teacherBlock < 1 || teacherLayers < teacherBlock || teacherLayers > 100000 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline teacher identity/backend/block is invalid")
		return 1
	}
	var teacherArtifact map[string]json.RawMessage
	if decodeJSONBytes(teacher["artifact"], &teacherArtifact) != nil || len(teacherArtifact) == 0 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline teacher artifact must be an object")
		return 1
	}
	studentRaw := []json.RawMessage{}
	if decodeJSONBytes(fields["students"], &studentRaw) != nil || len(studentRaw) < 1 || len(studentRaw) > 8 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline requires 1 to 8 students")
		return 1
	}
	type studentIdentity struct {
		Model, Checkpoint string
		Digest            string
		Block, Layers     int
	}
	type studentOutput struct {
		ModelSHA256           string `json:"model_sha256"`
		ModelCheckpointSHA256 string `json:"model_checkpoint_sha256"`
		Block                 int    `json:"block"`
		NLayers               int    `json:"n_layers"`
	}
	students := make([]studentIdentity, 0, len(studentRaw))
	seenStudents := map[string]bool{}
	teacherDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(teacherModel)))
	for _, raw := range studentRaw {
		student := decodeObject(raw, 5, 5)
		if student == nil || !validKeys(student, map[string]bool{
			"model": true, "checkpoint_sha256": true, "backend": true, "block": true, "n_layers": true,
		}) {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline student request is invalid")
			return 1
		}
		model, checkpoint, ok := readIdentity(student)
		identity := studentIdentity{Model: model, Checkpoint: checkpoint}
		if !ok || !readBackend(student["backend"]) || decodeJSONBytes(student["block"], &identity.Block) != nil ||
			decodeJSONBytes(student["n_layers"], &identity.Layers) != nil || identity.Block < 1 ||
			identity.Layers < identity.Block || identity.Layers > 100000 {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline student identity/backend/block is invalid")
			return 1
		}
		identity.Digest = fmt.Sprintf("%x", sha256.Sum256([]byte(identity.Model)))
		if identity.Digest == teacherDigest || seenStudents[identity.Digest] {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline student models must be unique and differ from the teacher")
			return 1
		}
		seenStudents[identity.Digest] = true
		students = append(students, identity)
	}
	calibrationExamples := 300
	if raw, ok := fields["calibration_examples"]; ok && decodeJSONBytes(raw, &calibrationExamples) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline calibration_examples is invalid")
		return 1
	}
	if calibrationExamples < 8 || calibrationExamples > 5000 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline calibration_examples is out of range")
		return 1
	}
	budgets := []int{300, 1200}
	if raw, ok := fields["budgets"]; ok && decodeJSONBytes(raw, &budgets) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline budgets are invalid")
		return 1
	}
	if len(budgets) < 1 || len(budgets) > 16 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline budgets are invalid")
		return 1
	}
	effectiveBudgets := map[int]bool{}
	for index, budget := range budgets {
		effective := budget
		if effective > perWorkflow {
			effective = perWorkflow
		}
		if budget < 1 || budget > 5000 || (index > 0 && budgets[index-1] >= budget) || effectiveBudgets[effective] {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline budgets are out of range or collapse at the synthetic count")
			return 1
		}
		effectiveBudgets[effective] = true
	}
	bootstrap := 1000
	if raw, ok := fields["bootstrap_resamples"]; ok && decodeJSONBytes(raw, &bootstrap) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline bootstrap count is invalid")
		return 1
	}
	if bootstrap < 100 || bootstrap > 10000 {
		_, _ = fmt.Fprintln(stderr, "distillation pipeline bootstrap count is out of range")
		return 1
	}
	if raw, ok := fields["seed"]; ok {
		var seed uint64
		if decodeJSONBytes(raw, &seed) != nil || seed > 1<<32-1 {
			_, _ = fmt.Fprintln(stderr, "distillation pipeline seed is invalid")
			return 1
		}
	}
	delete(fields, "schema_version")
	request, err := json.Marshal(fields)
	if err != nil || len(request) == 0 || len(request) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation pipeline request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-pipeline", "decision.distill.pipeline", request)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation pipeline failed safely; model paths and data were withheld")
		return 1
	}
	type exportStage struct {
		SchemaVersion string `json:"schema_version"`
		DatasetSHA256 string `json:"dataset_sha256"`
		Split         string `json:"split"`
		ExampleCount  int    `json:"example_count"`
	}
	type synthStage struct {
		SchemaVersion          string `json:"schema_version"`
		GeneratorSHA256        string `json:"generator_sha256"`
		GeneratorCheckpointSHA string `json:"generator_checkpoint_sha256"`
	}
	type labelStage struct {
		SchemaVersion        string `json:"schema_version"`
		TeacherSHA256        string `json:"teacher_sha256"`
		TeacherCheckpointSHA string `json:"teacher_checkpoint_sha256"`
		QuestionCount        int    `json:"question_count"`
	}
	type goldStage struct {
		SchemaVersion         string `json:"schema_version"`
		ModelRole             string `json:"model_role"`
		ModelSHA256           string `json:"model_sha256"`
		ModelCheckpointSHA256 string `json:"model_checkpoint_sha256"`
		QuestionCount         int    `json:"question_count"`
	}
	type syntheticFeatureStage struct {
		SchemaVersion           string `json:"schema_version"`
		StudentSHA256           string `json:"student_sha256"`
		StudentCheckpointSHA256 string `json:"student_checkpoint_sha256"`
		QuestionCount           int    `json:"question_count"`
	}
	type studentStage struct {
		ModelSHA256           string                `json:"model_sha256"`
		ModelCheckpointSHA256 string                `json:"model_checkpoint_sha256"`
		Block                 int                   `json:"block"`
		NLayers               int                   `json:"n_layers"`
		SyntheticFeatures     syntheticFeatureStage `json:"synthetic_features"`
		GoldFeatures          goldStage             `json:"gold_features"`
	}
	var result struct {
		SchemaVersion                string          `json:"schema_version"`
		DatasetSHA256                string          `json:"dataset_sha256"`
		WorkflowCount                int             `json:"workflow_count"`
		QuestionCount                int             `json:"question_count"`
		StudentCount                 int             `json:"student_count"`
		TrainExamplesPerQuestion     int             `json:"train_examples_per_question"`
		TestExamples                 int             `json:"test_examples"`
		SyntheticExamplesPerWorkflow int             `json:"synthetic_examples_per_workflow"`
		GeneratorSHA256              string          `json:"generator_sha256"`
		GeneratorCheckpointSHA256    string          `json:"generator_checkpoint_sha256"`
		TeacherSHA256                string          `json:"teacher_sha256"`
		TeacherCheckpointSHA256      string          `json:"teacher_checkpoint_sha256"`
		Students                     []studentOutput `json:"students"`
		Stages                       struct {
			ExportTrain     exportStage    `json:"export_train"`
			ExportTest      exportStage    `json:"export_test"`
			Synth           synthStage     `json:"synth"`
			TeacherLabel    labelStage     `json:"teacher_label"`
			TeacherGold     goldStage      `json:"teacher_gold_features"`
			StudentFeatures []studentStage `json:"student_features"`
			Fit             struct {
				SchemaVersion     string `json:"schema_version"`
				QuestionCount     int    `json:"question_count"`
				TotalTestExamples int    `json:"total_test_examples"`
			} `json:"fit"`
			Soft struct {
				SchemaVersion     string `json:"schema_version"`
				QuestionCount     int    `json:"question_count"`
				TotalTestExamples int    `json:"total_test_examples"`
			} `json:"soft"`
		} `json:"stages"`
		HumanReviewRequired     bool `json:"human_review_required"`
		ModelInferenceVerified  bool `json:"model_inference_verified"`
		ExecutionVerified       bool `json:"execution_verified"`
		CutoverEvidenceEligible bool `json:"cutover_evidence_eligible"`
	}
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-pipeline-result.v1" ||
		!validLowerSHA256(result.DatasetSHA256) || result.WorkflowCount != len(workflows) ||
		result.QuestionCount < 1 || result.QuestionCount > 128 || result.StudentCount != len(students) ||
		result.TrainExamplesPerQuestion != calibrationExamples || result.TestExamples < 2 ||
		result.SyntheticExamplesPerWorkflow != perWorkflow || result.GeneratorSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(generatorModel))) ||
		result.GeneratorCheckpointSHA256 != generatorSHA || result.TeacherSHA256 != teacherDigest ||
		result.TeacherCheckpointSHA256 != teacherSHA || len(result.Students) != len(students) ||
		!result.HumanReviewRequired || !result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev distillation pipeline result")
		return 1
	}
	if result.Stages.ExportTrain.SchemaVersion != "threadforge.anyjev-distillation-export-cases-result.v1" ||
		result.Stages.ExportTest.SchemaVersion != "threadforge.anyjev-distillation-export-cases-result.v1" ||
		result.Stages.ExportTrain.DatasetSHA256 != result.DatasetSHA256 || result.Stages.ExportTest.DatasetSHA256 != result.DatasetSHA256 ||
		result.Stages.ExportTrain.Split != "train" || result.Stages.ExportTest.Split != "test" ||
		result.Stages.Synth.SchemaVersion != "threadforge.anyjev-distillation-synth-result.v1" ||
		result.Stages.Synth.GeneratorSHA256 != result.GeneratorSHA256 || result.Stages.Synth.GeneratorCheckpointSHA != generatorSHA ||
		result.Stages.TeacherLabel.SchemaVersion != "threadforge.anyjev-distillation-label-result.v1" ||
		result.Stages.TeacherLabel.TeacherSHA256 != teacherDigest || result.Stages.TeacherLabel.TeacherCheckpointSHA != teacherSHA ||
		result.Stages.TeacherLabel.QuestionCount != result.QuestionCount ||
		result.Stages.TeacherGold.SchemaVersion != "threadforge.anyjev-distillation-extract-gold-result.v1" ||
		result.Stages.TeacherGold.ModelRole != "teacher" || result.Stages.TeacherGold.ModelSHA256 != teacherDigest ||
		result.Stages.TeacherGold.ModelCheckpointSHA256 != teacherSHA || result.Stages.TeacherGold.QuestionCount != result.QuestionCount ||
		result.Stages.Fit.SchemaVersion != "threadforge.anyjev-distillation-fit-result.v1" ||
		result.Stages.Fit.QuestionCount != result.QuestionCount || result.Stages.Fit.TotalTestExamples != result.TestExamples ||
		result.Stages.Soft.SchemaVersion != "threadforge.anyjev-distillation-soft-result.v1" ||
		result.Stages.Soft.QuestionCount != result.QuestionCount || result.Stages.Soft.TotalTestExamples != result.TestExamples ||
		len(result.Stages.StudentFeatures) != len(students) {
		_, _ = fmt.Fprintln(stderr, "Python pipeline stage receipts are inconsistent")
		return 1
	}
	expectedStudents := make(map[string]studentIdentity, len(students))
	for _, student := range students {
		expectedStudents[student.Digest] = student
	}
	seenStudentResults := make(map[string]bool, len(students))
	for _, student := range result.Students {
		expected, ok := expectedStudents[student.ModelSHA256]
		if !ok || seenStudentResults[student.ModelSHA256] || student.ModelCheckpointSHA256 != expected.Checkpoint ||
			student.Block != expected.Block || student.NLayers != expected.Layers {
			_, _ = fmt.Fprintln(stderr, "Python pipeline student identities do not match the request")
			return 1
		}
		seenStudentResults[student.ModelSHA256] = true
	}
	for _, stage := range result.Stages.StudentFeatures {
		expected, ok := expectedStudents[stage.ModelSHA256]
		if !ok || stage.ModelCheckpointSHA256 != expected.Checkpoint || stage.Block != expected.Block || stage.NLayers != expected.Layers ||
			stage.SyntheticFeatures.SchemaVersion != "threadforge.anyjev-distillation-extract-result.v1" ||
			stage.SyntheticFeatures.StudentSHA256 != stage.ModelSHA256 ||
			stage.SyntheticFeatures.StudentCheckpointSHA256 != expected.Checkpoint ||
			stage.SyntheticFeatures.QuestionCount != result.QuestionCount ||
			stage.GoldFeatures.SchemaVersion != "threadforge.anyjev-distillation-extract-gold-result.v1" ||
			stage.GoldFeatures.ModelRole != "student" || stage.GoldFeatures.ModelSHA256 != stage.ModelSHA256 ||
			stage.GoldFeatures.ModelCheckpointSHA256 != expected.Checkpoint || stage.GoldFeatures.QuestionCount != result.QuestionCount {
			_, _ = fmt.Fprintln(stderr, "Python pipeline student stage receipts are inconsistent")
			return 1
		}
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev distillation pipeline output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationSynth(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill synth", stderr)
	file := fs.String("file", "", "versioned local AnyJev synthetic-state generation request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill synth --file <distillation-synth-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 16<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "distillation synth request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 7 || len(fields) > 14 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev distillation synth request envelope")
		return 1
	}
	allowed := map[string]bool{
		"schema_version": true, "source_root": true, "cache_root": true, "generator_model": true,
		"generator_checkpoint_sha256": true, "backend": true, "workflows": true, "per_workflow": true,
		"batch_size": true, "max_new_tokens": true, "temperature": true, "top_p": true, "seed": true,
		"max_attempts": true,
	}
	for key := range fields {
		if !allowed[key] {
			_, _ = fmt.Fprintln(stderr, "distillation synth request contains unsupported fields")
			return 1
		}
	}
	var version, generatorModel, checkpointSHA string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-synth-request.v1" ||
		decodeJSONBytes(fields["generator_model"], &generatorModel) != nil || strings.TrimSpace(generatorModel) == "" ||
		decodeJSONBytes(fields["generator_checkpoint_sha256"], &checkpointSHA) != nil || !validLowerSHA256(checkpointSHA) {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev distillation synth request")
		return 1
	}
	var workflows []struct {
		Workflow      string `json:"workflow"`
		ExemplarsPath string `json:"exemplars_path"`
	}
	if decodeJSONBytes(fields["workflows"], &workflows) != nil || len(workflows) < 1 || len(workflows) > 256 {
		_, _ = fmt.Fprintln(stderr, "distillation synth request must contain 1 to 256 workflows")
		return 1
	}
	perWorkflow := 1200
	if raw, ok := fields["per_workflow"]; ok && decodeJSONBytes(raw, &perWorkflow) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation synth per_workflow is invalid")
		return 1
	}
	maxAttempts := min(100000, perWorkflow*10)
	if raw, ok := fields["max_attempts"]; ok && decodeJSONBytes(raw, &maxAttempts) != nil {
		_, _ = fmt.Fprintln(stderr, "distillation synth max_attempts is invalid")
		return 1
	}
	if perWorkflow < 1 || perWorkflow > 5000 || perWorkflow*len(workflows) > 100000 ||
		maxAttempts < perWorkflow || maxAttempts > 100000 || maxAttempts*len(workflows) > 100000 {
		_, _ = fmt.Fprintln(stderr, "distillation synth request exceeds bounded generation limits")
		return 1
	}
	wantWorkflows := make(map[string]bool, len(workflows))
	for _, workflow := range workflows {
		if strings.TrimSpace(workflow.Workflow) == "" {
			_, _ = fmt.Fprintln(stderr, "distillation synth request contains an invalid workflow")
			return 1
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(workflow.Workflow)))
		if wantWorkflows[digest] {
			_, _ = fmt.Fprintln(stderr, "distillation synth request contains a duplicate workflow")
			return 1
		}
		wantWorkflows[digest] = true
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev distillation synth request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-synth", "decision.distill.synth", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev synthetic-state generation failed safely; source paths and model details were withheld")
		return 1
	}
	type outputReceipt struct {
		WorkflowSHA256 string `json:"workflow_sha256"`
		SourceSHA256   string `json:"source_sha256"`
		Examples       int    `json:"examples"`
		Generated      int    `json:"generated"`
		Rejected       int    `json:"rejected"`
		RelativePath   string `json:"relative_path"`
		SHA256         string `json:"sha256"`
	}
	var result struct {
		SchemaVersion             string          `json:"schema_version"`
		GeneratorSHA256           string          `json:"generator_sha256"`
		GeneratorCheckpointSHA256 string          `json:"generator_checkpoint_sha256"`
		WorkflowCount             int             `json:"workflow_count"`
		TotalSyntheticExamples    int             `json:"total_synthetic_examples"`
		TotalGenerated            int             `json:"total_generated"`
		TotalRejected             int             `json:"total_rejected"`
		Outputs                   []outputReceipt `json:"outputs"`
		HumanReviewRequired       bool            `json:"human_review_required"`
		ModelInferenceVerified    bool            `json:"model_inference_verified"`
		ExecutionVerified         bool            `json:"execution_verified"`
		CutoverEvidenceEligible   bool            `json:"cutover_evidence_eligible"`
	}
	generatorDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(generatorModel)))
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-synth-result.v1" ||
		result.GeneratorSHA256 != generatorDigest || result.GeneratorCheckpointSHA256 != checkpointSHA ||
		result.WorkflowCount != len(workflows) || len(result.Outputs) != len(workflows) ||
		result.TotalSyntheticExamples != perWorkflow*len(workflows) ||
		result.TotalGenerated < result.TotalSyntheticExamples || result.TotalGenerated > maxAttempts*len(workflows) ||
		result.TotalRejected != result.TotalGenerated-result.TotalSyntheticExamples ||
		!result.HumanReviewRequired || !result.ModelInferenceVerified || result.ExecutionVerified || result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev distillation-synth result")
		return 1
	}
	seen := make(map[string]bool, len(result.Outputs))
	totalGenerated, totalRejected := 0, 0
	for _, output := range result.Outputs {
		if !wantWorkflows[output.WorkflowSHA256] || seen[output.WorkflowSHA256] ||
			!validLowerSHA256(output.SourceSHA256) || !validLowerSHA256(output.SHA256) ||
			output.Examples != perWorkflow || output.Generated < output.Examples || output.Generated > maxAttempts ||
			output.Rejected != output.Generated-output.Examples ||
			output.RelativePath != "distillation/synth/"+checkpointSHA+"/"+output.WorkflowSHA256+".json" {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev synthetic-state receipt")
			return 1
		}
		seen[output.WorkflowSHA256] = true
		totalGenerated += output.Generated
		totalRejected += output.Rejected
	}
	if totalGenerated != result.TotalGenerated || totalRejected != result.TotalRejected {
		_, _ = fmt.Fprintln(stderr, "Python returned inconsistent AnyJev synthetic-state counts")
		return 1
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed AnyJev distillation-synth output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func runAnyJevDistillationExportCases(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision distill export-cases", stderr)
	file := fs.String("file", "", "versioned local AnyJev typed-case export request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision distill export-cases --file <export-cases-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 1<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "typed-case export request contains credential material")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) < 5 || len(fields) > 6 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev typed-case export request envelope")
		return 1
	}
	allowed := map[string]bool{
		"schema_version": true, "dataset_path": true, "output_root": true, "split": true,
		"workflows": true, "max_cases_per_workflow": true,
	}
	for key := range fields {
		if !allowed[key] {
			_, _ = fmt.Fprintln(stderr, "typed-case export request contains unsupported fields")
			return 1
		}
	}
	var version, datasetPath, outputRoot, split string
	if decodeJSONBytes(fields["schema_version"], &version) != nil ||
		version != "threadforge.anyjev-distillation-export-cases-request.v1" ||
		decodeJSONBytes(fields["dataset_path"], &datasetPath) != nil || strings.TrimSpace(datasetPath) == "" ||
		decodeJSONBytes(fields["output_root"], &outputRoot) != nil || strings.TrimSpace(outputRoot) == "" ||
		decodeJSONBytes(fields["split"], &split) != nil || (split != "train" && split != "test") {
		_, _ = fmt.Fprintln(stderr, "unsupported or incomplete AnyJev typed-case export request")
		return 1
	}
	var workflows []string
	if decodeJSONBytes(fields["workflows"], &workflows) != nil || len(workflows) < 1 || len(workflows) > 4 {
		_, _ = fmt.Fprintln(stderr, "typed-case export request must select 1 to 4 workflows")
		return 1
	}
	allowedWorkflows := map[string]bool{
		"security_incidents": true, "invoice_processing": true,
		"customer_service": true, "agent_trace_observability": true,
	}
	wantWorkflows := make(map[string]bool, len(workflows))
	for _, workflow := range workflows {
		if !allowedWorkflows[workflow] {
			_, _ = fmt.Fprintln(stderr, "typed-case export request contains an unsupported workflow")
			return 1
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(workflow)))
		if wantWorkflows[digest] {
			_, _ = fmt.Fprintln(stderr, "typed-case export request contains a duplicate workflow")
			return 1
		}
		wantWorkflows[digest] = true
	}
	caseLimit := 5000
	if raw, ok := fields["max_cases_per_workflow"]; ok && decodeJSONBytes(raw, &caseLimit) != nil {
		_, _ = fmt.Fprintln(stderr, "typed-case export max_cases_per_workflow is invalid")
		return 1
	}
	if caseLimit < 1 || caseLimit > 5000 {
		_, _ = fmt.Fprintln(stderr, "typed-case export case limit exceeds the selected split's bounds")
		return 1
	}
	delete(fields, "schema_version")
	params, err := json.Marshal(fields)
	if err != nil || len(params) == 0 || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "AnyJev typed-case export request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-distill-export-cases", "decision.distill.export_cases", params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev local typed-case export failed safely; dataset paths and row contents were withheld")
		return 1
	}
	type outputReceipt struct {
		Role           string `json:"role"`
		WorkflowSHA256 string `json:"workflow_sha256"`
		QuestionSHA256 string `json:"question_sha256"`
		Examples       int    `json:"examples"`
		RelativePath   string `json:"relative_path"`
		SHA256         string `json:"sha256"`
	}
	var result struct {
		SchemaVersion           string          `json:"schema_version"`
		DatasetSHA256           string          `json:"dataset_sha256"`
		Split                   string          `json:"split"`
		WorkflowCount           int             `json:"workflow_count"`
		CaseCount               int             `json:"case_count"`
		ExampleCount            int             `json:"example_count"`
		OutputCount             int             `json:"output_count"`
		Outputs                 []outputReceipt `json:"outputs"`
		HumanReviewRequired     bool            `json:"human_review_required"`
		ModelInferenceVerified  bool            `json:"model_inference_verified"`
		ExecutionVerified       bool            `json:"execution_verified"`
		CutoverEvidenceEligible bool            `json:"cutover_evidence_eligible"`
	}
	if decodeJSONBytes(response.Result, &result) != nil || response.Status != "ok" ||
		result.SchemaVersion != "threadforge.anyjev-distillation-export-cases-result.v1" ||
		!validLowerSHA256(result.DatasetSHA256) || result.Split != split ||
		result.WorkflowCount != len(workflows) || result.CaseCount < len(workflows) ||
		result.CaseCount > caseLimit*len(workflows) || result.OutputCount != len(result.Outputs) ||
		len(result.Outputs) < len(workflows) || len(result.Outputs) > 256 ||
		result.ExampleCount < 1 || result.ExampleCount > 100000 ||
		!result.HumanReviewRequired || result.ModelInferenceVerified || result.ExecutionVerified ||
		result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev typed-case export result")
		return 1
	}
	seen := make(map[string]bool, len(result.Outputs))
	workflowOutputs := make(map[string]bool, len(workflows))
	workflowExemplars := make(map[string]int, len(workflows))
	workflowTrainCases := make(map[string]int, len(workflows))
	workflowTestCases := make(map[string]int, len(workflows))
	totalExamples := 0
	for _, output := range result.Outputs {
		if !validLowerSHA256(output.WorkflowSHA256) || !wantWorkflows[output.WorkflowSHA256] ||
			!validLowerSHA256(output.SHA256) || output.Examples < 1 || output.Examples > 5000 {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid typed-case export receipt")
			return 1
		}
		expectedPath := "distillation/source/" + result.DatasetSHA256 + "/" + split + "/"
		switch {
		case split == "train" && output.Role == "exemplars" && output.QuestionSHA256 == "":
			expectedPath += "exemplars/" + output.WorkflowSHA256 + ".json"
			workflowExemplars[output.WorkflowSHA256]++
		case split == "train" && output.Role == "train_cases" && validLowerSHA256(output.QuestionSHA256):
			expectedPath += "train-cases/" + output.WorkflowSHA256 + "/" + output.QuestionSHA256 + ".json"
			workflowTrainCases[output.WorkflowSHA256]++
		case split == "test" && output.Role == "test_cases" && validLowerSHA256(output.QuestionSHA256):
			expectedPath += "test-cases/" + output.WorkflowSHA256 + "/" + output.QuestionSHA256 + ".json"
			workflowTestCases[output.WorkflowSHA256]++
		default:
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid typed-case export role")
			return 1
		}
		if output.RelativePath != expectedPath || seen[output.RelativePath] {
			_, _ = fmt.Fprintln(stderr, "Python returned a duplicate or invalid typed-case export path")
			return 1
		}
		seen[output.RelativePath] = true
		workflowOutputs[output.WorkflowSHA256] = true
		totalExamples += output.Examples
	}
	if len(workflowOutputs) != len(workflows) || totalExamples != result.ExampleCount {
		_, _ = fmt.Fprintln(stderr, "Python returned inconsistent typed-case export counts")
		return 1
	}
	for workflowSHA := range wantWorkflows {
		if split == "train" && (workflowExemplars[workflowSHA] != 1 || workflowTrainCases[workflowSHA] < 1) {
			_, _ = fmt.Fprintln(stderr, "Python omitted required train exemplars or typed cases")
			return 1
		}
		if split == "test" && workflowTestCases[workflowSHA] < 1 {
			_, _ = fmt.Fprintln(stderr, "Python omitted held-out typed cases for a requested workflow")
			return 1
		}
	}
	var output any
	if decodeJSONBytes(response.Result, &output) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned malformed typed-case export output")
		return 1
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func fingerprintDecisionModel(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision model fingerprint", stderr)
	path := fs.String("path", "", "materialized local model checkpoint directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision model fingerprint --path <local-model-directory>")
		return 2
	}
	fingerprint, err := decision.FingerprintModelDirectory(*path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not fingerprint the local model checkpoint")
		return 1
	}
	return writeIndentedJSON(stdout, fingerprint, stderr)
}

func truncateDecisionModel(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision model truncate", stderr)
	modelPath := fs.String("model-path", "", "materialized local checkpoint directory")
	tokenizerPath := fs.String("tokenizer-path", "", "optional separate local tokenizer directory")
	blocks := fs.Int("blocks", 0, "number of initial transformer blocks to keep")
	outputPath := fs.String("output-path", "", "new output directory; existing paths are never overwritten")
	dtype := fs.String("dtype", "auto", "auto, float16, bfloat16, or float32")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *modelPath == "" || *outputPath == "" || fs.NArg() != 0 || *blocks < 1 || *blocks > 4096 ||
		!filepath.IsAbs(*modelPath) || !filepath.IsAbs(*outputPath) ||
		(*tokenizerPath != "" && !filepath.IsAbs(*tokenizerPath)) ||
		(*dtype != "auto" && *dtype != "float16" && *dtype != "bfloat16" && *dtype != "float32") {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision model truncate --model-path <local-dir> --blocks <1-4096> --output-path <new-local-dir> [--tokenizer-path <local-dir>] [--dtype auto|float16|bfloat16|float32] [--python python]")
		return 2
	}
	if _, err := os.Lstat(*outputPath); err == nil {
		_, _ = fmt.Fprintln(stderr, "output path already exists; truncation never overwrites local data")
		return 1
	} else if !errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(stderr, "output path cannot be inspected safely")
		return 1
	}
	params := map[string]any{
		"schema_version": "threadforge.anyjev-truncate-request.v1",
		"model_path":     *modelPath, "blocks": *blocks, "output_path": *outputPath, "dtype": *dtype,
	}
	if *tokenizerPath != "" {
		params["tokenizer_path"] = *tokenizerPath
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil || len(paramsJSON) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "checkpoint truncation request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-model-truncate", "decision.model.truncate", json.RawMessage(paramsJSON))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local checkpoint truncation failed safely; paths and model details were withheld")
		return 1
	}
	var result struct {
		SchemaVersion           string `json:"schema_version"`
		SourceCheckpointSHA256  string `json:"source_checkpoint_sha256"`
		TokenizerSourceSHA256   string `json:"tokenizer_source_sha256"`
		OriginalBlocks          int    `json:"original_blocks"`
		KeptBlocks              int    `json:"kept_blocks"`
		KeptTensorCount         int    `json:"kept_tensor_count"`
		DroppedTensorCount      int    `json:"dropped_tensor_count"`
		DType                   string `json:"dtype"`
		HumanReviewRequired     bool   `json:"human_review_required"`
		ExecutionVerified       bool   `json:"execution_verified"`
		CutoverEvidenceEligible bool   `json:"cutover_evidence_eligible"`
		OutputSHA256            string `json:"output_sha256"`
		OutputFileCount         int    `json:"output_file_count"`
		OutputTotalBytes        int64  `json:"output_total_bytes"`
		OutputReady             bool   `json:"output_ready"`
	}
	if response.Status != "ok" || decodeJSONBytes(response.Result, &result) != nil ||
		result.SchemaVersion != "threadforge.anyjev-truncated-checkpoint.v1" ||
		!validLowerSHA256(result.SourceCheckpointSHA256) || !validLowerSHA256(result.TokenizerSourceSHA256) ||
		!validLowerSHA256(result.OutputSHA256) || result.OriginalBlocks < *blocks || result.KeptBlocks != *blocks ||
		result.KeptTensorCount < 1 || result.DroppedTensorCount < 0 || result.OutputFileCount < 1 ||
		result.OutputTotalBytes < 1 || !result.OutputReady || !result.HumanReviewRequired ||
		result.ExecutionVerified || result.CutoverEvidenceEligible || result.DType != *dtype {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe checkpoint truncation receipt")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runAnyJevServingPipeline(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision benchmark pipeline", stderr)
	file := fs.String("file", "", "versioned local AnyJev serving-pipeline request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision benchmark pipeline --file <serving-pipeline-request.json> [--python python]")
		return 2
	}
	data, err := readBoundedText(*file, 4<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "serving pipeline request contains credential material; no model was loaded")
		return 1
	}
	var fields map[string]json.RawMessage
	if decodeJSONBytes([]byte(data), &fields) != nil || len(fields) < 4 || len(fields) > 13 {
		_, _ = fmt.Fprintln(stderr, "invalid AnyJev serving-pipeline request envelope")
		return 1
	}
	allowed := map[string]bool{
		"schema_version": true, "model_path": true, "case_root": true, "questions": true,
		"depths": true, "only_full": true, "repeats": true, "port": true,
		"gpu_memory_utilization": true, "prefix_caching": true, "quantization": true,
		"multi_question_states": true, "startup_timeout_seconds": true,
	}
	for key := range fields {
		if !allowed[key] {
			_, _ = fmt.Fprintln(stderr, "serving pipeline request contains unsupported fields")
			return 1
		}
	}
	var schemaVersion, modelPath, caseRoot string
	if decodeJSONBytes(fields["schema_version"], &schemaVersion) != nil ||
		schemaVersion != "threadforge.anyjev-serving-pipeline-request.v1" ||
		decodeJSONBytes(fields["model_path"], &modelPath) != nil || !filepath.IsAbs(modelPath) ||
		decodeJSONBytes(fields["case_root"], &caseRoot) != nil || !filepath.IsAbs(caseRoot) {
		_, _ = fmt.Fprintln(stderr, "serving pipeline requires the supported schema and absolute local roots")
		return 1
	}
	var questions []struct {
		QuestionID     string `json:"question_id"`
		TrainCasesPath string `json:"train_cases_path"`
		TestCasesPath  string `json:"test_cases_path"`
	}
	if decodeJSONBytes(fields["questions"], &questions) != nil || len(questions) < 1 || len(questions) > 32 {
		_, _ = fmt.Fprintln(stderr, "serving pipeline requires 1 to 32 typed questions")
		return 1
	}
	requestDepths := []int{}
	if raw, ok := fields["depths"]; ok && decodeJSONBytes(raw, &requestDepths) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline depths are malformed")
		return 1
	}
	if len(requestDepths) > 32 {
		_, _ = fmt.Fprintln(stderr, "serving pipeline depth count exceeds its limit")
		return 1
	}
	onlyFull := false
	if raw, ok := fields["only_full"]; ok && decodeJSONBytes(raw, &onlyFull) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline only_full must be a boolean")
		return 1
	}
	if onlyFull && len(requestDepths) > 0 {
		_, _ = fmt.Fprintln(stderr, "serving pipeline cannot combine only_full with explicit depths")
		return 1
	}
	repeats, port, multiStates, startupTimeout := 3, 8100, 64, 900
	gpuFraction := 0.85
	prefixCaching := false
	if raw, ok := fields["repeats"]; ok && decodeJSONBytes(raw, &repeats) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline repeats is invalid")
		return 1
	}
	if raw, ok := fields["port"]; ok && decodeJSONBytes(raw, &port) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline port is invalid")
		return 1
	}
	if raw, ok := fields["multi_question_states"]; ok && decodeJSONBytes(raw, &multiStates) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline multi_question_states is invalid")
		return 1
	}
	if raw, ok := fields["startup_timeout_seconds"]; ok && decodeJSONBytes(raw, &startupTimeout) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline startup timeout is invalid")
		return 1
	}
	if raw, ok := fields["gpu_memory_utilization"]; ok && decodeJSONBytes(raw, &gpuFraction) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline GPU memory fraction is invalid")
		return 1
	}
	if raw, ok := fields["prefix_caching"]; ok && decodeJSONBytes(raw, &prefixCaching) != nil {
		_, _ = fmt.Fprintln(stderr, "serving pipeline prefix_caching is invalid")
		return 1
	}
	var quantization *string
	if raw, ok := fields["quantization"]; ok && string(raw) != "null" {
		var value string
		if decodeJSONBytes(raw, &value) != nil || len(value) < 1 || len(value) > 32 {
			_, _ = fmt.Fprintln(stderr, "serving pipeline quantization is invalid")
			return 1
		}
		quantization = &value
	}
	if repeats < 2 || repeats > 10 || port < 0 || port > 65535 || multiStates < 1 || multiStates > 1000 ||
		startupTimeout < 10 || startupTimeout > 900 || math.IsNaN(gpuFraction) || math.IsInf(gpuFraction, 0) ||
		gpuFraction <= 0.05 || gpuFraction > 0.95 {
		_, _ = fmt.Fprintln(stderr, "serving pipeline parameters are outside their supported ranges")
		return 1
	}
	for _, question := range questions {
		if strings.TrimSpace(question.QuestionID) == "" || len(question.QuestionID) > 512 ||
			!safeServingCasePath(question.TrainCasesPath) || !safeServingCasePath(question.TestCasesPath) ||
			question.TrainCasesPath == question.TestCasesPath {
			_, _ = fmt.Fprintln(stderr, "serving pipeline question descriptors are invalid")
			return 1
		}
	}
	modelFingerprint, err := decision.FingerprintModelDirectory(modelPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local model checkpoint could not be fingerprinted safely")
		return 1
	}
	paramsJSON, err := json.Marshal(fields)
	if err != nil || len(paramsJSON) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "serving pipeline request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-serving-pipeline", "decision.serve.pipeline", paramsJSON)
	if err != nil || response.Status != "ok" {
		_, _ = fmt.Fprintln(stderr, "local AnyJev serving pipeline failed safely; model paths and case content were withheld")
		return 1
	}
	type questionRow struct {
		QuestionIDSHA256 string  `json:"question_id_sha256"`
		QuestionSHA256   string  `json:"question_sha256"`
		TrainExamples    int     `json:"train_examples"`
		TestExamples     int     `json:"test_examples"`
		Accuracy         float64 `json:"accuracy"`
		ECE              float64 `json:"ece"`
		FitMS            float64 `json:"fit_ms"`
		SingleMS         float64 `json:"single_ms"`
		SingleSpread     float64 `json:"single_spread"`
		HeadMethod       string  `json:"head_method"`
	}
	type servingRow struct {
		Blocks        int           `json:"blocks"`
		TotalBlocks   int           `json:"total_blocks"`
		CheckpointSHA string        `json:"checkpoint_sha256"`
		Questions     []questionRow `json:"questions"`
		MultiQuestion struct {
			QuestionCount       int     `json:"question_count"`
			SampledStates       int     `json:"sampled_states"`
			MedianMSPerDecision float64 `json:"median_ms_per_decision"`
			Spread              float64 `json:"spread"`
		} `json:"multi_question"`
	}
	var result struct {
		SchemaVersion     string `json:"schema_version"`
		SourceModelSHA256 string `json:"source_model_sha256"`
		CaseSetSHA256     string `json:"case_set_sha256"`
		TotalBlocks       int    `json:"total_blocks"`
		Parameters        struct {
			Repeats              int     `json:"repeats"`
			PrefixCaching        bool    `json:"prefix_caching"`
			GPUMemoryUtilization float64 `json:"gpu_memory_utilization"`
			MultiQuestionStates  int     `json:"multi_question_states"`
			Port                 int     `json:"port"`
			StartupTimeout       int     `json:"startup_timeout_seconds"`
			Quantization         *string `json:"quantization"`
		} `json:"parameters"`
		Rows                       []servingRow `json:"rows"`
		ModelInferenceVerified     bool         `json:"model_inference_verified"`
		RemoteInferenceUsed        bool         `json:"remote_inference_used"`
		IndependentHoldoutVerified bool         `json:"independent_holdout_verified"`
		ExecutionVerified          bool         `json:"execution_verified"`
		HumanReviewRequired        bool         `json:"human_review_required"`
		CutoverEvidenceEligible    bool         `json:"cutover_evidence_eligible"`
	}
	decoder := json.NewDecoder(bytes.NewReader(response.Result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned a malformed AnyJev serving-pipeline result")
		return 1
	}
	if result.SchemaVersion != "threadforge.anyjev-serving-pipeline-result.v1" ||
		!validLowerSHA256(result.SourceModelSHA256) || result.SourceModelSHA256 != modelFingerprint.SHA256 ||
		!validLowerSHA256(result.CaseSetSHA256) || result.TotalBlocks < 1 || result.TotalBlocks > 4096 ||
		len(result.Rows) < 1 || len(result.Rows) > 32 || result.Parameters.Repeats != repeats ||
		result.Parameters.PrefixCaching != prefixCaching || result.Parameters.GPUMemoryUtilization != gpuFraction ||
		result.Parameters.MultiQuestionStates != multiStates || result.Parameters.Port != port ||
		result.Parameters.StartupTimeout != startupTimeout || !sameOptionalString(result.Parameters.Quantization, quantization) ||
		!result.ModelInferenceVerified || result.RemoteInferenceUsed || result.IndependentHoldoutVerified ||
		result.ExecutionVerified || !result.HumanReviewRequired || result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe AnyJev serving-pipeline result")
		return 1
	}
	expectedDepths := requestDepths
	if len(expectedDepths) == 0 {
		if onlyFull {
			expectedDepths = []int{result.TotalBlocks}
		} else {
			expectedDepths = uniqueDepths(result.TotalBlocks, maxInt(1, result.TotalBlocks*2/3))
		}
	}
	if len(result.Rows) != len(expectedDepths) {
		_, _ = fmt.Fprintln(stderr, "Python returned an inconsistent AnyJev serving depth count")
		return 1
	}
	expectedQuestionIDs := make(map[string]bool, len(questions))
	for _, question := range questions {
		expectedQuestionIDs[fmt.Sprintf("%x", sha256.Sum256([]byte(question.QuestionID)))] = true
	}
	var baselineQuestions map[string]questionRow
	for i, row := range result.Rows {
		if row.Blocks != expectedDepths[i] || row.TotalBlocks != result.TotalBlocks || row.Blocks < 1 ||
			row.Blocks > row.TotalBlocks || !validLowerSHA256(row.CheckpointSHA) || len(row.Questions) != len(questions) ||
			row.MultiQuestion.QuestionCount != len(questions) || row.MultiQuestion.SampledStates < 1 ||
			row.MultiQuestion.SampledStates > minInt(multiStates, row.Questions[0].TestExamples) ||
			!validPositiveFinite(row.MultiQuestion.MedianMSPerDecision) || !validNonNegativeFinite(row.MultiQuestion.Spread) {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev serving depth receipt")
			return 1
		}
		questionIDs := make(map[string]bool, len(questions))
		rowQuestions := make(map[string]questionRow, len(row.Questions))
		for _, rowQuestion := range row.Questions {
			if !validLowerSHA256(rowQuestion.QuestionIDSHA256) || !validLowerSHA256(rowQuestion.QuestionSHA256) ||
				!expectedQuestionIDs[rowQuestion.QuestionIDSHA256] ||
				rowQuestion.TrainExamples < 8 || rowQuestion.TrainExamples > 5000 ||
				rowQuestion.TestExamples < 2 || rowQuestion.TestExamples > 5000 ||
				!validProbability(rowQuestion.Accuracy) || !validProbability(rowQuestion.ECE) ||
				!validNonNegativeFinite(rowQuestion.FitMS) || !validPositiveFinite(rowQuestion.SingleMS) ||
				!validNonNegativeFinite(rowQuestion.SingleSpread) ||
				(rowQuestion.HeadMethod != "head:lda" && rowQuestion.HeadMethod != "head:ridge") ||
				questionIDs[rowQuestion.QuestionIDSHA256] {
				_, _ = fmt.Fprintln(stderr, "Python returned an invalid AnyJev serving question result")
				return 1
			}
			questionIDs[rowQuestion.QuestionIDSHA256] = true
			rowQuestions[rowQuestion.QuestionIDSHA256] = rowQuestion
		}
		if i == 0 {
			baselineQuestions = rowQuestions
		} else {
			for id, baseline := range baselineQuestions {
				current, ok := rowQuestions[id]
				if !ok || current.QuestionSHA256 != baseline.QuestionSHA256 ||
					current.TrainExamples != baseline.TrainExamples || current.TestExamples != baseline.TestExamples {
					_, _ = fmt.Fprintln(stderr, "Python serving rows do not preserve question/source bindings across depths")
					return 1
				}
			}
		}
	}
	modelFingerprintAfter, err := decision.FingerprintModelDirectory(modelPath)
	if err != nil || modelFingerprintAfter.SHA256 != modelFingerprint.SHA256 {
		_, _ = fmt.Fprintln(stderr, "local model checkpoint could not be verified after serving")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func safeServingCasePath(value string) bool {
	if value == "" || len(value) > 4096 || strings.ContainsAny(value, "\\:\x00") || filepath.IsAbs(value) ||
		!strings.HasSuffix(value, ".json") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
	}
	return true
}

func validNonNegativeFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validPositiveFinite(value float64) bool {
	return validNonNegativeFinite(value) && value > 0
}

func validProbability(value float64) bool {
	return validNonNegativeFinite(value) && value <= 1
}

func sameOptionalString(got, want *string) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}

func uniqueDepths(values ...int) []int {
	result := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

type decisionObserveFile struct {
	SchemaVersion string          `json:"schema_version"`
	Question      json.RawMessage `json:"question"`
	Observations  json.RawMessage `json:"observations"`
	Backend       json.RawMessage `json:"backend,omitempty"`
	FitAt         json.RawMessage `json:"fit_at,omitempty"`
	RefitFactor   json.RawMessage `json:"refit_factor,omitempty"`
	FitOptions    json.RawMessage `json:"fit_options,omitempty"`
}

type pythonObservationResult struct {
	SchemaVersion            string          `json:"schema_version"`
	Status                   string          `json:"status"`
	QuestionID               string          `json:"question_id"`
	QuestionKey              string          `json:"question_key"`
	Model                    string          `json:"model"`
	NewObservations          int             `json:"new_observations"`
	ObservationCount         int             `json:"observation_count"`
	FitsThisCall             int             `json:"fits_this_call"`
	TotalFits                int             `json:"total_fits"`
	ArtifactTrainingExamples *int            `json:"artifact_training_examples"`
	Artifact                 json.RawMessage `json:"artifact"`
	CheckpointSHA256         string          `json:"checkpoint_sha256"`
	Checkpoint               json.RawMessage `json:"checkpoint"`
	HumanReviewRequired      bool            `json:"human_review_required"`
	ActivationAllowed        bool            `json:"activation_allowed"`
	ExecutionAuthorized      bool            `json:"execution_authorized"`
	CutoverEvidenceEligible  bool            `json:"cutover_evidence_eligible"`
}

func observeDecision(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision observe", stderr)
	streamID := fs.String("stream-id", "", "private observation stream identifier")
	expected := fs.Int("expected-revision", -1, "checkpoint revision for compare-and-swap")
	file := fs.String("file", "", "versioned observation request JSON (maximum 512 KiB)")
	artifactID := fs.String("artifact-id", "", "optional ID for a fitted L2 review candidate")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	actor := fs.String("actor", "local-user", "observation author")
	reason := fs.String("reason", "", "why these labeled observations were recorded")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !validObservationStreamID(*streamID) || *expected < 0 || *expected >= decision.MaxObservationCheckpointStates || *file == "" || fs.NArg() != 0 ||
		strings.TrimSpace(*actor) == "" || len(*actor) > 128 || strings.TrimSpace(*reason) == "" || len(*reason) > 2000 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision observe --stream-id <id> --expected-revision <n> --file <observe.json> --reason <text> [--artifact-id <id>] [--actor <name>] [--python python] [--db <path>]")
		return 2
	}
	if privacy.ContainsCredentialText(*actor) || privacy.ContainsCredentialText(*reason) {
		_, _ = fmt.Fprintln(stderr, "observation audit metadata contains credential material")
		return 1
	}
	inputText, err := readBoundedText(*file, 512<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	inputBytes := []byte(inputText)
	if privacy.ContainsCredential(inputBytes) {
		_, _ = fmt.Fprintln(stderr, "observation request contains credential material; no inference was run")
		return 1
	}
	var input decisionObserveFile
	if err := decodeJSONBytes(inputBytes, &input); err != nil || input.SchemaVersion != "threadforge.decision-observe-cli.v1" ||
		len(input.Question) == 0 || len(input.Observations) == 0 {
		_, _ = fmt.Fprintln(stderr, "invalid observation request; expected a versioned question and labeled observations")
		return 1
	}
	var examples []json.RawMessage
	if err := decodeJSONBytes(input.Observations, &examples); err != nil || len(examples) < 1 || len(examples) > 128 {
		_, _ = fmt.Fprintln(stderr, "observation batch must contain 1 to 128 labeled items")
		return 1
	}
	if *artifactID != "" && (len(*artifactID) > 64 || !validDecisionArtifactID(*artifactID)) {
		_, _ = fmt.Fprintln(stderr, "artifact ID must be a lowercase stable identifier of at most 64 characters")
		return 2
	}

	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	var previousCheckpoint json.RawMessage
	current, currentErr := databaseStore.GetDecisionObservation(context.Background(), *streamID)
	if currentErr == nil {
		if current.Revision != *expected {
			_, _ = fmt.Fprintf(stderr, "observation stream revision conflict: current revision is %d\n", current.Revision)
			return 1
		}
		previousCheckpoint = current.Content
	} else if errors.Is(currentErr, store.ErrDecisionObservationNotFound) {
		if *expected != 0 {
			_, _ = fmt.Fprintln(stderr, "observation stream does not exist at the expected revision")
			return 1
		}
	} else {
		_, _ = fmt.Fprintln(stderr, "could not load the private observation checkpoint")
		return 1
	}

	params := map[string]json.RawMessage{
		"question": input.Question, "observations": input.Observations,
	}
	if len(input.Backend) > 0 {
		params["backend"] = input.Backend
	} else {
		params["backend"] = defaultLocalBackend()
	}
	if len(previousCheckpoint) > 0 {
		params["checkpoint"] = previousCheckpoint
	}
	for key, value := range map[string]json.RawMessage{
		"fit_at": input.FitAt, "refit_factor": input.RefitFactor, "fit_options": input.FitOptions,
	} {
		if len(value) > 0 {
			params[key] = value
		}
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil || len(paramsJSON) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "observation request exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-observe", "decision.observe", paramsJSON)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "observation update failed safely; no checkpoint was committed")
		return 1
	}
	var result pythonObservationResult
	if response.Status != "ok" || decodeJSONBytes(response.Result, &result) != nil ||
		result.SchemaVersion != "threadforge.decision-observe-result.v1" || result.QuestionID == "" ||
		result.NewObservations != len(examples) || result.ObservationCount < 1 ||
		result.FitsThisCall < 0 || result.FitsThisCall > len(examples) || result.TotalFits < 0 ||
		result.TotalFits > result.ObservationCount || len(result.Checkpoint) == 0 || !validLowerSHA256(result.CheckpointSHA256) ||
		!result.HumanReviewRequired || result.ActivationAllowed || result.ExecutionAuthorized || result.CutoverEvidenceEligible {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid observation checkpoint; no changes were committed")
		return 1
	}
	checkpoint, err := decision.NewObservationCheckpoint(*streamID, *expected+1, result.Checkpoint, time.Now().UTC())
	if err != nil || checkpoint.Model != result.Model || checkpoint.QuestionKey != result.QuestionKey ||
		checkpoint.ObservationCount != result.ObservationCount || checkpoint.TotalFits != result.TotalFits {
		_, _ = fmt.Fprintln(stderr, "Python checkpoint failed binding, count, digest, or privacy validation; no changes were committed")
		return 1
	}
	if err := validateObservationCheckpointSuffix(checkpoint.Content, input.Observations, result.QuestionID); err != nil {
		_, _ = fmt.Fprintf(stderr, "Python checkpoint does not preserve the submitted observation batch: %v\n", err)
		return 1
	}
	artifactIsNull := bytes.Equal(bytes.TrimSpace(result.Artifact), []byte("null")) || len(result.Artifact) == 0
	var candidate *decision.Artifact
	switch result.Status {
	case "collecting":
		if !artifactIsNull || result.FitsThisCall != 0 || result.ArtifactTrainingExamples != nil {
			_, _ = fmt.Fprintln(stderr, "Python returned inconsistent collecting status; no changes were committed")
			return 1
		}
	case "fitted":
		if artifactIsNull || result.FitsThisCall < 1 || result.ArtifactTrainingExamples == nil {
			_, _ = fmt.Fprintln(stderr, "Python returned an incomplete L2 fit; no changes were committed")
			return 1
		}
		id := *artifactID
		if id == "" {
			id = fmt.Sprintf("%s-fit-%d", *streamID, result.TotalFits)
		}
		fitted, err := decision.NewArtifact(id, "L2", result.Artifact, time.Now().UTC())
		if err != nil || fitted.CalibrationExamples != *result.ArtifactTrainingExamples || fitted.Model != checkpoint.Model ||
			fitted.QuestionKey != checkpoint.QuestionKey {
			_, _ = fmt.Fprintln(stderr, "fitted L2 head failed candidate validation; no changes were committed")
			return 1
		}
		candidate = &fitted
	default:
		_, _ = fmt.Fprintln(stderr, "Python returned an unsupported observation status; no changes were committed")
		return 1
	}
	committed, err := databaseStore.CommitDecisionObservation(context.Background(), checkpoint, *expected, *actor, *reason, candidate, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "observation checkpoint was not committed: %v\n", err)
		return 1
	}
	output := map[string]any{
		"schema_version": "threadforge.decision-observe-cli-result.v1",
		"status":         result.Status, "stream_id": committed.StreamID, "revision": committed.Revision,
		"model": committed.Model, "question_key": committed.QuestionKey,
		"observation_count": committed.ObservationCount, "total_fits": committed.TotalFits,
		"checkpoint_sha256":     committed.ContentSHA256,
		"human_review_required": true, "activation_allowed": false, "execution_authorized": false,
		"cutover_evidence_eligible": false,
	}
	if candidate == nil {
		output["artifact"] = nil
	} else {
		output["artifact"] = map[string]any{
			"id": candidate.ID, "revision": candidate.Revision, "status": candidate.Status,
			"level": candidate.Level, "model": candidate.Model, "question_key": candidate.QuestionKey,
			"calibration_examples": candidate.CalibrationExamples, "content_sha256": candidate.ContentSHA256,
		}
	}
	return writeIndentedJSON(stdout, output, stderr)
}

func decisionObservationHistory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision observe history", stderr)
	streamID := fs.String("stream-id", "", "private observation stream identifier")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !validObservationStreamID(*streamID) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision observe history --stream-id <id> [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	history, err := databaseStore.DecisionObservationHistory(context.Background(), *streamID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{
		"schema_version": "threadforge.decision-observe-history.v1", "stream_id": *streamID, "history": history,
	}, stderr)
}

func validObservationStreamID(value string) bool {
	if len(value) < 1 || len(value) > 48 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validDecisionArtifactID(value string) bool {
	if len(value) < 1 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validateObservationCheckpointSuffix(checkpoint, requested json.RawMessage, questionID string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(checkpoint, &top); err != nil {
		return err
	}
	var observation map[string]json.RawMessage
	if err := json.Unmarshal(top["observation"], &observation); err != nil {
		return err
	}
	var storedQuestionID string
	var states []json.RawMessage
	var labels []int
	if err := json.Unmarshal(observation["question_id"], &storedQuestionID); err != nil || storedQuestionID != questionID {
		return errors.New("question ID differs from the operation result")
	}
	if err := json.Unmarshal(observation["states"], &states); err != nil {
		return err
	}
	if err := json.Unmarshal(observation["labels"], &labels); err != nil {
		return err
	}
	var examples []struct {
		State json.RawMessage `json:"state"`
		Label int             `json:"label"`
	}
	if err := json.Unmarshal(requested, &examples); err != nil || len(states) < len(examples) || len(labels) != len(states) {
		return errors.New("checkpoint observation count does not cover the submitted batch")
	}
	start := len(states) - len(examples)
	for index, example := range examples {
		if labels[start+index] != example.Label || !equalJSONValues(states[start+index], example.State) {
			return fmt.Errorf("checkpoint sample %d differs from submitted data", index)
		}
	}
	return nil
}

func equalJSONValues(left, right []byte) bool {
	decode := func(value []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

func calibrateDecision(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision calibrate", stderr)
	id := fs.String("id", "", "new stable calibration artifact identifier")
	expected := fs.Int("expected-revision", -1, "must be 0 when creating a new artifact")
	file := fs.String("file", "", "versioned calibration request JSON (maximum 512 KiB)")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	actor := fs.String("actor", "local-user", "calibration author")
	reason := fs.String("reason", "", "why this calibration was requested")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || *expected != 0 || *file == "" || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision calibrate --id <id> --expected-revision 0 --file <calibration.json> --actor <name> --reason <text> [--python python] [--db <path>]")
		return 2
	}
	inputText, err := readBoundedText(*file, 512<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	inputBytes := []byte(inputText)
	if privacy.ContainsCredential(inputBytes) {
		_, _ = fmt.Fprintln(stderr, "calibration input contains credential material; no inference was run")
		return 1
	}
	var input struct {
		SchemaVersion string          `json:"schema_version"`
		Question      json.RawMessage `json:"question"`
		Examples      json.RawMessage `json:"examples"`
		Backend       json.RawMessage `json:"backend,omitempty"`
		Level         string          `json:"level"`
		FitOptions    json.RawMessage `json:"fit_options,omitempty"`
	}
	if err := decodeJSONBytes(inputBytes, &input); err != nil || input.SchemaVersion != "threadforge.decision-calibration.v1" ||
		len(input.Question) == 0 || len(input.Examples) == 0 || (input.Level != "L1" && input.Level != "L2") {
		_, _ = fmt.Fprintln(stderr, "invalid calibration request; expected the versioned question, labeled examples, backend, and L1 or L2 level")
		return 1
	}
	var examples []json.RawMessage
	if err := decodeJSONBytes(input.Examples, &examples); err != nil || len(examples) < 2 || len(examples) > 128 {
		_, _ = fmt.Fprintln(stderr, "calibration examples must be an array containing 2 to 128 labeled items")
		return 1
	}
	params := map[string]json.RawMessage{"question": input.Question, "examples": input.Examples, "level": mustJSON(input.Level)}
	if len(input.Backend) != 0 {
		params["backend"] = input.Backend
	} else {
		params["backend"] = defaultLocalBackend()
	}
	if len(input.FitOptions) != 0 {
		params["fit_options"] = input.FitOptions
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not encode calibration request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-calibrate", "decision.calibrate", paramsJSON)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "decision calibration failed safely; Python backend details were withheld")
		return 1
	}
	var result struct {
		SchemaVersion           string          `json:"schema_version"`
		Status                  string          `json:"status"`
		QuestionID              string          `json:"question_id"`
		Level                   string          `json:"level"`
		Examples                int             `json:"examples"`
		Artifact                json.RawMessage `json:"artifact"`
		FitOptions              json.RawMessage `json:"fit_options,omitempty"`
		HumanReviewRequired     bool            `json:"human_review_required"`
		ActivationAllowed       bool            `json:"activation_allowed"`
		ExecutionAuthorized     bool            `json:"execution_authorized"`
		CutoverEvidenceEligible bool            `json:"cutover_evidence_eligible"`
	}
	if response.Status != "ok" || decodeJSONBytes(response.Result, &result) != nil || result.Level != input.Level ||
		result.SchemaVersion != "threadforge.decision-calibration-result.v1" || result.Status != "calibrated" ||
		result.Examples != len(examples) || strings.TrimSpace(result.QuestionID) == "" || len(result.Artifact) == 0 ||
		!result.HumanReviewRequired || result.ActivationAllowed || result.ExecutionAuthorized || result.CutoverEvidenceEligible ||
		(input.Level == "L2") != (len(result.FitOptions) != 0) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid calibration response")
		return 1
	}
	artifact, err := decision.NewArtifact(*id, input.Level, result.Artifact, time.Now().UTC())
	if err != nil || artifact.CalibrationExamples != result.Examples {
		_, _ = fmt.Fprintln(stderr, "calibration artifact failed structural, binding, digest, or privacy validation")
		return 1
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	if err := databaseStore.CreateDecisionArtifact(context.Background(), artifact, *actor, *reason); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{"status": "candidate", "artifact": artifact}, stderr)
}

func runTypedDecisionBatch(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision batch", stderr)
	file := fs.String("file", "", "versioned batch decision request JSON (maximum 512 KiB)")
	artifactID := fs.String("artifact-id", "", "explicit active calibration artifact identifier")
	bundleID := fs.String("bundle-id", "", "explicit active AnyJev native bundle identifier")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision batch --file <decision-batch.json> [--artifact-id <active-id>|--bundle-id <active-id>] [--python python] [--db <path>]")
		return 2
	}
	if *artifactID != "" && *bundleID != "" {
		_, _ = fmt.Fprintln(stderr, "choose either --artifact-id or --bundle-id, not both")
		return 2
	}
	data, err := readBoundedText(*file, 512<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "decision batch contains credential material; no inference was run")
		return 1
	}
	var input struct {
		SchemaVersion string          `json:"schema_version"`
		Question      json.RawMessage `json:"question"`
		States        json.RawMessage `json:"states"`
		Backend       json.RawMessage `json:"backend,omitempty"`
		Level         string          `json:"level,omitempty"`
		RequireLevel  string          `json:"require_level,omitempty"`
	}
	var states []json.RawMessage
	if err := decodeJSONBytes([]byte(data), &input); err != nil ||
		input.SchemaVersion != "threadforge.decision-batch-request.v1" || len(input.Question) == 0 ||
		decodeJSONBytes(input.States, &states) != nil || len(states) == 0 || len(states) > 128 {
		_, _ = fmt.Fprintln(stderr, "invalid batch decision request; expected one typed question and 1 to 128 states")
		return 1
	}
	params := map[string]json.RawMessage{"question": input.Question, "states": input.States}
	if len(input.Backend) != 0 {
		params["backend"] = input.Backend
	} else {
		params["backend"] = defaultLocalBackend()
	}
	if input.Level != "" {
		params["level"] = mustJSON(input.Level)
	}
	if input.RequireLevel != "" {
		params["require_level"] = mustJSON(input.RequireLevel)
	}
	operation := "decision.decide_batch"
	var expectedBundleModel, expectedBundleContentSHA256, expectedBundleCheckpointSHA256 string
	var expectedBundleRevision int
	var expectedArtifactModel, expectedArtifactCheckpointSHA256, expectedArtifactLevel string
	if *artifactID != "" {
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		artifact, readErr := databaseStore.GetDecisionArtifact(context.Background(), *artifactID)
		closeStore()
		if readErr != nil {
			_, _ = fmt.Fprintln(stderr, readErr)
			return 1
		}
		if artifact.Status != decision.ArtifactActive {
			_, _ = fmt.Fprintln(stderr, "decision artifact is not active; only explicitly approved local artifacts may be used")
			return 1
		}
		expectedArtifactModel = artifact.Model
		expectedArtifactCheckpointSHA256 = artifact.ModelCheckpointSHA256
		expectedArtifactLevel = artifact.Level
		if input.Level == "" {
			params["level"] = mustJSON(artifact.Level)
		} else if input.Level != "auto" && input.Level != artifact.Level {
			_, _ = fmt.Fprintf(stderr, "requested decision level %q does not match active artifact level %q\n", input.Level, artifact.Level)
			return 1
		} else {
			expectedArtifactLevel = input.Level
		}
		params["artifact"] = artifact.Content
		if expectedArtifactCheckpointSHA256 != "" {
			params["model_checkpoint_sha256"] = mustJSON(expectedArtifactCheckpointSHA256)
		}
	}
	if *bundleID != "" {
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		bundle, readErr := databaseStore.GetDecisionBundle(context.Background(), *bundleID)
		closeStore()
		if readErr != nil {
			_, _ = fmt.Fprintln(stderr, readErr)
			return 1
		}
		if bundle.Status != decision.NativeBundleActive {
			_, _ = fmt.Fprintln(stderr, "decision bundle is not active; only explicitly approved local bundles may be used")
			return 1
		}
		expectedBundleModel = bundle.Model
		expectedBundleContentSHA256 = bundle.ContentSHA256
		expectedBundleCheckpointSHA256 = bundle.ModelCheckpointSHA256
		expectedBundleRevision = bundle.Revision
		params["bundle"] = bundle.Content
		params["bundle_id"] = mustJSON(bundle.ID)
		params["bundle_revision"] = mustJSON(bundle.Revision)
		params["bundle_status"] = mustJSON(string(bundle.Status))
		params["bundle_content_sha256"] = mustJSON(bundle.ContentSHA256)
		if expectedBundleCheckpointSHA256 != "" {
			params["model_checkpoint_sha256"] = mustJSON(expectedBundleCheckpointSHA256)
		}
		operation = "decision.bundle.decide_batch"
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not encode decision batch request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-batch", operation, paramsJSON)
	if err != nil || response.Status != "ok" {
		_, _ = fmt.Fprintln(stderr, "decision batch inference failed safely; Python backend details were withheld")
		return 1
	}
	var result struct {
		SchemaVersion            string            `json:"schema_version"`
		Status                   string            `json:"status,omitempty"`
		BundleID                 string            `json:"bundle_id,omitempty"`
		BundleRevision           int               `json:"bundle_revision,omitempty"`
		BundleStatus             string            `json:"bundle_status,omitempty"`
		BundleContentSHA256      string            `json:"bundle_content_sha256,omitempty"`
		Model                    string            `json:"model,omitempty"`
		ModelCheckpointSHA256    string            `json:"model_checkpoint_sha256,omitempty"`
		QuestionID               string            `json:"question_id"`
		QuestionKey              string            `json:"question_key"`
		Count                    int               `json:"count"`
		Level                    string            `json:"level"`
		Decisions                []json.RawMessage `json:"decisions"`
		Backend                  string            `json:"backend"`
		RemoteOptIn              bool              `json:"remote_opt_in,omitempty"`
		BackendStats             map[string]int    `json:"backend_stats"`
		HumanReviewRequired      bool              `json:"human_review_required,omitempty"`
		ModelEnvironmentVerified bool              `json:"model_environment_verified,omitempty"`
		ExecutionAuthorized      bool              `json:"execution_authorized,omitempty"`
		CutoverEvidenceEligible  bool              `json:"cutover_evidence_eligible,omitempty"`
	}
	expectedSchema := "threadforge.decision-batch-result.v1"
	if *bundleID != "" {
		expectedSchema = "threadforge.decision-bundle-batch-result.v1"
	}
	if decodeJSONBytes(response.Result, &result) != nil || result.SchemaVersion != expectedSchema ||
		result.Count != len(states) || len(result.Decisions) != len(states) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid decision-batch response")
		return 1
	}
	if *artifactID != "" && (!result.HumanReviewRequired || result.Level != expectedArtifactLevel ||
		result.ModelEnvironmentVerified != (expectedArtifactCheckpointSHA256 != "") ||
		result.ModelCheckpointSHA256 != expectedArtifactCheckpointSHA256 ||
		result.ExecutionAuthorized || result.CutoverEvidenceEligible ||
		(expectedArtifactCheckpointSHA256 != "" && (result.Backend != expectedArtifactModel || result.RemoteOptIn))) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or non-advisory per-question artifact batch decision")
		return 1
	}
	if *bundleID != "" && (result.Status != "decided" || result.BundleID != *bundleID ||
		result.BundleRevision != expectedBundleRevision || result.BundleStatus != "active" ||
		result.BundleContentSHA256 != expectedBundleContentSHA256 || result.Model != expectedBundleModel ||
		result.Backend != expectedBundleModel || result.QuestionID == "" || !validLowerSHA256(result.BundleContentSHA256) ||
		(result.Level != "raw" && result.Level != "L0" && result.Level != "L1" && result.Level != "L2" && result.Level != "auto") ||
		!result.HumanReviewRequired || result.ModelEnvironmentVerified != (expectedBundleCheckpointSHA256 != "") ||
		result.ModelCheckpointSHA256 != expectedBundleCheckpointSHA256 ||
		result.ExecutionAuthorized || result.CutoverEvidenceEligible) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or non-advisory native bundle batch decision")
		return 1
	}
	for _, encoded := range result.Decisions {
		var item struct {
			Kind         string             `json:"kind"`
			Level        string             `json:"level"`
			Answer       json.RawMessage    `json:"answer"`
			Confidence   float64            `json:"confidence"`
			Distribution map[string]float64 `json:"distribution,omitempty"`
			Value        *float64           `json:"value,omitempty"`
			Probability  *float64           `json:"probability,omitempty"`
		}
		if decodeJSONBytes(encoded, &item) != nil ||
			(item.Kind != "choice" && item.Kind != "score" && item.Kind != "noul") ||
			(item.Level != "raw" && item.Level != "L0" && item.Level != "L1" && item.Level != "L2") ||
			len(item.Answer) == 0 || item.Confidence < 0 || item.Confidence > 1 {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid decision-batch item")
			return 1
		}
	}
	_, _ = stdout.Write(append(response.Result, '\n'))
	return 0
}

func runTypedDecision(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision run", stderr)
	file := fs.String("file", "", "versioned decision request JSON (maximum 512 KiB)")
	artifactID := fs.String("artifact-id", "", "explicit active calibration artifact identifier")
	bundleID := fs.String("bundle-id", "", "explicit active AnyJev native bundle identifier")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision run --file <decision.json> [--artifact-id <active-id>|--bundle-id <active-id>] [--python python] [--db <path>]")
		return 2
	}
	if *artifactID != "" && *bundleID != "" {
		_, _ = fmt.Fprintln(stderr, "choose either --artifact-id or --bundle-id, not both")
		return 2
	}
	data, err := readBoundedText(*file, 512<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var input struct {
		SchemaVersion string            `json:"schema_version"`
		Question      json.RawMessage   `json:"question,omitempty"`
		Questions     []json.RawMessage `json:"questions,omitempty"`
		State         json.RawMessage   `json:"state"`
		Backend       json.RawMessage   `json:"backend,omitempty"`
		Level         string            `json:"level,omitempty"`
		RequireLevel  string            `json:"require_level,omitempty"`
	}
	if err := decodeJSONBytes([]byte(data), &input); err != nil || input.SchemaVersion != "threadforge.decision-request.v1" || len(input.State) == 0 {
		_, _ = fmt.Fprintln(stderr, "invalid versioned decision request")
		return 1
	}
	if (len(input.Question) != 0) == (len(input.Questions) != 0) {
		_, _ = fmt.Fprintln(stderr, "decision request must provide exactly one of question or questions")
		return 1
	}
	params := map[string]json.RawMessage{"state": input.State}
	if len(input.Question) != 0 {
		params["question"] = input.Question
	}
	if len(input.Questions) != 0 {
		params["questions"] = mustJSON(input.Questions)
	}
	if len(input.Backend) != 0 {
		params["backend"] = input.Backend
	} else {
		params["backend"] = defaultLocalBackend()
	}
	if input.Level != "" {
		params["level"] = mustJSON(input.Level)
	}
	if input.RequireLevel != "" {
		params["require_level"] = mustJSON(input.RequireLevel)
	}
	var expectedBundleModel, expectedBundleContentSHA256, expectedBundleCheckpointSHA256 string
	var expectedBundleRevision int
	operation := "decision.decide"
	var expectedArtifactModel, expectedArtifactCheckpointSHA256, expectedArtifactLevel string
	if *artifactID != "" {
		if len(input.Question) == 0 && len(input.Questions) != 1 {
			_, _ = fmt.Fprintln(stderr, "one calibration artifact can only be attached to exactly one question")
			return 1
		}
		if len(input.Question) != 0 && len(input.Questions) != 0 {
			_, _ = fmt.Fprintln(stderr, "specify question or questions, not both")
			return 1
		}
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		artifact, readErr := databaseStore.GetDecisionArtifact(context.Background(), *artifactID)
		closeStore()
		if readErr != nil {
			_, _ = fmt.Fprintln(stderr, readErr)
			return 1
		}
		if artifact.Status != decision.ArtifactActive {
			_, _ = fmt.Fprintln(stderr, "decision artifact is not active; only explicitly approved local artifacts may be used")
			return 1
		}
		expectedArtifactModel = artifact.Model
		expectedArtifactCheckpointSHA256 = artifact.ModelCheckpointSHA256
		expectedArtifactLevel = artifact.Level
		if input.Level == "" {
			params["level"] = mustJSON(artifact.Level)
		} else if input.Level != "auto" && input.Level != artifact.Level {
			_, _ = fmt.Fprintf(stderr, "requested decision level %q does not match active artifact level %q\n", input.Level, artifact.Level)
			return 1
		} else {
			expectedArtifactLevel = input.Level
		}
		params["artifact"] = artifact.Content
		if expectedArtifactCheckpointSHA256 != "" {
			params["model_checkpoint_sha256"] = mustJSON(expectedArtifactCheckpointSHA256)
		}
	}
	if *bundleID != "" {
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		bundle, readErr := databaseStore.GetDecisionBundle(context.Background(), *bundleID)
		closeStore()
		if readErr != nil {
			_, _ = fmt.Fprintln(stderr, readErr)
			return 1
		}
		if bundle.Status != decision.NativeBundleActive {
			_, _ = fmt.Fprintln(stderr, "decision bundle is not active; only explicitly approved local bundles may be used")
			return 1
		}
		expectedBundleModel = bundle.Model
		expectedBundleContentSHA256 = bundle.ContentSHA256
		expectedBundleCheckpointSHA256 = bundle.ModelCheckpointSHA256
		expectedBundleRevision = bundle.Revision
		params["bundle"] = bundle.Content
		params["bundle_id"] = mustJSON(bundle.ID)
		params["bundle_revision"] = mustJSON(bundle.Revision)
		params["bundle_status"] = mustJSON(string(bundle.Status))
		params["bundle_content_sha256"] = mustJSON(bundle.ContentSHA256)
		if expectedBundleCheckpointSHA256 != "" {
			params["model_checkpoint_sha256"] = mustJSON(expectedBundleCheckpointSHA256)
		}
		operation = "decision.bundle.decide"
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not encode decision request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-run", operation, paramsJSON)
	if err != nil || response.Status != "ok" {
		_, _ = fmt.Fprintln(stderr, "decision inference failed safely; Python backend details were withheld")
		return 1
	}
	var result json.RawMessage
	if err := decodeJSONBytes(response.Result, &result); err != nil || len(result) == 0 {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid decision response")
		return 1
	}
	if *bundleID != "" {
		var bundleResult struct {
			SchemaVersion            string          `json:"schema_version"`
			Status                   string          `json:"status"`
			BundleID                 string          `json:"bundle_id"`
			BundleRevision           int             `json:"bundle_revision"`
			BundleStatus             string          `json:"bundle_status"`
			BundleContentSHA256      string          `json:"bundle_content_sha256"`
			Model                    string          `json:"model"`
			ModelCheckpointSHA256    string          `json:"model_checkpoint_sha256,omitempty"`
			Backend                  string          `json:"backend"`
			RemoteOptIn              bool            `json:"remote_opt_in"`
			Decision                 json.RawMessage `json:"decision"`
			BackendStats             map[string]int  `json:"backend_stats"`
			HumanReviewRequired      bool            `json:"human_review_required"`
			ModelEnvironmentVerified bool            `json:"model_environment_verified"`
			ExecutionAuthorized      bool            `json:"execution_authorized"`
			CutoverEvidenceEligible  bool            `json:"cutover_evidence_eligible"`
		}
		if decodeJSONBytes(response.Result, &bundleResult) != nil ||
			bundleResult.SchemaVersion != "threadforge.decision-bundle-decision.v1" || bundleResult.Status != "decided" ||
			bundleResult.BundleID != *bundleID || bundleResult.BundleRevision != expectedBundleRevision || bundleResult.BundleStatus != "active" ||
			bundleResult.BundleContentSHA256 != expectedBundleContentSHA256 || bundleResult.Model != expectedBundleModel ||
			bundleResult.Backend != expectedBundleModel || len(bundleResult.Decision) == 0 ||
			!bundleResult.HumanReviewRequired || bundleResult.ModelEnvironmentVerified != (expectedBundleCheckpointSHA256 != "") ||
			bundleResult.ModelCheckpointSHA256 != expectedBundleCheckpointSHA256 ||
			bundleResult.ExecutionAuthorized || bundleResult.CutoverEvidenceEligible {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or non-advisory native bundle decision")
			return 1
		}
		var decisionSet struct {
			Level     string                     `json:"level"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if decodeJSONBytes(bundleResult.Decision, &decisionSet) != nil ||
			(decisionSet.Level != "raw" && decisionSet.Level != "L0" && decisionSet.Level != "L1" && decisionSet.Level != "L2") ||
			len(decisionSet.Questions) == 0 {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid native bundle decision set")
			return 1
		}
		for _, encoded := range decisionSet.Questions {
			var item struct {
				Kind         string             `json:"kind"`
				Level        string             `json:"level"`
				Answer       json.RawMessage    `json:"answer"`
				Confidence   float64            `json:"confidence"`
				Distribution map[string]float64 `json:"distribution,omitempty"`
				Value        *float64           `json:"value,omitempty"`
				Probability  *float64           `json:"probability,omitempty"`
			}
			if decodeJSONBytes(encoded, &item) != nil ||
				(item.Kind != "choice" && item.Kind != "score" && item.Kind != "noul") ||
				(item.Level != "raw" && item.Level != "L0" && item.Level != "L1" && item.Level != "L2") ||
				len(item.Answer) == 0 || item.Confidence < 0 || item.Confidence > 1 {
				_, _ = fmt.Fprintln(stderr, "Python returned an invalid native bundle decision item")
				return 1
			}
		}
	}
	if *artifactID != "" {
		var artifactResult struct {
			Level                    string                     `json:"level"`
			Questions                map[string]json.RawMessage `json:"questions"`
			Backend                  string                     `json:"backend"`
			RemoteOptIn              bool                       `json:"remote_opt_in"`
			BackendStats             map[string]int             `json:"backend_stats"`
			ModelCheckpointSHA256    string                     `json:"model_checkpoint_sha256,omitempty"`
			HumanReviewRequired      bool                       `json:"human_review_required"`
			ModelEnvironmentVerified bool                       `json:"model_environment_verified"`
			ExecutionAuthorized      bool                       `json:"execution_authorized"`
			CutoverEvidenceEligible  bool                       `json:"cutover_evidence_eligible"`
		}
		if decodeJSONBytes(result, &artifactResult) != nil || len(artifactResult.Questions) != 1 ||
			!artifactResult.HumanReviewRequired ||
			artifactResult.ModelEnvironmentVerified != (expectedArtifactCheckpointSHA256 != "") ||
			artifactResult.ModelCheckpointSHA256 != expectedArtifactCheckpointSHA256 ||
			artifactResult.ExecutionAuthorized || artifactResult.CutoverEvidenceEligible ||
			(expectedArtifactCheckpointSHA256 != "" && artifactResult.Backend != expectedArtifactModel) ||
			artifactResult.Level != expectedArtifactLevel {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or non-advisory per-question artifact decision")
			return 1
		}
	}
	_, _ = stdout.Write(append(result, '\n'))
	return 0
}

func runDecisionArtifact(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision artifact <list|show|history|import|export|approve|reject|retire>")
		return 2
	}
	switch args[0] {
	case "import":
		return importDecisionArtifact(args[1:], stdout, stderr)
	case "export":
		return exportDecisionArtifact(args[1:], stdout, stderr)
	case "list":
		fs := flagSet("decision artifact list", stderr)
		status := fs.String("status", "", "optional status filter: candidate, active, rejected, or retired")
		limit := fs.Int("limit", 50, "maximum artifacts (1-200)")
		offset := fs.Int("offset", 0, "number of artifacts to skip")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			return 2
		}
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		items, total, err := databaseStore.ListDecisionArtifacts(context.Background(), *status, *limit, *offset)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, map[string]any{"total": total, "artifacts": items}, stderr)
	case "show", "history":
		fs := flagSet("decision artifact "+args[0], stderr)
		id := fs.String("id", "", "decision artifact identifier")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *id == "" || fs.NArg() != 0 {
			_, _ = fmt.Fprintf(stderr, "usage: threadforge decision artifact %s --id <id> [--db <path>]\n", args[0])
			return 2
		}
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		if args[0] == "show" {
			artifact, err := databaseStore.GetDecisionArtifact(context.Background(), *id)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
			return writeIndentedJSON(stdout, artifact, stderr)
		}
		history, err := databaseStore.DecisionArtifactHistory(context.Background(), *id)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, history, stderr)
	case "approve", "reject", "retire":
		fs := flagSet("decision artifact "+args[0], stderr)
		id := fs.String("id", "", "decision artifact identifier")
		expected := fs.Int("expected-revision", 0, "current artifact revision for compare-and-swap")
		actor := fs.String("actor", "", "human reviewer or owner")
		reason := fs.String("reason", "", "review or retirement rationale")
		modelPath := fs.String("model-path", "", "optional materialized local checkpoint to bind during approval")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *id == "" || *expected < 1 || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" || fs.NArg() != 0 {
			_, _ = fmt.Fprintf(stderr, "usage: threadforge decision artifact %s --id <id> --expected-revision <n> --actor <human> --reason <text> [--model-path <local-model-directory>] [--db <path>]\n", args[0])
			return 2
		}
		if args[0] != "approve" && *modelPath != "" {
			_, _ = fmt.Fprintln(stderr, "--model-path is only valid while approving an artifact")
			return 2
		}
		modelCheckpointSHA256 := ""
		if *modelPath != "" {
			fingerprint, err := decision.FingerprintModelDirectory(*modelPath)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "could not fingerprint the local model checkpoint")
				return 1
			}
			modelCheckpointSHA256 = fingerprint.SHA256
		}
		next := map[string]decision.ArtifactStatus{
			"approve": decision.ArtifactActive,
			"reject":  decision.ArtifactRejected,
			"retire":  decision.ArtifactRetired,
		}[args[0]]
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		artifact, err := databaseStore.TransitionDecisionArtifactWithCheckpoint(context.Background(), *id, *expected, next, *actor, *reason, modelCheckpointSHA256, time.Now().UTC())
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, artifact, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown decision artifact command %q\n", args[0])
		return 2
	}
}

func importDecisionArtifact(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision artifact import", stderr)
	file := fs.String("file", "", "versioned per-question AnyJev calibration import JSON")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision artifact import --file <calibration-import.json> [--db <path>]")
		return 2
	}
	data, err := readBoundedText(*file, int64(decision.MaxArtifactBytes+(64<<10)))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "decision artifact import contains credential material")
		return 1
	}
	var fields map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &fields); err != nil || len(fields) != 5 {
		_, _ = fmt.Fprintln(stderr, "invalid decision artifact import envelope")
		return 1
	}
	for key := range fields {
		if key != "schema_version" && key != "id" && key != "actor" && key != "reason" && key != "content" {
			_, _ = fmt.Fprintln(stderr, "decision artifact import envelope contains unsupported fields")
			return 1
		}
	}
	var input struct {
		SchemaVersion string          `json:"schema_version"`
		ID            string          `json:"id"`
		Actor         string          `json:"actor"`
		Reason        string          `json:"reason"`
		Content       json.RawMessage `json:"content"`
	}
	if err := decodeJSONBytes([]byte(data), &input); err != nil || input.SchemaVersion != "threadforge.decision-artifact-import.v1" ||
		strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Actor) == "" || strings.TrimSpace(input.Reason) == "" ||
		len(input.Content) == 0 || len(input.Content) > decision.MaxArtifactBytes {
		_, _ = fmt.Fprintln(stderr, "invalid decision artifact import fields or content bounds")
		return 1
	}
	artifact, err := decision.NewImportedArtifact(input.ID, input.Content, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev per-question artifact failed binding, structure, size, or privacy validation")
		return 1
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	if err := databaseStore.ImportDecisionArtifact(context.Background(), artifact, input.Actor, input.Reason); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{
		"schema_version": "threadforge.decision-artifact-import-result.v1",
		"status":         "candidate", "artifact_id": artifact.ID, "revision": artifact.Revision,
		"level": artifact.Level, "model": artifact.Model, "question_key": artifact.QuestionKey,
		"calibration_examples": artifact.CalibrationExamples, "content_sha256": artifact.ContentSHA256,
		"human_review_required": true,
	}, stderr)
}

func exportDecisionArtifact(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision artifact export", stderr)
	id := fs.String("id", "", "local decision artifact identifier")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision artifact export --id <id> [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	artifact, err := databaseStore.GetDecisionArtifact(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if !json.Valid(artifact.Content) {
		_, _ = fmt.Fprintln(stderr, "stored decision artifact content is invalid")
		return 1
	}
	if _, err := stdout.Write(append(artifact.Content, '\n')); err != nil {
		_, _ = fmt.Fprintln(stderr, "could not write exported AnyJev artifact")
		return 1
	}
	return 0
}

func runDecisionBundle(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision bundle <import|list|show|history|approve|reject|retire|export|run|batch>")
		return 2
	}
	switch args[0] {
	case "import":
		return importDecisionBundle(args[1:], stdout, stderr)
	case "run":
		return runDecisionBundleInference(args[1:], stdout, stderr)
	case "batch":
		return runDecisionBundleBatch(args[1:], stdout, stderr)
	case "export":
		return exportDecisionBundle(args[1:], stdout, stderr)
	case "list":
		fs := flagSet("decision bundle list", stderr)
		status := fs.String("status", "", "optional status filter: candidate, active, rejected, or retired")
		limit := fs.Int("limit", 50, "maximum bundles (1-200)")
		offset := fs.Int("offset", 0, "number of bundles to skip")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 0 {
			return 2
		}
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		items, total, err := databaseStore.ListDecisionBundles(context.Background(), *status, *limit, *offset)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, map[string]any{"schema_version": "threadforge.decision-bundle-list.v1", "total": total, "bundles": items}, stderr)
	case "show", "history":
		fs := flagSet("decision bundle "+args[0], stderr)
		id := fs.String("id", "", "local native bundle identifier")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *id == "" || fs.NArg() != 0 {
			_, _ = fmt.Fprintf(stderr, "usage: threadforge decision bundle %s --id <id> [--db <path>]\n", args[0])
			return 2
		}
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		if args[0] == "show" {
			bundle, err := databaseStore.GetDecisionBundle(context.Background(), *id)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				return 1
			}
			return writeIndentedJSON(stdout, decision.SummarizeNativeBundle(bundle), stderr)
		}
		history, err := databaseStore.DecisionBundleHistory(context.Background(), *id)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, map[string]any{"schema_version": "threadforge.decision-bundle-history.v1", "id": *id, "history": history}, stderr)
	case "approve", "reject", "retire":
		fs := flagSet("decision bundle "+args[0], stderr)
		id := fs.String("id", "", "local native bundle identifier")
		expected := fs.Int("expected-revision", 0, "current bundle revision for compare-and-swap")
		actor := fs.String("actor", "", "human reviewer or owner")
		reason := fs.String("reason", "", "review or retirement rationale")
		modelPath := fs.String("model-path", "", "optional materialized local checkpoint to bind during approval")
		database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *id == "" || *expected < 1 || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" || fs.NArg() != 0 {
			_, _ = fmt.Fprintf(stderr, "usage: threadforge decision bundle %s --id <id> --expected-revision <n> --actor <human> --reason <text> [--model-path <local-model-directory>] [--db <path>]\n", args[0])
			return 2
		}
		if args[0] != "approve" && *modelPath != "" {
			_, _ = fmt.Fprintln(stderr, "--model-path is only valid while approving a bundle")
			return 2
		}
		modelCheckpointSHA256 := ""
		if *modelPath != "" {
			fingerprint, err := decision.FingerprintModelDirectory(*modelPath)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "could not fingerprint the local model checkpoint")
				return 1
			}
			modelCheckpointSHA256 = fingerprint.SHA256
		}
		next := map[string]decision.NativeBundleStatus{
			"approve": decision.NativeBundleActive,
			"reject":  decision.NativeBundleRejected,
			"retire":  decision.NativeBundleRetired,
		}[args[0]]
		databaseStore, closeStore, err := openContinuityStore(*database)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		defer closeStore()
		bundle, err := databaseStore.TransitionDecisionBundle(context.Background(), *id, *expected, next, *actor, *reason, modelCheckpointSHA256, time.Now().UTC())
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return writeIndentedJSON(stdout, decision.SummarizeNativeBundle(bundle), stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown decision bundle command %q\n", args[0])
		return 2
	}
}

func runDecisionBundleInference(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision bundle run", stderr)
	file := fs.String("file", "", "versioned decision request JSON (maximum 512 KiB)")
	id := fs.String("id", "", "explicitly approved active AnyJev bundle identifier")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || !validDecisionArtifactID(*id) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision bundle run --file <decision.json> --id <active-bundle-id> [--python python] [--db <path>]")
		return 2
	}
	forwarded := []string{"--file", *file, "--bundle-id", *id, "--python", *python}
	if *database != "" {
		forwarded = append(forwarded, "--db", *database)
	}
	return runTypedDecision(forwarded, stdout, stderr)
}

func runDecisionBundleBatch(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision bundle batch", stderr)
	file := fs.String("file", "", "versioned batch decision request JSON (maximum 512 KiB)")
	id := fs.String("id", "", "explicitly approved active AnyJev bundle identifier")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || !validDecisionArtifactID(*id) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision bundle batch --file <decision-batch.json> --id <active-bundle-id> [--python python] [--db <path>]")
		return 2
	}
	forwarded := []string{"--file", *file, "--bundle-id", *id, "--python", *python}
	if *database != "" {
		forwarded = append(forwarded, "--db", *database)
	}
	return runTypedDecisionBatch(forwarded, stdout, stderr)
}

func importDecisionBundle(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision bundle import", stderr)
	file := fs.String("file", "", "versioned AnyJev native bundle import JSON")
	includeObservations := fs.Bool("include-observations", false, "explicitly allow private labeled observations in the local bundle")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision bundle import --file <bundle-import.json> [--include-observations] [--python python] [--db <path>]")
		return 2
	}
	data, err := readBoundedText(*file, int64(decision.MaxArtifactBytes+(64<<10)))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(data)) {
		_, _ = fmt.Fprintln(stderr, "decision bundle import contains credential material")
		return 1
	}
	var envelope map[string]json.RawMessage
	if err := decodeJSONBytes([]byte(data), &envelope); err != nil || envelope == nil {
		_, _ = fmt.Fprintln(stderr, "invalid decision bundle import envelope")
		return 1
	}
	for key := range envelope {
		if key != "schema_version" && key != "id" && key != "actor" && key != "reason" && key != "bundle" {
			_, _ = fmt.Fprintln(stderr, "decision bundle import envelope contains unsupported fields")
			return 1
		}
	}
	var input struct {
		SchemaVersion string          `json:"schema_version"`
		ID            string          `json:"id"`
		Actor         string          `json:"actor"`
		Reason        string          `json:"reason"`
		Bundle        json.RawMessage `json:"bundle"`
	}
	if err := decodeJSONBytes([]byte(data), &input); err != nil || input.SchemaVersion != "threadforge.decision-bundle-import.v1" ||
		!validDecisionArtifactID(input.ID) || strings.TrimSpace(input.Actor) == "" || len(input.Actor) > 128 ||
		strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2000 || len(input.Bundle) == 0 || len(input.Bundle) > decision.MaxArtifactBytes {
		_, _ = fmt.Fprintln(stderr, "invalid decision bundle import fields or content bounds")
		return 1
	}
	if privacy.ContainsCredentialText(input.Actor) || privacy.ContainsCredentialText(input.Reason) {
		_, _ = fmt.Fprintln(stderr, "decision bundle audit metadata contains credential material")
		return 1
	}
	params, err := json.Marshal(map[string]any{
		"bundle": json.RawMessage(input.Bundle), "include_observations": *includeObservations,
	})
	if err != nil || len(params) > maxPythonProtocolLine {
		_, _ = fmt.Fprintln(stderr, "decision bundle exceeds the Python protocol limit")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "decision-bundle-import", "decision.bundle.validate", params)
	if err != nil || response.Status != "ok" {
		_, _ = fmt.Fprintln(stderr, "AnyJev native bundle validation failed; no data was stored")
		return 1
	}
	var validation decision.NativeBundleValidation
	if decodeJSONBytes(response.Result, &validation) != nil {
		_, _ = fmt.Fprintln(stderr, "Python returned a malformed AnyJev bundle validation receipt")
		return 1
	}
	bundle, err := decision.NewNativeBundleArtifact(input.ID, input.Bundle, validation, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "AnyJev bundle failed binding, structure, size, or privacy validation")
		return 1
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	if err := databaseStore.ImportDecisionBundle(context.Background(), bundle, input.Actor, input.Reason); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{
		"schema_version":            "threadforge.decision-bundle-import-result.v1",
		"bundle":                    decision.SummarizeNativeBundle(bundle),
		"human_review_required":     true,
		"activation_allowed":        false,
		"execution_authorized":      false,
		"cutover_evidence_eligible": false,
	}, stderr)
}

func exportDecisionBundle(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("decision bundle export", stderr)
	id := fs.String("id", "", "local native bundle identifier")
	includeObservations := fs.Bool("include-observations", false, "explicitly allow labeled observations in exported output")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge decision bundle export --id <id> [--include-observations] [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	bundle, err := databaseStore.GetDecisionBundle(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if !*includeObservations && nativeBundleHasObservations(bundle.Content) {
		_, _ = fmt.Fprintln(stderr, "bundle contains labeled observations; repeat with --include-observations to export them")
		return 1
	}
	if !json.Valid(bundle.Content) {
		_, _ = fmt.Fprintln(stderr, "stored AnyJev native bundle content is invalid")
		return 1
	}
	if _, err := stdout.Write(append(bundle.Content, '\n')); err != nil {
		_, _ = fmt.Fprintln(stderr, "could not write exported AnyJev native bundle")
		return 1
	}
	return 0
}

func nativeBundleHasObservations(content json.RawMessage) bool {
	var value struct {
		Observations map[string]json.RawMessage `json:"observations"`
	}
	if err := json.Unmarshal(content, &value); err != nil {
		return true
	}
	return len(value.Observations) > 0
}

func invokePython(ctx context.Context, python, requestID, operation string, params json.RawMessage) (pythonProtocolResponse, error) {
	if len(params) == 0 || len(params) > maxPythonProtocolLine {
		return pythonProtocolResponse{}, errors.New("Python request is empty or exceeds the 18 MiB protocol limit")
	}
	request, err := json.Marshal(pythonProtocolRequest{
		SchemaVersion: "threadforge.python.v1", RequestID: requestID, Operation: operation, Params: params,
	})
	if err != nil || len(request) > maxPythonProtocolLine {
		return pythonProtocolResponse{}, errors.New("Python request exceeds the 18 MiB protocol limit")
	}
	command := exec.CommandContext(ctx, python, "-m", "threadforge_py")
	command.Env = pythonEnvironment()
	command.Stdin = bytes.NewReader(append(request, '\n'))
	command.Stderr = io.Discard
	output := &limitedOutput{limit: maxPythonProtocolResponse}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return pythonProtocolResponse{}, errors.New("Python process failed")
	}
	var response pythonProtocolResponse
	if err := decodeJSONBytes(output.Bytes(), &response); err != nil || response.SchemaVersion != "threadforge.python.v1" ||
		response.RequestID != requestID || response.Status != "ok" || len(response.Result) == 0 {
		return pythonProtocolResponse{}, errors.New("Python returned an invalid or unsuccessful protocol response")
	}
	return response, nil
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (output *limitedOutput) Write(data []byte) (int, error) {
	if len(data) > output.limit-output.Len() {
		return 0, errors.New("Python response exceeded its size limit")
	}
	return output.Buffer.Write(data)
}

func defaultLocalBackend() json.RawMessage {
	modelPath := strings.TrimSpace(os.Getenv("THREADFORGE_MODEL_PATH"))
	if modelPath == "" {
		return json.RawMessage(`{}`)
	}
	data, _ := json.Marshal(map[string]any{"type": "hf", "model_path": modelPath, "device": "cpu", "dtype": "float32"})
	return data
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic("known JSON value could not be marshaled: " + err.Error())
	}
	return data
}

func flagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
