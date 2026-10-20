package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

func runTeam(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team <identity generate|trust validate|trust install|scope status|scope decommission|scope restore|audit verify|recovery snapshot|recovery verify|recovery restore|recovery activate|sync pull|sync push>")
		return 2
	}
	switch args[0] {
	case "identity":
		if args[1] != "generate" {
			_, _ = fmt.Fprintln(stderr, "usage: threadforge team identity generate --private-key <path> --public-key <path>")
			return 2
		}
		return generateTeamIdentity(args[2:], stdout, stderr)
	case "trust":
		switch args[1] {
		case "validate":
			return validateTeamTrust(args[2:], stdout, stderr)
		case "install":
			return installTeamTrust(args[2:], stdout, stderr)
		default:
			_, _ = fmt.Fprintln(stderr, "usage: threadforge team trust <validate|install>")
			return 2
		}
	case "audit":
		if args[1] != "verify" {
			_, _ = fmt.Fprintln(stderr, "usage: threadforge team audit verify [--db <path>]")
			return 2
		}
		return verifyTeamContinuityAudit(args[2:], stdout, stderr)
	case "recovery":
		return runTeamRecovery(args[1:], stdout, stderr)
	case "sync":
		return runTeamSync(args[1:], stdout, stderr)
	case "scope":
		switch args[1] {
		case "status":
			return teamScopeStatus(args[2:], stdout, stderr)
		case "decommission":
			return changeTeamScopeLifecycle(args[2:], "decommissioned", stdout, stderr)
		case "restore":
			return changeTeamScopeLifecycle(args[2:], "restored", stdout, stderr)
		default:
			_, _ = fmt.Fprintln(stderr, "usage: threadforge team scope <status|decommission|restore>")
			return 2
		}
	default:
		_, _ = fmt.Fprintf(stderr, "unknown team command %q\n", args[0])
		return 2
	}
}

func teamScopeStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team scope status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project-id", "", "registered team project ID")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*projectID) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team scope status --project-id <id> [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	status, err := databaseStore.TeamProjectScopeStatus(context.Background(), *projectID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, status, stderr)
}

func changeTeamScopeLifecycle(args []string, eventAction string, stdout, stderr io.Writer) int {
	command := "team scope " + strings.TrimSuffix(eventAction, "ed")
	if eventAction == "decommissioned" {
		command = "team scope decommission"
	} else {
		command = "team scope restore"
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := fs.String("project-id", "", "registered team project ID")
	confirmProjectID := fs.String("confirm-project-id", "", "must exactly match --project-id to confirm the lifecycle change")
	actor := fs.String("actor", "local-user", "team audit subject (must match the authenticated identity)")
	reason := fs.String("reason", "", "required audit reason")
	identityPath := fs.String("identity-key", "", "private team identity authorized for team.scope.manage")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*projectID) == "" || *confirmProjectID != *projectID || strings.TrimSpace(*reason) == "" ||
		strings.TrimSpace(*identityPath) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "usage: %s --project-id <id> --confirm-project-id <same-id> --identity-key <private-key.json> --reason <text> [--actor <subject>] [--db <path>]\n", command)
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	status, err := databaseStore.TeamProjectScopeStatus(context.Background(), *projectID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	mutationAction := "team.scope.decommission"
	fromStatus, toStatus := "active", "decommissioned"
	if eventAction == "restored" {
		mutationAction, fromStatus, toStatus = "team.scope.restore", "decommissioned", "active"
	}
	resolvedActor, authorizer, err := authorizeContinuityActor(*database, *projectID, mutationAction, *actor, *identityPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if authorizer == nil {
		_, _ = fmt.Fprintln(stderr, "team scope lifecycle changes require a trusted team identity")
		return 1
	}
	entity := store.TeamScopeLifecycleMutation{ProjectID: *projectID, OrganizationID: status.OrganizationID,
		FromStatus: fromStatus, ToStatus: toStatus, Revision: status.Revision + 1}
	ctx, err := authorizer.withMutationProof(context.Background(), mutationAction, eventAction, "project_scope",
		store.TeamScopeResourceID(*projectID), *projectID, status.Revision, entity, *reason, time.Now().UTC())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := databaseStore.ChangeTeamProjectScopeLifecycle(ctx, *projectID, eventAction, resolvedActor, *reason); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	updated, err := databaseStore.TeamProjectScopeStatus(context.Background(), *projectID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, updated, stderr)
}

func verifyTeamContinuityAudit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team audit verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team audit verify [--db <path>]")
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	report, err := databaseStore.VerifyTeamContinuityAudit(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if code := writeIndentedJSON(stdout, report, stderr); code != 0 {
		return code
	}
	if report.Status != "passed" {
		return 1
	}
	return 0
}

func generateTeamIdentity(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team identity generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	privatePath := fs.String("private-key", "", "new private identity file (keep private and back it up securely)")
	publicPath := fs.String("public-key", "", "public identity file to send to the team administrator")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*privatePath) == "" || strings.TrimSpace(*publicPath) == "" || fs.NArg() != 0 || filepath.Clean(*privatePath) == filepath.Clean(*publicPath) {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team identity generate --private-key <path> --public-key <path>")
		return 2
	}
	identity, public, err := teamauth.Generate()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := teamauth.SaveIdentity(*privatePath, *publicPath, identity, public); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	// The public record is safe to distribute; private key bytes are never returned.
	return writeIndentedJSON(stdout, public, stderr)
}

func validateTeamTrust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team trust validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filename := fs.String("file", "", "team-admin-provisioned trust policy JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*filename) == "" || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team trust validate --file <policy.json>")
		return 2
	}
	policy, data, err := teamauth.ReadPolicy(*filename)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	digest := sha256.Sum256(data)
	return writeIndentedJSON(stdout, map[string]any{
		"schema_version": policy.SchemaVersion,
		"member_count":   len(policy.Members),
		"sha256":         hex.EncodeToString(digest[:]),
		"valid":          true,
	}, stderr)
}

func installTeamTrust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team trust install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	filename := fs.String("file", "", "team-admin-provisioned trust policy JSON")
	expectedSHA256 := fs.String("expected-sha256", "", "out-of-band pinned SHA-256 of the policy file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*filename) == "" || len(*expectedSHA256) != 64 || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team trust install --file <policy.json> --expected-sha256 <trusted-digest>")
		return 2
	}
	if _, err := hex.DecodeString(*expectedSHA256); err != nil || strings.ToLower(*expectedSHA256) != *expectedSHA256 {
		_, _ = fmt.Fprintln(stderr, "expected-sha256 must be a lowercase SHA-256 digest")
		return 2
	}
	policy, data, err := teamauth.ReadPolicy(*filename)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	digest := sha256.Sum256(data)
	actualSHA256 := hex.EncodeToString(digest[:])
	if actualSHA256 != *expectedSHA256 {
		_, _ = fmt.Fprintln(stderr, "team trust policy does not match the out-of-band pinned digest")
		return 1
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	target := appdata.TeamTrustPolicyPath(paths.Root)
	if err := writePrivateConfigFile(target, data); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := writePrivateConfigFile(appdata.TeamTrustPolicyDigestPath(paths.Root), []byte(actualSHA256+"\n")); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{
		"installed":    true,
		"member_count": len(policy.Members),
		"sha256":       actualSHA256,
	}, stderr)
}

func writePrivateConfigFile(target string, data []byte) error {
	directory := filepath.Dir(target)
	if err := ensureSafeDirectories(directory); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("existing team trust destination is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".trusted-reviewers-*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

func ensureSafeDirectories(directory string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	var missing []string
	current := filepath.Clean(absolute)
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("team trust destination contains an unsafe directory")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("team trust destination has no existing root")
		}
		current = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(missing[i])
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("team trust destination contains an unsafe directory")
		}
	}
	return nil
}
