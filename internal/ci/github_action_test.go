package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type githubActionDocument struct {
	Inputs map[string]struct {
		Default string `yaml:"default"`
	} `yaml:"inputs"`
	Runs struct {
		Using string `yaml:"using"`
		Steps []struct {
			Name string         `yaml:"name"`
			If   string         `yaml:"if"`
			Uses string         `yaml:"uses"`
			Run  string         `yaml:"run"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"runs"`
}

func TestGitHubCompositeActionContract(t *testing.T) {
	root := repositoryRoot(t)
	actionPath := filepath.Join(root, ".github", "actions", "threadforge-ci", "action.yml")
	actionData, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatal(err)
	}
	var action githubActionDocument
	if err := yaml.Unmarshal(actionData, &action); err != nil {
		t.Fatalf("parse GitHub composite action metadata: %v", err)
	}
	if action.Runs.Using != "composite" || action.Inputs["mode"].Default != "start" ||
		action.Inputs["min-events"].Default != "inherit" || action.Inputs["require-telemetry"].Default != "inherit" ||
		action.Inputs["security-fail-on"].Default != "" ||
		action.Inputs["upload-summary"].Default != "false" {
		t.Fatalf("unexpected composite action contract: %#v", action)
	}
	actionSHAPin := regexp.MustCompile(`^actions/[a-z-]+@[0-9a-f]{40}$`)
	foundPinnedSetup, foundScript, foundOptInSummaryUpload := false, false, false
	for _, step := range action.Runs.Steps {
		if strings.HasPrefix(step.Uses, "actions/") && !actionSHAPin.MatchString(step.Uses) {
			t.Errorf("external action is not pinned to a full commit SHA: %q", step.Uses)
		}
		if strings.HasPrefix(step.Uses, "actions/setup-go@") {
			foundPinnedSetup = actionSHAPin.MatchString(step.Uses)
			if step.With["cache"] != false {
				t.Errorf("setup-go cache must remain disabled by default: %#v", step.With)
			}
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			path, _ := step.With["path"].(string)
			condition := step.If
			retention := step.With["retention-days"]
			foundOptInSummaryUpload = actionSHAPin.MatchString(step.Uses) &&
				strings.Contains(condition, "success()") &&
				strings.Contains(condition, "inputs.upload-summary == 'true'") &&
				strings.Contains(condition, "inputs.mode == 'finish'") &&
				strings.Contains(path, "THREADFORGE_CI_ARTIFACT_DIR") &&
				strings.Contains(path, "threadforge-ci-public-summary.json") &&
				!strings.Contains(path, "runtime.jsonl") && retention == "3" && step.With["if-no-files-found"] == "error"
		}
		if strings.Contains(step.Run, "run.sh") {
			foundScript = true
		}
	}
	if !foundPinnedSetup || !foundScript || !foundOptInSummaryUpload {
		t.Fatalf("action must pin tool dependencies, invoke the runner, and gate upload to its sanitized summary: %#v", action.Runs.Steps)
	}

	scriptPath := filepath.Join(root, ".github", "actions", "threadforge-ci", "run.sh")
	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scriptData), "ci gate --log-path") ||
		!strings.Contains(string(scriptData), "THREADFORGE_ACTION_SECURITY_FAIL_ON") ||
		!strings.Contains(string(scriptData), "THREADFORGE_CI_ARTIFACT_DIR") {
		t.Fatal("composite finish action must run the optional security gate on its local captured log")
	}
	if runtime.GOOS != "windows" {
		if bash, err := exec.LookPath("bash"); err == nil {
			if output, err := exec.Command(bash, "-n", scriptPath).CombinedOutput(); err != nil {
				t.Fatalf("parse composite action script: %v\n%s", err, output)
			}
		} else {
			t.Log("bash unavailable; composite action script syntax check skipped")
		}
	} else {
		t.Log("bash path translation is host-specific on Windows; composite action script syntax check skipped")
	}
}

func TestGitHubCompositeActionSmokeWorkflowContract(t *testing.T) {
	root := repositoryRoot(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "threadforge-ci-action-smoke.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Steps []struct {
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse action smoke workflow: %v", err)
	}
	if workflow.Permissions["contents"] != "read" || len(workflow.Jobs) != 1 {
		t.Fatalf("smoke workflow must use read-only repository permission and one job: %#v", workflow)
	}
	var starts, finishes, codexStarts, codexFinish, fixture, codexFixture, claudeUpload, codexUpload, claudeGate, codexGate bool
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Uses == "./.github/actions/threadforge-ci" && step.With["mode"] == "start" {
				starts = true
				if step.With["harness"] == "codex-cli" {
					codexStarts = true
				}
			}
			if step.Uses == "./.github/actions/threadforge-ci" && step.With["mode"] == "finish" && strings.Contains(step.If, "always()") {
				finishes = true
				if codexStarts {
					codexFinish = true
					codexUpload = step.With["harness"] == "codex-cli" && step.With["upload-summary"] == "true"
					codexGate = step.With["security-fail-on"] == "critical"
				} else {
					claudeUpload = step.With["upload-summary"] == "true"
					claudeGate = step.With["security-fail-on"] == "critical"
				}
			}
			if strings.Contains(step.Run, "THREADFORGE_EVENT_ENDPOINT") && strings.Contains(step.Run, "session.started") {
				if strings.Contains(step.Run, "codex_cli") {
					codexFixture = strings.Contains(step.Run, "CODEX_HOME/config.toml")
				} else {
					fixture = true
				}
			}
			if strings.HasPrefix(step.Uses, "actions/") && !regexp.MustCompile(`^actions/checkout@[0-9a-f]{40}$`).MatchString(step.Uses) {
				t.Errorf("workflow action is not pinned to a full commit SHA: %q", step.Uses)
			}
		}
	}
	if !starts || !finishes || !fixture || !codexStarts || !codexFinish || !codexFixture || !claudeUpload || !codexUpload || !claudeGate || !codexGate {
		t.Fatalf("smoke workflow must cover Claude/Codex capture, always-finish, explicit summary upload, and opted-in security gates: starts=%t finishes=%t fixture=%t codex_start=%t codex_finish=%t codex_fixture=%t claude_upload=%t codex_upload=%t claude_gate=%t codex_gate=%t",
			starts, finishes, fixture, codexStarts, codexFinish, codexFixture, claudeUpload, codexUpload, claudeGate, codexGate)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate CI contract test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}
