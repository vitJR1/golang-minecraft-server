package bedwars

import (
	"fmt"
	"strings"

	"minecraft-server/game"
)

// upgrades.go is the team-upgrade villager (Hypixel "Team Upgrades"): one
// 4-row menu with the five permanent upgrades on the top row and the trap
// queue below. Upgrades cost diamonds and apply to every member at once —
// Sharpened Swords / Reinforced Armor re-enchant the items players already
// hold, Maniac Miner applies haste, Forge speeds the team's own generators
// (bedwars.go runGenerators), Heal Pool regenerates members near their base
// (traps.go). Traps queue up to three deep at 1 / 2 / 4 diamonds.

const (
	upgradeRows = 4
	// Upgrade slots (row 1).
	slotSharpened = 10
	slotArmor     = 11
	slotMiner     = 12
	slotForge     = 13
	slotHealPool  = 14
	// Trap purchase slots (row 2) and the queue display (row 3).
	slotTrapFirst  = 19
	slotQueueFirst = 29
	maxTraps       = 3
)

// upgradeDef is one tiered team upgrade.
type upgradeDef struct {
	Key    string
	Label  string
	Icon   string
	Desc   []string
	Prices []int // diamonds per tier (len = max tier)
	// tierOf / apply read and advance the team's state.
	tierOf func(ts *teamState) int
	apply  func(ts *teamState, tier int)
	// tierName renders a tier for lore ("Sharpness I", "+50% Resources").
	tierName func(tier int) string
}

var upgradeDefs = map[int]upgradeDef{
	slotSharpened: {
		Key: "sharpened", Label: "§aSharpened Swords", Icon: "minecraft:iron_sword",
		Desc:     []string{"Your team permanently gains", "Sharpness I on all swords!"},
		Prices:   []int{4},
		tierOf:   func(ts *teamState) int { return boolTier(ts.sharpened) },
		apply:    func(ts *teamState, _ int) { ts.sharpened = true },
		tierName: func(int) string { return "Sharpness I" },
	},
	slotArmor: {
		Key: "armor", Label: "§aReinforced Armor", Icon: "minecraft:iron_chestplate",
		Desc:     []string{"Your team permanently gains", "Protection on all armor pieces!"},
		Prices:   []int{2, 4, 8, 16},
		tierOf:   func(ts *teamState) int { return ts.protection },
		apply:    func(ts *teamState, tier int) { ts.protection = tier },
		tierName: func(tier int) string { return "Protection " + roman(tier) },
	},
	slotMiner: {
		Key: "miner", Label: "§aManiac Miner", Icon: "minecraft:golden_pickaxe",
		Desc:     []string{"All players on your team", "permanently gain Haste."},
		Prices:   []int{2, 4},
		tierOf:   func(ts *teamState) int { return ts.haste },
		apply:    func(ts *teamState, tier int) { ts.haste = tier },
		tierName: func(tier int) string { return "Haste " + roman(tier) },
	},
	slotForge: {
		Key: "forge", Label: "§aIron Forge", Icon: "minecraft:furnace",
		Desc:   []string{"Upgrade resource spawning on", "your island."},
		Prices: []int{2, 4, 6, 8},
		tierOf: func(ts *teamState) int { return ts.forge },
		apply:  func(ts *teamState, tier int) { ts.forge = tier },
		tierName: func(tier int) string {
			return []string{"", "+50% Resources", "+100% Resources", "Spawn emeralds", "+200% Resources"}[tier]
		},
	},
	slotHealPool: {
		Key: "healpool", Label: "§aHeal Pool", Icon: "minecraft:beacon",
		Desc:     []string{"Creates a Regeneration field", "around your base!"},
		Prices:   []int{1},
		tierOf:   func(ts *teamState) int { return boolTier(ts.healPool) },
		apply:    func(ts *teamState, _ int) { ts.healPool = true },
		tierName: func(int) string { return "Regeneration I at base" },
	},
}

// upgradeOrder is the display order of the top row.
var upgradeOrder = []int{slotSharpened, slotArmor, slotMiner, slotForge, slotHealPool}

func boolTier(b bool) int {
	if b {
		return 1
	}
	return 0
}

// roman renders 1..5 as a Roman numeral.
func roman(n int) string {
	if n < 1 || n > 5 {
		return fmt.Sprint(n)
	}
	return []string{"I", "II", "III", "IV", "V"}[n-1]
}

// trapKind is one queued base trap.
type trapKind int

const (
	trapBlind   trapKind = iota // It's a Trap!: Blindness + Slowness on the intruder
	trapCounter                 // Counter-Offensive: Speed + Jump Boost for defenders
	trapAlarm                   // Alarm: reveal invisible intruders, alert the team
	trapFatigue                 // Miner Fatigue: Mining Fatigue on the intruder
)

// trapDef describes a trap for the shop and the alert.
type trapDef struct {
	Kind  trapKind
	Label string
	Icon  string
	Desc  []string
}

var trapDefs = []trapDef{
	{trapBlind, "§aIt's a Trap!", "minecraft:tripwire_hook", []string{"Inflicts Blindness and Slowness", "for 8 seconds."}},
	{trapCounter, "§aCounter-Offensive Trap", "minecraft:feather", []string{"Grants Speed II and Jump Boost II", "for 15 seconds to allied players", "near your base."}},
	{trapAlarm, "§aAlarm Trap", "minecraft:redstone_torch", []string{"Reveals invisible players as", "well as their name and team."}},
	{trapFatigue, "§aMiner Fatigue Trap", "minecraft:iron_pickaxe", []string{"Inflicts Mining Fatigue", "for 10 seconds."}},
}

func (k trapKind) def() trapDef { return trapDefs[k] }

// trapPrice is the diamond cost of the n-th queued trap (0-based).
func trapPrice(queued int) int { return []int{1, 2, 4}[min(queued, maxTraps-1)] }

// openUpgradeShop shows the team-upgrade menu to p.
func (g *bedWars) openUpgradeShop(ctx *game.Ctx, p game.PlayerHandle) {
	ts, _, ok := g.playerState(p)
	if !ok {
		p.SendMessage("Join a team first.")
		return
	}
	diamonds := p.CountItem(Diamond.item())
	var items []game.MenuItem

	g.mu.Lock()
	for _, slot := range upgradeOrder {
		def := upgradeDefs[slot]
		tier := def.tierOf(ts)
		lore := make([]string, 0, len(def.Desc)+6)
		for _, d := range def.Desc {
			lore = append(lore, "§7"+d)
		}
		lore = append(lore, "")
		for t := 1; t <= len(def.Prices); t++ {
			mark := "§7"
			if t <= tier {
				mark = "§a"
			}
			lore = append(lore, fmt.Sprintf("%sTier %d: %s, §b%d Diamond", mark, t, def.tierName(t), def.Prices[t-1]))
		}
		lore = append(lore, "")
		if tier >= len(def.Prices) {
			lore = append(lore, "§aUNLOCKED")
		} else if diamonds >= def.Prices[tier] {
			lore = append(lore, "§eClick to purchase!")
		} else {
			lore = append(lore, "§cYou don't have enough Diamond!")
		}
		items = append(items, game.MenuItem{Slot: slot, Item: def.Icon, Name: def.Label, Lore: lore, Glint: tier > 0})
	}
	queued := len(ts.traps)
	for i, td := range trapDefs {
		lore := make([]string, 0, len(td.Desc)+4)
		for _, d := range td.Desc {
			lore = append(lore, "§7"+d)
		}
		lore = append(lore, "")
		switch {
		case queued >= maxTraps:
			lore = append(lore, "§cTrap queue full!")
		case diamonds >= trapPrice(queued):
			lore = append(lore, priceLine(Diamond, trapPrice(queued)), "§eClick to purchase!")
		default:
			lore = append(lore, priceLine(Diamond, trapPrice(queued)), "§cYou don't have enough Diamond!")
		}
		items = append(items, game.MenuItem{Slot: slotTrapFirst + i, Item: td.Icon, Name: td.Label, Lore: lore})
	}
	for i := 0; i < maxTraps; i++ {
		slot := slotQueueFirst + i
		if i < queued {
			td := ts.traps[i].def()
			items = append(items, game.MenuItem{Slot: slot, Item: td.Icon, Name: fmt.Sprintf("§7Trap #%d: %s", i+1, strings.TrimPrefix(td.Label, "§a")),
				Lore: []string{"§7Triggers when an enemy", "§7enters your base."}})
		} else {
			items = append(items, game.MenuItem{Slot: slot, Item: "minecraft:light_gray_stained_glass_pane", Name: fmt.Sprintf("§7Trap #%d: No Trap!", i+1),
				Lore: []string{fmt.Sprintf("§7The next trap will cost: §b%d Diamond", trapPrice(i))}})
		}
	}
	g.mu.Unlock()

	p.OpenMenu("Team Upgrades", upgradeRows, items, func(slot int) { g.upgradeClick(ctx, p, slot) })
}

// upgradeClick handles a click in the upgrade menu and reopens it.
func (g *bedWars) upgradeClick(ctx *game.Ctx, p game.PlayerHandle, slot int) {
	if _, ok := upgradeDefs[slot]; ok {
		g.buyUpgrade(ctx, p, slot)
	} else if slot >= slotTrapFirst && slot < slotTrapFirst+len(trapDefs) {
		g.buyTrap(ctx, p, trapKind(slot-slotTrapFirst))
	} else {
		return
	}
	g.openUpgradeShop(ctx, p)
}

// buyUpgrade advances one team upgrade a tier and applies it to every member.
func (g *bedWars) buyUpgrade(ctx *game.Ctx, p game.PlayerHandle, slot int) {
	def := upgradeDefs[slot]
	ts, _, ok := g.playerState(p)
	if !ok {
		return
	}
	g.mu.Lock()
	tier := def.tierOf(ts)
	g.mu.Unlock()
	if tier >= len(def.Prices) {
		p.SendMessage("§cYour team already has the highest tier!")
		return
	}
	if !g.pay(p, Diamond, def.Prices[tier]) {
		return
	}
	g.mu.Lock()
	def.apply(ts, tier+1)
	members := g.teamMembersLocked(ctx, ts)
	g.mu.Unlock()

	for _, m := range members {
		g.applyTeamUpgrades(m, ts)
	}
	label := strings.TrimPrefix(def.Label, "§a")
	for _, m := range members {
		m.SendMessage(fmt.Sprintf("§6%s §apurchased §6%s §7(%s)", p.Name(), label, def.tierName(tier+1)))
		m.PlaySound("minecraft:block.anvil.use", 1, 1.2)
	}
}

// buyTrap appends a trap to the team's queue.
func (g *bedWars) buyTrap(ctx *game.Ctx, p game.PlayerHandle, kind trapKind) {
	ts, _, ok := g.playerState(p)
	if !ok {
		return
	}
	g.mu.Lock()
	queued := len(ts.traps)
	g.mu.Unlock()
	if queued >= maxTraps {
		p.SendMessage("§cYour trap queue is full!")
		return
	}
	if !g.pay(p, Diamond, trapPrice(queued)) {
		return
	}
	g.mu.Lock()
	ts.traps = append(ts.traps, kind)
	members := g.teamMembersLocked(ctx, ts)
	g.mu.Unlock()
	label := strings.TrimPrefix(kind.def().Label, "§a")
	for _, m := range members {
		m.SendMessage(fmt.Sprintf("§6%s §apurchased §6%s", p.Name(), label))
	}
}

// applyTeamUpgrades re-enchants a member's swords and armour to the team's
// Sharpened / Reinforced levels and (re)applies Maniac Miner haste.
func (g *bedWars) applyTeamUpgrades(p game.PlayerHandle, ts *teamState) {
	for _, st := range p.Inventory() {
		if !isSword(st.Item) && !isArmor(st.Item) {
			continue
		}
		upgraded := ts.withTeamUpgrades(st)
		if len(upgraded.Enchantments) != len(st.Enchantments) || !sameEnchants(upgraded, st) {
			p.SetSlot(st.Slot, upgraded)
		}
	}
	if ts.haste > 0 {
		p.ApplyEffect("haste", ts.haste, hasteDuration)
	}
}

func sameEnchants(a, b game.ItemStack) bool {
	for k, v := range a.Enchantments {
		if b.Enchantments[k] != v {
			return false
		}
	}
	return true
}

// teamMembersLocked returns the handles of ts's current members. Caller
// holds g.mu.
func (g *bedWars) teamMembersLocked(ctx *game.Ctx, ts *teamState) []game.PlayerHandle {
	var out []game.PlayerHandle
	for _, p := range ctx.Instance.Players() {
		if ts.members[p.EntityID()] {
			out = append(out, p)
		}
	}
	return out
}
