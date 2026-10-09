-- Per-player ender chest contents. Keyed by the player's UUID (not the
-- players row) so it works for whichever UUID the player connects with and
-- doesn't need a join; slots is the server's JSON encoding of the 27 slots.
CREATE TABLE ender_chests (
    uuid       UUID        PRIMARY KEY,
    slots      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
