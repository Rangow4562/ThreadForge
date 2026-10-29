package strictjson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeRejectsDuplicateObjectKeys(t *testing.T) {
	tests := []string{
		`{"value":1,"value":2}`,
		`{"nested":{"value":1,"value":2}}`,
		`{"items":[{"value":1,"value":2}]}`,
		`{"value":1,"va\u006cue":2}`,
	}
	for _, input := range tests {
		var target any
		if err := Decode([]byte(input), &target); err == nil || !strings.Contains(err.Error(), "duplicate object key") {
			t.Fatalf("duplicate keys were not rejected for %s: %v", input, err)
		}
	}
}

func TestDecodeRejectsUnknownAndTrailingValues(t *testing.T) {
	type value struct {
		Known string `json:"known"`
	}
	for _, input := range []string{`{"known":"ok","extra":1}`, `{"known":"ok"} {}`} {
		var target value
		if err := Decode([]byte(input), &target); err == nil {
			t.Fatalf("invalid strict JSON was accepted: %s", input)
		}
	}
}

func TestDecodeEnforcesNestingLimitAndAllowsSiblingKeyReuse(t *testing.T) {
	tooDeep := "0"
	for range MaxDepth + 2 {
		tooDeep = "[" + tooDeep + "]"
	}
	var target any
	if err := Decode([]byte(tooDeep), &target); err == nil || !strings.Contains(err.Error(), "nesting limit") {
		t.Fatalf("excessively nested JSON was not rejected: %v", err)
	}
	if err := Decode([]byte(`{"items":[{"value":1},{"value":2}]}`), &target); err != nil {
		t.Fatalf("same key in separate objects should be valid: %v", err)
	}
}

func TestScannerEnforcesTokenLimit(t *testing.T) {
	scan := &scanner{decoder: json.NewDecoder(strings.NewReader(`[0,0]`)), maxTokens: 3}
	if err := scan.value(0); err == nil || !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("token limit was not enforced: %v", err)
	}
}
