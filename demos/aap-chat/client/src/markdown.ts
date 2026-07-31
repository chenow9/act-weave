import DOMPurify from "dompurify";
import hljs from "highlight.js/lib/core";
import javascript from "highlight.js/lib/languages/javascript";
import typescript from "highlight.js/lib/languages/typescript";
import json from "highlight.js/lib/languages/json";
import python from "highlight.js/lib/languages/python";
import bash from "highlight.js/lib/languages/bash";
import go from "highlight.js/lib/languages/go";
import sql from "highlight.js/lib/languages/sql";
import yaml from "highlight.js/lib/languages/yaml";
import xml from "highlight.js/lib/languages/xml";
import "highlight.js/styles/github-dark.min.css";
import katex from "katex";
import "katex/dist/katex.min.css";
import MarkdownIt from "markdown-it";
// @ts-expect-error no types for markdown-it-texmath
import texmath from "markdown-it-texmath";

hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("js", javascript);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("ts", typescript);
hljs.registerLanguage("json", json);
hljs.registerLanguage("python", python);
hljs.registerLanguage("py", python);
hljs.registerLanguage("bash", bash);
hljs.registerLanguage("shell", bash);
hljs.registerLanguage("sh", bash);
hljs.registerLanguage("go", go);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("yaml", yaml);
hljs.registerLanguage("yml", yaml);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("html", xml);

function highlightCode(code: string, lang: string): string {
  const language = (lang || "").trim().toLowerCase();
  if (language && hljs.getLanguage(language)) {
    try {
      return `<pre class="hljs"><code class="language-${escapeAttr(language)}">${
        hljs.highlight(code, { language, ignoreIllegals: true }).value
      }</code></pre>`;
    } catch {
      /* fall through */
    }
  }
  return `<pre class="hljs"><code>${escapeHtml(code)}</code></pre>`;
}

const md: MarkdownIt = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
  highlight: highlightCode,
});

md.use(texmath, {
  engine: katex,
  delimiters: ["dollars", "brackets"],
  katexOptions: { throwOnError: false, strict: "ignore" },
});

// Allow safe images (https/http)
md.renderer.rules.image = (tokens, idx) => {
  const token = tokens[idx];
  const src = token.attrGet("src") || "";
  const alt = token.content || "";
  if (!isSafeHttpUrl(src)) {
    return escapeHtml(alt || src);
  }
  return `<img src="${escapeAttr(src)}" alt="${escapeAttr(alt)}" loading="lazy" referrerpolicy="no-referrer" />`;
};

md.renderer.rules.link_open = (tokens, idx) => {
  const token = tokens[idx];
  const href = token.attrGet("href") || "";
  if (!isSafeHttpUrl(href) && !href.startsWith("mailto:")) {
    return "";
  }
  return `<a href="${escapeAttr(href)}" target="_blank" rel="noopener noreferrer">`;
};

const allowedTags = [
  "p",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "ul",
  "ol",
  "li",
  "strong",
  "em",
  "code",
  "pre",
  "blockquote",
  "a",
  "br",
  "hr",
  "img",
  "table",
  "thead",
  "tbody",
  "tr",
  "th",
  "td",
  "span",
  "div",
  "section",
];

const allowedAttr = [
  "href",
  "target",
  "rel",
  "title",
  "class",
  "src",
  "alt",
  "loading",
  "referrerpolicy",
  "width",
  "height",
  "aria-hidden",
  "style",
];

export function renderMarkdown(source: string, emptyFallback = ""): string {
  const normalized = (source || "").trim();
  if (!normalized) {
    return emptyFallback ? `<p>${escapeHtml(emptyFallback)}</p>` : "";
  }
  const raw = md.render(normalized);
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: allowedTags,
    ALLOWED_ATTR: allowedAttr,
    ALLOW_DATA_ATTR: false,
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
  });
}

export function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function escapeAttr(value: string): string {
  return escapeHtml(value).replaceAll("'", "&#39;");
}

function isSafeHttpUrl(value: string): boolean {
  const trimmed = value.trim().toLowerCase();
  return trimmed.startsWith("https://") || trimmed.startsWith("http://");
}
