-- Core identity + moderation, per-mode match history + participation, raw
-- bedwars event log, and per-mode ELO ratings (rank is computed at read time
-- from rating). See store/ repos for the queries that consume these tables.

-- ---------------------------------------------------------------------------
-- Identity
-- ---------------------------------------------------------------------------
CREATE TABLE players (
    id         BIGSERIAL PRIMARY KEY,
    uuid       UUID        NOT NULL UNIQUE,
    username   TEXT        NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_players_username_lower ON players (lower(username));

-- ---------------------------------------------------------------------------
-- Moderation
-- ---------------------------------------------------------------------------
CREATE TABLE bans (
    id         BIGSERIAL PRIMARY KEY,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    reason     TEXT        NOT NULL DEFAULT '',
    issued_by  TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,                       -- NULL = permanent
    active     BOOLEAN     NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_bans_player_active ON bans (player_id, active);

CREATE TABLE mutes (
    id         BIGSERIAL PRIMARY KEY,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    reason     TEXT        NOT NULL DEFAULT '',
    issued_by  TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,                       -- NULL = permanent
    active     BOOLEAN     NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_mutes_player_active ON mutes (player_id, active);

-- ---------------------------------------------------------------------------
-- Match history (one row per finished game)
-- ---------------------------------------------------------------------------
CREATE TABLE bedwars_matches (
    id          BIGSERIAL PRIMARY KEY,
    map         TEXT        NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at    TIMESTAMPTZ,
    winner_team TEXT        NOT NULL DEFAULT ''
);

CREATE TABLE skywars_matches (
    id               BIGSERIAL PRIMARY KEY,
    map              TEXT        NOT NULL DEFAULT '',
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    winner_player_id BIGINT REFERENCES players (id) ON DELETE SET NULL
);

CREATE TABLE ffa_matches (
    id               BIGSERIAL PRIMARY KEY,
    map              TEXT        NOT NULL DEFAULT '',
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    winner_player_id BIGINT REFERENCES players (id) ON DELETE SET NULL
);

-- ---------------------------------------------------------------------------
-- Per-match per-player results (aggregated stats for one game)
-- ---------------------------------------------------------------------------
CREATE TABLE bedwars_players (
    id          BIGSERIAL PRIMARY KEY,
    match_id    BIGINT  NOT NULL REFERENCES bedwars_matches (id) ON DELETE CASCADE,
    player_id   BIGINT  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    kills       INT     NOT NULL DEFAULT 0,
    deaths      INT     NOT NULL DEFAULT 0,
    final_kills INT     NOT NULL DEFAULT 0,
    beds_broken INT     NOT NULL DEFAULT 0,
    won         BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (match_id, player_id)
);
CREATE INDEX idx_bedwars_players_player ON bedwars_players (player_id);

CREATE TABLE skywars_players (
    id        BIGSERIAL PRIMARY KEY,
    match_id  BIGINT  NOT NULL REFERENCES skywars_matches (id) ON DELETE CASCADE,
    player_id BIGINT  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    kills     INT     NOT NULL DEFAULT 0,
    deaths    INT     NOT NULL DEFAULT 0,
    won       BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (match_id, player_id)
);
CREATE INDEX idx_skywars_players_player ON skywars_players (player_id);

CREATE TABLE ffa_players (
    id        BIGSERIAL PRIMARY KEY,
    match_id  BIGINT  NOT NULL REFERENCES ffa_matches (id) ON DELETE CASCADE,
    player_id BIGINT  NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    kills     INT     NOT NULL DEFAULT 0,
    deaths    INT     NOT NULL DEFAULT 0,
    won       BOOLEAN NOT NULL DEFAULT FALSE,
    UNIQUE (match_id, player_id)
);
CREATE INDEX idx_ffa_players_player ON ffa_players (player_id);

-- ---------------------------------------------------------------------------
-- Raw bedwars event log. During a match the server appends one row per
-- gameplay event (kill / death / bed_break / final_kill); after the match a
-- job aggregates these into bedwars_players. Append-only on the hot path.
-- ---------------------------------------------------------------------------
CREATE TABLE bedwars_events (
    id         BIGSERIAL PRIMARY KEY,
    match_id   BIGINT      NOT NULL REFERENCES bedwars_matches (id) ON DELETE CASCADE,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    type       TEXT        NOT NULL,                 -- kill | death | bed_break | final_kill
    target_id  BIGINT      REFERENCES players (id) ON DELETE SET NULL, -- victim for kills
    data       JSONB       NOT NULL DEFAULT '{}',    -- optional extra payload
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bedwars_events_match ON bedwars_events (match_id);
CREATE INDEX idx_bedwars_events_player ON bedwars_events (player_id);

-- ---------------------------------------------------------------------------
-- Per-mode ELO ratings. Rank (position / tier) is derived from rating at read
-- time via a window function — see the *RatingRepo leaderboard queries.
-- ---------------------------------------------------------------------------
CREATE TABLE bedwars_ratings (
    player_id  BIGINT      PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    rating     INT         NOT NULL DEFAULT 1000,
    games      INT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bedwars_ratings_rating ON bedwars_ratings (rating DESC);

CREATE TABLE skywars_ratings (
    player_id  BIGINT      PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    rating     INT         NOT NULL DEFAULT 1000,
    games      INT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_skywars_ratings_rating ON skywars_ratings (rating DESC);

CREATE TABLE ffa_ratings (
    player_id  BIGINT      PRIMARY KEY REFERENCES players (id) ON DELETE CASCADE,
    rating     INT         NOT NULL DEFAULT 1000,
    games      INT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_ffa_ratings_rating ON ffa_ratings (rating DESC);
