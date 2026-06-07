-- Optional per-player password hash, so credentials can live in Postgres
-- instead of (or alongside) the file-based auth store. NULL = no password set.
-- Store only a hash (e.g. bcrypt) here — never the plaintext password.
ALTER TABLE players ADD COLUMN password_hash TEXT;
