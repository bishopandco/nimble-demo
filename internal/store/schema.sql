CREATE TABLE IF NOT EXISTS chat_connections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id INTEGER NOT NULL REFERENCES chat_connections(id),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
    message TEXT NOT NULL CHECK (length(message) BETWEEN 1 AND 500),
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_bot_messages (
    message_id INTEGER PRIMARY KEY REFERENCES chat_messages(id),
    prompt_id INTEGER NOT NULL REFERENCES chat_messages(id),
    provider TEXT NOT NULL CHECK (provider IN ('codex', 'claude'))
);

CREATE TABLE IF NOT EXISTS bot_calls (
    prompt_id INTEGER PRIMARY KEY REFERENCES chat_messages(id),
    owner_pid INTEGER NOT NULL,
    provider TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'failed', 'canceled', 'interrupted')),
    queued_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    pid INTEGER NOT NULL DEFAULT 0,
    provider_pid INTEGER NOT NULL DEFAULT 0,
    memory_bytes INTEGER,
    cpu_percent REAL,
    process_count INTEGER NOT NULL DEFAULT 0,
    sampled_at TEXT,
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS bot_call_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    call_id INTEGER NOT NULL REFERENCES bot_calls(prompt_id),
    kind TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS bot_call_events_call ON bot_call_events(call_id, id);
