package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/memoryfeedback"
)

// RecordMemoryRetrievalReceipt appends a local-only record that binds one
// memory response to an existing task in the same project. The observation
// boundary is schema-versioned and never proves client delivery or exposure.
func (s *Store) RecordMemoryRetrievalReceipt(ctx context.Context, receipt memoryfeedback.RetrievalReceipt) (int64, error) {
	sequences, err := s.RecordMemoryRetrievalReceipts(ctx, []memoryfeedback.RetrievalReceipt{receipt})
	if err != nil {
		return 0, err
	}
	return sequences[0], nil
}

// RecordMemoryRetrievalReceipts atomically appends a bounded group of receipts
// from one task-scoped retrieval result.
func (s *Store) RecordMemoryRetrievalReceipts(ctx context.Context, receipts []memoryfeedback.RetrievalReceipt) ([]int64, error) {
	if len(receipts) == 0 || len(receipts) > memoryfeedback.MaxRetrievalReceiptBatch {
		return nil, fmt.Errorf("memory retrieval receipt batch must contain 1-%d records", memoryfeedback.MaxRetrievalReceiptBatch)
	}
	first := receipts[0]
	payloads := make([]string, len(receipts))
	seenIDs := make(map[string]bool, len(receipts))
	for i, receipt := range receipts {
		if err := memoryfeedback.ValidateRetrievalReceipt(receipt); err != nil {
			return nil, fmt.Errorf("validate memory retrieval receipt %d: %w", i, err)
		}
		if receipt.ProjectID != first.ProjectID || receipt.TaskID != first.TaskID || receipt.SessionID != first.SessionID {
			return nil, errors.New("memory retrieval receipt batch must share one project, task, and session scope")
		}
		if seenIDs[receipt.ID] {
			return nil, errors.New("memory retrieval receipt batch contains a duplicate ID")
		}
		seenIDs[receipt.ID] = true
		payload, err := json.Marshal(receipt)
		if err != nil {
			return nil, err
		}
		payloads[i] = string(payload)
	}
	sequences := make([]int64, 0, len(receipts))
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		task, err := readTask(ctx, tx, first.TaskID)
		if err != nil {
			return err
		}
		if task.ProjectID != first.ProjectID {
			return errors.New("memory retrieval receipt project_id does not match its task")
		}
		if first.SessionID != "" {
			session, err := readSession(ctx, tx, first.SessionID)
			if err != nil {
				return err
			}
			if session.TaskID != first.TaskID || session.ProjectID != first.ProjectID {
				return errors.New("memory retrieval receipt session does not belong to its task and project")
			}
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_retrieval_receipts WHERE project_id = ?`, first.ProjectID).Scan(&count); err != nil {
			return err
		}
		if count+len(receipts) > memoryfeedback.MaxProjectRecords {
			return fmt.Errorf("project memory retrieval receipt limit reached (%d)", memoryfeedback.MaxProjectRecords)
		}
		for i, receipt := range receipts {
			result, err := tx.ExecContext(ctx,
				`INSERT INTO memory_retrieval_receipts(id, project_id, task_id, target_id, target_revision, created_at, receipt_json)
				 VALUES(?, ?, ?, ?, ?, ?, ?)`, receipt.ID, receipt.ProjectID, receipt.TaskID,
				receipt.TargetID, receipt.TargetRevision, receipt.CreatedAt, payloads[i])
			if err != nil {
				return fmt.Errorf("append memory retrieval receipt: %w", err)
			}
			sequence, err := result.LastInsertId()
			if err != nil {
				return err
			}
			sequences = append(sequences, sequence)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sequences, nil
}

func validateMemoryFeedbackReceipt(ctx context.Context, tx *sql.Tx, feedback memoryfeedback.Feedback) error {
	if feedback.RetrievalReceiptID == "" {
		return nil
	}
	var payload string
	if err := tx.QueryRowContext(ctx,
		`SELECT receipt_json FROM memory_retrieval_receipts WHERE id = ? AND project_id = ? AND task_id = ?`,
		feedback.RetrievalReceiptID, feedback.ProjectID, feedback.TaskID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("memory retrieval receipt was not found in the same project and task")
		}
		return err
	}
	var receipt memoryfeedback.RetrievalReceipt
	if err := decodeStrictJSON(payload, &receipt); err != nil {
		return fmt.Errorf("decode memory retrieval receipt: %w", err)
	}
	if err := memoryfeedback.ValidateRetrievalReceipt(receipt); err != nil {
		return fmt.Errorf("validate memory retrieval receipt: %w", err)
	}
	if receipt.ID != feedback.RetrievalReceiptID || receipt.ProjectID != feedback.ProjectID || receipt.TaskID != feedback.TaskID ||
		receipt.SessionID != feedback.SessionID || receipt.TargetKind != feedback.TargetKind || receipt.TargetID != feedback.TargetID ||
		receipt.TargetRevision != feedback.TargetRevision || !receipt.GuidanceIncluded {
		return errors.New("memory retrieval receipt does not match the reported task, session, and approved memory revision")
	}
	return nil
}

// RecordMemoryFeedback appends a local-only, task-scoped report. It validates
// task and optional session ownership in the same transaction as the insert.
func (s *Store) RecordMemoryFeedback(ctx context.Context, feedback memoryfeedback.Feedback) (int64, error) {
	if err := memoryfeedback.Validate(feedback); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(feedback)
	if err != nil {
		return 0, err
	}
	var sequence int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		task, err := readTask(ctx, tx, feedback.TaskID)
		if err != nil {
			return err
		}
		if task.ProjectID != feedback.ProjectID {
			return errors.New("memory feedback project_id does not match its task")
		}
		if feedback.SessionID != "" {
			session, err := readSession(ctx, tx, feedback.SessionID)
			if err != nil {
				return err
			}
			if session.TaskID != feedback.TaskID || session.ProjectID != feedback.ProjectID {
				return errors.New("memory feedback session does not belong to its task and project")
			}
		}
		if err := validateMemoryFeedbackReceipt(ctx, tx, feedback); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_feedback_events WHERE project_id = ?`, feedback.ProjectID).Scan(&count); err != nil {
			return err
		}
		if count >= memoryfeedback.MaxProjectRecords {
			return fmt.Errorf("project memory feedback limit reached (%d)", memoryfeedback.MaxProjectRecords)
		}
		result, err := tx.ExecContext(ctx,
			`INSERT INTO memory_feedback_events(id, project_id, task_id, target_kind, target_id, target_revision, harness, created_at, feedback_json)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			feedback.ID, feedback.ProjectID, feedback.TaskID, feedback.TargetKind, feedback.TargetID,
			feedback.TargetRevision, feedback.Harness, feedback.CreatedAt, string(payload))
		if err != nil {
			return fmt.Errorf("append memory feedback: %w", err)
		}
		sequence, err = result.LastInsertId()
		return err
	})
	return sequence, err
}

// ListMemoryFeedback returns only reports in the required project scope, newest
// first. It cross-checks the indexed columns against the validated payload.
func (s *Store) ListMemoryFeedback(ctx context.Context, filter memoryfeedback.Filter) ([]memoryfeedback.Feedback, int, error) {
	if err := memoryfeedback.ValidateFilter(filter); err != nil {
		return nil, 0, err
	}
	conditions := []string{"project_id = ?"}
	args := []any{filter.ProjectID}
	if filter.TaskID != "" {
		conditions = append(conditions, "task_id = ?")
		args = append(args, filter.TaskID)
	}
	if filter.TargetKind != "" {
		conditions = append(conditions, "target_kind = ?")
		args = append(args, filter.TargetKind)
	}
	if filter.TargetID != "" {
		conditions = append(conditions, "target_id = ?")
		args = append(args, filter.TargetID)
	}
	if filter.Harness != "" {
		conditions = append(conditions, "harness = ?")
		args = append(args, filter.Harness)
	}
	where := strings.Join(conditions, " AND ")
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_feedback_events WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any(nil), args...), filter.Limit, filter.Offset)
	rows, err := tx.QueryContext(ctx,
		`SELECT sequence, id, project_id, task_id, target_kind, target_id, target_revision, harness, created_at, feedback_json
		 FROM memory_feedback_events WHERE `+where+` ORDER BY sequence DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]memoryfeedback.Feedback, 0, filter.Limit)
	for rows.Next() {
		var sequence int64
		var id, projectID, taskID, targetKind, targetID, harness, createdAt, payload string
		var revision int
		if err := rows.Scan(&sequence, &id, &projectID, &taskID, &targetKind, &targetID, &revision, &harness, &createdAt, &payload); err != nil {
			return nil, 0, err
		}
		var feedback memoryfeedback.Feedback
		if err := decodeStrictJSON(payload, &feedback); err != nil {
			return nil, 0, fmt.Errorf("decode memory feedback sequence %d: %w", sequence, err)
		}
		if err := memoryfeedback.Validate(feedback); err != nil || feedback.ID != id || feedback.ProjectID != projectID ||
			feedback.TaskID != taskID || string(feedback.TargetKind) != targetKind || feedback.TargetID != targetID ||
			feedback.TargetRevision != revision || feedback.Harness != harness || feedback.CreatedAt != createdAt {
			return nil, 0, errors.New("stored memory feedback index does not match its validated payload")
		}
		if err := validateMemoryFeedbackReceipt(ctx, tx, feedback); err != nil {
			return nil, 0, fmt.Errorf("validate linked memory retrieval receipt: %w", err)
		}
		items = append(items, feedback)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListMemoryRetrievalReceipts returns content-free receipts for one exact
// project/task scope, newest first, and validates indexed columns against each
// versioned receipt payload before returning it.
func (s *Store) ListMemoryRetrievalReceipts(ctx context.Context, filter memoryfeedback.RetrievalReceiptFilter) ([]memoryfeedback.RetrievalReceipt, int, error) {
	if err := memoryfeedback.ValidateRetrievalReceiptFilter(filter); err != nil {
		return nil, 0, err
	}
	where := "project_id = ? AND task_id = ?"
	args := []any{filter.ProjectID, filter.TaskID}
	if filter.TargetID != "" {
		where += " AND target_id = ?"
		args = append(args, filter.TargetID)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_retrieval_receipts WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any(nil), args...), filter.Limit, filter.Offset)
	rows, err := tx.QueryContext(ctx,
		`SELECT sequence, id, project_id, task_id, target_id, target_revision, created_at, receipt_json
		 FROM memory_retrieval_receipts WHERE `+where+` ORDER BY sequence DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]memoryfeedback.RetrievalReceipt, 0, filter.Limit)
	for rows.Next() {
		var sequence int64
		var id, projectID, taskID, targetID, createdAt, payload string
		var revision int
		if err := rows.Scan(&sequence, &id, &projectID, &taskID, &targetID, &revision, &createdAt, &payload); err != nil {
			return nil, 0, err
		}
		var receipt memoryfeedback.RetrievalReceipt
		if err := decodeStrictJSON(payload, &receipt); err != nil {
			return nil, 0, fmt.Errorf("decode memory retrieval receipt sequence %d: %w", sequence, err)
		}
		if err := memoryfeedback.ValidateRetrievalReceipt(receipt); err != nil || receipt.ID != id || receipt.ProjectID != projectID ||
			receipt.TaskID != taskID || receipt.TargetID != targetID || receipt.TargetRevision != revision || receipt.CreatedAt != createdAt {
			return nil, 0, errors.New("stored memory retrieval receipt index does not match its validated payload")
		}
		items = append(items, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// SummarizeMemoryFeedback produces counts only. It reads one consistent SQLite
// snapshot and explicitly marks the result truncated when the bounded sample
// does not include every project report.
func (s *Store) SummarizeMemoryFeedback(ctx context.Context, projectID string, maxRecords int) (memoryfeedback.SummaryResult, error) {
	result := memoryfeedback.SummaryResult{
		SchemaVersion: memoryfeedback.SummaryResultVersion,
		ProjectID:     projectID,
		Status:        "complete",
		Groups:        []memoryfeedback.SummaryGroup{},
		EvidenceClass: memoryfeedback.EvidenceSelfReported,
	}
	if strings.TrimSpace(projectID) == "" || len(projectID) > 256 || maxRecords < 1 || maxRecords > memoryfeedback.MaxSummaryRecords {
		return memoryfeedback.SummaryResult{}, errors.New("project_id is required and max_records must be 1-10000")
	}
	if err := memoryfeedback.ValidateFilter(memoryfeedback.Filter{ProjectID: projectID, Limit: 1}); err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_feedback_events WHERE project_id = ?`, projectID).Scan(&total); err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	queryLimit := maxRecords + 1
	rows, err := tx.QueryContext(ctx,
		`SELECT sequence, id, project_id, task_id, target_kind, target_id, target_revision, harness, created_at, feedback_json
		 FROM memory_feedback_events WHERE project_id = ? ORDER BY sequence DESC LIMIT ?`, projectID, queryLimit)
	if err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	type groupKey struct {
		kind, id, harness, exposure, utility, taskResult string
		revision                                         int
	}
	groups := make(map[groupKey]int)
	distinctTasks := make(map[groupKey]map[string]struct{})
	var scanned int
	var receiptLinked int
	truncated := false
	for rows.Next() {
		var sequence int64
		var id, indexedProject, taskID, targetKind, targetID, harness, createdAt, payload string
		var revision int
		if err := rows.Scan(&sequence, &id, &indexedProject, &taskID, &targetKind, &targetID, &revision, &harness, &createdAt, &payload); err != nil {
			_ = rows.Close()
			return memoryfeedback.SummaryResult{}, err
		}
		if scanned == maxRecords {
			truncated = true
			break
		}
		var feedback memoryfeedback.Feedback
		if err := decodeStrictJSON(payload, &feedback); err != nil {
			_ = rows.Close()
			return memoryfeedback.SummaryResult{}, fmt.Errorf("decode memory feedback sequence %d: %w", sequence, err)
		}
		if err := memoryfeedback.Validate(feedback); err != nil || feedback.ID != id || feedback.ProjectID != indexedProject ||
			feedback.TaskID != taskID || string(feedback.TargetKind) != targetKind || feedback.TargetID != targetID ||
			feedback.TargetRevision != revision || feedback.Harness != harness || feedback.CreatedAt != createdAt {
			_ = rows.Close()
			return memoryfeedback.SummaryResult{}, errors.New("stored memory feedback index does not match its validated payload")
		}
		if err := validateMemoryFeedbackReceipt(ctx, tx, feedback); err != nil {
			_ = rows.Close()
			return memoryfeedback.SummaryResult{}, fmt.Errorf("validate linked memory retrieval receipt: %w", err)
		}
		key := groupKey{kind: string(feedback.TargetKind), id: feedback.TargetID, revision: feedback.TargetRevision,
			harness: feedback.Harness, exposure: string(feedback.Exposure), utility: string(feedback.Utility),
			taskResult: feedback.TaskResult}
		groups[key]++
		if distinctTasks[key] == nil {
			distinctTasks[key] = make(map[string]struct{})
		}
		distinctTasks[key][feedback.TaskID] = struct{}{}
		if feedback.RetrievalReceiptID != "" {
			receiptLinked++
		}
		scanned++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return memoryfeedback.SummaryResult{}, err
	}
	if err := rows.Close(); err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	if total > maxRecords {
		truncated = true
	}
	if err := tx.Commit(); err != nil {
		return memoryfeedback.SummaryResult{}, err
	}
	result.Records = scanned
	result.RetrievalReceiptLinked = receiptLinked
	if truncated {
		result.Status = "truncated"
	}
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.id != b.id {
			return a.id < b.id
		}
		if a.revision != b.revision {
			return a.revision < b.revision
		}
		if a.harness != b.harness {
			return a.harness < b.harness
		}
		if a.exposure != b.exposure {
			return a.exposure < b.exposure
		}
		if a.utility != b.utility {
			return a.utility < b.utility
		}
		return a.taskResult < b.taskResult
	})
	for _, key := range keys {
		result.Groups = append(result.Groups, memoryfeedback.SummaryGroup{
			TargetKind: memoryfeedback.TargetKind(key.kind), TargetID: key.id, TargetRevision: key.revision,
			Harness: key.harness, Exposure: memoryfeedback.Exposure(key.exposure),
			Utility: memoryfeedback.Utility(key.utility), TaskResult: key.taskResult, Count: groups[key],
			DistinctTaskCount: len(distinctTasks[key]),
		})
	}
	return result, nil
}
