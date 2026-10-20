package security

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	beacon "github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve"
	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/rulestore"
	"github.com/asymptote-labs/agent-beacon/pkg/asymptoteobserve/threatrules"
	"github.com/threadforge-ai/threadforge/pkg/privacy"
)

const maxManagedRuleSourceBytes = 64 << 20

// ListManagedRules returns the effective local rule set without exposing file paths.
func ListManagedRules(storeDir string) ([]RuleSummary, error) {
	if err := validateManagedRuleStore(storeDir, false); err != nil {
		return nil, err
	}
	if rulestore.HasRuleFiles(storeDir) {
		if err := validateRuleDirectory(storeDir); err != nil {
			return nil, fmt.Errorf("validate managed threat-rule directory: %w", err)
		}
	}
	loaded, err := rulestore.LoadActive(storeDir, "")
	if err != nil {
		return nil, err
	}
	if len(loaded) == 0 || len(loaded) > MaxRules {
		return nil, fmt.Errorf("active rule set must contain 1 to %d rules", MaxRules)
	}
	rules := make([]RuleSummary, 0, len(loaded))
	for _, item := range loaded {
		rules = append(rules, RuleSummary{
			ID: cleanRuleText(item.Rule.ID, 128), Title: cleanRuleText(item.Rule.Title, 240),
			Severity: beacon.Severity(item.Rule.Severity), Posture: item.Rule.Posture, Source: item.Source,
		})
	}
	return rules, nil
}

// InstallManagedRules validates a bounded private source snapshot before the
// upstream Beacon rule-store installer performs its all-or-rollback installation.
func InstallManagedRules(storeDir, source string, force bool) ([]string, error) {
	if err := validateManagedRuleStore(storeDir, false); err != nil {
		return nil, err
	}
	staged, cleanup, count, err := stageRuleSource(source)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if count == 0 {
		return nil, errors.New("rule source contains no *.rule.yaml files")
	}
	if err := validateManagedRuleStore(storeDir, true); err != nil {
		return nil, err
	}
	installed, err := rulestore.InstallFiles(storeDir, staged, force)
	if err != nil {
		return nil, fmt.Errorf("install validated threat rules: %w", err)
	}
	ids := make([]string, 0, len(installed))
	for _, item := range installed {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

// RemoveManagedRule removes only a validated identifier from the local rule store.
func RemoveManagedRule(storeDir, id string) error {
	if err := validateManagedRuleStore(storeDir, false); err != nil {
		return err
	}
	if !threatrules.ValidID(id) {
		return errors.New("invalid threat rule identifier")
	}
	if _, err := rulestore.Remove(storeDir, id); err != nil {
		return fmt.Errorf("remove local threat rule: %w", err)
	}
	return nil
}

func validateManagedRuleStore(storeDir string, create bool) error {
	absolute, err := filepath.Abs(storeDir)
	if err != nil {
		return err
	}
	parent, err := os.Lstat(filepath.Dir(absolute))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed rule store parent must be an existing directory, not a symlink")
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(absolute, 0o700); err != nil {
			return fmt.Errorf("create private managed rule store: %w", err)
		}
		info, err = os.Lstat(absolute)
	}
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed rule store must be a regular directory, not a symlink")
	}
	return nil
}

func stageRuleSource(source string) (string, func(), int, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", func() {}, 0, err
	}
	rootInfo, err := os.Lstat(absolute)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || (!rootInfo.IsDir() && !rootInfo.Mode().IsRegular()) {
		return "", func() {}, 0, errors.New("rule source must be a regular file or directory, not a symlink")
	}
	if !rootInfo.IsDir() && !strings.HasSuffix(absolute, rulestore.RuleFileSuffix) {
		return "", func() {}, 0, fmt.Errorf("rule file must end with %s", rulestore.RuleFileSuffix)
	}
	staged, err := os.MkdirTemp("", "threadforge-rule-snapshot-")
	if err != nil {
		return "", func() {}, 0, err
	}
	cleanup := func() { _ = os.RemoveAll(staged) }
	count := 0
	var total int64
	copyRule := func(filename, relative string, fileInfo fs.FileInfo) error {
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			return errors.New("rule source contains a non-regular or symbolic-link file")
		}
		if fileInfo.Size() < 0 || fileInfo.Size() > MaxRuleBytes || total > maxManagedRuleSourceBytes-fileInfo.Size() {
			return errors.New("rule source exceeds the 1 MiB per-file or 64 MiB total limit")
		}
		file, err := os.Open(filename)
		if err != nil {
			return err
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !os.SameFile(fileInfo, openedInfo) {
			_ = file.Close()
			return errors.New("rule source changed while being staged")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, MaxRuleBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) > MaxRuleBytes || privacy.ContainsCredential(data) {
			return errors.New("rule source exceeds its size limit or contains credential material")
		}
		currentInfo, err := os.Lstat(filename)
		if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(fileInfo, currentInfo) ||
			currentInfo.Size() != int64(len(data)) || !currentInfo.ModTime().Equal(fileInfo.ModTime()) {
			return errors.New("rule source changed while being staged")
		}
		if !safeRuleRelative(relative) {
			return errors.New("rule source contains an unsafe relative path")
		}
		destination := filepath.Join(staged, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return err
		}
		count++
		total += int64(len(data))
		if count > MaxRules {
			return fmt.Errorf("rule source exceeds the %d-file limit", MaxRules)
		}
		return nil
	}
	if rootInfo.IsDir() {
		err = filepath.WalkDir(absolute, func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := os.Lstat(filename)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("rule source contains a symbolic link")
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), rulestore.RuleFileSuffix) ||
				strings.HasPrefix(entry.Name(), "._") {
				return nil
			}
			relative, err := filepath.Rel(absolute, filename)
			if err != nil {
				return err
			}
			return copyRule(filename, filepath.ToSlash(relative), info)
		})
	} else {
		err = copyRule(absolute, filepath.Base(absolute), rootInfo)
	}
	if err != nil {
		cleanup()
		return "", func() {}, 0, fmt.Errorf("stage threat rule source: %w", err)
	}
	return staged, cleanup, count, nil
}

func safeRuleRelative(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return strings.HasSuffix(value, rulestore.RuleFileSuffix)
}
