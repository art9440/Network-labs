package application

import (
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) handleSteer(steer *snakespb.GameMessage_SteerMsg, msg *snakespb.GameMessage) {
	if c.node.SelfRole != snakespb.NodeRole_MASTER {
		return
	}
	if c.game == nil || steer == nil {
		return
	}

	playerID := msg.GetSenderId()
	if playerID == 0 {
		return
	}

	var dir domain.Direction
	switch steer.GetDirection() {
	case snakespb.Direction_UP:
		dir = domain.DirUp
	case snakespb.Direction_DOWN:
		dir = domain.DirDown
	case snakespb.Direction_LEFT:
		dir = domain.DirLeft
	case snakespb.Direction_RIGHT:
		dir = domain.DirRight
	default:
		return
	}

	c.game.SetDirection(dir, playerID)
}

func (c *GameClient) buildSteerMessage(dir domain.Direction) *snakespb.GameMessage {
	var pdir snakespb.Direction
	switch dir {
	case domain.DirUp:
		pdir = snakespb.Direction_UP
	case domain.DirDown:
		pdir = snakespb.Direction_DOWN
	case domain.DirLeft:
		pdir = snakespb.Direction_LEFT
	case domain.DirRight:
		pdir = snakespb.Direction_RIGHT
	default:
		pdir = snakespb.Direction_UP
	}

	return &snakespb.GameMessage{
		SenderId: proto.Int32(c.node.SelfID),
		Type: &snakespb.GameMessage_Steer{
			Steer: &snakespb.GameMessage_SteerMsg{
				Direction: &pdir,
			},
		},
	}
}
