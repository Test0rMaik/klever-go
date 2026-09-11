package websocket

import "time"

// SubscribeUpgraderHandshakeTimeout exposes the /subscribe upgrader's write bound, which is
// set separately from NewUpgrader and would otherwise be unpinned.
func SubscribeUpgraderHandshakeTimeout() time.Duration {
	return upgrader.HandshakeTimeout
}
