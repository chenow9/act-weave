import { assertNoAccessTokenInURL, SSEFrameParser } from "./sse-parser.js";
import { AAPSESession, type AAPSESessionOptions } from "./sse-session.js";
import type { AAPSEMessage } from "./types.js";

export interface ReadAAPSEStreamOptions extends AAPSESessionOptions {
  /** Optional abort; when aborted, the generator throws DOMException AbortError. */
  signal?: AbortSignal;
  /**
   * Existing session to continue (for Last-Event-ID resume continuity of
   * eventId de-duplication). When omitted, a new session is created.
   */
  session?: AAPSESession;
}

export interface AAPSEStreamResult {
  session: AAPSESession;
  messages: AsyncGenerator<AAPSEMessage, void, unknown>;
}

/**
 * Reads an AAP SSE body (`fetch` response.body) as an async stream of messages.
 * Works in browsers and Node 20+ (ReadableStream + TextDecoder).
 */
export function openAAPSEStream(
  body: ReadableStream<Uint8Array> | null | undefined,
  options: ReadAAPSEStreamOptions = {},
): AAPSEStreamResult {
  if (body == null) {
    throw new Error("AAP SSE response body is missing");
  }
  const session = options.session ?? new AAPSESession(options);
  session.clearGapLatch();
  const messages = readFrames(body, session, options.signal);
  return { session, messages };
}

/**
 * Convenience for POST stream and GET resume using fetch.
 * For Token Provider, auto-reconnect, and Last-Event-ID resume use AgentAccessClient.
 */
export async function fetchAAPSEStream(
  input: string | URL,
  init: RequestInit & {
    session?: AAPSESession;
    sessionOptions?: AAPSESessionOptions;
  } = {},
): Promise<AAPSEStreamResult & { response: Response }> {
  assertNoAccessTokenInURL(input);

  const headers = new Headers(init.headers);
  if (!headers.has("Accept")) {
    headers.set("Accept", "text/event-stream");
  }

  const sessionOptions: AAPSESessionOptions = {};
  if (init.sessionOptions?.initialLastSequence !== undefined) {
    sessionOptions.initialLastSequence = init.sessionOptions.initialLastSequence;
  }
  if (init.sessionOptions?.maxSeenEventIds !== undefined) {
    sessionOptions.maxSeenEventIds = init.sessionOptions.maxSeenEventIds;
  }
  const session = init.session ?? new AAPSESession(sessionOptions);

  // GET resume attaches Last-Event-ID from the session cursor.
  const method = (init.method ?? "GET").toUpperCase();
  if (method === "GET") {
    const lastEventId = session.getLastEventId();
    if (lastEventId !== undefined && !headers.has("Last-Event-ID")) {
      headers.set("Last-Event-ID", lastEventId);
    }
  }

  const response = await fetch(input, {
    ...init,
    headers,
    // Ensure body streams; avoid automatic decompression buffering issues in some runtimes.
  });

  if (!response.ok) {
    throw new Error(`AAP SSE HTTP ${response.status}`);
  }
  const contentType = response.headers.get("content-type") ?? "";
  if (!contentType.toLowerCase().includes("text/event-stream")) {
    throw new Error(`AAP SSE unexpected Content-Type: ${contentType}`);
  }

  const openOptions: ReadAAPSEStreamOptions = { session };
  if (init.signal) {
    openOptions.signal = init.signal;
  }
  const opened = openAAPSEStream(response.body, openOptions);
  return { ...opened, response };
}

async function* readFrames(
  body: ReadableStream<Uint8Array>,
  session: AAPSESession,
  signal?: AbortSignal,
): AsyncGenerator<AAPSEMessage, void, unknown> {
  const parser = new SSEFrameParser();
  const reader = body.getReader();

  const onAbort = (): void => {
    void reader.cancel("aborted");
  };
  if (signal) {
    if (signal.aborted) {
      await reader.cancel("aborted");
      throw abortError();
    }
    signal.addEventListener("abort", onAbort, { once: true });
  }

  try {
    while (true) {
      if (signal?.aborted) {
        throw abortError();
      }
      const { done, value } = await reader.read();
      if (done) {
        for (const frame of parser.flush()) {
          for (const message of session.pushFrame(frame)) {
            yield message;
            if (message.kind === "sequence_gap") {
              return;
            }
          }
        }
        return;
      }
      if (!value) {
        continue;
      }
      for (const frame of parser.push(value)) {
        for (const message of session.pushFrame(frame)) {
          yield message;
          if (message.kind === "sequence_gap") {
            // Caller should disconnect and reconnect with Last-Event-ID.
            await reader.cancel("sequence_gap");
            return;
          }
        }
      }
    }
  } finally {
    signal?.removeEventListener("abort", onAbort);
    reader.releaseLock();
  }
}

function abortError(): DOMException {
  return new DOMException("The AAP SSE stream was aborted", "AbortError");
}
