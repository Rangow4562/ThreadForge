//go:build windows

package ci

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureDetachedProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200 | 0x08000000,
	}
}

func replaceSessionStateFile(source, destination string) error {
	return replaceSessionStateFileWith(source, destination, os.Rename, time.Sleep)
}

func replaceSessionStateFileWith(source, destination string, rename func(string, string) error, sleep func(time.Duration)) error {
	const attempts = 20
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		err = rename(source, destination)
		if err == nil || !isTransientSessionStateReplaceError(err) || attempt == attempts-1 {
			return err
		}
		sleep(25 * time.Millisecond)
	}
	return err
}

func isTransientSessionStateReplaceError(err error) bool {
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) {
		return false
	}
	var code syscall.Errno
	if !errors.As(linkErr.Err, &code) {
		return false
	}
	switch code {
	case syscall.Errno(5), syscall.Errno(32), syscall.Errno(33):
		return true
	default:
		return false
	}
}
