-- Native files are owned by the login account, independently of characters.
CREATE TABLE account_keyconfigs (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slot SMALLINT NOT NULL CHECK(slot IN (0,1)),
    revision BIGINT NOT NULL CHECK(revision BETWEEN 1 AND 2147483646),
    data BYTEA NOT NULL CHECK(octet_length(data) BETWEEN 1 AND 524288),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,slot)
);
