package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/migrate"
	"github.com/threadforge-ai/threadforge/pkg/migrationreview"
	"github.com/threadforge-ai/threadforge/pkg/migrationsources"
	"github.com/threadforge-ai/threadforge/pkg/strictjson"
	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

type mancodeSourceAuthorityResult struct {
	SchemaVersion             string                                     `json:"schema_version"`
	Product                   string                                     `json:"product"`
	ReviewerID                string                                     `json:"reviewer_id"`
	KeyID                     string                                     `json:"key_id"`
	Attestation               migrationreview.SourceAuthorityAttestation `json:"attestation"`
	AttestationPath           string                                     `json:"attestation_path"`
	AttestationArtifactSHA256 string                                     `json:"attestation_artifact_sha256"`
	SourceSetArtifactSHA256   string                                     `json:"source_set_artifact_sha256"`
	InventorySHA256           string                                     `json:"inventory_sha256"`
	ComponentCount            int                                        `json:"component_count"`
	SourceSetRecomputed       bool                                       `json:"source_set_recomputed"`
	TrustedSignatureVerified  bool                                       `json:"trusted_signature_verified"`
	ReviewerAssertionSigned   bool                                       `json:"reviewer_assertion_signed"`
	HumanReviewRequired       bool                                       `json:"human_review_required"`
	ExecutionAuthorized       bool                                       `json:"execution_authorized"`
	CutoverReady              bool                                       `json:"cutover_ready"`
}

func attestMancodeSourceSetAuthority(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate attest-source-set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "Mancode project root used to rediscover all local source roots")
	var manifests repeatedMigrationManifest
	fs.Var(&manifests, "manifest", "verified local Mancode component snapshot; repeat for every discovered source root")
	sourceSetPath := fs.String("source-set", "", "path-free JSON emitted by migrate source-set")
	evidenceRootPath := fs.String("evidence-root", "", "existing cutover evidence directory")
	attestationPath := fs.String("attestation-path", "", "new relative output path under evidence-root")
	identityPath := fs.String("identity-key", "", "dedicated source-authority reviewer's private Ed25519 identity")
	trustPath := fs.String("trusted-reviewers", "", "out-of-band migration-review trust policy with source-authority purpose")
	confirm := fs.Bool("confirm-authoritative-root-inventory", false, "confirm the complete discovered root set is authoritative after independent review")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*root) == "" || len(manifests) == 0 || strings.TrimSpace(*sourceSetPath) == "" ||
		strings.TrimSpace(*evidenceRootPath) == "" || strings.TrimSpace(*attestationPath) == "" ||
		strings.TrimSpace(*identityPath) == "" || strings.TrimSpace(*trustPath) == "" || !*confirm {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge migrate attest-source-set --root <mancode-project-root> --manifest <snapshot\\manifest.json> [--manifest <coordination-snapshot\\manifest.json> ...] --source-set <source-set.json> --evidence-root <cutover-evidence-dir> --attestation-path <new-relative-path.json> --identity-key <private-key.json> --trusted-reviewers <external-trust.json> --confirm-authoritative-root-inventory")
		return 2
	}

	sourceRoot, err := canonicalDirectory(*root, "Mancode project root")
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	evidenceRoot, err := canonicalDirectory(*evidenceRootPath, "evidence-root")
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	if rootsOverlap(sourceRoot, evidenceRoot) {
		return sourceAuthorityError(stderr, errors.New("evidence-root must be separate from the Mancode source tree"))
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	dataRoot, err := canonicalDirectory(paths.Root, "Threadforge local data root")
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	if rootsOverlap(sourceRoot, dataRoot) || rootsOverlap(evidenceRoot, dataRoot) {
		return sourceAuthorityError(stderr, errors.New("source, evidence, and Threadforge local data roots must be separate"))
	}

	verifiedManifests := make([]string, 0, len(manifests))
	for _, name := range manifests {
		manifestPath, err := validateLocalSnapshotManifest(name, paths.Root)
		if err != nil {
			return sourceAuthorityError(stderr, err)
		}
		verifiedManifests = append(verifiedManifests, manifestPath)
	}
	sourceSetFile, err := canonicalRegularFile(*sourceSetPath, "source-set inventory", 4<<20)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	sourceSetContent, err := readRegularNoLinks(sourceSetFile, 4<<20)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	var sourceSet migrationsources.MancodeReport
	if err := strictjson.Decode(sourceSetContent, &sourceSet); err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("decode source-set inventory: %w", err))
	}
	if err := migrationsources.ValidateMancode(sourceSet); err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("validate source-set inventory: %w", err))
	}
	currentSourceSet, err := migrate.BuildMancodeSourceSet(sourceRoot, verifiedManifests)
	if err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("recompute discovered Mancode source set: %w", err))
	}
	if currentSourceSet.InventorySHA256 != sourceSet.InventorySHA256 ||
		currentSourceSet.ProjectSourceFingerprint != sourceSet.ProjectSourceFingerprint ||
		currentSourceSet.ProjectSnapshotSHA256 != sourceSet.ProjectSnapshotSHA256 ||
		len(currentSourceSet.Components) != len(sourceSet.Components) {
		return sourceAuthorityError(stderr, errors.New("source-set inventory is stale or does not match the complete currently discovered roots"))
	}

	identityFile, err := canonicalRegularFile(*identityPath, "source-authority reviewer identity", 4096)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	trustFile, err := canonicalRegularFile(*trustPath, "trusted migration-review policy", 1<<20)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	for label, name := range map[string]string{"reviewer identity": identityFile, "trusted reviewer policy": trustFile} {
		if pathIsWithin(sourceRoot, name) || pathIsWithin(dataRoot, name) || pathIsWithin(evidenceRoot, name) {
			return sourceAuthorityError(stderr, fmt.Errorf("%s must be outside source, evidence, and Threadforge data roots", label))
		}
	}
	if samePath(identityFile, trustFile) || samePath(identityFile, sourceSetFile) || samePath(trustFile, sourceSetFile) {
		return sourceAuthorityError(stderr, errors.New("source-set, identity, and trusted reviewer inputs must be separate files"))
	}
	identity, err := teamauth.LoadIdentity(identityFile)
	if err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("load source-authority reviewer identity: %w", err))
	}
	trust, trustDigest, err := migrationreview.ReadTrustStore(trustFile)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	reviewer := ""
	for _, key := range trust.Keys {
		if key.KeyID == identity.KeyID && key.Reviewer != "" {
			for _, purpose := range key.Purposes {
				if purpose == migrationreview.PurposeSourceAuthority {
					reviewer = key.Reviewer
					break
				}
			}
			break
		}
	}
	if reviewer == "" {
		return sourceAuthorityError(stderr, errors.New("signing identity must be explicitly trusted for the Mancode source-authority purpose"))
	}

	attestationRelative, err := validateRelativeEvidencePath(*attestationPath)
	if err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("attestation-path: %w", err))
	}
	attestationTarget, err := validateNewEvidenceTarget(evidenceRoot, attestationRelative)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	if pathIsWithin(sourceRoot, attestationTarget) || pathIsWithin(dataRoot, attestationTarget) ||
		samePath(attestationTarget, identityFile) || samePath(attestationTarget, trustFile) || samePath(attestationTarget, sourceSetFile) {
		return sourceAuthorityError(stderr, errors.New("attestation output must not overwrite or be placed inside a source or trust input"))
	}
	binding, err := migrationreview.SourceAuthorityBindingForArtifact(sourceSetContent, sourceSet.InventorySHA256,
		sourceSet.ProjectSourceFingerprint, sourceSet.ProjectSnapshotSHA256, len(sourceSet.Components))
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	issuedAt := time.Now().UTC()
	attestation, err := migrationreview.SignSourceAuthority(binding, reviewer, identity.PrivateKey, issuedAt)
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	if err := migrationreview.VerifySourceAuthority(attestation, trust, binding); err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("source-authority signature is not trusted: %w", err))
	}
	encoded, err := json.MarshalIndent(attestation, "", "  ")
	if err != nil {
		return sourceAuthorityError(stderr, err)
	}
	encoded = append(encoded, '\n')
	attestationDigest := sha256.Sum256(encoded)

	if _, freshTrustDigest, trustErr := migrationreview.ReadTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		return sourceAuthorityError(stderr, errors.New("trusted reviewer policy changed while preparing source-authority attestation"))
	}
	if freshIdentity, identityErr := teamauth.LoadIdentity(identityFile); identityErr != nil || freshIdentity.KeyID != identity.KeyID || !freshIdentity.PrivateKey.Equal(identity.PrivateKey) {
		return sourceAuthorityError(stderr, errors.New("source-authority reviewer identity changed while preparing attestation"))
	}
	currentSourceSet, err = migrate.BuildMancodeSourceSet(sourceRoot, verifiedManifests)
	if err != nil || currentSourceSet.InventorySHA256 != sourceSet.InventorySHA256 {
		return sourceAuthorityError(stderr, errors.New("Mancode source roots changed while preparing authority attestation"))
	}
	if err := writeExclusiveEvidence(evidenceRoot, attestationRelative, encoded); err != nil {
		return sourceAuthorityError(stderr, fmt.Errorf("write source-authority attestation: %w", err))
	}
	if _, freshTrustDigest, trustErr := migrationreview.ReadTrustStore(trustFile); trustErr != nil || freshTrustDigest != trustDigest {
		removeCreatedFileIfDigest(attestationTarget, attestationDigest[:])
		return sourceAuthorityError(stderr, errors.New("trusted reviewer policy changed while writing source-authority attestation"))
	}
	if freshIdentity, identityErr := teamauth.LoadIdentity(identityFile); identityErr != nil || freshIdentity.KeyID != identity.KeyID || !freshIdentity.PrivateKey.Equal(identity.PrivateKey) {
		removeCreatedFileIfDigest(attestationTarget, attestationDigest[:])
		return sourceAuthorityError(stderr, errors.New("source-authority reviewer identity changed while writing attestation"))
	}
	currentSourceSet, err = migrate.BuildMancodeSourceSet(sourceRoot, verifiedManifests)
	if err != nil || currentSourceSet.InventorySHA256 != sourceSet.InventorySHA256 {
		removeCreatedFileIfDigest(attestationTarget, attestationDigest[:])
		return sourceAuthorityError(stderr, errors.New("Mancode source roots changed while writing authority attestation"))
	}
	result := mancodeSourceAuthorityResult{
		SchemaVersion: "threadforge.mancode-source-authority-result.v1", Product: "mancode", ReviewerID: reviewer,
		KeyID: attestation.KeyID, Attestation: attestation, AttestationPath: attestationRelative,
		AttestationArtifactSHA256: hex.EncodeToString(attestationDigest[:]),
		SourceSetArtifactSHA256:   binding.SourceSetArtifactSHA256, InventorySHA256: binding.InventorySHA256,
		ComponentCount: binding.ComponentCount, SourceSetRecomputed: true, TrustedSignatureVerified: true,
		ReviewerAssertionSigned: true, HumanReviewRequired: true, ExecutionAuthorized: false, CutoverReady: false,
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func sourceAuthorityError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err)
	return 1
}
