package application

import (
	snakespb "lab4/internal/infrastructure/proto"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) buildAckMessage(
	origSeq int64,
	receiverID int32,
) *snakespb.GameMessage {

	return &snakespb.GameMessage{
		MsgSeq:     proto.Int64(origSeq),
		SenderId:   proto.Int32(c.node.SelfID),
		ReceiverId: proto.Int32(receiverID),
		Type: &snakespb.GameMessage_Ack{
			Ack: &snakespb.GameMessage_AckMsg{},
		},
	}
}

func (c *GameClient) handleAck(msg *snakespb.GameMessage) {
	ack := msg.GetAck()
	if ack == nil {
		return
	}

	// id, который мастер нам присвоил
	rid := msg.GetReceiverId()
	if rid != 0 && c.node.SelfID == 0 {
		c.node.SelfID = rid
		c.log.Printf("JOIN OK, my id = %d", rid)
	}

	c.completePending(msg.GetMsgSeq(), nil)
}
