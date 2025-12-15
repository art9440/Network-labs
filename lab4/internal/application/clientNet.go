package application

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"
)

func (c *GameClient) HandlePacket(msg *snakespb.GameMessage, addr *net.UDPAddr) {
	senderID := msg.GetSenderId()
	if senderID != 0 {
		c.notePlayerHeard(senderID)
	}

	c.log.Printf("RECV from=%s seq=%d type=%s", addr, msg.GetMsgSeq(), MsgTypeName(msg))

	switch m := msg.Type.(type) {
	case *snakespb.GameMessage_Announcement:
		c.handleAnnouncement(m.Announcement, addr)
		if c.view != nil {
			c.view.RefreshGamesList()
			c.view.RefreshAvailableGames()
		}

	case *snakespb.GameMessage_Discover:
		msg := c.buildAnnouncementMessage()
		if msg == nil {
			return
		}
		if c.view != nil {
			_ = c.network.SendTo(msg, addr)
			// Discover не привязан к player_id, markSent не нужен
		}

	case *snakespb.GameMessage_Join:
		playerID, err := c.TryToJoin(m.Join, addr)
		if err != nil {
			ack := c.buildAckMessage(msg.GetMsgSeq(), 0)
			if err2 := c.network.SendTo(ack, addr); err2 == nil {
				// id игрока ещё нет – некого отмечать
			}

			errMsg := c.buildErrorMessage(msg.GetMsgSeq(), err.Error())
			if errMsg != nil {
				_ = c.network.SendTo(errMsg, addr)
			}
			return
		}

		ack := c.buildAckMessage(msg.GetMsgSeq(), playerID)
		if err := c.network.SendTo(ack, addr); err == nil {
			c.markSent(playerID)
		}

	case *snakespb.GameMessage_Ack:
		c.handleAck(msg)

	case *snakespb.GameMessage_Error:
		ack := c.buildAckMessage(msg.GetMsgSeq(), c.node.SelfID)
		if err := c.network.SendTo(ack, addr); err == nil {
			c.markSent(msg.GetSenderId())
		}
		c.handleError(msg, m, addr)

	case *snakespb.GameMessage_State:
		ack := c.buildAckMessage(msg.GetMsgSeq(), c.node.SelfID)
		if err := c.network.SendTo(ack, addr); err == nil {
			c.markSent(msg.GetSenderId())
		}
		c.handleState(m.State)

	case *snakespb.GameMessage_Steer:
		ack := c.buildAckMessage(msg.GetMsgSeq(), msg.GetSenderId())
		if err := c.network.SendTo(ack, addr); err == nil {
			c.markSent(msg.GetSenderId())
		}
		c.handleSteer(m.Steer, msg)

	case *snakespb.GameMessage_Ping:
		ack := c.buildAckMessage(msg.GetMsgSeq(), c.node.SelfID)
		if err := c.network.SendTo(ack, addr); err == nil {
			c.markSent(msg.GetSenderId())
		}
		c.notePlayerHeard(msg.GetSenderId())
	case *snakespb.GameMessage_RoleChange:

	default:
		c.log.Printf("unknown msg type: %T", m)
	}
}

func (c *GameClient) markSent(id int32) {
	if id == 0 {
		return
	}
	c.playerAddrsMu.Lock()
	defer c.playerAddrsMu.Unlock()

	if c.lastSent == nil {
		c.lastSent = make(map[int32]time.Time)
	}
	c.lastSent[id] = time.Now()
}

func (c *GameClient) notePlayerHeard(id int32) {
	if id == 0 {
		return
	}
	c.playerAddrsMu.Lock()
	defer c.playerAddrsMu.Unlock()

	if c.lastHeard == nil {
		c.lastHeard = make(map[int32]time.Time)
	}
	c.lastHeard[id] = time.Now()
}

func (c *GameClient) sendReliable(
	msg *snakespb.GameMessage,
	addr *net.UDPAddr,
	peerID int32, // <- добавили
	retryInterval time.Duration,
	maxAttempts int,
) error {
	seq := msg.GetMsgSeq()
	if seq == 0 {
		seq = c.nextSeq()
		msg.MsgSeq = &seq
	}

	ch := make(chan struct{})

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]chan struct{})
	}
	c.pending[seq] = ch
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, seq)
		c.pendingMu.Unlock()
	}()

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.network.SendTo(msg, addr); err != nil {
			return fmt.Errorf("send seq=%d attempt=%d: %w", seq, attempt+1, err)
		}

		if peerID != 0 {
			c.markSent(peerID)
		}

		select {
		case <-ch:
			return nil
		case <-time.After(retryInterval):
			// идём на следующую попытку
		}
	}

	return fmt.Errorf("no ack for seq=%d after %d attempts", seq, maxAttempts)
}

func (c *GameClient) startSendWorkers(n int) {
	for i := 0; i < n; i++ {
		go func(workerID int) {
			for {
				select {
				case job := <-c.sendQ:
					if job.addr == nil || job.msg == nil {
						continue
					}

					if job.reliable {
						_ = c.sendReliable(job.msg, job.addr, job.peerID, job.retryInterval, job.maxAttempts)
					} else {
						// “быстрая” отправка без ожидания Ack
						if err := c.network.SendTo(job.msg, job.addr); err == nil && job.peerID != 0 {
							c.markSent(job.peerID)
						}
					}

				case <-c.sendStop:
					return
				}
			}
		}(i)
	}
}

func (c *GameClient) enqueueReliable(msg *snakespb.GameMessage, addr *net.UDPAddr, peerID int32, retry time.Duration, attempts int) {
	select {
	case c.sendQ <- sendJob{
		msg: msg, addr: addr, peerID: peerID,
		retryInterval: retry, maxAttempts: attempts,
		reliable: true,
	}:
	default:
		// очередь переполнена — лучше дропнуть, чем плодить горутины
		c.log.Printf("sendQ overflow: drop reliable msg type=%s", MsgTypeName(msg))
	}
}

func (c *GameClient) enqueueUnreliable(msg *snakespb.GameMessage, addr *net.UDPAddr, peerID int32) {
	select {
	case c.sendQ <- sendJob{msg: msg, addr: addr, peerID: peerID, reliable: false}:
	default:
		c.log.Printf("sendQ overflow: drop msg type=%s", MsgTypeName(msg))
	}
}
