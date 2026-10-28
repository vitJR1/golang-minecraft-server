-- Anticheat punishment ladder (anticheat/punish.go). Keyed by the player's
-- UUID like ender_chests, so a punishment never needs the players row.
-- anticheat_status is the current step (0 clean … 5 max); only an admin's
-- /ac reset lowers it.
CREATE TABLE anticheat_status (
    uuid       UUID        PRIMARY KEY,
    status     SMALLINT    NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per punishment (kick / ban / reset), with the reasons: the checks
-- and evidence that led to it, as JSON [{check, detail, at, vl}, …].
CREATE TABLE anticheat_punishments (
    id            BIGSERIAL   PRIMARY KEY,
    uuid          UUID        NOT NULL,
    username      TEXT        NOT NULL,
    at            TIMESTAMPTZ NOT NULL,
    instance      TEXT        NOT NULL,
    status_before SMALLINT    NOT NULL,
    status_after  SMALLINT    NOT NULL,
    action        TEXT        NOT NULL,
    ban_until     TIMESTAMPTZ,
    flag_admin    BOOLEAN     NOT NULL DEFAULT false,
    reasons       JSONB       NOT NULL,
    issued_by     TEXT        NOT NULL
);

CREATE INDEX anticheat_punishments_uuid_at ON anticheat_punishments (uuid, at DESC);
CREATE INDEX anticheat_punishments_username ON anticheat_punishments (lower(username), at DESC);
