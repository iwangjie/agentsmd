// Package prompt owns the AGENTS.md model: the modules it is made of, the
// canonical text they compose, and the deterministic transforms that stand in
// for an AI when one is not available.
package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// Module is one section of the author's file, with the rewrite the model made
// of it. A module with an empty title is the preamble above the first
// heading. Organized is that rewrite and OrganizedHash is the SourceHash it
// was made from, so text that has not changed keeps its rewrite and never
// spends another model call.
type Module struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Level         int    `json:"level"`
	Body          string `json:"body"`
	Enabled       bool   `json:"enabled"`
	Lines         int    `json:"lines"`
	Chars         int    `json:"chars"`
	Organized     string `json:"organized"`
	OrganizedHash string `json:"organizedHash"`
	// OrganizedTitle is the heading as the model wrote it, which is how a
	// Chinese section becomes an English one in AGENTS.md while the sidebar
	// keeps showing the author's own wording.
	OrganizedTitle string `json:"organizedTitle"`
	SourceHash     string `json:"sourceHash"`
}

// Options are the transformations applied to the enabled modules before the
// result is written to AGENTS.md.
type Options struct {
	Translate bool `json:"translate"`
	Simplify  bool `json:"simplify"`
	Merge     bool `json:"merge"`
}

// DefaultOptions are what a fresh install starts with: every rewrite the app
// can do, so the first document it produces is already the polished one.
func DefaultOptions() Options {
	return Options{Translate: true, Simplify: true, Merge: true}
}

// Active reports whether any transformation is selected.
func (o Options) Active() bool { return o.Translate || o.Simplify || o.Merge }

// Key returns a stable description of the options, part of a cache signature.
func (o Options) Key() string {
	b := make([]byte, 0, 3)
	for _, on := range []bool{o.Translate, o.Simplify, o.Merge} {
		if on {
			b = append(b, '1')
		} else {
			b = append(b, '0')
		}
	}
	return string(b)
}

// Parse splits a document into modules at level 1 and 2 headings, keeping
// fenced code blocks intact. The enabled flag is always true; the caller owns
// the previous state and carries flags across.
func Parse(text string) []Module {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var modules []Module
	current := Module{Enabled: true}
	var body []string
	inFence := false

	flush := func() {
		raw := strings.Trim(strings.Join(body, "\n"), "\n")
		body = nil
		if current.Title == "" && strings.TrimSpace(raw) == "" {
			return
		}
		m := current
		m.Body = raw
		modules = append(modules, m)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isFence := strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
		if isFence {
			inFence = !inFence
		}
		if !inFence {
			if level, title, ok := heading(line); ok {
				flush()
				current = Module{Title: title, Level: level, Enabled: true}
				continue
			}
		}
		body = append(body, line)
	}
	flush()

	applyIDs(modules)
	return modules
}

// Heading reports a Markdown heading line: its level and its title. Fenced
// code is the caller's business; parse tracks it.
func Heading(line string) (int, string, bool) { return heading(line) }

func heading(line string) (int, string, bool) {
	s := strings.TrimLeft(line, " ")
	if len(line)-len(s) > 3 {
		return 0, "", false
	}
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n < 1 || n > 2 || n >= len(s) || s[n] != ' ' {
		return 0, "", false
	}
	title := strings.TrimSpace(s[n+1:])
	if title == "" {
		return 0, "", false
	}
	return n, title, true
}

func applyIDs(modules []Module) {
	seen := map[string]int{}
	for i := range modules {
		base := slug(modules[i].Title)
		if modules[i].Title == "" {
			base = "preamble"
		}
		seen[base]++
		id := base
		if seen[base] > 1 {
			id = base + "-" + itoa(seen[base])
		}
		modules[i].ID = id
		measure(&modules[i])
	}
}

func slug(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "module"
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func measure(m *Module) {
	m.Lines = LineCount(m.Body)
	m.Chars = len([]rune(m.Body))
	m.SourceHash = ModuleHash(*m)
}

// Compose writes the canonical document of all modules, enabled or not. A
// module's body sits directly under its heading, and one blank line separates
// modules, so a file written this way reads back unchanged.
func Compose(modules []Module) string {
	var b strings.Builder
	for _, m := range modules {
		body := strings.TrimSpace(m.Body)
		if m.Title == "" {
			if body == "" {
				continue
			}
			b.WriteString(body)
			b.WriteString("\n\n")
			continue
		}
		level := m.Level
		if level < 1 || level > 2 {
			level = 2
		}
		b.WriteString(strings.Repeat("#", level))
		b.WriteString(" ")
		b.WriteString(m.Title)
		b.WriteString("\n")
		if body != "" {
			b.WriteString(body)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	out := b.String()
	if out == "" {
		return ""
	}
	return strings.TrimRight(out, "\n") + "\n"
}

// ComposeEnabled writes the document of the enabled modules only.
func ComposeEnabled(modules []Module) string {
	return Compose(Enabled(modules))
}

// Effective composes the enabled modules as the model last rewrote them. It is
// the document agents read.
func Effective(modules []Module) string {
	return Compose(OrganizedModules(modules))
}

// OrganizedModules returns the modules switched on, each carrying the text of
// its latest rewrite in Body and its heading in Title.
func OrganizedModules(modules []Module) []Module {
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		if !m.Enabled {
			continue
		}
		kept := m
		kept.Body = Organized(m)
		kept.Title = OrganizedHeading(m)
		out = append(out, kept)
	}
	return out
}

// Organized returns a module's rewrite when it still belongs to the module's
// current text, and the author's own words otherwise.
func Organized(m Module) string {
	if strings.TrimSpace(m.Organized) != "" && m.OrganizedHash == m.SourceHash {
		return m.Organized
	}
	return strings.TrimSpace(m.Body)
}

// OrganizedHeading returns the module's heading as the model wrote it, falling
// back to the author's own when the rewrite is gone or stale.
func OrganizedHeading(m Module) string {
	if strings.TrimSpace(m.Organized) != "" && m.OrganizedHash == m.SourceHash &&
		strings.TrimSpace(m.OrganizedTitle) != "" {
		return strings.TrimSpace(m.OrganizedTitle)
	}
	return m.Title
}

// Stale reports whether a module needs the model again: it has a body, and no
// rewrite was made from that exact text, or the rewrite has no heading of its
// own yet.
func Stale(m Module) bool {
	if strings.TrimSpace(m.Body) == "" {
		return false
	}
	if strings.TrimSpace(m.Organized) == "" || m.OrganizedHash != m.SourceHash {
		return true
	}
	return strings.TrimSpace(m.Title) != "" && strings.TrimSpace(m.OrganizedTitle) == ""
}

// Enabled returns the modules that are switched on.
func Enabled(modules []Module) []Module {
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		if m.Enabled {
			out = append(out, m)
		}
	}
	return out
}

// Transform applies the deterministic half of the options: deduplication and
// filler removal. Translation is left to an AI. The modules are copied.
func Transform(modules []Module, o Options) []Module {
	out := make([]Module, len(modules))
	copy(out, modules)
	if o.Merge {
		out = mergeModules(out)
		for i := range out {
			out[i].Body = dedupeBody(out[i].Body)
		}
	}
	if o.Simplify {
		for i := range out {
			out[i].Body = simplifyBody(out[i].Body)
		}
		out = dropEmpty(out)
	}
	for i := range out {
		measure(&out[i])
	}
	return out
}

func mergeModules(modules []Module) []Module {
	out := make([]Module, 0, len(modules))
	index := map[string]int{}
	for _, m := range modules {
		key := strings.ToLower(strings.TrimSpace(m.Title))
		if key == "" {
			out = append(out, m)
			continue
		}
		if at, ok := index[key]; ok {
			a, b := strings.TrimSpace(out[at].Body), strings.TrimSpace(m.Body)
			switch {
			case a == "":
				out[at].Body = b
			case b != "":
				out[at].Body = a + "\n\n" + b
			}
			continue
		}
		index[key] = len(out)
		out = append(out, m)
	}
	return out
}

var bullet = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+`)

func dedupeBody(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	seen := map[string]bool{}
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if !inFence && bullet.MatchString(line) {
			key := strings.Join(strings.Fields(trimmed), " ")
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

var fillers = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)^(please note that|note that|it is important to note that|keep in mind that|remember that)\s+`), ""},
	{regexp.MustCompile(`(?i)^(please|kindly)\s+`), ""},
	{regexp.MustCompile(`需要注意的是[，,：:]\s*`), ""},
	{regexp.MustCompile(`需要指出的是[，,：:]\s*`), ""},
	{regexp.MustCompile(`请注意[，,：:]\s*`), ""},
	{regexp.MustCompile(`务必注意[，,：:]\s*`), ""},
	{regexp.MustCompile(`简单来说[，,：:]\s*`), ""},
	{regexp.MustCompile(`总的来说[，,：:]\s*`), ""},
	{regexp.MustCompile(`(?i)\bin order to\b`), "to"},
	{regexp.MustCompile(`(?i)\bbasically\b[,\s]*`), ""},
	{regexp.MustCompile(`(?i)\bsimply\b\s+`), ""},
}

func simplifyBody(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	blanks := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if inFence || strings.HasPrefix(trimmed, "#") {
			out = append(out, strings.TrimRight(line, " \t"))
			continue
		}
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			out = append(out, "")
			continue
		}
		blanks = 0
		prefix, text := splitBullet(line)
		for _, f := range fillers {
			text = f.re.ReplaceAllString(text, f.repl)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		out = append(out, prefix+capitalize(text))
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// splitBullet separates a list marker from its text, so a rewrite keeps the
// marker the author chose.
func splitBullet(line string) (string, string) {
	m := bullet.FindString(line)
	if m == "" {
		return "", strings.TrimSpace(line)
	}
	return line[:len(m)], line[len(m):]
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) > 0 && r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

func dropEmpty(modules []Module) []Module {
	out := modules[:0]
	for _, m := range modules {
		if m.Title == "" && strings.TrimSpace(m.Body) == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// BuildInstruction is the prompt sent to the model alongside the enabled
// modules. want is what the user typed as their expectation; the options are
// the app-wide defaults from settings.
func BuildInstruction(o Options, want string) string {
	var b strings.Builder
	b.WriteString("You maintain a global AGENTS.md instruction file for AI coding agents.\n")
	b.WriteString("Rewrite the file below and output only the complete revised Markdown: no commentary, no title of your own, no surrounding code fence.\n\n")
	b.WriteString("Always preserve:\n")
	b.WriteString("- Every actionable rule, constraint, preference and exception.\n")
	b.WriteString("- Every path, command, identifier, code span and URL exactly as written.\n")
	b.WriteString("- The heading levels and the order of sections. A heading's wording may change, its level and place may not.\n")
	b.WriteString("- The author's intent. Never invent rules, tools or facts.\n\n")
	if !o.Translate {
		b.WriteString("Keep the prose in its original language.\n")
	}
	b.WriteString("Transformations:\n")
	if o.Merge {
		b.WriteString("1. Merge duplicate or overlapping rules into one statement. Resolve contradictions by keeping the stricter, more specific rule and dropping the weaker one. Merge sections that serve the same purpose.\n")
	}
	if o.Simplify {
		b.WriteString("2. Maximize signal-to-noise: delete filler, repetition, meta-commentary and examples that teach nothing. State each rule once, as an imperative. Keep the file as short as it can be without losing a rule.\n")
	}
	if o.Translate {
		b.WriteString("3. Translate all human-readable prose into concise English, headings included. Leave code spans, commands, paths and identifiers unchanged.\n")
	}
	if strings.TrimSpace(want) != "" {
		b.WriteString("\nWhat the author asked for in this round, in their own words:\n")
		b.WriteString(strings.TrimSpace(want))
		b.WriteString("\n")
	}
	return b.String()
}

// ModuleHash is the hash of one module's own text. A stored rewrite carries
// the hash it was made from, so text that has not moved is never re-organized
// and never spends a model call.
func ModuleHash(m Module) string {
	return Hash(m.Title + "\x00" + itoa(m.Level) + "\x00" + strings.TrimSpace(m.Body))
}

// BuildModuleInstruction is BuildInstruction for a single section: the model
// answers with that section alone, so the app can keep each module's rewrite
// next to the text it came from. The heading is part of the answer, so a
// translated section is translated in its title too.
func BuildModuleInstruction(o Options, want string) string {
	var b strings.Builder
	b.WriteString(BuildInstruction(o, want))
	b.WriteString("\nScope: the document below is one section of the file. Answer with that one section only, including its heading line.\n")
	return b.String()
}

// Organize applies a model answer for one module: its heading becomes the
// module's published title and its body the published text. The author's own
// heading stays in Title, so the sidebar keeps their wording while AGENTS.md
// carries the translation. The hash of the text the answer was made from
// travels with it, so text that has not moved is never asked for again.
func Organize(m Module, answer string) Module {
	text := strings.TrimSpace(SanitizeOutput(answer))
	if text == "" {
		return m
	}
	title := ""
	if parsed := Parse(text); len(parsed) > 0 {
		parts := []string{parsed[0].Body}
		for _, rest := range parsed[1:] {
			parts = append(parts, strings.Repeat("#", rest.Level)+" "+rest.Title+"\n"+rest.Body)
		}
		title = parsed[0].Title
		text = strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	m.Organized = text
	if strings.TrimSpace(title) == "" {
		// The answer came back without a heading: keep the author's, so the
		// section is published rather than asked for again forever.
		title = m.Title
	}
	m.OrganizedTitle = title
	m.OrganizedHash = m.SourceHash
	return m
}

// SanitizeOutput cleans an AI answer into a Markdown document: it removes a
// fenced wrapper, and the sentence a chat model likes to put above it.
func SanitizeOutput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if body, ok := unwrapFence(s); ok {
		s = body
	}
	s = dropChatter(s)
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, "\n") + "\n"
}

// unwrapFence returns the body of the one fenced block when the text around it
// is commentary rather than a document.
func unwrapFence(s string) (string, bool) {
	lines := strings.Split(s, "\n")
	open, close := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, "~~~") {
			continue
		}
		if open < 0 {
			open = i
		} else {
			close = i
			break
		}
	}
	if open < 0 || close < 0 {
		return "", false
	}
	outside := 0
	for i, line := range lines {
		if i == open || i == close {
			continue
		}
		if i < open || i > close {
			if strings.TrimSpace(line) != "" {
				outside++
			}
		}
	}
	if outside > 3 {
		return "", false
	}
	return strings.TrimSpace(strings.Join(lines[open+1:close], "\n")), true
}

var chatter = []string{
	"here is", "here's", "below is", "following is", "revised", "rewritten",
	"updated version", "as requested", "sure,", "certainly",
	"以下是", "下面是", "这是", "如下", "已按要求", "修改后的",
}

// dropChatter removes a leading paragraph that announces the document rather
// than being part of it.
func dropChatter(s string) string {
	lines := strings.Split(s, "\n")
	first := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			first = i
			break
		}
	}
	if first < 0 {
		return ""
	}
	// The document itself starts at a heading, a list item or a fence.
	head := strings.TrimSpace(lines[first])
	if strings.HasPrefix(head, "#") || strings.HasPrefix(head, "-") ||
		strings.HasPrefix(head, "*") || strings.HasPrefix(head, "```") ||
		strings.HasPrefix(head, "~~~") {
		return strings.TrimSpace(s)
	}
	// A short paragraph that talks about the document, then a blank line and
	// the real document, is chatter.
	low := strings.ToLower(head)
	announces := strings.HasSuffix(head, ":") || strings.HasSuffix(head, "：")
	for _, word := range chatter {
		if strings.Contains(low, word) {
			announces = true
			break
		}
	}
	if !announces {
		return strings.TrimSpace(s)
	}
	end := first
	for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	for end < len(lines) && strings.TrimSpace(lines[end]) == "" {
		end++
	}
	if end >= len(lines) {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.Join(lines[end:], "\n"))
}

// LineCount counts the lines of a non-empty document.
func LineCount(text string) int {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

// Normalize is the form two documents are compared in: line endings and the
// trailing newline do not make one differ from the other.
func Normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, " \t\n")
}

// Hash is a short content hash used for cache and change detection.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
