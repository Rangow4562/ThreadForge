package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const mancodeDesignPolicyImportSchema = "threadforge.mancode-design-policy.v1"

type mancodeDesignPolicyImport struct {
	SchemaVersion       string `json:"schema_version"`
	Authority           string `json:"authority"`
	HumanReviewRequired bool   `json:"human_review_required"`
	ExecutionAuthorized bool   `json:"execution_authorized"`
	Revision            int64  `json:"revision"`
	SourceEnabled       bool   `json:"source_enabled"`
	Preset              string `json:"preset"`
	IconPolicy          string `json:"icon_policy"`
	EmojiPolicy         string `json:"emoji_policy"`
	MotionPolicy        string `json:"motion_policy"`
	BrowserValidation   string `json:"browser_validation"`
	UpdatedAt           string `json:"updated_at"`
}

func normalizeMancodeDesignPolicy(value any) ([]normalizedItem, bool, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, true, errors.New("Mancode design policy must be an object")
	}
	keys := []string{
		"schemaVersion", "revision", "enabled", "preset", "iconPolicy", "emojiPolicy",
		"motionPolicy", "browserValidation", "lastOperationId", "updatedAt",
	}
	if !mancodeHasAllKeys(object, keys) || !mancodeHasOnlyKeys(object, keys) {
		return nil, true, errors.New("Mancode design policy does not match the supported schema")
	}
	version, versionOK := mancodeIntegerValue(object["schemaVersion"])
	revision, revisionOK := mancodeIntegerValue(object["revision"])
	enabled, enabledOK := object["enabled"].(bool)
	preset := mancodeString(object["preset"])
	iconPolicy := mancodeString(object["iconPolicy"])
	emojiPolicy := mancodeString(object["emojiPolicy"])
	motionPolicy := mancodeString(object["motionPolicy"])
	browserValidation := mancodeString(object["browserValidation"])
	updatedAt := mancodeString(object["updatedAt"])
	if !versionOK || version != 1 || !revisionOK || revision < 1 || !enabledOK ||
		!mancodeHasString([]string{"preserve", "refine", "experimental"}, preset) ||
		!mancodeHasString([]string{"existing-first", "lucide"}, iconPolicy) ||
		!mancodeHasString([]string{"allow", "forbid-as-interface-icon"}, emojiPolicy) ||
		!mancodeHasString([]string{"minimal", "purposeful"}, motionPolicy) ||
		!mancodeHasString([]string{"off", "when-available", "required"}, browserValidation) {
		return nil, true, errors.New("Mancode design policy contains an unsupported value")
	}
	if operationID := object["lastOperationId"]; operationID != nil {
		text, valid := operationID.(string)
		if !valid || !mancodeOperationIDPattern.MatchString(text) {
			return nil, true, errors.New("Mancode design policy operation reference is invalid")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return nil, true, errors.New("Mancode design policy update time is invalid")
	}
	payload, err := json.Marshal(mancodeDesignPolicyImport{
		SchemaVersion: mancodeDesignPolicyImportSchema,
		Authority:     "local_pending_review", HumanReviewRequired: true, ExecutionAuthorized: false,
		Revision: revision, SourceEnabled: enabled, Preset: preset, IconPolicy: iconPolicy,
		EmojiPolicy: emojiPolicy, MotionPolicy: motionPolicy,
		BrowserValidation: browserValidation, UpdatedAt: updatedAt,
	})
	if err != nil {
		return nil, true, fmt.Errorf("encode Mancode design policy projection: %w", err)
	}
	return []normalizedItem{{Type: "project_policy", Payload: payload}}, true, nil
}
