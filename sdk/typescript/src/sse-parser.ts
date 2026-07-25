import type { SSEFrame } from "./types.js";

/**
 * Incremental WHATWG-compatible SSE field parser.
 *
 * - Decodes UTF-8 across chunk boundaries
 * - Joins multi-line `data:` fields with `\n`
 * - Treats lines starting with `:` as comments
 * - Dispatches a frame on a blank line
 *
 * Aligned with backend `transport/sse` wire frames:
 *   id: <sequence>
 *   event: <type>
 *   data: <single-line JSON>
 */
export class SSEFrameParser {
  private readonly decoder = new TextDecoder("utf-8", { fatal: false });
  private textBuffer = "";
  private partial: {
    id?: string;
    event?: string;
    dataLines: string[];
    retry?: number;
    comments: string[];
  } = emptyPartial();

  /** Feed the next binary or text chunk; returns complete frames. */
  push(chunk: Uint8Array | string): SSEFrame[] {
    if (typeof chunk === "string") {
      this.textBuffer += chunk;
    } else {
      this.textBuffer += this.decoder.decode(chunk, { stream: true });
    }
    return this.consumeLines(false);
  }

  /** Flush decoder/stream end and emit any trailing frame without blank line. */
  flush(): SSEFrame[] {
    this.textBuffer += this.decoder.decode();
    const frames = this.consumeLines(true);
    const trailing = this.finishFrame(true);
    if (trailing) {
      frames.push(trailing);
    }
    this.textBuffer = "";
    return frames;
  }

  reset(): void {
    this.textBuffer = "";
    this.partial = emptyPartial();
  }

  private consumeLines(flush: boolean): SSEFrame[] {
    const frames: SSEFrame[] = [];
    // Normalize CRLF as we go without requiring complete lines for mid-chunk UTF-8.
    this.textBuffer = this.textBuffer.replace(/\r\n/g, "\n").replace(/\r/g, "\n");

    while (true) {
      const newline = this.textBuffer.indexOf("\n");
      if (newline < 0) {
        if (flush && this.textBuffer.length > 0) {
          this.handleLine(this.textBuffer);
          this.textBuffer = "";
        }
        break;
      }
      const line = this.textBuffer.slice(0, newline);
      this.textBuffer = this.textBuffer.slice(newline + 1);
      const frame = this.handleLine(line);
      if (frame) {
        frames.push(frame);
      }
    }
    return frames;
  }

  private handleLine(line: string): SSEFrame | null {
    if (line === "") {
      return this.finishFrame(false);
    }
    if (line.startsWith(":")) {
      this.partial.comments.push(line.slice(1).replace(/^\s/, ""));
      return null;
    }

    let field: string;
    let value: string;
    const colon = line.indexOf(":");
    if (colon < 0) {
      field = line;
      value = "";
    } else {
      field = line.slice(0, colon);
      value = line.slice(colon + 1);
      if (value.startsWith(" ")) {
        value = value.slice(1);
      }
    }

    switch (field) {
      case "event":
        this.partial.event = value;
        break;
      case "data":
        this.partial.dataLines.push(value);
        break;
      case "id":
        // Per WHATWG, ignore id values that contain null.
        if (!value.includes("\0")) {
          this.partial.id = value;
        }
        break;
      case "retry": {
        if (/^\d+$/.test(value)) {
          this.partial.retry = Number.parseInt(value, 10);
        }
        break;
      }
      default:
        // Unknown fields are ignored (forward compatible).
        break;
    }
    return null;
  }

  private finishFrame(allowEmpty: boolean): SSEFrame | null {
    const hasContent =
      this.partial.dataLines.length > 0 ||
      this.partial.event !== undefined ||
      this.partial.id !== undefined ||
      this.partial.retry !== undefined ||
      this.partial.comments.length > 0;
    if (!hasContent) {
      this.partial = emptyPartial();
      return null;
    }
    // Heartbeat-only comment frames are still useful to callers.
    if (!allowEmpty && this.partial.dataLines.length === 0 && this.partial.event === undefined &&
        this.partial.id === undefined && this.partial.retry === undefined &&
        this.partial.comments.length > 0) {
      const frame: SSEFrame = {
        data: "",
        comments: this.partial.comments.slice(),
      };
      this.partial = emptyPartial();
      return frame;
    }
    if (this.partial.dataLines.length === 0 && this.partial.event === undefined &&
        this.partial.id === undefined && !allowEmpty) {
      // Blank dispatch with only comments already handled; pure empty is a no-op.
      this.partial = emptyPartial();
      return null;
    }

    const frame: SSEFrame = {
      data: this.partial.dataLines.join("\n"),
      comments: this.partial.comments.slice(),
    };
    if (this.partial.id !== undefined) {
      frame.id = this.partial.id;
    }
    if (this.partial.event !== undefined) {
      frame.event = this.partial.event;
    }
    if (this.partial.retry !== undefined) {
      frame.retry = this.partial.retry;
    }
    this.partial = emptyPartial();
    return frame;
  }
}

function emptyPartial(): {
  id?: string;
  event?: string;
  dataLines: string[];
  retry?: number;
  comments: string[];
} {
  return { dataLines: [], comments: [] };
}

/**
 * Rejects URLs that embed access tokens in the query string.
 * AAP forbids Token Query for SSE attach / resume.
 */
export function assertNoAccessTokenInURL(url: string | URL): void {
  const parsed = typeof url === "string" ? new URL(url, "https://actweave.invalid") : url;
  for (const [key, value] of parsed.searchParams.entries()) {
    const lower = key.toLowerCase();
    if (
      lower === "access_token" ||
      lower === "token" ||
      lower === "authorization" ||
      lower.includes("access_token")
    ) {
      throw new Error(`AAP SSE URL must not carry credentials in query parameter "${key}"`);
    }
    // Defense in depth: bearer-shaped values in any query param.
    if (/^bearer\s+/i.test(value) || /^eyJ[A-Za-z0-9_-]+\./.test(value)) {
      throw new Error("AAP SSE URL must not carry credential-like values in the query string");
    }
  }
}
