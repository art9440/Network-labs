package application

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"

	"google.golang.org/protobuf/proto"
)

// nextSeq — атомарно увеличивает и возвращает msg_seq
func (c *GameClient) nextSeq() int64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.seq++
	return c.seq
}

// markSent — отмечаем, что мы что-то отправили игроку
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

// sendReliable — синхронная надёжная отправка:
//   - шлём сообщение
//   - ждём Ack
//   - делаем ретраи
func (c *GameClient) sendReliable(
	msg *snakespb.GameMessage,
	addr *net.UDPAddr,
	peerID int32,
	retryInterval time.Duration,
	maxAttempts int,
) error {
	seq := msg.GetMsgSeq()
	if seq == 0 {
		seq = c.nextSeq()
		msg.MsgSeq = &seq
	}

	pi := &pendingItem{done: make(chan error, 1)}

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]*pendingItem)
	}
	c.pending[seq] = pi
	c.pendingMu.Unlock()

	// если выходим по таймауту/ошибке — сами завершим pending
	defer func() {
		// если ack уже пришёл, completePending уже удалил запись — ок
		c.pendingMu.Lock()
		_, still := c.pending[seq]
		c.pendingMu.Unlock()
		if still {
			c.completePending(seq, fmt.Errorf("no ack for seq=%d after %d attempts", seq, maxAttempts))
		}
	}()

	// цикл ретраев
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := c.network.SendTo(msg, addr); err != nil {
			c.completePending(seq, err)
			return fmt.Errorf("send seq=%d attempt=%d: %w", seq, attempt+1, err)
		}

		if peerID != 0 {
			c.markSent(peerID)
		}

		// ждём либо Ack, либо таймаут
		select {
		case err := <-pi.done:
			return err // nil если ok
		case <-time.After(retryInterval):
			// retry
		}
	}

	return fmt.Errorf("no ack for seq=%d after %d attempts", seq, maxAttempts)
}

// reliableSendAsync — асинхронная надёжная отправка:
//   - сразу возвращаемся
//   - Ack придёт в канал done
//   - ретраи делает reliability loop
func (c *GameClient) reliableSendAsync(
	msg *snakespb.GameMessage,
	addr *net.UDPAddr,
	peerID int32,
	retry time.Duration,
	attempts int,
) (int64, <-chan error, error) {

	if addr == nil || msg == nil {
		return 0, nil, fmt.Errorf("nil addr/msg")
	}

	seq := msg.GetMsgSeq()
	if seq == 0 {
		seq = c.nextSeq()
		msg.MsgSeq = &seq
	}

	if retry <= 0 {
		retry = 100 * time.Millisecond
	}

	// pendingItem хранит всё для повторных отправок
	pi := &pendingItem{
		seq:          seq,
		msg:          proto.Clone(msg).(*snakespb.GameMessage), // копия!
		addr:         copyUDPAddr(addr),
		peerID:       peerID,
		retryEvery:   retry,
		nextSend:     time.Now(), // сразу отправить
		attemptsLeft: attempts,
		done:         make(chan error, 1),
	}

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]*pendingItem)
	}
	c.pending[seq] = pi
	c.pendingMu.Unlock()

	//первая отправка сразу
	c.enqueueUnreliable(pi.msg, pi.addr, pi.peerID)

	return seq, pi.done, nil
}

func copyUDPAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// reliableSendWait — удобный helper:
// асинхронная отправка + ожидание Ack с общим таймаутом
func (c *GameClient) reliableSendWait(
	msg *snakespb.GameMessage,
	addr *net.UDPAddr,
	peerID int32,
	retry time.Duration,
	attempts int,
	waitTotal time.Duration, // общий таймаут ожидания (например 2*stateDelay)
) error {
	_, done, err := c.reliableSendAsync(msg, addr, peerID, retry, attempts)
	if err != nil {
		return err
	}

	if waitTotal <= 0 {
		waitTotal = 2 * time.Second
	}

	select {
	case e := <-done:
		return e
	case <-time.After(waitTotal):
		return fmt.Errorf("timeout waiting ack")
	}
}

// reroutePending — при смене мастера:
// все pending-сообщения старому мастеру
// перенаправляем новому мастеру
func (c *GameClient) reroutePending(oldPeerID, newPeerID int32, newAddr *net.UDPAddr) {
	if oldPeerID == 0 || newPeerID == 0 || newAddr == nil {
		return
	}

	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for _, pi := range c.pending {
		if pi == nil {
			continue
		}
		if pi.peerID != oldPeerID {
			continue
		}

		pi.peerID = newPeerID
		pi.addr = copyUDPAddr(newAddr)
		pi.nextSend = time.Now()

		//если сообщение было адресовано старому мастеру — перепишем receiver_id
		if pi.msg != nil && pi.msg.GetReceiverId() == oldPeerID {
			pi.msg.ReceiverId = proto.Int32(newPeerID)
		}
	}
}
