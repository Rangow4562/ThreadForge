// Package migrationreview defines signed, human-review evidence for legacy
// Mancode plans whose original format did not bind the plan body to a version.
package migrationreview

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	TrustStoreSchemaVersion  = "threadforge.mancode-plan-review-trust.v1"
	AttestationSchemaVersion = "threadforge.mancode-plan-review-attestation.v1"
	ReviewDecision           = "pairing_and_declared_version_confirmed"
	maxTrustStoreBytes       = 1 << 20
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var reviewerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@/-]{0,127}$`)

type TrustStore struct {
	SchemaVersion string        `json:"schema_version"`
	Keys          []ReviewerKey `json:"keys"`
}

type ReviewerKey struct {
	KeyID     string   `json:"key_id"`
	PublicKey string   `json:"public_key"`
	Reviewer  string   `json:"reviewer_id"`
	Products  []string `json:"products"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
}

// Binding commits to the exact migration snapshot and the legacy workflow's
// metadata/plan source sets. A signed review is a present-day reviewer claim,
// not a retroactive assertion that the source format had a historical digest.
type Binding struct {
	Product                      string `json:"product"`
	SourceFingerprint            string `json:"source_fingerprint"`
	SnapshotSHA256               string `json:"snapshot_sha256"`
	WorkflowIDSHA256             string `json:"workflow_id_sha256"`
	MetadataSourceSetSHA256      string `json:"metadata_source_set_sha256"`
	PlanSourceSetSHA256          string `json:"plan_source_set_sha256"`
	MetadataPlanVersion          int64  `json:"metadata_plan_version"`
	MetadataPlanVersionDefaulted bool   `json:"metadata_plan_version_defaulted"`
}

type Attestation struct {
	SchemaVersion string `json:"schema_version"`
	Reviewer      string `json:"reviewer_id"`
	KeyID         string `json:"key_id"`
	Binding
	Decision  string `json:"decision"`
	IssuedAt  string `json:"issued_at"`
	Signature string `json:"signature"`
}

type signedPayload struct {
	SchemaVersion string `json:"schema_version"`
	Reviewer      string `json:"reviewer_id"`
	KeyID         string `json:"key_id"`
	Binding
	Decision string `json:"decision"`
	IssuedAt string `json:"issued_at"`
}

func KeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func ValidateBinding(binding Binding) error {
	if binding.Product != "mancode" || !digestPattern.MatchString(binding.SourceFingerprint) ||
		!digestPattern.MatchString(binding.SnapshotSHA256) || !digestPattern.MatchString(binding.WorkflowIDSHA256) ||
		!digestPattern.MatchString(binding.MetadataSourceSetSHA256) || !digestPattern.MatchString(binding.PlanSourceSetSHA256) ||
		binding.MetadataPlanVersion < 1 || binding.MetadataPlanVersion > 1_000_000 {
		return errors.New("Mancode plan review binding is incomplete or invalid")
	}
	return nil
}

func ValidateTrustStore(trust TrustStore) error {
	if trust.SchemaVersion != TrustStoreSchemaVersion || len(trust.Keys) == 0 || len(trust.Keys) > 256 {
		return errors.New("Mancode plan-review trust store schema or key count is invalid")
	}
	seen := make(map[string]bool, len(trust.Keys))
	for index, key := range trust.Keys {
		publicKey, err := base64.StdEncoding.Strict().DecodeString(key.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || KeyID(ed25519.PublicKey(publicKey)) != key.KeyID || seen[key.KeyID] {
			return fmt.Errorf("reviewer key %d has an invalid or duplicate key identity", index)
		}
		seen[key.KeyID] = true
		if !reviewerPattern.MatchString(key.Reviewer) || len(key.Products) == 0 || len(key.Products) > 4 {
			return fmt.Errorf("reviewer key %s has invalid reviewer or product scope", key.KeyID)
		}
		seenProduct := make(map[string]bool, len(key.Products))
		for _, product := range key.Products {
			if product != "mancode" || seenProduct[product] {
				return fmt.Errorf("reviewer key %s has unknown or duplicate product scope", key.KeyID)
			}
			seenProduct[product] = true
		}
		before, beforeErr := time.Parse(time.RFC3339Nano, key.NotBefore)
		after, afterErr := time.Parse(time.RFC3339Nano, key.NotAfter)
		if beforeErr != nil || afterErr != nil || !before.Before(after) {
			return fmt.Errorf("reviewer key %s requires an increasing RFC3339 validity window", key.KeyID)
		}
	}
	return nil
}

func TrustStoreSHA256(trust TrustStore) (string, error) {
	if err := ValidateTrustStore(trust); err != nil {
		return "", err
	}
	canonical := TrustStore{SchemaVersion: trust.SchemaVersion, Keys: append([]ReviewerKey(nil), trust.Keys...)}
	for index := range canonical.Keys {
		canonical.Keys[index].Products = append([]string(nil), canonical.Keys[index].Products...)
		sort.Strings(canonical.Keys[index].Products)
	}
	sort.Slice(canonical.Keys, func(i, j int) bool { return canonical.Keys[i].KeyID < canonical.Keys[j].KeyID })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ReadTrustStore(filename string) (TrustStore, string, error) {
	var trust TrustStore
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return trust, "", err
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxTrustStoreBytes {
		return trust, "", errors.New("Mancode plan-review trust file must be a regular non-symlink file no larger than 1 MiB")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return trust, "", err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxTrustStoreBytes+1))
	finalInfo, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil || int64(len(content)) != info.Size() ||
		finalInfo.Size() != info.Size() || !finalInfo.ModTime().Equal(info.ModTime()) {
		return trust, "", errors.New("Mancode plan-review trust file changed or could not be read consistently")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&trust); err != nil {
		return trust, "", fmt.Errorf("decode Mancode plan-review trust file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return trust, "", errors.New("Mancode plan-review trust file contains trailing data")
	}
	if err := ValidateTrustStore(trust); err != nil {
		return trust, "", err
	}
	digest, err := TrustStoreSHA256(trust)
	return trust, digest, err
}

func Sign(binding Binding, reviewer string, privateKey ed25519.PrivateKey, issuedAt time.Time) (Attestation, error) {
	if err := ValidateBinding(binding); err != nil {
		return Attestation{}, err
	}
	if !reviewerPattern.MatchString(reviewer) || len(privateKey) != ed25519.PrivateKeySize || issuedAt.IsZero() {
		return Attestation{}, errors.New("Mancode plan-review signer identity or timestamp is invalid")
	}
	attestation := Attestation{
		SchemaVersion: AttestationSchemaVersion, Reviewer: reviewer,
		KeyID: KeyID(privateKey.Public().(ed25519.PublicKey)), Binding: binding,
		Decision: ReviewDecision, IssuedAt: issuedAt.UTC().Format(time.RFC3339Nano),
	}
	message, err := signingBytes(attestation)
	if err != nil {
		return Attestation{}, err
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message))
	return attestation, nil
}

func ValidateAttestation(attestation Attestation) error {
	if attestation.SchemaVersion != AttestationSchemaVersion || attestation.Decision != ReviewDecision ||
		!reviewerPattern.MatchString(attestation.Reviewer) || !digestPattern.MatchString(attestation.KeyID) {
		return errors.New("Mancode plan-review attestation identity or decision is invalid")
	}
	if err := ValidateBinding(attestation.Binding); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt); err != nil {
		return errors.New("Mancode plan-review attestation issued_at must use RFC3339")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("Mancode plan-review signature is malformed")
	}
	return nil
}

func Verify(attestation Attestation, trust TrustStore, expected Binding) error {
	if err := ValidateTrustStore(trust); err != nil {
		return err
	}
	if err := ValidateBinding(expected); err != nil {
		return err
	}
	if err := ValidateAttestation(attestation); err != nil {
		return err
	}
	if attestation.Binding != expected {
		return errors.New("Mancode plan-review attestation does not match the expected source binding")
	}
	issuedAt, _ := time.Parse(time.RFC3339Nano, attestation.IssuedAt)
	var trusted *ReviewerKey
	for index := range trust.Keys {
		if trust.Keys[index].KeyID == attestation.KeyID {
			trusted = &trust.Keys[index]
			break
		}
	}
	if trusted == nil || trusted.Reviewer != attestation.Reviewer || !contains(trusted.Products, expected.Product) {
		return errors.New("Mancode plan-review signing key is not trusted for this reviewer and product")
	}
	notBefore, _ := time.Parse(time.RFC3339Nano, trusted.NotBefore)
	notAfter, _ := time.Parse(time.RFC3339Nano, trusted.NotAfter)
	if issuedAt.Before(notBefore) || issuedAt.After(notAfter) {
		return errors.New("Mancode plan-review attestation was issued outside the trusted key validity window")
	}
	publicKey, _ := base64.StdEncoding.Strict().DecodeString(trusted.PublicKey)
	signature, err := base64.StdEncoding.Strict().DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("Mancode plan-review signature is malformed")
	}
	message, err := signingBytes(attestation)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return errors.New("Mancode plan-review signature verification failed")
	}
	return nil
}

func signingBytes(attestation Attestation) ([]byte, error) {
	return json.Marshal(signedPayload{
		SchemaVersion: attestation.SchemaVersion, Reviewer: attestation.Reviewer, KeyID: attestation.KeyID,
		Binding: attestation.Binding, Decision: attestation.Decision, IssuedAt: attestation.IssuedAt,
	})
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
