// Package inventory is the server's item model: Stack (one slot, with the
// NBT fields the server tracks) and its wire Slot encoding, a player's
// window-0 inventory (Player), and the Click Container simulation (View,
// Drag, Claim) that keeps the model authoritative over the client's
// prediction. It knows nothing about connections; the server package applies
// the results and sends the packets.
package inventory
