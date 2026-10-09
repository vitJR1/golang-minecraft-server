package bedwars

import "minecraft-server/game"

// upgrades.go: the team-upgrade villager (Sharpened Swords, Reinforced
// Armor, Maniac Miner, Forge, Heal Pool, traps). The full shop lands with
// phase 2; for now the NPC acknowledges the click.

// trapKind is one queued base trap.
type trapKind int

// openUpgradeShop shows the team-upgrade menu to p.
func (g *bedWars) openUpgradeShop(_ *game.Ctx, p game.PlayerHandle) {
	p.SendMessage("§eTeam upgrades are coming soon.")
}
