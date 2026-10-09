package bedwars

import (
	"fmt"
	"strings"

	"minecraft-server/game"
)

// shop.go is the villager item shop UI (Hypixel layout): a 6-row chest with
// the category tabs across the top row, a separator row, and the selected
// category's wares below. Prices come from shop_catalog.go; purchases are
// applied by buyItem (plain goods, armour / tool tiers, permanent items).
// The menu is rebuilt after every click so lore (affordability, next tool
// tier) stays current. The upgrade villager opens the team-upgrade shop
// (upgrades.go) instead.

const (
	shopRows      = 6
	shopTabSlot0  = 1  // first category tab (row 0, slots 1..7)
	shopSepRow    = 9  // row 1 is a separator
	shopFirstWare = 19 // row 2, column 1
	shopSeparator = "minecraft:gray_stained_glass_pane"
)

// wareSlots are the menu slots wares fill, in order: rows 2..5, columns 1..7.
var wareSlots = func() []int {
	var out []int
	for row := 2; row < shopRows; row++ {
		for col := 1; col <= 7; col++ {
			out = append(out, row*9+col)
		}
	}
	return out
}()

// OnEntityInteract opens the matching shop when a player right-clicks a
// villager: the arena's villager table says whether it's the item shop or
// the team upgrades; an unknown villager defaults to the item shop.
func (g *bedWars) OnEntityInteract(ctx *game.Ctx, p game.PlayerHandle, ei game.EntityInteraction) bool {
	if ei.Type != "minecraft:villager" {
		return true
	}
	kind := villagerItem
	if spot, ok := g.arena.villagerAt(ei.X, ei.Y, ei.Z); ok {
		kind = spot.Kind
	}
	if kind == villagerUpgrade {
		g.openUpgradeShop(ctx, p)
	} else {
		g.openShop(ctx, p, 0)
	}
	return false
}

// openShop shows category cat to p.
func (g *bedWars) openShop(ctx *game.Ctx, p game.PlayerHandle, cat int) {
	ts, k, ok := g.playerState(p)
	if !ok {
		p.SendMessage("Join a team first.")
		return
	}
	if cat < 0 || cat >= len(shopCategories) {
		cat = 0
	}
	items := make([]game.MenuItem, 0, 9+len(shopCategories[cat].Items))
	for i, c := range shopCategories {
		items = append(items, game.MenuItem{
			Slot: shopTabSlot0 + i, Item: c.Icon, Name: "§a" + c.Name,
			Lore:  []string{"§eClick to view!"},
			Glint: i == cat,
		})
	}
	for col := 0; col < 9; col++ {
		items = append(items, game.MenuItem{Slot: shopSepRow + col, Item: shopSeparator, Name: "§8↑ Categories", Lore: []string{"§8↓ Items"}})
	}
	for i, it := range shopCategories[cat].Items {
		if i >= len(wareSlots) {
			break
		}
		if mi, ok := g.wareMenuItem(p, ts, k, it, wareSlots[i]); ok {
			items = append(items, mi)
		}
	}
	title := fmt.Sprintf("Item Shop — %s", shopCategories[cat].Name)
	p.OpenMenu(title, shopRows, items, func(slot int) { g.shopClick(ctx, p, cat, slot) })
}

// wareMenuItem renders one catalogue entry for the buyer: resolves team
// colour, picks the next tool tier, and writes the price / status lore.
func (g *bedWars) wareMenuItem(p game.PlayerHandle, ts *teamState, k *kit, it shopItem, slot int) (game.MenuItem, bool) {
	icon, count := resolveGoods(it.Icon, ts.team), it.Count
	cost, price := it.Cost, it.Price
	label := it.Label
	var status []string

	switch it.Kind {
	case kindTool:
		cur := k.toolTierOf(it.Tool)
		if cur >= maxToolTier(it.Tool) {
			tier := toolTiers[it.Tool][cur-1]
			icon = tier.Item
			status = []string{"§aMax tier reached!"}
			cost, price = tier.Cost, 0
		} else {
			tier := toolTiers[it.Tool][cur]
			icon, cost, price = tier.Item, tier.Cost, tier.Price
			label = fmt.Sprintf("%s §7(Tier %d)", it.Label, cur+1)
		}
	case kindArmor:
		if k.armor >= it.Tier {
			status = []string{"§aAlready owned!"}
		}
	case kindPermanent:
		if it.Key == "shears" && k.shears {
			status = []string{"§aAlready owned!"}
		}
	}
	if count < 1 {
		count = 1
	}

	lore := make([]string, 0, len(it.Desc)+4)
	for _, d := range it.Desc {
		lore = append(lore, "§7"+d)
	}
	if len(status) > 0 {
		lore = append(lore, "", status[0])
	} else {
		lore = append(lore, "", priceLine(cost, price))
		if p.CountItem(cost.item()) >= price {
			lore = append(lore, "§eClick to purchase!")
		} else {
			lore = append(lore, fmt.Sprintf("§cYou don't have enough %s!", cost.String()))
		}
	}
	return game.MenuItem{Slot: slot, Item: icon, Count: count, Name: label, Lore: lore, Glint: hasEnchant(it, ts)}, true
}

// hasEnchant reports whether the ware is shown with a shimmer: enchanted
// goods, or swords/armour the team has upgraded.
func hasEnchant(it shopItem, ts *teamState) bool {
	if it.Stack != nil {
		st := it.Stack(ts.team)
		if len(st.Enchantments) > 0 {
			return true
		}
	}
	return (isSword(it.Icon) && ts.sharpened) || (it.Kind == kindArmor && ts.protection > 0)
}

// shopClick dispatches a click: tab → switch category, ware → buy. The menu
// is reopened afterwards so lore reflects the new balance.
func (g *bedWars) shopClick(ctx *game.Ctx, p game.PlayerHandle, cat, slot int) {
	if slot >= shopTabSlot0 && slot < shopTabSlot0+len(shopCategories) {
		g.openShop(ctx, p, slot-shopTabSlot0)
		return
	}
	for i, s := range wareSlots {
		if s != slot {
			continue
		}
		if i < len(shopCategories[cat].Items) {
			g.buyItem(p, shopCategories[cat].Items[i])
			g.openShop(ctx, p, cat)
		}
		return
	}
}

// buyItem performs a purchase: price out, goods in (per kind). Messages the
// buyer on failure.
func (g *bedWars) buyItem(p game.PlayerHandle, it shopItem) {
	ts, k, ok := g.playerState(p)
	if !ok {
		return
	}
	switch it.Kind {
	case kindTool:
		cur := k.toolTierOf(it.Tool)
		if cur >= maxToolTier(it.Tool) {
			p.SendMessage("§cYou already have the best tier!")
			return
		}
		tier := toolTiers[it.Tool][cur]
		if !g.pay(p, tier.Cost, tier.Price) {
			return
		}
		next, _ := toolStack(it.Tool, cur+1)
		g.mu.Lock()
		k.setToolTier(it.Tool, cur+1)
		g.mu.Unlock()
		g.replaceTool(p, it.Tool, next)
		p.SendMessage(fmt.Sprintf("§aYou purchased §6%s §7(Tier %d)", strings.TrimPrefix(it.Label, "§a"), cur+1))
	case kindArmor:
		if k.armor >= it.Tier {
			p.SendMessage("§cYou already have this or better armor!")
			return
		}
		if !g.pay(p, it.Cost, it.Price) {
			return
		}
		g.mu.Lock()
		k.armor = it.Tier
		g.mu.Unlock()
		pieces := armorPieces[it.Tier]
		p.SetSlot(game.SlotLeggings, ts.armorPiece(pieces[0]))
		p.SetSlot(game.SlotBoots, ts.armorPiece(pieces[1]))
		p.SendMessage("§aYou purchased §6" + strings.TrimPrefix(it.Label, "§a"))
	case kindPermanent:
		if it.Key == "shears" && k.shears {
			p.SendMessage("§cYou already own this!")
			return
		}
		if !g.pay(p, it.Cost, it.Price) {
			return
		}
		g.mu.Lock()
		if it.Key == "shears" {
			k.shears = true
		}
		g.mu.Unlock()
		p.GiveStack(it.Stack(ts.team))
		p.SendMessage("§aYou purchased §6" + strings.TrimPrefix(it.Label, "§a"))
	default:
		if !g.pay(p, it.Cost, it.Price) {
			return
		}
		st := game.ItemStack{Item: resolveGoods(it.Icon, ts.team), Count: it.Count}
		if it.Stack != nil {
			st = it.Stack(ts.team)
		}
		st = ts.withTeamUpgrades(st)
		if left := p.GiveStack(st); left > 0 {
			// Inventory full: drop the remainder at the player's feet.
			pose := p.Pose()
			g.ctx.Instance.DropItem(pose.X, pose.Y+0.5, pose.Z, st.Item, left)
		}
		p.SendMessage(fmt.Sprintf("§aYou purchased §6%d× %s", it.Count, strings.TrimPrefix(it.Label, "§a")))
	}
	p.PlaySound("minecraft:entity.item.pickup", 1, 1)
}

// pay charges price units of cost, or tells the buyer why not.
func (g *bedWars) pay(p game.PlayerHandle, cost Resource, price int) bool {
	if have := p.CountItem(cost.item()); have < price {
		p.SendMessage(fmt.Sprintf("§cYou don't have enough %s! Need %d, have %d.", cost.String(), price, have))
		p.PlaySound("minecraft:entity.villager.no", 1, 1)
		return false
	}
	return p.TakeItem(cost.item(), price)
}

// replaceTool swaps the player's existing pickaxe/axe for the new tier in
// place (or gives it if they hold none).
func (g *bedWars) replaceTool(p game.PlayerHandle, kind toolKind, next game.ItemStack) {
	suffix := "_pickaxe"
	if kind == toolAxe {
		suffix = "_axe"
	}
	for _, st := range p.Inventory() {
		if strings.HasSuffix(st.Item, suffix) && !strings.HasSuffix(st.Item, "_pickaxe"+"_") {
			p.SetSlot(st.Slot, next)
			return
		}
	}
	p.GiveStack(next)
}

// playerState resolves the player's team and kit.
func (g *bedWars) playerState(p game.PlayerHandle) (*teamState, *kit, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.byEntity[p.EntityID()]
	if !ok {
		return nil, nil, false
	}
	return g.teams[id], g.kitOfLocked(p.EntityID()), true
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
