// render.ts draws Markdown. The document comes from the user's file and from a
// model, and this page can call the app, so the renderer escapes every
// character it does not generate itself.
import MarkdownIt from "markdown-it";
import hljs from "highlight.js/lib/common";

const escapeHtml = (s: string): string =>
  s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");

const md = new MarkdownIt({
  html: false, // raw HTML in the file must not become an element
  linkify: false,
  breaks: false,
  highlight(code: string, lang: string): string {
    if (lang && hljs.getLanguage(lang)) {
      try {
        return `<pre class="hljs"><code>${hljs.highlight(code, { language: lang, ignoreIllegals: true }).value}</code></pre>`;
      } catch {
        /* fall through to the escaped block */
      }
    }
    return `<pre class="hljs"><code>${escapeHtml(code)}</code></pre>`;
  },
});

/** renderMarkdown turns the document into the preview's HTML. */
export function renderMarkdown(text: string): string {
  if (text.trim() === "") return "";
  return md.render(text);
}

/** RenderToken is one block of the rendered document, kept separate so a
 * caller can mark the block that belongs to a chosen section without
 * re-parsing the Markdown or trusting a string replace. */
export interface RenderToken {
  /** The heading line this block starts at, when it is a heading. */
  heading: string;
  /** The rendered HTML of the block, heading included when it has one. */
  html: string;
}

/** renderTokens splits the document into top-level blocks. A block starting
 * with a level 1 or 2 heading begins a section; the blocks after it belong to
 * that section until the next such heading. */
export function renderTokens(text: string): RenderToken[] {
  if (text.trim() === "") return [];
  const tokens = md.parse(text, {});
  const groups: { heading: string; tokens: typeof tokens }[] = [];

  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]!;
    const isSection =
      token.type === "heading_open" && (token.tag === "h1" || token.tag === "h2");
    if (isSection || groups.length === 0) {
      const heading = isSection ? (tokens[i + 1]?.content ?? "").trim() : "";
      groups.push({ heading, tokens: [] });
    }
    groups[groups.length - 1]!.tokens.push(token);
  }

  return groups.map((group) => ({
    heading: group.heading,
    html: md.renderer.render(group.tokens, md.options, {}),
  }));
}

/** renderMarked renders the document with the block selected by `match`
 * wrapped in a marked group, so the preview shows the whole file with the
 * chosen section highlighted, exactly as the source pane marks it in the
 * author's own text. */
export function renderMarked(text: string, match: (heading: string) => boolean): string {
  const tokens = renderTokens(text);
  if (tokens.length === 0) return "";
  const out: string[] = [];
  let open = false;
  for (const token of tokens) {
    if (token.heading) {
      if (open) out.push("</div>");
      open = match(token.heading);
      if (open) out.push(`<div class="module-pick">`);
    }
    out.push(token.html);
  }
  if (open) out.push("</div>");
  return out.join("");
}

interface Span {
  text: string;
  cls?: string;
}

/** highlightMarkdown colors a Markdown source file line by line: headings,
 * list markers, emphasis, code spans and links each get their own class, so the
 * editor reads as Markdown and still shows the text that will be written. */
export function highlightMarkdown(text: string): string[] {
  const out: string[] = [];
  let inFence = false;
  let fenceLang = "";

  for (const line of text.split("\n")) {
    const trimmed = line.trimStart();
    if (/^(```|~~~)/.test(trimmed)) {
      if (!inFence) fenceLang = trimmed.slice(3).trim();
      inFence = !inFence;
      out.push(`<span class="md-fence">${escapeHtml(line)}</span>`);
      continue;
    }
    if (inFence) {
      out.push(highlightCodeLine(line, fenceLang));
      continue;
    }
    out.push(highlightLine(line));
  }
  return out;
}

function highlightCodeLine(line: string, lang: string): string {
  const escaped = escapeHtml(line);
  if (!lang || !hljs.getLanguage(lang)) {
    return `<span class="md-code-line">${escaped || " "}</span>`;
  }
  // Highlighting one line at a time keeps the source's own line breaks and
  // indentation; a lexer that only matches across lines simply yields plain
  // text for the line.
  try {
    const { value } = hljs.highlight(line, { language: lang, ignoreIllegals: true });
    return value || " ";
  } catch {
    return escaped || " ";
  }
}

function highlightLine(line: string): string {
  if (line.trim() === "") return " ";

  const heading = /^(\s*)(#{1,6})(\s+)(.*)$/.exec(line);
  if (heading) {
    return (
      heading[1] +
      `<span class="md-hash">${heading[2]}</span>` +
      heading[3] +
      `<span class="md-heading">${inline(heading[4]!)}</span>`
    );
  }

  const quote = /^(\s*)(&gt;|>)(\s?)(.*)$/.exec(line);
  if (quote) {
    return `${quote[1]}<span class="md-quote">${quote[2]}${quote[3]}${inline(quote[4]!)}</span>`;
  }

  const bullet = /^(\s*)([-*+])(\s+)(.*)$/.exec(line);
  if (bullet) {
    return (
      bullet[1] +
      `<span class="md-marker">${bullet[2]}</span>` +
      bullet[3] +
      inline(bullet[4]!)
    );
  }

  const ordered = /^(\s*)(\d+[.)])(\s+)(.*)$/.exec(line);
  if (ordered) {
    return (
      ordered[1] +
      `<span class="md-marker">${ordered[2]}</span>` +
      ordered[3] +
      inline(ordered[4]!)
    );
  }

  if (/^(\s*)(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
    return `<span class="md-rule">${escapeHtml(line)}</span>`;
  }

  return inline(line);
}

/** inline colors the spans that live inside a line: code, bold, italic, links. */
function inline(text: string): string {
  const parts: Span[] = [];
  let rest = text;

  // Code spans first: their contents are literal.
  const re = /`([^`]+)`|\*\*([^*]+)\*\*|\[([^\]]+)\]\(([^)]+)\)|(^|[\s(])\*([^*\n]+)\*/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(rest)) !== null) {
    if (m.index > last) parts.push({ text: rest.slice(last, m.index) });
    if (m[1] !== undefined) {
      parts.push({ text: "`" + m[1] + "`", cls: "md-code" });
    } else if (m[2] !== undefined) {
      parts.push({ text: m[2], cls: "md-strong" });
    } else if (m[3] !== undefined) {
      parts.push({ text: m[3], cls: "md-link" });
    } else if (m[6] !== undefined) {
      parts.push({ text: m[6], cls: "md-em" });
    }
    last = re.lastIndex;
  }
  if (last < rest.length) parts.push({ text: rest.slice(last) });

  return parts
    .map((p) => (p.cls ? `<span class="${p.cls}">${escapeHtml(p.text)}</span>` : escapeHtml(p.text)))
    .join("");
}
