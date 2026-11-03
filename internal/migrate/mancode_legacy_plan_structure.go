package migrate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const (
	mancodePlanBaselineStart      = "<!-- mancode:plan-baseline:start -->"
	mancodePlanBaselineEnd        = "<!-- mancode:plan-baseline:end -->"
	mancodePlanDeliveryStart      = "<!-- mancode:delivery-record:start -->"
	mancodePlanDeliveryEnd        = "<!-- mancode:delivery-record:end -->"
	maxMancodePlanSections        = 32
	maxMancodePlanHeadings        = 1024
	maxMancodePlanSectionBytes    = 8 << 10
	maxMancodePlanCandidateBytes  = 32 << 10
	maxMancodePlanScopeEntries    = 128
	maxMancodePlanScopeValueBytes = 1 << 10
	maxMancodePlanScopeBytes      = 8 << 10
)

var (
	mancodeProgressTaskPattern      = regexp.MustCompile(`^<!-- mancode:progress-task ([A-Za-z0-9][A-Za-z0-9._:-]{0,127}) -->$`)
	mancodeAcceptanceIDPattern      = regexp.MustCompile(`\bAC-[A-Za-z0-9][A-Za-z0-9-]{0,63}\b`)
	mancodePlanSemanticHeadingKinds = map[string]string{
		"implementation scope": "implementation_scope",
		"scope":                "implementation_scope",
		"technical decisions":  "technical_decisions",
		"technical choices":    "technical_decisions",
		"risks and rollback":   "risks_and_rollback",
		"risk and rollback":    "risks_and_rollback",
		"risks & rollback":     "risks_and_rollback",
		"validation":           "validation",
		"verification":         "validation",
		"completion criteria":  "completion_criteria",
		"acceptance criteria":  "completion_criteria",
		"domain matrix":        "domain_matrix",
		"defaults":             "defaults",
		"behavior":             "behavior",
	}
)

type mancodeLegacyPlanStructure struct {
	MarkerStatus          string `json:"marker_status"`
	BaselineBlockPresent  bool   `json:"baseline_block_present"`
	ProgressTaskPresent   bool   `json:"progress_task_present"`
	DeliveryRecordPresent bool   `json:"delivery_record_present"`
	DomainMatrixPresent   bool   `json:"domain_matrix_present"`
	AcceptanceMarkerCount int    `json:"acceptance_marker_count"`
	CoachSignal           string `json:"coach_signal"`
}

type markerLocation struct {
	line int
	id   string
}

type mancodeLegacyPlanCandidates struct {
	Status            string                              `json:"status"`
	SectionsTruncated bool                                `json:"sections_truncated"`
	Sections          []mancodeLegacyPlanSectionCandidate `json:"sections"`
	Scope             mancodeLegacyPlanScopeCandidates    `json:"scope_candidates"`
}

type mancodeLegacyPlanSectionCandidate struct {
	Kind                   string `json:"kind"`
	SourceStartLine        int    `json:"source_start_line"`
	SourceEndLine          int    `json:"source_end_line"`
	CandidateText          string `json:"candidate_text"`
	CandidateTextBytes     int    `json:"candidate_text_bytes"`
	CandidateTextSHA256    string `json:"candidate_text_sha256"`
	CandidateTextTruncated bool   `json:"candidate_text_truncated"`
}

type mancodeLegacyPlanScopeCandidates struct {
	Status        string                        `json:"status"`
	Truncated     bool                          `json:"truncated"`
	OmittedValues int                           `json:"omitted_values"`
	Entries       []mancodeLegacyPlanScopeEntry `json:"entries"`
}

type mancodeLegacyPlanScopeEntry struct {
	Field                  string `json:"field"`
	SourceLine             int    `json:"source_line"`
	CandidateText          string `json:"candidate_text"`
	CandidateTextBytes     int    `json:"candidate_text_bytes"`
	CandidateTextSHA256    string `json:"candidate_text_sha256"`
	CandidateTextTruncated bool   `json:"candidate_text_truncated"`
}

type mancodePlanHeading struct {
	line  int
	level int
	kind  string
}

func extractMancodeLegacyPlanCandidates(content string) mancodeLegacyPlanCandidates {
	result := mancodeLegacyPlanCandidates{
		Status: "not_extracted", Sections: []mancodeLegacyPlanSectionCandidate{},
		Scope: mancodeLegacyPlanScopeCandidates{Status: "not_extracted", Entries: []mancodeLegacyPlanScopeEntry{}},
	}
	structure := analyzeMancodeLegacyPlanStructure(content)
	if structure.MarkerStatus != "well_formed" {
		return result
	}
	lines := strings.Split(content, "\n")
	baselineStart, baselineEnd, found := uniquePlanBaseline(lines)
	if !found {
		return result
	}

	headings := make([]mancodePlanHeading, 0, 16)
	recognizedHeadings := 0
	headingsTruncated := false
	fenceChar := byte(0)
	fenceLength := 0
	for lineNumber := baselineStart + 1; lineNumber < baselineEnd; lineNumber++ {
		line := strings.TrimPrefix(lines[lineNumber], "\ufeff")
		if fenceChar != 0 {
			if isMancodeFenceClose(line, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := mancodeFenceOpening(line); ok {
			fenceChar = char
			fenceLength = length
			continue
		}
		level, kind, ok := mancodePlanSemanticHeading(line)
		if ok {
			if len(headings) >= maxMancodePlanHeadings {
				headingsTruncated = true
				break
			}
			headings = append(headings, mancodePlanHeading{line: lineNumber, level: level, kind: kind})
			if kind != "" {
				recognizedHeadings++
			}
		}
	}
	if recognizedHeadings == 0 {
		return result
	}

	result.Status = "candidate_sections_found"
	result.SectionsTruncated = headingsTruncated
	scopeSectionFound := false
	scopeFieldsPresent := false
	scopeCandidateBytes := 0
	remainingBytes := maxMancodePlanCandidateBytes
	for index, heading := range headings {
		if heading.kind == "" {
			continue
		}
		if len(result.Sections) >= maxMancodePlanSections || remainingBytes <= 0 {
			result.SectionsTruncated = true
			break
		}
		sectionEnd := baselineEnd
		for next := index + 1; next < len(headings); next++ {
			if headings[next].level <= heading.level || headings[next].kind != "" {
				sectionEnd = headings[next].line
				break
			}
		}
		sectionText := mancodeLegacyPlanSectionText(lines, heading.line+1, sectionEnd)
		if sectionText == "" {
			continue
		}
		if heading.kind == "implementation_scope" {
			scopeSectionFound = true
			collectMancodePlanScopeCandidates(
				lines, heading.line+1, sectionEnd, heading.level,
				&result.Scope, &scopeFieldsPresent, &scopeCandidateBytes,
			)
		}
		candidateText := sectionText
		textTruncated := false
		if len(candidateText) > maxMancodePlanSectionBytes {
			candidateText = truncateMancodePlanText(candidateText, maxMancodePlanSectionBytes)
			textTruncated = true
		}
		if len(candidateText) > remainingBytes {
			candidateText = truncateMancodePlanText(candidateText, remainingBytes)
			textTruncated = true
		}
		if candidateText == "" {
			result.SectionsTruncated = true
			break
		}
		result.Sections = append(result.Sections, mancodeLegacyPlanSectionCandidate{
			Kind: heading.kind, SourceStartLine: heading.line + 1, SourceEndLine: sectionEnd,
			CandidateText: candidateText, CandidateTextBytes: len(sectionText),
			CandidateTextSHA256: digest([]byte(sectionText)), CandidateTextTruncated: textTruncated,
		})
		remainingBytes -= len(candidateText)
		if textTruncated {
			result.SectionsTruncated = true
		}
	}
	if len(result.Sections) == 0 {
		result.Status = "not_extracted"
	}
	if scopeSectionFound {
		switch {
		case len(result.Scope.Entries) > 0:
			result.Scope.Status = "candidate_values_found"
		case scopeFieldsPresent:
			result.Scope.Status = "fields_without_values"
		default:
			result.Scope.Status = "no_explicit_scope_fields"
		}
	}
	return result
}

func collectMancodePlanScopeCandidates(
	lines []string,
	start int,
	end int,
	parentHeadingLevel int,
	result *mancodeLegacyPlanScopeCandidates,
	fieldsPresent *bool,
	totalCandidateBytes *int,
) {
	activeField := ""
	fenceChar := byte(0)
	fenceLength := 0
	for lineNumber := start; lineNumber < end; lineNumber++ {
		line := strings.TrimPrefix(lines[lineNumber], "\ufeff")
		if fenceChar != 0 {
			if isMancodeFenceClose(line, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := mancodeFenceOpening(line); ok {
			fenceChar = char
			fenceLength = length
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "<!-- mancode:") {
			continue
		}
		if level, headingText, ok := mancodeMarkdownHeading(line); ok {
			if level <= parentHeadingLevel {
				break
			}
			if field := mancodePlanScopeField(headingText); field != "" {
				*fieldsPresent = true
				activeField = field
			} else {
				activeField = ""
			}
			continue
		}

		listText, isListItem := mancodePlanListItem(line)
		assignmentText := strings.TrimSpace(line)
		if isListItem {
			assignmentText = listText
		}
		field, value, isField := mancodePlanScopeAssignment(assignmentText)
		if isField {
			*fieldsPresent = true
			if value != "" {
				activeField = ""
				appendMancodePlanScopeCandidate(result, totalCandidateBytes, field, lineNumber+1, value)
			} else {
				activeField = field
			}
			continue
		}
		if activeField != "" {
			if isListItem {
				appendMancodePlanScopeCandidate(result, totalCandidateBytes, activeField, lineNumber+1, listText)
			} else {
				activeField = ""
			}
		}
	}
}

func appendMancodePlanScopeCandidate(
	result *mancodeLegacyPlanScopeCandidates,
	totalCandidateBytes *int,
	field string,
	sourceLine int,
	value string,
) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if privacy.ContainsCredentialText(value) ||
		((field == "include" || field == "exclude") && !mancodePlanRelativeScopeCandidate(value)) {
		incrementMancodePlanScopeOmitted(result)
		return
	}
	if len(result.Entries) >= maxMancodePlanScopeEntries || *totalCandidateBytes >= maxMancodePlanScopeBytes {
		result.Truncated = true
		incrementMancodePlanScopeOmitted(result)
		return
	}
	maximum := maxMancodePlanScopeValueBytes
	if remaining := maxMancodePlanScopeBytes - *totalCandidateBytes; maximum > remaining {
		maximum = remaining
	}
	candidateText := truncateMancodePlanText(value, maximum)
	if candidateText == "" {
		result.Truncated = true
		incrementMancodePlanScopeOmitted(result)
		return
	}
	textTruncated := len(candidateText) < len(value)
	result.Entries = append(result.Entries, mancodeLegacyPlanScopeEntry{
		Field: field, SourceLine: sourceLine, CandidateText: candidateText,
		CandidateTextBytes: len(value), CandidateTextSHA256: digest([]byte(value)),
		CandidateTextTruncated: textTruncated,
	})
	*totalCandidateBytes += len(candidateText)
	if textTruncated {
		result.Truncated = true
	}
}

func incrementMancodePlanScopeOmitted(result *mancodeLegacyPlanScopeCandidates) {
	if result.OmittedValues < 1<<20 {
		result.OmittedValues++
	}
}

func mancodePlanScopeAssignment(line string) (field string, value string, ok bool) {
	separator := strings.IndexByte(line, ':')
	if separator < 1 {
		return "", "", false
	}
	field = mancodePlanScopeField(line[:separator])
	if field == "" {
		return "", "", false
	}
	return field, strings.Trim(strings.TrimSpace(line[separator+1:]), "`\"' "), true
}

func mancodePlanScopeField(value string) string {
	switch strings.ToLower(strings.Trim(value, "*_` ")) {
	case "include":
		return "include"
	case "exclude":
		return "exclude"
	case "modules":
		return "modules"
	default:
		return ""
	}
}

func mancodePlanListItem(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || !strings.ContainsRune("-*+", rune(trimmed[0])) || trimmed[1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[2:]), true
}

func mancodePlanRelativeScopeCandidate(value string) bool {
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") ||
		strings.HasPrefix(value, "~") || strings.Contains(value, "\\") || strings.Contains(value, "://") ||
		(len(value) >= 2 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':') {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func uniquePlanBaseline(lines []string) (int, int, bool) {
	var starts, ends []int
	fenceChar := byte(0)
	fenceLength := 0
	for lineNumber, line := range lines {
		line = strings.TrimPrefix(line, "\ufeff")
		if fenceChar != 0 {
			if isMancodeFenceClose(line, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := mancodeFenceOpening(line); ok {
			fenceChar = char
			fenceLength = length
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case mancodePlanBaselineStart:
			starts = append(starts, lineNumber)
		case mancodePlanBaselineEnd:
			ends = append(ends, lineNumber)
		}
	}
	start, end, ok := uniqueOrderedPair(starts, ends)
	return start, end, ok
}

func mancodePlanSemanticHeading(line string) (int, string, bool) {
	level, text, ok := mancodeMarkdownHeading(line)
	if !ok {
		return 0, "", false
	}
	return level, mancodePlanSemanticHeadingKinds[text], true
}

func mancodeMarkdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' && level < 6 {
		level++
	}
	if level == 0 || level == len(trimmed) || (trimmed[level] != ' ' && trimmed[level] != '\t') {
		return 0, "", false
	}
	text := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(trimmed[level:])), " "))
	text = strings.TrimSpace(strings.TrimRight(text, "# "))
	text = strings.TrimSuffix(text, ":")
	return level, text, true
}

func mancodeLegacyPlanSectionText(lines []string, start, end int) string {
	if start < 0 || end > len(lines) || start >= end {
		return ""
	}
	var selected []string
	fenceChar := byte(0)
	fenceLength := 0
	for lineNumber := start; lineNumber < end; lineNumber++ {
		line := strings.TrimPrefix(lines[lineNumber], "\ufeff")
		if fenceChar != 0 {
			if isMancodeFenceClose(line, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := mancodeFenceOpening(line); ok {
			fenceChar = char
			fenceLength = length
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!-- mancode:") {
			continue
		}
		selected = append(selected, line)
	}
	return strings.TrimSpace(strings.Join(selected, "\n"))
}

func truncateMancodePlanText(value string, maximum int) string {
	if maximum <= 0 || value == "" {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func analyzeMancodeLegacyPlanStructure(content string) mancodeLegacyPlanStructure {
	positions := map[string][]int{
		mancodePlanBaselineStart: nil,
		mancodePlanBaselineEnd:   nil,
		mancodePlanDeliveryStart: nil,
		mancodePlanDeliveryEnd:   nil,
	}
	var progress []markerLocation
	var acceptanceIDs []markerLocation
	var domainMatrixLines []int
	seenAnyMarker := false
	malformedMarker := false
	coachSignal := "absent"
	firstNonemptySeen := false
	fenceChar := byte(0)
	fenceLength := 0

	for lineNumber, line := range strings.Split(content, "\n") {
		line = strings.TrimPrefix(line, "\ufeff")
		if fenceChar != 0 {
			if isMancodeFenceClose(line, fenceChar, fenceLength) {
				fenceChar = 0
				fenceLength = 0
			}
			continue
		}
		if char, length, ok := mancodeFenceOpening(line); ok {
			fenceChar = char
			fenceLength = length
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !firstNonemptySeen {
			firstNonemptySeen = true
			switch trimmed {
			case "READY_FOR_PLAN":
				coachSignal = "ready_for_plan"
			case "NEEDS_CLARIFICATION":
				coachSignal = "needs_clarification"
			}
		}

		matchedBlockMarker := false
		for marker := range positions {
			if trimmed == marker {
				positions[marker] = append(positions[marker], lineNumber)
				seenAnyMarker = true
				matchedBlockMarker = true
				break
			}
			if strings.Contains(trimmed, marker) {
				seenAnyMarker = true
				malformedMarker = true
			}
		}
		if matchedBlockMarker {
			continue
		}
		if strings.Contains(trimmed, "<!-- mancode:progress-task") {
			seenAnyMarker = true
			match := mancodeProgressTaskPattern.FindStringSubmatch(trimmed)
			if len(match) != 2 {
				malformedMarker = true
			} else {
				progress = append(progress, markerLocation{line: lineNumber, id: match[1]})
			}
			continue
		}
		if mancodeDomainMatrixHeading(trimmed) {
			domainMatrixLines = append(domainMatrixLines, lineNumber)
		}
		for _, id := range mancodeAcceptanceIDPattern.FindAllString(trimmed, -1) {
			acceptanceIDs = append(acceptanceIDs, markerLocation{line: lineNumber, id: id})
		}
	}

	baselineStart, baselineEnd, baselineOK := uniqueOrderedPair(positions[mancodePlanBaselineStart], positions[mancodePlanBaselineEnd])
	deliveryStart, deliveryEnd, deliveryOK := uniqueOrderedPair(positions[mancodePlanDeliveryStart], positions[mancodePlanDeliveryEnd])
	progressOK := len(progress) == 0 || (len(progress) == 1 && baselineOK &&
		progress[0].line > baselineStart && progress[0].line < baselineEnd)
	validOrder := baselineOK && deliveryOK && baselineStart < baselineEnd &&
		baselineEnd < deliveryStart && deliveryStart < deliveryEnd

	structure := mancodeLegacyPlanStructure{
		MarkerStatus:          "unmarked",
		BaselineBlockPresent:  baselineOK,
		DeliveryRecordPresent: deliveryOK,
		CoachSignal:           coachSignal,
	}
	if len(progress) == 1 && progressOK {
		structure.ProgressTaskPresent = true
	}
	if baselineOK {
		for _, line := range domainMatrixLines {
			if line > baselineStart && line < baselineEnd {
				structure.DomainMatrixPresent = true
				break
			}
		}
		uniqueIDs := make(map[string]struct{})
		for _, marker := range acceptanceIDs {
			if marker.line > baselineStart && marker.line < baselineEnd {
				uniqueIDs[marker.id] = struct{}{}
			}
		}
		structure.AcceptanceMarkerCount = len(uniqueIDs)
	}
	if seenAnyMarker {
		if validOrder && progressOK && !malformedMarker {
			structure.MarkerStatus = "well_formed"
		} else {
			structure.MarkerStatus = "malformed"
		}
	}
	return structure
}

func uniqueOrderedPair(start, end []int) (int, int, bool) {
	if len(start) != 1 || len(end) != 1 || start[0] >= end[0] {
		return 0, 0, false
	}
	return start[0], end[0], true
}

func mancodeFenceOpening(line string) (byte, int, bool) {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 || indent == len(line) {
		return 0, 0, false
	}
	body := line[indent:]
	char := body[0]
	if char != '`' && char != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(body) && body[length] == char {
		length++
	}
	if length < 3 || (char == '`' && strings.Contains(body[length:], "`")) {
		return 0, 0, false
	}
	return char, length, true
}

func isMancodeFenceClose(line string, char byte, minimumLength int) bool {
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 || indent == len(line) {
		return false
	}
	body := line[indent:]
	length := 0
	for length < len(body) && body[length] == char {
		length++
	}
	return length >= minimumLength && strings.TrimSpace(body[length:]) == ""
}

func mancodeDomainMatrixHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	headingLength := 0
	for headingLength < len(trimmed) && trimmed[headingLength] == '#' && headingLength < 6 {
		headingLength++
	}
	if headingLength == 0 || headingLength == len(trimmed) || (trimmed[headingLength] != ' ' && trimmed[headingLength] != '\t') {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(trimmed[headingLength:]))
	return text == "domain matrix" || strings.HasPrefix(text, "domain matrix ") || strings.HasPrefix(text, "domain matrix:")
}
