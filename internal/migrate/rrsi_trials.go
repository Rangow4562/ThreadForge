package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	RRSITrialImportSchema             = "threadforge.rrsi-trial-artifact.v1"
	RRSITrajectorySummarySchema       = "threadforge.rrsi-trajectory-summary.v1"
	maxRRSITrialFileBytes             = maxFileBytes
	maxRRSITrialReadBytes       int64 = maxTotalBytes
	maxRRSIToolSequenceHashes         = 256
)

type rrsiTrialSource struct {
	domain string
	kind   string
	tail   []string
}

func rrsiTrialSourceForPath(relative string) (rrsiTrialSource, bool) {
	domain, supported := rrsiDomain(relative)
	if !supported {
		return rrsiTrialSource{}, false
	}
	tail, ok := rrsiRunTailOriginal(relative)
	if !ok || len(tail) == 0 || !strings.EqualFold(tail[0], "jobs") {
		return rrsiTrialSource{}, false
	}
	base := strings.ToLower(filepath.Base(relative))
	switch domain {
	case "coding":
		if len(tail) == 4 && base == "result.json" && tail[1] != "" && tail[2] != "" {
			return rrsiTrialSource{domain: domain, kind: "coding_result", tail: tail}, true
		}
		if len(tail) == 5 && base == "trajectory.json" && tail[1] != "" && tail[2] != "" &&
			strings.EqualFold(tail[3], "agent") {
			return rrsiTrialSource{domain: domain, kind: "coding_trajectory", tail: tail}, true
		}
	case "workspace":
		if len(tail) == 5 && base == "scores.json" && tail[1] != "" && isRRSISampleName(tail[2]) && tail[3] != "" {
			return rrsiTrialSource{domain: domain, kind: "workspace_scores", tail: tail}, true
		}
		if len(tail) == 5 && base == "trajectory.json" && tail[1] != "" && isRRSISampleName(tail[2]) && tail[3] != "" {
			return rrsiTrialSource{domain: domain, kind: "workspace_trajectory", tail: tail}, true
		}
	case "eng":
		if len(tail) == 5 && tail[1] != "" && tail[2] != "" && isRRSITrialName(tail[3]) {
			switch base {
			case "meta.json":
				return rrsiTrialSource{domain: domain, kind: "engineering_meta", tail: tail}, true
			case "verdict.json":
				return rrsiTrialSource{domain: domain, kind: "engineering_verdict", tail: tail}, true
			case "traj.json":
				return rrsiTrialSource{domain: domain, kind: "engineering_trajectory", tail: tail}, true
			}
		}
	}
	return rrsiTrialSource{}, false
}

func isRRSITrialMetadataPath(relative string) bool {
	_, ok := rrsiTrialSourceForPath(relative)
	return ok
}

func isRRSIEngineeringMetaPath(relative string) bool {
	source, ok := rrsiTrialSourceForPath(relative)
	return ok && source.kind == "engineering_meta"
}

func isRRSIEngineeringVerdictPath(relative string) bool {
	source, ok := rrsiTrialSourceForPath(relative)
	return ok && source.kind == "engineering_verdict"
}

func rrsiEngineeringVerdictPath(metaPath string) (string, bool) {
	if !isRRSIEngineeringMetaPath(metaPath) {
		return "", false
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(filepath.FromSlash(metaPath)), "verdict.json")), true
}

func isRRSISampleName(value string) bool { return hasNumericSuffix(value, 's') }

func isRRSITrialName(value string) bool { return hasNumericSuffix(value, 't') }

func hasNumericSuffix(value string, prefix byte) bool {
	if len(value) < 2 || !strings.EqualFold(value[:1], string(prefix)) {
		return false
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func readSafeRRSIFile(sourceRoot, relative string, maxBytes int64) ([]byte, FileEntry, error) {
	if !safeRelative(relative) || maxBytes < 0 {
		return nil, FileEntry{}, errors.New("invalid bounded RRSI source path")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, FileEntry{}, err
	}
	current := root
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	var before os.FileInfo
	for index, part := range parts {
		current = filepath.Join(current, part)
		before, err = os.Lstat(current)
		if err != nil {
			return nil, FileEntry{}, err
		}
		if before.Mode()&os.ModeSymlink != 0 {
			return nil, FileEntry{}, errors.New("RRSI source path contains a symlink")
		}
		if index < len(parts)-1 && !before.IsDir() {
			return nil, FileEntry{}, errors.New("RRSI source parent is not a directory")
		}
	}
	if before == nil || !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > maxBytes {
		return nil, FileEntry{}, errors.New("RRSI metadata file is not regular or exceeds its size limit")
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, FileEntry{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() {
		return nil, FileEntry{}, errors.New("RRSI metadata file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) != opened.Size() || int64(len(data)) > maxBytes {
		return nil, FileEntry{}, errors.New("RRSI metadata file changed while reading")
	}
	after, err := os.Lstat(current)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) || after.Size() != opened.Size() {
		return nil, FileEntry{}, errors.New("RRSI metadata file changed after reading")
	}
	entry := FileEntry{Path: relative, SourceSHA256: digest(data), SourceBytes: int64(len(data))}
	return data, entry, nil
}

func normalizeRRSITrialFile(sourceRoot, relative string, data []byte, peerReadBudget int64) (normalizedItem, *FileEntry, int64, error) {
	source, ok := rrsiTrialSourceForPath(relative)
	if !ok {
		return normalizedItem{}, nil, 0, errors.New("RRSI trial metadata path is unsupported")
	}
	var value any
	if isRRSITrajectoryKind(source.kind) {
		var err error
		value, err = decodeStrictRRSIProposalJSON(data)
		if err != nil {
			return normalizedItem{}, nil, 0, errors.New("invalid or ambiguous RRSI trajectory JSON")
		}
	} else if err := json.Unmarshal(data, &value); err != nil {
		return normalizedItem{}, nil, 0, errors.New("invalid RRSI trial metadata JSON")
	}
	var payload json.RawMessage
	var companion *FileEntry
	var peerBytes int64
	var err error
	switch source.kind {
	case "coding_result":
		payload, err = normalizeRRSICodingTrial(source, value, digest(data))
	case "workspace_scores":
		payload, err = normalizeRRSIWorkspaceTrial(source, value, digest(data))
	case "engineering_meta":
		peerPath, valid := rrsiEngineeringVerdictPath(relative)
		if !valid {
			return normalizedItem{}, nil, 0, errors.New("RRSI engineering metadata has no valid verdict partner path")
		}
		peerData, peerEntry, readErr := readSafeRRSIFile(sourceRoot, peerPath,
			minInt64(maxRRSITrialFileBytes, peerReadBudget))
		if readErr != nil {
			return normalizedItem{}, nil, 0, errors.New("RRSI engineering verdict partner is missing, unsafe, or oversized")
		}
		peerBytes = peerEntry.SourceBytes
		var verdict any
		if err := json.Unmarshal(peerData, &verdict); err != nil {
			return normalizedItem{}, nil, peerBytes, errors.New("invalid RRSI engineering verdict JSON")
		}
		payload, err = normalizeRRSIEngineeringTrial(source, value, verdict, digest(data), peerEntry.SourceSHA256)
		if err == nil {
			peerEntry.Status = "joined_companion"
			peerEntry.JoinedInto = relative
			companion = &peerEntry
		}
	case "engineering_verdict":
		return normalizedItem{}, nil, 0, errors.New("RRSI engineering verdict has no verified metadata join")
	case "coding_trajectory", "workspace_trajectory", "engineering_trajectory":
		payload, err = normalizeRRSITrajectory(source, value, digest(data))
	default:
		return normalizedItem{}, nil, 0, errors.New("unsupported RRSI trial metadata kind")
	}
	if err != nil {
		return normalizedItem{}, nil, peerBytes, err
	}
	recordType := "rrsi_trial_artifact"
	if isRRSITrajectoryKind(source.kind) {
		recordType = "rrsi_trajectory_summary"
	}
	return normalizedItem{Type: recordType, Payload: payload}, companion, peerBytes, nil
}

func isRRSITrajectoryKind(kind string) bool {
	switch kind {
	case "coding_trajectory", "workspace_trajectory", "engineering_trajectory":
		return true
	default:
		return false
	}
}

func normalizeRRSICodingTrial(source rrsiTrialSource, value any, sourceSHA string) (json.RawMessage, error) {
	root, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI coding result must be an object")
	}
	taskName, ok := root["task_name"].(string)
	if !ok || strings.TrimSpace(taskName) == "" || len(taskName) > 1024 {
		return nil, errors.New("RRSI coding result task identity is invalid")
	}
	taskName = strings.ReplaceAll(taskName, "\\", "/")
	taskID := filepath.Base(taskName)
	trialDir := source.tail[2]
	separator := strings.LastIndex(trialDir, "__")
	if separator < 1 || trialDir[:separator] != taskID {
		return nil, errors.New("RRSI coding result does not match its trial directory")
	}

	var reward any
	verifier, _ := rrsiObject(root["verifier_result"])
	rewards, _ := rrsiObject(verifier["rewards"])
	rewardValue, hasReward, validReward := rrsiOptionalNumber(rewards["reward"])
	if !validReward || (hasReward && (rewardValue < 0 || rewardValue > 1)) {
		return nil, errors.New("RRSI coding trial reward is invalid")
	}
	if hasReward {
		reward = rewardValue
	}
	exceptionReported := hasRRSIException(root["exception_info"])
	var inputTokens, outputTokens, episodes any
	if !exceptionReported {
		agent, _ := rrsiObject(root["agent_result"])
		input, hasInput, validInput := rrsiOptionalInteger(agent["n_input_tokens"], 0, 1_000_000_000_000)
		output, hasOutput, validOutput := rrsiOptionalInteger(agent["n_output_tokens"], 0, 1_000_000_000_000)
		if !validInput || !validOutput {
			return nil, errors.New("RRSI coding token counts are invalid")
		}
		if hasInput {
			inputTokens = input
		}
		if hasOutput {
			outputTokens = output
		}
		metadata, _ := rrsiObject(agent["metadata"])
		count, hasCount, validCount := rrsiOptionalInteger(metadata["n_episodes"], 0, 1_000_000_000)
		if !validCount {
			return nil, errors.New("RRSI coding episode count is invalid")
		}
		if hasCount {
			episodes = count
		}
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, taskID)
	return rrsiMarshal(map[string]any{
		"schema_version": RRSITrialImportSchema, "domain": "coding", "record_kind": "coding_result",
		"job_id_sha256": jobHash, "task_id_sha256": taskHash, "trial_key_sha256": trialHash,
		"source_sha256": sourceSHA, "reward": reward, "input_tokens": inputTokens,
		"output_tokens": outputTokens, "episodes": episodes, "exception_reported": exceptionReported,
	})
}

func normalizeRRSIWorkspaceTrial(source rrsiTrialSource, value any, sourceSHA string) (json.RawMessage, error) {
	root, ok := rrsiObject(value)
	if !ok {
		return nil, errors.New("RRSI workspace scores must be an object")
	}
	taskName, ok := root["task"].(string)
	if !ok || strings.TrimSpace(taskName) == "" || len(taskName) > 1024 || filepath.Base(taskName) != source.tail[3] {
		return nil, errors.New("RRSI workspace score identity does not match its trial directory")
	}
	score, scoreOK := rrsiNumber(root["score"])
	passed, passedOK := rrsiInteger(root["passed"])
	total, totalOK := rrsiInteger(root["total"])
	allPassNumber, allPassOK := rrsiInteger(root["all_pass"])
	if !scoreOK || score < 0 || score > 1 || !passedOK || !totalOK || total < 1 || total > maxRecords || passed > total ||
		!allPassOK || allPassNumber > 1 || (allPassNumber == 1) != (passed == total) {
		return nil, errors.New("RRSI workspace aggregate scores do not reconcile")
	}
	sampleIndex, err := parseRRSIIndex(source.tail[2], 's')
	if err != nil {
		return nil, errors.New("RRSI workspace sample index is invalid")
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, source.tail[3])
	return rrsiMarshal(map[string]any{
		"schema_version": RRSITrialImportSchema, "domain": "workspace", "record_kind": "workspace_scores",
		"job_id_sha256": jobHash, "task_id_sha256": taskHash, "trial_key_sha256": trialHash,
		"source_sha256": sourceSHA, "sample_index": sampleIndex, "score": score,
		"passed_criteria": passed, "total_criteria": total, "all_pass": allPassNumber == 1,
	})
}

func normalizeRRSIEngineeringTrial(source rrsiTrialSource, metaValue, verdictValue any, metaSHA, verdictSHA string) (json.RawMessage, error) {
	meta, metaOK := rrsiObject(metaValue)
	verdict, verdictOK := rrsiObject(verdictValue)
	if !metaOK || !verdictOK || source.kind != "engineering_meta" {
		return nil, errors.New("RRSI engineering trial pair must contain metadata and verdict objects")
	}
	taskID := source.tail[2]
	id, idOK := meta["id"].(string)
	trialIndex, trialOK := rrsiInteger(meta["trial"])
	pathTrial, pathTrialErr := parseRRSIIndex(source.tail[3], 't')
	status, statusOK := meta["status"].(string)
	if !idOK || id != taskID || !trialOK || pathTrialErr != nil || trialIndex != pathTrial ||
		!statusOK || strings.TrimSpace(status) == "" || len(status) > 128 {
		return nil, errors.New("RRSI engineering metadata identity or status is invalid")
	}
	metaWall, metaWallOK := rrsiNumber(meta["wall_s"])
	payloadChars, payloadOK := rrsiInteger(meta["payload_chars"])
	noPayload, noPayloadOK := meta["no_payload"].(bool)
	if !metaWallOK || metaWall < 0 || metaWall > 1e9 || !payloadOK || payloadChars > 1_000_000_000_000 || !noPayloadOK {
		return nil, errors.New("RRSI engineering metadata metrics are invalid")
	}
	var totalTokens, nMessages any
	if raw, exists := meta["total_tokens"]; exists && raw != nil {
		value, valid := rrsiInteger(raw)
		if !valid || value > 1_000_000_000_000 {
			return nil, errors.New("RRSI engineering token count is invalid")
		}
		totalTokens = value
	}
	if raw, exists := meta["n_messages"]; exists && raw != nil {
		value, valid := rrsiInteger(raw)
		if !valid || value > 1_000_000_000 {
			return nil, errors.New("RRSI engineering message count is invalid")
		}
		nMessages = value
	}
	score, scoreOK := rrsiNumber(verdict["combined_score"])
	valid, validOK := rrsiNumber(verdict["valid"])
	passed, passedOK := verdict["passed"].(bool)
	if !scoreOK || score < 0 || score > 1 || !validOK || valid < 0 || valid > 1 || !passedOK {
		return nil, errors.New("RRSI engineering verdict metrics are incomplete or invalid")
	}
	var verdictWall any
	if raw, exists := verdict["wall_s"]; exists && raw != nil {
		value, validNumber := rrsiNumber(raw)
		if !validNumber || value < 0 || value > 1e9 {
			return nil, errors.New("RRSI engineering verifier duration is invalid")
		}
		verdictWall = value
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, taskID)
	return rrsiMarshal(map[string]any{
		"schema_version": RRSITrialImportSchema, "domain": "eng", "record_kind": "engineering_trial",
		"job_id_sha256": jobHash, "task_id_sha256": taskHash, "trial_key_sha256": trialHash,
		"source_sha256": metaSHA, "verdict_source_sha256": verdictSHA, "trial_index": trialIndex,
		"total_tokens": totalTokens, "payload_chars": payloadChars, "no_payload": noPayload,
		"n_messages": nMessages, "wall_seconds": metaWall, "verifier_wall_seconds": verdictWall,
		"combined_score": score, "valid_rate": valid, "passed": passed,
	})
}

func rrsiTrialHashes(source rrsiTrialSource, taskID string) (jobHash, taskHash, trialHash string) {
	jobHash = digest([]byte(source.tail[1]))
	taskHash = digest([]byte(taskID))
	trialParts := source.tail[:len(source.tail)-1]
	if source.kind == "engineering_meta" || source.kind == "engineering_verdict" || source.kind == "engineering_trajectory" {
		trialParts = source.tail[:4]
	} else if source.kind == "workspace_scores" || source.kind == "workspace_trajectory" {
		trialParts = source.tail[:4]
	} else {
		trialParts = source.tail[:3]
	}
	trialHash = digest([]byte(strings.Join(trialParts, "/")))
	return jobHash, taskHash, trialHash
}

func normalizeRRSITrajectory(source rrsiTrialSource, value any, sourceSHA string) (json.RawMessage, error) {
	root, ok := rrsiObject(value)
	if !ok || !validDigest(sourceSHA) {
		return nil, errors.New("RRSI trajectory root or source digest is invalid")
	}
	taskID, ok := rrsiTrajectoryTaskID(source)
	if !ok {
		return nil, errors.New("RRSI trajectory identity is invalid")
	}
	jobHash, taskHash, trialHash := rrsiTrialHashes(source, taskID)
	payload := map[string]any{
		"schema_version": RRSITrajectorySummarySchema, "domain": source.domain, "record_kind": source.kind,
		"job_id_sha256": jobHash, "task_id_sha256": taskHash, "trial_key_sha256": trialHash,
		"source_sha256": sourceSHA,
	}
	switch source.kind {
	case "coding_trajectory":
		steps, ok := rrsiArray(root["steps"])
		if !ok || len(steps) > maxRecords {
			return nil, errors.New("RRSI coding trajectory steps are invalid or exceed the limit")
		}
		toolCalls, observations := 0, 0
		toolNameHashes := make([]string, 0, maxRRSIToolSequenceHashes)
		for _, rawStep := range steps {
			step, ok := rrsiObject(rawStep)
			if !ok {
				return nil, errors.New("RRSI coding trajectory step structure is invalid")
			}
			calls, err := rrsiTrajectoryToolCalls(step)
			if err != nil || len(calls) > maxRecords-toolCalls {
				return nil, errors.New("RRSI coding trajectory tool-call count is invalid")
			}
			toolCalls += len(calls)
			for _, rawCall := range calls {
				nameHash, err := rrsiTrajectoryToolNameHash(rawCall)
				if err != nil {
					return nil, errors.New("RRSI coding trajectory tool-call identity is invalid")
				}
				if len(toolNameHashes) < maxRRSIToolSequenceHashes {
					toolNameHashes = append(toolNameHashes, nameHash)
				}
			}
			if rawObservation, exists := step["observation"]; exists && rawObservation != nil {
				observation, ok := rrsiObject(rawObservation)
				if !ok {
					return nil, errors.New("RRSI coding trajectory observation structure is invalid")
				}
				count, err := rrsiTrajectoryArrayCount(observation, "results")
				if err != nil || count > maxRecords-observations {
					return nil, errors.New("RRSI coding trajectory observation count is invalid")
				}
				observations += count
			}
		}
		payload["step_count"] = len(steps)
		payload["tool_call_count"] = toolCalls
		payload["observation_result_count"] = observations
		payload["tool_call_name_hashes"] = toolNameHashes
		payload["tool_sequence_truncated"] = toolCalls > len(toolNameHashes)
	case "workspace_trajectory", "engineering_trajectory":
		messages, ok := rrsiArray(root["messages"])
		if !ok || len(messages) > maxRecords {
			return nil, errors.New("RRSI trajectory messages are invalid or exceed the limit")
		}
		roles := map[string]int{"system": 0, "user": 0, "assistant": 0, "tool": 0, "other": 0}
		toolCalls := 0
		toolNameHashes := make([]string, 0, maxRRSIToolSequenceHashes)
		for _, rawMessage := range messages {
			message, ok := rrsiObject(rawMessage)
			if !ok {
				return nil, errors.New("RRSI trajectory message structure is invalid")
			}
			role, ok := message["role"].(string)
			if !ok || strings.TrimSpace(role) == "" {
				return nil, errors.New("RRSI trajectory message role is invalid")
			}
			role = strings.ToLower(role)
			if _, known := roles[role]; !known || role == "other" {
				role = "other"
			}
			roles[role]++
			calls, err := rrsiTrajectoryToolCalls(message)
			if err != nil || len(calls) > maxRecords-toolCalls {
				return nil, errors.New("RRSI trajectory tool-call count is invalid")
			}
			toolCalls += len(calls)
			for _, rawCall := range calls {
				nameHash, err := rrsiTrajectoryToolNameHash(rawCall)
				if err != nil {
					return nil, errors.New("RRSI trajectory tool-call identity is invalid")
				}
				if len(toolNameHashes) < maxRRSIToolSequenceHashes {
					toolNameHashes = append(toolNameHashes, nameHash)
				}
			}
		}
		payload["message_count"] = len(messages)
		payload["system_message_count"] = roles["system"]
		payload["user_message_count"] = roles["user"]
		payload["assistant_message_count"] = roles["assistant"]
		payload["tool_message_count"] = roles["tool"]
		payload["other_message_count"] = roles["other"]
		payload["tool_call_count"] = toolCalls
		payload["tool_call_name_hashes"] = toolNameHashes
		payload["tool_sequence_truncated"] = toolCalls > len(toolNameHashes)
	default:
		return nil, errors.New("unsupported RRSI trajectory kind")
	}
	return json.Marshal(payload)
}

func rrsiTrajectoryTaskID(source rrsiTrialSource) (string, bool) {
	switch source.kind {
	case "coding_trajectory":
		if len(source.tail) != 5 {
			return "", false
		}
		trialDir := source.tail[2]
		separator := strings.LastIndex(trialDir, "__")
		if separator < 1 || separator == len(trialDir)-2 {
			return "", false
		}
		return trialDir[:separator], true
	case "workspace_trajectory":
		if len(source.tail) != 5 {
			return "", false
		}
		return source.tail[3], source.tail[3] != ""
	case "engineering_trajectory":
		if len(source.tail) != 5 {
			return "", false
		}
		return source.tail[2], source.tail[2] != ""
	default:
		return "", false
	}
}

func rrsiTrajectoryArrayCount(object map[string]any, key string) (int, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return 0, nil
	}
	items, ok := rrsiArray(value)
	if !ok || len(items) > maxRecords {
		return 0, errors.New("RRSI trajectory count array is invalid")
	}
	return len(items), nil
}

func rrsiTrajectoryToolCalls(object map[string]any) ([]any, error) {
	value, exists := object["tool_calls"]
	if !exists || value == nil {
		return nil, nil
	}
	calls, ok := rrsiArray(value)
	if !ok || len(calls) > maxRecords {
		return nil, errors.New("tool calls are not a bounded array")
	}
	return calls, nil
}

func rrsiTrajectoryToolNameHash(value any) (string, error) {
	call, ok := rrsiObject(value)
	if !ok {
		return "", errors.New("tool call must be an object")
	}
	names := make([]string, 0, 3)
	for _, key := range []string{"name", "function_name"} {
		if raw, exists := call[key]; exists {
			name, ok := raw.(string)
			if !ok {
				return "", errors.New("tool name is not text")
			}
			names = append(names, name)
		}
	}
	if rawFunction, exists := call["function"]; exists {
		function, ok := rrsiObject(rawFunction)
		if !ok {
			return "", errors.New("tool function is not an object")
		}
		rawName, exists := function["name"]
		if !exists {
			return "", errors.New("tool function name is missing")
		}
		functionName, ok := rawName.(string)
		if !ok {
			return "", errors.New("tool function name is not text")
		}
		names = append(names, functionName)
	}
	if len(names) == 0 {
		return "", errors.New("tool call name is missing or ambiguous")
	}
	name := names[0]
	for _, candidate := range names[1:] {
		if candidate != name {
			return "", errors.New("tool call name is missing or ambiguous")
		}
	}
	if name == "" || name != strings.TrimSpace(name) || len(name) > 256 || privacy.ContainsCredentialText(name) {
		return "", errors.New("tool call name is empty, unsafe, or oversized")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", errors.New("tool call name contains a control character")
		}
	}
	return digest([]byte("rrsi/trajectory/tool-name/v1\x00" + name)), nil
}

func rrsiTrialSourceAlias(domain, jobHash, taskHash, trialHash string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "trials", domain, jobHash, taskHash, trialHash))
}

func rrsiTrajectorySourceAlias(domain, jobHash, taskHash, trialHash string) string {
	return filepath.ToSlash(filepath.Join("rrsi", "trajectories", domain, jobHash, taskHash, trialHash))
}

func parseRRSIIndex(value string, prefix byte) (int, error) {
	if !hasNumericSuffix(value, prefix) {
		return 0, errors.New("invalid RRSI trial index")
	}
	var index int
	for _, char := range value[1:] {
		index = index*10 + int(char-'0')
		if index > maxRecords {
			return 0, errors.New("RRSI trial index exceeds migration limit")
		}
	}
	return index, nil
}

func rrsiOptionalInteger(value any, minimum, maximum int) (int, bool, bool) {
	if value == nil {
		return 0, false, true
	}
	integer, ok := rrsiInteger(value)
	if !ok || integer < minimum || integer > maximum {
		return 0, false, false
	}
	return integer, true, true
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func validateRRSITrialPayload(record Record, source FileEntry, entries map[string]FileEntry) error {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(record.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("RRSI trial artifact payload is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("RRSI trial artifact payload contains trailing data")
	}
	if payload["schema_version"] != RRSITrialImportSchema || payload["source_sha256"] != source.SourceSHA256 ||
		!validDigest(stringValue(payload["job_id_sha256"])) || !validDigest(stringValue(payload["task_id_sha256"])) ||
		!validDigest(stringValue(payload["trial_key_sha256"])) {
		return errors.New("RRSI trial artifact provenance is invalid")
	}
	domain := stringValue(payload["domain"])
	kind := stringValue(payload["record_kind"])
	alias := rrsiTrialSourceAlias(domain, stringValue(payload["job_id_sha256"]),
		stringValue(payload["task_id_sha256"]), stringValue(payload["trial_key_sha256"]))
	if record.SourcePath != alias || !safeRelative(alias) {
		return errors.New("RRSI trial artifact identity alias is invalid")
	}
	sourceInfo, ok := rrsiTrialSourceForPath(source.Path)
	if !ok {
		return errors.New("RRSI trial artifact source path is invalid")
	}
	var taskID string
	switch sourceInfo.kind {
	case "coding_result":
		trialDir := sourceInfo.tail[2]
		separator := strings.LastIndex(trialDir, "__")
		if separator < 1 {
			return errors.New("RRSI coding trial source identity is invalid")
		}
		taskID = trialDir[:separator]
	case "workspace_scores":
		taskID = sourceInfo.tail[3]
	case "engineering_meta":
		taskID = sourceInfo.tail[2]
	default:
		return errors.New("RRSI trial artifact source kind is invalid")
	}
	expectedJob, expectedTask, expectedTrial := rrsiTrialHashes(sourceInfo, taskID)
	if stringValue(payload["job_id_sha256"]) != expectedJob || stringValue(payload["task_id_sha256"]) != expectedTask ||
		stringValue(payload["trial_key_sha256"]) != expectedTrial {
		return errors.New("RRSI trial hashes do not match the source path")
	}
	known := map[string]struct{}{
		"schema_version": {}, "domain": {}, "record_kind": {}, "job_id_sha256": {}, "task_id_sha256": {},
		"trial_key_sha256": {}, "source_sha256": {},
	}
	allow := func(keys ...string) {
		for _, key := range keys {
			known[key] = struct{}{}
		}
	}
	switch sourceInfo.kind {
	case "coding_result":
		if domain != "coding" || kind != "coding_result" {
			return errors.New("RRSI coding result domain mismatch")
		}
		allow("reward", "input_tokens", "output_tokens", "episodes", "exception_reported")
		if !hasRRSIFields(payload, "reward", "input_tokens", "output_tokens", "episodes", "exception_reported") {
			return errors.New("RRSI coding trial record is incomplete")
		}
		if _, ok := payload["exception_reported"].(bool); !ok {
			return errors.New("RRSI coding exception flag is invalid")
		}
		if !validNullableNumber(payload["reward"], 0, 1) || !validNullableInteger(payload["input_tokens"], 0, 1_000_000_000_000) ||
			!validNullableInteger(payload["output_tokens"], 0, 1_000_000_000_000) || !validNullableInteger(payload["episodes"], 0, 1_000_000_000) {
			return errors.New("RRSI coding trial metric is invalid")
		}
	case "workspace_scores":
		if domain != "workspace" || kind != "workspace_scores" {
			return errors.New("RRSI workspace score domain mismatch")
		}
		allow("sample_index", "score", "passed_criteria", "total_criteria", "all_pass")
		if !hasRRSIFields(payload, "sample_index", "score", "passed_criteria", "total_criteria", "all_pass") {
			return errors.New("RRSI workspace trial record is incomplete")
		}
		sample, sampleOK := rrsiInteger(payload["sample_index"])
		expectedSample, sampleErr := parseRRSIIndex(sourceInfo.tail[2], 's')
		score, scoreOK := rrsiNumber(payload["score"])
		passed, passedOK := rrsiInteger(payload["passed_criteria"])
		total, totalOK := rrsiInteger(payload["total_criteria"])
		allPass, allPassOK := payload["all_pass"].(bool)
		if !sampleOK || sampleErr != nil || sample != expectedSample || !scoreOK || score < 0 || score > 1 || !passedOK ||
			!totalOK || total < 1 || passed > total ||
			!allPassOK || allPass != (passed == total) {
			return errors.New("RRSI workspace score record is invalid")
		}
	case "engineering_meta":
		if domain != "eng" || kind != "engineering_trial" {
			return errors.New("RRSI engineering trial domain mismatch")
		}
		allow("verdict_source_sha256", "trial_index", "total_tokens", "payload_chars", "no_payload", "n_messages",
			"wall_seconds", "verifier_wall_seconds", "combined_score", "valid_rate", "passed")
		if !hasRRSIFields(payload, "verdict_source_sha256", "trial_index", "total_tokens", "payload_chars", "no_payload", "n_messages",
			"wall_seconds", "verifier_wall_seconds", "combined_score", "valid_rate", "passed") {
			return errors.New("RRSI engineering trial record is incomplete")
		}
		trial, trialOK := rrsiInteger(payload["trial_index"])
		expectedTrialIndex, trialIndexErr := parseRRSIIndex(sourceInfo.tail[3], 't')
		tokensOK := validNullableInteger(payload["total_tokens"], 0, 1_000_000_000_000)
		payloadChars, charsOK := rrsiInteger(payload["payload_chars"])
		messagesOK := validNullableInteger(payload["n_messages"], 0, 1_000_000_000)
		wall, wallOK := rrsiNumber(payload["wall_seconds"])
		verifierWallOK := validNullableNumber(payload["verifier_wall_seconds"], 0, 1e9)
		score, scoreOK := rrsiNumber(payload["combined_score"])
		valid, validOK := rrsiNumber(payload["valid_rate"])
		_, noPayloadOK := payload["no_payload"].(bool)
		_, passedOK := payload["passed"].(bool)
		if !trialOK || trialIndexErr != nil || trial != expectedTrialIndex || !tokensOK || !charsOK || payloadChars > 1_000_000_000_000 || !messagesOK ||
			!wallOK || wall < 0 || wall > 1e9 || !verifierWallOK || !scoreOK || score < 0 || score > 1 ||
			!validOK || valid < 0 || valid > 1 || !noPayloadOK || !passedOK {
			return errors.New("RRSI engineering trial metrics are invalid")
		}
		verdictPath, _ := rrsiEngineeringVerdictPath(source.Path)
		verdict, exists := entries[verdictPath]
		if !exists || verdict.Status != "joined_companion" || verdict.JoinedInto != source.Path ||
			payload["verdict_source_sha256"] != verdict.SourceSHA256 {
			return errors.New("RRSI engineering trial verdict provenance does not reconcile")
		}
	default:
		return errors.New("RRSI trial artifact kind is unsupported")
	}
	for key := range payload {
		if _, ok := known[key]; !ok {
			return errors.New("RRSI trial artifact contains an unsupported field")
		}
	}
	return nil
}

func validateRRSIToolSequence(payload map[string]any, toolCallCount int) (bool, error) {
	toolHashes, hashesPresent := payload["tool_call_name_hashes"]
	truncatedValue, truncatedPresent := payload["tool_sequence_truncated"]
	if hashesPresent != truncatedPresent {
		return false, errors.New("RRSI trajectory tool-sequence fields are incomplete")
	}
	if !hashesPresent {
		return false, nil
	}
	hashes, ok := rrsiArray(toolHashes)
	truncated, boolOK := truncatedValue.(bool)
	if !ok || len(hashes) > maxRRSIToolSequenceHashes || !boolOK {
		return false, errors.New("RRSI trajectory tool sequence is invalid or oversized")
	}
	for _, rawHash := range hashes {
		nameHash, ok := rawHash.(string)
		if !ok || !validDigest(nameHash) {
			return false, errors.New("RRSI trajectory tool-name hash is invalid")
		}
	}
	if !truncated && len(hashes) != toolCallCount ||
		truncated && (len(hashes) != maxRRSIToolSequenceHashes || toolCallCount <= len(hashes)) {
		return false, errors.New("RRSI trajectory tool sequence does not reconcile with its call count")
	}
	return true, nil
}

func validateRRSITrajectoryPayload(record Record, source FileEntry) error {
	value, err := decodeStrictRRSIProposalJSON(record.Payload)
	if err != nil {
		return errors.New("RRSI trajectory summary payload is invalid")
	}
	payload, ok := rrsiObject(value)
	if !ok || record.Type != "rrsi_trajectory_summary" || !validDigest(source.SourceSHA256) ||
		payload["schema_version"] != RRSITrajectorySummarySchema || payload["source_sha256"] != source.SourceSHA256 {
		return errors.New("RRSI trajectory summary provenance is invalid")
	}
	domain := stringValue(payload["domain"])
	kind := stringValue(payload["record_kind"])
	sourceInfo, ok := rrsiTrialSourceForPath(source.Path)
	if !ok || !isRRSITrajectoryKind(sourceInfo.kind) || sourceInfo.domain != domain || sourceInfo.kind != kind {
		return errors.New("RRSI trajectory summary source kind is invalid")
	}
	taskID, ok := rrsiTrajectoryTaskID(sourceInfo)
	if !ok {
		return errors.New("RRSI trajectory summary identity is invalid")
	}
	expectedJob, expectedTask, expectedTrial := rrsiTrialHashes(sourceInfo, taskID)
	if payload["job_id_sha256"] != expectedJob || payload["task_id_sha256"] != expectedTask ||
		payload["trial_key_sha256"] != expectedTrial ||
		record.SourcePath != rrsiTrajectorySourceAlias(domain, expectedJob, expectedTask, expectedTrial) {
		return errors.New("RRSI trajectory summary path identity does not reconcile")
	}
	known := map[string]struct{}{
		"schema_version": {}, "domain": {}, "record_kind": {}, "job_id_sha256": {}, "task_id_sha256": {},
		"trial_key_sha256": {}, "source_sha256": {},
	}
	allow := func(keys ...string) {
		for _, key := range keys {
			known[key] = struct{}{}
		}
	}
	validCount := func(key string) (int, bool) {
		count, ok := rrsiInteger(payload[key])
		return count, ok && count >= 0 && count <= maxRecords
	}
	switch kind {
	case "coding_trajectory":
		allow("step_count", "tool_call_count", "observation_result_count")
		_, stepsOK := validCount("step_count")
		toolCallCount, toolsOK := validCount("tool_call_count")
		_, observationsOK := validCount("observation_result_count")
		if !hasRRSIFields(payload, "step_count", "tool_call_count", "observation_result_count") ||
			!stepsOK || !toolsOK || !observationsOK {
			return errors.New("RRSI coding trajectory summary counts are invalid")
		}
		sequencePresent, err := validateRRSIToolSequence(payload, toolCallCount)
		if err != nil {
			return err
		}
		if sequencePresent {
			allow("tool_call_name_hashes", "tool_sequence_truncated")
		}
	case "workspace_trajectory", "engineering_trajectory":
		allow("message_count", "system_message_count", "user_message_count", "assistant_message_count",
			"tool_message_count", "other_message_count", "tool_call_count")
		messageCount, messageOK := validCount("message_count")
		systemCount, systemOK := validCount("system_message_count")
		userCount, userOK := validCount("user_message_count")
		assistantCount, assistantOK := validCount("assistant_message_count")
		toolMessageCount, toolMessageOK := validCount("tool_message_count")
		otherCount, otherOK := validCount("other_message_count")
		toolCallCount, toolCallOK := validCount("tool_call_count")
		if !hasRRSIFields(payload, "message_count", "system_message_count", "user_message_count", "assistant_message_count",
			"tool_message_count", "other_message_count", "tool_call_count") || !messageOK || !systemOK || !userOK ||
			!assistantOK || !toolMessageOK || !otherOK || !toolCallOK ||
			systemCount+userCount+assistantCount+toolMessageCount+otherCount != messageCount {
			return errors.New("RRSI trajectory summary message counts are invalid")
		}
		sequencePresent, err := validateRRSIToolSequence(payload, toolCallCount)
		if err != nil {
			return err
		}
		if sequencePresent {
			allow("tool_call_name_hashes", "tool_sequence_truncated")
		}
	default:
		return errors.New("RRSI trajectory summary kind is unsupported")
	}
	for key := range payload {
		if _, ok := known[key]; !ok {
			return errors.New("RRSI trajectory summary contains an unsupported field")
		}
	}
	return nil
}

func hasRRSIFields(value map[string]any, fields ...string) bool {
	for _, field := range fields {
		if _, exists := value[field]; !exists {
			return false
		}
	}
	return true
}

func hasRRSIException(value any) bool {
	switch item := value.(type) {
	case nil:
		return false
	case bool:
		return item
	case string:
		return item != ""
	case float64:
		return item != 0
	case map[string]any:
		return len(item) != 0
	case []any:
		return len(item) != 0
	default:
		return true
	}
}

func validNullableNumber(value any, minimum, maximum float64) bool {
	if value == nil {
		return true
	}
	number, ok := rrsiNumber(value)
	return ok && !math.IsNaN(number) && number >= minimum && number <= maximum
}

func validNullableInteger(value any, minimum, maximum int) bool {
	if value == nil {
		return true
	}
	integer, ok := rrsiInteger(value)
	return ok && integer >= minimum && integer <= maximum
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}
