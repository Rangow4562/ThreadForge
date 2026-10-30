package migrate

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
)

type mancodeWorkflowArchiveLocation struct {
	directoryKey string
	workflowPath string
	namespace    string
	taskID       string
	archiveID    string
	fileName     string
	validPath    bool
}

type mancodeWorkflowArchiveGroup struct {
	location     mancodeWorkflowArchiveLocation
	all          []int
	manifests    []int
	requirements []int
	plans        []int
	other        []int
}

func mancodeWorkflowArchiveLocationForPath(relative, sourceRoot string) (mancodeWorkflowArchiveLocation, bool) {
	standard := strings.Trim(mancodePath(relative, sourceRoot), "/")
	parts := strings.Split(standard, "/")
	for workflowIndex := 0; workflowIndex+2 < len(parts); workflowIndex++ {
		if parts[workflowIndex] != "workflows" || parts[workflowIndex+2] != "archives" {
			continue
		}
		archiveIndex := workflowIndex + 2
		archiveID := ""
		fileName := ""
		if archiveIndex+1 < len(parts) {
			archiveID = parts[archiveIndex+1]
		}
		if archiveIndex+2 < len(parts) {
			fileName = strings.Join(parts[archiveIndex+2:], "/")
		}
		directoryEnd := archiveIndex + 2
		if directoryEnd > len(parts) {
			directoryEnd = len(parts)
		}
		location := mancodeWorkflowArchiveLocation{
			directoryKey: strings.Join(parts[:directoryEnd], "/"),
			workflowPath: strings.Join(parts[:workflowIndex+2], "/"),
			fileName:     fileName,
			archiveID:    archiveID,
		}
		if workflowIndex > 0 {
			location.namespace = parts[workflowIndex-1]
		}
		if workflowIndex > 1 {
			location.taskID = parts[workflowIndex+1]
		}
		location.validPath = workflowIndex == 2 && parts[0] == ".mancode" &&
			(location.namespace == "local" || location.namespace == "shared") &&
			parts[workflowIndex-1] == location.namespace &&
			mancodeOperationIDPattern.MatchString(strings.ToUpper(location.taskID)) &&
			mancodeOperationIDPattern.MatchString(strings.ToUpper(location.archiveID)) &&
			len(parts) == archiveIndex+3 &&
			(location.fileName == "archive.json" || location.fileName == "requirements.json" || location.fileName == "plan.md")
		return location, true
	}
	return mancodeWorkflowArchiveLocation{}, false
}

func isMancodeWorkflowArchivePath(relative, sourceRoot string) bool {
	_, ok := mancodeWorkflowArchiveLocationForPath(relative, sourceRoot)
	return ok
}

func isMancodeWorkflowArchiveDocumentPath(relative, sourceRoot string) bool {
	location, ok := mancodeWorkflowArchiveLocationForPath(relative, sourceRoot)
	return ok && location.validPath && (location.fileName == "archive.json" ||
		location.fileName == "requirements.json" || location.fileName == "plan.md")
}

func bindMancodeWorkflowArchives(sourceRoot string, scanned []scannedFile) []migrationcoverage.MancodeWorkflowArchiveCheck {
	groups := groupMancodeWorkflowArchives(sourceRoot, scanned)
	checks := make([]migrationcoverage.MancodeWorkflowArchiveCheck, 0, len(groups))
	for _, key := range sortedMancodeWorkflowArchiveGroupKeys(groups) {
		group := groups[key]
		checks = append(checks, buildMancodeWorkflowArchiveCheck(group, scanned))
	}
	sort.Slice(checks, func(i, j int) bool {
		return checks[i].WorkflowIDSHA256+"/"+checks[i].ArchiveIDSHA256 <
			checks[j].WorkflowIDSHA256+"/"+checks[j].ArchiveIDSHA256
	})
	return checks
}

func groupMancodeWorkflowArchives(sourceRoot string, scanned []scannedFile) map[string]*mancodeWorkflowArchiveGroup {
	groups := make(map[string]*mancodeWorkflowArchiveGroup)
	for index := range scanned {
		location, ok := mancodeWorkflowArchiveLocationForPath(scanned[index].entry.Path, sourceRoot)
		if !ok {
			continue
		}
		group := groups[location.directoryKey]
		if group == nil {
			group = &mancodeWorkflowArchiveGroup{location: location}
			groups[location.directoryKey] = group
		}
		group.all = append(group.all, index)
		switch location.fileName {
		case "archive.json":
			group.manifests = append(group.manifests, index)
		case "requirements.json":
			group.requirements = append(group.requirements, index)
		case "plan.md":
			group.plans = append(group.plans, index)
		default:
			group.other = append(group.other, index)
		}
	}
	return groups
}

func sortedMancodeWorkflowArchiveGroupKeys(groups map[string]*mancodeWorkflowArchiveGroup) []string {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func buildMancodeWorkflowArchiveCheck(group *mancodeWorkflowArchiveGroup, scanned []scannedFile) migrationcoverage.MancodeWorkflowArchiveCheck {
	location := group.location
	check := migrationcoverage.MancodeWorkflowArchiveCheck{
		SchemaVersion:         migrationcoverage.MancodeWorkflowArchiveBindingSchemaVersion,
		Relationship:          "mancode_workflow_archive_artifacts",
		WorkflowIDSHA256:      mancodeCoordinationIdentityHash("workflow-archive-task", location.workflowPath),
		ArchiveIDSHA256:       mancodeCoordinationIdentityHash("workflow-archive-id", location.archiveID),
		ArchiveFileCount:      len(group.all),
		ManifestFileCount:     len(group.manifests),
		RequirementsFileCount: len(group.requirements),
		PlanFileCount:         len(group.plans),
		OtherFileCount:        len(group.other),
		Authority:             "local_pending_review",
		HumanReviewRequired:   true,
		ExecutionAuthorized:   false,
		Issues:                []string{},
	}
	check.ArchiveSourceSetSHA256 = mancodeWorkflowArchiveSourceSetDigest(scanned, group.all)
	check.ManifestSourceSetSHA256 = mancodeWorkflowArchiveSourceSetDigest(scanned, group.manifests)
	check.RequirementsSourceSetSHA256 = mancodeWorkflowArchiveSourceSetDigest(scanned, group.requirements)
	check.PlanSourceSetSHA256 = mancodeWorkflowArchiveSourceSetDigest(scanned, group.plans)
	check.OtherSourceSetSHA256 = mancodeWorkflowArchiveSourceSetDigest(scanned, group.other)

	addIssue := func(issue string) { check.Issues = append(check.Issues, issue) }
	for _, index := range group.all {
		if scanned[index].entry.ExcludedCount > 0 {
			addIssue("credential_excluded")
			break
		}
	}
	if !location.validPath {
		addIssue("invalid_path")
	}
	if len(group.other) > 0 {
		addIssue("unexpected_file")
	}
	if len(group.manifests) == 0 {
		addIssue("archive_manifest_missing")
	} else if len(group.manifests) > 1 {
		addIssue("archive_manifest_ambiguous")
	}
	if len(group.requirements) == 0 {
		addIssue("requirements_missing")
	} else if len(group.requirements) > 1 {
		addIssue("requirements_ambiguous")
	}
	if len(group.plans) > 1 {
		addIssue("plan_ambiguous")
	}

	var manifest map[string]any
	var taskRef map[string]any
	var requirementsContent string
	var planContent *string
	if len(group.manifests) == 1 {
		content, ok := mancodeWorkflowArchiveDocumentContent(scanned[group.manifests[0]])
		if !ok || len(content) > maxFileBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) ||
			strictjson.Decode([]byte(content), &manifest) != nil || !validMancodeWorkflowArchiveManifest(manifest) {
			addIssue("archive_manifest_invalid")
		} else {
			check.ManifestValid = true
			parsedTaskRef, taskRefOK := mancodeTaskRefFromValue(manifest["taskRef"])
			if !taskRefOK {
				check.ManifestValid = false
				addIssue("archive_manifest_invalid")
			} else {
				taskRef = parsedTaskRef
				if taskRef["namespace"] != location.namespace || taskRef["taskId"] != strings.ToUpper(location.taskID) {
					addIssue("task_ref_mismatch")
				}
				if mancodeString(manifest["archiveId"]) != strings.ToUpper(location.archiveID) ||
					mancodeString(manifest["operationId"]) != strings.ToUpper(location.archiveID) {
					addIssue("archive_id_mismatch")
				}
			}
		}
	}
	if len(group.requirements) == 1 {
		content, ok := mancodeWorkflowArchiveDocumentContent(scanned[group.requirements[0]])
		if !ok || len(content) > maxFileBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			addIssue("requirements_invalid")
		} else {
			requirementsContent = content
			var requirements map[string]any
			if strictjson.Decode([]byte(content), &requirements) != nil || !validMancodeWorkflowArchiveRequirements(requirements) {
				addIssue("requirements_invalid")
			} else {
				check.RequirementsValid = true
			}
		}
	}
	if len(group.plans) == 1 {
		content, ok := mancodeWorkflowArchiveDocumentContent(scanned[group.plans[0]])
		if !ok || len(content) > maxFileBytes || !utf8.ValidString(content) || strings.ContainsRune(content, 0) || strings.TrimSpace(content) == "" {
			addIssue("plan_invalid")
		} else {
			planContent = &content
			check.PlanValid = true
		}
	}

	if manifest != nil && check.ManifestValid && check.RequirementsValid &&
		(len(group.plans) == 0 || check.PlanValid) && len(group.manifests) == 1 && len(group.requirements) == 1 && len(group.plans) <= 1 {
		check.RequirementsDigestBindingVerified = mancodeWorkflowArchiveRequirementsBinding(manifest, requirementsContent, location, taskRef)
		if !check.RequirementsDigestBindingVerified {
			addIssue("requirements_digest_mismatch")
		}
		check.PlanDigestBindingVerified = mancodeWorkflowArchivePlanBinding(manifest, planContent, taskRef)
		if !check.PlanDigestBindingVerified {
			addIssue("plan_digest_mismatch")
		}
		if check.RequirementsDigestBindingVerified && check.PlanDigestBindingVerified {
			check.ArchiveDigestBindingVerified = mancodeWorkflowArchiveDigestBinding(manifest, requirementsContent, planContent)
			if !check.ArchiveDigestBindingVerified {
				addIssue("archive_digest_mismatch")
			}
		}
	}
	if check.ArchiveFileCount != len(group.manifests)+len(group.requirements)+len(group.plans)+len(group.other) {
		addIssue("unexpected_file")
	}
	sort.Strings(check.Issues)
	check.Issues = uniqueMancodeWorkflowArchiveIssues(check.Issues)
	switch {
	case len(check.Issues) == 0 && check.ArchiveDigestBindingVerified:
		check.Status = "verified"
	case containsAnyMancodeWorkflowArchiveConflict(check.Issues):
		check.Status = "conflicting"
	default:
		check.Status = "incomplete"
	}
	return check
}

func uniqueMancodeWorkflowArchiveIssues(issues []string) []string {
	if len(issues) < 2 {
		return issues
	}
	unique := issues[:1]
	for _, issue := range issues[1:] {
		if issue != unique[len(unique)-1] {
			unique = append(unique, issue)
		}
	}
	return unique
}

func mancodeWorkflowArchiveDocumentContent(file scannedFile) (string, bool) {
	if file.entry.RecordCount != 1 || file.entry.ImportedCount != 1 || file.entry.ExcludedCount != 0 ||
		file.entry.RejectedCount != 0 || len(file.records) != 1 || file.records[0].Type != "workflow_document" {
		return "", false
	}
	var document map[string]any
	if strictjson.Decode(file.records[0].Payload, &document) != nil || !mancodeHasOnlyKeys(document, []string{"content"}) {
		return "", false
	}
	content, ok := document["content"].(string)
	return content, ok
}

var mancodeWorkflowArchiveManifestKeys = []string{
	"schemaVersion", "archiveId", "operationId", "taskRef", "sourceTaskRevision", "sourceRequirementsRevision",
	"sourceRequirementsDigest", "sourcePlanVersion", "sourcePlanDigest", "requirementsFileDigest", "planFileDigest",
	"archiveDigest", "createdAt",
}

func validMancodeWorkflowArchiveManifest(manifest map[string]any) bool {
	if manifest == nil || !mancodeHasOnlyKeys(manifest, mancodeWorkflowArchiveManifestKeys) ||
		!mancodeHasAllKeys(manifest, mancodeWorkflowArchiveManifestKeys) || mancodeInteger(manifest["schemaVersion"]) != 1 ||
		!mancodeOperationIDPattern.MatchString(mancodeString(manifest["archiveId"])) ||
		!mancodeOperationIDPattern.MatchString(mancodeString(manifest["operationId"])) ||
		!mancodeIsPositiveInteger(manifest["sourceTaskRevision"]) || !mancodeIsPositiveInteger(manifest["sourceRequirementsRevision"]) ||
		!validMancodeDigest(mancodeString(manifest["sourceRequirementsDigest"])) || !mancodeIsPositiveInteger(manifest["sourcePlanVersion"]) ||
		!mancodeNullableArchiveDigest(manifest["sourcePlanDigest"]) || !validMancodeDigest(mancodeString(manifest["requirementsFileDigest"])) ||
		!mancodeNullableArchiveDigest(manifest["planFileDigest"]) || !validMancodeDigest(mancodeString(manifest["archiveDigest"])) ||
		!mancodeRFC3339(manifest["createdAt"]) {
		return false
	}
	_, ok := mancodeTaskRefFromValue(manifest["taskRef"])
	return ok
}

func mancodeNullableArchiveDigest(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && validMancodeDigest(text)
}

func validMancodeWorkflowArchiveRequirements(requirements map[string]any) bool {
	if requirements == nil || !mancodeHasOnlyKeys(requirements, mancodeWorkflowRequirementsKeys) ||
		!mancodeHasAllKeys(requirements, requiredWorkflowLedgerKeys("requirements")) ||
		mancodeInteger(requirements["schemaVersion"]) != 1 ||
		mancodeString(requirements["canonicalizationVersion"]) != "mancode-jcs-v1" ||
		!mancodeWorkflowLedgerShape("requirements", requirements) {
		return false
	}
	digest, err := mancodeWorkflowLedgerDigest("requirements", requirements)
	return err == nil && digest == mancodeString(requirements["contentDigest"])
}

func mancodeWorkflowArchiveRequirementsBinding(manifest map[string]any, requirementsContent string,
	location mancodeWorkflowArchiveLocation, taskRef map[string]any) bool {
	var requirements map[string]any
	if strictjson.Decode([]byte(requirementsContent), &requirements) != nil || !validMancodeWorkflowArchiveRequirements(requirements) {
		return false
	}
	requirementsTaskRef, ok := mancodeTaskRefFromValue(requirements["taskRef"])
	if !ok || !sameMancodeTaskRef(requirementsTaskRef, taskRef) ||
		requirementsTaskRef["namespace"] != location.namespace || requirementsTaskRef["taskId"] != strings.ToUpper(location.taskID) ||
		mancodeInteger(requirements["revision"]) != mancodeInteger(manifest["sourceRequirementsRevision"]) ||
		mancodeString(requirements["contentDigest"]) != mancodeString(manifest["sourceRequirementsDigest"]) {
		return false
	}
	requirementsFileDigest, err := mancodeCanonicalDigest(map[string]any{"content": requirementsContent})
	return err == nil && requirementsFileDigest == mancodeString(manifest["requirementsFileDigest"])
}

func mancodeWorkflowArchivePlanBinding(manifest map[string]any, planContent *string, taskRef map[string]any) bool {
	planDigest := manifest["sourcePlanDigest"]
	fileDigest := manifest["planFileDigest"]
	if planContent == nil {
		return planDigest == nil && fileDigest == nil
	}
	if planDigest == nil || fileDigest == nil {
		return false
	}
	actualPlanDigest, err := mancodeCanonicalDigest(map[string]any{
		"artifactRef": map[string]any{"taskRef": taskRef, "kind": "plan"},
		"content":     *planContent,
	})
	if err != nil || actualPlanDigest != mancodeString(planDigest) {
		return false
	}
	actualFileDigest, err := mancodeCanonicalDigest(map[string]any{"content": *planContent})
	return err == nil && actualFileDigest == mancodeString(fileDigest)
}

func mancodeWorkflowArchiveDigestBinding(manifest map[string]any, requirementsContent string, planContent *string) bool {
	var plan any
	if planContent != nil {
		plan = *planContent
	}
	archiveID := mancodeString(manifest["archiveId"])
	actual, err := mancodeCanonicalDigest(map[string]any{
		"archiveId": archiveID, "operationId": archiveID, "taskRef": manifest["taskRef"],
		"sourceTaskRevision":         manifest["sourceTaskRevision"],
		"sourceRequirementsRevision": manifest["sourceRequirementsRevision"],
		"sourceRequirementsDigest":   manifest["sourceRequirementsDigest"],
		"sourcePlanVersion":          manifest["sourcePlanVersion"], "sourcePlanDigest": manifest["sourcePlanDigest"],
		"createdAt": manifest["createdAt"], "requirementsContent": requirementsContent, "planContent": plan,
	})
	return err == nil && actual == mancodeString(manifest["archiveDigest"])
}

func containsAnyMancodeWorkflowArchiveConflict(issues []string) bool {
	for _, issue := range issues {
		switch issue {
		case "task_ref_mismatch", "archive_id_mismatch", "requirements_digest_mismatch", "plan_digest_mismatch", "archive_digest_mismatch":
			return true
		}
	}
	return false
}

func mancodeWorkflowArchiveSourceSetDigest(scanned []scannedFile, indices []int) string {
	if len(indices) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(indices))
	for _, index := range indices {
		hashes = append(hashes, scanned[index].entry.SourceSHA256)
	}
	sort.Strings(hashes)
	return mancodeWorkflowArchiveDigestList(hashes)
}

func mancodeWorkflowArchiveSourceSetDigestEntries(entries []FileEntry) string {
	if len(entries) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(entries))
	for _, entry := range entries {
		hashes = append(hashes, entry.SourceSHA256)
	}
	sort.Strings(hashes)
	return mancodeWorkflowArchiveDigestList(hashes)
}

func mancodeWorkflowArchiveDigestList(hashes []string) string {
	var builder strings.Builder
	for _, hash := range hashes {
		builder.WriteString(hash)
		builder.WriteByte('\n')
	}
	return digest([]byte(builder.String()))
}

func legacyMancodeWorkflowArchiveChecks(files []FileEntry, sourceRoot string) []migrationcoverage.MancodeWorkflowArchiveCheck {
	groups := make(map[string]*mancodeWorkflowArchiveGroup)
	for _, file := range files {
		location, ok := mancodeWorkflowArchiveLocationForPath(file.Path, sourceRoot)
		if !ok {
			continue
		}
		group := groups[location.directoryKey]
		if group == nil {
			group = &mancodeWorkflowArchiveGroup{location: location}
			groups[location.directoryKey] = group
		}
		index := len(group.all)
		group.all = append(group.all, index)
		switch location.fileName {
		case "archive.json":
			group.manifests = append(group.manifests, index)
		case "requirements.json":
			group.requirements = append(group.requirements, index)
		case "plan.md":
			group.plans = append(group.plans, index)
		default:
			group.other = append(group.other, index)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	checks := make([]migrationcoverage.MancodeWorkflowArchiveCheck, 0, len(groups))
	for _, key := range keys {
		group := groups[key]
		location := group.location
		groupFiles := make([]FileEntry, 0, len(group.all))
		manifestFiles := make([]FileEntry, 0, len(group.manifests))
		requirementsFiles := make([]FileEntry, 0, len(group.requirements))
		planFiles := make([]FileEntry, 0, len(group.plans))
		otherFiles := make([]FileEntry, 0, len(group.other))
		for _, file := range files {
			fileLocation, ok := mancodeWorkflowArchiveLocationForPath(file.Path, sourceRoot)
			if !ok || fileLocation.directoryKey != key {
				continue
			}
			groupFiles = append(groupFiles, file)
			switch fileLocation.fileName {
			case "archive.json":
				manifestFiles = append(manifestFiles, file)
			case "requirements.json":
				requirementsFiles = append(requirementsFiles, file)
			case "plan.md":
				planFiles = append(planFiles, file)
			default:
				otherFiles = append(otherFiles, file)
			}
		}
		check := migrationcoverage.MancodeWorkflowArchiveCheck{
			SchemaVersion:    migrationcoverage.MancodeWorkflowArchiveBindingSchemaVersion,
			Relationship:     "mancode_workflow_archive_artifacts",
			WorkflowIDSHA256: mancodeCoordinationIdentityHash("workflow-archive-task", location.workflowPath),
			ArchiveIDSHA256:  mancodeCoordinationIdentityHash("workflow-archive-id", location.archiveID),
			ArchiveFileCount: len(groupFiles), ArchiveSourceSetSHA256: mancodeWorkflowArchiveSourceSetDigestEntries(groupFiles),
			ManifestFileCount: len(manifestFiles), ManifestSourceSetSHA256: mancodeWorkflowArchiveSourceSetDigestEntries(manifestFiles),
			RequirementsFileCount: len(requirementsFiles), RequirementsSourceSetSHA256: mancodeWorkflowArchiveSourceSetDigestEntries(requirementsFiles),
			PlanFileCount: len(planFiles), PlanSourceSetSHA256: mancodeWorkflowArchiveSourceSetDigestEntries(planFiles),
			OtherFileCount: len(otherFiles), OtherSourceSetSHA256: mancodeWorkflowArchiveSourceSetDigestEntries(otherFiles),
			Status: "incomplete", Issues: []string{"legacy_unverified"}, Authority: "local_pending_review",
			HumanReviewRequired: true, ExecutionAuthorized: false,
		}
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool {
		return checks[i].WorkflowIDSHA256+"/"+checks[i].ArchiveIDSHA256 <
			checks[j].WorkflowIDSHA256+"/"+checks[j].ArchiveIDSHA256
	})
	return checks
}

func validateMancodeWorkflowArchiveManifestChecks(manifest Manifest) error {
	groups := groupMancodeWorkflowArchiveManifestEntries(manifest.Files, manifest.SourceRoot)
	if len(manifest.MancodeWorkflowArchiveChecks) == 0 {
		return nil // Older snapshots are reported as legacy_unverified by coverage generation.
	}
	if len(groups) != len(manifest.MancodeWorkflowArchiveChecks) {
		return errors.New("Mancode workflow archive checks do not cover every archive directory")
	}
	for key := range groups {
		group := groups[key]
		identity := mancodeCoordinationIdentityHash("workflow-archive-task", group.location.workflowPath) + "/" +
			mancodeCoordinationIdentityHash("workflow-archive-id", group.location.archiveID)
		var check *migrationcoverage.MancodeWorkflowArchiveCheck
		for index := range manifest.MancodeWorkflowArchiveChecks {
			candidate := &manifest.MancodeWorkflowArchiveChecks[index]
			candidateIdentity := candidate.WorkflowIDSHA256 + "/" + candidate.ArchiveIDSHA256
			if candidateIdentity == identity {
				check = candidate
				break
			}
		}
		if check == nil || !mancodeWorkflowArchiveCheckInventoryMatches(*check, manifest.Files, manifest.SourceRoot, key) {
			return errors.New("Mancode workflow archive binding does not match manifest file inventory")
		}
	}
	return nil
}

func groupMancodeWorkflowArchiveManifestEntries(files []FileEntry, sourceRoot string) map[string]*mancodeWorkflowArchiveGroup {
	groups := make(map[string]*mancodeWorkflowArchiveGroup)
	for _, file := range files {
		location, ok := mancodeWorkflowArchiveLocationForPath(file.Path, sourceRoot)
		if !ok {
			continue
		}
		group := groups[location.directoryKey]
		if group == nil {
			group = &mancodeWorkflowArchiveGroup{location: location}
			groups[location.directoryKey] = group
		}
		switch location.fileName {
		case "archive.json":
			group.manifests = append(group.manifests, -1)
		case "requirements.json":
			group.requirements = append(group.requirements, -1)
		case "plan.md":
			group.plans = append(group.plans, -1)
		default:
			group.other = append(group.other, -1)
		}
	}
	return groups
}

func mancodeWorkflowArchiveCheckInventoryMatches(check migrationcoverage.MancodeWorkflowArchiveCheck,
	files []FileEntry, sourceRoot, key string) bool {
	var all, manifests, requirements, plans, other []FileEntry
	for _, file := range files {
		location, ok := mancodeWorkflowArchiveLocationForPath(file.Path, sourceRoot)
		if !ok {
			continue
		}
		if location.directoryKey != key {
			continue
		}
		all = append(all, file)
		switch location.fileName {
		case "archive.json":
			manifests = append(manifests, file)
		case "requirements.json":
			requirements = append(requirements, file)
		case "plan.md":
			plans = append(plans, file)
		default:
			other = append(other, file)
		}
	}
	if check.ArchiveFileCount != len(all) || check.ManifestFileCount != len(manifests) ||
		check.RequirementsFileCount != len(requirements) || check.PlanFileCount != len(plans) || check.OtherFileCount != len(other) ||
		check.ArchiveSourceSetSHA256 != mancodeWorkflowArchiveSourceSetDigestEntries(all) ||
		check.ManifestSourceSetSHA256 != mancodeWorkflowArchiveSourceSetDigestEntries(manifests) ||
		check.RequirementsSourceSetSHA256 != mancodeWorkflowArchiveSourceSetDigestEntries(requirements) ||
		check.PlanSourceSetSHA256 != mancodeWorkflowArchiveSourceSetDigestEntries(plans) ||
		check.OtherSourceSetSHA256 != mancodeWorkflowArchiveSourceSetDigestEntries(other) {
		return false
	}
	if check.ManifestValid && !mancodeWorkflowArchiveFileEntryReady(manifests) ||
		check.RequirementsValid && !mancodeWorkflowArchiveFileEntryReady(requirements) ||
		check.PlanValid && !mancodeWorkflowArchiveFileEntryReady(plans) {
		return false
	}
	return true
}

func validateMancodeWorkflowArchiveSet(manifest Manifest, records []Record) error {
	if len(manifest.MancodeWorkflowArchiveChecks) == 0 {
		return nil // Legacy snapshots carry no source-bound archive check.
	}
	recordsByPath := make(map[string][]Record)
	for _, record := range records {
		recordsByPath[record.SourcePath] = append(recordsByPath[record.SourcePath], record)
	}
	scanned := make([]scannedFile, 0, len(manifest.Files))
	for _, entry := range manifest.Files {
		file := scannedFile{entry: entry}
		file.records = append(file.records, recordsByPath[entry.Path]...)
		scanned = append(scanned, file)
	}
	checks := bindMancodeWorkflowArchives(manifest.SourceRoot, scanned)
	actual, err := json.Marshal(checks)
	if err != nil {
		return errors.New("encode Mancode workflow archive snapshot checks")
	}
	want, err := json.Marshal(manifest.MancodeWorkflowArchiveChecks)
	if err != nil || string(actual) != string(want) {
		return errors.New("Mancode workflow archive checks do not match snapshot records")
	}
	return nil
}

func mancodeWorkflowArchiveFileEntryReady(files []FileEntry) bool {
	return len(files) == 1 && files[0].RecordCount == 1 && files[0].ImportedCount == 1 &&
		files[0].ExcludedCount == 0 && files[0].RejectedCount == 0
}
