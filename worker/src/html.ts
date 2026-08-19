/**
 * Readable text and links out of HTML.
 *
 * The Go implementation carries its own tokenizer because Go's standard library
 * has no HTML parser. Workers has HTMLRewriter, which is a streaming parser in
 * the runtime itself, so this is the one place the port is not a translation:
 * using anything else here would be carrying a parser to a platform that
 * already has a better one.
 */

/** Elements contributing nothing a reader would see. */
const SKIPPED = new Set(["script", "style", "noscript", "svg", "head", "template", "iframe"]);

/**
 * Elements that force a line break, so separate paragraphs, list items and table
 * rows do not run together. That matters more than it sounds: an extractor
 * reading "claims deadlineOctober 15 2026opt out" has to guess at boundaries the
 * markup already knew.
 */
const BLOCKS = new Set([
  "p", "div", "br", "hr", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
  "section", "article", "header", "footer", "main", "table", "ul", "ol",
  "dl", "dt", "dd", "blockquote", "form", "figure", "nav", "aside",
]);

export interface Link {
  url: string;
  text: string;
}

export interface Page {
  text: string;
  links: Link[];
  title: string;
}

/**
 * Collapse whitespace within a line and blank runs between lines, leaving text
 * that reads the way the page looks.
 */
function tidy(s: string): string {
  const lines = s.split("\n");
  const out: string[] = [];
  let blank = 0;
  for (const raw of lines) {
    const line = raw.trim().replace(/\s+/g, " ");
    if (!line) {
      blank++;
      if (blank > 1) continue;
    } else {
      blank = 0;
    }
    out.push(line);
  }
  return out.join("\n").trim();
}

/**
 * Parse a document once, producing the text, the links and the title.
 *
 * One pass rather than three: a settlement notice is tens of kilobytes and the
 * scan reads hundreds of them, and HTMLRewriter charges for the traversal, not
 * for the handlers.
 */
export async function parse(html: string, baseURL: string): Promise<Page> {
  const chunks: string[] = [];
  const links: Link[] = [];
  let title = "";
  let h1 = "";

  let skipDepth = 0;
  let current: { url: string; parts: string[] } | null = null;
  let capture: "title" | "h1" | null = null;

  const rewriter = new HTMLRewriter()
    .on("*", {
      element(el) {
        const name = el.tagName.toLowerCase();

        if (SKIPPED.has(name)) {
          // head is skipped for text but must still yield its title.
          if (name !== "head") {
            skipDepth++;
            el.onEndTag(() => { skipDepth--; });
            return;
          }
        }
        if (skipDepth > 0) return;

        if (name === "title" || (name === "h1" && !h1)) {
          capture = name === "title" ? "title" : "h1";
          el.onEndTag(() => { capture = null; });
        }

        if (BLOCKS.has(name)) {
          chunks.push("\n");
          // Void elements have no end tag; asking for one throws.
          if (name !== "br" && name !== "hr") {
            el.onEndTag(() => { chunks.push("\n"); });
          }
        }
        if (name === "td" || name === "th") chunks.push("\t");

        if (name === "a") {
          const href = (el.getAttribute("href") ?? "").trim();
          if (current) { flush(); }
          if (href && !href.startsWith("#") && !/^(javascript|mailto|tel):/i.test(href)) {
            let resolved = href;
            try { resolved = new URL(href, baseURL).toString(); } catch { /* keep raw */ }
            current = { url: resolved, parts: [] };
            el.onEndTag(() => { flush(); });
          }
        }
      },
      text(chunk) {
        if (skipDepth > 0) return;
        // Whitespace inside a text node, newlines included, is just whitespace:
        // HTML source is wrapped at whatever column the author's editor used.
        // Collapsing here rather than at the end is what keeps a sentence the
        // markup wrapped mid-phrase from being read as two lines, which would
        // put "the claims deadline is" and the date that follows it out of reach
        // of the same regular expression.
        const t = chunk.text.replace(/\s+/g, " ");
        if (!t) return;
        chunks.push(t);
        if (current) current.parts.push(t);
        if (capture === "title") title += t;
        else if (capture === "h1") h1 += t;
      },
    });

  function flush() {
    if (!current) return;
    links.push({ url: current.url, text: tidy(current.parts.join("")) });
    current = null;
  }

  await rewriter
    .transform(new Response(html))
    .arrayBuffer();
  flush();

  return {
    text: tidy(chunks.join("")),
    links,
    title: tidy(title) || tidy(h1),
  };
}

/**
 * Strip the site name a content management system appends to every page title.
 * Only removed when the tail looks like the site rather than part of the name:
 * "Smith v. Loblaw - Ontario" keeps its second half.
 */
export function cleanTitle(title: string, pageURL: string): string {
  let host = "";
  try {
    host = new URL(pageURL).hostname.toLowerCase().replace(/^www\./, "").split(".")[0] ?? "";
  } catch { /* no host to compare against */ }

  for (const sep of [" | ", " - ", " — ", " · "]) {
    const i = title.lastIndexOf(sep);
    if (i <= 0) continue;
    const head = title.slice(0, i).trim();
    const tail = title.slice(i + sep.length).trim();
    if (!head || tail.length > 60 || tail.split(/\s+/).length > 7) continue;
    if (looksLikeMasthead(tail, host)) return head;
  }
  return title;
}

function looksLikeMasthead(tail: string, host: string): boolean {
  const key = tail.toLowerCase().replace(/[\s.\-&']/g, "");
  if (host.length > 2 && key.includes(host)) return true;
  // A firm's name anywhere in the tail, not only at the end.
  for (const mark of ["llp", "lawyers", "lawgroup", "barristers", "avocats", "attorneys", "lawfirm"]) {
    if (key.includes(mark)) return true;
  }
  switch (key) {
    case "home": case "index": case "classactions": case "classaction":
    case "settlements": case "cases": case "ourcases": case "news":
    case "recourscollectifs":
      return true;
  }
  return key.endsWith("law");
}
