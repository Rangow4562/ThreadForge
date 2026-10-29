// Package strictjson decodes bounded JSON while rejecting duplicate object
// keys, unknown fields, and trailing values.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	MaxDepth  = 128
	MaxTokens = 4_000_000
)

// Decode first validates JSON structure and unique object keys, then decodes
// one value into target with unknown fields rejected.
func Decode(data []byte, target any) error {
	scan := &scanner{decoder: json.NewDecoder(bytes.NewReader(data)), maxTokens: MaxTokens}
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

type scanner struct {
	decoder   *json.Decoder
	tokens    int
	maxTokens int
}

func (s *scanner) token() (json.Token, error) {
	s.tokens++
	if s.tokens > s.maxTokens {
		return nil, errors.New("JSON token limit exceeded")
	}
	return s.decoder.Token()
}

func (s *scanner) value(depth int) error {
	if depth > MaxDepth {
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
