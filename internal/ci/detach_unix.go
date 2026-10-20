//go:build !windows

package ci

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func replaceSessionStateFile(source, destination string) error {
	return os.Rename(source, destination)
}
