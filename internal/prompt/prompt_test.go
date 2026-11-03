package prompt

import (
	"strings"
	"testing"
)

const sample = `Intro line.

## SOUL
- Be concise.

## Tool Usage
- ` + "`lcu`" + `: control apps.
- ` + "`lcu`" + `: control apps.

### Nested
Kept in the section body.

## Tailscale
- Port 443 is reserved.

` + "```sh" + `
# not a heading
echo hi
` + "```" + `
`

func TestParseSplitsAtTopHeadings(t *testing.T) {
	mods := Parse(sample)
	if len(mods) != 4 {
		t.Fatalf("got %d modules, want 4: %+v", len(mods), titles(mods))
	}
	if mods[0].Title != "" || !strings.Contains(mods[0].Body, "Intro line.") {
		t.Fatalf("preamble = %+v", mods[0])
	}
	if mods[1].Title != "SOUL" || mods[1].Level != 2 {
		t.Fatalf("second module = %+v", mods[1])
	}
	if !strings.Contains(mods[1].Body, "Be concise.") {
		t.Fatalf("body = %q", mods[1].Body)
	}
	if !strings.Contains(mods[3].Body, "# not a heading") {
		t.Fatalf("fence lost: %q", mods[3].Body)
	}
	if mods[3].ID != "tailscale" {
		t.Fatalf("id = %q", mods[3].ID)
	}
}

func TestComposeRoundTrips(t *testing.T) {
	mods := Parse(sample)
	again := Parse(Compose(mods))
	if len(again) != len(mods) {
		t.Fatalf("got %d modules, want %d", len(again), len(mods))
	}
	for i := range mods {
		if again[i].Title != mods[i].Title {
			t.Fatalf("module %d title %q, want %q", i, again[i].Title, mods[i].Title)
		}
	}
}

// The editor shows the parsed file back; formatting the writer chose must not
// drift every time the app opens.
func TestComposeIsStable(t *testing.T) {
	doc := "## A\n- one\n- two\n\n## B\n\ntext\n"
	once := Compose(Parse(doc))
	if twice := Compose(Parse(once)); twice != once {
		t.Fatalf("Compose drifted:\nfirst  %q\nsecond %q", once, twice)
	}
	if !strings.Contains(once, "## B") || !strings.Contains(once, "text") {
		t.Fatalf("content lost: %q", once)
	}
}

func TestComposeEnabledSkipsDisabled(t *testing.T) {
	mods := Parse(sample)
	mods[1].Enabled = false
	out := ComposeEnabled(mods)
	if strings.Contains(out, "Be concise.") {
		t.Fatalf("disabled module leaked: %s", out)
	}
	if !strings.Contains(out, "## Tailscale") {
		t.Fatalf("enabled module missing: %s", out)
	}
}

func TestMergeDedupesBulletsAndSections(t *testing.T) {
	mods := Parse("## A\n- one\n- one\n- two\n\n## A\n- three\n")
	out := Transform(mods, Options{Merge: true})
	if len(out) != 1 {
		t.Fatalf("got %d modules, want 1", len(out))
	}
	body := out[0].Body
	if strings.Count(body, "- one") != 1 {
		t.Fatalf("duplicate bullet kept: %q", body)
	}
	for _, want := range []string{"- two", "- three"} {
		if !strings.Contains(body, want) {
			t.Fatalf("%q missing from %q", want, body)
		}
	}
}

func TestSimplifyDropsFiller(t *testing.T) {
	mods := Parse("## Rules\n- Please note that in order to ship, run the tests.\n- 需要注意的是，提交前要跑测试。\n")
	out := Transform(mods, Options{Simplify: true})
	body := out[0].Body
	if strings.Contains(strings.ToLower(body), "please note") || strings.Contains(body, "in order to") {
		t.Fatalf("filler kept: %q", body)
	}
	if strings.Contains(body, "需要注意的是") {
		t.Fatalf("chinese filler kept: %q", body)
	}
	if !strings.Contains(body, "run the tests.") || !strings.Contains(body, "提交前要跑测试") {
		t.Fatalf("rule lost: %q", body)
	}
}

func TestSanitizeOutputStripsFence(t *testing.T) {
	got := SanitizeOutput("Here you go:\n```markdown\n## A\n\n- x\n```\n")
	if strings.HasPrefix(got, "Here") {
		t.Fatalf("commentary kept: %q", got)
	}
	if !strings.Contains(got, "## A") {
		t.Fatalf("content lost: %q", got)
	}
}

// A rewrite carries the model's heading, so a Chinese section publishes an
// English one while the author's own heading stays put for the sidebar.
func TestOrganizeKeepsTranslatedHeading(t *testing.T) {
	m := Parse("## 网络环境\n- 公司使用对称\n")[0]
	got := Organize(m, "## Network\n- The office uses symmetric networking.\n")

	if got.Title != "网络环境" {
		t.Fatalf("author heading changed: %q", got.Title)
	}
	if got.OrganizedTitle != "Network" {
		t.Fatalf("translated heading lost: %q", got.OrganizedTitle)
	}
	if strings.Contains(got.Organized, "网络环境") || strings.Contains(got.Organized, "## ") {
		t.Fatalf("heading kept in the body: %q", got.Organized)
	}
	if Stale(got) {
		t.Fatal("fresh rewrite reported as stale")
	}

	doc := Effective([]Module{got})
	if !strings.Contains(doc, "## Network") || strings.Contains(doc, "网络环境") {
		t.Fatalf("published document = %q", doc)
	}
}

// A rewrite from before titles were translated has no heading of its own, so
// it is asked for once more rather than publishing a heading nobody chose.
func TestRewriteWithoutHeadingIsStale(t *testing.T) {
	m := Parse("## Tools\n- lcu\n")[0]
	m.Organized = "- lcu\n"
	m.OrganizedHash = m.SourceHash
	if !Stale(m) {
		t.Fatal("heading-less rewrite reported as fresh")
	}
	if got := OrganizedHeading(m); got != "Tools" {
		t.Fatalf("heading = %q", got)
	}
}

func TestBuildInstructionNamesSelectedOptions(t *testing.T) {
	all := BuildInstruction(Options{Translate: true, Simplify: true, Merge: true}, "")
	for _, want := range []string{"duplicate", "signal-to-noise", "English"} {
		if !strings.Contains(all, want) {
			t.Fatalf("%q missing", want)
		}
	}
	none := BuildInstruction(Options{}, "")
	for _, unwanted := range []string{"duplicate", "signal-to-noise", "English"} {
		if strings.Contains(none, unwanted) {
			t.Fatalf("%q should not appear", unwanted)
		}
	}
}

func TestBuildInstructionIncludesExpectation(t *testing.T) {
	got := BuildInstruction(Options{}, "把 Tailscale 的规则合并成一条")
	if !strings.Contains(got, "把 Tailscale 的规则合并成一条") {
		t.Fatalf("expectation missing: %q", got)
	}
}

func titles(mods []Module) []string {
	out := make([]string, len(mods))
	for i, m := range mods {
		out[i] = m.Title
	}
	return out
}
