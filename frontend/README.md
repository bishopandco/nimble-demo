# Common room

A small Svelte 5 + Vite chatroom using the chat server's `/ws` endpoint. All connections
share one conversation. Messages are broadcast to everyone, including the sender.
The server shares its latest 100 messages when a new client connects.

## Layout and typography

The layout uses a 12-column grid with 24px gutters and an 8px spacing rhythm.
Below 700px, it stacks onto four columns. Headings use the system Helvetica
grotesk stack; body text and controls use the system monospace stack.
Six type styles are defined in `src/style.css`:

| Style | Size / leading | Weight |
| --- | --- | --- |
| Display | 64 / 72px | 500 |
| Heading | 32 / 40px | 500 |
| Section | 20 / 24px | 500 |
| Body | 14 / 24px | 400 |
| Detail | 12 / 16px | 400 |
| Emphasis | 14 / 24px | 600 |

Interface copy stays within ten words per line. `MeasuredText.svelte` adds
presentation breaks every ten words in messages, names, and errors. It preserves
the original text, whitespace, and explicit newlines without changing socket data.
Fenced code blocks preserve their original lines and indentation, with horizontal
scrolling for long lines. Code is displayed as text, never executed as HTML.

## Development

Use Node 24, or a version matching `package.json`'s engines field.
If you use nvm, run `nvm use` in this directory first; `.nvmrc` selects Node 24.

Start the server from the repository root:

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

Use `/bot <prompt>`, `/bot codex <prompt>`, or `/bot claude <prompt>` to ask the
host CLI. A thinking indicator appears immediately after the server accepts a
prompt, as a bot message directly after the prompt. Its spinner stays active until
a reply or error replaces it in that same thread position. Pending rows are restored
on reconnect. The spinner respects reduced-motion preferences. Bot replies are
labeled `bot`, broadcast to the room, and saved alongside their prompt IDs.
Reply chunks are combined into one displayed message before parsing backtick or
tilde code fences. Provider errors replace the sender's pending message; other
participants see a generic failure message.
See the root README for CLI installation, login, and sidecar configuration.

`make dev` rebuilds and restarts the server when Go source changes.
The frontend reconnects after each restart. SQLite preserves chat history.
Vite handles frontend changes separately.

**Disconnect** pauses the connection and automatic retries. **Connect to room**
resumes it. Dropped connections retry with a delay from one to ten seconds.
On reconnect or reload, the app replaces its local conversation with the server's
latest 100-message history snapshot. The server stores chat messages in SQLite
before broadcasting them. History survives restarts when using the same database.
Each reconnect gets a new connection identity. Display names are not logins.

Only chat messages appear in the conversation. Heartbeat responses use the same
socket. Server validation errors appear by the composer.

The browser connects to `/ws` on the frontend's origin. Vite proxies it to
`http://127.0.0.1:8080` and preserves Host and Origin, so the server's existing origin
check stays in effect. The Go server does not need CORS or origin-policy changes.

For a different server address, set `API_TARGET`:

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
the same WebSocket proxy. A static deployment must route `/ws` to the Go server on
the same public origin and support WebSocket upgrades.
