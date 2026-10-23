package migrate

import (
	"strings"
	"testing"
)

func TestMancodeSensitiveScannerConfiguredRules(t *testing.T) {
	tests := []struct {
		name string
		rule string
		text string
	}{
		{name: "private key", rule: "pem-private-key", text: "-----BEGIN RSA PRIVATE KEY-----"},
		{name: "authorization header", rule: "authorization-header", text: "Authorization: Bearer sample-token"},
		{name: "cookie header", rule: "cookie-header", text: "Cookie: session=sample"},
		{name: "named secret", rule: "named-secret", text: `--api-key="sample-secret"`},
		{name: "absolute local path", rule: "absolute-local-path", text: `C:\Users\someone\notes.txt`},
		{name: "email", rule: "email-address", text: "person@example.invalid"},
		{name: "vendor key", rule: "vendor-api-key", text: "ghp_abcdefghijklmnopqrstuvwxyz123456"},
		{name: "cloud key", rule: "cloud-access-key", text: "AKIAABCDEFGHIJKLMNOP"},
		{name: "connection password", rule: "connection-password", text: "postgres://alice:sample-password@db.invalid/app"},
		{name: "bearer token", rule: "bearer-token", text: "bearer sample-token-123"},
		{name: "JWT shape", rule: "jwt-token", text: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature"},
		{name: "China mobile", rule: "cn-mobile-phone", text: "+86 138 1234 5678"},
		{name: "China identity card 18", rule: "cn-identity-card-18", text: mancodeSensitiveTestIdentity18()},
		{name: "China identity card 15", rule: "cn-identity-card-15", text: "110105000101001"},
		{name: "payment card", rule: "payment-card", text: "4111111111111111"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			found, err := mancodeContainsSensitiveValue(test.text, map[string]bool{test.rule: true})
			if err != nil || !found {
				t.Fatalf("configured rule failed to detect its contract fixture: found=%t err=%v", found, err)
			}
			found, err = mancodeContainsSensitiveValue(test.text, nil)
			if err != nil || found {
				t.Fatalf("disabled rules unexpectedly blocked their fixture: found=%t err=%v", found, err)
			}
		})
	}
}

func TestMancodeSensitiveScannerRedactionsAndFailClosedBounds(t *testing.T) {
	for _, test := range []struct {
		text string
		rule string
		want bool
	}{
		{text: "api-key=[REDACTED:secret]", rule: "named-secret", want: false},
		{text: "api-key=[REDACTED:unknown]", rule: "named-secret", want: true},
		{text: "contact person@example.invalid", rule: "email-address", want: true},
	} {
		found, err := mancodeSensitiveTextContains(test.text, map[string]bool{test.rule: true})
		if err != nil || found != test.want {
			t.Fatalf("unexpected scan for %q: found=%t err=%v want=%t", test.text, found, err, test.want)
		}
	}
	for _, text := range []string{"contains\x00nul", strings.Repeat("x", maxMancodeSensitiveTextBytes+1)} {
		if _, err := mancodeSensitiveTextContains(text, map[string]bool{"email-address": true}); err == nil {
			t.Fatal("invalid or oversized text did not fail closed")
		}
	}
	if _, err := mancodeSensitiveTextContains("safe", map[string]bool{"unknown-rule": true}); err == nil {
		t.Fatal("unknown enabled rule did not fail closed")
	}
	if found, err := mancodeContainsSensitiveValue(map[string]any{"password": "sample-secret"}, map[string]bool{"named-secret": true}); err != nil {
		t.Fatalf("key-aware scan failed unexpectedly: %v", err)
	} else if !found {
		t.Fatal("key-aware scan missed a named secret")
	}
}

func mancodeSensitiveTestIdentity18() string {
	value := "11010520000101001"
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for index, weight := range weights {
		sum += int(value[index]-'0') * weight
	}
	return value + checks[sum%11:sum%11+1]
}
