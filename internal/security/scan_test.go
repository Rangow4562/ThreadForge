package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
)

func TestScansCompleteVendoredRuleCorpus(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(tracePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join("..", "..", "third_party", "agent-beacon", "rules")
	result, err := ScanFile(context.Background(), tracePath, "", rulesPath, beacon.SeverityInfo)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.EventCount != 0 || result.RuleCount < 20 {
		t.Fatalf("vendored full corpus was not loaded: %#v", result)
	}
}
