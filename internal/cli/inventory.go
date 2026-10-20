package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	runtimeinventory "github.com/threadforge-ai/threadforge/internal/inventory"
)

func runInventory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "scan" {
		_, _ = fmt.Fprintln(stderr, "usage: threadforge inventory scan [--home <dir>] [--project-root <dir>] [--runtime <name>] [--include-system]")
		return 2
	}
	fs := flag.NewFlagSet("inventory scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	homeArg := fs.String("home", "", "home directory to inspect (defaults to the current user)")
	projectArg := fs.String("project-root", "", "project directory to inspect (defaults to the current directory)")
	includeSystem := fs.Bool("include-system", false, "include system- and managed-scope configuration")
	var runtimeArgs repeatedStringFlag
	fs.Var(&runtimeArgs, "runtime", "limit scan to runtime name(s), repeat or comma-separate")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "inventory scan does not accept positional arguments")
		return 2
	}

	home := strings.TrimSpace(*homeArg)
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "resolve current user home:", err)
			return 1
		}
	}
	projectRoot := strings.TrimSpace(*projectArg)
	if projectRoot == "" {
		var err error
		projectRoot, err = os.Getwd()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "resolve current project directory:", err)
			return 1
		}
	}
	var err error
	home, err = runtimeinventory.ValidateScanRoot(home)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid home scan root:", err)
		return 1
	}
	projectRoot, err = runtimeinventory.ValidateScanRoot(projectRoot)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "invalid project scan root:", err)
		return 1
	}

	runtimes := make([]string, 0, len(runtimeArgs))
	for _, value := range runtimeArgs {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				_, _ = fmt.Fprintln(stderr, "runtime names must be non-empty")
				return 2
			}
			runtimes = append(runtimes, name)
		}
	}

	result := runtimeinventory.Scan(runtimeinventory.Options{
		HomeDir:       home,
		WorkingDir:    projectRoot,
		Runtimes:      runtimes,
		IncludeSystem: *includeSystem,
	})
	return writeIndentedJSON(stdout, result, stderr)
}
