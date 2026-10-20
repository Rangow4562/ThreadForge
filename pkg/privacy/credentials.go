package privacy

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

var (
	credentialAssignment     = regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|client[_-]?secret|password|passwd|authorization|cookie|private[_-]?key)["']?\s*[:=]\s*["']?([^\s"',;]+)`)
	credentialFlagAssignment = regexp.MustCompile(`(?i)(?:^|\s)--?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|token|client[_-]?secret|password|passwd|authorization|cookie|private[_-]?key)\s+([^\s"',;]+)`)
	credentialURLPattern     = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@/\s]+@`)
	bearerTokenPattern       = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{12,}`)
	privateKeyPattern        = regexp.MustCompile(`(?i)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)
	knownTokenPattern        = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b|\bAKIA[A-Z0-9]{16}\b|\bxox[baprs]-[A-Za-z0-9-]{20,}\b|\bAIza[0-9A-Za-z_-]{30,}\b|\bsk-[A-Za-z0-9_-]{20,}\b`)
	knownTokenHints          = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "AKIA", "xoxb-", "xoxa-", "xoxp-", "xoxr-", "xoxs-", "AIza", "sk-"}
)

// ContainsCredential checks structured credential field names and secret-like values.
func ContainsCredential(data []byte) bool {
	var value any
	if json.Unmarshal(data, &value) == nil {
		return containsCredentialValue(value)
	}
	return ContainsCredentialText(string(data))
}

// ContainsCredentialText checks free text for explicit secret assignments and known token formats.
func ContainsCredentialText(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "://") && strings.Contains(text, "@") && credentialURLPattern.MatchString(text) {
		return true
	}
	if strings.Contains(lower, "bearer") && bearerTokenPattern.MatchString(text) {
		return true
	}
	if strings.Contains(lower, "-----begin") && privateKeyPattern.MatchString(text) {
		return true
	}
	hasKnownTokenHint := false
	for _, hint := range knownTokenHints {
		if strings.Contains(text, hint) {
			hasKnownTokenHint = true
			break
		}
	}
	if hasKnownTokenHint && knownTokenPattern.MatchString(text) {
		return true
	}
	if !hasCredentialAssignmentHint(lower) {
		return false
	}
	for _, match := range credentialAssignment.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || !isPlaceholder(match[1]) {
			return true
		}
	}
	for _, match := range credentialFlagAssignment.FindAllStringSubmatch(text, -1) {
		if len(match) < 2 || !isPlaceholder(match[1]) {
			return true
		}
	}
	return false
}

// hasCredentialAssignmentHint avoids running the broader assignment expressions
// over ordinary prose and model output. Every key accepted by those expressions
// contains at least one of these case-folded fragments; the token-value patterns
// above are checked independently before this conservative fast path.
func hasCredentialAssignmentHint(lower string) bool {
	for _, hint := range []string{"api", "token", "secret", "password", "passwd", "authorization", "cookie", "private"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

// ContainsCredentialPath identifies repository-relative paths that may point
// to credentials or private key material. It does not access the filesystem.
func ContainsCredentialPath(value string) bool {
	for _, segment := range strings.Split(strings.ToLower(path.Clean(strings.ReplaceAll(value, "\\", "/"))), "/") {
		if segment == ".env" || strings.HasPrefix(segment, ".env.") || strings.Contains(segment, "credential") ||
			strings.Contains(segment, "secret") || strings.Contains(segment, "private-key") || strings.Contains(segment, "private_key") ||
			segment == "id_rsa" || segment == "id_ed25519" || strings.Contains(segment, "auth-token") || segment == ".kube" ||
			strings.HasSuffix(segment, ".pem") || strings.HasSuffix(segment, ".p12") || strings.HasSuffix(segment, ".pfx") ||
			strings.HasSuffix(segment, ".key") || strings.HasSuffix(segment, ".keystore") {
			return true
		}
	}
	return false
}

func containsCredentialValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			compact := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
			if isCredentialField(compact) && !isEmptyOrRedacted(item) {
				return true
			}
			if (compact == "argv" || compact == "runnerargv") && containsCredentialArguments(item) {
				return true
			}
			if containsCredentialValue(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsCredentialValue(item) {
				return true
			}
		}
	case string:
		return ContainsCredentialText(typed)
	}
	return false
}

func containsCredentialArguments(value any) bool {
	arguments, ok := value.([]any)
	if !ok {
		return false
	}
	for index, argument := range arguments {
		text, ok := argument.(string)
		if !ok {
			continue
		}
		flag := strings.TrimLeft(text, "-")
		if split := strings.IndexByte(flag, '='); split >= 0 {
			flag = flag[:split]
			if isCredentialField(strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(flag))) {
				value := text[strings.IndexByte(text, '=')+1:]
				if value != "" && !isPlaceholder(value) {
					return true
				}
			}
			continue
		}
		compact := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(flag))
		if isCredentialField(compact) && index+1 < len(arguments) {
			if next, ok := arguments[index+1].(string); ok && !isPlaceholder(next) {
				return true
			}
		}
	}
	return false
}

func isCredentialField(compact string) bool {
	switch compact {
	case "password", "passwd", "secret", "apikey", "token", "accesstoken", "refreshtoken", "idtoken",
		"authorization", "cookie", "setcookie", "credentials", "credential", "privatekey", "clientsecret":
		return true
	default:
		return false
	}
}

func isEmptyOrRedacted(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return isPlaceholder(typed)
	default:
		return false
	}
}

func isPlaceholder(value string) bool {
	value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), `"'`)
	switch value {
	case "", "redacted", "[redacted]", "<redacted>", "${api_key}", "${token}", "your_api_key", "your-token", "changeme", "change-me", "example", "placeholder", "...":
		return true
	default:
		return strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">")
	}
}
