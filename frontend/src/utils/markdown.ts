import DOMPurify from "dompurify";
import MarkdownIt from "markdown-it";

const markdown = new MarkdownIt({
  html: false,
  linkify: false,
  breaks: true,
});

markdown.renderer.rules.image = () => "";

const defaultLinkOpen =
  markdown.renderer.rules.link_open || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));

markdown.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const token = tokens[idx];
  const hrefIndex = token.attrIndex("href");
  if (hrefIndex >= 0 && token.attrs) {
    const href = token.attrs[hrefIndex][1] || "";
    if (!isSafeUrl(href)) {
      // Drop unsafe links to plain text by clearing href and not rendering as anchor.
      token.attrs[hrefIndex][1] = "";
      token.attrSet("data-blocked", "1");
    } else {
      token.attrSet("target", "_blank");
      token.attrSet("rel", "noopener noreferrer");
    }
  }
  if (token.attrGet("data-blocked") === "1") {
    return "";
  }
  return defaultLinkOpen(tokens, idx, options, env, self);
};

const defaultLinkClose =
  markdown.renderer.rules.link_close || ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options));

markdown.renderer.rules.link_close = (tokens, idx, options, env, self) => {
  // If the matching open was blocked, skip close tag.
  for (let i = idx - 1; i >= 0; i -= 1) {
    if (tokens[i].type === "link_open") {
      if (tokens[i].attrGet("data-blocked") === "1") return "";
      break;
    }
  }
  return defaultLinkClose(tokens, idx, options, env, self);
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
];

function isSafeUrl(value: string) {
  const trimmed = value.trim().toLowerCase();
  return trimmed.startsWith("https://") || trimmed.startsWith("http://") || trimmed.startsWith("mailto:");
}

/**
 * Safe Markdown render for Agents prompt viewing (TD3-A):
 * markdown-it html=false + no images + URL allowlist, then DOMPurify tag/attr allowlist.
 */
export function renderMarkdown(source: string, emptyFallback = "暂无内容。") {
  const normalized = (source || "").trim();
  if (!normalized) {
    return `<p>${escapeHTML(emptyFallback)}</p>`;
  }
  const raw = markdown.render(normalized);
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS: allowedTags,
    ALLOWED_ATTR: ["href", "target", "rel", "title", "class"],
    ALLOW_DATA_ATTR: false,
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[^a-z]|[a-z+.-]+(?:[^a-z+.:-]|$))/i,
  });
}

function escapeHTML(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}
