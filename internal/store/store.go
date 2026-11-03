// Package store reads and writes the files the app owns: the global AGENTS.md,
// the modules it keeps switched off, and timestamped backups.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentsmd/internal/prompt"
)

// Store is the app's view of the two files it maintains and its sidecar
// state: RawPath holds what the author wrote, Path the compiled document agents
// read. Keeping the raw text in a file of its own is what lets the author
// reopen the app and keep editing prose that the compiler has since rewritten.
type Store struct {
	// Path of the global AGENTS.md: the compiled document.
	Path string
	// RawPath of the author's own text, beside it.
	RawPath string
	// StateDir holds preferences, disabled modules and backups.
	StateDir string

	lockPath string
}

// processAlive reports whether a PID still belongs to a running process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// New resolves the default locations: ~/.agents/AGENTS.md and the app's data
// directory.
func New(path, stateDir string) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = filepath.Join(home, ".agents", "AGENTS.md")
	}
	if stateDir == "" {
		stateDir = filepath.Join(home, "Library", "Application Support", "AgentsMD")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "backups"), 0o755); err != nil {
		return nil, err
	}
	raw := filepath.Join(filepath.Dir(path), "AGENTS.raw.md")
	return &Store{Path: path, RawPath: raw, StateDir: stateDir}, nil
}

// State is what the app remembers between launches: the modules the user
// switched off, the transformations selected, the chosen model, and the
// expectation last typed.
type State struct {
	Disabled []prompt.Module `json:"disabled"`
	Options  prompt.Options  `json:"options"`
	Model    string          `json:"model"`
	Want     string          `json:"want"`
	// Organized keeps each module's rewrite next to the hash it was made
	// from, so a restart reuses it instead of spending model calls.
	Organized []prompt.Module `json:"organized"`
}

func (s *Store) stateFile() string { return filepath.Join(s.StateDir, "state.json") }

// LoadState reads the sidecar state. A missing or unreadable file yields the
// defaults, so a fresh install starts with every rewrite switched on; a file
// that exists keeps exactly the choices it recorded.
func (s *Store) LoadState() State {
	data, err := os.ReadFile(s.stateFile())
	if err != nil {
		return State{Options: prompt.DefaultOptions()}
	}
	var st State
	if json.Unmarshal(data, &st) != nil {
		return State{Options: prompt.DefaultOptions()}
	}
	return st
}

// SaveState writes the sidecar state.
func (s *Store) SaveState(st State) error {
	if st.Disabled == nil {
		st.Disabled = []prompt.Module{}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.stateFile(), data)
}

// ReadRaw returns the author's text. When the raw file does not exist yet, the
// compiled file on disk stands in for it: that is how a file written before
// the app existed becomes the first draft.
func (s *Store) ReadRaw() (string, bool, error) {
	data, err := os.ReadFile(s.RawPath)
	if err == nil {
		return string(data), true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	text, exists, err := s.ReadSource()
	if err != nil || !exists {
		return "", false, err
	}
	if err := writeAtomic(s.RawPath, []byte(text)); err != nil {
		return "", false, err
	}
	return text, true, nil
}

// WriteRaw saves the author's text. The mirror is written first, so the text
// survives a crash between the two writes; recovery reads it only when the raw
// file is missing or empty.
func (s *Store) WriteRaw(text string) error {
	if err := writeAtomic(s.mirrorPath(), []byte(text)); err != nil {
		return err
	}
	return writeAtomic(s.RawPath, []byte(text))
}

// ReadMirror returns the recovery copy of the raw text.
func (s *Store) ReadMirror() (string, bool, error) {
	data, err := os.ReadFile(s.mirrorPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

func (s *Store) mirrorPath() string {
	return filepath.Join(s.StateDir, "AGENTS.raw.md")
}

// ReadSource returns the compiled file on disk, or an empty document when it
// does not exist yet.
func (s *Store) ReadSource() (string, bool, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// Backup copies the current file into the backups directory and returns its
// path. It returns "" when there was nothing to copy.
func (s *Store) Backup() (string, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.StateDir, "backups")
	base := time.Now().Format("20060102-150405")
	target := filepath.Join(dir, base+".md")
	for i := 2; ; i++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, i))
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return "", err
	}
	return target, nil
}

// Write installs text as the global AGENTS.md, atomically.
func (s *Store) Write(text string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	return writeAtomic(s.Path, []byte(text))
}

// Backup is one file in the backups directory.
type Backup struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
	Size int64     `json:"size"`
}

// Backups lists saved versions, newest first.
func (s *Store) Backups() []Backup {
	entries, err := os.ReadDir(filepath.Join(s.StateDir, "backups"))
	if err != nil {
		return []Backup{}
	}
	out := make([]Backup, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: e.Name(), At: info.ModTime(), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// ReadBackup returns the text of a backup by name.
func (s *Store) ReadBackup(name string) (string, error) {
	if name != filepath.Base(name) || !strings.HasSuffix(name, ".md") {
		return "", errors.New("bad backup name")
	}
	data, err := os.ReadFile(filepath.Join(s.StateDir, "backups", name))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Cache stores generated documents keyed by their inputs, so switching a
// module back and forth does not spend a model call on the same text.
type Cache struct{ dir string }

// Cache returns the cache in the store's state directory.
func (s *Store) Cache() *Cache { return &Cache{dir: filepath.Join(s.StateDir, "cache")} }

// Get returns the cached answer for key.
func (c *Cache) Get(key string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(c.dir, prompt.Hash(key)+".md"))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Put saves an answer for key.
func (c *Cache) Put(key, value string) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(c.dir, prompt.Hash(key)+".md"), []byte(value), 0o644)
}

// writeAtomic replaces path with data so that a crash at any moment leaves
// either the old file or the new one, never a truncated mix: the bytes go to a
// temporary file in the same directory, are flushed to the platter, renamed
// over the target, and the directory entry is flushed too. The result is read
// back, so a write that did not land reports an error instead of a success the
// next launch would contradict.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".agentsmd-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	written, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(written, data) {
		return fmt.Errorf("%s 写入后校验不一致", filepath.Base(path))
	}
	return nil
}

// Lock makes the running app the only writer of these files. A second window
// or a stale process would otherwise compile over the first one's work; the
// lock is an exclusive file holding the owner's PID, and a lock whose process
// is gone is taken over.
func (s *Store) Lock() error {
	path := filepath.Join(s.StateDir, "lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			defer f.Close()
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			s.lockPath = path
			return werr
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return err
		}
		pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if convErr != nil || !processAlive(pid) {
			_ = os.Remove(path)
			continue
		}
		return fmt.Errorf("AgentsMD 已在运行（进程 %d）", pid)
	}
	return errors.New("无法获取文件锁")
}

// Unlock releases the app's hold on its files.
func (s *Store) Unlock() {
	if s.lockPath != "" {
		_ = os.Remove(s.lockPath)
		s.lockPath = ""
	}
}
