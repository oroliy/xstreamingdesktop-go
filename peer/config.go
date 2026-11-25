package peer

import "github.com/pion/webrtc/v4"

// DefaultRTCConfiguration provides STUN alternatives used by the original client.
// STUN discovers public addresses; it is not a TURN relay.
func DefaultRTCConfiguration() webrtc.Configuration {
	return webrtc.Configuration{ICEServers: []webrtc.ICEServer{
		{URLs: []string{"stun:worldaz.relay.teams.microsoft.com:3478"}},
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun.douyucdn.cn:18000"}},
	}}
}
