package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/store"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	localRecoverySchema = "threadforge.local-store-recovery.v1"
	localRecoveryFile   = "manifest.json"
	localRecoveryDB     = "store.sqlite"
	maxRecoveryManifest = 64 << 10
)

var recoveryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type localRecoveryManifest struct {
	SchemaVersion        string `json:"schema_version"`
	Kind                 string `json:"kind"`
	ID                   string `json:"id"`
	CreatedAt            string `json:"created_at"`
	StoreFile            string `json:"store_file"`
	StoreUserVersion     int    `json:"store_user_version"`
	StoreSizeBytes       int64  `json:"store_size_bytes"`
	StoreSHA256          string `json:"store_sha256"`
	SourceSnapshotID     string `json:"source_snapshot_id,omitempty"`
	SourceSnapshotSHA256 string `json:"source_snapshot_sha256,omitempty"`
	Reason               string `json:"reason,omitempty"`
	ActivationReason     string `json:"activation_reason,omitempty"`
	ActivatedAt          string `json:"activated_at,omitempty"`
	ActivationMethod     string `json:"activation_method,omitempty"`
	TeamProjectCount     *int   `json:"team_project_count,omitempty"`
	TeamScopesFrozen     *int   `json:"team_scopes_frozen,omitempty"`
	TeamAuditStatus      string `json:"team_audit_status,omitempty"`
	ActivationAllowed    bool   `json:"activation_allowed"`
	CurrentStoreSHA256   string `json:"-"`
	StoreMatchesManifest bool   `json:"-"`
}

func runTeamRecovery(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team recovery <snapshot|verify|restore|activate>")
		return 2
	}
	switch args[0] {
	case "snapshot":
		return snapshotLocalStore(args[1:], stdout, stderr)
	case "verify":
		return verifyLocalStoreRecovery(args[1:], stdout, stderr)
	case "restore":
		return restoreLocalStoreSnapshot(args[1:], stdout, stderr)
	case "activate":
		return activateLocalStoreRestore(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown team recovery command %q\n", args[0])
		return 2
	}
}

func snapshotLocalStore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team recovery snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "new local snapshot ID (lowercase letters, digits, hyphens)")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !recoveryIDPattern.MatchString(*id) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team recovery snapshot --id <safe-id> [--db <path>]")
		return 2
	}
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	parent, final, err := localRecoveryPaths(paths.Root, "snapshots", *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "recovery snapshot ID already exists; snapshots are immutable")
		return 1
	}
	stage, err := os.MkdirTemp(parent, ".snapshot-"+*id+"-")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	storePath := filepath.Join(stage, localRecoveryDB)
	if err := createEmptyPrivateFile(storePath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := databaseStore.CreateLocalRecoverySnapshot(context.Background(), storePath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := syncRecoveryFile(storePath); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	info, err := store.InspectLocalRecoveryDatabase(context.Background(), storePath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	manifest := localRecoveryManifest{SchemaVersion: localRecoverySchema, Kind: "snapshot", ID: *id,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), StoreFile: localRecoveryDB,
		StoreUserVersion: info.UserVersion, StoreSizeBytes: info.SizeBytes, StoreSHA256: info.SHA256,
		ActivationAllowed: false}
	if err := writeRecoveryManifest(filepath.Join(stage, localRecoveryFile), manifest); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	verifiedManifest, err := verifyRecoveryDirectory(stage, *id, "snapshot")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, fmt.Errorf("verify newly created snapshot: %w", err))
		return 1
	}
	manifest = verifiedManifest
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "recovery snapshot ID was concurrently created; refusing to overwrite it")
		return 1
	}
	if err := os.Rename(stage, final); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, recoveryReceipt(manifest, final), stderr)
}

func verifyLocalStoreRecovery(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team recovery verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "snapshot or staged restore ID")
	kind := fs.String("kind", "snapshot", "bundle kind: snapshot or restore")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !recoveryIDPattern.MatchString(*id) || (*kind != "snapshot" && *kind != "restore") || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team recovery verify --id <safe-id> [--kind snapshot|restore]")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, directory, err := localRecoveryPaths(paths.Root, recoveryDirectoryKind(*kind), *id)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	manifest, err := verifyRecoveryDirectory(directory, *id, recoveryManifestKind(*kind))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, recoveryReceipt(manifest, directory), stderr)
}

func activateLocalStoreRestore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team recovery activate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	restoreID := fs.String("restore-id", "", "verified staged restore ID")
	activationID := fs.String("activation-id", "", "new ID for the activated copy")
	confirmID := fs.String("confirm-activation-id", "", "must exactly match --activation-id")
	identityPath := fs.String("identity-key", "", "team identity authorized to freeze restored team scopes")
	reason := fs.String("reason", "", "required local activation reason")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !recoveryIDPattern.MatchString(*restoreID) || !recoveryIDPattern.MatchString(*activationID) ||
		*restoreID == *activationID || *activationID != *confirmID || strings.TrimSpace(*reason) != *reason ||
		len(*reason) < 4 || len(*reason) > 1024 || privacy.ContainsCredentialText(*reason) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team recovery activate --restore-id <staged-id> --activation-id <new-id> --confirm-activation-id <same-id> --reason <text> [--identity-key <private-key.json>]")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, sourceDirectory, err := localRecoveryPaths(paths.Root, "restored", *restoreID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	sourceManifest, err := verifyRecoveryDirectory(sourceDirectory, *restoreID, "staged_restore")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, fmt.Errorf("verify staged recovery before activation: %w", err))
		return 1
	}
	parent, final, err := localRecoveryPaths(paths.Root, "restored", *activationID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "activation ID already exists; activated copies are immutable")
		return 1
	}
	stage, err := os.MkdirTemp(parent, ".activate-"+*activationID+"-")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	target := filepath.Join(stage, localRecoveryDB)
	if err := createEmptyPrivateFile(target); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := store.RestoreLocalRecoverySnapshot(context.Background(), filepath.Join(sourceDirectory, localRecoveryDB), target, *activationID); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	databaseStore, err := store.OpenStagedRecovery(target, *activationID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	storeClosed := false
	defer func() {
		if !storeClosed {
			_ = databaseStore.Close()
		}
	}()
	projectIDs, err := databaseStore.TeamProjectScopeIDs(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	frozen, newlyFrozen := 0, 0
	for _, projectID := range projectIDs {
		status, err := databaseStore.TeamProjectScopeStatus(context.Background(), projectID)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if status.Status == "decommissioned" {
			frozen++
			continue
		}
		actor, authorizer, err := authorizeContinuityActor(target, projectID, "team.scope.decommission", "local-user", *identityPath)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if authorizer == nil {
			_, _ = fmt.Fprintf(stderr, "staged team project %q requires a trusted identity to freeze before activation\n", projectID)
			return 1
		}
		entity := store.TeamScopeLifecycleMutation{ProjectID: projectID, OrganizationID: status.OrganizationID,
			FromStatus: "active", ToStatus: "decommissioned", Revision: status.Revision + 1}
		ctx, err := authorizer.withMutationProof(context.Background(), "team.scope.decommission", "decommissioned", "project_scope",
			store.TeamScopeResourceID(projectID), projectID, status.Revision, entity, *reason, time.Now().UTC())
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if err := databaseStore.ChangeTeamProjectScopeLifecycle(ctx, projectID, "decommissioned", actor, *reason); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		frozen++
		newlyFrozen++
	}
	audit, err := databaseStore.VerifyTeamContinuityAudit(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if (len(projectIDs) == 0 && audit.Status != "no_team_projects") ||
		(len(projectIDs) > 0 && audit.Status != "passed") {
		_, _ = fmt.Fprintf(stderr, "team audit status %q does not permit recovery activation\n", audit.Status)
		return 1
	}
	if err := databaseStore.ActivateStagedRecovery(context.Background(), *activationID); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := databaseStore.Close(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	storeClosed = true
	if err := syncRecoveryFile(target); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	info, err := store.InspectLocalRecoveryDatabase(context.Background(), target)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	method := "local-explicit-confirmation"
	if len(projectIDs) > 0 && newlyFrozen > 0 {
		method = "signed-team-scope-freezes"
	} else if len(projectIDs) > 0 {
		method = "verified-existing-team-freezes"
	}
	manifest := localRecoveryManifest{SchemaVersion: localRecoverySchema, Kind: "activated_restore", ID: *activationID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), StoreFile: localRecoveryDB, StoreUserVersion: info.UserVersion,
		StoreSizeBytes: info.SizeBytes, StoreSHA256: info.SHA256, SourceSnapshotID: sourceManifest.SourceSnapshotID,
		SourceSnapshotSHA256: sourceManifest.SourceSnapshotSHA256, Reason: sourceManifest.Reason,
		ActivationReason: *reason, ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano), ActivationMethod: method,
		TeamProjectCount: intPointer(len(projectIDs)), TeamScopesFrozen: intPointer(frozen),
		TeamAuditStatus: audit.Status, ActivationAllowed: true}
	if err := writeRecoveryManifest(filepath.Join(stage, localRecoveryFile), manifest); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	verifiedManifest, err := verifyRecoveryDirectory(stage, *activationID, "activated_restore")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, fmt.Errorf("verify activated recovery copy: %w", err))
		return 1
	}
	manifest = verifiedManifest
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "activation ID was concurrently created; refusing to overwrite it")
		return 1
	}
	if err := os.Rename(stage, final); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, recoveryReceipt(manifest, final), stderr)
}

func restoreLocalStoreSnapshot(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("team recovery restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	snapshotID := fs.String("snapshot-id", "", "verified local snapshot ID")
	restoreID := fs.String("restore-id", "", "new staged restore ID")
	confirmID := fs.String("confirm-restore-id", "", "must exactly match --restore-id")
	reason := fs.String("reason", "", "local recovery reason, retained in the private manifest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !recoveryIDPattern.MatchString(*snapshotID) || !recoveryIDPattern.MatchString(*restoreID) ||
		*restoreID != *confirmID || strings.TrimSpace(*reason) != *reason || len(*reason) < 4 || len(*reason) > 1024 ||
		privacy.ContainsCredentialText(*reason) || fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team recovery restore --snapshot-id <id> --restore-id <new-id> --confirm-restore-id <same-id> --reason <text>")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, sourceDirectory, err := localRecoveryPaths(paths.Root, "snapshots", *snapshotID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	sourceManifest, err := verifyRecoveryDirectory(sourceDirectory, *snapshotID, "snapshot")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, fmt.Errorf("verify source snapshot: %w", err))
		return 1
	}
	parent, final, err := localRecoveryPaths(paths.Root, "restored", *restoreID)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "restore ID already exists; staged restores are immutable")
		return 1
	}
	stage, err := os.MkdirTemp(parent, ".restore-"+*restoreID+"-")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	target := filepath.Join(stage, localRecoveryDB)
	if err := createEmptyPrivateFile(target); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := store.RestoreLocalRecoverySnapshot(context.Background(), filepath.Join(sourceDirectory, localRecoveryDB), target, *restoreID); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := syncRecoveryFile(target); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	info, err := store.InspectLocalRecoveryDatabase(context.Background(), target)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	manifest := localRecoveryManifest{SchemaVersion: localRecoverySchema, Kind: "staged_restore", ID: *restoreID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), StoreFile: localRecoveryDB,
		StoreUserVersion: info.UserVersion, StoreSizeBytes: info.SizeBytes, StoreSHA256: info.SHA256,
		SourceSnapshotID: sourceManifest.ID, SourceSnapshotSHA256: sourceManifest.StoreSHA256,
		Reason: *reason, ActivationAllowed: false}
	if err := writeRecoveryManifest(filepath.Join(stage, localRecoveryFile), manifest); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	verifiedManifest, err := verifyRecoveryDirectory(stage, *restoreID, "staged_restore")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, fmt.Errorf("verify staged restore: %w", err))
		return 1
	}
	manifest = verifiedManifest
	if pathExists(final) {
		_, _ = fmt.Fprintln(stderr, "restore ID was concurrently created; refusing to overwrite it")
		return 1
	}
	if err := os.Rename(stage, final); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, recoveryReceipt(manifest, final), stderr)
}

func localRecoveryPaths(home, kind, id string) (string, string, error) {
	if !recoveryIDPattern.MatchString(id) {
		return "", "", errors.New("invalid local recovery ID")
	}
	root, err := filepath.Abs(home)
	if err != nil {
		return "", "", err
	}
	if strings.HasPrefix(root, `\\`) {
		return "", "", errors.New("local recovery does not support network paths")
	}
	if err := ensureRecoveryDirectory(root); err != nil {
		return "", "", err
	}
	base := filepath.Join(root, "team-recovery")
	if err := ensureRecoveryDirectory(base); err != nil {
		return "", "", err
	}
	parent := filepath.Join(base, kind)
	if err := ensureRecoveryDirectory(parent); err != nil {
		return "", "", err
	}
	return parent, filepath.Join(parent, id), nil
}

func ensureRecoveryDirectory(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("local recovery path must be a real directory, not a symlink")
	}
	if filepath.Base(path) == "team-recovery" || filepath.Base(path) == "snapshots" || filepath.Base(path) == "restored" {
		if err := os.Chmod(path, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("restrict local recovery directory permissions: %w", err)
		}
	}
	return nil
}

func createEmptyPrivateFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func syncRecoveryFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeRecoveryManifest(path string, manifest localRecoveryManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxRecoveryManifest {
		return errors.New("local recovery manifest exceeds its size limit")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func verifyRecoveryDirectory(directory, expectedID, expectedKind string) (localRecoveryManifest, error) {
	var manifest localRecoveryManifest
	dirInfo, err := os.Lstat(directory)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("local recovery bundle directory is missing or unsafe")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 || entries[0].Name() != localRecoveryFile || entries[1].Name() != localRecoveryDB {
		return manifest, errors.New("local recovery bundle must contain only manifest.json and store.sqlite")
	}
	for _, name := range []string{localRecoveryFile, localRecoveryDB} {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return manifest, fmt.Errorf("local recovery bundle file %s is not a regular file", name)
		}
	}
	manifestPath := filepath.Join(directory, localRecoveryFile)
	info, err := os.Lstat(manifestPath)
	if err != nil || info.Size() < 2 || info.Size() > maxRecoveryManifest {
		return manifest, errors.New("local recovery manifest is missing or exceeds its size limit")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, errors.New("local recovery manifest is invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return manifest, errors.New("local recovery manifest contains trailing JSON")
	}
	if err := validateLocalRecoveryManifest(manifest, expectedID, expectedKind); err != nil {
		return manifest, err
	}
	databasePath := filepath.Join(directory, localRecoveryDB)
	databaseInfo, err := store.InspectLocalRecoveryDatabase(context.Background(), databasePath)
	if err != nil {
		return manifest, fmt.Errorf("inspect local recovery database: %w", err)
	}
	if databaseInfo.UserVersion != manifest.StoreUserVersion || databaseInfo.UserVersion != store.CurrentSchemaVersion {
		return manifest, errors.New("local recovery database schema version does not match its manifest")
	}
	manifest.StoreMatchesManifest = databaseInfo.SizeBytes == manifest.StoreSizeBytes && databaseInfo.SHA256 == manifest.StoreSHA256
	manifest.CurrentStoreSHA256 = databaseInfo.SHA256
	if !manifest.StoreMatchesManifest && manifest.Kind != "activated_restore" {
		return manifest, errors.New("local recovery database digest, size, or schema version does not match its manifest")
	}
	switch manifest.Kind {
	case "snapshot":
		if databaseInfo.RecoveryState != "active" {
			return manifest, errors.New("snapshot database is not an active source store")
		}
	case "staged_restore":
		if databaseInfo.RecoveryState != "staged" || databaseInfo.RecoveryID != manifest.ID {
			return manifest, errors.New("staged restore manifest does not match its SQLite activation guard")
		}
	case "activated_restore":
		if databaseInfo.RecoveryState != "active" || databaseInfo.RecoveryID != manifest.ID {
			return manifest, errors.New("activated restore manifest does not match its SQLite activation guard")
		}
	}
	return manifest, nil
}

func validateLocalRecoveryManifest(manifest localRecoveryManifest, expectedID, expectedKind string) error {
	kindMatches := manifest.Kind == expectedKind || expectedKind == "restore" &&
		(manifest.Kind == "staged_restore" || manifest.Kind == "activated_restore")
	if manifest.SchemaVersion != localRecoverySchema || !kindMatches || manifest.ID != expectedID ||
		!recoveryIDPattern.MatchString(manifest.ID) || manifest.StoreFile != localRecoveryDB ||
		manifest.ActivationAllowed != (manifest.Kind == "activated_restore") ||
		manifest.StoreUserVersion != store.CurrentSchemaVersion || manifest.StoreSizeBytes < 1 ||
		manifest.StoreSizeBytes > store.MaxLocalRecoveryDatabaseBytes || !lowercaseSHA256(manifest.StoreSHA256) {
		return errors.New("local recovery manifest fields are invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return errors.New("local recovery manifest timestamp is invalid")
	}
	if expectedKind == "snapshot" {
		if manifest.SourceSnapshotID != "" || manifest.SourceSnapshotSHA256 != "" || manifest.Reason != "" {
			return errors.New("snapshot manifest contains restore-only fields")
		}
	} else if manifest.Kind == "staged_restore" || manifest.Kind == "activated_restore" {
		if !recoveryIDPattern.MatchString(manifest.SourceSnapshotID) || !lowercaseSHA256(manifest.SourceSnapshotSHA256) ||
			len(manifest.Reason) < 4 || len(manifest.Reason) > 1024 || privacy.ContainsCredentialText(manifest.Reason) {
			return errors.New("staged restore manifest source or reason is invalid")
		}
		if manifest.Kind == "staged_restore" {
			if manifest.ActivationReason != "" || manifest.ActivatedAt != "" || manifest.ActivationMethod != "" ||
				manifest.TeamProjectCount != nil || manifest.TeamScopesFrozen != nil || manifest.TeamAuditStatus != "" {
				return errors.New("staged restore manifest contains activation-only fields")
			}
		} else if len(manifest.ActivationReason) < 4 || len(manifest.ActivationReason) > 1024 ||
			privacy.ContainsCredentialText(manifest.ActivationReason) || parseRecoveryTimestamp(manifest.ActivatedAt) != nil ||
			(manifest.ActivationMethod != "signed-team-scope-freezes" && manifest.ActivationMethod != "verified-existing-team-freezes" &&
				manifest.ActivationMethod != "local-explicit-confirmation") ||
			manifest.TeamProjectCount == nil || manifest.TeamScopesFrozen == nil || *manifest.TeamProjectCount < 0 ||
			*manifest.TeamProjectCount > 10000 || *manifest.TeamScopesFrozen != *manifest.TeamProjectCount ||
			(manifest.TeamAuditStatus != "passed" && manifest.TeamAuditStatus != "no_team_projects") ||
			(*manifest.TeamProjectCount == 0) != (manifest.TeamAuditStatus == "no_team_projects") ||
			(*manifest.TeamProjectCount == 0 && manifest.ActivationMethod != "local-explicit-confirmation") ||
			(*manifest.TeamProjectCount > 0 && manifest.ActivationMethod == "local-explicit-confirmation") {
			return errors.New("activated restore manifest fields are invalid")
		}
	} else {
		return errors.New("unsupported local recovery bundle kind")
	}
	return nil
}

func parseRecoveryTimestamp(value string) error {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err
}

func intPointer(value int) *int { return &value }

func lowercaseSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func recoveryDirectoryKind(kind string) string {
	if kind == "restore" {
		return "restored"
	}
	return "snapshots"
}

func recoveryManifestKind(kind string) string {
	if kind == "restore" {
		return "restore"
	}
	return "snapshot"
}

func recoveryReceipt(manifest localRecoveryManifest, directory string) map[string]any {
	currentDigest := manifest.CurrentStoreSHA256
	if currentDigest == "" {
		currentDigest = manifest.StoreSHA256
	}
	status := "verified"
	storeMatchesManifest := manifest.StoreMatchesManifest || manifest.Kind != "activated_restore"
	if !storeMatchesManifest {
		status = "modified_since_activation"
	}
	receipt := map[string]any{
		"schema_version":         manifest.SchemaVersion,
		"kind":                   manifest.Kind,
		"id":                     manifest.ID,
		"directory":              directory,
		"store_size_bytes":       manifest.StoreSizeBytes,
		"store_sha256":           manifest.StoreSHA256,
		"current_store_sha256":   currentDigest,
		"store_matches_manifest": storeMatchesManifest,
		"status":                 status,
		"store_user_version":     manifest.StoreUserVersion,
		"activation_allowed":     manifest.ActivationAllowed,
	}
	if manifest.Kind == "activated_restore" {
		receipt["activation_reason"] = manifest.ActivationReason
		receipt["activated_at"] = manifest.ActivatedAt
		receipt["activation_method"] = manifest.ActivationMethod
		receipt["team_project_count"] = manifest.TeamProjectCount
		receipt["team_scopes_frozen"] = manifest.TeamScopesFrozen
		receipt["team_audit_status"] = manifest.TeamAuditStatus
	}
	return receipt
}
