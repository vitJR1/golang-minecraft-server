package bedwars

import (
	"fmt"
	"strings"

	"minecraft-server/game"
)

// shop.go is the villager item shop. Right-clicking any villager NPC placed
// by the arena config (teams[].villagers) opens a one-row chest GUI with the
// block offers below; clicking an offer takes the price from the player's
// inventory and hands over the goods. Team-coloured goods (wool, terracotta)
// are resolved from the buyer's team at click time.

// shopOffer is one purchasable stack. Goods is a namespaced item id, or one
// of the team-colour placeholders resolved by resolveGoods.
type shopOffer struct {
	Goods string // item id, or "wool" / "terracotta" for team colour
	Count int    // units per purchase
	Cost  string // currency item id
	Price int    // units of Cost per purchase
	Label string // human name of the goods
}

// shopOffers is the catalogue, in GUI slot order.
var shopOffers = []shopOffer{
	{Goods: "wool", Count: 16, Cost: "minecraft:iron_ingot", Price: 4, Label: "Wool"},
	{Goods: "minecraft:oak_planks", Count: 8, Cost: "minecraft:gold_ingot", Price: 4, Label: "Oak Planks"},
	{Goods: "terracotta", Count: 8, Cost: "minecraft:iron_ingot", Price: 24, Label: "Terracotta"},
	{Goods: "minecraft:end_stone", Count: 8, Cost: "minecraft:gold_ingot", Price: 8, Label: "End Stone"},
	{Goods: "minecraft:obsidian", Count: 4, Cost: "minecraft:emerald", Price: 6, Label: "Obsidian"},
}

// currencyName turns "minecraft:iron_ingot" into "Iron" for labels.
func currencyName(item string) string {
	switch item {
	case "minecraft:iron_ingot":
		return "Iron"
	case "minecraft:gold_ingot":
		return "Gold"
	case "minecraft:diamond":
		return "Diamond"
	case "minecraft:emerald":
		return "Emerald"
	default:
		return strings.TrimPrefix(item, "minecraft:")
	}
}

// resolveGoods maps a team-colour placeholder to the team's item: the wool
// block's name doubles as the item id, and terracotta shares the colour
// prefix ("minecraft:red_wool" → "minecraft:red_terracotta").
func resolveGoods(goods string, t Team) string {
	switch goods {
	case "wool":
		return t.Wool.Name
	case "terracotta":
		return strings.TrimSuffix(t.Wool.Name, "_wool") + "_terracotta"
	default:
		return goods
	}
}

// OnEntityInteract opens the shop when a player right-clicks a villager.
func (g *bedWars) OnEntityInteract(ctx *game.Ctx, p game.PlayerHandle, ei game.EntityInteraction) bool {
	if ei.Type != "minecraft:villager" {
		return true
	}
	g.openShop(ctx, p)
	return false
}

// openShop shows the catalogue to p, with goods coloured for their team.
func (g *bedWars) openShop(_ *game.Ctx, p game.PlayerHandle) {
	team, ok := g.teamOf(p)
	if !ok {
		p.SendMessage("Join a team first.")
		return
	}
	items := make([]game.MenuItem, 0, len(shopOffers))
	for i, o := range shopOffers {
		items = append(items, game.MenuItem{
			Slot:  i,
			Item:  resolveGoods(o.Goods, team),
			Count: o.Count,
			Name:  fmt.Sprintf("%d× %s — %d %s", o.Count, o.Label, o.Price, currencyName(o.Cost)),
		})
	}
	p.OpenMenu("Item Shop", 1, items, func(slot int) { g.buy(p, slot) })
}

// buy performs the purchase for the offer at slot: price out, goods in.
func (g *bedWars) buy(p game.PlayerHandle, slot int) {
	if slot < 0 || slot >= len(shopOffers) {
		return
	}
	o := shopOffers[slot]
	team, ok := g.teamOf(p)
	if !ok {
		return
	}
	if have := p.CountItem(o.Cost); have < o.Price {
		p.SendMessage(fmt.Sprintf("Not enough %s: need %d, have %d.", currencyName(o.Cost), o.Price, have))
		return
	}
	if !p.TakeItem(o.Cost, o.Price) {
		return
	}
	goods := resolveGoods(o.Goods, team)
	p.GiveItem(goods, o.Count)
	p.SendMessage(fmt.Sprintf("Bought %d× %s for %d %s.", o.Count, o.Label, o.Price, currencyName(o.Cost)))
}

// teamOf returns the player's Team, if they're on one.
func (g *bedWars) teamOf(p game.PlayerHandle) (Team, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byEntity[p.EntityID()]
	if !ok {
		return Team{}, false
	}
	return g.teams[id].team, true
}
