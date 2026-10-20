package ci

import (
	"net/url"
	"strings"
	"unicode"
)

// resourceAttributesFromEnvironment exports a strict GitHub Actions metadata
// allowlist. It never copies the whole CI environment into telemetry.
func resourceAttributesFromEnvironment(values []string) string {
	env := make(map[string]string, len(values))
	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	var fields []struct{ key, value string }
	if strings.EqualFold(strings.TrimSpace(env["GITHUB_ACTIONS"]), "true") {
		fields = append(fields,
			struct{ key, value string }{"beacon.origin", "ci"},
			struct{ key, value string }{"beacon.run.provider", "github_actions"},
			struct{ key, value string }{"beacon.run.run_id", env["GITHUB_RUN_ID"]},
			struct{ key, value string }{"beacon.run.run_attempt", env["GITHUB_RUN_ATTEMPT"]},
			struct{ key, value string }{"beacon.run.workflow", env["GITHUB_WORKFLOW"]},
			struct{ key, value string }{"beacon.run.job", env["GITHUB_JOB"]},
			struct{ key, value string }{"beacon.run.event_name", env["GITHUB_EVENT_NAME"]},
			struct{ key, value string }{"beacon.run.commit", env["GITHUB_SHA"]},
			struct{ key, value string }{"beacon.run.repository", env["GITHUB_REPOSITORY"]},
			struct{ key, value string }{"beacon.run.actor", env["GITHUB_ACTOR"]},
			struct{ key, value string }{"beacon.run.ephemeral", "true"},
		)
		branch := env["GITHUB_HEAD_REF"]
		if strings.TrimSpace(branch) == "" {
			branch = env["GITHUB_REF_NAME"]
		}
		fields = append(fields, struct{ key, value string }{"beacon.run.branch", branch})
		if isPullRequestEvent(env["GITHUB_EVENT_NAME"]) && strings.HasPrefix(env["GITHUB_REF"], "refs/pull/") {
			pr := env["GITHUB_REF"]
			if number := parsePRNumber(pr); number != "" {
				fields = append(fields, struct{ key, value string }{"beacon.run.pr", pr})
				fields = append(fields, struct{ key, value string }{"beacon.run.pr_number", number})
			}
		}
	} else if strings.TrimSpace(env["CI"]) != "" {
		fields = append(fields,
			struct{ key, value string }{"beacon.origin", "ci"},
			struct{ key, value string }{"beacon.run.provider", "ci"},
			struct{ key, value string }{"beacon.run.ephemeral", "true"},
		)
	}
	attrs := make([]string, 0, len(fields))
	for _, field := range fields {
		value := safeRunValue(field.value)
		if value == "" && field.key != "beacon.run.ephemeral" {
			continue
		}
		attrs = append(attrs, field.key+"="+strings.ReplaceAll(url.QueryEscape(value), "+", "%20"))
	}
	return strings.Join(attrs, ",")
}

func safeRunValue(value string) string {
	value = strings.TrimSpace(value)
	var out strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			continue
		}
		if out.Len()+len(string(r)) > 256 {
			break
		}
		out.WriteRune(r)
	}
	return out.String()
}

func isPullRequestEvent(eventName string) bool {
	return eventName == "pull_request" || eventName == "pull_request_target"
}

func parsePRNumber(ref string) string {
	const prefix = "refs/pull/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	number, _, ok := strings.Cut(strings.TrimPrefix(ref, prefix), "/")
	if !ok || number == "" {
		return ""
	}
	for _, char := range number {
		if char < '0' || char > '9' {
			return ""
		}
	}
	return number
}
