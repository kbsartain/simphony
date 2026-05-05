# Simphony Dashboard

This is the optional React/Vite dashboard for Simphony. It displays the current orchestrator state, running Codex sessions, queued retries, token totals, and a manual refresh button.

## Development

Install dependencies:

```bash
npm ci
```

Start Vite:

```bash
npm run dev
```

The development server runs on `http://localhost:3000` and proxies `/api` requests to `http://localhost:8080`. If the Simphony backend uses a different `server.port`, update `vite.config.ts`.

## Production Build

Build static assets:

```bash
npm run build
```

The build writes to `dist/`. When `dashboard/dist` exists, the Go server serves it from `/`.

## API Types

Dashboard API shapes live in `src/api/types.ts` and should stay aligned with the Go types in `pkg/api/types.go`.
