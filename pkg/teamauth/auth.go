package teamauth

import (
	"crypto/ed25519"
	"crypto/rand"
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
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	PolicySchemaVersionV1    = "threadforge.team-trust-policy.v1"
	PolicySchemaVersion      = "threadforge.team-trust-policy.v2"
	IdentitySchemaVersion    = "threadforge.team-identity.v1"
	PublicKeySchemaVersion   = "threadforge.team-public-identity.v1"
	AttestationSchemaVersion = "threadforge.team-attestation.v1"
	MaxPolicyBytes           = 1 << 20
	MaxMembers               = 1000
)

const (
	RoleProcedureApprove     = "procedure.approve"
	RoleProcedurePublish     = "procedure.publish"
	RolePrivacyReview        = "procedure.privacy-review"
	RoleProcedureRetire      = "procedure.supersede"
	RoleContinuityWrite      = "continuity.write"
	RoleContinuityReview     = "continuity.review"
	RoleRequirementsFinalize = "continuity.requirements.finalize"
	RoleTaskPlanConfirm      = "continuity.plan.confirm"
	RoleTeamScopeManage      = "team.scope.manage"
)

var (
	keyIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	namePattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:@/-]{0,255}$`)
)

type Policy struct {
	SchemaVersion  string   `json:"schema_version"`
	OrganizationID string   `json:"organization_id"`
	Members        []Member `json:"members"`
}

type Member struct {
	Subject   string   `json:"subject"`
	KeyID     string   `json:"key_id"`
	PublicKey string   `json:"public_key"`
	Projects  []string `json:"projects"`
	Roles     []string `json:"roles"`
}

type IdentityFile struct {
	SchemaVersion string `json:"schema_version"`
	KeyID         string `json:"key_id"`
	PrivateKey    string `json:"private_key"`
}

type PublicIdentity struct {
	SchemaVersion string `json:"schema_version"`
	KeyID         string `json:"key_id"`
	PublicKey     string `json:"public_key"`
}

type Identity struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

type Claims struct {
	Action          string `json:"action"`
	ProjectID       string `json:"project_id"`
	ProcedureID     string `json:"procedure_id"`
	Revision        int    `json:"revision"`
	GraphSHA256     string `json:"graph_sha256"`
	PrivacyReviewed bool   `json:"privacy_reviewed"`
}

type Attestation struct {
	SchemaVersion   string `json:"schema_version"`
	Subject         string `json:"subject"`
	KeyID           string `json:"key_id"`
	Action          string `json:"action"`
	ProjectID       string `json:"project_id"`
	ProcedureID     string `json:"procedure_id"`
	Revision        int    `json:"revision"`
	GraphSHA256     string `json:"graph_sha256"`
	PrivacyReviewed bool   `json:"privacy_reviewed"`
	IssuedAt        string `json:"issued_at"`
	Signature       string `json:"signature"`
}

type signedPayload struct {
	SchemaVersion   string `json:"schema_version"`
	Subject         string `json:"subject"`
	KeyID           string `json:"key_id"`
	Action          string `json:"action"`
	ProjectID       string `json:"project_id"`
	ProcedureID     string `json:"procedure_id"`
	Revision        int    `json:"revision"`
	GraphSHA256     string `json:"graph_sha256"`
	PrivacyReviewed bool   `json:"privacy_reviewed"`
	IssuedAt        string `json:"issued_at"`
}

func Generate() (Identity, PublicIdentity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, PublicIdentity{}, err
	}
	keyID := KeyID(publicKey)
	return Identity{KeyID: keyID, PrivateKey: privateKey}, PublicIdentity{
		SchemaVersion: PublicKeySchemaVersion,
		KeyID:         keyID,
		PublicKey:     base64.StdEncoding.EncodeToString(publicKey),
	}, nil
}

func KeyID(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func SaveIdentity(privatePath, publicPath string, identity Identity, public PublicIdentity) error {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize || identity.KeyID == "" || identity.KeyID != public.KeyID || public.SchemaVersion != PublicKeySchemaVersion {
		return errors.New("private and public identity do not match")
	}
	publicKey := identity.PrivateKey.Public().(ed25519.PublicKey)
	if KeyID(publicKey) != identity.KeyID || public.PublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		return errors.New("private and public identity do not match")
	}
	private, err := json.MarshalIndent(IdentityFile{
		SchemaVersion: IdentitySchemaVersion,
		KeyID:         identity.KeyID,
		PrivateKey:    base64.StdEncoding.EncodeToString(identity.PrivateKey),
	}, "", "  ")
	if err != nil {
		return err
	}
	private = append(private, '\n')
	publicBytes, err := json.MarshalIndent(public, "", "  ")
	if err != nil {
		return err
	}
	publicBytes = append(publicBytes, '\n')
	if err := writeNewFile(privatePath, private, 0o600); err != nil {
		return fmt.Errorf("write private identity: %w", err)
	}
	if err := writeNewFile(publicPath, publicBytes, 0o644); err != nil {
		_ = os.Remove(privatePath)
		return fmt.Errorf("write public identity: %w", err)
	}
	return nil
}

func LoadIdentity(filename string) (Identity, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return Identity{}, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return Identity{}, errors.New("private identity file permissions must be restricted to the current user")
	}
	data, err := readRegularFile(filename, 4096)
	if err != nil {
		return Identity{}, err
	}
	var file IdentityFile
	if err := decodeStrict(data, &file); err != nil {
		return Identity{}, fmt.Errorf("decode private identity: %w", err)
	}
	if file.SchemaVersion != IdentitySchemaVersion || !keyIDPattern.MatchString(file.KeyID) {
		return Identity{}, errors.New("unsupported or invalid private identity")
	}
	privateKey, err := base64.StdEncoding.DecodeString(file.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return Identity{}, errors.New("private identity key must be a valid Ed25519 private key")
	}
	identity := Identity{KeyID: file.KeyID, PrivateKey: ed25519.PrivateKey(privateKey)}
	if KeyID(identity.PrivateKey.Public().(ed25519.PublicKey)) != identity.KeyID {
		return Identity{}, errors.New("private identity key identifier does not match its public key")
	}
	return identity, nil
}

func LoadPolicy(filename string) (Policy, error) {
	policy, _, err := ReadPolicy(filename)
	return policy, err
}

func ReadPolicy(filename string) (Policy, []byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return Policy{}, nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return Policy{}, nil, errors.New("team trust policy must not be writable by group or other users")
	}
	data, err := readRegularFile(filename, MaxPolicyBytes)
	if err != nil {
		return Policy{}, nil, err
	}
	var policy Policy
	if err := decodeStrict(data, &policy); err != nil {
		return Policy{}, nil, fmt.Errorf("decode team trust policy: %w", err)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, nil, err
	}
	return policy, data, nil
}

func (p Policy) Validate() error {
	if (p.SchemaVersion != PolicySchemaVersionV1 && p.SchemaVersion != PolicySchemaVersion) || !namePattern.MatchString(p.OrganizationID) {
		return errors.New("unsupported team trust policy or invalid organization_id")
	}
	if len(p.Members) == 0 || len(p.Members) > MaxMembers {
		return fmt.Errorf("team trust policy must contain between 1 and %d members", MaxMembers)
	}
	seen := make(map[string]bool, len(p.Members))
	for i, member := range p.Members {
		field := fmt.Sprintf("members[%d]", i)
		if !namePattern.MatchString(member.Subject) || !keyIDPattern.MatchString(member.KeyID) {
			return fmt.Errorf("%s has an invalid subject or key_id", field)
		}
		if seen[member.KeyID] {
			return fmt.Errorf("%s duplicates a trusted key_id", field)
		}
		seen[member.KeyID] = true
		publicKey, err := base64.StdEncoding.DecodeString(member.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || KeyID(ed25519.PublicKey(publicKey)) != member.KeyID {
			return fmt.Errorf("%s public key does not match key_id", field)
		}
		if len(member.Projects) == 0 || len(member.Projects) > 1000 || len(member.Roles) == 0 || len(member.Roles) > 16 {
			return fmt.Errorf("%s must have bounded project and role grants", field)
		}
		projects := make(map[string]bool, len(member.Projects))
		for _, project := range member.Projects {
			if !namePattern.MatchString(project) || projects[project] {
				return fmt.Errorf("%s has an invalid or duplicate project grant", field)
			}
			projects[project] = true
		}
		roles := make(map[string]bool, len(member.Roles))
		for _, role := range member.Roles {
			if !validPolicyRole(p.SchemaVersion, role) || roles[role] {
				return fmt.Errorf("%s has an unknown or duplicate role grant", field)
			}
			roles[role] = true
		}
	}
	return nil
}

func (p Policy) SubjectFor(keyID string) (string, bool) {
	for _, member := range p.Members {
		if member.KeyID == keyID {
			return member.Subject, true
		}
	}
	return "", false
}

// AuthorizeIdentity checks possession of a trusted private identity and its
// explicit project/role grants. The returned subject is the audit identity to
// record; caller-provided actor labels must not override it.
func (p Policy) AuthorizeIdentity(identity Identity, projectID string, roles ...string) (Member, error) {
	if err := p.Validate(); err != nil {
		return Member{}, fmt.Errorf("invalid team trust policy: %w", err)
	}
	if !namePattern.MatchString(projectID) || len(identity.PrivateKey) != ed25519.PrivateKeySize ||
		!keyIDPattern.MatchString(identity.KeyID) || KeyID(identity.PrivateKey.Public().(ed25519.PublicKey)) != identity.KeyID {
		return Member{}, errors.New("invalid team identity or project scope")
	}
	if len(roles) == 0 {
		return Member{}, errors.New("at least one team role is required")
	}
	for _, role := range roles {
		if !validRole(role) {
			return Member{}, fmt.Errorf("unknown team authorization role %q", role)
		}
	}
	for _, member := range p.Members {
		if member.KeyID != identity.KeyID {
			continue
		}
		if member.PublicKey != base64.StdEncoding.EncodeToString(identity.PrivateKey.Public().(ed25519.PublicKey)) {
			return Member{}, errors.New("team identity public key does not match the trusted member")
		}
		if !contains(member.Projects, projectID) {
			return Member{}, errors.New("team identity is not authorized for this project")
		}
		for _, role := range roles {
			if !contains(member.Roles, role) {
				return Member{}, fmt.Errorf("team identity lacks required role %q", role)
			}
		}
		return member, nil
	}
	return Member{}, errors.New("team identity is not trusted by the installed policy")
}

func Sign(identity Identity, subject string, claims Claims, at time.Time) (Attestation, error) {
	if at.IsZero() {
		return Attestation{}, errors.New("team attestation time is required")
	}
	if len(identity.PrivateKey) != ed25519.PrivateKeySize || KeyID(identity.PrivateKey.Public().(ed25519.PublicKey)) != identity.KeyID {
		return Attestation{}, errors.New("invalid signing identity")
	}
	if !namePattern.MatchString(subject) || !validClaims(claims) {
		return Attestation{}, errors.New("invalid team attestation claims")
	}
	issuedAt := at.UTC().Format(time.RFC3339Nano)
	payload := signedPayload{
		SchemaVersion: AttestationSchemaVersion, Subject: subject, KeyID: identity.KeyID,
		Action: claims.Action, ProjectID: claims.ProjectID, ProcedureID: claims.ProcedureID,
		Revision: claims.Revision, GraphSHA256: claims.GraphSHA256,
		PrivacyReviewed: claims.PrivacyReviewed, IssuedAt: issuedAt,
	}
	message, err := json.Marshal(payload)
	if err != nil {
		return Attestation{}, err
	}
	signature := ed25519.Sign(identity.PrivateKey, message)
	return Attestation{
		SchemaVersion: payload.SchemaVersion, Subject: payload.Subject, KeyID: payload.KeyID,
		Action: payload.Action, ProjectID: payload.ProjectID, ProcedureID: payload.ProcedureID,
		Revision: payload.Revision, GraphSHA256: payload.GraphSHA256,
		PrivacyReviewed: payload.PrivacyReviewed, IssuedAt: payload.IssuedAt,
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, nil
}

func Verify(attestation Attestation, policy Policy, claims Claims) error {
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("invalid team trust policy: %w", err)
	}
	if attestation.SchemaVersion != AttestationSchemaVersion || !namePattern.MatchString(attestation.Subject) ||
		!keyIDPattern.MatchString(attestation.KeyID) || !validClaims(claims) {
		return errors.New("invalid team attestation")
	}
	if attestation.Action != claims.Action || attestation.ProjectID != claims.ProjectID ||
		attestation.ProcedureID != claims.ProcedureID || attestation.Revision != claims.Revision ||
		attestation.GraphSHA256 != claims.GraphSHA256 || attestation.PrivacyReviewed != claims.PrivacyReviewed {
		return errors.New("team attestation does not bind the expected procedure revision")
	}
	if _, err := time.Parse(time.RFC3339Nano, attestation.IssuedAt); err != nil {
		return errors.New("team attestation issued_at must use RFC3339")
	}
	var member *Member
	for i := range policy.Members {
		if policy.Members[i].KeyID == attestation.KeyID {
			member = &policy.Members[i]
			break
		}
	}
	if member == nil || member.Subject != attestation.Subject || !contains(member.Projects, claims.ProjectID) {
		return errors.New("team attestation signer is not trusted for this project")
	}
	for _, role := range requiredRoles(claims.Action, claims.PrivacyReviewed) {
		if !contains(member.Roles, role) {
			return fmt.Errorf("team attestation signer lacks required role %q", role)
		}
	}
	publicKeyBytes, _ := base64.StdEncoding.DecodeString(member.PublicKey)
	signature, err := base64.StdEncoding.DecodeString(attestation.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("team attestation signature is malformed")
	}
	payload := signedPayload{
		SchemaVersion: attestation.SchemaVersion, Subject: attestation.Subject, KeyID: attestation.KeyID,
		Action: attestation.Action, ProjectID: attestation.ProjectID, ProcedureID: attestation.ProcedureID,
		Revision: attestation.Revision, GraphSHA256: attestation.GraphSHA256,
		PrivacyReviewed: attestation.PrivacyReviewed, IssuedAt: attestation.IssuedAt,
	}
	message, err := json.Marshal(payload)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), message, signature) {
		return errors.New("team attestation signature verification failed")
	}
	return nil
}

func validClaims(claims Claims) bool {
	return (claims.Action == "publish" || claims.Action == "supersede") &&
		namePattern.MatchString(claims.ProjectID) && namePattern.MatchString(claims.ProcedureID) &&
		claims.Revision > 0 && keyIDPattern.MatchString(claims.GraphSHA256)
}

func requiredRoles(action string, privacyReviewed bool) []string {
	roles := []string{RoleProcedurePublish}
	if action == "publish" {
		roles = append(roles, RoleProcedureApprove)
	} else {
		roles = append(roles, RoleProcedureRetire)
	}
	if privacyReviewed {
		roles = append(roles, RolePrivacyReview)
	}
	sort.Strings(roles)
	return roles
}

func validRole(role string) bool {
	switch role {
	case RoleProcedureApprove, RoleProcedurePublish, RolePrivacyReview, RoleProcedureRetire,
		RoleContinuityWrite, RoleContinuityReview, RoleRequirementsFinalize, RoleTaskPlanConfirm,
		RoleTeamScopeManage:
		return true
	default:
		return false
	}
}

func validPolicyRole(version, role string) bool {
	if version == PolicySchemaVersionV1 {
		switch role {
		case RoleProcedureApprove, RoleProcedurePublish, RolePrivacyReview, RoleProcedureRetire:
			return true
		default:
			return false
		}
	}
	return version == PolicySchemaVersion && validRole(role)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func readRegularFile(filename string, maxBytes int64) ([]byte, error) {
	if err := ensureDirectories(filepath.Dir(filename), false, 0); err != nil {
		return nil, err
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maxBytes {
		return nil, errors.New("team identity or policy file is not a bounded regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return nil, errors.New("team identity or policy file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	finalInfo, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(data)) > maxBytes ||
		int64(len(data)) != openedInfo.Size() || finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
		return nil, errors.New("team identity or policy file exceeds its size limit")
	}
	return data, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON content")
	}
	return nil
}

func writeNewFile(filename string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(filename)
	if err := ensureDirectories(directory, true, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(filename)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func ensureDirectories(directory string, create bool, mode os.FileMode) error {
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
				return errors.New("team identity or trust path contains an unsafe directory")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if !create {
			return statErr
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("team identity or trust path has no existing root")
		}
		current = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], mode); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(missing[i])
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("team identity or trust path contains an unsafe directory")
		}
	}
	return nil
}
