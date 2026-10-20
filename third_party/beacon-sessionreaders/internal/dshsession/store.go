package dshsession

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asymptote-labs/agent-beacon/cli/beacon/internal/sessionlimits"
	"github.com/klauspost/compress/zstd"
)

type Store struct {
	DSHHome     string
	SessionsDir string
}

func DefaultDSHHome() (string, error) {
	if value := strings.TrimSpace(os.Getenv("DSH_HOME")); value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	return filepath.Join(home, ".dsh"), nil
}

func NewStore(dshHome string) (*Store, error) {
	root := strings.TrimSpace(dshHome)
	if root == "" {
		def, err := DefaultDSHHome()
		if err != nil {
			return nil, err
		}
		root = def
	}
	return &Store{DSHHome: root, SessionsDir: filepath.Join(root, "sessions")}, nil
}

func (s *Store) Exists() bool {
	info, err := os.Stat(s.SessionsDir)
	return err == nil && info.IsDir()
}

func (s *Store) List() ([]SessionRef, error) {
	budget := sessionlimits.NewBudget()
	type candidate struct {
		dir        string
		path       string
		compressed bool
	}
	byDir := map[string]candidate{}
	err := budget.WalkDir(s.SessionsDir, sessionlimits.MaxDirectoryDepth, func(path string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}
		compressed, ok := classifySessionFile(d.Name())
		if !ok {
			return nil
		}
		dir := filepath.Dir(path)
		prior, exists := byDir[dir]
		// One directory can hold both spellings; the compressed frame is the one DSH
		// writes, and it wins whichever order the walk visits them in.
		if !exists || compressed || !prior.compressed {
			byDir[dir] = candidate{dir: dir, path: path, compressed: compressed}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read DeepSeek sessions directory: %w", err)
	}
	refs := make([]SessionRef, 0, len(byDir))
	for _, item := range byDir {
		info, err := os.Stat(item.path)
		if err != nil || info.IsDir() {
			continue
		}
		ref := SessionRef{
			ID:            filepath.Base(item.dir),
			Path:          item.path,
			Dir:           item.dir,
			DSHHome:       s.DSHHome,
			ModTimeUnixMS: info.ModTime().UnixMilli(),
			SizeBytes:     info.Size(),
			Compressed:    item.compressed,
		}
		if meta := s.readMeta(ref, budget); meta != nil {
			ref.Meta = meta
			if strings.TrimSpace(meta.ID) != "" {
				ref.ID = meta.ID
			}
		}
		refs = append(refs, ref)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].ModTimeUnixMS != refs[j].ModTimeUnixMS {
			return refs[i].ModTimeUnixMS < refs[j].ModTimeUnixMS
		}
		return refs[i].Path < refs[j].Path
	})
	return refs, budget.Warning()
}

func (s *Store) Read(ref SessionRef) ([]Record, Stats, error) {
	var reader io.Reader
	var stats Stats
	budget := sessionlimits.NewBudget()
	data, err := budget.ReadFileLimit(ref.Path, sessionlimits.MaxNativeSessionReadBytes)
	if err != nil {
		return nil, stats, err
	}
	if data == nil {
		return nil, stats, sessionlimits.ErrScanLimit
	}
	if ref.Compressed {
		plain, partial, err := decodeZstdFramesBounded(data, sessionlimits.MaxNativeSessionExpandedBytes)
		if err != nil {
			return nil, stats, err
		}
		stats.PartialFrame = partial
		stats.PartialTail = partial
		reader = bytes.NewReader(plain)
	} else {
		reader = bytes.NewReader(data)
	}
	records, lineStats, err := decodeRecords(reader)
	lineStats.PartialFrame = stats.PartialFrame
	if lineStats.PartialFrame {
		lineStats.PartialTail = true
	}
	return records, lineStats, err
}

func (s *Store) readMeta(ref SessionRef, budget *sessionlimits.Budget) *SessionMeta {
	var data []byte
	if ref.Compressed {
		compressed, err := budget.ReadFileLimit(ref.Path, sessionlimits.MaxMetadataFileBytes)
		if err != nil || compressed == nil {
			return nil
		}
		decoded, err := decodeZstdMetadataPrefix(compressed, sessionlimits.MaxMetadataFileBytes)
		if err != nil {
			budget.MarkTruncated()
			return nil
		}
		if !budget.ChargeBytes(int64(len(decoded))) {
			return nil
		}
		data = decoded
	} else {
		lines, err := budget.ReadLines(ref.Path, 128, sessionlimits.MaxMetadataFileBytes)
		if err != nil {
			return nil
		}
		data = bytes.Join(lines, []byte("\n"))
	}
	records, _, err := decodeRecords(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	meta := &SessionMeta{}
	for _, record := range records {
		switch record.Kind {
		case "session":
			meta.ID = firstString(record.Data, "id", "sessionId", "session_id")
			meta.CWD = firstString(record.Data, "cwd", "workingDirectory", "working_directory")
			meta.ParentSessionID = firstString(record.Data, "parentSession", "parent_session", "parentSessionId")
			meta.Origin = firstString(record.Data, "origin")
		case "session/title":
			meta.Title = firstString(record.Data, "title")
		case "user/message":
			if meta.FirstPrompt == "" {
				meta.FirstPrompt = strings.TrimSpace(userMessageText(record.Data))
			}
		case "request/header":
			meta.Model = requestHeaderModel(record.Data)
		}
		if meta.ID != "" && meta.CWD != "" && meta.Title != "" && meta.Model != "" {
			break
		}
	}
	if meta.ID == "" && meta.CWD == "" && meta.ParentSessionID == "" && meta.Origin == "" && meta.Title == "" && meta.FirstPrompt == "" && meta.Model == "" {
		return nil
	}
	return meta
}

// decodeZstdMetadataPrefix extracts complete concatenated frames while bounding both the
// decoder window and expanded metadata. A partial final frame is expected for an actively
// written DSH session, so metadata from earlier complete frames remains usable.
func decodeZstdMetadataPrefix(compressed []byte, maxExpanded int64) ([]byte, error) {
	decoded, _, err := decodeZstdFramesBounded(compressed, maxExpanded)
	return decoded, err
}

func decodeZstdFramesBounded(compressed []byte, maxExpanded int64) ([]byte, bool, error) {
	if maxExpanded < 1 {
		return nil, false, errors.New("metadata expansion limit must be positive")
	}
	var expanded bytes.Buffer
	partial := false
	for offset := 0; offset < len(compressed); {
		next := bytes.Index(compressed[offset:], zstdMagic)
		if next < 0 {
			partial = strings.TrimSpace(string(compressed[offset:])) != ""
			break
		}
		start := offset + next
		follow := bytes.Index(compressed[start+len(zstdMagic):], zstdMagic)
		end := len(compressed)
		if follow >= 0 {
			end = start + len(zstdMagic) + follow
		}
		remaining := maxExpanded - int64(expanded.Len())
		if remaining <= 0 {
			return nil, partial, sessionlimits.ErrScanLimit
		}
		windowLimit := max(maxExpanded, int64(sessionlimits.MaxMetadataFileBytes))
		decoder, err := zstd.NewReader(bytes.NewReader(compressed[start:end]), zstd.WithDecoderMaxMemory(uint64(windowLimit)))
		if err != nil {
			if follow < 0 {
				partial = true
				break
			}
			return nil, partial, err
		}
		frame, readErr := io.ReadAll(io.LimitReader(decoder, remaining+1))
		decoder.Close()
		if int64(len(frame)) > remaining {
			return nil, partial, sessionlimits.ErrScanLimit
		}
		if readErr != nil {
			if follow < 0 {
				partial = true
				break
			}
			return nil, partial, readErr
		}
		_, _ = expanded.Write(frame)
		if follow < 0 {
			break
		}
		offset = end
	}
	return expanded.Bytes(), partial, nil
}

func decodeRecords(r io.Reader) ([]Record, Stats, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var records []Record
	var stats Stats
	lineNo := 0
	for {
		line, partial, oversized, err := readLine(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if partial {
					stats.PartialTail = true
				}
				break
			}
			return records, stats, err
		}
		lineNo++
		stats.Lines = lineNo
		if oversized {
			stats.Malformed++
			if stats.FirstError == nil {
				stats.FirstError = fmt.Errorf("dsh session line exceeds %d bytes", MaxLineBytes)
			}
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		record, err := decodeRecord(line, lineNo)
		if err != nil {
			stats.Malformed++
			if stats.FirstError == nil {
				stats.FirstError = err
			}
			continue
		}
		stats.Decoded++
		records = append(records, *record)
	}
	return records, stats, nil
}

func readLine(br *bufio.Reader) (line []byte, partial, oversized bool, err error) {
	var buf []byte
	for {
		chunk, readErr := br.ReadSlice('\n')
		if oversized || len(buf)+len(chunk) > MaxLineBytes {
			oversized = true
			buf = nil
		} else {
			buf = append(buf, chunk...)
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if oversized {
				return nil, false, true, nil
			}
			if len(buf) > 0 {
				return buf, true, false, io.EOF
			}
			return nil, false, false, io.EOF
		}
		if readErr != nil {
			return nil, false, false, readErr
		}
		break
	}
	if oversized {
		return nil, false, true, nil
	}
	return bytes.TrimSuffix(bytes.TrimSuffix(buf, []byte("\n")), []byte("\r")), false, false, nil
}

var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

func decodeZstdFrames(data []byte) ([]byte, bool, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, false, err
	}
	defer decoder.Close()
	var out bytes.Buffer
	partial := false
	offset := 0
	for offset < len(data) {
		next := bytes.Index(data[offset:], zstdMagic)
		if next < 0 {
			partial = strings.TrimSpace(string(data[offset:])) != ""
			break
		}
		start := offset + next
		follow := bytes.Index(data[start+len(zstdMagic):], zstdMagic)
		end := len(data)
		if follow >= 0 {
			end = start + len(zstdMagic) + follow
		}
		decoded, err := decoder.DecodeAll(data[start:end], nil)
		if err != nil {
			if follow < 0 {
				partial = true
				break
			}
			return nil, partial, fmt.Errorf("decode DeepSeek zstd frame: %w", err)
		}
		out.Write(decoded)
		offset = end
	}
	return out.Bytes(), partial, nil
}
