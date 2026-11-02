package command

import (
	"fmt"
	"strconv"
	"strings"

	"minecraft-server/player"
)

// args.go: parsers for the argument kinds the built-in commands share.

// ParseOnOff accepts the usual truthy/falsy words for a boolean argument.
func ParseOnOff(s string) (value, ok bool) {
	switch strings.ToLower(s) {
	case "on", "true", "yes", "1", "enable", "enabled":
		return true, true
	case "off", "false", "no", "0", "disable", "disabled":
		return false, true
	default:
		return false, false
	}
}

// OnOff renders a boolean the way ParseOnOff reads it back.
func OnOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// ParseGamemode reads a gamemode by name, vanilla short name or number.
func ParseGamemode(s string) (player.Gamemode, bool) {
	switch strings.ToLower(s) {
	case "survival", "s", "0":
		return player.Survival, true
	case "creative", "c", "1":
		return player.Creative, true
	case "adventure", "a", "2":
		return player.Adventure, true
	case "spectator", "sp", "3":
		return player.Spectator, true
	}
	return 0, false
}

// ParseCoords reads three numbers as x, y, z; the error names the first bad
// axis and its text ("Bad Y: abc") and is meant for the sender.
func ParseCoords(args []string) (x, y, z float64, err error) {
	if len(args) != 3 {
		return 0, 0, 0, fmt.Errorf("need x y z")
	}
	var v [3]float64
	for i, axis := range []string{"X", "Y", "Z"} {
		if v[i], err = strconv.ParseFloat(args[i], 64); err != nil {
			return 0, 0, 0, fmt.Errorf("Bad %s: %s", axis, args[i])
		}
	}
	return v[0], v[1], v[2], nil
}

// ArgFrom completes argument n (1-based) with list(sender); other
// positions get nothing.
func ArgFrom[S any](n int, list func(S) []string) func(S, []string) []string {
	return func(s S, args []string) []string {
		if len(args) == n {
			return list(s)
		}
		return nil
	}
}

// FixedArg completes argument n from a fixed list.
func FixedArg[S any](n int, words ...string) func(S, []string) []string {
	return func(_ S, args []string) []string {
		if len(args) == n {
			return words
		}
		return nil
	}
}
