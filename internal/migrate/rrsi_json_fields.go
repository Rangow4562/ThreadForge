package migrate

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
)

func rrsiJSONFieldTypesMatch(fields map[string]json.RawMessage, stringFields, boolFields []string,
	integerFields map[string][2]int64) bool {
	for _, key := range stringFields {
		if !rrsiJSONStringValue(fields[key]) {
			return false
		}
	}
	for _, key := range boolFields {
		if !rrsiJSONBoolValue(fields[key]) {
			return false
		}
	}
	for key, bounds := range integerFields {
		if !rrsiJSONIntegerValue(fields[key], bounds[0], bounds[1]) {
			return false
		}
	}
	return true
}

func rrsiJSONStringValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return false
	}
	var value string
	return json.Unmarshal(trimmed, &value) == nil
}

func rrsiJSONBoolValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false"))
}

func rrsiJSONIntegerValue(raw json.RawMessage, minimum, maximum int64) bool {
	trimmed := bytes.TrimSpace(raw)
	value, err := strconv.ParseInt(string(trimmed), 10, 64)
	return err == nil && value >= minimum && value <= maximum
}

func rrsiJSONNumberValue(raw json.RawMessage, minimum, maximum float64) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false
	}
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	parsed, err := number.Float64()
	return err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed >= minimum && parsed <= maximum
}
