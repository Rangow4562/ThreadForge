package appdata

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/threadforge-ai/threadforge/pkg/teamauth"
)

var ErrTeamTrustPolicyNotInstalled = errors.New("team trust policy is not installed")

type Paths struct {
	Root   string
	SQLite string
	Traces string
}

func TeamTrustPolicyPath(root string) string {
	return filepath.Join(root, "team-trust", "trusted-reviewers-v1.json")
}

func TeamTrustPolicyDigestPath(root string) string {
	return TeamTrustPolicyPath(root) + ".sha256"
}

func ReadTeamTrustPolicy(root string) (teamauth.Policy, []byte, string, error) {
	policy, data, err := teamauth.ReadPolicy(TeamTrustPolicyPath(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, pinErr := readPinnedPolicyDigest(TeamTrustPolicyDigestPath(root)); errors.Is(pinErr, os.ErrNotExist) {
				return teamauth.Policy{}, nil, "", ErrTeamTrustPolicyNotInstalled
			} else if pinErr != nil {
				return teamauth.Policy{}, nil, "", fmt.Errorf("installed team trust pin exists but its policy is unavailable: %w", pinErr)
			}
			return teamauth.Policy{}, nil, "", errors.New("installed team trust pin exists but its policy is unavailable")
		}
		return teamauth.Policy{}, nil, "", err
	}
	pinnedDigest, err := readPinnedPolicyDigest(TeamTrustPolicyDigestPath(root))
	if err != nil {
		return teamauth.Policy{}, nil, "", err
	}
	digest := sha256.Sum256(data)
	actualDigest := hex.EncodeToString(digest[:])
	if pinnedDigest != actualDigest {
		return teamauth.Policy{}, nil, "", errors.New("installed team trust policy does not match its pinned digest")
	}
	return policy, data, actualDigest, nil
}

func readPinnedPolicyDigest(filename string) (string, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 65 ||
		(runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return "", errors.New("installed team trust digest must be a private 64-hex regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return "", errors.New("installed team trust digest changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 66))
	finalInfo, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) != 65 || finalInfo.Size() != 65 ||
		!finalInfo.ModTime().Equal(openedInfo.ModTime()) || data[64] != '\n' {
		return "", errors.New("installed team trust digest is malformed")
	}
	digest := string(data[:64])
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || strings.ToLower(digest) != digest {
		return "", errors.New("installed team trust digest must be lowercase SHA-256")
	}
	return digest, nil
}

func Resolve(databaseOverride, traceOverride string) (Paths, error) {
	root := strings.TrimSpace(os.Getenv("THREADFORGE_HOME"))
	if root == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(base, "threadforge")
	}
	if strings.TrimSpace(databaseOverride) == "" {
		databaseOverride = strings.TrimSpace(os.Getenv("THREADFORGE_DB"))
	}
	if strings.TrimSpace(traceOverride) == "" {
		traceOverride = strings.TrimSpace(os.Getenv("THREADFORGE_TRACE_FILE"))
	}
	paths := Paths{Root: filepath.Clean(root)}
	paths.SQLite = databaseOverride
	if paths.SQLite == "" {
		paths.SQLite = filepath.Join(paths.Root, "threadforge.sqlite")
	}
	paths.Traces = traceOverride
	if paths.Traces == "" {
		paths.Traces = filepath.Join(paths.Root, "traces", "events.jsonl")
	}
	if paths.SQLite == "" || paths.Traces == "" {
		return Paths{}, errors.New("Threadforge database and trace paths must not be empty")
	}
	return paths, nil
}
