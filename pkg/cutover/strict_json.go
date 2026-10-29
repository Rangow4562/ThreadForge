package cutover

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	maxCutoverJSONDepth  = 128
	maxCutoverJSONTokens = 4_000_000
)

// DecodeReportJSON decodes a top-level cutover report using the same strict
// duplicate-key, field, and resource checks as the report's evidence artifacts.
func DecodeReportJSON(data []byte) (Report, error) {
	var report Report
	if err := decodeUniqueJSON(data, &report); err != nil {
		return report, err
	}
	return report, nil
}

// decodeUniqueJSON rejects duplicate object keys before decoding into a typed
// value. encoding/json otherwise accepts duplicates and keeps the later value,
// which makes signed evidence ambiguous to other JSON implementations.
func decodeUniqueJSON(data []byte, target any) error {
	scan := &uniqueJSONScanner{decoder: json.NewDecoder(bytes.NewReader(data))}
	if err := scan.value(0); err != nil {
		return fmt.Errorf("invalid JSON structure: %w", err)
	}
	if _, err := scan.token(); err != io.EOF {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return fmt.Errorf("invalid JSON trailing data: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("JSON must contain exactly one value")
	}
	return nil
}

type uniqueJSONScanner struct {
	decoder *json.Decoder
	tokens  int
}

func (s *uniqueJSONScanner) token() (json.Token, error) {
	s.tokens++
	if s.tokens > maxCutoverJSONTokens {
		return nil, errors.New("JSON token limit exceeded")
	}
	return s.decoder.Token()
}

func (s *uniqueJSONScanner) value(depth int) error {
	if depth > maxCutoverJSONDepth {
		return errors.New("JSON nesting limit exceeded")
	}
	token, err := s.token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for s.decoder.More() {
			keyToken, err := s.token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := s.value(depth + 1); err != nil {
				return err
			}
		}
		end, err := s.token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for s.decoder.More() {
			if err := s.value(depth + 1); err != nil {
				return err
			}
		}
		end, err := s.token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}
