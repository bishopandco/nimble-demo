# Common room

A shared chatroom with a Go WebSocket server and a Svelte frontend. Every
connection joins the same conversation. SQLite stores accepted chat messages and
connection identities. The latest 100 messages are sent to new connections and
restored after server restarts.

## Development

Requires Go 1.24.2 or newer and Node 24.

Start the server:

```sh
make dev
```

In a second terminal:

```sh
npm --prefix frontend ci
make frontend
```

Open [the chatroom](http://127.0.0.1:5173) in two tabs. Choose a display name or
use the generated guest name. Every message appears in both tabs, including the
sender's. Enter sends; Shift+Enter adds a new line.

`make dev` uses pinned [Air](https://github.com/air-verse/air) to rebuild and restart
on changes to Go source, embedded SQL, `go.mod`, or `go.sum`, after a 300ms debounce. Test files,
the frontend, editor files, and generated binaries are excluded. The first run
downloads the watcher; a global installation is not required.

Air sends an interrupt and allows two seconds for graceful shutdown. Build
failures stop the server until the source is fixed. Compiler output appears in
the terminal, with a failure summary under `.air/`. Ctrl+C stops the watcher and
server. The frontend reconnects automatically after server restarts.

Use `make run` for a single server process without watching. Run only one of
`make dev` and `make run` on a given port. To build a standalone server, use
`make build` and run `./bin/api`.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `DB_PATH` | `data/chat.db` | SQLite file; `:memory:` for temporary chat |

```sh
ADDR=127.0.0.1:9000 DB_PATH=data/development.db make dev
```

The frontend proxies `/ws` to `http://127.0.0.1:8080`. To change that target, set
`API_TARGET` for the frontend. See [frontend/README.md](frontend/README.md) for
configuration, typography, and hosting details.

## Chat bots

Type a command in the chat composer:

```text
/bot Explain how WebSockets work.
/bot codex Explain the Go server.
/bot claude Explain the Svelte frontend.
```

`make dev`, `make run`, and `make build` build `bin/bot-sidecar` automatically.
`make sidecar` rebuilds both sidecars. The API sends one JSON request to the
sidecar over stdin, and reads one JSON response from stdout. The sidecar invokes
an installed host CLI without a shell. Prompts are passed through stdin as text.

With `auto`, the sidecar looks for Codex first, then Claude. It checks PATH and
common install directories. On macOS it also checks the Codex app bundle under
`/Applications` and `~/Applications`. `/bot claude` prefers Claude and falls back
to Codex when Claude is missing. `/bot codex` requires Codex. Provider commands
override `BOT_PROVIDER`, which sets the preference for plain `/bot` commands.
Explicit executable paths remain authoritative for each provider.

The sidecar uses the selected host CLI's existing login and default model. A
missing executable or authentication failure produces a private error in chat.
It does not switch providers after a failed run.

Calls run one at a time, with space for eight queued prompts. Normal chat and
heartbeat handling continue during a call. Accepted prompts immediately emit a
`thinking` status. The room inserts a bot message with an animated spinner after
the prompt. A reply or error replaces that same message;
every connected client receives the completed reply. Replies are committed to
SQLite before broadcast. Long replies are split into 500-character messages,
with all parts saved atomically. Output is capped at 16,000 Unicode characters,
with an explicit truncation marker. Raw process output is bounded to 128 KiB.
The frontend combines chunks into one reply, including fenced code blocks with
preserved indentation. It renders code as text without executing HTML.

Bot messages carry `provider` and `reply_to` fields. `reply_to` is the persisted
prompt message ID. `chat_bot_messages` stores this metadata. Bot output is never
parsed as another command. Bot errors are private, and host stderr is not sent
into the room. Queue status events use `type: "bot_status"`, the prompt ID, and a
`state` of `thinking`, `running`, `done`, or `failed`; they are not stored as chat.
A `ready` event includes `pending_bot_ids` when work is pending, so reconnecting
clients restore the indicator. Bot errors include `reply_to` to identify the prompt.
Unknown status values do not clear pending requests. If work ended while a client
was offline and no reply is in its history, the UI reports an unavailable result.

Codex runs with a read-only sandbox and unattended approval policy. Claude runs
with built-in tools disabled, no MCP servers, and no interactive permissions.
Each tool step starts a fresh CLI conversation with application-managed context.
The request continues if its sender disconnects. Server shutdown
cancels active work and discards queued jobs. Pending jobs are not replayed after
a restart. On macOS/Linux, cancellation also terminates provider process groups.

The server's CLI login and account usage apply to every `/bot` request in this
shared room. This remains a local, trusted demo without user authentication.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `BOT_ENABLED` | `true` | Set `false` to disable bot execution |
| `BOT_PROVIDER` | `auto` | `auto`, `codex`, or `claude` |
| `BOT_SIDECAR_PATH` | `bin/bot-sidecar` | Sidecar executable path |
| `BOT_TIMEOUT` | `2m` | Timeout per active request |
| `BOT_CODEX_BIN` | Auto-discovered | Codex executable path |
| `BOT_CLAUDE_BIN` | Auto-discovered | Claude executable path |
| `BOT_WORKDIR` | Server working directory | Working directory for provider CLIs |
| `BOT_PROMPT_FILE` | Embedded `internal/bot/prompt.md` | Optional UTF-8 guiding prompt file, read each call; maximum 16 KiB |

For example:

```sh
BOT_PROVIDER=claude BOT_TIMEOUT=90s make dev
```

The CLI adapters follow the official [Codex noninteractive interface](https://learn.chatgpt.com/docs/non-interactive-mode)
and [Claude CLI interface](https://code.claude.com/docs/en/cli-reference).
They use your installed CLI configuration and require no additional API server.

## Bot guidance and memory

The base prompt lives in [internal/bot/prompt.md](internal/bot/prompt.md). It asks
the bot to give concise, precise answers, add technical depth gradually, use
fenced code, and distinguish facts, proposals, uncertainty, and completed work.
It also tells the bot to search before claiming to remember a discussion, and to
treat earlier messages and memory as evidence rather than overriding instructions.

The sidecar loads 20 messages preceding the current prompt and all active memory
notes from the same `DB_PATH` as the API. The current message ID and speaker are
included separately. Memory is shared by the room and by both CLI providers;
display names are not authenticated identities. This requires a file database;
`:memory:` cannot share context across sidecar processes.

Both providers use the same JSON action interface, implemented by the sidecar:

| Action | Behavior |
| --- | --- |
| `history` | Read older SQLite messages, search literal text, and page by message ID |
| `remember` | Create or replace a durable note by key |
| `forget` | Remove a note from active memory |
| `reply` | Return the final chat response |

History lookups return at most 20 rows, never newer than the current prompt.
`before_id` and `after_id` are exclusive cursors. Search matches literal
substrings, with ASCII case-insensitive comparison. Bot replies may span several
rows linked by `reply_to`; the bot can retrieve surrounding rows for context.
The interface uses parameterized queries, without executing model-supplied SQL.

`bot_memory` stores up to 32 notes, each with a key, up to 1,000 characters of
content, a source prompt ID, and an update timestamp. The guide asks the bot to
save useful preferences, project facts, and decisions, update changed facts,
and skip transient chatter, secrets, and private personal details. `forget`
removes the active note; it does not delete the original conversation. Memory
writes commit immediately and survive even if a later provider step fails.

Examples:

```text
/bot Remember: keep explanations concise and use bullets.
/bot What do you remember about this room?
/bot Find our older discussion about SQLite persistence.
/bot Forget the note about our explanation style.
```

One bot call allows at most eight CLI steps, including the final reply, within
the existing `BOT_TIMEOUT`. Expanded context is capped at 256 KiB; older tool
results are discarded first. Only final reply text enters the chat. The monitor
continues tracking the same bot call while provider child processes change.

To customize the guide without rebuilding the binary, set a file path:

```sh
BOT_PROMPT_FILE=internal/bot/prompt.md make dev
```

That file is reread for each bot call. The JSON action protocol is appended by
the application separately. Without `BOT_PROMPT_FILE`, the default guide is
embedded at build time.

## Routes

| Method | Route | Result |
| --- | --- | --- |
| `GET` | `/health` | Database connectivity (`200`, or `503` when unavailable) |
| `GET` | `/ws` | Upgrade to a WebSocket connection (`101`) |
| `GET` | `/ws/monitor` | Independent bot monitor WebSocket (`101`) |

Unknown routes return `404`. Unsupported methods return `405` with an `Allow`
header. HTTP errors use `{"error":"message"}`.

## Bot monitor

The second sidecar, `bin/bot-monitor`, samples bot call process trees. The API
starts and supervises it automatically. **Bot monitor** opens beneath the chat
by default and connects to `/ws/monitor`. Chat and monitoring use separate sockets.

The panel shows the latest 50 calls, prioritizing active calls, and two recent
events. Completed calls display ✅. Each call includes its prompt ID, requested
or resolved provider, state,
sidecar PID, provider PID, runtime, memory, and CPU. Queued calls have no runtime
until they start. The frontend updates elapsed time between snapshots.

The API records queueing, execution start, the exact sidecar PID, and completion
in SQLite. The monitor discovers provider children and records resource samples.
All lifecycle changes and samples remain in `bot_call_events`; `bot_calls` holds
the latest state. Each event includes its own call snapshot. No prompt text is
copied into monitoring events. Tables are created alongside existing chat data.

Sampling defaults to once per second. Memory is the sum of RSS for the sidecar
and its descendants, in bytes; shared pages can count more than once. CPU is the
sum of OS-reported process CPU percentages, which are estimates and may exceed
100% across cores. The panel displays the latest sample, including for finished
calls. A missing sample is shown as a dash. Very short calls can end between
samples, but their sidecar PID and lifecycle are still recorded.

Process collection uses `ps` on macOS/Linux. The collector checks the sidecar's
parent PID before sampling its tree. If the API process disappears, unfinished
calls become `interrupted`. Graceful shutdown records remaining calls as
`canceled` and stops the monitor. If the monitor exits unexpectedly, the API
restarts it and the frontend reconnects with a fresh persisted snapshot.

The stream emits JSON messages with this shape:

```json
{
  "type": "monitor_snapshot",
  "source": "live",
  "timestamp": "2026-10-21T18:00:00Z",
  "calls": [],
  "events": []
}
```

Call records include `id`, `owner_pid`, `provider`, `status`, `queued_at`,
`started_at`, `finished_at`, `pid`, `provider_pid`, `memory_bytes`, `cpu_percent`,
`process_count`, `sampled_at`, and `elapsed_ms`. Unavailable measurements are
`null`. Event records include `id`, `call_id`, `kind`, `timestamp`, and `call`.
The feed is read-only and uses the same origin policy and heartbeats as chat.

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `BOT_MONITOR_ENABLED` | `true` | Set `false` to disable monitoring |
| `BOT_MONITOR_PATH` | `bin/bot-monitor` | Monitor executable path |
| `BOT_MONITOR_INTERVAL` | `1s` | Sample interval; minimum `250ms` |

Monitoring requires a file database shared by the API and sidecar. It is disabled
for `DB_PATH=:memory:`. `make monitor` builds only the monitor. `make sidecar`,
`make dev`, `make run`, and `make build` build both sidecars. To inspect its JSON
output separately, run:

```sh
bin/bot-monitor -database data/chat.db -interval 1s
```

Use one collector per database to avoid duplicate sample events.

## Chat protocol

Connect to `ws://127.0.0.1:8080/ws` (`wss://` over HTTPS). The server sends a
`ready` event containing a `client_id` and up to 100 messages in `history`.
The history field is omitted when the conversation is empty.

Send:

```json
{ "type": "chat", "name": "Ada", "message": "Hello everyone!" }
```

Every connected client, including the sender, receives:

```json
{
  "type": "chat",
  "id": 1,
  "client_id": 7,
  "name": "Ada",
  "message": "Hello everyone!",
  "timestamp": "2026-10-21T15:00:00Z"
}
```

The server assigns message IDs, connection IDs, and UTC timestamps. Names and
messages are trimmed. Names allow up to 40 Unicode characters; an empty name
becomes `Guest {client_id}`. Messages require 1–500 Unicode characters. Unknown
fields and forged identity fields are rejected. Invalid messages produce an
`error` event only for the sender.

Clients can send `{"type":"ping"}` to receive a private `pong` event. Both `pong`
and `error` events include a timestamp; errors also include a `message` field.

All connected clients receive messages in the same order. On reconnect, clients
replace their conversation with the server's latest 100-message history snapshot.
Messages are committed to SQLite before broadcast; failed writes produce a private
error and never enter live history. All accepted messages remain in the database,
including those beyond the 100-message display window. Invalid messages, ready
events, heartbeats, and error replies are not stored as chat messages.

The default database is `data/chat.db`, created with its parent directory on
startup. Set `DB_PATH=:memory:` for temporary data that resets on restart.
Connection IDs and message IDs are assigned by SQLite and remain unique across
restarts. A reconnect is a new connection, so previous messages do not acquire
another connection's identity. Display names remain freely chosen; there is no
authentication. Live broadcasts still use one server process.

The embedded schema creates `chat_connections`, `chat_messages`, and
`chat_bot_messages` atomically.
Existing unrelated tables are preserved. SQLite uses foreign keys, WAL, full
synchronous commits, a two-second busy timeout, and one database connection per
process. A database write failure is logged; the sender receives an error.
Startup fails if the database or saved history cannot be loaded.

Browser connections require an Origin host and port matching the server.
Non-browser clients may omit Origin. Vite preserves Host and Origin through its
WebSocket proxy. Incoming messages must be UTF-8 JSON text, at most 4 KiB. Binary,
invalid UTF-8, and oversized frames close the connection.

The server sends protocol heartbeat pings every 25 seconds and closes connections
without a protocol pong for 60 seconds. Browsers handle these control frames
automatically. Each connection has a 64-message outgoing queue. A full queue
closes that client without blocking other clients. Shutdown closes sockets with
WebSocket code `1001`.

## Socket logs

The server logs socket activity through `slog`, to stderr by default:

- INFO: connections, incoming data messages, and disconnections.
- WARN: rejected handshakes and invalid or unsupported messages.
- Connection context: `component=websocket`, process-local `connection_id`, and
  `remote_addr`. Accepted sessions also log their durable `client_id`.
- Incoming messages: `frame_type`, `bytes`, and valid UTF-8 text `payload`.
- Disconnections: `reason`, `duration`, `messages_received`, and the peer's close
  code when available.

## Verification

```sh
make test            # Go tests with race detection and coverage
make check           # go vet and formatting
make build           # bin/api
make frontend-test   # Svelte tests
make frontend-build  # frontend/dist
```

Tests cover shared broadcasts and ordering, bounded history and late joins,
Unicode limits, private validation errors, malformed JSON, forged identity,
origin checks, frame limits, slow clients, structured logging, route errors,
graceful subprocess shutdown, database reopen, server restart, continued IDs,
SQLite constraints, failed-save isolation, bot command routing, provider adapters,
bounded queues, long replies, and sidecar process cancellation. Tests use isolated databases.
Frontend tests cover composition, line wrapping,
server echoes, safe text rendering, history, reconnection, and cleanup.

GitHub Actions runs checks, tests, and builds on pushes and pull requests.

## Layout

```text
cmd/api/       Server entry point and shutdown tests
cmd/bot-sidecar/ One-request host CLI bridge
internal/api/  Health route, WebSocket hub, validation, and tests
internal/model/ Chat messages, connections, and socket input/output models
internal/store/ SQLite schema, message storage, and persistence tests
internal/bot/   Bot commands, sidecar protocol, and provider CLI adapters
internal/process/ Bounded subprocess execution and cancellation
frontend/      Svelte chatroom, Vite proxy, and frontend tests
```
