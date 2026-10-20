package continuity

import (
	"strings"
	"testing"
)

func TestTaskHandoffEventValidatesDocumentedLifecycle(t *testing.T) {
	base := TaskHandoff{
		SchemaVersion: TaskHandoffSchemaVersion, ID: "handoff-one", ProjectID: "repo-a", TaskID: "task-one",
		FromOwner: "alice", ToOwner: "bob", TaskRevision: 4, ClaimID: "claim-one", ClaimRevision: 2,
		BaselineSHA256: strings.Repeat("a", 64), CheckpointEntryID: "checkpoint-one",
		CheckpointSummary: "Parser is repaired; run the focused suite next.", Status: TaskHandoffDraft, Revision: 1,
		CreatedAt: "2026-10-19T12:00:00Z", UpdatedAt: "2026-10-19T12:00:00Z",
	}
	cases := []struct {
		name   string
		action string
		want   TaskHandoff
	}{
		{"drafted", "drafted", base},
		{"offered", "offered", func() TaskHandoff {
			value := base
			value.Status = TaskHandoffOffered
			value.Revision = 2
			value.UpdatedAt = "2026-10-19T12:00:01Z"
			return value
		}()},
		{"accepted", "accepted", func() TaskHandoff {
			value := base
			value.Status = TaskHandoffAccepted
			value.Revision = 3
			value.UpdatedAt = "2026-10-19T12:00:02Z"
			value.TransferredTaskRevision = 5
			value.TransferredClaimRevision = 3
			return value
		}()},
		{"rejected", "rejected", func() TaskHandoff {
			value := base
			value.Status = TaskHandoffRejected
			value.Revision = 3
			value.UpdatedAt = "2026-10-19T12:00:02Z"
			return value
		}()},
		{"cancelled", "cancelled", func() TaskHandoff {
			value := base
			value.Status = TaskHandoffCancelled
			value.Revision = 2
			value.UpdatedAt = "2026-10-19T12:00:01Z"
			return value
		}()},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			expected := 0
			if test.action != "drafted" {
				expected = test.want.Revision - 1
			}
			if err := ValidateTaskHandoffEvent(TaskHandoffEvent{
				SchemaVersion: TaskHandoffEventSchemaVersion, Action: test.action,
				ExpectedRevision: expected, Handoff: test.want,
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTaskHandoffAcceptanceRequiresLinkedOwnerAndClaimRevisions(t *testing.T) {
	handoff := TaskHandoff{
		SchemaVersion: TaskHandoffSchemaVersion, ID: "handoff-one", ProjectID: "repo-a", TaskID: "task-one",
		FromOwner: "alice", ToOwner: "bob", TaskRevision: 4, ClaimID: "claim-one", ClaimRevision: 2,
		BaselineSHA256: strings.Repeat("a", 64), CheckpointEntryID: "checkpoint-one",
		CheckpointSummary: "Checkpoint", Status: TaskHandoffAccepted, Revision: 3,
		CreatedAt: "2026-10-19T12:00:00Z", UpdatedAt: "2026-10-19T12:00:02Z",
		TransferredTaskRevision: 5, TransferredClaimRevision: 3,
	}
	event := TaskHandoffEvent{SchemaVersion: TaskHandoffEventSchemaVersion, Action: "accepted", ExpectedRevision: 2, Handoff: handoff}
	if err := ValidateTaskHandoffEvent(event); err != nil {
		t.Fatal(err)
	}
	event.Handoff.TransferredTaskRevision++
	if err := ValidateTaskHandoffEvent(event); err == nil {
		t.Fatal("accepted handoff with a mismatched task-head revision was valid")
	}
}
