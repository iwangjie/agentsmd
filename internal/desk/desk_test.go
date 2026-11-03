package desk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agentsmd/internal/engine"
	"agentsmd/internal/prompt"
)

// blockedEngine points the service at a model server that answers each request
// with the module it was sent, counting the calls so a test can prove the app
// did not ask twice for the same text. The counter is atomic because sections
// are compiled side by side.
func blockedEngine(t *testing.T, calls *int64) *engine.Gateway {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		atomic.AddInt64(calls, 1)
		sent := body.Messages[len(body.Messages)-1].Content
		_, section, _ := strings.Cut(sent, "\n---\n\n")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": section}}},
		})
	}))
	t.Cleanup(server.Close)
	gw := engine.New(t.TempDir())
	gw.BaseURL = server.URL
	return gw
}

func newService(t *testing.T, doc string) *Service {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".agents", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if doc != "" {
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(path, filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const doc = `## SOUL
- Be concise.

## Tools
- ` + "`lcu`" + `: control apps.

## Tailscale
- Port 443 is reserved.
`

func TestSetRawKeepsRawAndParsesModules(t *testing.T) {
	s := newService(t, "")
	snap := s.SetRaw(doc)
	if snap.Raw != doc {
		t.Fatalf("raw = %q", snap.Raw)
	}
	if len(snap.Modules) != 3 {
		t.Fatalf("modules = %d", len(snap.Modules))
	}
	if snap.Effective == "" {
		t.Fatal("effective empty")
	}
}

// The switches are the publishing control: turning one off writes AGENTS.md
// without touching the author's raw text.
func TestToggleWritesCompiledFileOnly(t *testing.T) {
	s := newService(t, doc)
	snap := s.ToggleModule("tools")
	if snap.Modules[1].Enabled {
		t.Fatal("module still enabled")
	}

	onDisk, err := os.ReadFile(s.store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), "control apps") {
		t.Fatalf("disabled module written: %s", onDisk)
	}

	raw, err := os.ReadFile(s.store.RawPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "control apps") {
		t.Fatalf("raw text lost the module: %s", raw)
	}

	snap = s.ToggleModule("tools")
	onDisk, _ = os.ReadFile(s.store.Path)
	if !strings.Contains(string(onDisk), "control apps") {
		t.Fatalf("re-enabled module missing: %s", onDisk)
	}
}

func TestAddRenameDeleteModule(t *testing.T) {
	s := newService(t, doc)

	snap := s.AddModule("Memory")
	if !strings.Contains(snap.Raw, "## Memory") {
		t.Fatalf("add: %q", snap.Raw)
	}
	if !strings.Contains(snap.Effective, "Memory") {
		t.Fatalf("add not compiled: %q", snap.Effective)
	}

	snap = s.RenameModule("memory", "Recall")
	if !strings.Contains(snap.Raw, "## Recall") || strings.Contains(snap.Raw, "## Memory") {
		t.Fatalf("rename: %q", snap.Raw)
	}

	snap = s.DeleteModule("recall")
	if strings.Contains(snap.Raw, "Recall") {
		t.Fatalf("delete: %q", snap.Raw)
	}
	for _, want := range []string{"## SOUL", "## Tools", "## Tailscale"} {
		if !strings.Contains(snap.Raw, want) {
			t.Fatalf("delete removed a neighbour: %q", snap.Raw)
		}
	}

	onDisk, _ := os.ReadFile(s.store.Path)
	if strings.Contains(string(onDisk), "Recall") {
		t.Fatalf("deleted module still in AGENTS.md: %s", onDisk)
	}
}

func TestDeleteLastModuleLeavesPrecedingText(t *testing.T) {
	s := newService(t, "Intro line.\n\n## Only\n- rule\n")
	snap := s.DeleteModule("only")
	if strings.Contains(snap.Raw, "## Only") {
		t.Fatalf("heading kept: %q", snap.Raw)
	}
	if !strings.Contains(snap.Raw, "Intro line.") {
		t.Fatalf("preamble lost: %q", snap.Raw)
	}
}

// A rewrite belongs to the text it was made from: editing a section drops its
// rewrite, and the section beside it keeps its own.
func TestRewriteFollowsItsModule(t *testing.T) {
	s := newService(t, doc)
	s.mu.Lock()
	for i := range s.modules {
		if s.modules[i].ID != "tools" {
			continue
		}
		s.modules[i] = prompt.Organize(s.modules[i], "## Tools\n- `lcu`: drive the desktop.\n")
	}
	s.applyCompiled()
	s.mu.Unlock()

	snap := s.Snapshot()
	if !strings.Contains(snap.Effective, "drive the desktop") {
		t.Fatalf("rewrite not shown: %q", snap.Effective)
	}
	if !strings.Contains(snap.Effective, "## Tailscale") {
		t.Fatalf("other modules lost: %q", snap.Effective)
	}

	// The author edits the Tailscale section: its neighbour's rewrite stays.
	snap = s.SetRaw(strings.Replace(snap.Raw, "Port 443 is reserved.", "Port 443 is for the gateway.", 1))
	if !strings.Contains(snap.Effective, "drive the desktop") {
		t.Fatalf("neighbour rewrite dropped: %q", snap.Effective)
	}
	if !strings.Contains(snap.Effective, "Port 443 is for the gateway.") {
		t.Fatalf("edited text missing: %q", snap.Effective)
	}

	// Editing the Tools section itself retires its rewrite.
	changed := strings.Replace(snap.Raw, "control apps", "drive apps", 1)
	snap = s.SetRaw(changed)
	if strings.Contains(snap.Effective, "drive the desktop") {
		t.Fatalf("stale rewrite kept: %q", snap.Effective)
	}
	if !strings.Contains(snap.Effective, "drive apps") {
		t.Fatalf("new text missing: %q", snap.Effective)
	}
}

// A translated section publishes its English heading, while the sidebar keeps
// the author's own wording next to the text they typed.
func TestHeadingTranslatedInDocumentOnly(t *testing.T) {
	s := newService(t, "## 网络环境\n- 公司使用对称\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{
				"content": "## Network\n- The office uses symmetric\n",
			}}},
		})
	}))
	t.Cleanup(server.Close)
	gw := engine.New(t.TempDir())
	gw.BaseURL = server.URL
	s.mu.Lock()
	s.engine = gw
	s.model = "test"
	s.mu.Unlock()

	snap := s.Compile(t.Context())
	if !strings.Contains(snap.Effective, "## Network") {
		t.Fatalf("published heading not translated: %q", snap.Effective)
	}
	if strings.Contains(snap.Effective, "网络环境") {
		t.Fatalf("source heading leaked into the document: %q", snap.Effective)
	}
	if snap.Modules[0].Title != "网络环境" {
		t.Fatalf("sidebar heading changed: %q", snap.Modules[0].Title)
	}
	if !strings.Contains(snap.Modules[0].OrganizedTitle, "Network") {
		t.Fatalf("organized heading not kept: %q", snap.Modules[0].OrganizedTitle)
	}

	onDisk, err := os.ReadFile(s.store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "## Network") || strings.Contains(string(onDisk), "网络环境") {
		t.Fatalf("AGENTS.md = %s", onDisk)
	}

	raw, err := os.ReadFile(s.store.RawPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "## 网络环境") {
		t.Fatalf("source file lost its own heading: %s", raw)
	}

	again, err := New(s.store.Path, s.store.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	restored := again.Snapshot()
	if !strings.Contains(restored.Effective, "## Network") {
		t.Fatalf("translated heading lost on restart: %q", restored.Effective)
	}
	if prompt.Stale(restored.Modules[0]) {
		t.Fatal("restored module reported as stale")
	}
}

// Rewrites survive a restart, so a launch does not spend model calls on text
// the app already organized.
func TestRewritesSurviveRestart(t *testing.T) {
	s := newService(t, doc)
	s.mu.Lock()
	for i := range s.modules {
		if s.modules[i].ID == "soul" {
			s.modules[i] = prompt.Organize(s.modules[i], "## SOUL\n- Say little.\n")
		}
	}
	s.applyCompiled()
	s.persist()
	s.mu.Unlock()

	again, err := New(s.store.Path, s.store.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	snap := again.Snapshot()
	if !strings.Contains(snap.Effective, "Say little") {
		t.Fatalf("rewrite lost on restart: %q", snap.Effective)
	}
	for _, m := range snap.Modules {
		if m.ID == "soul" && prompt.Stale(m) {
			t.Fatal("restored rewrite reported as stale")
		}
	}
}

// A rewrite saved before headings were translated has no heading of its own,
// so the app reports work outstanding rather than presenting it as finished.
func TestHeadinglessRewriteIsOutstanding(t *testing.T) {
	s := newService(t, doc)
	var calls int64
	s.engine = blockedEngine(t, &calls)
	s.mu.Lock()
	s.model = "test"
	for i := range s.modules {
		s.modules[i].Organized = "- body only\n"
		s.modules[i].OrganizedHash = s.modules[i].SourceHash
	}
	s.applyCompiled()
	compiled := s.compiledOK
	s.mu.Unlock()

	if compiled {
		t.Fatal("heading-less rewrites reported as compiled")
	}

	// The wait must end: one pass over these modules gives each a heading.
	snap := s.Compile(t.Context())
	if snap.Compiling {
		t.Fatal("compile did not finish")
	}
	for _, m := range snap.Modules {
		if prompt.Stale(m) {
			t.Fatalf("module %q still stale after a pass", m.ID)
		}
	}
	if atomic.LoadInt64(&calls) == 0 {
		t.Fatal("model not asked for the missing headings")
	}
}

// Compile skips sections whose rewrite still matches, so a second pass over
// unchanged text asks the model nothing.
func TestCompileSkipsUnchangedModules(t *testing.T) {
	s := newService(t, doc)
	var calls int64
	s.engine = blockedEngine(t, &calls)
	s.mu.Lock()
	s.model = "test"
	s.mu.Unlock()

	snap := s.Compile(t.Context())
	if got := atomic.LoadInt64(&calls); got != int64(len(snap.Modules)) {
		t.Fatalf("calls = %d, want one per module (%d)", got, len(snap.Modules))
	}
	for _, m := range snap.Modules {
		if m.OrganizedHash != m.SourceHash {
			t.Fatalf("%s has no rewrite for its text", m.ID)
		}
	}

	s.Compile(t.Context())
	if got := atomic.LoadInt64(&calls); got != int64(len(snap.Modules)) {
		t.Fatalf("unchanged text asked the model again: %d calls", got)
	}
}

// A rewrite is kept across a restart, so launching the app does not spend a
// model call on text the author already organized.
func TestCompilePersistsRewrites(t *testing.T) {
	s := newService(t, doc)
	var calls int64
	s.engine = blockedEngine(t, &calls)
	s.mu.Lock()
	s.model = "test"
	s.mu.Unlock()

	s.Compile(t.Context())
	first := atomic.LoadInt64(&calls)
	if first == 0 {
		t.Fatal("no rewrite was asked for")
	}

	again, err := New(s.store.Path, s.store.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	again.engine = blockedEngine(t, &calls)
	again.mu.Lock()
	again.model = "test"
	again.mu.Unlock()
	again.Compile(t.Context())
	if got := atomic.LoadInt64(&calls); got != first {
		t.Fatalf("restart asked the model again: %d calls, was %d", got, first)
	}
}

func TestRawSurvivesRestart(t *testing.T) {
	s := newService(t, "")
	s.SetRaw("# 我的规则\n\n尽量用中文回复。\n")

	again, err := New(s.store.Path, s.store.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	snap := again.Snapshot()
	if !strings.Contains(snap.Raw, "尽量用中文回复") {
		t.Fatalf("raw lost on restart: %q", snap.Raw)
	}
}
