package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

type mancodeLegacyWorkflowComponents struct {
	metadata     []int
	requirements []int
	review       []int
	verification []int
	plan         []int
}

func bindMancodeLegacyWorkflows(sourceRoot string, scanned *[]scannedFile, summary *Summary) ([]migrationcoverage.MancodeLegacyWorkflowCheck, error) {
	groups := make(map[string]*mancodeLegacyWorkflowComponents)
	for index := range *scanned {
		file := (*scanned)[index]
		component := ""
		for _, candidate := range []struct {
			name string
			path func(string, string) (string, bool)
		}{
			{"metadata", isMancodeLegacyWorkflowMetadataPath},
			{"requirements", isMancodeLegacyWorkflowRequirementsPath},
			{"review", isMancodeLegacyWorkflowReviewPath},
			{"verification", isMancodeLegacyWorkflowVerificationPath},
			{"plan", isMancodeLegacyWorkflowPlanPath},
		} {
			if _, ok := candidate.path(file.entry.Path, sourceRoot); ok {
				component = candidate.name
				break
			}
		}
		if component == "" {
			continue
		}
		directory := strings.ToLower(strings.Trim(mancodePath(filepath.Dir(file.entry.Path), sourceRoot), "/"))
		group := groups[directory]
		if group == nil {
			group = &mancodeLegacyWorkflowComponents{}
			groups[directory] = group
		}
		switch component {
		case "metadata":
			group.metadata = append(group.metadata, index)
		case "requirements":
			group.requirements = append(group.requirements, index)
		case "review":
			group.review = append(group.review, index)
		case "verification":
			group.verification = append(group.verification, index)
		case "plan":
			group.plan = append(group.plan, index)
		}
	}

	directories := make([]string, 0, len(groups))
	for directory := range groups {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.MancodeLegacyWorkflowCheck, 0, len(directories))
	for _, directory := range directories {
		group := groups[directory]
		check := migrationcoverage.MancodeLegacyWorkflowCheck{
			SchemaVersion: migrationcoverage.MancodeLegacyWorkflowBindingSchemaVersion,
			Relationship:  "mancode_legacy_workflow_ledgers", WorkflowIDSHA256: mancodeLegacyWorkflowPairID(directory),
			MetadataFileCount: len(group.metadata), RequirementsFileCount: len(group.requirements),
			ReviewFileCount: len(group.review), VerificationFileCount: len(group.verification), PlanFileCount: len(group.plan),
			WorkflowPathBindingVerified: true, Issues: []string{},
			Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		}
		check.MetadataSourceSetSHA256 = mancodeLegacyWorkflowSourceSetDigest(*scanned, group.metadata)
		check.RequirementsSourceSetSHA256 = mancodeLegacyWorkflowSourceSetDigest(*scanned, group.requirements)
		check.ReviewSourceSetSHA256 = mancodeLegacyWorkflowSourceSetDigest(*scanned, group.review)
		check.VerificationSourceSetSHA256 = mancodeLegacyWorkflowSourceSetDigest(*scanned, group.verification)
		check.PlanSourceSetSHA256 = mancodeLegacyWorkflowSourceSetDigest(*scanned, group.plan)
		var artifactReferences []mancodeLegacyWorkflowArtifactReference
		for _, indexes := range [][]int{group.review, group.verification} {
			for _, index := range indexes {
				artifactReferences = append(artifactReferences, (*scanned)[index].legacyArtifactReferences...)
			}
		}
		reconciledReferences, artifactIssues, err := reconcileMancodeLegacyArtifactReferences(
			sourceRoot, artifactReferences, scanned, summary)
		if err != nil {
			return nil, err
		}
		check.ArtifactReferences = reconciledReferences
		check.Issues = append(check.Issues, artifactIssues...)

		var metadata mancodeLegacyWorkflowSummary
		check.MetadataValid = mancodeLegacyWorkflowSingleRecord(*scanned, group.metadata, "mancode_legacy_workflow_summary", &metadata)
		var requirements mancodeLegacyRequirementsProjection
		check.RequirementsValid = mancodeLegacyWorkflowSingleRecord(*scanned, group.requirements, "mancode_legacy_requirements", &requirements)
		var review mancodeLegacyReviewProjection
		check.ReviewValid = mancodeLegacyWorkflowSingleRecord(*scanned, group.review, "mancode_legacy_review_summary", &review)
		var verification mancodeLegacyVerificationProjection
		check.VerificationValid = mancodeLegacyWorkflowSingleRecord(*scanned, group.verification, "mancode_legacy_verification_summary", &verification)
		var plan mancodeLegacyPlanProjection
		check.PlanValid = mancodeLegacyWorkflowSingleRecord(*scanned, group.plan, "mancode_legacy_workflow_plan", &plan)

		appendMancodeLegacyComponentIssues(&check, "metadata", check.MetadataFileCount, check.MetadataValid)
		appendMancodeLegacyComponentIssues(&check, "requirements", check.RequirementsFileCount, check.RequirementsValid)
		appendMancodeLegacyComponentIssues(&check, "verification", check.VerificationFileCount, check.VerificationValid)
		appendMancodeLegacyComponentIssues(&check, "plan", check.PlanFileCount, check.PlanValid)
		if check.ReviewFileCount > 0 {
			appendMancodeLegacyComponentIssues(&check, "review", check.ReviewFileCount, check.ReviewValid)
		}

		if check.MetadataValid {
			planVersion := int64(1)
			check.MetadataPlanVersionDefaulted = metadata.PlanVersion == nil
			if metadata.PlanVersion != nil {
				planVersion = *metadata.PlanVersion
			}
			check.MetadataRequirementsDigestPresent = metadata.RequirementsDigestPresent
			if check.MetadataRequirementsDigestPresent && check.RequirementsValid {
				wantDigest := mancodeLegacyRequirementsDigest(requirements.Requirements)
				check.MetadataRequirementsDigestBindingVerified = wantDigest != "" &&
					wantDigest == mancodeLegacyMetadataRequirementsDigest(*scanned, group.metadata)
				if !check.MetadataRequirementsDigestBindingVerified {
					check.Issues = append(check.Issues, "metadata_requirements_digest_mismatch")
				}
			}
			check.MetadataRequirementsStatusPresent = metadata.RequirementsStatus != ""
			if check.MetadataRequirementsStatusPresent && check.RequirementsValid {
				check.MetadataRequirementsStatusBindingVerified = metadata.RequirementsStatus == requirements.Readiness
				if !check.MetadataRequirementsStatusBindingVerified {
					check.Issues = append(check.Issues, "metadata_requirements_status_mismatch")
				}
			}
			check.MetadataVerificationStatusPresent = metadata.VerificationStatus != ""
			if check.MetadataVerificationStatusPresent && check.VerificationValid {
				check.MetadataVerificationStatusBindingVerified = metadata.VerificationStatus == verification.SourceStatus
				if !check.MetadataVerificationStatusBindingVerified {
					check.Issues = append(check.Issues, "metadata_verification_status_mismatch")
				}
			}
			if check.VerificationValid && check.PlanValid {
				check.PlanVersionBindingVerified = planVersion == verification.PlanVersion
				if !check.PlanVersionBindingVerified {
					check.Issues = append(check.Issues, "plan_version_mismatch")
				}
			}
		} else if check.MetadataFileCount == 0 {
			check.MetadataPlanVersionDefaulted = true
		}
		check.PlanPresenceVerified = check.PlanFileCount == 1 && check.PlanValid

		if check.RequirementsValid && check.VerificationValid {
			check.RequirementsDigestBindingVerified = mancodeLegacyRequirementsDigest(requirements.Requirements) != "" &&
				mancodeLegacyRequirementsDigest(requirements.Requirements) == mancodeLegacyVerificationDigest(*scanned, group.verification)
			if !check.RequirementsDigestBindingVerified {
				check.Issues = append(check.Issues, "requirements_digest_mismatch")
			}
			check.AcceptanceCriteriaBindingVerified = mancodeLegacyVerificationMatchesRequirements(verification, requirements.Requirements)
			if !check.AcceptanceCriteriaBindingVerified {
				check.Issues = append(check.Issues, "acceptance_criteria_mismatch")
			}
		}
		if check.VerificationValid && (check.ReviewValid || check.ReviewFileCount == 0) {
			expectedRound := int64(0)
			if check.ReviewValid {
				expectedRound = review.RemediationRounds
			}
			check.ReviewRemediationBindingVerified = expectedRound == verification.RemediationRound
			if !check.ReviewRemediationBindingVerified {
				check.Issues = append(check.Issues, "remediation_round_mismatch")
			}
		}

		sort.Strings(check.Issues)
		switch {
		case len(check.Issues) == 0 && check.MetadataValid && check.RequirementsValid && check.VerificationValid &&
			check.PlanValid && check.RequirementsDigestBindingVerified && check.AcceptanceCriteriaBindingVerified &&
			check.PlanVersionBindingVerified && check.ReviewRemediationBindingVerified && check.PlanPresenceVerified &&
			(check.ArtifactReferences == nil || check.ArtifactReferences.BindingVerified):
			check.Status = "verified"
		case mancodeLegacyWorkflowHasBindingMismatch(check.Issues):
			check.Status = "conflicting"
		default:
			check.Status = "incomplete"
		}
		upgradeMancodeLegacyVerificationBinding(scanned, group.verification, check)
		checks = append(checks, check)
	}
	return checks, nil
}

func appendMancodeLegacyComponentIssues(check *migrationcoverage.MancodeLegacyWorkflowCheck, name string, count int, valid bool) {
	switch {
	case count == 0:
		check.Issues = append(check.Issues, name+"_missing")
	case count > 1:
		check.Issues = append(check.Issues, name+"_ambiguous")
	case !valid:
		check.Issues = append(check.Issues, name+"_invalid")
	}
}

func mancodeLegacyWorkflowSingleRecord(scanned []scannedFile, indexes []int, recordType string, output any) bool {
	if len(indexes) != 1 {
		return false
	}
	file := scanned[indexes[0]]
	if file.entry.ImportedCount != 1 || file.entry.ExcludedCount != 0 || file.entry.RejectedCount != 0 || len(file.records) != 1 ||
		file.records[0].Type != recordType {
		return false
	}
	return json.Unmarshal(file.records[0].Payload, output) == nil
}

func mancodeLegacyVerificationDigest(scanned []scannedFile, indexes []int) string {
	if len(indexes) != 1 {
		return ""
	}
	return scanned[indexes[0]].legacyRequirementsDigest
}

func mancodeLegacyMetadataRequirementsDigest(scanned []scannedFile, indexes []int) string {
	if len(indexes) != 1 {
		return ""
	}
	return scanned[indexes[0]].legacyMetadataRequirementsDigest
}

func mancodeLegacyRequirementsDigest(ledger mancodeLegacyRequirementsLedger) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(ledger); err != nil {
		return ""
	}
	canonical := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	canonical = mancodeLegacyJSONStringUnicode(canonical)
	return digest(canonical)
}

// JavaScript JSON.stringify emits U+2028 and U+2029 literally. Go's JSON
// encoder escapes them, so undo only actual JSON unicode escapes, not escaped
// backslash text, before hashing Mancode's normalized requirements ledger.
func mancodeLegacyJSONStringUnicode(encoded []byte) []byte {
	output := make([]byte, 0, len(encoded))
	quoted := false
	for index := 0; index < len(encoded); {
		character := encoded[index]
		if quoted && character == '\\' {
			if index+5 < len(encoded) && encoded[index+1] == 'u' {
				escape := string(encoded[index+2 : index+6])
				if escape == "2028" || escape == "2029" {
					if escape == "2028" {
						output = append(output, []byte("\u2028")...)
					} else {
						output = append(output, []byte("\u2029")...)
					}
					index += 6
					continue
				}
			}
			output = append(output, character)
			if index+1 < len(encoded) {
				output = append(output, encoded[index+1])
				index += 2
			} else {
				index++
			}
			continue
		}
		output = append(output, character)
		if character == '"' {
			quoted = !quoted
		}
		index++
	}
	return output
}

func mancodeLegacyVerificationMatchesRequirements(verification mancodeLegacyVerificationProjection, requirements mancodeLegacyRequirementsLedger) bool {
	if len(verification.Checks) != len(requirements.AcceptanceCriteria) {
		return false
	}
	checks := make(map[string]mancodeLegacyVerificationCheck, len(verification.Checks))
	for _, check := range verification.Checks {
		checks[check.AcceptanceID] = check
	}
	for _, criterion := range requirements.AcceptanceCriteria {
		check, exists := checks[criterion.ID]
		if !exists || check.Required != criterion.Required {
			return false
		}
		needsAutomated := criterion.Method == "automated" || criterion.Method == "hybrid"
		needsManual := criterion.Method == "manual" || criterion.Method == "hybrid"
		if (check.Automated != nil) != needsAutomated || (check.Manual != nil) != needsManual {
			return false
		}
	}
	return true
}

func upgradeMancodeLegacyVerificationBinding(scanned *[]scannedFile, indexes []int, check migrationcoverage.MancodeLegacyWorkflowCheck) {
	if len(indexes) != 1 || (*scanned)[indexes[0]].entry.ImportedCount != 1 {
		return
	}
	file := &(*scanned)[indexes[0]]
	for index := range file.records {
		if file.records[index].Type != "mancode_legacy_verification_summary" {
			continue
		}
		var projection mancodeLegacyVerificationProjection
		if json.Unmarshal(file.records[index].Payload, &projection) != nil {
			return
		}
		projection.RequirementsBindingVerified = check.RequirementsDigestBindingVerified && check.AcceptanceCriteriaBindingVerified
		// Mancode v1 plan.md has no body digest or embedded version marker, so
		// metadata/ledger version agreement cannot verify the plan text itself.
		projection.PlanBindingVerified = false
		payload, err := json.Marshal(projection)
		if err != nil {
			return
		}
		file.records[index].Payload = payload
		file.records[index].ContentSHA256 = digest(payload)
	}
}

func mancodeLegacyWorkflowSourceSetDigest(scanned []scannedFile, indexes []int) string {
	if len(indexes) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(indexes))
	for _, index := range indexes {
		hashes = append(hashes, scanned[index].entry.SourceSHA256)
	}
	return mancodePrivacySourceHashSetDigest(hashes)
}

func mancodeLegacyWorkflowPairID(directory string) string {
	return digest([]byte("threadforge/mancode/legacy-workflow-binding/v1\x00" + strings.ToLower(filepath.ToSlash(directory))))
}

func validateMancodeLegacyWorkflowManifestChecks(manifest Manifest) error {
	if manifest.Product != Mancode || len(manifest.MancodeLegacyWorkflowChecks) == 0 {
		return nil
	}
	type componentEntries struct {
		metadata, requirements, review, verification, plan []FileEntry
	}
	groups := make(map[string]*componentEntries)
	for _, file := range manifest.Files {
		component := ""
		for _, candidate := range []struct {
			name string
			path func(string, string) (string, bool)
		}{
			{"metadata", isMancodeLegacyWorkflowMetadataPath},
			{"requirements", isMancodeLegacyWorkflowRequirementsPath},
			{"review", isMancodeLegacyWorkflowReviewPath},
			{"verification", isMancodeLegacyWorkflowVerificationPath},
			{"plan", isMancodeLegacyWorkflowPlanPath},
		} {
			if _, ok := candidate.path(file.Path, manifest.SourceRoot); ok {
				component = candidate.name
				break
			}
		}
		if component == "" {
			continue
		}
		directory := strings.ToLower(strings.Trim(mancodePath(filepath.Dir(file.Path), manifest.SourceRoot), "/"))
		group := groups[directory]
		if group == nil {
			group = &componentEntries{}
			groups[directory] = group
		}
		switch component {
		case "metadata":
			group.metadata = append(group.metadata, file)
		case "requirements":
			group.requirements = append(group.requirements, file)
		case "review":
			group.review = append(group.review, file)
		case "verification":
			group.verification = append(group.verification, file)
		case "plan":
			group.plan = append(group.plan, file)
		}
	}
	if len(groups) != len(manifest.MancodeLegacyWorkflowChecks) {
		return errors.New("Mancode legacy workflow checks do not cover every legacy workflow component directory")
	}
	for directory, group := range groups {
		var check *migrationcoverage.MancodeLegacyWorkflowCheck
		for index := range manifest.MancodeLegacyWorkflowChecks {
			candidate := &manifest.MancodeLegacyWorkflowChecks[index]
			if candidate.WorkflowIDSHA256 == mancodeLegacyWorkflowPairID(directory) {
				check = candidate
				break
			}
		}
		if check == nil ||
			!mancodeLegacyManifestComponentMatches(group.metadata, check.MetadataFileCount, check.MetadataValid, check.MetadataSourceSetSHA256) ||
			!mancodeLegacyManifestComponentMatches(group.requirements, check.RequirementsFileCount, check.RequirementsValid, check.RequirementsSourceSetSHA256) ||
			!mancodeLegacyManifestComponentMatches(group.review, check.ReviewFileCount, check.ReviewValid, check.ReviewSourceSetSHA256) ||
			!mancodeLegacyManifestComponentMatches(group.verification, check.VerificationFileCount, check.VerificationValid, check.VerificationSourceSetSHA256) ||
			!mancodeLegacyManifestComponentMatches(group.plan, check.PlanFileCount, check.PlanValid, check.PlanSourceSetSHA256) {
			return errors.New("Mancode legacy workflow source commitments do not match the manifest file inventory")
		}
		if check.ArtifactReferences != nil && !mancodeLegacyManifestArtifactTargetsMatch(
			manifest.Files, directory, manifest.SourceRoot, check.ArtifactReferences) {
			return errors.New("Mancode legacy workflow artifact commitments do not match the manifest file inventory")
		}
	}
	return nil
}

func legacyMancodeWorkflowBindingChecks(files []FileEntry, sourceRoot string) []migrationcoverage.MancodeLegacyWorkflowCheck {
	type componentEntries struct {
		metadata, requirements, review, verification, plan []FileEntry
	}
	groups := make(map[string]*componentEntries)
	for _, file := range files {
		component := ""
		for _, candidate := range []struct {
			name string
			path func(string, string) (string, bool)
		}{
			{"metadata", isMancodeLegacyWorkflowMetadataPath},
			{"requirements", isMancodeLegacyWorkflowRequirementsPath},
			{"review", isMancodeLegacyWorkflowReviewPath},
			{"verification", isMancodeLegacyWorkflowVerificationPath},
			{"plan", isMancodeLegacyWorkflowPlanPath},
		} {
			if _, ok := candidate.path(file.Path, sourceRoot); ok {
				component = candidate.name
				break
			}
		}
		if component == "" {
			continue
		}
		directory := strings.ToLower(strings.Trim(mancodePath(filepath.Dir(file.Path), sourceRoot), "/"))
		group := groups[directory]
		if group == nil {
			group = &componentEntries{}
			groups[directory] = group
		}
		switch component {
		case "metadata":
			group.metadata = append(group.metadata, file)
		case "requirements":
			group.requirements = append(group.requirements, file)
		case "review":
			group.review = append(group.review, file)
		case "verification":
			group.verification = append(group.verification, file)
		case "plan":
			group.plan = append(group.plan, file)
		}
	}
	directories := make([]string, 0, len(groups))
	for directory := range groups {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.MancodeLegacyWorkflowCheck, 0, len(directories))
	for _, directory := range directories {
		group := groups[directory]
		checks = append(checks, migrationcoverage.MancodeLegacyWorkflowCheck{
			SchemaVersion: migrationcoverage.MancodeLegacyWorkflowBindingSchemaVersion,
			Relationship:  "mancode_legacy_workflow_ledgers", WorkflowIDSHA256: mancodeLegacyWorkflowPairID(directory),
			MetadataFileCount: len(group.metadata), MetadataSourceSetSHA256: mancodeLegacyManifestSourceSet(group.metadata),
			RequirementsFileCount: len(group.requirements), RequirementsSourceSetSHA256: mancodeLegacyManifestSourceSet(group.requirements),
			ReviewFileCount: len(group.review), ReviewSourceSetSHA256: mancodeLegacyManifestSourceSet(group.review),
			VerificationFileCount: len(group.verification), VerificationSourceSetSHA256: mancodeLegacyManifestSourceSet(group.verification),
			PlanFileCount: len(group.plan), PlanSourceSetSHA256: mancodeLegacyManifestSourceSet(group.plan),
			WorkflowPathBindingVerified: true, Status: "incomplete", Issues: []string{"legacy_unverified"},
			Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		})
	}
	return checks
}

func mancodeLegacyManifestComponentMatches(files []FileEntry, count int, valid bool, sourceSet string) bool {
	if count != len(files) || sourceSet != mancodeLegacyManifestSourceSet(files) {
		return false
	}
	wantValid := len(files) == 1 && files[0].ImportedCount == 1 && files[0].ExcludedCount == 0 && files[0].RejectedCount == 0
	return valid == wantValid
}

func mancodeLegacyManifestSourceSet(files []FileEntry) string {
	if len(files) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(files))
	for _, file := range files {
		hashes = append(hashes, file.SourceSHA256)
	}
	return mancodePrivacySourceHashSetDigest(hashes)
}

func mancodeLegacyManifestArtifactTargetsMatch(files []FileEntry, directory, sourceRoot string,
	references *migrationcoverage.MancodeLegacyArtifactReferences) bool {
	if references == nil || references.ReferenceCount < 1 || len(references.Targets) != references.FoundCount {
		return false
	}
	matched := make(map[string]bool, len(references.Targets))
	workflowPrefix := strings.ToLower(filepath.ToSlash(strings.Trim(directory, "/"))) + "/"
	for _, target := range references.Targets {
		var match *FileEntry
		for index := range files {
			file := &files[index]
			canonical := strings.ToLower(strings.Trim(mancodePath(file.Path, sourceRoot), "/"))
			if !strings.HasPrefix(canonical, workflowPrefix) || mancodeLegacyArtifactPathHash(file.Path, sourceRoot) != target.PathSHA256 {
				continue
			}
			if match != nil {
				return false
			}
			match = file
		}
		if match == nil || matched[target.PathSHA256] || match.SourceSHA256 != target.SourceSHA256 {
			return false
		}
		imported := match.ImportedCount > 0 && match.ExcludedCount == 0 && match.RejectedCount == 0 &&
			match.Status != "unsupported" && match.Status != "credential_path_excluded"
		if imported != target.Imported {
			return false
		}
		matched[target.PathSHA256] = true
	}
	return true
}

func mancodeLegacyWorkflowHasBindingMismatch(issues []string) bool {
	for _, issue := range issues {
		switch issue {
		case "requirements_digest_mismatch", "acceptance_criteria_mismatch", "plan_version_mismatch",
			"remediation_round_mismatch", "metadata_requirements_digest_mismatch",
			"metadata_requirements_status_mismatch", "metadata_verification_status_mismatch",
			"referenced_artifact_ambiguous":
			return true
		}
	}
	return false
}
