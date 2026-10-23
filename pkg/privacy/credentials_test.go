package privacy

import (
	"strings"
	"testing"
)

func TestContainsCredentialDetectsStructuredKeysAndTokenValues(t *testing.T) {
	for _, value := range []string{
		`{"api_key":"abc123"}`,
		`{"nested":{"refresh-token":"value"}}`,
		`Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature`,
		`token=abc123xyz`,
		`{"remote":"https://agent:private-pass@example.invalid/repository.git"}`,
		`{"argv":["tool","--token","opaque-command-line-secret"]}`,
		`{"runnerArgv":["test","--api-key=opaque-inline-secret"]}`,
		`Run the command with --password opaque-prose-secret`,
		`-----BEGIN PRIVATE KEY-----`,
		`ghp_abcdefghijklmnopqrstuvwxyz1234567890`,
	} {
		if !ContainsCredential([]byte(value)) {
			t.Errorf("ContainsCredential(%q) = false, want true", value)
		}
	}
}

func TestContainsCredentialAllowsRedactedExamplesAndOrdinaryText(t *testing.T) {
	for _, value := range []string{
		`{"token":"[redacted]"}`,
		`{"argv":["tool","--token","<your-token>"]}`,
		`{"argv":["tool","--token"]}`,
		`Example: set API_KEY=<your-api-key> in your environment.`,
		`Inspect the request and report whether authentication is configured.`,
		`https://example.invalid/repository.git`,
	} {
		if ContainsCredential([]byte(value)) {
			t.Errorf("ContainsCredential(%q) = true, want false", value)
		}
	}
}

func TestContainsCredentialTextAssignmentHintPreservesCredentialAndPlaceholderDetection(t *testing.T) {
	for _, value := range []string{
		`APIKEY=opaque-secret`,
		`access-token:opaque-secret`,
		`refresh_token=opaque-secret`,
		`id-token='opaque-secret'`,
		`client-secret=opaque-secret`,
		`password:opaque-secret`,
		`passwd=opaque-secret`,
		`authorization:opaque-secret`,
		`cookie=opaque-secret`,
		`privatekey=opaque-secret`,
		`API_KEY=<your-api-key>; token=opaque-secret`,
		`ghp_short sample sk-abcdefghijklmnopqrstuvwxyz1234567890`,
	} {
		if !ContainsCredentialText(value) {
			t.Errorf("ContainsCredentialText(%q) = false, want true", value)
		}
	}
	for _, value := range []string{
		`API_KEY=<your-api-key>`,
		`token=[redacted]`,
		`The authorization behavior is documented; this token is not a value assignment.`,
		strings.Repeat("calibration artifact and evaluation score ", 128),
	} {
		if ContainsCredentialText(value) {
			t.Errorf("ContainsCredentialText(%q) = true, want false", value)
		}
	}
}

func TestContainsCredentialTextPrefiltersLargeHintedValuesWithoutAssignments(t *testing.T) {
	// Compact calibration arrays can be large, opaque strings. Keyword
	// substrings and base64 padding must not force the assignment regexes to
	// scan the entire value when no assignment syntax is present.
	value := "api " + strings.Repeat("calibration-token-metadata-apiary-", 4096) + "=="
	if ContainsCredentialText(value) {
		t.Fatal("large opaque metadata was classified as a credential")
	}

	for _, assignment := range []string{
		`prefix token = opaque-value`,
		`"api_key"   : "opaque-value"`,
		`run --client-secret opaque-value`,
		`-refresh_token opaque-value`,
	} {
		if !ContainsCredentialText(assignment) {
			t.Errorf("ContainsCredentialText(%q) = false, want true", assignment)
		}
	}
	for _, placeholder := range []string{
		`token=[redacted]`,
		`--api-key <your-api-key>`,
	} {
		if ContainsCredentialText(placeholder) {
			t.Errorf("ContainsCredentialText(%q) = true, want false", placeholder)
		}
	}
}

func BenchmarkContainsCredentialTextLongHintedOpaqueValue(b *testing.B) {
	value := "api " + strings.Repeat("calibration-token-metadata-apiary-", 4096) + "=="
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ContainsCredentialText(value) {
			b.Fatal("opaque benchmark input was classified as a credential")
		}
	}
}

func TestContainsCredentialPath(t *testing.T) {
	for _, value := range []string{".env", ".env.local", "keys/id_ed25519", "config/service.pem", ".kube/config", "docs/readme.md"} {
		got := ContainsCredentialPath(value)
		want := value != "docs/readme.md"
		if got != want {
			t.Errorf("ContainsCredentialPath(%q)=%t, want %t", value, got, want)
		}
	}
}
