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
