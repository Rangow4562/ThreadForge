package migrate

import (
	"regexp"
	"strings"
)

const (
	mancodePlanBaselineStart = "<!-- mancode:plan-baseline:start -->"
	mancodePlanBaselineEnd   = "<!-- mancode:plan-baseline:end -->"
	mancodePlanDeliveryStart = "<!-- mancode:delivery-record:start -->"
	mancodePlanDeliveryEnd   = "<!-- mancode:delivery-record:end -->"
)

var (
	mancodeProgressTaskPattern = regexp.MustCompile(`^<!-- mancode:progress-task ([A-Za-z0-9][A-Za-z0-9._:-]{0,127}) -->$`)
	mancodeAcceptanceIDPattern = regexp.MustCompile(`\bAC-[A-Za-z0-9][A-Za-z0-9-]{0,63}\b`)
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
