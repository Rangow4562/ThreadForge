package ci

import (
	"strings"
	"testing"
)

func TestDaemonEnvironmentUsesOnlyRuntimeAllowlist(t *testing.T) {
	result := daemonEnvironment([]string{
		"PATH=/usr/bin",
		"SYSTEMROOT=C:\\Windows",
		"AWS_ACCESS_KEY_ID=must-not-inherit",
		"THREADFORGE_TEST_SECRET=must-not-inherit",
		"GITHUB_TOKEN=must-not-inherit",
	})
	values := map[string]string{}
	for _, entry := range result {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["THREADFORGE_CI_DAEMON_MODE"] != "1" {
		t.Fatalf("missing internal daemon marker: %#v", values)
	}
	if values["PATH"] != "/usr/bin" {
		t.Fatalf("PATH was not inherited: %#v", values)
	}
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "THREADFORGE_TEST_SECRET", "GITHUB_TOKEN"} {
		if _, ok := values[key]; ok {
			t.Fatalf("secret environment variable %q reached daemon: %#v", key, values)
		}
	}
}

func TestIsLoopbackEndpointRejectsRemoteOrAmbiguousURLs(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:4318",
		"http://example.invalid:4318",
		"http://127.0.0.1:4318/path",
		"http://127.0.0.1:99999",
		"http://127.0.0.1",
	} {
		if isLoopbackEndpoint(endpoint) {
			t.Errorf("accepted invalid receiver endpoint %q", endpoint)
		}
	}
	if !isLoopbackEndpoint("http://127.0.0.1:4318") {
		t.Fatal("rejected a valid local receiver endpoint")
	}
}
