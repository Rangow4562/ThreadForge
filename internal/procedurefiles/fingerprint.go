package procedurefiles

import (
	"github.com/threadforge-ai/threadforge/internal/sourcefingerprint"
	"github.com/threadforge-ai/threadforge/pkg/proceduregraph"
)

// FingerprintSources is retained as the procedure-files API while sharing the
// same bounded implementation with project-memory source evidence.
func FingerprintSources(projectRoot string, sourcePaths []string) ([]proceduregraph.PathFingerprint, error) {
	return sourcefingerprint.FingerprintSources(projectRoot, sourcePaths)
}
