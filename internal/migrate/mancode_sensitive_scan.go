package migrate

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxMancodeSensitiveTextBytes = 1 << 20
	maxMancodeSensitiveNodes     = 100_000
	maxMancodeSensitiveMatches   = 4096
	maxMancodeSensitiveScanTime  = 500 * time.Millisecond
)

var (
	mancodeSensitivePrivateKeyRE         = regexp.MustCompile(`(?i)-----BEGIN(?: [A-Z0-9]+)? PRIVATE KEY-----`)
	mancodeSensitiveAuthHeaderRE         = regexp.MustCompile(`(?i)\b(?:authorization|proxy-authorization)\s*:\s*(?:bearer|basic|token)\s+[^\s,;]+`)
	mancodeSensitiveCookieHeaderRE       = regexp.MustCompile(`(?i)\b(?:set-cookie|cookie)\s*:\s*[^\r\n]+`)
	mancodeSensitiveNamedAssignmentRE    = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(?:-{1,2})?([A-Za-z][A-Za-z0-9_-]{0,127})(?:"\s*|'\s*|\s*)[=:]\s*`)
	mancodeSensitiveNamedSuffixRE        = regexp.MustCompile(`(?i)(?:^|[_-])(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)$`)
	mancodeSensitiveAbsolutePathRE       = regexp.MustCompile("(?:\\b[A-Z]:\\\\|/(?:Users|home|private|var/folders|tmp|etc)/)[^\\s'\"`]+")
	mancodeSensitiveEmailRE              = regexp.MustCompile(`(^|[^A-Za-z0-9._%+-])([A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,253}\.[A-Za-z]{2,63})([^A-Za-z0-9_%+-]|$)`)
	mancodeSensitiveVendorKeyRE          = regexp.MustCompile(`(^|[^A-Za-z0-9_-])((?:gh[pousr]_[A-Za-z0-9]{20,255}|github_pat_[A-Za-z0-9_]{20,255}|sk-(?:proj-|ant-)?[A-Za-z0-9_-]{16,255}|(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,255}|AIza[A-Za-z0-9_-]{30,64}|xox[baprs]-[A-Za-z0-9-]{10,255}))([^A-Za-z0-9_-]|$)`)
	mancodeSensitiveCloudKeyRE           = regexp.MustCompile(`(^|[^A-Za-z0-9])((?:AKIA[A-Z0-9]{16}|ASIA[A-Z0-9]{16}|LTAI[A-Za-z0-9]{12,32}|AKID[A-Za-z0-9]{13,40}))([^A-Za-z0-9]|$)`)
	mancodeSensitiveConnectionPasswordRE = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{0,31}://[^\s:@/]{1,255}:([^\s@/]+)@`)
	mancodeSensitiveBearerRE             = regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._~+/-]+=*)`)
	mancodeSensitiveJWTokenRE            = regexp.MustCompile(`(^|[^A-Za-z0-9_-])(eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*)([^A-Za-z0-9_-]|$)`)
	mancodeSensitivePhoneRE              = regexp.MustCompile(`(^|[^A-Za-z0-9_])((?:(?:\+?86|0086|[（(]\+?86[）)])(?:[ -]?)?)?1[3-9][0-9](?:[ -][0-9]{4}[ -][0-9]{4}|[0-9]{8}))([^A-Za-z0-9_]|$)`)
	mancodeSensitiveIdentity18RE         = regexp.MustCompile(`(^|[^A-Za-z0-9_])([0-9]{17}[0-9Xx])([^A-Za-z0-9_]|$)`)
	mancodeSensitiveIdentity15RE         = regexp.MustCompile(`(^|[^A-Za-z0-9_])([0-9]{15})([^A-Za-z0-9_]|$)`)
	mancodeSensitivePaymentCardRE        = regexp.MustCompile(`(^|[^A-Za-z0-9_])((?:[2-6][0-9]{12,18}|[2-6][0-9]{3}[ -][0-9]{4}[ -][0-9]{4}[ -][0-9]{1,4}(?:[ -][0-9]{3})?|[3-6][0-9]{3}[ -][0-9]{6}[ -][0-9]{5}))([^A-Za-z0-9_]|$)`)
	mancodeSensitivePhonePrefixRE        = regexp.MustCompile(`^(?:0086|\+?86|[（(]\+?86[）)])`)
	mancodeSensitiveRedactionRE          = regexp.MustCompile(`^\[REDACTED(?::([a-z_,]{1,256}))?\]$`)
)

var mancodeSensitiveRedactionCategories = map[string]bool{
	"private_key": true, "authorization": true, "cookie": true, "secret": true,
	"absolute_path": true, "email": true, "vendor_key": true, "cloud_key": true,
	"connection_password": true, "jwt": true, "phone": true, "identity_card": true,
	"payment_card": true,
}

var mancodeChinaProvinces = map[string]bool{
	"11": true, "12": true, "13": true, "14": true, "15": true,
	"21": true, "22": true, "23": true, "31": true, "32": true,
	"33": true, "34": true, "35": true, "36": true, "37": true,
	"41": true, "42": true, "43": true, "44": true, "45": true,
	"46": true, "50": true, "51": true, "52": true, "53": true,
	"54": true, "61": true, "62": true, "63": true, "64": true,
	"65": true, "71": true, "81": true, "82": true,
}

func mancodeGitRefPrivacyViolation(value any, guard mancodeGitRefPrivacyGuard) error {
	if len(guard.excludedDigests) > 0 {
		digest, err := mancodeCanonicalDigest(value)
		if err != nil {
			return errors.New("Mancode privacy exclusion digest could not be checked")
		}
		if guard.excludedDigests[digest] {
			return errors.New("Mancode privacy exclusion blocks this entity")
		}
	}
	if !guard.enabled {
		return nil
	}
	contains, err := mancodeContainsSensitiveValue(value, guard.ruleIDs)
	if err != nil {
		return errors.New("Mancode sensitive-text scan could not be completed")
	}
	if contains {
		return errors.New("Mancode sensitive-text policy blocks this entity")
	}
	return nil
}

func mancodeContainsSensitiveValue(value any, enabledRules map[string]bool) (bool, error) {
	type frame struct {
		value any
		key   string
	}
	stack := []frame{{value: value}}
	visited := 0
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		visited++
		if visited > maxMancodeSensitiveNodes {
			return false, errors.New("privacy scan node limit exceeded")
		}
		switch typed := current.value.(type) {
		case string:
			if len(typed) == 0 {
				continue
			}
			text := typed
			if current.key != "" {
				text = current.key + ": " + typed
			}
			found, err := mancodeSensitiveTextContains(text, enabledRules)
			if err != nil {
				return false, err
			}
			if found {
				return true, nil
			}
		case []any:
			for _, item := range typed {
				stack = append(stack, frame{value: item, key: current.key})
			}
		case map[string]any:
			for key, item := range typed {
				stack = append(stack, frame{value: key}, frame{value: item, key: key})
			}
		}
	}
	return false, nil
}

func mancodeSensitiveTextContains(text string, enabledRules map[string]bool) (bool, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len(text) > maxMancodeSensitiveTextBytes {
		return false, errors.New("privacy scan text is invalid or oversized")
	}
	for rule := range enabledRules {
		if !mancodePrivacyRuleIDs[rule] {
			return false, errors.New("privacy scan rule is unsupported")
		}
	}
	started := time.Now()
	for _, rule := range []string{
		"pem-private-key", "authorization-header", "cookie-header", "named-secret", "absolute-local-path",
		"email-address", "vendor-api-key", "cloud-access-key", "connection-password", "bearer-token",
		"jwt-token", "cn-mobile-phone", "cn-identity-card-18", "cn-identity-card-15", "payment-card",
	} {
		if !enabledRules[rule] {
			continue
		}
		found, err := mancodeSensitiveRuleContains(text, rule, started)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func mancodeSensitiveRuleContains(text, rule string, started time.Time) (bool, error) {
	if time.Since(started) > maxMancodeSensitiveScanTime {
		return false, errors.New("privacy scan time budget exceeded")
	}
	switch rule {
	case "pem-private-key":
		return mancodeSensitivePrivateKeyRE.MatchString(text), nil
	case "authorization-header":
		return mancodeSensitiveAuthHeaderRE.MatchString(text), nil
	case "cookie-header":
		return mancodeSensitiveCookieHeaderRE.MatchString(text), nil
	case "named-secret":
		return mancodeSensitiveNamedSecret(text, started)
	case "absolute-local-path":
		return mancodeSensitiveAbsolutePathRE.MatchString(text), nil
	case "email-address":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveEmailRE, 2, nil, started)
	case "vendor-api-key":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveVendorKeyRE, 2, nil, started)
	case "cloud-access-key":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveCloudKeyRE, 2, nil, started)
	case "connection-password":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveConnectionPasswordRE, 1, mancodeConnectionPasswordValid, started)
	case "bearer-token":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveBearerRE, 1, nil, started)
	case "jwt-token":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveJWTokenRE, 2, mancodeJWTShapeValid, started)
	case "cn-mobile-phone":
		return mancodeSensitiveCaptureContains(text, mancodeSensitivePhoneRE, 2, mancodeChinaPhoneValid, started)
	case "cn-identity-card-18":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveIdentity18RE, 2, mancodeChinaIdentityValid, started)
	case "cn-identity-card-15":
		return mancodeSensitiveCaptureContains(text, mancodeSensitiveIdentity15RE, 2, mancodeChinaIdentityValid, started)
	case "payment-card":
		return mancodeSensitiveCaptureContains(text, mancodeSensitivePaymentCardRE, 2, mancodePaymentCardValid, started)
	default:
		return false, errors.New("privacy rule is unsupported")
	}
}

func mancodeSensitiveCaptureContains(text string, pattern *regexp.Regexp, group int, validate func(string) bool, started time.Time) (bool, error) {
	matches := pattern.FindAllStringSubmatchIndex(text, maxMancodeSensitiveMatches+1)
	if len(matches) > maxMancodeSensitiveMatches {
		return false, errors.New("privacy scan match limit exceeded")
	}
	for _, match := range matches {
		if time.Since(started) > maxMancodeSensitiveScanTime {
			return false, errors.New("privacy scan time budget exceeded")
		}
		start := 0
		if group > 0 {
			start = group * 2
		}
		if len(match) <= start+1 || match[start] < 0 || match[start+1] < 0 {
			continue
		}
		candidate := text[match[start]:match[start+1]]
		if mancodeSensitiveRedacted(candidate) || validate != nil && !validate(candidate) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func mancodeConnectionPasswordValid(value string) bool {
	return len(value) <= 4096
}

func mancodeSensitiveNamedSecret(text string, started time.Time) (bool, error) {
	matches := mancodeSensitiveNamedAssignmentRE.FindAllStringSubmatchIndex(text, maxMancodeSensitiveMatches+1)
	if len(matches) > maxMancodeSensitiveMatches {
		return false, errors.New("privacy scan match limit exceeded")
	}
	for _, match := range matches {
		if time.Since(started) > maxMancodeSensitiveScanTime {
			return false, errors.New("privacy scan time budget exceeded")
		}
		if len(match) < 6 || match[4] < 0 || match[5] < 0 || match[1] < 0 {
			continue
		}
		name := text[match[4]:match[5]]
		if !mancodeSensitiveNamedSuffixRE.MatchString(name) {
			continue
		}
		start := match[1]
		if start >= len(text) {
			continue
		}
		quote := text[start]
		valueStart, end := start, start
		if quote == '"' || quote == '\'' {
			valueStart++
			end = valueStart
			for end < len(text) && text[end] != quote {
				if text[end] == '\\' && end+1 < len(text) {
					end += 2
				} else {
					end++
				}
			}
		} else {
			for end < len(text) && !strings.ContainsRune(" \t\r\n'\";,", rune(text[end])) {
				end++
			}
		}
		if end <= valueStart {
			continue
		}
		if mancodeSensitiveRedacted(text[valueStart:end]) {
			continue
		}
		return true, nil
	}
	return false, nil
}

func mancodeSensitiveRedacted(value string) bool {
	if value == "[REDACTED]" {
		return true
	}
	match := mancodeSensitiveRedactionRE.FindStringSubmatch(value)
	if len(match) != 2 || match[1] == "" {
		return false
	}
	for _, category := range strings.Split(match[1], ",") {
		if !mancodeSensitiveRedactionCategories[category] {
			return false
		}
	}
	return true
}

func mancodeChinaPhoneValid(value string) bool {
	if prefix := mancodeSensitivePhonePrefixRE.FindString(value); prefix != "" {
		value = value[len(prefix):]
		value = strings.TrimLeft(value, " -")
	}
	if len(value) == 11 {
		return value[0] == '1' && value[1] >= '3' && value[1] <= '9' && mancodeASCIIInteger(value)
	}
	if len(value) != 13 || (value[3] != ' ' && value[3] != '-') || value[8] != value[3] {
		return false
	}
	digits := value[:3] + value[4:8] + value[9:]
	return value[0] == '1' && value[1] >= '3' && value[1] <= '9' && mancodeASCIIInteger(digits)
}

func mancodeChinaIdentityValid(value string) bool {
	if len(value) != 15 && len(value) != 18 {
		return false
	}
	province := value[:2]
	if !mancodeChinaProvinces[province] {
		return false
	}
	dateText := value[6:14]
	if len(value) == 15 {
		dateText = "19" + value[6:12]
	}
	if len(dateText) != 8 || !mancodeASCIIInteger(dateText) {
		return false
	}
	year, _ := strconv.Atoi(dateText[:4])
	month, _ := strconv.Atoi(dateText[4:6])
	day, _ := strconv.Atoi(dateText[6:8])
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if year < 1880 || year > time.Now().UTC().Year() || int(date.Month()) != month || date.Day() != day {
		return false
	}
	if len(value) == 15 {
		return mancodeASCIIInteger(value)
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for i, weight := range weights {
		sum += int(value[i]-'0') * weight
	}
	return mancodeASCIIInteger(value[:17]) && strings.EqualFold(value[17:], checks[sum%11:sum%11+1])
}

func mancodePaymentCardValid(value string) bool {
	digits := strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), "-", "")
	if len(digits) < 13 || len(digits) > 19 || digits[0] < '2' || digits[0] > '6' || !mancodeASCIIInteger(digits) {
		return false
	}
	separator := byte(0)
	for i := 0; i < len(value); i++ {
		if value[i] == ' ' || value[i] == '-' {
			if separator == 0 {
				separator = value[i]
			} else if value[i] != separator {
				return false
			}
		}
	}
	if digits[0] == '2' {
		if len(digits) != 16 {
			return false
		}
		bin, _ := strconv.Atoi(digits[:6])
		if bin < 222100 || bin > 272099 {
			return false
		}
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		digit := int(digits[i] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

func mancodeJWTShapeValid(value string) bool {
	segments := strings.Split(value, ".")
	if len(segments) != 3 || len(segments[0]) < 4 || len(segments[0]) > 1027 ||
		len(segments[1]) < 2 || len(segments[1]) > 8192 || len(segments[2]) < 1 || len(segments[2]) > 2048 {
		return false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(headerBytes) != segments[0] || !utf8.Valid(headerBytes) {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(payloadBytes) != segments[1] || !utf8.Valid(payloadBytes) {
		return false
	}
	var header map[string]any
	var payload map[string]any
	if json.Unmarshal(headerBytes, &header) != nil || json.Unmarshal(payloadBytes, &payload) != nil || header == nil || payload == nil {
		return false
	}
	algorithm, ok := header["alg"].(string)
	return ok && algorithm != ""
}

func mancodeASCIIInteger(value string) bool {
	if value == "" {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
