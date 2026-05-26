package ban

import "time"

type Info struct {
	PlayerName string
	Reason     string
	ExpiresAt  time.Time
	BannedAt   time.Time
}

func IsBanned(playerName string) *Info {
	if playerName == "BannedPerson" {
		return &Info{
			PlayerName: playerName,
			Reason:     "Griefing",
			ExpiresAt:  time.Now().Add(24 * time.Hour),
			BannedAt:   time.Now(),
		}
	} else {
		return nil
	}
}
