# ActWeave AAP Chat Demo

A small **Agent Access Protocol** chat dialog demo that matches ActWeave console styling.

- Rich content: Markdown, KaTeX math, code highlighting, images  
- **Attachments**: pick / drag / paste → upload → send with `fileIds` → render in bubbles  
- **BFF** holds the Client Secret; the browser only uses short-lived access tokens  
- **Live AAP** or offline **Mock** preview mode  

## Mock UI (no credentials)

```bash
cd demos/aap-chat
npm install
npm run dev:mock
```

Open http://127.0.0.1:5188  

Use the paperclip **附件** control (or drag/paste) to stage images/PDFs; Mock renders local previews in the user bubble without uploading.

## Attachments

| Mode | Behavior |
| --- | --- |
| Mock | Local object-URL preview in the message bubble |
| Live | Browser: `createFile` → presigned PUT → `complete` → `waitUntilReady`, then `POST /bff/chat` with `fileIds` |
| Protocol | BFF adds `input_file` content parts on the user message |
| Limits | png/jpeg/webp/gif/pdf · ≤25MB each · max 8 |

Live needs `file:write file:read` in `AAP_SCOPES` and `agentAccess.files.enabled` on the server.

## Live AAP

1. Copy `.env.example` → `.env` and fill Client / Workspace / Agent IDs  
2. `npm install && npm run dev`  
3. UI on :5188, BFF on :8790 (`/bff` proxied)

See **README.zh-CN.md** for full Chinese documentation.
