package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/continuity"
)

const (
	planGateRuntimeMarker = "v1"
	planGateMaxInputBytes = 1 << 20
)

type taskPlanGateHookInput struct {
	HookEventName   string                       `json:"hook_event_name"`
	CopilotEvent    string                       `json:"eventName"`
	ToolName        string                       `json:"tool_name"`
	CopilotToolName string                       `json:"toolName"`
	CWD             string                       `json:"cwd"`
	ToolInput       json.RawMessage              `json:"tool_input"`
	CopilotToolArgs json.RawMessage              `json:"toolArgs"`
	AntigravityTool *taskPlanGateAntigravityTool `json:"toolCall"`
	WorkspacePaths  []string                     `json:"workspacePaths"`
}

type taskPlanGateAntigravityTool struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type taskPlanGateToolInput struct {
	FilePath   string `json:"file_path"`
	Path       string `json:"path"`
	TargetFile string `json:"TargetFile"`
	Patch      string `json:"patch"`
	Input      string `json:"input"`
	Command    string `json:"command"`
	Cmd        string `json:"cmd"`
}

type taskPlanGateHookResult struct {
	HookSpecificOutput *taskPlanGateHookDecision "json:\"hookSpecificOutput,omitempty\""
}

type taskPlanGateHookDecision struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

type taskPlanGateCopilotHookResult struct {
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

func runTaskPlanGuard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("task plan guard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	host := fs.String("host", "claude-code", "hook contract: claude-code or qwen-code")
	taskID := fs.String("task-id", "", "explicit task identifier")
	projectID := fs.String("project-id", "", "expected project identifier")
	projectRoot := fs.String("project-root", "", "absolute local project root")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	claudeMarker := fs.String("threadforge-managed-claude-plan-gate", "", "required Claude Code hook marker")
	qwenMarker := fs.String("threadforge-managed-qwen-plan-gate", "", "required Qwen Code hook marker")
	copilotMarker := fs.String("threadforge-managed-copilot-plan-gate", "", "required GitHub Copilot CLI hook marker")
	codexMarker := fs.String("threadforge-managed-codex-plan-gate", "", "required Codex CLI hook marker")
	geminiMarker := fs.String("threadforge-managed-gemini-plan-gate", "", "required Gemini CLI hook marker")
	cursorMarker := fs.String("threadforge-managed-cursor-plan-gate", "", "required Cursor hook marker")
	antigravityMarker := fs.String("threadforge-managed-antigravity-plan-gate", "", "required Antigravity hook marker")
	clineMarker := fs.String("threadforge-managed-cline-plan-gate", "", "required Cline CLI plugin marker")
	planGateMarker := fs.String("threadforge-plan-gate", "", "required host-specific plan-gate ownership marker")
	if err := fs.Parse(args); err != nil || *taskID == "" || *projectID == "" || *projectRoot == "" || fs.NArg() != 0 {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	switch *host {
	case "claude-code":
		if *claudeMarker != planGateRuntimeMarker || *qwenMarker != "" || *copilotMarker != "" || *codexMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "claude-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "qwen-code":
		if *qwenMarker != planGateRuntimeMarker || *claudeMarker != "" || *copilotMarker != "" || *codexMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "qwen-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "github-copilot":
		if *copilotMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *codexMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "copilot-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "codex-cli":
		if *codexMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *copilotMarker != "" || *geminiMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "codex-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "gemini-cli":
		if *geminiMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *copilotMarker != "" || *codexMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "gemini-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "cursor":
		if *cursorMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *copilotMarker != "" || *codexMarker != "" || *geminiMarker != "" || *antigravityMarker != "" || *clineMarker != "" || *planGateMarker != "cursor-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "antigravity-cli":
		if *antigravityMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *copilotMarker != "" || *codexMarker != "" || *geminiMarker != "" || *cursorMarker != "" || *clineMarker != "" || *planGateMarker != "antigravity-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	case "cline-cli":
		if *clineMarker != planGateRuntimeMarker || *claudeMarker != "" || *qwenMarker != "" || *copilotMarker != "" || *codexMarker != "" || *geminiMarker != "" || *cursorMarker != "" || *antigravityMarker != "" || *planGateMarker != "cline-v1" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	default:
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, planGateMaxInputBytes+1))
	if err != nil || len(raw) > planGateMaxInputBytes {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	var hookInput taskPlanGateHookInput
	if json.Unmarshal(raw, &hookInput) != nil {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	toolName, toolArgs := hookInput.ToolName, hookInput.ToolInput
	if *host == "github-copilot" {
		if hookInput.CopilotEvent != "" && hookInput.CopilotEvent != "preToolUse" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
		toolName, toolArgs = hookInput.CopilotToolName, hookInput.CopilotToolArgs
	} else if *host == "cursor" {
		if hookInput.HookEventName != "" && hookInput.HookEventName != "preToolUse" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	} else if *host == "antigravity-cli" {
		if hookInput.HookEventName != "" || hookInput.AntigravityTool == nil || hookInput.AntigravityTool.Name == "" ||
			len(hookInput.WorkspacePaths) != 1 || hookInput.WorkspacePaths[0] == "" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
		toolName, toolArgs = hookInput.AntigravityTool.Name, hookInput.AntigravityTool.Args
		hookInput.CWD = hookInput.WorkspacePaths[0]
	} else if *host == "cline-cli" {
		if hookInput.HookEventName != "BeforeTool" {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	} else {
		expectedEvent := "PreToolUse"
		if *host == "gemini-cli" {
			expectedEvent = "BeforeTool"
		}
		if hookInput.HookEventName != expectedEvent {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	}
	if *host == "github-copilot" {
		if taskPlanGateCopilotReadOnlyMCPTool(toolName) {
			return writeTaskPlanGateResult(stdout, stderr, true, *host)
		}
		if taskPlanGateCopilotRequiresGovernedPlan(toolName) {
			return guardCopilotUnscopedTool(*taskID, *projectID, *projectRoot, hookInput.CWD, *database, stdout, stderr)
		}
		if !taskPlanGateToolIsSupported(*host, toolName) && taskPlanGateCopilotUnsupportedMutationTool(toolName) {
			return writeCopilotUnscopedPlanDecision(stdout, stderr, false)
		}
	}
	if !taskPlanGateToolIsSupported(*host, toolName) {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	if *host == "github-copilot" && taskPlanGateCopilotReadOnlyOperation(toolName, toolArgs) {
		return writeTaskPlanGateResult(stdout, stderr, true, *host)
	}
	targetPaths, ok := taskPlanGateToolTargetPaths(*host, toolName, toolArgs)
	if !ok {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}

	root, err := taskPlanGateProjectRoot(*projectRoot)
	if err != nil {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	defer closeStore()
	task, err := databaseStore.GetTask(context.Background(), *taskID)
	if err != nil || task.ProjectID != *projectID || task.Requirements == nil ||
		task.Requirements.Status != continuity.RequirementsFinalized ||
		(task.Status != continuity.TaskOpen && task.Status != continuity.TaskInProgress) ||
		task.Plan == nil || task.Plan.Decision != continuity.PlanGovernedExecution ||
		continuity.ValidateTaskPlan(*task.Plan) != nil || task.Plan.Scope == nil {
		return writeTaskPlanGateResult(stdout, stderr, false, *host)
	}
	if *host == "codex-cli" && toolName == "exec_command" {
		if _, pathErr := taskPlanGateRepositoryPath(root, hookInput.CWD, "__threadforge_plan_gate_cwd_check__"); pathErr != nil {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
		return writeTaskPlanGateResult(stdout, stderr, true, *host)
	}

	for _, targetPath := range targetPaths {
		repositoryPath, pathErr := taskPlanGateRepositoryPath(root, hookInput.CWD, targetPath)
		if pathErr != nil || !continuity.PathInImplementationScope(*task.Plan.Scope, repositoryPath) {
			return writeTaskPlanGateResult(stdout, stderr, false, *host)
		}
	}
	return writeTaskPlanGateResult(stdout, stderr, true, *host)
}

func taskPlanGateCopilotReadOnlyOperation(toolName string, raw json.RawMessage) bool {
	if strings.ToLower(toolName) != "str_replace_editor" {
		return false
	}
	var input taskPlanGateToolInput
	return json.Unmarshal(raw, &input) == nil && input.Command == "view"
}

// Shell and MCP calls do not expose a reliable filesystem target contract to
// this hook. Require a finalized, confirmed governed task before deferring
// those calls to Copilot's normal permission policy; this hook never grants
// permission and cannot prove that their effects stay inside implementation scope.
func taskPlanGateCopilotRequiresGovernedPlan(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	for _, marker := range []string{"bash", "shell", "terminal", "powershell", "cmd", "exec", "command"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return taskPlanGateCopilotIsMCPTool(name) && !taskPlanGateCopilotReadOnlyMCPTool(name)
}

func taskPlanGateCopilotIsMCPTool(toolName string) bool {
	return toolName == "mcp" || strings.HasPrefix(toolName, "mcp_") || strings.HasPrefix(toolName, "mcp:")
}

// MCP names are host-controlled. Recognize read-only operations by their final
// tool component and treat unknown names as potentially mutating.
func taskPlanGateCopilotReadOnlyMCPTool(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if !taskPlanGateCopilotIsMCPTool(name) {
		return false
	}
	identity := strings.TrimPrefix(name, "mcp:")
	identity = strings.TrimPrefix(identity, "mcp_")
	if index := strings.LastIndex(identity, "__"); index >= 0 {
		identity = identity[index+2:]
	}
	parts := strings.FieldsFunc(identity, func(value rune) bool {
		return value == '_' || value == '-' || value == ':' || value == '.'
	})
	mutations := map[string]bool{
		"approve": true, "commit": true, "create": true, "delete": true, "edit": true,
		"execute": true, "install": true, "move": true, "publish": true, "push": true,
		"remove": true, "replace": true, "reset": true, "run": true, "send": true,
		"submit": true, "update": true, "write": true,
	}
	for _, part := range parts {
		if mutations[part] {
			return false
		}
	}
	reads := map[string]bool{
		"describe": true, "fetch": true, "get": true, "guide": true, "inspect": true,
		"list": true, "preview": true, "query": true, "read": true, "search": true,
		"status": true, "view": true,
	}
	for _, part := range parts {
		if reads[part] {
			return true
		}
	}
	return strings.HasSuffix(identity, "task_resume_context") || strings.HasSuffix(identity, "memory_context")
}

func taskPlanGateCopilotUnsupportedMutationTool(toolName string) bool {
	name := strings.ToLower(strings.TrimSpace(toolName))
	for _, marker := range []string{"write", "create", "edit", "patch", "delete", "remove", "move", "replace", "save", "apply"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func guardCopilotUnscopedTool(taskID, projectID, projectRoot, cwd, database string, stdout, stderr io.Writer) int {
	root, rootErr := taskPlanGateProjectRoot(projectRoot)
	permittedByPlan := false
	if rootErr == nil {
		if _, cwdErr := taskPlanGateRepositoryPath(root, cwd, "__threadforge_plan_gate_cwd_check__"); cwdErr == nil {
			procedures, closeStore, err := openContinuityStore(database)
			if err == nil {
				defer closeStore()
				task, taskErr := procedures.GetTask(context.Background(), taskID)
				permittedByPlan = taskErr == nil && task.ProjectID == projectID &&
					task.Requirements != nil && task.Requirements.Status == continuity.RequirementsFinalized &&
					(task.Status == continuity.TaskOpen || task.Status == continuity.TaskInProgress) &&
					task.Plan != nil && task.Plan.Decision == continuity.PlanGovernedExecution &&
					continuity.ValidateTaskPlan(*task.Plan) == nil && task.Plan.Scope != nil
			}
		}
	}
	return writeCopilotUnscopedPlanDecision(stdout, stderr, permittedByPlan)
}

func writeCopilotUnscopedPlanDecision(stdout, stderr io.Writer, permittedByPlan bool) int {
	result := taskPlanGateCopilotHookResult{}
	if !permittedByPlan {
		result.PermissionDecision = "deny"
		result.PermissionDecisionReason = "Threadforge withheld this Copilot command or unclassified tool call until requirements are finalized and governed execution is confirmed."
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func taskPlanGateToolIsSupported(host, toolName string) bool {
	switch host {
	case "claude-code":
		return toolName == "Write" || toolName == "Edit"
	case "qwen-code":
		return toolName == "write_file" || toolName == "edit"
	case "github-copilot":
		switch toolName {
		case "create", "edit", "apply_patch", "str_replace_editor":
			return true
		}
		return false
	case "codex-cli":
		return toolName == "apply_patch" || toolName == "exec_command"
	case "gemini-cli":
		return toolName == "write_file" || toolName == "replace"
	case "cursor":
		return toolName == "Write"
	case "antigravity-cli":
		switch toolName {
		case "write_to_file", "replace_file_content", "multi_replace_file_content":
			return true
		default:
			return false
		}
	case "cline-cli":
		return toolName == "editor" || toolName == "apply_patch"
	default:
		return false
	}
}

func taskPlanGateToolTargetPaths(host, toolName string, raw json.RawMessage) ([]string, bool) {
	var input taskPlanGateToolInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, false
	}
	if host == "codex-cli" {
		if toolName == "exec_command" {
			command := input.Command
			if command == "" {
				command = input.Cmd
			}
			if strings.TrimSpace(command) == "" {
				return nil, false
			}
			// Shell commands have no reliably parseable file-scope contract here.
			// Empty targets intentionally yield no opinion only after the task
			// itself passes the governed-execution checks above.
			return []string{}, true
		}
		if toolName != "apply_patch" || strings.TrimSpace(input.Command) == "" {
			return nil, false
		}
		return taskPlanGateCopilotPatchPaths(input.Command)
	}
	if host == "cursor" {
		if input.FilePath != "" && input.Path != "" && input.FilePath != input.Path {
			return nil, false
		}
		targetPath := input.FilePath
		if targetPath == "" {
			targetPath = input.Path
		}
		if targetPath == "" {
			return nil, false
		}
		return []string{targetPath}, true
	}
	if host == "antigravity-cli" {
		if input.TargetFile == "" {
			return nil, false
		}
		return []string{input.TargetFile}, true
	}
	if host == "cline-cli" {
		if toolName == "apply_patch" {
			return taskPlanGateCopilotPatchPaths(input.Input)
		}
		if toolName != "editor" || input.Path == "" {
			return nil, false
		}
		return []string{input.Path}, true
	}
	if host != "github-copilot" {
		if input.FilePath == "" {
			return nil, false
		}
		return []string{input.FilePath}, true
	}

	name := strings.ToLower(toolName)
	if name == "apply_patch" {
		return taskPlanGateCopilotPatchPaths(input.Patch)
	}
	if name == "str_replace_editor" {
		switch input.Command {
		case "view":
			return []string{}, true
		case "create", "str_replace", "insert", "undo_edit":
		default:
			return nil, false
		}
	}
	filePath := input.FilePath
	if filePath == "" {
		filePath = input.Path
	}
	if filePath == "" {
		return nil, false
	}
	return []string{filePath}, true
}

func taskPlanGateCopilotPatchPaths(patch string) ([]string, bool) {
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	lines := strings.Split(patch, "\n")
	if len(lines) < 3 || lines[0] != "*** Begin Patch" || lines[len(lines)-1] != "*** End Patch" {
		return nil, false
	}
	paths := make([]string, 0, 2)
	canMove := false
	for _, line := range lines[1 : len(lines)-1] {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, prefix) {
				path := strings.TrimPrefix(line, prefix)
				if strings.TrimSpace(path) == "" || strings.ContainsAny(path, "\x00\r\n") {
					return nil, false
				}
				paths = append(paths, path)
				canMove = prefix == "*** Update File: "
				goto nextLine
			}
		}
		if strings.HasPrefix(line, "*** Move to: ") {
			path := strings.TrimPrefix(line, "*** Move to: ")
			if !canMove || strings.TrimSpace(path) == "" || strings.ContainsAny(path, "\x00\r\n") {
				return nil, false
			}
			paths = append(paths, path)
			canMove = false
			goto nextLine
		}
		if strings.HasPrefix(line, "*** ") && line != "*** End of File" {
			return nil, false
		}
		canMove = false
	nextLine:
	}
	return paths, len(paths) > 0
}

func writeTaskPlanGateResult(stdout, stderr io.Writer, withinScope bool, host string) int {
	if host == "cline-cli" {
		result := struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason,omitempty"`
		}{Decision: "allow"}
		if !withinScope {
			result.Decision = "deny"
			result.Reason = "Threadforge withheld this Cline file operation; task requirements, plan decision, or implementation scope did not satisfy the configured guard."
		}
		return writeIndentedJSON(stdout, result, stderr)
	}
	if host == "antigravity-cli" {
		result := struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason,omitempty"`
		}{Decision: "allow"}
		if !withinScope {
			result.Decision = "deny"
			result.Reason = "Threadforge withheld this Antigravity file operation; task requirements, plan decision, workspace, or implementation scope did not satisfy the configured guard."
		}
		return writeIndentedJSON(stdout, result, stderr)
	}
	if host == "cursor" {
		result := struct {
			Permission   string `json:"permission,omitempty"`
			UserMessage  string `json:"user_message,omitempty"`
			AgentMessage string `json:"agent_message,omitempty"`
		}{}
		if !withinScope {
			result.Permission = "deny"
			result.UserMessage = "Threadforge withheld this Cursor file write; task requirements, plan decision, or implementation scope did not satisfy the configured guard."
			result.AgentMessage = "The file write was withheld by the configured Threadforge task plan gate. Review the task requirements and declared implementation scope."
		}
		return writeIndentedJSON(stdout, result, stderr)
	}
	if host == "gemini-cli" {
		result := struct {
			Decision string `json:"decision,omitempty"`
			Reason   string `json:"reason,omitempty"`
		}{}
		if !withinScope {
			result.Decision = "deny"
			result.Reason = "Threadforge withheld this Gemini file operation; task requirements, plan decision, or implementation scope did not satisfy the configured guard."
		}
		return writeIndentedJSON(stdout, result, stderr)
	}
	if host == "github-copilot" {
		result := taskPlanGateCopilotHookResult{}
		if !withinScope {
			result.PermissionDecision = "deny"
			result.PermissionDecisionReason = "Threadforge withheld this Copilot file operation; task requirements, plan decision, or implementation scope did not satisfy the configured guard."
		}
		return writeIndentedJSON(stdout, result, stderr)
	}
	result := taskPlanGateHookResult{}
	if !withinScope {
		result.HookSpecificOutput = &taskPlanGateHookDecision{
			HookEventName: "PreToolUse", PermissionDecision: "deny",
			PermissionDecisionReason: "Threadforge plan gate withheld this file edit; task requirements, plan decision, or implementation scope did not satisfy the configured guard.",
		}
	}
	return writeIndentedJSON(stdout, result, stderr)
}

func taskPlanGateProjectRoot(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || strings.HasPrefix(value, "\\\\") {
		return "", errors.New("invalid local project root")
	}
	info, err := os.Lstat(value)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("project root must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", errors.New("project root cannot be resolved")
	}
	return filepath.Clean(resolved), nil
}

func taskPlanGateRepositoryPath(root, cwd, filePath string) (string, error) {
	if cwd == "" || filePath == "" || !filepath.IsAbs(cwd) {
		return "", errors.New("hook input lacks an absolute working directory or file path")
	}
	resolvedCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil || !isWithinPlanGateRoot(root, resolvedCWD, true) {
		return "", errors.New("hook working directory is outside the configured project")
	}
	info, err := os.Stat(resolvedCWD)
	if err != nil || !info.IsDir() {
		return "", errors.New("hook working directory is not an existing directory")
	}

	target := filePath
	if !filepath.IsAbs(target) {
		target = filepath.Join(resolvedCWD, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", errors.New("file path cannot be resolved")
	}
	target = filepath.Clean(target)
	if !isWithinPlanGateRoot(root, target, false) {
		return "", errors.New("file path is outside the configured project")
	}
	parent := filepath.Dir(target)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || !isWithinPlanGateRoot(root, resolvedParent, true) {
		return "", errors.New("file parent cannot be safely resolved within the project")
	}

	resolvedTarget := filepath.Join(resolvedParent, filepath.Base(target))
	if _, err := os.Lstat(target); err == nil {
		resolvedTarget, err = filepath.EvalSymlinks(target)
		if err != nil {
			return "", errors.New("existing file target cannot be resolved")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("file target cannot be inspected")
	}
	if !isWithinPlanGateRoot(root, resolvedTarget, false) {
		return "", errors.New("resolved file path is outside the configured project")
	}
	relative, err := filepath.Rel(root, resolvedTarget)
	if err != nil || relative == "." || filepath.IsAbs(relative) {
		return "", errors.New("file path is not a repository-relative file")
	}
	return filepath.ToSlash(relative), nil
}

func isWithinPlanGateRoot(root, candidate string, allowRoot bool) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
	return allowRoot || relative != "."
}
