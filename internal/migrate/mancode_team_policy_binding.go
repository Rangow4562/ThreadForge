package migrate

import (
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/migrationcoverage"
)

func bindMancodeTeamPolicyWorkspace(scanned *[]scannedFile, sourceRoot string) ([]migrationcoverage.MancodeTeamPolicyCheck, error) {
	type components struct {
		teamPolicies  []int
		projectConfig []int
	}
	groups := make(map[string]*components)
	for index := range *scanned {
		file := &(*scanned)[index]
		kind := ""
		switch {
		case isMancodeTeamPolicyPath(file.entry.Path, sourceRoot):
			kind = "team_policy"
		case isMancodeSharedProjectConfigPath(file.entry.Path, sourceRoot):
			kind = "project_config"
		default:
			continue
		}
		sharedDirectory := mancodeTeamPolicySharedDirectory(file.entry.Path, sourceRoot)
		group := groups[sharedDirectory]
		if group == nil {
			group = &components{}
			groups[sharedDirectory] = group
		}
		if kind == "team_policy" {
			group.teamPolicies = append(group.teamPolicies, index)
		} else {
			group.projectConfig = append(group.projectConfig, index)
		}
	}

	directories := make([]string, 0, len(groups))
	for directory, group := range groups {
		if len(group.teamPolicies) > 0 {
			directories = append(directories, directory)
		}
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.MancodeTeamPolicyCheck, 0, len(directories))
	for _, directory := range directories {
		group := groups[directory]
		check := migrationcoverage.MancodeTeamPolicyCheck{
			SchemaVersion:       migrationcoverage.MancodeTeamPolicyBindingSchemaVersion,
			Relationship:        "mancode_team_policy_project_config",
			PairIDSHA256:        mancodeTeamPolicyPairID(directory),
			TeamPolicyFileCount: len(group.teamPolicies), ProjectConfigFileCount: len(group.projectConfig),
			Authority: "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
			Issues: []string{},
		}
		if len(group.teamPolicies) > 0 {
			check.TeamPolicySourceSetSHA256 = mancodeTeamPolicySourceSetDigest(*scanned, group.teamPolicies)
		}
		if len(group.projectConfig) > 0 {
			check.ProjectConfigSourceSetSHA256 = mancodeTeamPolicySourceSetDigest(*scanned, group.projectConfig)
		}
		switch {
		case len(group.teamPolicies) == 0:
			check.Issues = append(check.Issues, "team_policy_missing")
		case len(group.teamPolicies) > 1:
			check.Issues = append(check.Issues, "team_policy_ambiguous")
		default:
			file := &(*scanned)[group.teamPolicies[0]]
			check.TeamPolicyValid = file.entry.RecordCount == 1 && file.entry.ImportedCount == 1 &&
				file.entry.RejectedCount == 0 && file.teamPolicyWorkspaceID != ""
			if !check.TeamPolicyValid {
				check.Issues = append(check.Issues, "team_policy_invalid")
			}
		}
		switch {
		case len(group.projectConfig) == 0:
			check.Issues = append(check.Issues, "project_config_missing")
		case len(group.projectConfig) > 1:
			check.Issues = append(check.Issues, "project_config_ambiguous")
		default:
			file := &(*scanned)[group.projectConfig[0]]
			check.ProjectConfigValid = file.entry.RecordCount == 1 && file.entry.ImportedCount == 1 &&
				file.entry.RejectedCount == 0 && file.projectConfigWorkspaceID != ""
			if !check.ProjectConfigValid {
				check.Issues = append(check.Issues, "project_config_invalid")
			}
		}

		if check.TeamPolicyValid && check.ProjectConfigValid {
			policy := (*scanned)[group.teamPolicies[0]].teamPolicyWorkspaceID
			config := (*scanned)[group.projectConfig[0]].projectConfigWorkspaceID
			check.WorkspaceBindingVerified = policy == config
			if !check.WorkspaceBindingVerified {
				check.Issues = append(check.Issues, "workspace_mismatch")
			}
		}
		sort.Strings(check.Issues)
		switch {
		case len(check.Issues) == 0 && check.TeamPolicyValid && check.ProjectConfigValid && check.WorkspaceBindingVerified:
			check.Status = "verified"
		case containsString(check.Issues, "workspace_mismatch"):
			check.Status = "conflicting"
		default:
			check.Status = "incomplete"
		}
		for _, index := range group.teamPolicies {
			if err := setMancodeTeamPolicyWorkspaceBinding(&(*scanned)[index], check.WorkspaceBindingVerified); err != nil {
				return nil, err
			}
		}
		checks = append(checks, check)
	}
	return checks, nil
}

func setMancodeTeamPolicyWorkspaceBinding(file *scannedFile, verified bool) error {
	for index := range file.records {
		if file.records[index].Type != "mancode_team_policy" {
			continue
		}
		var projection mancodeTeamPolicyImport
		if err := json.Unmarshal(file.records[index].Payload, &projection); err != nil {
			return errors.New("decode normalized Mancode team policy for workspace binding")
		}
		projection.WorkspaceBindingVerified = verified
		payload, err := json.Marshal(projection)
		if err != nil {
			return errors.New("encode normalized Mancode team policy workspace binding")
		}
		file.records[index].Payload = payload
		file.records[index].ContentSHA256 = digest(payload)
		return nil
	}
	return nil
}

func validateMancodeTeamPolicyManifestChecks(manifest Manifest) error {
	if manifest.Product != Mancode || len(manifest.MancodeTeamPolicyChecks) == 0 {
		return nil
	}
	type entries struct {
		teamPolicies  []FileEntry
		projectConfig []FileEntry
	}
	groups := make(map[string]*entries)
	for _, file := range manifest.Files {
		kind := ""
		switch {
		case isMancodeTeamPolicyPath(file.Path, manifest.SourceRoot):
			kind = "team_policy"
		case isMancodeSharedProjectConfigPath(file.Path, manifest.SourceRoot):
			kind = "project_config"
		default:
			continue
		}
		sharedDirectory := mancodeTeamPolicySharedDirectory(file.Path, manifest.SourceRoot)
		group := groups[sharedDirectory]
		if group == nil {
			group = &entries{}
			groups[sharedDirectory] = group
		}
		if kind == "team_policy" {
			group.teamPolicies = append(group.teamPolicies, file)
		} else {
			group.projectConfig = append(group.projectConfig, file)
		}
	}
	policyGroupCount := 0
	for _, group := range groups {
		if len(group.teamPolicies) > 0 {
			policyGroupCount++
		}
	}
	if policyGroupCount != len(manifest.MancodeTeamPolicyChecks) {
		return errors.New("Mancode team-policy checks do not cover every policy source directory")
	}
	for directory, group := range groups {
		if len(group.teamPolicies) == 0 {
			continue
		}
		var check *migrationcoverage.MancodeTeamPolicyCheck
		wantID := mancodeTeamPolicyPairID(directory)
		for index := range manifest.MancodeTeamPolicyChecks {
			candidate := &manifest.MancodeTeamPolicyChecks[index]
			if candidate.PairIDSHA256 == wantID {
				check = candidate
				break
			}
		}
		if check == nil || check.TeamPolicyFileCount != len(group.teamPolicies) ||
			check.ProjectConfigFileCount != len(group.projectConfig) ||
			check.TeamPolicySourceSetSHA256 != mancodeTeamPolicyManifestSourceSetDigest(group.teamPolicies) ||
			check.ProjectConfigSourceSetSHA256 != mancodeTeamPolicyManifestSourceSetDigest(group.projectConfig) ||
			check.TeamPolicyValid != (len(group.teamPolicies) == 1 && group.teamPolicies[0].RecordCount == 1 &&
				group.teamPolicies[0].ImportedCount == 1 && group.teamPolicies[0].RejectedCount == 0) ||
			check.ProjectConfigValid != (len(group.projectConfig) == 1 && group.projectConfig[0].RecordCount == 1 &&
				group.projectConfig[0].ImportedCount == 1 && group.projectConfig[0].RejectedCount == 0) {
			return errors.New("Mancode team-policy binding does not match manifest file inventory")
		}
	}
	return nil
}

func legacyMancodeTeamPolicyChecks(files []FileEntry, sourceRoot string) []migrationcoverage.MancodeTeamPolicyCheck {
	type entries struct {
		teamPolicies  []FileEntry
		projectConfig []FileEntry
	}
	groups := make(map[string]*entries)
	for _, file := range files {
		kind := ""
		switch {
		case isMancodeTeamPolicyPath(file.Path, sourceRoot):
			kind = "team_policy"
		case isMancodeSharedProjectConfigPath(file.Path, sourceRoot):
			kind = "project_config"
		default:
			continue
		}
		sharedDirectory := mancodeTeamPolicySharedDirectory(file.Path, sourceRoot)
		group := groups[sharedDirectory]
		if group == nil {
			group = &entries{}
			groups[sharedDirectory] = group
		}
		if kind == "team_policy" {
			group.teamPolicies = append(group.teamPolicies, file)
		} else {
			group.projectConfig = append(group.projectConfig, file)
		}
	}
	directories := make([]string, 0, len(groups))
	for directory, group := range groups {
		if len(group.teamPolicies) > 0 {
			directories = append(directories, directory)
		}
	}
	sort.Strings(directories)
	checks := make([]migrationcoverage.MancodeTeamPolicyCheck, 0, len(directories))
	for _, directory := range directories {
		group := groups[directory]
		check := migrationcoverage.MancodeTeamPolicyCheck{
			SchemaVersion: migrationcoverage.MancodeTeamPolicyBindingSchemaVersion,
			Relationship:  "mancode_team_policy_project_config", PairIDSHA256: mancodeTeamPolicyPairID(directory),
			TeamPolicyFileCount: len(group.teamPolicies), TeamPolicyValid: len(group.teamPolicies) == 1 &&
				group.teamPolicies[0].RecordCount == 1 && group.teamPolicies[0].ImportedCount == 1 && group.teamPolicies[0].RejectedCount == 0,
			TeamPolicySourceSetSHA256: "",
			ProjectConfigFileCount:    len(group.projectConfig), ProjectConfigValid: len(group.projectConfig) == 1 &&
				group.projectConfig[0].RecordCount == 1 && group.projectConfig[0].ImportedCount == 1 && group.projectConfig[0].RejectedCount == 0,
			Status: "incomplete", Issues: []string{"legacy_unverified"}, Authority: "local_pending_review",
			HumanReviewRequired: true, ExecutionAuthorized: false,
		}
		if len(group.teamPolicies) > 0 {
			check.TeamPolicySourceSetSHA256 = mancodeTeamPolicyManifestSourceSetDigest(group.teamPolicies)
		}
		if len(group.projectConfig) > 0 {
			check.ProjectConfigSourceSetSHA256 = mancodeTeamPolicyManifestSourceSetDigest(group.projectConfig)
		}
		checks = append(checks, check)
	}
	return checks
}

func isMancodeTeamPolicyPath(relative, sourceRoot string) bool {
	canonical := mancodePath(relative, sourceRoot)
	return canonical == "/.mancode/shared/team/policy.json" || canonical == "/shared/team/policy.json"
}

func isMancodeSharedProjectConfigPath(relative, sourceRoot string) bool {
	canonical := mancodePath(relative, sourceRoot)
	return canonical == "/.mancode/shared/config.json" || canonical == "/shared/config.json"
}

func mancodeTeamPolicySharedDirectory(relative, sourceRoot string) string {
	canonical := path.Clean(mancodePath(relative, sourceRoot))
	directory := path.Dir(canonical)
	if isMancodeTeamPolicyPath(relative, sourceRoot) {
		directory = path.Dir(directory)
	}
	return strings.ToLower(directory)
}

func mancodeTeamPolicyPairID(directory string) string {
	return digest([]byte("threadforge/mancode/team-policy-binding/v1\x00" + directory))
}

func mancodeTeamPolicySourceSetDigest(scanned []scannedFile, indexes []int) string {
	if len(indexes) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(indexes))
	for _, index := range indexes {
		hashes = append(hashes, scanned[index].entry.SourceSHA256)
	}
	sort.Strings(hashes)
	var builder strings.Builder
	for _, hash := range hashes {
		builder.WriteString(hash)
		builder.WriteByte('\n')
	}
	return digest([]byte(builder.String()))
}

func mancodeTeamPolicyManifestSourceSetDigest(files []FileEntry) string {
	if len(files) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(files))
	for _, file := range files {
		hashes = append(hashes, file.SourceSHA256)
	}
	sort.Strings(hashes)
	var builder strings.Builder
	for _, hash := range hashes {
		builder.WriteString(hash)
		builder.WriteByte('\n')
	}
	return digest([]byte(builder.String()))
}
