package application

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"
)

// pendingItem — одно сообщение, ожидающее Ack
type pendingItem struct {
	seq    int64
	msg    *snakespb.GameMessage
	addr   *net.UDPAddr
	peerID int32

	retryEvery time.Duration
	nextSend   time.Time

	attemptsLeft int

	done chan error // закрываем ошибкой/успехом
}

// completePending — завершает pending по Ack или ошибке
func (c *GameClient) completePending(seq int64, err error) {
	c.pendingMu.Lock()
	pi, ok := c.pending[seq]
	if ok {
		delete(c.pending, seq)
	}
	c.pendingMu.Unlock()

	if !ok || pi == nil {
		return
	}

	// не close(ch) молча, а сигналим результат
	select {
	case pi.done <- err:
	default:
	}
	close(pi.done)
}

// state_delay / 10
func (c *GameClient) reliabilityInterval() time.Duration {
	if c.game == nil {
		return 50 * time.Millisecond
	}

	d := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
	if d < 20*time.Millisecond {
		d = 20 * time.Millisecond
	}
	return d
}

// startReliabilityLoop — запускает фоновый цикл ретраев pending-сообщений
func (c *GameClient) startReliabilityLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 20 * time.Millisecond
	}

	if c.reliabilityStop != nil {
		close(c.reliabilityStop)
	}
	stop := make(chan struct{})
	c.reliabilityStop = stop

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.reliabilityTick()
			case <-stop:
				return
			}
		}
	}()
}

// reliabilityTick — один тик reliable-механизма:
//   - выбираем сообщения, которые пора ретраить
//   - уменьшаем attempts
//   - шлём через send workers
func (c *GameClient) reliabilityTick() {
	now := time.Now()

	// соберём due отправки без удержания lock во время сетевых операций
	var due []*pendingItem

	c.pendingMu.Lock()
	for _, pi := range c.pending {
		if now.Before(pi.nextSend) {
			continue
		}
		due = append(due, pi)

		// планируем следующую попытку
		pi.nextSend = now.Add(pi.retryEvery)

		if pi.attemptsLeft > 0 {
			pi.attemptsLeft--
			if pi.attemptsLeft == 0 {
				// исчерпали попытки — завершаем с ошибкой, убираем из pending
				delete(c.pending, pi.seq)

				select {
				case pi.done <- fmt.Errorf("no ack seq=%d", pi.seq):
				default:
				}
				close(pi.done)
			}
		}
	}
	c.pendingMu.Unlock()

	// отправляем всё, что due (через send workers)
	for _, pi := range due {
		select {
		case <-pi.done:
			// done уже закрыт/прочитан — пропускаем
			continue
		default:
		}

		// отправка: просто UDP send
		c.enqueueUnreliable(pi.msg, pi.addr, pi.peerID)
	}
}

// clearAllPending — аварийно завершаем все pending
// используется при выходе из игры / смене роли
func (c *GameClient) clearAllPending(reason error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for seq, pi := range c.pending {
		delete(c.pending, seq)
		if pi == nil {
			continue
		}
		select {
		case pi.done <- reason:
		default:
		}
		close(pi.done)
	}
}
