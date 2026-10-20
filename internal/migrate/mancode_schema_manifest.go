package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const mancodeSchemaManifestImportSchema = "threadforge.mancode-schema-manifest.v1"

var mancodeManifestVersionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?$`)
var mancodeManifestDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

var mancodeManagedAdapterNames = []string{
	"claude-code", "codex", "cursor", "copilot", "zcode", "kimi-code", "qoder", "dsh",
}

var mancodeActivationStates = map[string]bool{
	"initializing": true, "dual_read": true, "activating": true,
	"v3_active": true, "repair_required": true,
}

type mancodeSchemaManifestImport struct {
	SchemaVersion                 string   `json:"schema_version"`
	Authority                     string   `json:"authority"`
	HumanReviewRequired           bool     `json:"human_review_required"`
	ExecutionAuthorized           bool     `json:"execution_authorized"`
	SourceManifestVersion         int64    `json:"source_manifest_version"`
	LayoutVersion                 int64    `json:"layout_version"`
	SourceActivationState         string   `json:"source_activation_state"`
	SourceActivationIsAuthority   bool     `json:"source_activation_is_authority"`
	MinReaderVersion              string   `json:"min_reader_version"`
	MinWriterVersion              string   `json:"min_writer_version"`
	ActivatedAt                   *string  `json:"activated_at"`
	LegacyBaselinePresent         bool     `json:"legacy_baseline_present"`
	ManagedAdapters               []string `json:"managed_adapters"`
	SourcePlanningPolicy          *int64   `json:"source_planning_policy"`
	PrivacyPolicyReferencePresent bool     `json:"privacy_policy_reference_present"`
	PrivacyPolicyRevision         *int64   `json:"privacy_policy_revision"`
	PrivacyPolicyDigestOmitted    bool     `json:"privacy_policy_digest_omitted"`
	SourceEpochOmitted            bool     `json:"source_epoch_omitted"`
	SourceOperationIDOmitted      bool     `json:"source_operation_id_omitted"`
}

func normalizeMancodeSchemaManifest(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode schema manifest must be an object")
	}
	manifestVersion, versionOK := mancodeIntegerValue(object["manifestVersion"])
	if !versionOK || manifestVersion < 1 || manifestVersion > 3 {
		return nil, true, errors.New("Mancode schema manifest version is unsupported")
	}
	keys := []string{
		"manifestVersion", "layoutVersion", "epoch", "activationState", "minReaderVersion",
		"minWriterVersion", "activatedAt", "legacyBaseline", "managedAdapters", "lastOperationId",
	}
	if manifestVersion >= 2 {
		keys = append(keys, "workflowPolicyDefaults")
	}
	if manifestVersion == 3 {
		keys = append(keys, "privacyPolicy")
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode schema manifest does not match its versioned schema")
	}
	layoutVersion, layoutOK := mancodeIntegerValue(object["layoutVersion"])
	epoch := mancodeString(object["epoch"])
	activationState := mancodeString(object["activationState"])
	minReaderVersion := mancodeString(object["minReaderVersion"])
	minWriterVersion := mancodeString(object["minWriterVersion"])
	if !layoutOK || layoutVersion != 3 || !mancodeOperationIDPattern.MatchString(epoch) ||
		!mancodeActivationStates[activationState] || !mancodeManifestVersionValid(minReaderVersion) ||
		!mancodeManifestVersionValid(minWriterVersion) {
		return nil, true, errors.New("Mancode schema manifest identity or version fields are invalid")
	}
	if manifestVersion >= 2 {
		floor := "0.4.0"
		if manifestVersion == 3 {
			floor = "0.6.5"
		}
		if !mancodeManifestVersionAtLeast(minReaderVersion, floor) || !mancodeManifestVersionAtLeast(minWriterVersion, floor) {
			return nil, true, errors.New("Mancode schema manifest reader/writer minimum is too old")
		}
	}
	var activatedAt *string
	if object["activatedAt"] != nil {
		text, ok := object["activatedAt"].(string)
		if !ok || !mancodeRFC3339(text) {
			return nil, true, errors.New("Mancode schema manifest activation timestamp is invalid")
		}
		activatedAt = &text
	}
	baselinePresent, err := validateMancodeManifestLegacyBaseline(object["legacyBaseline"])
	if err != nil {
		return nil, true, err
	}
	if (activationState == "initializing" && baselinePresent) ||
		((activationState == "dual_read" || activationState == "activating") && !baselinePresent) ||
		(activationState == "v3_active" && activatedAt == nil) ||
		((activationState == "initializing" || activationState == "dual_read" || activationState == "activating") && activatedAt != nil) {
		return nil, true, errors.New("Mancode schema manifest activation state is inconsistent")
	}
	adapters, err := validateMancodeManifestAdapters(object["managedAdapters"])
	if err != nil {
		return nil, true, err
	}
	if !mancodeNullableULID(object["lastOperationId"]) {
		return nil, true, errors.New("Mancode schema manifest operation ID is invalid")
	}
	var planning *int64
	if manifestVersion >= 2 {
		defaults, ok := object["workflowPolicyDefaults"].(map[string]any)
		if !ok || !mancodeHasAllKeys(defaults, []string{"planning"}) || !mancodeHasOnlyKeys(defaults, []string{"planning"}) {
			return nil, true, errors.New("Mancode schema manifest workflow defaults are invalid")
		}
		value, valid := mancodeIntegerValue(defaults["planning"])
		if !valid || (manifestVersion == 2 && value != 2) ||
			(manifestVersion == 3 && value != 1 && value != 2) {
			return nil, true, errors.New("Mancode schema manifest planning policy is unsupported")
		}
		planning = &value
	}
	var privacyRevision *int64
	privacyReferencePresent := manifestVersion == 3
	if privacyReferencePresent {
		reference, ok := object["privacyPolicy"].(map[string]any)
		if !ok || !mancodeHasAllKeys(reference, []string{"revision", "digest"}) || !mancodeHasOnlyKeys(reference, []string{"revision", "digest"}) {
			return nil, true, errors.New("Mancode schema manifest privacy reference is invalid")
		}
		revision, valid := mancodeIntegerValue(reference["revision"])
		digest := mancodeString(reference["digest"])
		if !valid || revision < 1 || !mancodeManifestDigestPattern.MatchString(digest) {
			return nil, true, errors.New("Mancode schema manifest privacy reference revision or digest is invalid")
		}
		privacyRevision = &revision
	}
	payload, err := json.Marshal(mancodeSchemaManifestImport{
		SchemaVersion: mancodeSchemaManifestImportSchema,
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		SourceManifestVersion: manifestVersion, LayoutVersion: layoutVersion,
		SourceActivationState: activationState, SourceActivationIsAuthority: false,
		MinReaderVersion: minReaderVersion, MinWriterVersion: minWriterVersion,
		ActivatedAt: activatedAt, LegacyBaselinePresent: baselinePresent, ManagedAdapters: adapters,
		SourcePlanningPolicy: planning, PrivacyPolicyReferencePresent: privacyReferencePresent,
		PrivacyPolicyRevision: privacyRevision, PrivacyPolicyDigestOmitted: privacyReferencePresent,
		SourceEpochOmitted: true, SourceOperationIDOmitted: true,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode schema-manifest projection: %w", err)
	}
	return []normalizedItem{{Type: "mancode_schema_manifest", Payload: payload}}, true, nil
}

func validateMancodeManifestLegacyBaseline(value any) (bool, error) {
	if value == nil {
		return false, nil
	}
	baseline, ok := value.(map[string]any)
	keys := []string{"stateDigest", "workflowIndexDigest"}
	if !ok || !mancodeHasAllKeys(baseline, keys) || !mancodeHasOnlyKeys(baseline, keys) ||
		!mancodeManifestDigestPattern.MatchString(mancodeString(baseline["stateDigest"])) ||
		!mancodeManifestDigestPattern.MatchString(mancodeString(baseline["workflowIndexDigest"])) {
		return false, errors.New("Mancode schema manifest legacy baseline is invalid")
	}
	return true, nil
}

func validateMancodeManifestAdapters(value any) ([]string, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("Mancode schema manifest managed adapters must be an object")
	}
	allowed := make(map[string]bool, len(mancodeManagedAdapterNames))
	for _, name := range mancodeManagedAdapterNames {
		allowed[name] = true
	}
	for name, rawVersion := range object {
		version, ok := rawVersion.(string)
		if !allowed[name] || !ok || strings.TrimSpace(version) == "" {
			return nil, errors.New("Mancode schema manifest managed adapter is invalid")
		}
	}
	adapters := make([]string, 0, len(object))
	for name := range object {
		adapters = append(adapters, name)
	}
	sort.Strings(adapters)
	return adapters, nil
}

func mancodeManifestVersionValid(value string) bool {
	match := mancodeManifestVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	for _, component := range match[1:4] {
		parsed, err := strconv.ParseUint(component, 10, 53)
		if err != nil || parsed > 9007199254740991 {
			return false
		}
	}
	return true
}

func mancodeManifestVersionAtLeast(value, minimum string) bool {
	left := mancodeManifestVersionPattern.FindStringSubmatch(value)
	right := mancodeManifestVersionPattern.FindStringSubmatch(minimum)
	if left == nil || right == nil {
		return false
	}
	for index := 1; index <= 3; index++ {
		leftNumber, leftErr := strconv.ParseUint(left[index], 10, 53)
		rightNumber, rightErr := strconv.ParseUint(right[index], 10, 53)
		if leftErr != nil || rightErr != nil {
			return false
		}
		if leftNumber != rightNumber {
			return leftNumber > rightNumber
		}
	}
	// The required floors are stable releases; a prerelease of the same core
	// version is still below that floor.
	return left[4] == ""
}
