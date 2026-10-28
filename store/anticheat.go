package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"minecraft-server/anticheat"
)

// AnticheatRepo persists the anticheat punishment ladder: the current status
// per player (anticheat_status) and the punishment log with its reasons
// (anticheat_punishments). Satisfies anticheat.Store.
type AnticheatRepo struct{ db *pgxpool.Pool }

func NewAnticheatRepo(db *pgxpool.Pool) *AnticheatRepo { return &AnticheatRepo{db: db} }

var _ anticheat.Store = (*AnticheatRepo)(nil)

// Status returns the player's status, StatusClean when never punished.
func (r *AnticheatRepo) Status(ctx context.Context, uuid string) (anticheat.Status, error) {
	var s int16
	err := r.db.QueryRow(ctx,
		`SELECT status FROM anticheat_status WHERE uuid = $1::uuid`, uuid).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return anticheat.StatusClean, nil
	}
	if err != nil {
		return 0, err
	}
	return anticheat.Status(s), nil
}

// Record appends rec and moves the status to rec.After in one transaction.
func (r *AnticheatRepo) Record(ctx context.Context, rec anticheat.Record) error {
	reasons, err := json.Marshal(rec.Reasons)
	if err != nil {
		return fmt.Errorf("encoding reasons: %w", err)
	}
	if rec.Reasons == nil {
		reasons = []byte("[]")
	}
	var banUntil *time.Time
	if !rec.BanUntil.IsZero() {
		banUntil = &rec.BanUntil
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO anticheat_punishments
			(uuid, username, at, instance, status_before, status_after, action,
			 ban_until, flag_admin, reasons, issued_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)`,
		rec.UUID, rec.Name, rec.At, rec.Instance, int16(rec.Before), int16(rec.After), rec.Action,
		banUntil, rec.FlagAdmin, reasons, rec.By); err != nil {
		return fmt.Errorf("inserting punishment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO anticheat_status (uuid, status) VALUES ($1::uuid, $2)
		ON CONFLICT (uuid) DO UPDATE SET status = EXCLUDED.status, updated_at = now()`,
		rec.UUID, int16(rec.After)); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	return tx.Commit(ctx)
}

// History returns the player's latest records, newest first.
func (r *AnticheatRepo) History(ctx context.Context, uuid string, limit int) ([]anticheat.Record, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, uuid::text, username, at, instance, status_before, status_after,
		       action, ban_until, flag_admin, reasons, issued_by
		FROM anticheat_punishments
		WHERE uuid = $1::uuid
		ORDER BY at DESC, id DESC
		LIMIT $2`, uuid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []anticheat.Record
	for rows.Next() {
		var (
			rec           anticheat.Record
			before, after int16
			banUntil      *time.Time
			reasons       []byte
		)
		if err := rows.Scan(&rec.ID, &rec.UUID, &rec.Name, &rec.At, &rec.Instance, &before, &after,
			&rec.Action, &banUntil, &rec.FlagAdmin, &reasons, &rec.By); err != nil {
			return nil, err
		}
		rec.Before, rec.After = anticheat.Status(before), anticheat.Status(after)
		if banUntil != nil {
			rec.BanUntil = *banUntil
		}
		if err := json.Unmarshal(reasons, &rec.Reasons); err != nil {
			return nil, fmt.Errorf("decoding reasons of punishment %d: %w", rec.ID, err)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// UUIDByName finds a player by the username on their latest punishment.
func (r *AnticheatRepo) UUIDByName(ctx context.Context, name string) (string, bool, error) {
	var uuid string
	err := r.db.QueryRow(ctx, `
		SELECT uuid::text FROM anticheat_punishments
		WHERE lower(username) = lower($1)
		ORDER BY at DESC, id DESC LIMIT 1`, name).Scan(&uuid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return uuid, true, nil
}
