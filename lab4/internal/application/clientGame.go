package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"time"
)

func (c *GameClient) StopGame() {
	if c.tickerStop != nil {
		close(c.tickerStop)
		c.tickerStop = nil
	}
	if c.monitorStop != nil {
		close(c.monitorStop)
		c.monitorStop = nil
	}
	if c.reliabilityStop != nil {
		close(c.reliabilityStop)
		c.reliabilityStop = nil
	}

	c.stopAnnouncement()
	c.stopSendWorkers()

	// чтобы после выхода не летели старые ретраи/ping’и
	c.clearAllPending(fmt.Errorf("game stopped"))

	c.game = nil

	c.setInGame(false)
}

func (c *GameClient) stopSendWorkers() {
	if c.sendStop == nil {
		return
	}
	select {
	case <-c.sendStop:
		// уже закрыт
	default:
		close(c.sendStop)
	}
	c.sendStop = nil
}

func (c *GameClient) ensureSendWorkers() {
	if c.sendStop != nil {
		return // уже есть живые воркеры
	}
	c.sendStop = make(chan struct{})
	c.startSendWorkers(4)
}

func (c *GameClient) makeSnakeZombie(id int32) {
	if c.game == nil {
		return
	}

	// Змея становится зомби, если есть
	if s, ok := c.game.Snakes[id]; ok && s != nil {
		s.State = domain.SnakeZombie
	}

	// Игрок перестаёт быть NORMAL, становится VIEWER
	if p, ok := c.game.Players[id]; ok && p != nil {
		p.Role = snakespb.NodeRole_VIEWER
	}
}

func (c *GameClient) StartGame() {
	// остановить старый цикл если был
	if c.tickerStop != nil {
		close(c.tickerStop)
	}
	fmt.Println(c.game.Tick)
	c.view.ShowGameScreen(c.game)

	c.tickerStop = make(chan struct{})
	delay := time.Millisecond * time.Duration(c.game.Tick)

	if delay <= 0 {
		delay = 200 * time.Millisecond
	}

	c.log.Printf("StartGame: delay=%v snakes=%d", delay, len(c.game.Snakes))
	go func() {
		ticker := time.NewTicker(time.Millisecond * time.Duration(c.game.Tick))
		defer ticker.Stop()

		c.startMonitorLoop()
		for {
			select {
			case <-ticker.C:
				if c.game == nil {
					continue
				}
				c.applyPendingSteer()
				alive := c.game.OneTick()
				c.broadcastState()
				c.view.RefreshBoard()
				c.view.RefreshRating()

				_ = alive
			case <-c.tickerStop:
				return
			}
		}
	}()
}
