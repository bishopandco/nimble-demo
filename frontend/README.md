# The common room

A small Svelte 5 + Vite chatroom using the blog API's `/ws` endpoint. All connections
share one conversation. Messages are broadcast to everyone, including the sender.
The server shares its latest 100 messages when a new client connects.

## Development

Use Node 24, or a version matching `package.json`'s engines field.
If you use nvm, run `nvm use` in this directory first; `.nvmrc` selects Node 24.

Start the API from the repository root:

```sh
make dev
```

In another terminal, from `frontend/`:

```sh
npm ci
npm run dev
```

Open [the chatroom](http://127.0.0.1:5173) in two tabs. Choose a display name or
leave it blank to use a generated guest name. Type a message and click **Send**.
The same message appears in both tabs. Enter sends; Shift+Enter adds a new line.
Messages allow up to 500 Unicode characters; names allow up to 40.

`make dev` rebuilds and restarts the API when Go source or embedded SQL changes.
The frontend reconnects after each restart. Chat history resets on restart;
SQLite blog data persists. Vite handles frontend changes separately.

**Disconnect** pauses the connection and automatic retries. **Connect to room**
resumes it. Dropped connections retry with a delay from one to ten seconds.
On reconnect or reload, the app replaces its local conversation with the server's
history snapshot. Chat history is in memory, shared by this API process, and resets
when the API restarts. It is not saved in SQLite. Display names are not logins.

Only chat messages appear in the conversation. REST change events and heartbeat
responses still use the same socket. Server validation errors appear by the composer.

The browser connects to `/ws` on the frontend's origin. Vite proxies it to
`http://127.0.0.1:8080` and preserves Host and Origin, so the API's existing origin
check stays in effect. The Go API does not need CORS or origin-policy changes.

For a different API address, set `API_TARGET`:

```sh
API_TARGET=http://127.0.0.1:9000 npm run dev
```

You can also copy `.env.example` to `.env` and set `API_TARGET` there.

## Verify and build

```sh
npm test
npm run build
npm run preview
```

Tests cover shared history, server echoes, message composition, Unicode limits,
keyboard behavior, reconnect delays, manual disconnection, stale callbacks,
message retention, safe text rendering, and lifecycle cleanup.
`npm run build` produces `dist/`. Preview runs on `http://127.0.0.1:4173` and uses
the same WebSocket proxy. A static deployment must route `/ws` to the Go API on
the same public origin and support WebSocket upgrades.
