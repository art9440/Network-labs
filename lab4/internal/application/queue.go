package application

import (
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"
)

// sendJob — задача на отправку одного UDP сообщения
// reliable=true  -> отправляем через sendReliable (Ack + ретраи)
// reliable=false -> отправляем один раз (fire-and-forget)
type sendJob struct {
	msg           *snakespb.GameMessage
	addr          *net.UDPAddr
	peerID        int32
	retryInterval time.Duration
	maxAttempts   int
	reliable      bool
}

// startSendWorkers — запускает N воркеров, которые читают очередь sendQ и шлют UDP
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

// enqueueReliable — кладём надёжную отправку в очередь sendQ
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

// enqueueUnreliable — кладём быструю отправку в очередь sendQ (без Ack)
// используется для ping, первичной отправки pending и т.п.
func (c *GameClient) enqueueUnreliable(msg *snakespb.GameMessage, addr *net.UDPAddr, peerID int32) {
	select {
	case c.sendQ <- sendJob{msg: msg, addr: addr, peerID: peerID, reliable: false}:
	default:
		c.log.Printf("sendQ overflow: drop msg type=%s", MsgTypeName(msg))
	}
}
