package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/threadforge-ai/threadforge/pkg/privacy"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

func (s *Store) Supersede(ctx context.Context, id string, expectedRevision int, replacementID, actor, reason string, at time.Time) (proceduregraph.Graph, error) {
	if strings.TrimSpace(actor) == "" || len(actor) > 128 || strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return proceduregraph.Graph{}, errors.New("superseding actor and reason are required and bounded")
	}
	if privacy.ContainsCredentialText(actor) || privacy.ContainsCredentialText(reason) {
		return proceduregraph.Graph{}, errors.New("superseding audit metadata contains credential material")
	}
	var updated proceduregraph.Graph
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		graph, err := readGraphTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if graph.Revision != expectedRevision {
			return ErrConflict
		}
		replacement, err := readGraphTx(ctx, tx, replacementID)
		if err != nil {
			return err
		}
		if replacement.Status != proceduregraph.StatusApproved || replacement.ProjectID != graph.ProjectID {
			return errors.New("replacement must be an approved procedure in the same project")
		}
		superseded, err := proceduregraph.Supersede(graph, replacementID)
		if err != nil {
			return err
		}
		stamp := at.UTC().Format(time.RFC3339Nano)
		if err := updateGraphTx(ctx, tx, superseded, expectedRevision, stamp); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, superseded.ID, superseded.Revision, "superseded", actor, reason, stamp); err != nil {
			return err
		}
		updated = superseded
		return nil
	})
	return updated, err
}
