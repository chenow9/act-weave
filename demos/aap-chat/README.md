# ActWeave AAP Chat Demo

A small **Agent Access Protocol** chat dialog demo that matches ActWeave console styling.

- Rich content: Markdown, KaTeX math, code highlighting, images  
- **BFF** holds the Client Secret; the browser only uses short-lived access tokens  
- **Live AAP** or offline **Mock** preview mode  

## Mock UI (no credentials)

```bash
cd demos/aap-chat
npm install
npm run dev:mock
```

Open http://127.0.0.1:5188

## Live AAP

1. Copy `.env.example` → `.env` and fill Client / Workspace / Agent IDs  
2. `npm install && npm run dev`  
3. UI on :5188, BFF on :8790 (`/bff` proxied)

See **README.zh-CN.md** for full Chinese documentation.
