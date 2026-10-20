package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/internal/procedurefiles"
	"github.com/threadforge-ai/threadforge/internal/teamtransport"
)

func runTeamSync(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge team sync <pull|push>")
		return 2
	}
	direction := args[0]
	if direction != "pull" && direction != "push" {
		_, _ = fmt.Fprintf(stderr, "unknown team sync command %q\n", direction)
		return 2
	}
	fs := flag.NewFlagSet("team sync "+direction, flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectRoot := fs.String("project-root", "", "project Git working-tree root")
	projectID := fs.String("project-id", "", "registered team project identifier")
	remote := fs.String("remote", "origin", "configured Git remote name")
	expectedRevision := fs.Int64("expected-revision", -1, "expected current remote revision (required for push; use 0 for first push)")
	database := fs.String("db", "", "SQLite path (defaults under the local Threadforge data directory)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*projectRoot) == "" || strings.TrimSpace(*projectID) == "" || fs.NArg() != 0 ||
		(direction == "push" && *expectedRevision < 0) || (direction == "pull" && *expectedRevision >= 0) {
		_, _ = fmt.Fprintf(stderr, "usage: threadforge team sync %s --project-root <root> --project-id <id> [--remote <name>]", direction)
		if direction == "push" {
			_, _ = fmt.Fprintln(stderr, " --expected-revision <n> [--db <path>]")
		} else {
			_, _ = fmt.Fprintln(stderr, " [--db <path>]")
		}
		return 2
	}
	databaseStore, closeStore, err := openContinuityStore(*database)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeStore()
	paths, err := appdata.Resolve(*database, "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	projectStore, err := procedurefiles.NewWithProjectScopeWriteGate(*projectRoot, databaseStore)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var result teamtransport.Result
	if direction == "pull" {
		result, err = teamtransport.Pull(context.Background(), *projectRoot, *remote, *projectID, projectStore, paths.Root)
	} else {
		result, err = teamtransport.Push(context.Background(), *projectRoot, *remote, *projectID, *expectedRevision, projectStore, paths.Root)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return writeIndentedJSON(stdout, result, stderr)
}
