package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/threadforge-ai/threadforge/internal/appdata"
	"github.com/threadforge-ai/threadforge/pkg/migrationbackup"
)

func migrationSourceBackupVerification(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate backup-verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifestName := fs.String("manifest", "", "path to a verified local Threadforge migration snapshot manifest")
	backupRoot := fs.String("backup-root", "", "independent retained copy of the complete migration source tree")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *manifestName == "" || *backupRoot == "" {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge migrate backup-verify --manifest <snapshot\\manifest.json> --backup-root <independent-copy>")
		return 2
	}
	paths, err := appdata.Resolve("", "")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "local Threadforge data directory is unavailable")
		return 1
	}
	manifestPath, err := validateLocalSnapshotManifest(*manifestName, paths.Root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "migration snapshot is unavailable or outside local Threadforge data")
		return 1
	}
	report, err := migrationbackup.VerifySnapshot(manifestPath, *backupRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "source backup verification failed safely: %s\n", err)
		return 1
	}
	if code := writeIndentedJSON(stdout, report, stderr); code != 0 {
		return code
	}
	if report.Status != "verified" {
		return 1
	}
	return 0
}
