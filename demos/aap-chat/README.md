# ActWeave AAP Chat Demo

A small **Agent Access Protocol** chat dialog demo that matches ActWeave console styling.

- Rich content: Markdown, KaTeX math, code highlighting, images  
- **Attachments**: pick / drag / paste → upload → send with `fileIds` → render in bubbles; assistant `output_file` cards (Mock + Live) with download  

- **BFF** holds the Client Secret; the browser only uses short-lived access tokens  
- **Live AAP** or offline **Mock** preview mode  

## Mock UI (no credentials)

```bash
cd demos/aap-chat
npm install
npm run dev:mock
```

Open http://127.0.0.1:5188 and use the suggestion chips (booking form, trend, KPIs, **生成本月对账单**, **看看这几张现场图**, **出一份巡检复盘包**) or **插入富文本样例**. **开发者** reveals protocol chrome, raw surface JSON, and the shared rendering fixtures.

Use the paperclip **附件** control (or drag/paste) to stage images/PDFs; Mock renders local previews in the user bubble without uploading. Assistant outbound cards in `.msg-row.is-assistant .msg-attachments`: **生成本月对账单** is a CSV file; **看看这几张现场图** is a 2×2 image gallery; **出一份巡检复盘包** mixes Markdown, photos, file cards, and an A2UI surface.

## Attachments

| Mode | Behavior |
| --- | --- |
| Mock (user) | Local object-URL preview in the user bubble |
| Mock (assistant) | Story cards from `assistant_done.attachments` (CSV, JSON, and PNG gallery) |
| Live (user) | Browser: `createFile` → presigned PUT → `complete` → `waitUntilReady`, then `POST /bff/chat` with `fileIds` |
| Live (assistant) | Snapshot `output_file` → placeholder card → `getFile` / `getFileContent` hydrate by `fileId` (never `links.content`) |
| Protocol | BFF adds `input_file` on the user message; assistant returns `output_file` |
| Download | Live: Bearer `getFileContent` → Blob; Mock: object URL. Includes inbound PDFs |
| Limits | inbound png/jpeg/webp/gif/pdf/Word/Excel/zip · ≤25MB each · max 8 (composer only) |

Live needs `file:write file:read` in `AAP_SCOPES` and `agentAccess.files.enabled` on the server. Hydrating and downloading assistant files requires **`file:read`**.

## Live AAP

1. Copy `.env.example` → `.env` and fill Client / Workspace / Agent IDs  
2. `npm install && npm run dev`  
3. UI on :5188, BFF on :8790 (`/bff` proxied)

See **README.zh-CN.md** for full Chinese documentation.
