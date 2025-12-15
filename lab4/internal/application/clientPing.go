package application

import (
	snakespb "lab4/internal/infrastructure/proto"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) buildPingMessage() *snakespb.GameMessage {
	return &snakespb.GameMessage{
		SenderId: proto.Int32(c.node.SelfID),
		Type: &snakespb.GameMessage_Ping{
			Ping: &snakespb.GameMessage_PingMsg{},
		},
	}
}

func (c *GameClient) sendPingToPlayer(id int32) {
	c.playerAddrsMu.RLock()
	addr := c.playersAddr[id]
	c.playerAddrsMu.RUnlock()

	if addr == nil {
		return
	}

	msg := &snakespb.GameMessage{
		SenderId:   proto.Int32(c.node.SelfID),
		ReceiverId: proto.Int32(id),
		Type: &snakespb.GameMessage_Ping{
			Ping: &snakespb.GameMessage_PingMsg{},
		},
	}

	// Пинг можно не очень надёжно, 1 попытка достаточно
	go func() {
		retry := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
		if retry <= 0 {
			retry = 100 * time.Millisecond
		}
		_ = c.sendReliable(msg, addr, id, retry, 1)
	}()
}
