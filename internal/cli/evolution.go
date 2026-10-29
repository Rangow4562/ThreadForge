package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/evolution"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

type evolutionDenyLiteral struct {
	Literal string `json:"literal"`
	Reason  string `json:"reason"`
}

type evolutionBenchmarkInventory struct {
	SourceID         string   `json:"source_id"`
	SourceRevision   string   `json:"source_revision"`
	SourceFileSHA256 string   `json:"source_file_sha256"`
	TaskIDs          []string `json:"task_ids"`
}

func validEvolutionInventoryCoverage(domain string, complete bool, verified, missing []string) bool {
	return evolution.ValidateInventoryCoverage(domain, complete, verified, missing) == nil
}

type evolutionScreenRequest struct {
	SchemaVersion        string                        `json:"schema_version"`
	Domain               string                        `json:"domain"`
	Diff                 string                        `json:"diff"`
	DeniedLiterals       []evolutionDenyLiteral        `json:"denied_literals,omitempty"`
	BenchmarkTaskIDs     []string                      `json:"benchmark_task_ids,omitempty"`
	BenchmarkInventories []evolutionBenchmarkInventory `json:"benchmark_inventories,omitempty"`
}

type evolutionHarveyInventory struct {
	SourceID         string   `json:"source_id"`
	SourceRevision   string   `json:"source_revision"`
	SourceFileSHA256 string   `json:"source_file_sha256"`
	TaskIDs          []string `json:"task_ids"`
}

type evolutionHarveyInventoryCommitment struct {
	SourceID      string `json:"source_id"`
	TaskCount     int    `json:"task_count"`
	TaskIDsSHA256 string `json:"task_ids_sha256"`
}

type evolutionHarveySplitGenerator struct {
	SourceFile       string `json:"source_file"`
	SourceFileSHA256 string `json:"source_file_sha256"`
	SourceRevision   string `json:"source_revision"`
}

type evolutionHarveyInventoryResult struct {
	SchemaVersion               string                               `json:"schema_version"`
	Status                      string                               `json:"status"`
	SplitGenerator              evolutionHarveySplitGenerator        `json:"split_generator"`
	DatasetRevision             string                               `json:"dataset_revision"`
	BenchmarkInventories        []evolutionHarveyInventory           `json:"benchmark_inventories"`
	InventoryCommitments        []evolutionHarveyInventoryCommitment `json:"inventory_commitments"`
	TrustAnchorStatus           string                               `json:"trust_anchor_status"`
	ScreeningUsable             bool                                 `json:"screening_usable"`
	BenchmarkExecutionPerformed bool                                 `json:"benchmark_execution_performed"`
	CutoverEvidenceEligible     bool                                 `json:"cutover_evidence_eligible"`
	ExecutionAuthorized         bool                                 `json:"execution_authorized"`
}

type evolutionHarveyInventoryBundle struct {
	SchemaVersion        string                     `json:"schema_version"`
	Domain               string                     `json:"domain"`
	BenchmarkInventories []evolutionHarveyInventory `json:"benchmark_inventories"`
}

type evolutionHarveyBundleWriteResult struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Domain        string `json:"domain"`
	SHA256        string `json:"sha256"`
	SourceCount   int    `json:"source_count"`
	TaskCount     int    `json:"task_count"`
}

type evolutionEngineeringInventoryBundle struct {
	SchemaVersion        string                         `json:"schema_version"`
	Domain               string                         `json:"domain"`
	BenchmarkInventories []evolutionRRSISourceInventory `json:"benchmark_inventories"`
}

type evolutionEngineeringBundleWriteResult struct {
	SchemaVersion   string `json:"schema_version"`
	Status          string `json:"status"`
	Domain          string `json:"domain"`
	SHA256          string `json:"sha256"`
	SourceCount     int    `json:"source_count"`
	TaskCount       int    `json:"task_count"`
	SplitEntryCount int    `json:"split_entry_count"`
}

type evolutionCodingInventoryBundle struct {
	SchemaVersion        string                         `json:"schema_version"`
	Domain               string                         `json:"domain"`
	BenchmarkInventories []evolutionRRSISourceInventory `json:"benchmark_inventories"`
}

type evolutionCodingBundleWriteResult struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Domain        string `json:"domain"`
	SHA256        string `json:"sha256"`
	SourceCount   int    `json:"source_count"`
	TaskCount     int    `json:"task_count"`
}

type evolutionRRSISourceInventoryCommitment struct {
	SourceID      string `json:"source_id"`
	TaskCount     int    `json:"task_count"`
	TaskIDsSHA256 string `json:"task_ids_sha256"`
}

type evolutionRRSISourceInventory struct {
	SourceID         string   `json:"source_id"`
	SourceRevision   string   `json:"source_revision"`
	SourceFileSHA256 string   `json:"source_file_sha256"`
	TaskIDs          []string `json:"task_ids"`
}

type evolutionRRSISourceInventoryResult struct {
	SchemaVersion                string                                 `json:"schema_version"`
	Status                       string                                 `json:"status"`
	SourceID                     string                                 `json:"source_id"`
	SourceRevision               string                                 `json:"source_revision"`
	SourceFile                   string                                 `json:"source_file"`
	SourceFileSHA256             string                                 `json:"source_file_sha256"`
	SourceRelationshipFile       *string                                `json:"source_relationship_file"`
	SourceRelationshipFileSHA256 *string                                `json:"source_relationship_file_sha256"`
	InventoryCommitment          evolutionRRSISourceInventoryCommitment `json:"inventory_commitment"`
	BenchmarkInventory           evolutionRRSISourceInventory           `json:"benchmark_inventory"`
	TrustAnchorStatus            string                                 `json:"trust_anchor_status"`
	ScreeningUsable              bool                                   `json:"screening_usable"`
	BenchmarkExecutionPerformed  bool                                   `json:"benchmark_execution_performed"`
	CutoverEvidenceEligible      bool                                   `json:"cutover_evidence_eligible"`
	ExecutionAuthorized          bool                                   `json:"execution_authorized"`
}

type evolutionJobBenchInventoryCommitment struct {
	SourceID      string `json:"source_id"`
	TaskCount     int    `json:"task_count"`
	TaskIDsSHA256 string `json:"task_ids_sha256"`
}

type evolutionJobBenchInventory struct {
	SourceID         string   `json:"source_id"`
	SourceRevision   string   `json:"source_revision"`
	SourceFileSHA256 string   `json:"source_file_sha256"`
	TaskIDs          []string `json:"task_ids"`
}

type evolutionJobBenchInventoryResult struct {
	SchemaVersion                string                               `json:"schema_version"`
	Status                       string                               `json:"status"`
	SourceID                     string                               `json:"source_id"`
	SourceRevision               string                               `json:"source_revision"`
	SourceFile                   string                               `json:"source_file"`
	SourceFileSHA256             string                               `json:"source_file_sha256"`
	SourceFileCount              int                                  `json:"source_file_count"`
	SourceRelationshipFile       string                               `json:"source_relationship_file"`
	SourceRelationshipFileSHA256 string                               `json:"source_relationship_file_sha256"`
	InventoryCommitment          evolutionJobBenchInventoryCommitment `json:"inventory_commitment"`
	BenchmarkInventory           evolutionJobBenchInventory           `json:"benchmark_inventory"`
	TrustAnchorStatus            string                               `json:"trust_anchor_status"`
	ScreeningUsable              bool                                 `json:"screening_usable"`
	BenchmarkExecutionPerformed  bool                                 `json:"benchmark_execution_performed"`
	CutoverEvidenceEligible      bool                                 `json:"cutover_evidence_eligible"`
	ExecutionAuthorized          bool                                 `json:"execution_authorized"`
}

type rrsiSourceInventoryPin struct {
	SourceFile         string
	SourceFileSHA256   string
	SourceRevision     string
	TaskCount          int
	TaskIDsSHA256      string
	RelationshipFile   string
	RelationshipSHA256 string
}

var rrsiSourceInventoryPins = map[string]rrsiSourceInventoryPin{
	"swe-bench/swe-bench-verified": {
		SourceFile:       "princeton-nlp/SWE-bench_Verified@c104f840cc67f8b6eec6f759ebc8b2693d585d4a/data/test-00000-of-00001.parquet",
		SourceFileSHA256: "a45b1fe4e2f0c8390b2b2938ac83e92ed5979000856808f3679c07812e9e6dcd",
		SourceRevision:   "c104f840cc67f8b6eec6f759ebc8b2693d585d4a",
		TaskCount:        500,
		TaskIDsSHA256:    "a6b0fd7c8c2969a0eef892e032250adcfa6d32362d395c246930e61b575ac9b9",
	},
	"terminal-bench/terminal-bench-2-1": {
		SourceFile:       "domains/coding/data/tb21_tasks.txt",
		SourceFileSHA256: "a90fd41919cd172b3a7cab9e18c87da6163d0e42e568da17f4f1c0cff9a74e11",
		TaskCount:        89,
		TaskIDsSHA256:    "3778b86071c74c8a342222cfff41089916adb5c7338d1afa69f42c9cae21fe3e",
	},
	"engdesign-open": {
		SourceFile:       "domains/eng/data/split_engd.json",
		SourceFileSHA256: "f9bbbd09dff4198133fa61056bcab5799e4a8ec15f32914a3bf58bb389184839",
		TaskCount:        61,
		TaskIDsSHA256:    "ec89ab9387d44e5fbe584b77636ecc7e53171a3f59b0684955fd2385c0e6b827",
	},
	"engdesign-v1": {
		SourceFile:         "domains/eng/data/split_engd.json",
		SourceFileSHA256:   "f9bbbd09dff4198133fa61056bcab5799e4a8ec15f32914a3bf58bb389184839",
		TaskCount:          61,
		TaskIDsSHA256:      "ec89ab9387d44e5fbe584b77636ecc7e53171a3f59b0684955fd2385c0e6b827",
		RelationshipFile:   "domains/eng/scripts/build_testsurfaces.sh",
		RelationshipSHA256: "a4dd639cbb094ec90b2042837359ae1fd3302d995cbe4b1f84ffed32093d73d5",
	},
	"frontier-eng": {
		SourceFile: "benchmarks/frontier-eval-task-manifest.v1",
	},
}

const (
	jobBenchPinnedRevision  = "d43a66575635da0d3f6cb9a20976dc1a60f3ffc9"
	jobBenchManifestSHA256  = "8fee9939969c1a4fa68e280ea99743305d5dcecc2bee83d0f18fd0a532cbddcd"
	jobBenchREADME_SHA256   = "6793a68d3dac7c0deccd174a85d724e614706049f7579aaac9efe86da79be291"
	jobBenchTaskIDsSHA256   = "baadaf4f2afd1fa31506f9b50e5d04078d51908435b2a5328cd0638bcf186171"
	jobBenchTaskCount       = 65
	jobBenchSourceFileCount = 566
)

const (
	harveyLabPinnedRevision    = "1da4750171bc5a534960b3d82d15ba7fd2cf653f"
	harveyRRSIPinnedRevision   = "be50316e1db05914068a973f322770ef08ed7ba1"
	harveySplitGeneratorSHA256 = "1f791a034644828b11e5d01f75a1f35046cc69824ce0e3cb88d36e2f8c87636d"
	harveyEvolveTaskIDsSHA256  = "9d55740ad2cea88e6a094721f127f8e6b7a5902b2321b3e2fdd04accc605471b"
	harveyHeldOutTaskIDsSHA256 = "249c167630271857d81b2bc8ca2854bf287c38cbe7c331657957b57d3a7d601c"
)

type evolutionSelectionRequest struct {
	SchemaVersion string          `json:"schema_version"`
	Params        json.RawMessage `json:"params"`
}

type evolutionCandidateIdentity struct {
	ID          string           `json:"id"`
	Diff        string           `json:"diff"`
	Measured    *bool            `json:"measured"`
	Score       *float64         `json:"score"`
	Cost        *float64         `json:"cost"`
	GateFailure string           `json:"gate_failure"`
	Detail      string           `json:"detail"`
	Edits       []evolution.Edit `json:"edits"`
	Guards      []string         `json:"guards"`
}

type evolutionSelectionParams struct {
	Incumbent  evolution.Evaluation         `json:"incumbent"`
	Delta      float64                      `json:"delta"`
	SStar      *float64                     `json:"s_star"`
	Candidates []evolutionCandidateIdentity `json:"candidates"`
}

type evolutionDecision struct {
	Variant    string   `json:"variant"`
	Admissible bool     `json:"admissible"`
	Reason     string   `json:"reason"`
	Score      *float64 `json:"S"`
	Cost       *float64 `json:"C"`
	DeltaScore *float64 `json:"delta_S"`
	DeltaCost  *float64 `json:"delta_C"`
	Novelty    int      `json:"novelty"`
	Guards     []string `json:"guards"`
}

type evolutionSelectionResult struct {
	SelectedID      *string             `json:"selected_id"`
	KeepIncumbent   bool                `json:"keep_incumbent"`
	Decisions       []evolutionDecision `json:"decisions"`
	SelectionConfig map[string]float64  `json:"selection_config"`
	IncumbentCounts map[string]int      `json:"incumbent_counts"`
}

type evolutionRoundSummary struct {
	ID            string  `json:"id"`
	ProjectID     string  `json:"project_id"`
	Domain        string  `json:"domain"`
	CreatedAt     string  `json:"created_at"`
	Candidates    int     `json:"candidate_count"`
	SelectedID    *string `json:"selected_id"`
	KeepIncumbent bool    `json:"keep_incumbent"`
	ReviewStatus  string  `json:"review_status"`
}

func runEvolution(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution <screen|critic|repair|select|domain-validate|domain-verify|inventory|evaluate|history|show|repair-history|repair-show>")
		return 2
	}
	switch args[0] {
	case "screen":
		return runEvolutionScreen(args[1:], stdout, stderr)
	case "domain-validate":
		return runEvolutionDomainValidate(args[1:], stdout, stderr)
	case "domain-verify":
		return runEvolutionDomainVerify(args[1:], stdout, stderr)
	case "inventory":
		if len(args) > 1 && args[1] == "harvey" {
			return runEvolutionHarveyInventory(args[2:], stdout, stderr)
		}
		if len(args) > 1 && args[1] == "coding" {
			return runEvolutionCodingInventory(args[2:], stdout, stderr)
		}
		if len(args) > 1 && args[1] == "engineering" {
			return runEvolutionEngineeringInventory(args[2:], stdout, stderr)
		}
		if len(args) > 1 && args[1] == "source" {
			return runEvolutionRRSISourceInventory(args[2:], stdout, stderr)
		}
		if len(args) > 1 && args[1] == "jobbench" {
			return runEvolutionJobBenchInventory(args[2:], stdout, stderr)
		}
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory <harvey|coding|engineering|source|jobbench> --file <inventory-request.json> [--python python]")
		return 2
	case "evaluate":
		if len(args) > 1 && args[1] == "paired" {
			return runEvolutionEvaluatePaired(args[2:], stdout, stderr)
		}
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution evaluate paired --file <paired-evaluation-request.json> [--python python]")
		return 2
	case "critic":
		return runEvolutionCritic(args[1:], stdout, stderr)
	case "repair":
		return runEvolutionRepair(args[1:], stdout, stderr)
	case "repair-history":
		return runEvolutionRepairHistory(args[1:], stdout, stderr)
	case "repair-show":
		return runEvolutionRepairShow(args[1:], stdout, stderr)
	case "select":
		return runEvolutionSelect(args[1:], stdout, stderr)
	case "history":
		return runEvolutionHistory(args[1:], stdout, stderr)
	case "show":
		return runEvolutionShow(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown evolution action %q\n", args[0])
		return 2
	}
}

type evolutionJSONRequest struct {
	SchemaVersion string          `json:"schema_version"`
	Params        json.RawMessage `json:"params"`
}

func runEvolutionDomainValidate(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution domain-validate", stderr)
	file := fs.String("file", "", "versioned RRSI domain-result evidence JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution domain-validate --file <domain-validation-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 2<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing domain-validation request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-domain-validation-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned evolution domain-validation request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) == 0 {
		_, _ = fmt.Fprintln(stderr, "invalid evolution domain-validation parameters")
		return 1
	}
	var domain string
	if raw := params["domain"]; len(raw) == 0 || decodeJSONBytes(raw, &domain) != nil || strings.TrimSpace(domain) == "" {
		_, _ = fmt.Fprintln(stderr, "evolution domain-validation domain is required")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-domain-validation", "evolution.domain.validate", request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "evolution domain-result validation failed safely")
		return 1
	}
	var result map[string]any
	if err := decodeJSONBytes(response.Result, &result); err != nil ||
		result["schema_version"] != "threadforge.rrsi-domain-validation.v1" || result["domain"] != domain ||
		(result["status"] != "validated" && result["status"] != "incomplete" && result["status"] != "privacy_blocked") ||
		result["acceptance_status"] != "not_evaluated" || result["benchmark_execution_performed"] != false ||
		result["human_approval_required"] != true || result["execution_authorized"] != false {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe domain-validation response")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionDomainVerify(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution domain-verify", stderr)
	file := fs.String("file", "", "versioned local RRSI domain artifact verification request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution domain-verify --file <domain-verification-request.v1-or-v2.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 4<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing domain-verification request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		(request.SchemaVersion != "threadforge.evolution-domain-verification-request.v1" &&
			request.SchemaVersion != "threadforge.evolution-domain-verification-request.v2") ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned evolution domain-verification request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) == 0 {
		_, _ = fmt.Fprintln(stderr, "invalid evolution domain-verification parameters")
		return 1
	}
	var domain string
	if raw := params["domain"]; len(raw) == 0 || decodeJSONBytes(raw, &domain) != nil || strings.TrimSpace(domain) == "" {
		_, _ = fmt.Fprintln(stderr, "evolution domain-verification domain is required")
		return 1
	}
	operation := "evolution.domain.verify"
	resultSchema := "threadforge.rrsi-domain-verification.v1"
	if request.SchemaVersion == "threadforge.evolution-domain-verification-request.v2" {
		operation = "evolution.domain.verify.v2"
		resultSchema = "threadforge.rrsi-domain-verification.v2"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-domain-verification", operation, request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "evolution domain artifact verification failed safely")
		return 1
	}
	var result map[string]any
	if err := decodeJSONBytes(response.Result, &result); err != nil ||
		result["schema_version"] != resultSchema || result["domain"] != domain ||
		(result["status"] != "artifacts_verified" && result["status"] != "incomplete" &&
			result["status"] != "integrity_failed" && result["status"] != "privacy_blocked") ||
		result["acceptance_status"] != "not_evaluated" || result["benchmark_execution_performed"] != false ||
		result["cutover_evidence_eligible"] != false ||
		result["human_approval_required"] != true || result["execution_authorized"] != false {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe domain-verification response")
		return 1
	}
	if request.SchemaVersion == "threadforge.evolution-domain-verification-request.v1" {
		if result["split_identity_verified"] != false {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe v1 domain-verification identity result")
			return 1
		}
	} else {
		if !validDomainVerificationV2Result(result, domain) {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe v2 source-bound identity result")
			return 1
		}
	}
	verified, hasVerified := result["artifact_hashes_verified"].(bool)
	scopeComplete, hasScope := result["required_split_scope_complete"].(bool)
	verifiedCount, hasCount := result["verified_artifact_count"].(float64)
	manifest, hasManifest := result["artifact_manifest_sha256"].(string)
	manifestBytes, manifestErr := hex.DecodeString(manifest)
	manifestValid := hasManifest && manifestErr == nil && len(manifestBytes) == sha256.Size && strings.ToLower(manifest) == manifest
	if !hasVerified || !hasScope || !hasCount || verifiedCount < 0 || verifiedCount > 384 ||
		verifiedCount != float64(int(verifiedCount)) ||
		(verified && (!manifestValid || verifiedCount == 0)) || (!verified && result["artifact_manifest_sha256"] != nil) ||
		(result["status"] == "artifacts_verified" && (!verified || !scopeComplete)) {
		_, _ = fmt.Fprintln(stderr, "Python returned inconsistent artifact-verification status")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func validDomainVerificationV2Result(result map[string]any, domain string) bool {
	requiredSources := map[string][]string{
		"rrsi-coding":             {"terminal-bench/terminal-bench-2-1", "swe-bench/swe-bench-verified"},
		"rrsi-agentic-workspace":  {"harvey-lab/evolve", "harvey-lab/held-out", "jobbench", "gdpval", "apex-agents"},
		"rrsi-engineering-design": {"engdesign-open", "engdesign-v1", "frontier-eng"},
	}
	expectedSplits := map[string][]string{
		"rrsi-coding":             {"terminal-bench/terminal-bench-2-1", "swe-bench/swe-bench-verified"},
		"rrsi-agentic-workspace":  {"harvey-lab/evolve", "harvey-lab/held-out", "jobbench", "gdpval", "apex-agents"},
		"rrsi-engineering-design": {"engdesign-open", "engdesign-v1", "frontier-eng"},
	}
	required, ok := requiredSources[domain]
	if !ok {
		return false
	}
	splitIdentity, identityOK := result["split_identity_verified"].(bool)
	taskBindingVerified, taskBindingOK := result["task_set_binding_verified"].(bool)
	independent, independentOK := result["independent_split_sets_verified"].(bool)
	artifactHashes, artifactsOK := result["artifact_hashes_verified"].(bool)
	scopeComplete, scopeOK := result["required_split_scope_complete"].(bool)
	coverage, coverageOK := result["source_inventory_coverage"].([]any)
	bindings, bindingsOK := result["task_set_bindings"].([]any)
	overlaps, overlapsOK := result["cross_split_overlaps"].([]any)
	if !identityOK || !taskBindingOK || !independentOK || !artifactsOK || !scopeOK || !coverageOK || !bindingsOK || !overlapsOK ||
		len(coverage) != len(required) || len(bindings) > len(expectedSplits[domain]) ||
		(splitIdentity && (!taskBindingVerified || !scopeComplete || !artifactHashes)) ||
		(independent && (!splitIdentity || len(overlaps) != 0)) || len(overlaps) > 32 {
		return false
	}
	covered := make(map[string]string, len(coverage))
	for _, raw := range coverage {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		sourceID, idOK := entry["source_id"].(string)
		status, statusOK := entry["status"].(string)
		if !idOK || !statusOK || (status != "verified" && status != "unavailable") ||
			!containsDomainValue(required, sourceID) || covered[sourceID] != "" {
			return false
		}
		if status == "unavailable" {
			reason, reasonOK := entry["reason"].(string)
			if !reasonOK || reason == "" {
				return false
			}
		} else if _, hasReason := entry["reason"]; hasReason {
			return false
		}
		covered[sourceID] = status
	}
	for _, sourceID := range required {
		if covered[sourceID] == "" || (splitIdentity && covered[sourceID] != "verified") {
			return false
		}
	}
	seenBindings := make(map[string]bool, len(bindings))
	for _, raw := range bindings {
		binding, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		splitID, splitOK := binding["split_id"].(string)
		sourceVerified, sourceOK := binding["source_inventory_verified"].(bool)
		taskDigest, digestOK := binding["task_set_sha256"].(string)
		baseline, baselineOK := binding["baseline_runs_bound"].(float64)
		candidate, candidateOK := binding["candidate_runs_bound"].(float64)
		if !splitOK || !sourceOK || !containsDomainValue(expectedSplits[domain], splitID) || seenBindings[splitID] ||
			!baselineOK || !candidateOK || baseline < 0 || candidate < 0 || baseline > 32 || candidate > 32 ||
			baseline != float64(int(baseline)) || candidate != float64(int(candidate)) {
			return false
		}
		seenBindings[splitID] = true
		if sourceVerified != (digestOK && validLowerHex(taskDigest, 64)) {
			return false
		}
		if taskBindingVerified && (!sourceVerified || baseline < 1 || candidate < 1) {
			return false
		}
	}
	if taskBindingVerified && len(bindings) != len(expectedSplits[domain]) {
		return false
	}
	if splitIdentity || independent {
		if len(bindings) != len(expectedSplits[domain]) {
			return false
		}
		for _, sourceID := range required {
			if covered[sourceID] != "verified" {
				return false
			}
		}
	}
	seenOverlaps := make(map[string]bool, len(overlaps))
	for _, raw := range overlaps {
		overlap, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		left, leftOK := overlap["left_split_id"].(string)
		right, rightOK := overlap["right_split_id"].(string)
		shared, sharedOK := overlap["shared_task_count"].(float64)
		pair := left + "\x00" + right
		if !leftOK || !rightOK || !sharedOK || shared < 1 || shared > 2048 || shared != float64(int(shared)) ||
			!containsDomainValue(expectedSplits[domain], left) || !containsDomainValue(expectedSplits[domain], right) ||
			left >= right || seenOverlaps[pair] {
			return false
		}
		seenOverlaps[pair] = true
	}
	if independent && len(overlaps) != 0 {
		return false
	}
	return true
}

func containsDomainValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validHarveyTaskID(value string) bool {
	if len(value) < 3 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		valid := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if index == 0 {
			if !valid {
				return false
			}
			continue
		}
		if !valid && character != '.' && character != '_' && character != ':' && character != '/' && character != '-' {
			return false
		}
	}
	return true
}

func harveyInventoryDigest(taskIDs []string) (string, bool) {
	canonicalIDs := append([]string(nil), taskIDs...)
	for _, taskID := range canonicalIDs {
		if !validHarveyTaskID(taskID) {
			return "", false
		}
	}
	sort.Slice(canonicalIDs, func(left, right int) bool {
		return strings.ToLower(canonicalIDs[left]) < strings.ToLower(canonicalIDs[right])
	})
	var canonical strings.Builder
	seen := make(map[string]struct{}, len(canonicalIDs))
	for _, taskID := range canonicalIDs {
		key := strings.ToLower(taskID)
		if _, exists := seen[key]; exists {
			return "", false
		}
		seen[key] = struct{}{}
		canonical.WriteString(taskID)
		canonical.WriteByte('\n')
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:]), true
}

func exactJSONFields(raw []byte, expected ...string) bool {
	var fields map[string]json.RawMessage
	if decodeJSONBytes(raw, &fields) != nil || len(fields) != len(expected) {
		return false
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}

func validHarveyInventoryEnvelope(raw []byte) bool {
	var fields map[string]json.RawMessage
	if decodeJSONBytes(raw, &fields) != nil || len(fields) != 11 {
		return false
	}
	for _, name := range []string{
		"schema_version", "status", "split_generator", "dataset_revision", "inventory_commitments",
		"benchmark_inventories", "trust_anchor_status", "screening_usable",
		"benchmark_execution_performed", "cutover_evidence_eligible", "execution_authorized",
	} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	for _, name := range []string{"screening_usable", "benchmark_execution_performed", "cutover_evidence_eligible", "execution_authorized"} {
		if strings.TrimSpace(string(fields[name])) != "false" {
			return false
		}
	}
	if !exactJSONFields(fields["split_generator"], "source_file", "source_file_sha256", "source_revision") {
		return false
	}
	var inventories []json.RawMessage
	if decodeJSONBytes(fields["benchmark_inventories"], &inventories) != nil || inventories == nil {
		return false
	}
	for _, inventory := range inventories {
		if !exactJSONFields(inventory, "source_id", "source_revision", "source_file_sha256", "task_ids") {
			return false
		}
	}
	var commitments []json.RawMessage
	if decodeJSONBytes(fields["inventory_commitments"], &commitments) != nil || commitments == nil {
		return false
	}
	for _, commitment := range commitments {
		if !exactJSONFields(commitment, "source_id", "task_count", "task_ids_sha256") {
			return false
		}
	}
	return true
}

func validHarveyInventoryResult(result evolutionHarveyInventoryResult) bool {
	if result.SchemaVersion != "threadforge.rrsi-harvey-inventory-candidate.v1" ||
		(result.Status != "candidate_unanchored" && result.Status != "candidate_source_verified" && result.Status != "privacy_blocked") ||
		result.BenchmarkExecutionPerformed || result.CutoverEvidenceEligible || result.ExecutionAuthorized ||
		result.DatasetRevision != harveyLabPinnedRevision ||
		result.SplitGenerator.SourceFile != "domains/workspace/split_workspace.py" ||
		result.SplitGenerator.SourceFileSHA256 != harveySplitGeneratorSHA256 ||
		result.SplitGenerator.SourceRevision != harveyRRSIPinnedRevision ||
		result.BenchmarkInventories == nil || result.InventoryCommitments == nil {
		return false
	}
	if result.Status == "privacy_blocked" {
		return result.TrustAnchorStatus == "uncommitted" && !result.ScreeningUsable &&
			len(result.BenchmarkInventories) == 0 && len(result.InventoryCommitments) == 0
	}
	if len(result.BenchmarkInventories) != 2 ||
		len(result.InventoryCommitments) != 2 {
		return false
	}
	wantCounts := map[string]int{"harvey-lab/evolve": 120, "harvey-lab/held-out": 40}
	wantDigests := map[string]string{
		"harvey-lab/evolve":   harveyEvolveTaskIDsSHA256,
		"harvey-lab/held-out": harveyHeldOutTaskIDsSHA256,
	}
	commitments := make(map[string]evolutionHarveyInventoryCommitment, len(result.InventoryCommitments))
	for _, commitment := range result.InventoryCommitments {
		if _, exists := wantCounts[commitment.SourceID]; exists {
			if _, duplicate := commitments[commitment.SourceID]; duplicate {
				return false
			}
			commitments[commitment.SourceID] = commitment
		}
	}
	allIDs := make(map[string]struct{}, 160)
	seenSources := make(map[string]struct{}, 2)
	for _, inventory := range result.BenchmarkInventories {
		count, exists := wantCounts[inventory.SourceID]
		if !exists || inventory.SourceRevision != harveyLabPinnedRevision ||
			inventory.SourceFileSHA256 != harveySplitGeneratorSHA256 || len(inventory.TaskIDs) != count {
			return false
		}
		if _, duplicate := seenSources[inventory.SourceID]; duplicate {
			return false
		}
		seenSources[inventory.SourceID] = struct{}{}
		digest, valid := harveyInventoryDigest(inventory.TaskIDs)
		commitment, hasCommitment := commitments[inventory.SourceID]
		if !valid || !hasCommitment || commitment.TaskCount != count || commitment.TaskIDsSHA256 != digest {
			return false
		}
		for _, taskID := range inventory.TaskIDs {
			key := strings.ToLower(taskID)
			if _, duplicate := allIDs[key]; duplicate {
				return false
			}
			allIDs[key] = struct{}{}
		}
	}
	if len(seenSources) != 2 || len(commitments) != 2 || len(allIDs) != 160 {
		return false
	}
	anchorsMatch := true
	for sourceID, expectedDigest := range wantDigests {
		if commitments[sourceID].TaskIDsSHA256 != expectedDigest {
			anchorsMatch = false
		}
	}
	if result.Status == "candidate_source_verified" {
		return result.TrustAnchorStatus == "committed" && result.ScreeningUsable && anchorsMatch
	}
	return result.Status == "candidate_unanchored" && result.TrustAnchorStatus == "uncommitted" &&
		!result.ScreeningUsable && !anchorsMatch
}

func writeHarveyInventoryBundle(path string, result evolutionHarveyInventoryResult) (evolutionHarveyBundleWriteResult, error) {
	if result.Status != "candidate_source_verified" || !validHarveyInventoryResult(result) {
		return evolutionHarveyBundleWriteResult{}, errors.New("inventory is not source-verified")
	}
	bundle := evolutionHarveyInventoryBundle{
		SchemaVersion:        "threadforge.rrsi-domain-inventory-bundle.v1",
		Domain:               "rrsi-agentic-workspace",
		BenchmarkInventories: result.BenchmarkInventories,
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return evolutionHarveyBundleWriteResult{}, errors.New("inventory bundle could not be encoded")
	}
	data = append(data, '\n')
	writeStatus, err := writePrivateInventoryBundle(path, data)
	if err != nil {
		return evolutionHarveyBundleWriteResult{}, err
	}
	digest := sha256.Sum256(data)
	taskCount := 0
	for _, inventory := range result.BenchmarkInventories {
		taskCount += len(inventory.TaskIDs)
	}
	return evolutionHarveyBundleWriteResult{
		SchemaVersion: "threadforge.rrsi-inventory-bundle-write.v1",
		Status:        writeStatus,
		Domain:        bundle.Domain,
		SHA256:        hex.EncodeToString(digest[:]),
		SourceCount:   len(result.BenchmarkInventories),
		TaskCount:     taskCount,
	}, nil
}

func writeEngineeringInventoryBundle(
	path string, results []evolutionRRSISourceInventoryResult,
) (evolutionEngineeringBundleWriteResult, error) {
	if len(results) != 2 {
		return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory requires both pinned splits")
	}
	bySource := make(map[string]evolutionRRSISourceInventoryResult, len(results))
	for _, result := range results {
		if result.Status != "candidate_source_verified" || !validRRSISourceInventoryResult(result) {
			return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory split is not source-verified")
		}
		if result.SourceID != "engdesign-open" && result.SourceID != "engdesign-v1" {
			return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory contains an unsupported split")
		}
		if _, duplicate := bySource[result.SourceID]; duplicate {
			return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory contains a duplicate split")
		}
		bySource[result.SourceID] = result
	}
	evolve, hasEvolve := bySource["engdesign-open"]
	heldOut, hasHeldOut := bySource["engdesign-v1"]
	if !hasEvolve || !hasHeldOut || len(evolve.BenchmarkInventory.TaskIDs) != 61 ||
		len(heldOut.BenchmarkInventory.TaskIDs) != 61 {
		return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory splits are incomplete")
	}
	ids := make(map[string]string, 61)
	for _, taskID := range evolve.BenchmarkInventory.TaskIDs {
		ids[strings.ToLower(taskID)] = taskID
	}
	if len(ids) != 61 {
		return evolutionEngineeringBundleWriteResult{}, errors.New("engineering evolve task set is invalid")
	}
	for _, taskID := range heldOut.BenchmarkInventory.TaskIDs {
		if ids[strings.ToLower(taskID)] != taskID {
			return evolutionEngineeringBundleWriteResult{}, errors.New("engineering held-out surface does not match the pinned task set")
		}
	}
	bundle := evolutionEngineeringInventoryBundle{
		SchemaVersion: "threadforge.rrsi-domain-inventory-bundle.v1",
		Domain:        "rrsi-engineering-design",
		BenchmarkInventories: []evolutionRRSISourceInventory{
			evolve.BenchmarkInventory, heldOut.BenchmarkInventory,
		},
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return evolutionEngineeringBundleWriteResult{}, errors.New("engineering inventory bundle could not be encoded")
	}
	data = append(data, '\n')
	writeStatus, err := writePrivateInventoryBundle(path, data)
	if err != nil {
		return evolutionEngineeringBundleWriteResult{}, err
	}
	digest := sha256.Sum256(data)
	return evolutionEngineeringBundleWriteResult{
		SchemaVersion:   "threadforge.rrsi-engineering-inventory-bundle-write.v1",
		Status:          writeStatus,
		Domain:          bundle.Domain,
		SHA256:          hex.EncodeToString(digest[:]),
		SourceCount:     len(bundle.BenchmarkInventories),
		TaskCount:       len(ids),
		SplitEntryCount: len(evolve.BenchmarkInventory.TaskIDs) + len(heldOut.BenchmarkInventory.TaskIDs),
	}, nil
}

func writeCodingInventoryBundle(path string, results []evolutionRRSISourceInventoryResult) (evolutionCodingBundleWriteResult, error) {
	if len(results) != 2 {
		return evolutionCodingBundleWriteResult{}, errors.New("coding inventory requires both pinned splits")
	}
	bySource := make(map[string]evolutionRRSISourceInventoryResult, len(results))
	for _, result := range results {
		if result.Status != "candidate_source_verified" || !validRRSISourceInventoryResult(result) {
			return evolutionCodingBundleWriteResult{}, errors.New("coding inventory split is not source-verified")
		}
		if result.SourceID != "terminal-bench/terminal-bench-2-1" && result.SourceID != "swe-bench/swe-bench-verified" {
			return evolutionCodingBundleWriteResult{}, errors.New("coding inventory contains an unsupported split")
		}
		if _, duplicate := bySource[result.SourceID]; duplicate {
			return evolutionCodingBundleWriteResult{}, errors.New("coding inventory contains a duplicate split")
		}
		bySource[result.SourceID] = result
	}
	terminal, hasTerminal := bySource["terminal-bench/terminal-bench-2-1"]
	swe, hasSWE := bySource["swe-bench/swe-bench-verified"]
	if !hasTerminal || !hasSWE || len(terminal.BenchmarkInventory.TaskIDs) != 89 ||
		len(swe.BenchmarkInventory.TaskIDs) != 500 {
		return evolutionCodingBundleWriteResult{}, errors.New("coding inventory splits are incomplete")
	}
	ids := make(map[string]struct{}, 589)
	for _, inventory := range []evolutionRRSISourceInventory{terminal.BenchmarkInventory, swe.BenchmarkInventory} {
		for _, taskID := range inventory.TaskIDs {
			key := strings.ToLower(taskID)
			if _, duplicate := ids[key]; duplicate {
				return evolutionCodingBundleWriteResult{}, errors.New("coding inventory source sets overlap")
			}
			ids[key] = struct{}{}
		}
	}
	if len(ids) != 589 {
		return evolutionCodingBundleWriteResult{}, errors.New("coding inventory task set is incomplete")
	}
	bundle := evolutionCodingInventoryBundle{
		SchemaVersion: "threadforge.rrsi-domain-inventory-bundle.v1",
		Domain:        "rrsi-coding",
		BenchmarkInventories: []evolutionRRSISourceInventory{
			terminal.BenchmarkInventory, swe.BenchmarkInventory,
		},
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return evolutionCodingBundleWriteResult{}, errors.New("coding inventory bundle could not be encoded")
	}
	data = append(data, '\n')
	writeStatus, err := writePrivateInventoryBundle(path, data)
	if err != nil {
		return evolutionCodingBundleWriteResult{}, err
	}
	digest := sha256.Sum256(data)
	return evolutionCodingBundleWriteResult{
		SchemaVersion: "threadforge.rrsi-coding-inventory-bundle-write.v1",
		Status:        writeStatus,
		Domain:        bundle.Domain,
		SHA256:        hex.EncodeToString(digest[:]),
		SourceCount:   len(bundle.BenchmarkInventories),
		TaskCount:     len(ids),
	}, nil
}

func writePrivateInventoryBundle(path string, data []byte) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.HasPrefix(path, `\\`) {
		return "", errors.New("bundle path must be an absolute local path")
	}
	if len(data) > 384<<10 || privacy.ContainsCredential(data) {
		return "", errors.New("inventory bundle failed local safety checks")
	}
	if err := ensureSafeDirectories(filepath.Dir(path)); err != nil {
		return "", errors.New("bundle destination contains an unsafe path")
	}
	resultStatus := "bundle_written"
	if existing, found, err := readExistingInventoryBundle(path); err != nil {
		return "", err
	} else if found {
		if !bytes.Equal(existing, data) {
			return "", errors.New("bundle destination already contains different data")
		}
		resultStatus = "bundle_already_present"
	} else {
		temporary, err := os.CreateTemp(filepath.Dir(path), ".threadforge-harvey-inventory-*.tmp")
		if err != nil {
			return "", errors.New("bundle destination could not be prepared")
		}
		temporaryPath := temporary.Name()
		defer os.Remove(temporaryPath)
		if err := temporary.Chmod(0o600); err != nil {
			_ = temporary.Close()
			return "", errors.New("bundle permissions could not be restricted")
		}
		if _, err := temporary.Write(data); err != nil {
			_ = temporary.Close()
			return "", errors.New("bundle could not be written")
		}
		if err := temporary.Sync(); err != nil {
			_ = temporary.Close()
			return "", errors.New("bundle could not be synchronized")
		}
		if err := temporary.Close(); err != nil {
			return "", errors.New("bundle could not be closed safely")
		}
		if err := os.Link(temporaryPath, path); err != nil {
			if existing, found, readErr := readExistingInventoryBundle(path); readErr == nil && found && bytes.Equal(existing, data) {
				resultStatus = "bundle_already_present"
			} else if file, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); createErr == nil {
				_, writeErr := file.Write(data)
				syncErr := file.Sync()
				closeErr := file.Close()
				if writeErr != nil || syncErr != nil || closeErr != nil {
					_ = os.Remove(path)
					return "", errors.New("bundle could not be published without replacing existing data")
				}
			} else {
				return "", errors.New("bundle could not be published without replacing existing data")
			}
		}
	}
	return resultStatus, nil
}

func readExistingInventoryBundle(path string) ([]byte, bool, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("bundle destination could not be inspected")
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > 384<<10 {
		return nil, false, errors.New("bundle destination must be a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, errors.New("bundle destination could not be read")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) || int64(len(data)) != before.Size() || len(data) > 384<<10 {
		return nil, false, errors.New("bundle destination changed while being read")
	}
	return data, true, nil
}

func validRRSISourceInventoryEnvelope(raw []byte) bool {
	var fields map[string]json.RawMessage
	if decodeJSONBytes(raw, &fields) != nil || len(fields) != 15 {
		return false
	}
	for _, name := range []string{
		"schema_version", "status", "source_id", "source_revision", "source_file",
		"source_file_sha256", "source_relationship_file", "source_relationship_file_sha256",
		"inventory_commitment", "benchmark_inventory", "trust_anchor_status", "screening_usable",
		"benchmark_execution_performed", "cutover_evidence_eligible", "execution_authorized",
	} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	for _, name := range []string{"benchmark_execution_performed", "cutover_evidence_eligible", "execution_authorized"} {
		if strings.TrimSpace(string(fields[name])) != "false" {
			return false
		}
	}
	return exactJSONFields(fields["inventory_commitment"], "source_id", "task_count", "task_ids_sha256") &&
		exactJSONFields(fields["benchmark_inventory"], "source_id", "source_revision", "source_file_sha256", "task_ids")
}

func validRRSISourceInventoryResult(result evolutionRRSISourceInventoryResult) bool {
	pin, ok := rrsiSourceInventoryPins[result.SourceID]
	expectedRevision := pin.SourceRevision
	if expectedRevision == "" {
		expectedRevision = harveyRRSIPinnedRevision
	}
	if !ok || result.SchemaVersion != "threadforge.rrsi-source-inventory-candidate.v1" ||
		(result.Status != "candidate_unanchored" && result.Status != "candidate_source_verified") ||
		result.SourceFile != pin.SourceFile || !validLowerHex(result.SourceFileSHA256, 64) ||
		!validLowerHex(result.SourceRevision, 40) ||
		result.BenchmarkExecutionPerformed || result.CutoverEvidenceEligible || result.ExecutionAuthorized ||
		result.BenchmarkInventory.SourceID != result.SourceID ||
		result.BenchmarkInventory.SourceRevision != result.SourceRevision ||
		result.BenchmarkInventory.SourceFileSHA256 != result.SourceFileSHA256 ||
		result.InventoryCommitment.SourceID != result.SourceID ||
		result.InventoryCommitment.TaskCount != len(result.BenchmarkInventory.TaskIDs) {
		return false
	}
	digest, valid := harveyInventoryDigest(result.BenchmarkInventory.TaskIDs)
	if !valid || result.InventoryCommitment.TaskIDsSHA256 != digest {
		return false
	}
	if pin.RelationshipFile == "" {
		if result.SourceRelationshipFile != nil || result.SourceRelationshipFileSHA256 != nil {
			return false
		}
	} else if result.SourceRelationshipFile == nil || *result.SourceRelationshipFile != pin.RelationshipFile ||
		result.SourceRelationshipFileSHA256 == nil || !validLowerHex(*result.SourceRelationshipFileSHA256, 64) {
		return false
	}

	if result.Status == "candidate_unanchored" {
		return result.TrustAnchorStatus == "uncommitted" && !result.ScreeningUsable
	}
	if result.TrustAnchorStatus != "committed" || !result.ScreeningUsable ||
		result.SourceRevision != expectedRevision || result.SourceFileSHA256 != pin.SourceFileSHA256 ||
		result.InventoryCommitment.TaskCount != pin.TaskCount ||
		result.InventoryCommitment.TaskIDsSHA256 != pin.TaskIDsSHA256 || len(result.BenchmarkInventory.TaskIDs) != pin.TaskCount {
		return false
	}
	return pin.RelationshipFile == "" || *result.SourceRelationshipFileSHA256 == pin.RelationshipSHA256
}

func validJobBenchInventoryEnvelope(raw []byte) bool {
	var fields map[string]json.RawMessage
	if decodeJSONBytes(raw, &fields) != nil || len(fields) != 16 {
		return false
	}
	for _, name := range []string{
		"schema_version", "status", "source_id", "source_revision", "source_file",
		"source_file_sha256", "source_file_count", "source_relationship_file",
		"source_relationship_file_sha256", "inventory_commitment", "benchmark_inventory",
		"trust_anchor_status", "screening_usable", "benchmark_execution_performed",
		"cutover_evidence_eligible", "execution_authorized",
	} {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	for _, name := range []string{"benchmark_execution_performed", "cutover_evidence_eligible", "execution_authorized"} {
		if strings.TrimSpace(string(fields[name])) != "false" {
			return false
		}
	}
	return exactJSONFields(fields["inventory_commitment"], "source_id", "task_count", "task_ids_sha256") &&
		exactJSONFields(fields["benchmark_inventory"], "source_id", "source_revision", "source_file_sha256", "task_ids")
}

func validJobBenchInventoryResult(result evolutionJobBenchInventoryResult) bool {
	if result.SchemaVersion != "threadforge.jobbench-inventory-candidate.v1" ||
		(result.Status != "candidate_unanchored" && result.Status != "candidate_source_verified") ||
		result.SourceID != "jobbench" || result.SourceRevision != jobBenchPinnedRevision ||
		result.SourceFile != "dataset/main-file-manifest.v1" ||
		!validLowerHex(result.SourceFileSHA256, 64) || result.SourceFileCount < 1 || result.SourceFileCount > 10000 ||
		result.SourceRelationshipFile != "README.md#main-task_id" ||
		!validLowerHex(result.SourceRelationshipFileSHA256, 64) ||
		result.BenchmarkExecutionPerformed || result.CutoverEvidenceEligible || result.ExecutionAuthorized ||
		result.BenchmarkInventory.SourceID != result.SourceID ||
		result.BenchmarkInventory.SourceRevision != result.SourceRevision ||
		result.BenchmarkInventory.SourceFileSHA256 != result.SourceFileSHA256 ||
		result.InventoryCommitment.SourceID != result.SourceID ||
		result.InventoryCommitment.TaskCount != len(result.BenchmarkInventory.TaskIDs) {
		return false
	}
	digest, valid := harveyInventoryDigest(result.BenchmarkInventory.TaskIDs)
	if !valid || result.InventoryCommitment.TaskIDsSHA256 != digest {
		return false
	}
	if result.Status == "candidate_unanchored" {
		return result.TrustAnchorStatus == "uncommitted" && !result.ScreeningUsable
	}
	return result.TrustAnchorStatus == "committed" && result.ScreeningUsable &&
		result.SourceFileSHA256 == jobBenchManifestSHA256 && result.SourceFileCount == jobBenchSourceFileCount &&
		result.SourceRelationshipFileSHA256 == jobBenchREADME_SHA256 &&
		result.InventoryCommitment.TaskCount == jobBenchTaskCount && len(result.BenchmarkInventory.TaskIDs) == jobBenchTaskCount &&
		result.InventoryCommitment.TaskIDsSHA256 == jobBenchTaskIDsSHA256
}

func validLowerHex(value string, characters int) bool {
	if len(value) != characters || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func runEvolutionRRSISourceInventory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution inventory source", stderr)
	file := fs.String("file", "", "versioned local RRSI source inventory derivation request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory source --file <rrsi-source-inventory-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 16<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing RRSI source inventory request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-rrsi-source-inventory-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned RRSI source inventory request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) != 2 {
		_, _ = fmt.Fprintln(stderr, "invalid RRSI source inventory parameters")
		return 1
	}
	var sourceID string
	if raw := params["source_id"]; len(raw) == 0 || decodeJSONBytes(raw, &sourceID) != nil {
		_, _ = fmt.Fprintln(stderr, "RRSI source inventory requires a supported source_id")
		return 1
	}
	if _, ok := rrsiSourceInventoryPins[sourceID]; !ok {
		_, _ = fmt.Fprintln(stderr, "RRSI source inventory source_id is not supported")
		return 1
	}
	var localSource string
	if sourceID == "swe-bench/swe-bench-verified" {
		if _, hasRRSIRoot := params["rrsi_source_root"]; hasRRSIRoot {
			_, _ = fmt.Fprintln(stderr, "SWE-bench source inventory requires its pinned local Parquet file")
			return 1
		}
		if _, hasFrontierRoot := params["frontier_source_root"]; hasFrontierRoot {
			_, _ = fmt.Fprintln(stderr, "SWE-bench source inventory requires its pinned local Parquet file")
			return 1
		}
		if raw := params["swe_bench_dataset_file"]; len(raw) == 0 || decodeJSONBytes(raw, &localSource) != nil ||
			!filepath.IsAbs(localSource) || len(localSource) > 4096 || strings.HasPrefix(localSource, `\\`) {
			_, _ = fmt.Fprintln(stderr, "SWE-bench source inventory requires an absolute local dataset file")
			return 1
		}
	} else if sourceID == "frontier-eng" {
		if _, hasRRSIRoot := params["rrsi_source_root"]; hasRRSIRoot {
			_, _ = fmt.Fprintln(stderr, "Frontier-Eng source inventory requires its local benchmark checkout")
			return 1
		}
		if raw := params["frontier_source_root"]; len(raw) == 0 || decodeJSONBytes(raw, &localSource) != nil ||
			!filepath.IsAbs(localSource) || len(localSource) > 4096 || strings.HasPrefix(localSource, `\\`) {
			_, _ = fmt.Fprintln(stderr, "Frontier-Eng source inventory requires an absolute local checkout path")
			return 1
		}
	} else {
		if _, hasDatasetFile := params["swe_bench_dataset_file"]; hasDatasetFile {
			_, _ = fmt.Fprintln(stderr, "RRSI source inventory requires a pinned source checkout")
			return 1
		}
		if _, hasFrontierRoot := params["frontier_source_root"]; hasFrontierRoot {
			_, _ = fmt.Fprintln(stderr, "RRSI source inventory requires its pinned RRSI checkout")
			return 1
		}
		if raw := params["rrsi_source_root"]; len(raw) == 0 || decodeJSONBytes(raw, &localSource) != nil ||
			!filepath.IsAbs(localSource) || len(localSource) > 4096 {
			_, _ = fmt.Fprintln(stderr, "RRSI source inventory requires an absolute local checkout path")
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-rrsi-source-inventory", "evolution.inventory.rrsi.derive", request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "RRSI source inventory derivation failed safely")
		return 1
	}
	var result evolutionRRSISourceInventoryResult
	if !validRRSISourceInventoryEnvelope(response.Result) || decodeJSONBytes(response.Result, &result) != nil ||
		!validRRSISourceInventoryResult(result) || strings.Contains(string(response.Result), localSource) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe RRSI source inventory candidate")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionCodingInventory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution inventory coding", stderr)
	file := fs.String("file", "", "versioned local coding inventory request JSON")
	bundleOut := fs.String("bundle-out", "", "write the source-verified private coding inventory bundle")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *bundleOut == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory coding --file <coding-inventory-request.json> --bundle-out <private-bundle.json> [--python python]")
		return 2
	}
	if !filepath.IsAbs(*bundleOut) || filepath.Clean(*bundleOut) != *bundleOut ||
		len(*bundleOut) > 4096 || strings.HasPrefix(*bundleOut, `\\`) {
		_, _ = fmt.Fprintln(stderr, "coding bundle output requires an absolute local path")
		return 2
	}
	input, err := readBoundedText(*file, 16<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing coding inventory request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-coding-inventory-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned coding inventory request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) != 2 {
		_, _ = fmt.Fprintln(stderr, "invalid coding inventory parameters")
		return 1
	}
	var rrsiRoot, sweFile string
	if raw := params["rrsi_source_root"]; len(raw) == 0 || decodeJSONBytes(raw, &rrsiRoot) != nil ||
		!filepath.IsAbs(rrsiRoot) || len(rrsiRoot) > 4096 {
		_, _ = fmt.Fprintln(stderr, "coding inventory requires an absolute local RRSI checkout path")
		return 1
	}
	if raw := params["swe_bench_dataset_file"]; len(raw) == 0 || decodeJSONBytes(raw, &sweFile) != nil ||
		!filepath.IsAbs(sweFile) || len(sweFile) > 4096 || strings.HasPrefix(sweFile, `\\`) {
		_, _ = fmt.Fprintln(stderr, "coding inventory requires the absolute path to the local pinned SWE-bench Parquet file")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	requests := []struct {
		sourceID string
		params   map[string]string
		path     string
	}{
		{
			sourceID: "terminal-bench/terminal-bench-2-1",
			params: map[string]string{
				"rrsi_source_root": rrsiRoot,
				"source_id":        "terminal-bench/terminal-bench-2-1",
			},
			path: rrsiRoot,
		},
		{
			sourceID: "swe-bench/swe-bench-verified",
			params: map[string]string{
				"swe_bench_dataset_file": sweFile,
				"source_id":              "swe-bench/swe-bench-verified",
			},
			path: sweFile,
		},
	}
	results := make([]evolutionRRSISourceInventoryResult, 0, len(requests))
	for _, item := range requests {
		encoded, err := json.Marshal(item.params)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "coding inventory request could not be encoded")
			return 1
		}
		response, err := invokePython(ctx, *python, "evolution-coding-source-inventory",
			"evolution.inventory.rrsi.derive", encoded)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "coding inventory source derivation failed safely; no bundle was written")
			return 1
		}
		var result evolutionRRSISourceInventoryResult
		if !validRRSISourceInventoryEnvelope(response.Result) || decodeJSONBytes(response.Result, &result) != nil ||
			!validRRSISourceInventoryResult(result) || result.SourceID != item.sourceID ||
			result.Status != "candidate_source_verified" || strings.Contains(string(response.Result), item.path) {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe coding inventory candidate; no bundle was written")
			return 1
		}
		results = append(results, result)
	}
	bundleResult, err := writeCodingInventoryBundle(*bundleOut, results)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "verified coding inventory bundle could not be written safely")
		return 1
	}
	return writeIndentedJSON(stdout, bundleResult, stderr)
}

func runEvolutionEngineeringInventory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution inventory engineering", stderr)
	file := fs.String("file", "", "versioned local RRSI engineering inventory request JSON")
	bundleOut := fs.String("bundle-out", "", "write the source-verified private engineering inventory bundle")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *bundleOut == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory engineering --file <engineering-inventory-request.json> --bundle-out <private-bundle.json> [--python python]")
		return 2
	}
	if !filepath.IsAbs(*bundleOut) || filepath.Clean(*bundleOut) != *bundleOut ||
		len(*bundleOut) > 4096 || strings.HasPrefix(*bundleOut, `\\`) {
		_, _ = fmt.Fprintln(stderr, "engineering bundle output requires an absolute local path")
		return 2
	}
	input, err := readBoundedText(*file, 16<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing engineering inventory request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-engineering-inventory-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned engineering inventory request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) != 1 {
		_, _ = fmt.Fprintln(stderr, "invalid engineering inventory parameters")
		return 1
	}
	var root string
	if raw := params["rrsi_source_root"]; len(raw) == 0 || decodeJSONBytes(raw, &root) != nil ||
		!filepath.IsAbs(root) || len(root) > 4096 {
		_, _ = fmt.Fprintln(stderr, "engineering inventory requires an absolute local RRSI checkout path")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	results := make([]evolutionRRSISourceInventoryResult, 0, 2)
	for _, sourceID := range []string{"engdesign-open", "engdesign-v1"} {
		sourceParams, err := json.Marshal(map[string]string{
			"rrsi_source_root": root,
			"source_id":        sourceID,
		})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "engineering inventory request could not be encoded")
			return 1
		}
		response, err := invokePython(ctx, *python, "evolution-engineering-source-inventory",
			"evolution.inventory.rrsi.derive", sourceParams)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "engineering source derivation failed safely; no bundle was written")
			return 1
		}
		var result evolutionRRSISourceInventoryResult
		if !validRRSISourceInventoryEnvelope(response.Result) || decodeJSONBytes(response.Result, &result) != nil ||
			!validRRSISourceInventoryResult(result) || strings.Contains(string(response.Result), root) {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe engineering inventory candidate")
			return 1
		}
		if result.Status != "candidate_source_verified" {
			_, _ = fmt.Fprintln(stderr, "engineering source split did not match its committed anchors; no bundle was written")
			return 1
		}
		results = append(results, result)
	}
	bundleResult, err := writeEngineeringInventoryBundle(*bundleOut, results)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "verified engineering inventory bundle could not be written safely")
		return 1
	}
	return writeIndentedJSON(stdout, bundleResult, stderr)
}

func runEvolutionJobBenchInventory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution inventory jobbench", stderr)
	file := fs.String("file", "", "versioned local JobBench inventory derivation request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory jobbench --file <jobbench-inventory-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 16<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing JobBench inventory request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-jobbench-inventory-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned JobBench inventory request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) != 1 {
		_, _ = fmt.Fprintln(stderr, "invalid JobBench inventory parameters")
		return 1
	}
	var root string
	if raw := params["jobbench_source_root"]; len(raw) == 0 || decodeJSONBytes(raw, &root) != nil ||
		!filepath.IsAbs(root) || len(root) > 4096 {
		_, _ = fmt.Fprintln(stderr, "JobBench inventory requires an absolute local checkout path")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-jobbench-inventory", "evolution.inventory.jobbench.derive", request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "JobBench inventory derivation failed safely")
		return 1
	}
	var result evolutionJobBenchInventoryResult
	if !validJobBenchInventoryEnvelope(response.Result) || decodeJSONBytes(response.Result, &result) != nil ||
		!validJobBenchInventoryResult(result) || strings.Contains(string(response.Result), root) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe JobBench inventory candidate")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionHarveyInventory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution inventory harvey", stderr)
	file := fs.String("file", "", "versioned local Harvey LAB inventory derivation request JSON")
	bundleOut := fs.String("bundle-out", "", "write a source-verified private workspace inventory bundle to this local path")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution inventory harvey --file <harvey-inventory-request.json> [--bundle-out <private-bundle.json>] [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 16<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing Harvey inventory request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		request.SchemaVersion != "threadforge.evolution-harvey-inventory-request.v1" ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned Harvey inventory request")
		return 1
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) != 1 {
		_, _ = fmt.Fprintln(stderr, "invalid Harvey inventory parameters")
		return 1
	}
	var root string
	if raw := params["harvey_lab_root"]; len(raw) == 0 || decodeJSONBytes(raw, &root) != nil ||
		!filepath.IsAbs(root) || len(root) > 4096 {
		_, _ = fmt.Fprintln(stderr, "Harvey inventory requires an absolute local checkout path")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-harvey-inventory", "evolution.inventory.harvey.derive", request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "Harvey inventory derivation failed safely")
		return 1
	}
	var result evolutionHarveyInventoryResult
	if !validHarveyInventoryEnvelope(response.Result) || decodeJSONBytes(response.Result, &result) != nil ||
		!validHarveyInventoryResult(result) ||
		strings.Contains(string(response.Result), root) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe Harvey inventory candidate")
		return 1
	}
	if *bundleOut != "" {
		if result.Status != "candidate_source_verified" {
			_, _ = fmt.Fprintln(stderr, "Harvey inventory did not match the committed source anchors; no bundle was written")
			return 1
		}
		bundleResult, err := writeHarveyInventoryBundle(*bundleOut, result)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "verified Harvey inventory bundle could not be written safely")
			return 1
		}
		return writeIndentedJSON(stdout, bundleResult, stderr)
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionEvaluatePaired(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution evaluate paired", stderr)
	file := fs.String("file", "", "versioned paired held-out result comparison JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution evaluate paired --file <paired-evaluation-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 18<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if privacy.ContainsCredential([]byte(input)) {
		_, _ = fmt.Fprintln(stderr, "credential-bearing paired-evaluation request rejected")
		return 1
	}
	var request evolutionJSONRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		(request.SchemaVersion != "threadforge.evolution-paired-evaluation-request.v1" &&
			request.SchemaVersion != "threadforge.evolution-paired-evaluation-request.v2") ||
		len(request.Params) == 0 || string(request.Params) == "null" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned paired-evaluation request")
		return 1
	}
	operation := "evolution.evaluate.paired"
	responseSchema := "threadforge.evolution-paired-evaluation.v1"
	if request.SchemaVersion == "threadforge.evolution-paired-evaluation-request.v2" {
		operation = "evolution.evaluate.paired.v2"
		responseSchema = "threadforge.evolution-paired-evaluation.v2"
	}
	var params map[string]json.RawMessage
	if err := decodeJSONBytes(request.Params, &params); err != nil || len(params) == 0 {
		_, _ = fmt.Fprintln(stderr, "invalid paired-evaluation parameters")
		return 1
	}
	var splitRole string
	if raw := params["split_role"]; len(raw) == 0 || decodeJSONBytes(raw, &splitRole) != nil || splitRole != "held_out" {
		_, _ = fmt.Fprintln(stderr, "paired evaluation requires split_role held_out")
		return 1
	}
	timeout := 60 * time.Second
	if operation == "evolution.evaluate.paired.v2" {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-paired-evaluation", operation, request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "paired held-out result comparison failed safely")
		return 1
	}
	var result map[string]any
	if err := decodeJSONBytes(response.Result, &result); err != nil ||
		result["schema_version"] != responseSchema ||
		(result["status"] != "compared" && result["status"] != "privacy_blocked") ||
		(result["metric_gate_status"] != "passed" && result["metric_gate_status"] != "failed" && result["metric_gate_status"] != "privacy_blocked") ||
		result["acceptance_status"] != "not_evaluated" || result["cutover_evidence_eligible"] != false ||
		result["benchmark_execution_performed"] != false ||
		result["human_approval_required"] != true || result["execution_authorized"] != false {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid or unsafe paired-evaluation response")
		return 1
	}
	if operation == "evolution.evaluate.paired.v2" && result["status"] == "compared" {
		evidence, evidenceOK := result["evidence"].(map[string]any)
		statistics, statisticsOK := result["statistics"].(map[string]any)
		repeated, repeatedOK := statistics["repeated_failures"].(map[string]any)
		calibration, calibrationOK := repeated["noise_calibration"].(map[string]any)
		baselineCount, baselineCountOK := evidence["baseline_run_count"].(float64)
		candidateCount, candidateCountOK := evidence["candidate_run_count"].(float64)
		resamples, resamplesOK := statistics["bootstrap_resamples"].(float64)
		calibrationCount, calibrationCountOK := calibration["calibration_sample_count"].(float64)
		if !evidenceOK || !statisticsOK || !repeatedOK || !calibrationOK ||
			!baselineCountOK || baselineCount != 8 || !candidateCountOK || candidateCount < 4 || candidateCount > 8 ||
			statistics["run_level_uncertainty_included"] != true ||
			statistics["resampling_unit"] != "paired_task_id_and_independent_run_resampling" ||
			!resamplesOK || !calibrationCountOK || calibrationCount != resamples ||
			calibration["method"] != "paired_baseline_run_bootstrap_null" ||
			evidence["artifact_hashes_verified"] != false {
			_, _ = fmt.Fprintln(stderr, "Python returned an invalid v2 run-aware paired-evaluation response")
			return 1
		}
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionScreen(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution screen", stderr)
	file := fs.String("file", "", "versioned deterministic pre-screen request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution screen --file <screen-request.json> [--python python]")
		return 2
	}
	input, err := readBoundedText(*file, 512<<10)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var request evolutionScreenRequest
	if err := decodeJSONBytes([]byte(input), &request); err != nil ||
		(request.SchemaVersion != "threadforge.evolution-screen-request.v1" && request.SchemaVersion != "threadforge.evolution-screen-request.v2") ||
		strings.TrimSpace(request.Domain) == "" || strings.TrimSpace(request.Diff) == "" {
		_, _ = fmt.Fprintln(stderr, "invalid versioned evolution screen request")
		return 1
	}
	if request.DeniedLiterals == nil {
		request.DeniedLiterals = []evolutionDenyLiteral{}
	}
	if request.BenchmarkTaskIDs == nil {
		request.BenchmarkTaskIDs = []string{}
	}
	params := map[string]any{
		"domain": request.Domain, "diff": request.Diff, "denied_literals": request.DeniedLiterals,
		"benchmark_task_ids": request.BenchmarkTaskIDs,
	}
	operation := "evolution.screen"
	resultSchema := "threadforge.evolution-screen.v1"
	if request.SchemaVersion == "threadforge.evolution-screen-request.v2" {
		operation = "evolution.screen.v2"
		resultSchema = "threadforge.evolution-screen.v2"
		if request.BenchmarkInventories == nil {
			request.BenchmarkInventories = []evolutionBenchmarkInventory{}
		}
		params["benchmark_inventories"] = request.BenchmarkInventories
	} else if request.BenchmarkInventories != nil {
		_, _ = fmt.Fprintln(stderr, "benchmark inventories require evolution screen request v2")
		return 1
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not encode evolution screen request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-screen", operation, paramsJSON)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "evolution pre-screen failed safely")
		return 1
	}
	var result map[string]any
	if err := decodeJSONBytes(response.Result, &result); err != nil || result["schema_version"] != resultSchema || result["action_ready"] != false {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid evolution screen response")
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func runEvolutionSelect(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution select", stderr)
	file := fs.String("file", "", "versioned RRSI selection request JSON")
	python := fs.String("python", defaultPythonCommand(), "Python executable for the versioned JSON-stdio module")
	recordID := fs.String("record-id", "", "persist this selection as an append-only round with this stable ID")
	projectID := fs.String("project-id", "", "local Threadforge project ID required with --record-id")
	domain := fs.String("domain", "", "RRSI domain required with --record-id")
	actor := fs.String("actor", "", "local audit actor required with --record-id")
	reason := fs.String("reason", "", "audit reason required with --record-id")
	database := fs.String("db", "", "Threadforge SQLite database path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution select --file <selection-request.json> [--python python]")
		return 2
	}
	var request evolutionSelectionRequest
	if err := decodeJSONFile(*file, 4<<20, &request); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if request.SchemaVersion != "threadforge.evolution-selection-request.v1" ||
		len(request.Params) == 0 || len(request.Params) > 3<<20 || !json.Valid(request.Params) ||
		len(bytes.TrimSpace(request.Params)) == 0 || bytes.TrimSpace(request.Params)[0] != '{' ||
		privacy.ContainsCredential(request.Params) {
		_, _ = fmt.Fprintln(stderr, "invalid or credential-bearing versioned evolution selection request")
		return 1
	}
	var params evolutionSelectionParams
	if err := json.Unmarshal(request.Params, &params); err != nil || len(params.Candidates) == 0 || len(params.Candidates) > 128 {
		_, _ = fmt.Fprintln(stderr, "evolution selection candidates must contain 1-128 entries")
		return 1
	}
	candidateIDs := make(map[string]bool, len(params.Candidates))
	for _, candidate := range params.Candidates {
		if strings.TrimSpace(candidate.ID) == "" || candidateIDs[candidate.ID] {
			_, _ = fmt.Fprintln(stderr, "evolution selection candidate IDs must be non-empty and unique")
			return 1
		}
		candidateIDs[candidate.ID] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := invokePython(ctx, *python, "evolution-select", "evolution.select", request.Params)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "RRSI candidate selection failed safely")
		return 1
	}
	var result evolutionSelectionResult
	if err := decodeJSONBytes(response.Result, &result); err != nil || len(result.Decisions) != len(params.Candidates) || !validEvolutionSelectionMetadata(result) {
		_, _ = fmt.Fprintln(stderr, "Python returned an invalid RRSI selection response")
		return 1
	}
	seenDecisions := make(map[string]bool, len(result.Decisions))
	decisionByID := make(map[string]evolutionDecision, len(result.Decisions))
	for _, decision := range result.Decisions {
		if !candidateIDs[decision.Variant] || seenDecisions[decision.Variant] {
			_, _ = fmt.Fprintln(stderr, "Python returned a decision for an unknown or duplicate candidate")
			return 1
		}
		seenDecisions[decision.Variant] = true
		decisionByID[decision.Variant] = decision
	}
	bestID := ""
	bestScore := -1.0
	for _, candidate := range params.Candidates {
		decision := decisionByID[candidate.ID]
		if !decision.Admissible {
			continue
		}
		if decision.Score == nil || decision.Cost == nil {
			_, _ = fmt.Fprintln(stderr, "Python marked a candidate admissible without measured metrics")
			return 1
		}
		if bestID == "" || *decision.Score > bestScore {
			bestID, bestScore = candidate.ID, *decision.Score
		}
	}
	if result.SelectedID == nil {
		if !result.KeepIncumbent || bestID != "" {
			_, _ = fmt.Fprintln(stderr, "Python selection omitted a winner without retaining the incumbent")
			return 1
		}
	} else if result.KeepIncumbent || !candidateIDs[*result.SelectedID] || bestID != *result.SelectedID {
		_, _ = fmt.Fprintln(stderr, "Python selection returned a non-admissible or unknown winner")
		return 1
	}
	responseJSON := map[string]any{
		"schema_version": "threadforge.evolution-selection.v1",
		"status":         map[bool]string{true: "incumbent_retained", false: "selected"}[result.KeepIncumbent],
		"selected_id":    result.SelectedID, "keep_incumbent": result.KeepIncumbent,
		"decisions": result.Decisions, "selection_config": result.SelectionConfig,
		"incumbent_counts": result.IncumbentCounts, "requires_human_approval": true,
		"execution_authorized": false,
	}
	recording := strings.TrimSpace(*recordID) != ""
	if !recording && (strings.TrimSpace(*projectID) != "" || strings.TrimSpace(*domain) != "" || strings.TrimSpace(*actor) != "" || strings.TrimSpace(*reason) != "") {
		_, _ = fmt.Fprintln(stderr, "--project-id, --domain, --actor, and --reason require --record-id")
		return 2
	}
	if recording {
		if strings.TrimSpace(*projectID) == "" || strings.TrimSpace(*domain) == "" || strings.TrimSpace(*actor) == "" || strings.TrimSpace(*reason) == "" {
			_, _ = fmt.Fprintln(stderr, "--record-id requires --project-id, --domain, --actor, and --reason")
			return 2
		}
		round, err := evolutionRoundFromSelection(*recordID, *projectID, *domain, params, result)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "could not validate evolution history record")
			return 1
		}
		paths, err := appdata.Resolve(*database, "")
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
			return 1
		}
		databaseStore, err := store.Open(paths.SQLite)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
			return 1
		}
		defer databaseStore.Close()
		if err := databaseStore.RecordEvolutionRound(context.Background(), round, *actor, *reason); err != nil {
			_, _ = fmt.Fprintf(stderr, "could not record evolution history: %v\n", err)
			return 1
		}
		responseJSON["recorded_round_id"] = round.ID
		responseJSON["review_status"] = round.ReviewStatus
	}
	return writeIndentedJSON(stdout, responseJSON, stderr)
}

func evolutionRoundFromSelection(id, projectID, domain string, params evolutionSelectionParams, result evolutionSelectionResult) (evolution.Round, error) {
	star := params.Incumbent.Score
	if params.SStar != nil {
		star = *params.SStar
	}
	decisionByID := make(map[string]evolutionDecision, len(result.Decisions))
	for _, decision := range result.Decisions {
		decisionByID[decision.Variant] = decision
	}
	round := evolution.Round{
		SchemaVersion: evolution.RoundSchemaVersion,
		ID:            id, ProjectID: projectID, Domain: domain,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Incumbent: params.Incumbent, SStar: star, Delta: params.Delta,
		SelectionConfig: result.SelectionConfig, IncumbentCounts: result.IncumbentCounts,
		SelectedID: result.SelectedID, KeepIncumbent: result.KeepIncumbent,
		ReviewStatus: "pending_human_review", ExecutionAuthorized: false,
		Candidates: make([]evolution.Candidate, 0, len(params.Candidates)),
	}
	for _, input := range params.Candidates {
		decision, ok := decisionByID[input.ID]
		if !ok {
			return evolution.Round{}, fmt.Errorf("decision missing for candidate")
		}
		measured := input.Measured == nil || *input.Measured
		candidate := evolution.Candidate{
			ID: input.ID, Diff: input.Diff, Measured: measured, GateFailure: input.GateFailure,
			Detail: input.Detail, Edits: input.Edits,
			Decision: evolution.CandidateDecision{
				Variant: decision.Variant, Admissible: decision.Admissible, Reason: decision.Reason,
				Score: decision.Score, Cost: decision.Cost, DeltaScore: decision.DeltaScore,
				DeltaCost: decision.DeltaCost, Novelty: decision.Novelty, Guards: decision.Guards,
			},
		}
		if candidate.Edits == nil {
			candidate.Edits = []evolution.Edit{}
		}
		if len(input.Guards) != 0 && strings.Join(input.Guards, "\x00") != strings.Join(decision.Guards, "\x00") {
			return evolution.Round{}, fmt.Errorf("candidate guard evidence differs from selection decision")
		}
		if measured {
			if input.Score == nil || input.Cost == nil {
				return evolution.Round{}, fmt.Errorf("measured candidate evaluation missing")
			}
			candidate.Evaluation = &evolution.Evaluation{Score: *input.Score, Cost: *input.Cost}
		}
		candidate.Outcome = "rejected"
		if !measured {
			candidate.Outcome = "unmeasured"
		} else if decision.Admissible {
			candidate.Outcome = "lost"
		}
		if result.SelectedID != nil && input.ID == *result.SelectedID {
			candidate.Outcome = "selected_pending_review"
		}
		for index := range candidate.Edits {
			if candidate.Edits[index].ID == "" {
				candidate.Edits[index].ID = fmt.Sprintf("C%d", index+1)
			}
			if candidate.Edits[index].Hypothesis == "" {
				candidate.Edits[index].Hypothesis = candidate.Edits[index].Mechanism
			}
		}
		round.Candidates = append(round.Candidates, candidate)
	}
	if err := evolution.ValidateRound(round); err != nil {
		return evolution.Round{}, err
	}
	return round, nil
}

func validEvolutionSelectionMetadata(result evolutionSelectionResult) bool {
	want := []string{"beta0", "beta1", "w_s", "w_c", "w_n"}
	if len(result.SelectionConfig) != len(want) || result.IncumbentCounts == nil {
		return false
	}
	for _, key := range want {
		value, ok := result.SelectionConfig[key]
		if !ok || value < 0 {
			return false
		}
	}
	allowedComponents := map[string]bool{
		"prompt": true, "control_flow": true, "config": true, "output_plumbing": true,
		"context_mgmt": true, "client_tool": true, "skill": true, "memory": true, "subagent": true,
	}
	for component, count := range result.IncumbentCounts {
		if !allowedComponents[component] || count < 0 {
			return false
		}
	}
	return true
}

func runEvolutionHistory(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution history", stderr)
	projectID := fs.String("project-id", "", "local Threadforge project ID")
	database := fs.String("db", "", "Threadforge SQLite database path")
	limit := fs.Int("limit", 50, "maximum rounds to return (1-200)")
	offset := fs.Int("offset", 0, "round offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *projectID == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution history --project-id <id> [--limit 50] [--offset 0] [--db <path>]")
		return 2
	}
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
		return 1
	}
	databaseStore, err := store.Open(paths.SQLite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
		return 1
	}
	defer databaseStore.Close()
	rounds, total, err := databaseStore.ListEvolutionRounds(context.Background(), *projectID, *limit, *offset)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	items := make([]evolutionRoundSummary, 0, len(rounds))
	for _, round := range rounds {
		items = append(items, evolutionRoundSummary{
			ID: round.ID, ProjectID: round.ProjectID, Domain: round.Domain,
			CreatedAt: round.CreatedAt, Candidates: len(round.Candidates),
			SelectedID: round.SelectedID, KeepIncumbent: round.KeepIncumbent,
			ReviewStatus: round.ReviewStatus,
		})
	}
	return writeIndentedJSON(stdout, map[string]any{"rounds": items, "total": total, "limit": *limit, "offset": *offset}, stderr)
}

func runEvolutionShow(args []string, stdout, stderr io.Writer) int {
	fs := flagSet("evolution show", stderr)
	id := fs.String("id", "", "evolution round ID")
	database := fs.String("db", "", "Threadforge SQLite database path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *id == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge evolution show --id <round-id> [--db <path>]")
		return 2
	}
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not resolve local Threadforge database")
		return 1
	}
	databaseStore, err := store.Open(paths.SQLite)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "could not open local Threadforge database")
		return 1
	}
	defer databaseStore.Close()
	round, err := databaseStore.GetEvolutionRound(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	audit, err := databaseStore.EvolutionRoundHistory(context.Background(), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{"round": round, "audit": audit}, stderr)
}
