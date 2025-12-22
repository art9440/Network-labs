package application

import (
	snakespb "lab4/internal/infrastructure/proto"
	"net"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) buildErrorMessage(seq int64, reason string) *snakespb.GameMessage {
	return &snakespb.GameMessage{
		MsgSeq: proto.Int64(seq),
		Type: &snakespb.GameMessage_Error{
			Error: &snakespb.GameMessage_ErrorMsg{
				ErrorMessage: proto.String(reason),
			},
		},
	}
}

func (c *GameClient) handleError(
	msg *snakespb.GameMessage,
	m *snakespb.GameMessage_Error,
	addr *net.UDPAddr,
) {
	errText := m.Error.GetErrorMessage()
	if errText == "" {
		errText = "Неизвестная ошибка от узла " + addr.String()
	}

	c.log.Printf("ERROR from=%s seq=%d msg=%q",
		addr, msg.GetMsgSeq(), errText)

	if c.view != nil {
		c.view.ShowError(errText)
	}
}
