package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentsmd/internal/prompt"
)

func TestWriteAtomicReplacesContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("got %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want the file's own 600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".agentsmd-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// A crash between the two writes of WriteRaw must leave the text recoverable.
func TestRawMirrorSurvivesMissingRawFile(t *testing.T) {
	st := newTestStore(t)
	if err := st.WriteRaw("## A\n- keep me\n"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(st.RawPath); err != nil {
		t.Fatal(err)
	}
	mirror, ok, err := st.ReadMirror()
	if err != nil || !ok {
		t.Fatalf("mirror: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(mirror, "keep me") {
		t.Fatalf("mirror lost the text: %q", mirror)
	}
}

// A fresh install starts with every rewrite on; a saved choice is kept as it
// was, so the app never silently changes what the author turned off.
func TestOptionsDefaultOnAndSavedOnceChosen(t *testing.T) {
	st := newTestStore(t)
	if got := st.LoadState().Options; got != prompt.DefaultOptions() {
		t.Fatalf("fresh options = %+v", got)
	}
	if err := st.SaveState(State{Options: prompt.Options{Simplify: true}}); err != nil {
		t.Fatal(err)
	}
	got := st.LoadState().Options
	if got.Translate || got.Merge || !got.Simplify {
		t.Fatalf("saved options changed: %+v", got)
	}
}

func TestReadRawSeedsFromCompiledFile(t *testing.T) {
	st := newTestStore(t)
	if err := st.Write("## Existing\n- rule\n"); err != nil {
		t.Fatal(err)
	}
	raw, exists, err := st.ReadRaw()
	if err != nil || !exists {
		t.Fatalf("ok=%v err=%v", exists, err)
	}
	if !strings.Contains(raw, "rule") {
		t.Fatalf("seed = %q", raw)
	}
	if _, err := os.Stat(st.RawPath); err != nil {
		t.Fatalf("raw file not created: %v", err)
	}
}

func TestLockRefusesSecondOwnerAndRecoversStaleOne(t *testing.T) {
	dir := t.TempDir()
	first := &Store{StateDir: dir}
	if err := first.Lock(); err != nil {
		t.Fatal(err)
	}
	second := &Store{StateDir: dir}
	if err := second.Lock(); err == nil {
		t.Fatal("two owners of the same files")
	}
	first.Unlock()

	// A lock whose process is gone is taken over.
	stale := &Store{StateDir: dir}
	path := filepath.Join(dir, "lock")
	if err := os.WriteFile(path, []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stale.Lock(); err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	stale.Unlock()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock file left behind")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := New(filepath.Join(dir, ".agents", "AGENTS.md"), filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}
