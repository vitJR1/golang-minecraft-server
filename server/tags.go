package server

import (
	"minecraft-server/protocol/packetid"
	"minecraft-server/server/net/registry"
)

// sendUpdateTags sends Update Tags with registry.ServerTags.
func (c *ClientConnection) sendUpdateTags() error {
	return c.safeWrite(packetid.CbPlayUpdateTags, registry.UpdateTagsPayload(registry.ServerTags))
}
