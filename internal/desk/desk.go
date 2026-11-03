// Package desk is the app's state. It keeps two documents: the author's own
// text in AGENTS.raw.md, which it never rewrites, and the compiled document in
// AGENTS.md, which agents read. The compilation turns the raw text into
// modules and rewrites them per the settings and an optional instruction,
// without the author having to care how.
package desk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"agentsmd/internal/engine"
	"agentsmd/internal/prompt"
	"agentsmd/internal/store"
)

// Snapshot is everything the page draws. Bound methods return it whole, so the
// page keeps no copy of a fact the app owns.
type Snapshot struct {
	Path        string `json:"path"`
	RawPath     string `json:"rawPath"`
	DisplayPath string `json:"displayPath"`
	Exists      bool   `json:"exists"`

	// Raw is the author's own text. The page edits this, and nothing
	// rewrites it.
	Raw string `json:"raw"`
	// Modules is the structure the compiled document is made of, kept
	// switched on or off. Each module carries its own rewrite and the hash of
	// the text that rewrite was made from.
	Modules []prompt.Module `json:"modules"`
	// Effective is the document agents read: the enabled modules as the model
	// last rewrote them.
	Effective string `json:"effective"`
	// Disk is AGENTS.md as it is now.
	Disk string `json:"disk"`

	Options prompt.Options `json:"options"`
	Want    string         `json:"want"`

	Models  []engine.Model `json:"models"`
	Model   string         `json:"model"`
	Gateway engine.Status  `json:"gateway"`

	// Compiled is true when Effective came from a model rather than the raw
	// text alone; Compiling is true while a compile runs.
	Compiled    bool   `json:"compiled"`
	Compiling   bool   `json:"compiling"`
	CompileNote string `json:"compileNote"`

	Dirty bool `json:"dirty"`

	Backups    []store.Backup `json:"backups"`
	LastBackup string         `json:"lastBackup"`

	Message     string `json:"message"`
	MessageKind string `json:"messageKind"` // info | success | warning | error

	RawLines    int `json:"rawLines"`
	ResultLines int `json:"resultLines"`
	DiskLines   int `json:"diskLines"`
	Chars       int `json:"chars"`
	RawChars    int `json:"rawChars"`
	DiskChars   int `json:"diskChars"`
	Total       int `json:"total"`
	Enabled     int `json:"enabled"`
}

// Focus is emitted when the window comes to the front, so the page can offer
// to reload a file that changed outside the app.
var Focus = mygo.NewEvent[bool]("focused")

// Service is the bound side of the app.
type Service struct {
	mu       sync.Mutex
	store    *store.Store
	cache    *store.Cache
	engine   *engine.Gateway
	stateDir string
	win      *mygo.Window

	raw        string
	disk       string
	fileExists bool
	modules    []prompt.Module
	options    prompt.Options
	model      string
	want       string

	models  []engine.Model
	gateway engine.Status

	// compiling guards one pass at a time; queued asks for another pass once
	// it ends.
	compiling bool
	queued    bool
	cancel    context.CancelFunc

	compiledOK  bool
	effective   string
	compileNote string
	busy        bool
	message     string
	messageKind string
	lastBackup  string
}

// New opens the store at the user's global AGENTS.md.
func New(agentsPath, stateDir string) (*Service, error) {
	st, err := store.New(agentsPath, stateDir)
	if err != nil {
		return nil, err
	}
	s := &Service{store: st, cache: st.Cache(), engine: engine.New(stateDir), stateDir: stateDir}
	saved := st.LoadState()
	s.options = saved.Options
	s.want = saved.Want
	s.model = saved.Model
	if s.model == "" {
		s.model = s.engine.Model
	}
	if s.model != "" {
		s.engine.Choose(stateDir, s.model)
	}
	s.load()
	return s, nil
}

// Attach gives the service the window it publishes events to. It is a package
// function so it stays out of the page's bound surface.
func Attach(s *Service, win *mygo.Window) {
	s.mu.Lock()
	s.win = win
	s.mu.Unlock()
	win.OnFocus(func() {
		s.mu.Lock()
		w := s.win
		s.mu.Unlock()
		if w != nil {
			_ = Focus.Emit(w, true)
		}
	})
}

// load reads both files and re-joins the modules the author switched off,
// which the compiled document no longer holds.
func (s *Service) load() {
	s.recover()
	raw, _, err := s.store.ReadRaw()
	if err != nil {
		raw = ""
		s.setMessage("无法读取 AGENTS.raw.md："+err.Error(), "error")
	}
	disk, exists, err := s.store.ReadSource()
	if err != nil {
		s.setMessage("无法读取 AGENTS.md："+err.Error(), "error")
	}
	s.raw, s.disk, s.fileExists = raw, disk, exists

	saved := s.store.LoadState()
	rewrites := map[string]prompt.Module{}
	for _, m := range saved.Organized {
		rewrites[m.ID] = m
	}
	modules := prompt.Parse(raw)
	present := map[string]bool{}
	for i := range modules {
		present[modules[i].ID] = true
		if was, ok := rewrites[modules[i].ID]; ok && was.OrganizedHash == modules[i].SourceHash {
			modules[i].Organized = was.Organized
			modules[i].OrganizedHash = was.OrganizedHash
			modules[i].OrganizedTitle = was.OrganizedTitle
		}
	}
	for _, off := range saved.Disabled {
		if present[off.ID] {
			continue
		}
		off.Enabled = false
		measure(&off)
		modules = append(modules, off)
	}
	s.modules = modules
	s.applyCompiled()
}

// recover restores AGENTS.raw.md from its mirror when a crash or a bad write
// left it missing or empty. The mirror is written before the raw file and read
// only when the raw file is gone, so the author's text survives either way.
func (s *Service) recover() {
	raw, exists, err := s.store.ReadRaw()
	if err != nil || (exists && strings.TrimSpace(raw) != "") {
		return
	}
	mirror, ok, err := s.store.ReadMirror()
	if err != nil || !ok || strings.TrimSpace(mirror) == "" {
		return
	}
	_ = s.store.WriteRaw(mirror)
	s.setMessage("已从备份副本恢复 AGENTS.raw.md", "warning")
}

func measure(m *prompt.Module) {
	m.Lines = prompt.LineCount(m.Body)
	m.Chars = len([]rune(m.Body))
}

// inputs is everything a single module's rewrite is made from. A rewrite whose
// inputs moved is not reused for the text that replaced them. The version tag
// retires answers made before a rewrite carried its own heading.
func (s *Service) inputs(m prompt.Module) string {
	return "titled\x00" + m.SourceHash + "\x00" + s.options.Key() + "\x00" + strings.TrimSpace(s.want)
}

// applyCompiled derives Effective: the enabled modules as the model last
// rewrote them, falling back to the author's own text section by section. A
// module keeps its rewrite only while that rewrite was made from its current
// text, so the right side always describes what is on the left.
func (s *Service) applyCompiled() {
	s.effective = prompt.Effective(s.modules)
	s.compiledOK = false
	for _, m := range s.modules {
		if m.Enabled && strings.TrimSpace(m.Organized) != "" && m.OrganizedHash == m.SourceHash && !prompt.Stale(m) {
			s.compiledOK = true
			break
		}
	}
}

// Compile asks the model to turn the current modules into the document agents
// should read. It runs on the page's demand and again, coalesced, whenever the
// text changes while one runs.
func (s *Service) Compile(ctx context.Context) Snapshot {
	s.mu.Lock()
	if s.compiling {
		s.queued = true
		s.mu.Unlock()
		return s.Snapshot()
	}
	if strings.TrimSpace(s.raw) == "" {
		s.setMessage("还没有内容可整理", "info")
		s.mu.Unlock()
		return s.Snapshot()
	}
	model := s.model
	if model == "" {
		s.setMessage("在设置里选择一个模型", "warning")
		s.mu.Unlock()
		return s.Snapshot()
	}

	// Only sections whose text moved since their rewrite go to the model. The
	// rest keep the answer they already have, so editing one section never
	// rewords the others.
	pending := make([]prompt.Module, 0, len(s.modules))
	for _, m := range s.modules {
		if prompt.Stale(m) {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		s.mu.Unlock()
		return s.Snapshot()
	}
	options, want := s.options, s.want

	s.compiling = true
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	instruction := prompt.BuildModuleInstruction(options, want)
	answers := make(map[string]string, len(pending))

	// Sections are independent, so they go to the model side by side. A
	// section that fails keeps the author's own text and leaves its
	// neighbours' answers intact.
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	slots := make(chan struct{}, 4)

	for _, m := range pending {
		cacheKey := s.inputs(m)
		if hit, ok := s.cache.Get(cacheKey); ok && strings.TrimSpace(hit) != "" {
			answers[m.ID] = hit
			continue
		}
		wg.Add(1)
		go func(m prompt.Module, cacheKey string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			message := instruction + "\n---\n\n" + prompt.Compose([]prompt.Module{m})
			raw, err := s.engine.Chat(runCtx, model, message)
			if err == nil {
				answer := prompt.SanitizeOutput(raw)
				if strings.TrimSpace(answer) == "" {
					err = errors.New("模型返回了空内容")
				} else {
					s.cache.Put(cacheKey, answer)
					mu.Lock()
					answers[m.ID] = answer
					mu.Unlock()
					return
				}
			}
			mu.Lock()
			if firstErr == nil && !errors.Is(err, context.Canceled) {
				firstErr = err
			}
			mu.Unlock()
		}(m, cacheKey)
	}
	wg.Wait()

	err := firstErr

	s.mu.Lock()
	again := s.queued
	s.queued = false
	s.compiling = false
	s.cancel = nil
	if errors.Is(err, context.Canceled) {
		s.mu.Unlock()
		return s.Snapshot()
	}
	if err != nil {
		s.compileNote = friendlyError(err)
	}
	// A module that was edited again mid-flight keeps the answer only when it
	// still belongs to the text that answer was made from.
	for i := range s.modules {
		answer, ok := answers[s.modules[i].ID]
		if !ok {
			continue
		}
		s.modules[i] = prompt.Organize(s.modules[i], answer)
	}
	s.applyCompiled()
	if err == nil {
		s.compileNote = ""
	}
	// The rewrites are part of what the app remembers: a restart reuses them
	// instead of spending a model call on text that has not moved.
	s.persist()
	if s.writeCompiled() && err == nil {
		s.setMessage("已整理并写入 "+displayPath(s.store.Path), "success")
	}
	snap := s.snapshot()
	s.mu.Unlock()

	if again {
		// The author kept typing: one more pass for whatever moved, never a
		// second pass for text that already has its rewrite.
		if s.hasStale() {
			return s.Compile(context.Background())
		}
	}
	return snap
}

// hasStale reports whether any module still needs the model.
func (s *Service) hasStale() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.modules {
		if prompt.Stale(m) {
			return true
		}
	}
	return false
}

// CancelCompile stops a compile in flight, for a quit or a reload.
func (s *Service) CancelCompile() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.queued = false
	s.mu.Unlock()
}

// Snapshot returns the current state.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

func (s *Service) snapshot() Snapshot {
	snap := Snapshot{
		Path:        s.store.Path,
		RawPath:     s.store.RawPath,
		DisplayPath: displayPath(s.store.Path),
		Exists:      s.fileExists,
		Raw:         s.raw,
		Modules:     s.modules,
		Effective:   s.effective,
		Disk:        s.disk,
		Options:     s.options,
		Want:        s.want,
		Models:      s.models,
		Model:       s.model,
		Gateway:     s.gateway,
		Compiled:    s.compiledOK,
		Compiling:   s.compiling,
		CompileNote: s.compileNote,
		Dirty:       prompt.Normalize(s.effective) != prompt.Normalize(s.disk),
		Backups:     s.store.Backups(),
		LastBackup:  s.lastBackup,
		Message:     s.message,
		MessageKind: s.messageKind,
		RawLines:    prompt.LineCount(s.raw),
		ResultLines: prompt.LineCount(s.effective),
		DiskLines:   prompt.LineCount(s.disk),
		Chars:       len([]rune(s.effective)),
		RawChars:    len([]rune(s.raw)),
		DiskChars:   len([]rune(s.disk)),
		Total:       len(s.modules),
	}
	for _, m := range s.modules {
		if m.Enabled {
			snap.Enabled++
		}
	}
	if len(snap.Backups) > 15 {
		snap.Backups = snap.Backups[:15]
	}
	return snap
}

// RefreshGateway re-reads the model catalog. The page calls it on launch and
// when the gateway was unreachable, so a gateway started later is picked up
// without a restart.
func (s *Service) RefreshGateway(ctx context.Context) Snapshot {
	status := s.engine.Status(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	wasUp := s.gateway.Reachable
	s.gateway = status
	s.models = status.Models
	if status.Reachable && !wasUp {
		s.setMessage("已连接模型服务，共 "+fmt.Sprint(len(status.Models))+" 个模型", "info")
	}
	s.gateway.Model = s.model
	s.applyCompiled()
	return s.snapshot()
}

// SetRaw stores the author's text and re-parses it. It is the one call the
// page makes while typing, and it never waits for a model.
func (s *Service) SetRaw(text string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	off := map[string]bool{}
	rewrites := map[string]prompt.Module{}
	for _, m := range s.modules {
		if !m.Enabled {
			off[m.ID] = true
		}
		if strings.TrimSpace(m.Organized) != "" {
			rewrites[m.ID] = m
		}
	}
	present := map[string]bool{}
	modules := prompt.Parse(text)
	for i := range modules {
		present[modules[i].ID] = true
		if off[modules[i].ID] {
			modules[i].Enabled = false
		}
		if was, ok := rewrites[modules[i].ID]; ok && was.OrganizedHash == modules[i].SourceHash {
			modules[i].Organized = was.Organized
			modules[i].OrganizedHash = was.OrganizedHash
			modules[i].OrganizedTitle = was.OrganizedTitle
		}
	}
	for _, m := range s.modules {
		if !m.Enabled && !present[m.ID] {
			modules = append(modules, m)
		}
	}
	s.raw = text
	s.modules = modules
	s.applyCompiled()
	_ = s.store.WriteRaw(text)
	s.persist()
	return s.snapshot()
}

// SetWant stores an optional instruction for the next compile.
func (s *Service) SetWant(text string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want = text
	s.applyCompiled()
	s.persist()
	return s.snapshot()
}

// ToggleModule switches one module. The raw text keeps it; only the compiled
// document loses or gains it.
func (s *Service) ToggleModule(id string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.modules {
		if s.modules[i].ID == id {
			s.modules[i].Enabled = !s.modules[i].Enabled
			break
		}
	}
	s.applyCompiled()
	s.persist()
	s.setMessage("", "")
	s.writeCompiled()
	return s.snapshot()
}

// SetAll switches every module at once.
func (s *Service) SetAll(enabled bool) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.modules {
		s.modules[i].Enabled = enabled
	}
	s.applyCompiled()
	s.persist()
	s.writeCompiled()
	return s.snapshot()
}

// SetOptions selects what every compile does.
func (s *Service) SetOptions(o prompt.Options) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options = o
	s.applyCompiled()
	s.persist()
	return s.snapshot()
}

// SetModel chooses the model the gateway should run.
func (s *Service) SetModel(id string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = id
	s.engine.Choose(s.stateDir, id)
	s.compileNote = ""
	s.applyCompiled()
	s.persist()
	return s.snapshot()
}

// writeCompiled produces AGENTS.md from the effective document, backing up the
// version it replaces. It is called by every change that can alter what agents
// read, so the switches are the whole publishing control. A failed write
// leaves the previous file untouched and reports why.
func (s *Service) writeCompiled() bool {
	doc := prompt.Effective(s.modules)
	if prompt.Normalize(doc) == prompt.Normalize(s.disk) {
		return false
	}

	backup, err := s.store.Backup()
	if err != nil {
		s.setMessage("备份失败，未写入："+err.Error(), "error")
		return false
	}
	if err := s.store.Write(doc); err != nil {
		s.setMessage("写入失败，原文件未改动："+err.Error(), "error")
		return false
	}
	if backup != "" {
		s.lastBackup = filepath.Base(backup)
	}
	s.disk = doc
	s.fileExists = true
	return true
}

// Apply writes the compiled document now, for the ⌘S shortcut and the page's
// explicit command.
func (s *Service) Apply() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draftTimerPending() {
		// The page saves the draft on its own timer; a write here would use
		// text the author is still typing.
		return s.snapshot()
	}
	if s.writeCompiled() {
		s.setMessage("已写入 "+displayPath(s.store.Path), "success")
	} else {
		s.setMessage("已是最新，无需写入", "info")
	}
	return s.snapshot()
}

// draftTimerPending always answers false: the page owns its own debounce, and
// this seam exists so Apply reads as a decision rather than an accident.
func (s *Service) draftTimerPending() bool { return false }

// AddModule appends a module and writes the result.
func (s *Service) AddModule(title string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	title = strings.TrimSpace(title)
	if title == "" {
		title = "新模块"
	}
	s.raw = strings.TrimRight(s.raw, "\n")
	if s.raw != "" {
		s.raw += "\n\n"
	}
	s.raw += "## " + title + "\n"
	s.reparseLocked()
	s.writeRawLocked()
	s.writeCompiled()
	s.setMessage("已新增「"+title+"」，写入 AGENTS.md", "success")
	return s.snapshot()
}

// RenameModule retitles a module in the raw text and writes the result.
func (s *Service) RenameModule(id, title string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	title = strings.TrimSpace(title)
	if title == "" {
		return s.snapshot()
	}
	for _, m := range s.modules {
		if m.ID != id {
			continue
		}
		old := "#" + strings.Repeat("#", m.Level-1) + " " + m.Title
		if m.Title == "" {
			// The preamble has no heading to retitle; it becomes a module.
			s.raw = "## " + title + "\n" + s.raw
		} else {
			next := "#" + strings.Repeat("#", m.Level-1) + " " + title
			s.raw = strings.Replace(s.raw, old, next, 1)
		}
		s.reparseLocked()
		s.writeRawLocked()
		s.writeCompiled()
		s.setMessage("已重命名为「"+title+"」", "success")
		return s.snapshot()
	}
	return s.snapshot()
}

// DeleteModule removes a module, heading and body, from the raw text.
func (s *Service) DeleteModule(id string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.modules {
		if m.ID != id {
			continue
		}
		s.raw = removeModule(s.raw, m)
		s.modules = append(s.modules[:i], s.modules[i+1:]...)
		s.reparseLocked()
		s.writeRawLocked()
		s.writeCompiled()
		s.setMessage("已删除「"+(m.Title)+"」", "success")
		return s.snapshot()
	}
	return s.snapshot()
}

// reparseLocked re-reads the modules from the raw text, keeping the switches
// the author set on modules that still exist and the rewrites that still
// belong to their text.
func (s *Service) reparseLocked() {
	off := map[string]bool{}
	rewrites := map[string]prompt.Module{}
	for _, m := range s.modules {
		if !m.Enabled {
			off[m.ID] = true
		}
		if strings.TrimSpace(m.Organized) != "" {
			rewrites[m.ID] = m
		}
	}
	modules := prompt.Parse(s.raw)
	for i := range modules {
		if off[modules[i].ID] {
			modules[i].Enabled = false
		}
		if was, ok := rewrites[modules[i].ID]; ok && was.OrganizedHash == modules[i].SourceHash {
			modules[i].Organized = was.Organized
			modules[i].OrganizedHash = was.OrganizedHash
			modules[i].OrganizedTitle = was.OrganizedTitle
		}
	}
	s.modules = modules
	s.applyCompiled()
	s.persist()
}

// friendlyError turns a gateway error into what the author can act on.
func friendlyError(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "switched off"), strings.Contains(text, "is not served"):
		return "所选模型在 magpie 里已关闭或不再提供，在设置里换一个模型"
	case strings.Contains(text, "no access to model"):
		return "所选模型没有访问权限，在设置里换一个模型"
	case strings.Contains(text, "invalid converted request type"):
		return "所选模型不接受这种请求，在设置里换一个模型"
	case strings.Contains(text, "Connection refused"), strings.Contains(text, "connect: connection refused"):
		return "连不上模型服务，确认它在运行"
	case strings.Contains(text, "context deadline exceeded"), strings.Contains(text, "超时"):
		return "模型服务没有及时返回"
	}
	if len(text) > 200 {
		return text[:200] + "…"
	}
	return text
}

func (s *Service) writeRawLocked() {
	if err := s.store.WriteRaw(s.raw); err != nil {
		s.setMessage("保存原文失败："+err.Error(), "error")
	}
}

// removeModule deletes one module's heading and body from a raw document,
// leaving the text that follows it intact.
func removeModule(raw string, m prompt.Module) string {
	lines := strings.Split(raw, "\n")
	if m.Title == "" {
		return raw
	}
	heading := strings.Repeat("#", m.Level) + " " + m.Title
	start, end := -1, len(lines)
	for i, line := range lines {
		if start < 0 {
			if strings.TrimSpace(line) == heading {
				start = i
			}
			continue
		}
		if lvl, _, ok := prompt.Heading(line); ok && lvl <= m.Level {
			end = i
			break
		}
	}
	if start < 0 {
		return raw
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := append(append([]string{}, lines[:start]...), lines[end:]...)
	return strings.TrimLeft(strings.Join(out, "\n"), "\n")
}

// Reload reads both files again, dropping unsaved edits.
func (s *Service) Reload() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.setMessage("已重新读取磁盘上的文件", "info")
	return s.snapshot()
}

// DiskChanged reports whether AGENTS.md changed outside the app.
func (s *Service) DiskChanged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, _, err := s.store.ReadSource()
	if err != nil {
		return false
	}
	return text != s.disk
}

// Restore puts a backup of the compiled document back on disk.
func (s *Service) Restore(name string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, err := s.store.ReadBackup(name)
	if err != nil {
		s.setMessage("读取备份失败："+err.Error(), "error")
		return s.snapshot()
	}
	if backup, err := s.store.Backup(); err != nil {
		s.setMessage("备份当前版本失败："+err.Error(), "error")
		return s.snapshot()
	} else if backup != "" {
		s.lastBackup = filepath.Base(backup)
	}
	if err := s.store.Write(text); err != nil {
		s.setMessage("写入失败："+err.Error(), "error")
		return s.snapshot()
	}
	s.disk = text
	s.fileExists = true
	s.setMessage("已恢复备份 "+name, "success")
	return s.snapshot()
}

// Reveal shows AGENTS.md in the file manager.
func (s *Service) Reveal() error {
	s.mu.Lock()
	path := s.store.Path
	s.mu.Unlock()
	return Reveal(path)
}

// RevealBackups opens the backups folder.
func (s *Service) RevealBackups() error {
	return mygo.Shell.OpenPath(filepath.Join(s.store.StateDir, "backups"))
}

// Reveal shows a file in the file manager. It is a package function so the
// native menu can call it without a service value.
func Reveal(path string) error {
	mygo.Shell.ShowItemInFolder(path)
	return nil
}

func (s *Service) setMessage(text, kind string) {
	s.message, s.messageKind = text, kind
}

func (s *Service) persist() {
	st := store.State{Options: s.options, Model: s.model, Want: s.want}
	for _, m := range s.modules {
		if !m.Enabled {
			m.Enabled = false
			st.Disabled = append(st.Disabled, m)
		}
		if strings.TrimSpace(m.Organized) != "" {
			st.Organized = append(st.Organized, m)
		}
	}
	_ = s.store.SaveState(st)
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
