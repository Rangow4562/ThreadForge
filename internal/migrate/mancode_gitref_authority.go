package migrate

import (
	"encoding/json"
	"errors"
)

const mancodeGitRefAuthorityTransitionType = "mancode_git_ref_authority_transition"
const mancodeGitRefAuthorityTransitionSchema = "threadforge.mancode-git-ref-authority-transition.v1"

type mancodeGitRefAuthorityTransitionImport struct {
	SchemaVersion             string `json:"schema_version"`
	Authority                 string `json:"authority"`
	HumanReviewRequired       bool   `json:"human_review_required"`
	ExecutionAuthorized       bool   `json:"execution_authorized"`
	Transition                string `json:"transition"`
	SuccessorMode             string `json:"successor_mode"`
	SuccessorEpoch            int64  `json:"successor_epoch"`
	TransportEpoch            int64  `json:"transport_epoch"`
	EpochAdvance              int64  `json:"epoch_advance"`
	OccurredAt                string `json:"occurred_at"`
	OperationReferenceOmitted bool   `json:"operation_reference_omitted"`
	WorkspaceIdentityOmitted  bool   `json:"workspace_identity_omitted"`
}

func normalizeMancodeGitRefAuthorityTransitions(object map[string]any, transportEpoch int64) ([]normalizedItem, error) {
	state := mancodeString(object["authorityState"])
	if state == "active" {
		return nil, nil
	}
	transitions := make([]normalizedItem, 0, 2)
	appendTransition := func(value any, timestampKey, transition string) error {
		if !validMancodeGitRefAuthorityDetail(value, timestampKey, transportEpoch, mancodeString(object["lastOperationId"])) {
			return errors.New("Mancode git-ref authority transition is invalid")
		}
		detail := value.(map[string]any)
		epoch, _ := mancodeGitRefPositiveInt(detail["successorEpoch"])
		occurredAt, ok := detail[timestampKey].(string)
		if !ok {
			return errors.New("Mancode git-ref authority transition timestamp is invalid")
		}
		payload, err := json.Marshal(mancodeGitRefAuthorityTransitionImport{
			SchemaVersion: mancodeGitRefAuthorityTransitionSchema, Authority: "local_pending_review",
			HumanReviewRequired: true, ExecutionAuthorized: false, Transition: transition,
			SuccessorMode: mancodeString(detail["successorMode"]), SuccessorEpoch: epoch,
			TransportEpoch: transportEpoch, EpochAdvance: epoch - transportEpoch, OccurredAt: occurredAt,
			OperationReferenceOmitted: true, WorkspaceIdentityOmitted: true,
		})
		if err != nil {
			return errors.New("Mancode git-ref authority transition summary could not be encoded")
		}
		transitions = append(transitions, normalizedItem{Type: mancodeGitRefAuthorityTransitionType, Payload: payload})
		return nil
	}
	if state == "frozen" || state == "tombstoned" {
		if err := appendTransition(object["authorityFreeze"], "frozenAt", "freeze"); err != nil {
			return nil, err
		}
	}
	if state == "tombstoned" {
		if err := appendTransition(object["authorityTombstone"], "tombstonedAt", "tombstone"); err != nil {
			return nil, err
		}
	}
	return transitions, nil
}
