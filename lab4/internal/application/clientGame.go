package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"time"
)

func (c *GameClient) startMonitorLoop() {
	if c.game == nil {
		return
	}

	// остановить старый, если был
	if c.monitorStop != nil {
		close(c.monitorStop)
	}
	stop := make(chan struct{})
	c.monitorStop = stop

	delayMs := c.game.Config().StateDelayMs
	if delayMs <= 0 {
		delayMs = 1000
	}

	interval := time.Duration(delayMs/10) * time.Millisecond
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.monitorTick(interval)
			case <-stop:
				return
			}
		}
	}()
}

func (c *GameClient) StopGame() {
	if c.tickerStop != nil {
		close(c.tickerStop)
		c.tickerStop = nil
	}
	if c.monitorStop != nil {
		close(c.monitorStop)
		c.monitorStop = nil
	}
	c.stopAnnouncement()
	c.stopSendWorkers()
	c.game = nil
}

func (c *GameClient) stopSendWorkers() {
	// закрывать sendQ не обязательно; просто гасим воркеры
	select {
	case <-c.sendStop:
		return
	default:
		close(c.sendStop)
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
				alive := c.game.OneTick()
				c.broadcastState()
				c.view.RefreshBoard()
				c.view.RefreshRating()
				if !alive {
					c.BackToStart()
					return
				}
			case <-c.tickerStop:
				return
			}
		}
	}()
}

func (c *GameClient) ChangeDirection(dir domain.Direction) {
	switch c.node.SelfRole {
	case snakespb.NodeRole_MASTER:
		// локальная игра – просто правим своё состояние
		if c.game == nil {
			return
		}
		c.game.SetDirection(dir, c.node.SelfID)

	case snakespb.NodeRole_NORMAL:
		// обычный игрок – шлём Steer мастеру
		if c.node.MasterAddr == nil {
			return // ещё не знаем, куда слать
		}

		msg := c.buildSteerMessage(dir)
		if msg == nil {
			return
		}

		retry := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
		if retry <= 0 {
			retry = 100 * time.Millisecond
		}
		c.enqueueReliable(msg, c.node.MasterAddr, c.node.MasterID, retry, 3)

	default:
		// VIEWER или что-то странное — не имеет змеи, игнорим
	}
}
