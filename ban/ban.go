package ban

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type Info struct {
	PlayerName string    `json:"playerName"`
	Reason     string    `json:"Reason"`
	BannedAt   time.Time `json:"BannedAt"`
	ExpiresAt  time.Time `json:"ExpiresAt"`
}

// rawEntry decodes the on-disk JSON form, which uses "YYYY-MM-DD HH:MM:SS"
// time strings rather than RFC 3339.
type rawEntry struct {
	PlayerName string `json:"playerName"`
	Reason     string `json:"Reason"`
	BannedAt   string `json:"BannedAt"`
	ExpiresAt  string `json:"ExpiresAt"`
}

const timeLayout = "2006-01-02 15:04:05"

var (
	mu   sync.RWMutex
	bans = map[string]*Info{}
)

// Load reads banlist.json (or another path) and replaces the in-memory ban
// set. Safe to call again to reload. A missing file is not an error — the
// server just has no bans.
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read ban file: %w", err)
	}
	var raw []rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse ban file: %w", err)
	}
	next := make(map[string]*Info, len(raw))
	for i, r := range raw {
		bannedAt, err := time.Parse(timeLayout, r.BannedAt)
		if err != nil {
			return fmt.Errorf("entry %d (%s): BannedAt %q: %w", i, r.PlayerName, r.BannedAt, err)
		}
		expiresAt, err := time.Parse(timeLayout, r.ExpiresAt)
		if err != nil {
			return fmt.Errorf("entry %d (%s): ExpiresAt %q: %w", i, r.PlayerName, r.ExpiresAt, err)
		}
		next[r.PlayerName] = &Info{
			PlayerName: r.PlayerName,
			Reason:     r.Reason,
			BannedAt:   bannedAt,
			ExpiresAt:  expiresAt,
		}
	}
	mu.Lock()
	bans = next
	mu.Unlock()
	return nil
}

// IsBanned returns the ban Info for a player if they are currently banned
// (entry exists and hasn't expired), otherwise nil.
func IsBanned(playerName string) *Info {
	mu.RLock()
	info, ok := bans[playerName]
	mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().After(info.ExpiresAt) {
		return nil
	}
	return info
}
