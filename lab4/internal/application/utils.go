package application

import snakespb "lab4/internal/infrastructure/proto"

func MsgTypeName(msg *snakespb.GameMessage) string {
	switch msg.Type.(type) {
	case *snakespb.GameMessage_Ping:
		return "Ping"
	case *snakespb.GameMessage_Steer:
		return "Steer"
	case *snakespb.GameMessage_Ack:
		return "Ack"
	case *snakespb.GameMessage_State:
		return "State"
	case *snakespb.GameMessage_Announcement:
		return "Announcement"
	case *snakespb.GameMessage_Join:
		return "Join"
	case *snakespb.GameMessage_Error:
		return "Error"
	case *snakespb.GameMessage_RoleChange:
		return "RoleChange"
	case *snakespb.GameMessage_Discover:
		return "Discover"
	default:
		return "Unknown"
	}
}
