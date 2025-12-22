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

	dir, ok := fromProtoDirection(steer.GetDirection())
	if !ok {
		return
	}

	seq := msg.GetMsgSeq()

	c.steerMu.Lock()
	defer c.steerMu.Unlock()

	if c.lastSteerSeq == nil {
		c.lastSteerSeq = make(map[int32]int64)
	}
	if c.pendingSteer == nil {
		c.pendingSteer = make(map[int32]domain.Direction)
	}

	// В пределах тика принимаем только самый новый steer по msg_seq
	if prev := c.lastSteerSeq[playerID]; seq <= prev {
		return
	}

	c.lastSteerSeq[playerID] = seq
	c.pendingSteer[playerID] = dir
}

func fromProtoDirection(d snakespb.Direction) (domain.Direction, bool) {
	switch d {
	case snakespb.Direction_UP:
		return domain.DirUp, true
	case snakespb.Direction_DOWN:
		return domain.DirDown, true
	case snakespb.Direction_LEFT:
		return domain.DirLeft, true
	case snakespb.Direction_RIGHT:
		return domain.DirRight, true
	default:
		return 0, false
	}
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

func (c *GameClient) applyPendingSteer() {
	if c.node.SelfRole != snakespb.NodeRole_MASTER || c.game == nil {
		return
	}

	c.steerMu.Lock()
	defer c.steerMu.Unlock()

	for pid, dir := range c.pendingSteer {
		c.game.SetDirection(dir, pid)
	}

	// очищаем “тик-буфер”
	for k := range c.pendingSteer {
		delete(c.pendingSteer, k)
	}
	for k := range c.lastSteerSeq {
		delete(c.lastSteerSeq, k)
	}
}
