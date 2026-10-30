package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/migrate"
)

type repeatedMigrationManifest []string

func (values *repeatedMigrationManifest) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedMigrationManifest) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("manifest path cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

// listMancodeMigrationSources lists local paths solely to help an operator
// create per-root snapshots. This response is not a shareable evidence artifact.
func listMancodeMigrationSources(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate sources", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "Mancode project root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*root) == "" {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge migrate sources --root <mancode-project-root>")
		return 2
	}
	roots, err := migrate.DiscoverMancodeSourceRoots(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, map[string]any{
		"schema_version":            "threadforge.mancode-source-root-list.v1",
		"source_set_report_created": false,
		"sources":                   roots,
	}, stderr)
}

// mancodeMigrationSourceSet emits a path-free source-set inventory only when
// one verified local snapshot is supplied for the project and each discovered
// coordination root.
func mancodeMigrationSourceSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate source-set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "Mancode project root used to discover the complete source set")
	var manifests repeatedMigrationManifest
	fs.Var(&manifests, "manifest", "verified local Mancode component snapshot; repeat once for every discovered source root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || strings.TrimSpace(*root) == "" || len(manifests) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge migrate source-set --root <mancode-project-root> --manifest <snapshot\\manifest.json> [--manifest <coordination-snapshot\\manifest.json> ...]")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	verifiedManifests := make([]string, 0, len(manifests))
	for _, name := range manifests {
		manifestPath, err := validateLocalSnapshotManifest(name, paths.Root)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		verifiedManifests = append(verifiedManifests, manifestPath)
	}
	report, err := migrate.BuildMancodeSourceSet(*root, verifiedManifests)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, report, stderr)
}
