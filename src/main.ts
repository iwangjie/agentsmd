// main.ts draws the app. The author writes plain AGENTS.md on the left; the app
// splits it into modules and keeps the compiled document on the right current
// on its own. Nothing about models, prompts or transforms belongs on this
// screen: a file that is verbose and unstructured is the expected input.
import { Agents, events, type Module, type Snapshot } from "./mygo";
import { highlightMarkdown, renderMarkdown, renderMarked } from "./render";

const $ = <T extends Element>(selector: string) => document.querySelector<T>(selector)!;

const icons = {
  doc: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M4 1.8h5l3 3v9.4H4z"/><path d="M9 1.8v3h3"/></svg>`,
  layers: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2 1.9 5.2 8 8.4l6.1-3.2z"/><path d="M2 9.1 8 12.3l6-3.2"/></svg>`,
  refresh: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M13.2 8a5.2 5.2 0 1 1-1.6-3.7"/><path d="M13.4 2.6v3.2h-3.2"/></svg>`,
  check: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 8.4 6.2 11.6 13 4.6"/></svg>`,
  clock: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="5.8"/><path d="M8 4.8V8l2.2 1.4"/></svg>`,
  gear: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="2"/><path d="M8 1.8v1.4M8 12.8v1.4M2.3 8h1.4M12.3 8h1.4M4 4l1 1M11 11l1 1M12 4l-1 1M5 11l-1 1"/></svg>`,
  alert: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="6"/><path d="M8 4.8v3.6M8 11h.01"/></svg>`,
  spark: `<svg viewBox="0 0 16 16" fill="currentColor"><path d="M8 1.6l1.5 4.1L13.6 7l-4.1 1.4L8 12.6 6.5 8.4 2.4 7l4.1-1.3z"/></svg>`,
  pencil: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M11.2 2.4 13.6 4.8 5.8 12.6 2.6 13.4 3.4 10.2z"/></svg>`,
  trash: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M2.8 4.4h10.4M6.4 4.4V2.8h3.2v1.6M4.2 4.4l.7 8.8h6.2l.7-8.8"/></svg>`,
  lock: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><rect x="3.4" y="7" width="9.2" height="6.2" rx="1.4"/><path d="M5.6 7V5.2a2.4 2.4 0 0 1 4.8 0V7"/></svg>`,
};

type Pane = "both" | "raw" | "final";

interface UiState {
  snapshot: Snapshot | null;
  selected: string; // "" means the whole document
  pane: Pane;
  draft: string; // the raw text as typed, ahead of the debounced save
  draftTimer: number | null;
  diskChanged: boolean;
  scrollTo: string;
  settings: boolean;
  compiling: boolean;
  compileTimer: number | null;
}

const state: UiState = {
  snapshot: null,
  selected: "",
  pane: "both",
  draft: "",
  draftTimer: null,
  diskChanged: false,
  scrollTo: "",
  settings: false,
  compiling: false,
  compileTimer: null,
};

/** leftPane is the whole source file, always editable: a module is a span in
 * it, so picking one paints and scrolls to the lines it holds rather than
 * replacing the text you are writing. */
function leftPane(snap: Snapshot): string {
  const selected = snap.modules.find((m) => m.id === state.selected);
  const lines = highlightMarkdown(state.draft);
  const painted = selected ? paintModule(lines, state.draft, selected) : lines;
  return `<div class="panel-head">
      <span class="panel-title">源文件</span>
      <span class="badge">${snap.rawLines} 行</span>
      <span class="grow"></span>
      <span class="panel-sub">随时可改，永远保留</span>
      <button class="linkish" id="show-raw" title="${state.pane === "raw" ? "并排显示" : "只看源文件"}">${
        state.pane === "raw" ? "并排" : "放大"
      }</button>
    </div>
    <div class="code-body">
      <div class="editor-wrap">
        <div class="sel-band" id="sel-band" hidden></div>
        <pre class="highlight" id="highlight" aria-hidden="true">${painted.join("\n")}</pre>
        <textarea class="editor" id="editor" spellcheck="false" placeholder="把你的全局规则都写在这里，可以很啰嗦。用 ## 分节，右边会自动整理。">${esc(
          state.draft,
        )}</textarea>
      </div>
    </div>`;
}

/** paintModule marks the lines one module occupies, so the chosen section is
 * visible in the middle of the file it belongs to. The band is painted by the
 * editor wrapper, which knows the line height; the span here only marks the
 * text that belongs to the module. */
function paintModule(lines: string[], text: string, m: Module): string[] {
  const [start, end] = moduleRange(text, m);
  if (start >= end) return lines;
  return [
    ...lines.slice(0, start),
    `<span class="module-sel">${lines.slice(start, end).join("\n")}</span>`,
    ...lines.slice(end),
  ];
}

/** moduleRange is where a module sits in the source: its heading line up to
 * the next heading of the same or a higher level. */
function moduleRange(text: string, m: Module): [number, number] {
  const lines = text.split("\n");
  if (!m.title) return [0, lines.length];
  const heading = "#".repeat(m.level) + " " + m.title;
  const start = lines.findIndex((line) => line.trim() === heading);
  if (start < 0) return [0, lines.length];
  for (let i = start + 1; i < lines.length; i++) {
    const found = /^(#{1,2})\s+\S/.exec(lines[i]!.trim());
    if (found && found[1]!.length <= m.level) return [start, i];
  }
  return [start, lines.length];
}

/** finalPane is the document agents read, always in full. Picking a module in
 * the sidebar marks its words in the source on the left and marks the same
 * section in the result on the right, so the two panes describe one thing at
 * one place instead of showing different documents.
 *
 * A rewrite runs while the author types, and it is nobody's emergency: no
 * overlay, no spinner over the text. The pane keeps showing the last finished
 * rewrite and only a quiet dot in the header says another one is on the way. */
function finalPane(snap: Snapshot): string {
  const selected = snap.modules.find((m) => m.id === state.selected);
  const text = snap.effective;
  const fresh = selected ? !stale(selected) : snap.compiled;
  const compiling = state.compiling || snap.compiling;
  const picked = selected ? organizedTitle(selected).toLowerCase() : "";
  const body =
    text.trim() === ""
      ? `<div class="empty">左边写下内容后，这里会自动整理。</div>`
      : picked
        ? renderMarked(text, (heading) => heading.trim().toLowerCase() === picked)
        : renderMarkdown(text);

  return `<div class="panel-head">
      <span class="panel-title">最终结果</span>
      ${
        compiling && (!selected || stale(selected))
          ? `<span class="badge quiet" id="final-badge" title="停手后会自动更新"><span class="spin tiny"></span>整理中</span>`
          : fresh
            ? `<span class="badge accent" id="final-badge" title="这份整理结果对应的原文没有变化">已是最新</span>`
            : selected
              ? `<span class="badge warn" id="final-badge" title="原文改过了，稍后自动更新">待整理</span>`
              : snap.dirty
                ? `<span class="badge warn" id="final-badge">待写入</span>`
                : `<span class="badge" id="final-badge">原文</span>`
      }
      <span class="grow"></span>
      <span class="panel-sub">${fmtNumber(snap.chars)} 字</span>
      <button class="linkish" id="show-final" title="${state.pane === "final" ? "并排显示" : "只看结果"}">${
        state.pane === "final" ? "并排" : "放大"
      }</button>
    </div>
    <div class="code-body">
      <div class="read preview markdown-body" id="final-body" tabindex="0" role="document" aria-readonly="true">${body}</div>
    </div>
  </div>`;
}

/** organizedTitle is the module's heading as it appears in AGENTS.md. The
 * sidebar keeps the author's own wording; the document carries this one. It is
 * the key the result pane marks the chosen section by. */
function organizedTitle(m: Module): string {
  return m.organized && m.organizedHash === m.sourceHash ? m.organizedTitle || m.title : m.title;
}

/** stale reports whether a module's rewrite no longer matches its text, or has
 * never had its heading translated. */
function stale(m: Module): boolean {
  if (m.body.trim() === "") return false;
  if (!m.organized || m.organizedHash !== m.sourceHash) return true;
  return m.title.trim() !== "" && m.organizedTitle === "";
}

function esc(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function fmtNumber(n: number): string {
  if (n >= 100000) return Math.round(n / 1000) + "k";
  if (n >= 1000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function percentChange(from: number, to: number): string {
  if (!from) return to ? "+100%" : "—";
  const pct = Math.round(((to - from) / from) * 100);
  return pct === 0 ? "—" : (pct > 0 ? "+" : "") + pct + "%";
}

function timeAgo(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const min = Math.round(diff / 60000);
  if (min < 1) return "刚刚";
  if (min < 60) return `${min} 分钟前`;
  const h = Math.round(min / 60);
  if (h < 24) return `${h} 小时前`;
  return `${Math.round(h / 24)} 天前`;
}

/* ---------- shell ---------- */

function shell(snap: Snapshot): void {
  $("#app").innerHTML = `
  <div class="app">
    ${titlebar(snap)}

    <div class="workspace">
      <aside class="sidebar" id="sidebar">
        ${sidebar(snap)}
      </aside>

      <main class="stage">
        <div class="split ${state.pane === "raw" ? "focus-raw" : state.pane === "final" ? "focus-final" : ""}">
          <section class="panel raw-panel" id="raw-panel">
            ${leftPane(snap)}
          </section>

          <section class="panel final-panel" id="final-panel">
            ${finalPane(snap)}
          </section>
        </div>

        ${banner(snap)}
      </main>
    </div>

    ${statusbar(snap)}
  </div>
  <div id="modal-root"></div>`;

  // The status bar belongs to the app grid's last row. It is moved into place
  // by hand because a trailing block element in the template would otherwise
  // be parsed as a sibling of .app, leaving the grid's last row empty.
  const app = document.querySelector(".app");
  const bar = document.getElementById("statusbar");
  if (app && bar && bar.parentElement !== app) app.append(bar);

  if (state.settings) showSettings(snap);
}

/** refresh paints everything outside the editor. It is what runs while the
 * author types: the editor keeps its DOM, its selection and its composition,
 * and the rest of the page follows along. */
function refresh(snap: Snapshot): void {
  const sidebarEl = document.getElementById("sidebar");
  if (sidebarEl) sidebarEl.innerHTML = sidebar(snap);

  const finalEl = document.getElementById("final-panel");
  if (finalEl) finalEl.innerHTML = finalPane(snap);

  const stage = document.querySelector(".stage");
  const oldBanner = stage?.querySelector(".banner");
  const nextBanner = banner(snap);
  if (stage) {
    if (oldBanner) oldBanner.remove();
    if (nextBanner) stage.insertAdjacentHTML("beforeend", nextBanner);
  }

  const statusEl = document.getElementById("statusbar");
  if (statusEl) statusEl.innerHTML = statusInner(snap);

  wire();
  placeBand();
}

function titlebar(snap: Snapshot): string {
  const modelName = snap.models.find((m) => m.id === snap.model)?.name ?? snap.model;
  void modelName;
  return `<header class="titlebar">
      <div class="brand">
        <span class="brand-mark">
          <svg viewBox="0 0 16 16" fill="none">
            <path d="M2.6 4.2h10.8M2.6 8h7.2M2.6 11.8h4.8" stroke="#fff" stroke-width="1.7" stroke-linecap="round"/>
            <circle cx="13" cy="11.8" r="1.5" fill="#ff5c76"/>
          </svg>
        </span>
        <span class="brand-name">AgentsMD</span>
      </div>
      <button class="path" id="reveal" title="在访达中显示 AGENTS.md">
        ${icons.doc}<span>${esc(snap.displayPath)}</span>
      </button>
      <span class="titlebar-spacer"></span>
      <div class="toolbar">
        <button class="btn ghost icon-only" id="reload" title="重新读取磁盘（⌘R）">${icons.refresh}</button>
        <button class="btn ghost icon-only" id="backups" title="历史备份">${icons.clock}</button>
        <button class="btn ghost icon-only" id="settings" title="设置（⌘,）">${icons.gear}</button>
        <span class="autosave" title="每次改动都会自动写入 AGENTS.md">
          <span class="dot ${snap.dirty ? "pending" : "on"}"></span>${snap.dirty ? "同步中" : "已写入"}
        </span>
      </div>
    </header>`;
}

function sidebar(snap: Snapshot): string {
  return `<div class="side-head">
          <span class="side-title">模块</span>
          <span class="side-count">${snap.enabled}/${snap.total}</span>
          <span class="grow"></span>
          <button class="linkish" id="all-on" title="全部启用">全开</button>
          <button class="linkish" id="all-off" title="全部关闭">全关</button>
          <button class="linkish add-module" id="add-module" title="新增模块">＋</button>
        </div>

        <div class="module-list">
          <button class="module all ${state.selected === "" ? "active" : ""}" data-all="1">
            <span class="module-icon">${icons.layers}</span>
            <span class="module-name">最终结果</span>
          </button>
          <div class="side-rule"></div>
          ${
            snap.modules.length === 0
              ? `<div class="side-empty">在左边写下内容，<br>这里会自动出现模块。</div>`
              : snap.modules
                  .map(
                    (m) => `
            <div class="module ${m.enabled ? "" : "off"} ${
              state.selected === m.id ? "active" : ""
            }" data-id="${esc(m.id)}">
              <span class="module-icon">${icons.doc}</span>
              <span class="module-name" title="${esc(m.title || "前言")}">${esc(m.title || "前言")}</span>
              <span class="module-actions">
                <button class="row-act" data-rename="${esc(m.id)}" title="重命名">${icons.pencil}</button>
                <button class="row-act" data-delete="${esc(m.id)}" title="删除">${icons.trash}</button>
              </span>
              <button class="switch ${m.enabled ? "on" : ""}" data-toggle="${esc(m.id)}" role="switch" aria-checked="${
                m.enabled
              }" title="${m.enabled ? "关闭后不写入 AGENTS.md" : "启用后写入 AGENTS.md"}"></button>
            </div>`,
                  )
                  .join("")
          }
        </div>`;
}

function banner(snap: Snapshot): string {
  const gatewayOk = snap.gateway.reachable;
  if (!gatewayOk) {
    return `<div class="banner warn">${icons.alert}<span>模型服务未连接（${esc(
      snap.gateway.baseURL,
    )}），现在只按原文整理。<button class="linkish" id="retry-gateway">重试</button></span></div>`;
  }
  if (snap.compileNote) {
    return `<div class="banner warn">${icons.alert}<span>上次整理没有完成：${esc(
      snap.compileNote,
    )}<button class="linkish" id="retry-compile">重试</button></span></div>`;
  }
  return "";
}

function statusbar(snap: Snapshot): string {
  return `<footer class="statusbar" id="statusbar">${statusInner(snap)}</footer>`;
}

function statusInner(snap: Snapshot): string {
  const gatewayOk = snap.gateway.reachable;
  const modelName = snap.models.find((m) => m.id === snap.model)?.name ?? snap.model;
  return `
      ${
        snap.message
          ? `<span class="status-msg ${esc(snap.messageKind)}"><span class="dot"></span>${esc(snap.message)}</span>`
          : state.diskChanged
            ? `<span class="status-msg warning"><span class="dot"></span>AGENTS.md 被其他程序修改<button class="linkish" id="status-reload">重新读取</button></span>`
            : `<span class="status-msg"><span class="dot"></span>${snap.dirty ? "尚未写入 AGENTS.md" : "已与磁盘同步"}</span>`
      }
      <span class="status-spacer"></span>
      <span class="status-stat">${snap.diskLines} → ${snap.resultLines} 行</span>
      <span class="status-stat">${fmtNumber(snap.diskChars)} → ${fmtNumber(snap.chars)} 字</span>
      <span class="status-stat ${
        snap.chars < snap.diskChars ? "good" : ""
      }">${percentChange(snap.diskChars, snap.chars)}</span>
      <span class="status-sep"></span>
      <span class="status-item ${gatewayOk ? "" : "offline"}" title="${esc(modelName || "")}">
        <span class="dot ${gatewayOk ? "on" : ""}"></span>${gatewayOk ? "自动整理中" : "离线整理"}
      </span>`;
}

/* ---------- data flow ---------- */

function apply(snap: Snapshot, opts: { replaceDraft?: boolean } = {}): void {
  state.snapshot = snap;
  state.compiling = snap.compiling;

  const editor = document.getElementById("editor") as HTMLTextAreaElement | null;
  const typing = editor !== null && document.activeElement === editor;
  if (!typing || opts.replaceDraft) state.draft = snap.raw;

  if (state.selected && !snap.modules.some((m) => m.id === state.selected)) {
    state.selected = "";
  }

  if (typing && !opts.replaceDraft) {
    refresh(snap);
    return;
  }
  render();
}

/** render repaints the page. While the author is typing it repaints around
 * the editor instead of over it: replacing a textarea mid-composition makes
 * WebKit commit the half-typed word, which is how Chinese input turns into
 * stray newlines. */
function render(): void {
  const snap = state.snapshot;
  if (!snap) return;
  const editor = document.getElementById("editor") as HTMLTextAreaElement | null;
  const typing = document.activeElement === editor && editor !== null;

  if (typing) {
    state.draft = editor.value;
    refresh(snap);
    return;
  }

  shell(snap);
  wire();

  if (state.scrollTo) {
    const id = state.scrollTo;
    state.scrollTo = "";
    requestAnimationFrame(() => revealModule(id));
  } else {
    placeBand();
    revealPick();
  }
  requestAnimationFrame(placeBand);
}

/** revealPick brings the marked section of the result into view. The source
 * pane scrolls to the author's words and the result pane scrolls to the
 * rewrite of those same words, so both sides show the same section. */
function revealPick(): void {
  const body = document.getElementById("final-body");
  const pick = body?.querySelector<HTMLElement>(".module-pick");
  if (!body || !pick) return;
  const top = pick.offsetTop - body.clientHeight / 3;
  body.scrollTop = Math.max(0, top);
}

/** revealModule scrolls the source pane to a module's heading, measured on the
 * highlighted copy so wrapped lines land in the right place. It reads the
 * draft, which is what the editor currently shows. */
function revealModule(id: string): void {
  const snap = state.snapshot;
  const editor = document.getElementById("editor") as HTMLTextAreaElement | null;
  const highlight = document.getElementById("highlight");
  const module = snap?.modules.find((m) => m.id === id);
  if (!editor || !highlight || !module || !snap) return;
  if (!module.title) {
    highlight.scrollTop = 0;
    editor.scrollTop = 0;
    return;
  }

  const [start] = moduleRange(state.draft, module);
  const offset = state.draft.split("\n").slice(0, start).join("\n").length + (start > 0 ? 1 : 0);

  const walker = document.createTreeWalker(highlight, NodeFilter.SHOW_TEXT);
  let seen = 0;
  let node: Node | null = walker.nextNode();
  while (node) {
    const len = node.textContent?.length ?? 0;
    if (seen + len > offset) break;
    seen += len;
    node = walker.nextNode();
  }
  if (!node) {
    placeBand();
    return;
  }

  const range = document.createRange();
  const at = Math.min(offset - seen, node.textContent?.length ?? 0);
  range.setStart(node, at);
  range.setEnd(node, at);
  const target = range.getBoundingClientRect().top - highlight.getBoundingClientRect().top;
  const top = Math.max(0, target - 14);
  highlight.scrollTop = top;
  editor.scrollTop = top;
  placeBand();
}

/** placeBand parks the selection band behind the chosen module's lines. It is
 * measured from the marked span itself, so wrapped lines and scrolling move the
 * band with the text it belongs to. */
function placeBand(): void {
  const band = document.getElementById("sel-band");
  const mark = document.querySelector<HTMLElement>(".module-sel");
  const highlight = document.getElementById("highlight");
  if (!band || !highlight) return;
  if (!mark) {
    band.hidden = true;
    return;
  }
  const line = lineHeight(highlight);
  const top = mark.getBoundingClientRect().top - highlight.getBoundingClientRect().top;
  const height = mark.getBoundingClientRect().height;
  band.hidden = false;
  band.style.top = `${top}px`;
  band.style.height = `${Math.max(line, height)}px`;
}

function lineHeight(el: HTMLElement): number {
  const value = parseFloat(getComputedStyle(el).lineHeight);
  return Number.isFinite(value) ? value : 20;
}

/** queueSave saves the raw text shortly after typing stops. */
function queueSave(): void {
  if (state.draftTimer !== null) clearTimeout(state.draftTimer);
  state.draftTimer = window.setTimeout(async () => {
    state.draftTimer = null;
    if (!state.snapshot || state.draft === state.snapshot.raw) return;
    apply(await Agents.setRaw(state.draft));
  }, 220);
}

/** queueCompile asks for a fresh compilation once typing settles. Only one
 * rewrite is ever in flight: a keystroke cancels the wait, and a compile that
 * finishes while more typing happened is followed by a single new wait. The
 * author never waits for the model with their cursor. */
function queueCompile(delay = 1300): void {
  if (state.compileTimer !== null) clearTimeout(state.compileTimer);
  state.compileTimer = window.setTimeout(() => {
    state.compileTimer = null;
    void compile();
  }, delay);
}

async function compile(): Promise<void> {
  if (!state.snapshot || state.compiling) return;
  state.compiling = true;
  setCompileBadge();
  try {
    apply(await Agents.compile());
  } catch (err) {
    state.compiling = false;
    apply(await Agents.snapshot());
    toast(String((err as Error)?.message ?? err), "error");
  }
}

/** setCompileBadge updates only the badge, so a rewrite under way never
 * repaints the editor or moves the cursor. */
function setCompileBadge(): void {
  const snap = state.snapshot;
  if (!snap) return;
  const selected = snap.modules.find((m) => m.id === state.selected);
  const badge = document.getElementById("final-badge");
  if (!badge) return;
  const fresh = selected ? !stale(selected) : snap.compiled;
  if (state.compiling && (!selected || stale(selected))) {
    badge.className = "badge quiet";
    badge.innerHTML = `<span class="spin tiny"></span>整理中`;
  } else if (fresh) {
    badge.className = "badge accent";
    badge.textContent = "已是最新";
  } else {
    badge.className = selected ? "badge warn" : "badge";
    badge.textContent = selected ? "待整理" : snap.dirty ? "待写入" : "原文";
  }
}

/** wire installs the page's listeners once. Everything is delegated from a
 * stable root, so the editor can be repainted around while a rewrite lands
 * without stacking a second copy of every handler. */
let wired = false;
function wire(): void {
  bindEditor();
  if (wired) return;
  wired = true;

  document.addEventListener("click", async (e) => {
    const target = e.target as Element | null;
    if (!target) return;
    const route = (selector: string) => target.closest<HTMLElement>(selector);

    if (route("#reveal")) return void Agents.reveal();
    if (route("#backups")) return void showBackups();
    if (route("#reload")) return void reload();
    if (route("#settings")) {
      state.settings = !state.settings;
      if (state.settings && state.snapshot) showSettings(state.snapshot);
      else closeModal();
      return;
    }
    if (route("#add-module")) {
      return promptName("新模块", async (name) => {
        if (name) apply(await Agents.addModule(name));
      });
    }
    if (route("#all-on")) {
      apply(await Agents.setAll(true));
      queueCompile(400);
      return;
    }
    if (route("#all-off")) {
      apply(await Agents.setAll(false));
      queueCompile(400);
      return;
    }
    if (route("#show-raw")) {
      state.pane = state.pane === "raw" ? "both" : "raw";
      render();
      return;
    }
    if (route("#show-final")) {
      state.pane = state.pane === "final" ? "both" : "final";
      render();
      return;
    }
    if (route("#retry-gateway")) {
      apply(await Agents.refreshGateway());
      queueCompile(400);
      return;
    }
    if (route("#retry-compile")) return void compile();
    if (route("#status-reload")) return void reload();

    const toggle = route("[data-toggle]");
    if (toggle) {
      e.stopPropagation();
      apply(await Agents.toggleModule(toggle.dataset.toggle!));
      queueCompile(400);
      return;
    }
    const renameEl = route("[data-rename]");
    if (renameEl) {
      e.stopPropagation();
      const module = state.snapshot?.modules.find((m) => m.id === renameEl.dataset.rename);
      promptName(module?.title ?? "", async (name) => {
        if (name) apply(await Agents.renameModule(renameEl.dataset.rename!, name));
      });
      return;
    }
    const deleteEl = route("[data-delete]");
    if (deleteEl) {
      e.stopPropagation();
      const module = state.snapshot?.modules.find((m) => m.id === deleteEl.dataset.delete);
      const ok = await confirmAction("删除模块？", `「${module?.title || "前言"}」会从你的原文里删掉，AGENTS.md 同步更新。`);
      if (ok) {
        if (state.selected === deleteEl.dataset.delete) state.selected = "";
        apply(await Agents.deleteModule(deleteEl.dataset.delete!));
      }
      return;
    }
    if (route("[data-all]")) {
      state.selected = "";
      render();
      return;
    }
    const row = route("[data-id]");
    if (row) {
      state.selected = row.dataset.id!;
      state.scrollTo = row.dataset.id!;
      if (state.pane === "final") state.pane = "both";
      render();
    }
  });

  bindShortcuts();
}

/** bindEditor attaches the editor's own listeners, once per textarea. The
 * editor element itself survives repaints while the author types. */
function bindEditor(): void {
  const editor = document.getElementById("editor") as HTMLTextAreaElement | null;
  if (!editor || editor.dataset.wired === "1") return;
  editor.dataset.wired = "1";
  const highlight = () => document.getElementById("highlight");

  editor.addEventListener("input", (e) => {
    state.draft = editor.value;
    if ((e as InputEvent).isComposing) return;
    const hl = highlight();
    if (hl) hl.innerHTML = highlightMarkdown(editor.value).join("\n");
    queueSave();
    queueCompile(1500);
  });
  // A composition ends the moment the author commits a word, and that commit
  // is when the text is final: highlight and schedule from here, never from
  // the half-composed keystrokes.
  editor.addEventListener("compositionend", () => {
    state.draft = editor.value;
    const hl = highlight();
    if (hl) hl.innerHTML = highlightMarkdown(editor.value).join("\n");
    queueSave();
    queueCompile(1500);
  });
  editor.addEventListener("scroll", () => {
    const hl = highlight();
    if (hl) hl.scrollTop = editor.scrollTop;
    placeBand();
  });
  editor.addEventListener("keydown", (e) => {
    if (e.key !== "Tab") return;
    e.preventDefault();
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    editor.setRangeText("  ", start, end, "end");
    editor.dispatchEvent(new Event("input"));
  });
}

/** flush saves the raw text and writes AGENTS.md, for ⌘S and on quit. */
async function flush(): Promise<void> {
  if (!state.snapshot) return;
  if (state.draftTimer !== null) {
    clearTimeout(state.draftTimer);
    state.draftTimer = null;
  }
  if (state.draft !== state.snapshot.raw) {
    apply(await Agents.setRaw(state.draft));
  }
  apply(await Agents.apply());
}

async function reload(): Promise<void> {
  if (state.snapshot?.dirty && !(await confirmDiscard())) return;
  state.diskChanged = false;
  apply(await Agents.reload(), { replaceDraft: true });
}

function closeModal(): void {
  const root = document.getElementById("modal-root");
  if (root) root.innerHTML = "";
}

function toast(text: string, kind: "info" | "success" | "warning" | "error"): void {
  const bar = document.querySelector<HTMLElement>(".statusbar");
  if (!bar) return;
  const el = document.createElement("span");
  el.className = `status-msg ${kind}`;
  el.innerHTML = `<span class="dot"></span>${esc(text)}`;
  bar.prepend(el);
  setTimeout(() => el.remove(), 7000);
}

function showSettings(snap: Snapshot): void {
  const root = document.getElementById("modal-root");
  if (!root) return;
  const gatewayOk = snap.gateway.reachable;
  root.innerHTML = `
    <div class="modal-backdrop" data-backdrop>
      <div class="modal">
        <div class="modal-head">设置</div>
        <div class="modal-body">
          <div class="setting-group">整理模型</div>
          <div class="setting-row">
            <span class="setting-label">服务</span>
            <code class="setting-value">${esc(snap.gateway.baseURL)}</code>
          </div>
          <div class="setting-row">
            <span class="setting-label">状态</span>
            <span class="setting-value ${gatewayOk ? "ok" : "warn"}">
              ${gatewayOk ? `已连接 · ${snap.models.length} 个模型` : esc(snap.gateway.detail || "未连接")}
            </span>
          </div>
          <div class="setting-row">
            <span class="setting-label">模型</span>
            ${
              gatewayOk && snap.models.length
                ? `<select class="select" id="model">
                     ${snap.models
                       .map(
                         (m) =>
                           `<option value="${esc(m.id)}" ${m.id === snap.model ? "selected" : ""}>${esc(m.name)}</option>`,
                       )
                       .join("")}
                   </select>`
                : `—`
            }
          </div>

          <div class="setting-group">整理方式</div>
          ${settingToggle("translate", "翻译为英文", "所有规则译成英文，命令、路径与标识符保持不变", snap.options.translate)}
          ${settingToggle("simplify", "高信噪比简化", "删掉铺垫与重复，每条规则只说一次", snap.options.simplify)}
          ${settingToggle("merge", "合并重复与错误", "重复条目合一，冲突时保留更严格的一条", snap.options.merge)}
          <div class="setting-note">这些都留空也可以：模型会按你的原文整理出结构清晰的模块，绝不改写你的原文文件。</div>

          <div class="setting-group">文件</div>
          <div class="setting-row">
            <span class="setting-label">原文</span>
            <code class="setting-value">AGENTS.raw.md</code>
          </div>
          <div class="setting-row">
            <span class="setting-label">生效</span>
            <code class="setting-value">AGENTS.md</code>
          </div>
          <div class="setting-note">原文永远保留；AGENTS.md 是整理后被 agent 读取的版本，每次写入前都会自动备份。</div>
        </div>
        <div class="modal-foot">
          <button class="btn" data-folder>备份文件夹</button>
          <button class="btn primary" data-cancel>完成</button>
        </div>
      </div>
    </div>`;

  const close = () => {
    state.settings = false;
    closeModal();
  };
  root.querySelector("[data-cancel]")?.addEventListener("click", close);
  root.querySelector("[data-folder]")?.addEventListener("click", () => void Agents.revealBackups());
  root.querySelector("[data-backdrop]")?.addEventListener("click", (e) => {
    if (e.target === e.currentTarget) close();
  });
  (root.querySelector("#model") as HTMLSelectElement | null)?.addEventListener("change", async (e) => {
    const next = await Agents.setModel((e.target as HTMLSelectElement).value);
    apply(next);
    showSettings(next);
    queueCompile(400);
  });
  root.querySelectorAll<HTMLElement>("[data-setting]").forEach((el) => {
    el.addEventListener("click", async () => {
      const key = el.dataset.setting as keyof Snapshot["options"];
      const next = await Agents.setOptions({ ...snap.options, [key]: !snap.options[key] });
      apply(next);
      showSettings(next);
      queueCompile(400);
    });
  });
}

function settingToggle(key: keyof Snapshot["options"], name: string, desc: string, on: boolean): string {
  return `<div class="setting-row tall">
    <div class="setting-text">
      <div class="setting-name">${name}</div>
      <div class="setting-desc">${desc}</div>
    </div>
    <button class="switch ${on ? "on" : ""}" data-setting="${key}" role="switch" aria-checked="${on}" aria-label="${name}"></button>
  </div>`;
}

function confirmDiscard(): Promise<boolean> {
  return new Promise((resolve) => {
    const root = document.getElementById("modal-root");
    if (!root) return resolve(true);
    root.innerHTML = `
      <div class="modal-backdrop" data-backdrop>
        <div class="modal narrow">
          <div class="modal-head">放弃未写入的修改？</div>
          <div class="modal-body">重新读取会丢弃左边尚未保存的编辑。AGENTS.md 与 AGENTS.raw.md 都不会被改动。</div>
          <div class="modal-foot">
            <button class="btn" data-cancel>取消</button>
            <button class="btn primary" data-ok>重新读取</button>
          </div>
        </div>
      </div>`;
    const close = (v: boolean) => {
      root.innerHTML = "";
      resolve(v);
    };
    root.querySelector("[data-ok]")?.addEventListener("click", () => close(true));
    root.querySelector("[data-cancel]")?.addEventListener("click", () => close(false));
    root.querySelector("[data-backdrop]")?.addEventListener("click", (e) => {
      if (e.target === e.currentTarget) close(false);
    });
  });
}

function confirmAction(title: string, body: string): Promise<boolean> {
  return new Promise((resolve) => {
    const root = document.getElementById("modal-root");
    if (!root) return resolve(false);
    root.innerHTML = `
      <div class="modal-backdrop" data-backdrop>
        <div class="modal narrow">
          <div class="modal-head">${esc(title)}</div>
          <div class="modal-body">${esc(body)}</div>
          <div class="modal-foot">
            <button class="btn" data-cancel>取消</button>
            <button class="btn primary" data-ok>删除</button>
          </div>
        </div>
      </div>`;
    const close = (v: boolean) => {
      root.innerHTML = "";
      resolve(v);
    };
    root.querySelector("[data-ok]")?.addEventListener("click", () => close(true));
    root.querySelector("[data-cancel]")?.addEventListener("click", () => close(false));
    root.querySelector("[data-backdrop]")?.addEventListener("click", (e) => {
      if (e.target === e.currentTarget) close(false);
    });
    (root.querySelector("[data-ok]") as HTMLElement | null)?.focus();
  });
}

function promptName(initial: string, done: (name: string) => void): void {
  const root = document.getElementById("modal-root");
  if (!root) return;
  root.innerHTML = `
    <div class="modal-backdrop" data-backdrop>
      <div class="modal narrow">
        <div class="modal-head">${initial && initial !== "新模块" ? "重命名模块" : "新增模块"}</div>
        <div class="modal-body">
          <input class="dialog-input" id="dialog-name" value="${esc(initial)}" spellcheck="false" />
        </div>
        <div class="modal-foot">
          <button class="btn" data-cancel>取消</button>
          <button class="btn primary" data-ok>确定</button>
        </div>
      </div>
    </div>`;
  const input = root.querySelector<HTMLInputElement>("#dialog-name");
  const close = (value: string | null) => {
    root.innerHTML = "";
    if (value !== null) done(value.trim());
  };
  root.querySelector("[data-ok]")?.addEventListener("click", () => close(input?.value ?? ""));
  root.querySelector("[data-cancel]")?.addEventListener("click", () => close(null));
  root.querySelector("[data-backdrop]")?.addEventListener("click", (e) => {
    if (e.target === e.currentTarget) close(null);
  });
  input?.addEventListener("keydown", (e) => {
    if (e.key === "Enter") close(input.value);
    if (e.key === "Escape") close(null);
    e.stopPropagation();
  });
  requestAnimationFrame(() => {
    input?.focus();
    input?.select();
  });
}

function showBackups(): void {
  const root = document.getElementById("modal-root");
  const snap = state.snapshot;
  if (!root || !snap) return;
  const rows = snap.backups.length
    ? snap.backups
        .map(
          (b) => `
        <div class="backup-row">
          <span class="backup-time">${timeAgo(b.at)}</span>
          <span class="backup-size">${(b.size / 1024).toFixed(1)} KB</span>
          <button class="btn small" data-restore="${esc(b.name)}">恢复</button>
        </div>`,
        )
        .join("")
    : `<div class="empty">还没有备份。每次应用前，上一版都会自动保存在这里。</div>`;

  root.innerHTML = `
    <div class="modal-backdrop" data-backdrop>
      <div class="modal">
        <div class="modal-head">历史备份</div>
        <div class="modal-body">${rows}</div>
        <div class="modal-foot">
          <button class="btn" data-folder>打开文件夹</button>
          <button class="btn primary" data-cancel>完成</button>
        </div>
      </div>
    </div>`;

  const close = () => closeModal();
  root.querySelector("[data-cancel]")?.addEventListener("click", close);
  root.querySelector("[data-folder]")?.addEventListener("click", () => void Agents.revealBackups());
  root.querySelector("[data-backdrop]")?.addEventListener("click", (e) => {
    if (e.target === e.currentTarget) close();
  });
  root.querySelectorAll<HTMLElement>("[data-restore]").forEach((el) => {
    el.addEventListener("click", async () => {
      close();
      apply(await Agents.restore(el.dataset.restore!));
    });
  });
}

let shortcutsBound = false;
function bindShortcuts(): void {
  if (shortcutsBound) return;
  shortcutsBound = true;
  document.addEventListener("keydown", (e) => {
    const meta = e.metaKey || e.ctrlKey;
    if (!meta) {
      if (e.key === "Escape") {
        state.settings = false;
        closeModal();
      }
      return;
    }
    const key = e.key.toLowerCase();
    if (key === "s") {
      e.preventDefault();
      void flush();
    } else if (key === "r") {
      e.preventDefault();
      void reload();
    } else if (key === ",") {
      e.preventDefault();
      state.settings = !state.settings;
      if (state.settings && state.snapshot) showSettings(state.snapshot);
      else closeModal();
    }
  });
}

// Coming back to the app is the moment to notice a file edited elsewhere.
events.focused.on(() => {
  void (async () => {
    const changed = await Agents.diskChanged().catch(() => false);
    if (changed) {
      state.diskChanged = true;
      render();
    }
  })();
});

// A flush before the window closes, so a last keystroke is not lost.
window.addEventListener("beforeunload", () => {
  if (state.draftTimer !== null) {
    clearTimeout(state.draftTimer);
    state.draftTimer = null;
    void Agents.setRaw(state.draft);
  }
});

let snap = await Agents.snapshot();
if (!snap.gateway.reachable) {
  // The gateway may come up after the app does; one retry covers starting both
  // at login.
  snap = await Agents.refreshGateway().catch(() => snap);
}
apply(snap);
if (!snap.compiled && snap.raw.trim() !== "") {
  queueCompile(600);
}
