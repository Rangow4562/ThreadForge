// Package sessionlimits provides resource budgets for read-only native session discovery.
// Copyright (c) 2026 Asymptote Labs.
// Threadforge-authored; licensed under Apache-2.0. See the repository-root LICENSE.
package sessionlimits

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
)

const (
	MaxEntriesPerStore            = 10_000
	MaxEntriesPerDirectory        = 10_000
	MaxDirectoryDepth             = 24
	MaxMetadataFileBytes          = 1 << 20
	MaxMetadataTotalBytes         = 64 << 20
	MaxNativeSessionReadBytes     = 64 << 20
	MaxNativeSessionExpandedBytes = 128 << 20
)

// ErrScanLimit means discovery returned a bounded partial result. Callers should keep the
// sessions already found and surface a path-free truncation warning.
var ErrScanLimit = errors.New("native session scan reached a configured resource limit")

// Budget accounts for directory entries and metadata bytes read while listing one runtime store.
// It must not be shared by concurrent scans.
type Budget struct {
	entries   int
	bytes     int64
	truncated bool
}

func NewBudget() *Budget { return &Budget{} }

func (b *Budget) Truncated() bool { return b != nil && b.truncated }

// MarkTruncated records that a source-specific parser stopped at its documented byte or record
// cap.
func (b *Budget) MarkTruncated() {
	if b != nil {
		b.truncated = true
	}
}

// ChargeEntry accounts for a record discovered from a non-filesystem source such as SQLite.
func (b *Budget) ChargeEntry() bool {
	if b == nil || b.entries >= MaxEntriesPerStore {
		if b != nil {
			b.truncated = true
		}
		return false
	}
	b.entries++
	return true
}

// ChargeBytes accounts for bounded reads performed by a source-specific parser.
func (b *Budget) ChargeBytes(n int64) bool {
	if b == nil || n < 0 {
		return false
	}
	remaining := MaxMetadataTotalBytes - b.bytes
	if n > remaining {
		b.bytes = MaxMetadataTotalBytes
		b.truncated = true
		return false
	}
	b.bytes += n
	return true
}

func (b *Budget) Warning() error {
	if b != nil && b.truncated {
		return ErrScanLimit
	}
	return nil
}

// ReadDir reads no more than the remaining per-store budget and per-directory cap. Entries are
// sorted by name so a truncated result is stable for a given directory layout.
func (b *Budget) ReadDir(path string) ([]fs.DirEntry, error) {
	if b == nil {
		return nil, errors.New("session scan budget is required")
	}
	remaining := MaxEntriesPerStore - b.entries
	if remaining <= 0 {
		b.truncated = true
		return nil, nil
	}
	limit := min(remaining, MaxEntriesPerDirectory)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("session store entry is not a local directory")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.IsDir() {
		return nil, errors.New("session store directory changed during scan")
	}
	entries, readErr := file.ReadDir(limit + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if len(entries) > limit {
		entries = entries[:limit]
		b.truncated = true
	}
	b.entries += len(entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// WalkDir performs a bounded, deterministic depth-first traversal. Symlink entries are visited
// but never followed because DirEntry.IsDir is false for symlinks.
func (b *Budget) WalkDir(root string, maxDepth int, visit func(path string, entry fs.DirEntry) error) error {
	if b == nil {
		return errors.New("session scan budget is required")
	}
	if maxDepth < 0 || maxDepth > MaxDirectoryDepth {
		return fmt.Errorf("walk depth must be between 0 and %d", MaxDirectoryDepth)
	}
	type node struct {
		path  string
		depth int
	}
	pending := []node{{path: root}}
	for len(pending) > 0 && !b.truncated {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		entries, err := b.ReadDir(current.path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		children := make([]node, 0)
		for _, entry := range entries {
			path := filepath.Join(current.path, entry.Name())
			if visit != nil {
				if err := visit(path, entry); err != nil {
					return err
				}
			}
			if !entry.IsDir() {
				continue
			}
			if current.depth >= maxDepth {
				b.truncated = true
				break
			}
			children = append(children, node{path: path, depth: current.depth + 1})
		}
		// Push in reverse so traversal visits lexically earlier paths first.
		for i := len(children) - 1; i >= 0; i-- {
			pending = append(pending, children[i])
		}
	}
	return nil
}

// ReadFile reads bounded metadata and charges it against the per-store byte budget. A too-large
// file is omitted and marks the result truncated; a short or changing read is rejected.
func (b *Budget) ReadFile(path string) ([]byte, error) {
	return b.ReadFileLimit(path, MaxMetadataFileBytes)
}

// ReadFileLimit reads a regular file up to maxFileBytes while charging actual bytes to the shared
// store budget. It is intended for source formats with a larger documented per-file cap, such as
// Cline's task-history JSON.
func (b *Budget) ReadFileLimit(path string, maxFileBytes int64) ([]byte, error) {
	if b == nil {
		return nil, errors.New("session scan budget is required")
	}
	if maxFileBytes < 1 {
		return nil, errors.New("file byte limit must be positive")
	}
	remaining := MaxMetadataTotalBytes - b.bytes
	if remaining <= 0 {
		b.truncated = true
		return nil, nil
	}
	limit := min(maxFileBytes, remaining)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("metadata is not a regular file")
	}
	if info.Size() > limit {
		b.truncated = true
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("metadata changed during open")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		b.truncated = true
		b.bytes += limit
		return nil, nil
	}
	if after, err := file.Stat(); err != nil || after.Size() != opened.Size() || after.ModTime() != opened.ModTime() {
		return nil, errors.New("metadata changed during read")
	}
	b.bytes += int64(len(data))
	return data, nil
}

// ReadFirstLine reads one bounded metadata line without consuming the rest of a transcript.
func (b *Budget) ReadFirstLine(path string, maxLineBytes int) ([]byte, error) {
	if b == nil {
		return nil, errors.New("session scan budget is required")
	}
	if maxLineBytes < 1 || maxLineBytes > MaxMetadataFileBytes {
		return nil, fmt.Errorf("first-line limit must be between 1 and %d bytes", MaxMetadataFileBytes)
	}
	remaining := MaxMetadataTotalBytes - b.bytes
	if remaining <= 0 {
		b.truncated = true
		return nil, nil
	}
	limit := min(int64(maxLineBytes), remaining)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("metadata is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("metadata changed during open")
	}
	counter := &countingReader{reader: io.LimitReader(file, limit+1)}
	bufferSize := int(min(int64(4096), limit))
	reader := bufio.NewReaderSize(counter, bufferSize)
	var line bytes.Buffer
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if line.Len()+len(chunk) > maxLineBytes || int64(line.Len()+len(chunk)) > limit {
			b.truncated = true
			b.bytes += min(counter.read, remaining)
			return nil, nil
		}
		if _, err := line.Write(chunk); err != nil {
			return nil, err
		}
		if readErr == nil {
			break
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		return nil, readErr
	}
	if counter.read > remaining {
		b.truncated = true
		b.bytes = MaxMetadataTotalBytes
		return nil, nil
	}
	b.bytes += counter.read
	data := bytes.TrimSuffix(bytes.TrimSuffix(line.Bytes(), []byte("\n")), []byte("\r"))
	return append([]byte(nil), data...), nil
}

// ReadLines reads a bounded prefix of JSONL metadata and accounts for buffered bytes as well as
// returned line bytes. It never scans beyond maxLines and maxLineBytes per line.
func (b *Budget) ReadLines(path string, maxLines, maxLineBytes int) ([][]byte, error) {
	if b == nil {
		return nil, errors.New("session scan budget is required")
	}
	if maxLines < 1 || maxLineBytes < 1 || maxLineBytes > MaxMetadataFileBytes {
		return nil, errors.New("invalid bounded metadata line limits")
	}
	remaining := MaxMetadataTotalBytes - b.bytes
	if remaining <= 0 {
		b.truncated = true
		return nil, nil
	}
	perFileLimit := min(int64(MaxMetadataFileBytes), remaining)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("metadata is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return nil, errors.New("metadata changed during open")
	}
	counter := &countingReader{reader: io.LimitReader(file, perFileLimit+1)}
	reader := bufio.NewReaderSize(counter, int(min(int64(4096), perFileLimit)))
	lines := make([][]byte, 0, maxLines)
	var line bytes.Buffer
	for len(lines) < maxLines {
		chunk, readErr := reader.ReadSlice('\n')
		if line.Len()+len(chunk) > maxLineBytes || int64(line.Len()+len(chunk)) > perFileLimit {
			b.truncated = true
			b.bytes += min(counter.read, remaining)
			break
		}
		if _, err := line.Write(chunk); err != nil {
			return lines, err
		}
		if readErr == nil {
			value := bytes.TrimSuffix(bytes.TrimSuffix(line.Bytes(), []byte("\n")), []byte("\r"))
			lines = append(lines, append([]byte(nil), value...))
			line.Reset()
			continue
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			if line.Len() > 0 {
				lines = append(lines, append([]byte(nil), line.Bytes()...))
			}
			break
		}
		return lines, readErr
	}
	if counter.read > remaining {
		b.truncated = true
		return lines, nil
	}
	b.bytes += counter.read
	return lines, nil
}

type countingReader struct {
	reader io.Reader
	read   int64
}

func (r *countingReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.read += int64(n)
	return n, err
}
