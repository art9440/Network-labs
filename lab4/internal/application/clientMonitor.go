package application

import (
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) monitorTick(pingInterval time.Duration) {
	if c.game == nil {
		return
	}

	cfg := c.game.Config()
	timeout := time.Duration(float64(cfg.StateDelayMs)*0.8) * time.Millisecond
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}

	now := time.Now()

	switch c.node.SelfRole {
	case snakespb.NodeRole_MASTER:
		c.monitorAsMaster(now, pingInterval, timeout)
	case snakespb.NodeRole_NORMAL, snakespb.NodeRole_VIEWER, snakespb.NodeRole_DEPUTY:
		c.monitorAsClient(now, pingInterval, timeout)
	default:
		// неизвестная роль — ничего не делаем
	}
}

func (c *GameClient) monitorAsMaster(now time.Time, pingInterval, timeout time.Duration) {
	c.playerAddrsMu.RLock()

	// копия id-шников, чтобы не держать лок во время сетевых операций
	ids := make([]int32, 0, len(c.playersAddr))
	for id := range c.playersAddr {
		if id == c.node.SelfID {
			continue // себя не мониторим
		}
		ids = append(ids, id)
	}

	// делаем срезы lastSent/lastHeard
	lastSentCopy := make(map[int32]time.Time, len(ids))
	lastHeardCopy := make(map[int32]time.Time, len(ids))
	for _, id := range ids {
		if t, ok := c.lastSent[id]; ok {
			lastSentCopy[id] = t
		}
		if t, ok := c.lastHeard[id]; ok {
			lastHeardCopy[id] = t
		}
	}

	c.playerAddrsMu.RUnlock()

	for _, id := range ids {
		// --- Ping: давно ничего не слали этому игроку
		if t, ok := lastSentCopy[id]; !ok || now.Sub(t) >= pingInterval {
			c.sendPingToPlayer(id)
		}

		// --- Timeout: давно ничего не получали от него
		if t, ok := lastHeardCopy[id]; !ok || now.Sub(t) >= timeout {
			c.handleNodeTimeoutAsMaster(id)
		}
	}
}

func (c *GameClient) handleNodeTimeoutAsMaster(id int32) {
	c.log.Printf("master: node id=%d timeout", id)

	// убрать из адресов и таймингов
	c.playerAddrsMu.Lock()
	delete(c.playersAddr, id)
	if c.lastHeard != nil {
		delete(c.lastHeard, id)
	}
	if c.lastSent != nil {
		delete(c.lastSent, id)
	}
	c.playerAddrsMu.Unlock()

	if c.game == nil {
		return
	}

	p, ok := c.game.Players[id]
	if !ok {
		return
	}

	switch p.Role {
	case snakespb.NodeRole_DEPUTY:
		// (б) мастер заметил, что отвалился DEPUTY
		c.log.Printf("master: deputy id=%d timed out, choosing new deputy", id)
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		c.chooseNewDeputyAsMaster()

	case snakespb.NodeRole_NORMAL:
		// игрок умер -> его змею делаем ZOMBIE
		c.makeSnakeZombie(id)

	case snakespb.NodeRole_VIEWER:
		// просто забываем про него
		delete(c.game.Players, id)

	default:
		// на всякий случай
		delete(c.game.Players, id)
	}
}

func (c *GameClient) monitorAsClient(now time.Time, pingInterval, timeout time.Duration) {
	masterID := c.node.MasterID
	if masterID == 0 || c.node.MasterAddr == nil {
		return
	}

	c.playerAddrsMu.RLock()
	lastSentMaster := time.Time{}
	lastHeardMaster := time.Time{}

	if c.lastSent != nil {
		lastSentMaster = c.lastSent[masterID]
	}
	if c.lastHeard != nil {
		lastHeardMaster = c.lastHeard[masterID]
	}
	c.playerAddrsMu.RUnlock()

	// Ping мастеру, если давно ничего не слали
	if lastSentMaster.IsZero() || now.Sub(lastSentMaster) >= pingInterval {
		c.sendPingToMaster()
	}

	// Если давно ничего не получали от мастера — считаем, что он умер
	if lastHeardMaster.IsZero() || now.Sub(lastHeardMaster) >= timeout {
		c.handleMasterTimeout()
	}
}

func (c *GameClient) sendPingToMaster() {
	if c.node.MasterAddr == nil || c.node.MasterID == 0 {
		return
	}

	msg := &snakespb.GameMessage{
		SenderId:   proto.Int32(c.node.SelfID),
		ReceiverId: proto.Int32(c.node.MasterID),
		Type: &snakespb.GameMessage_Ping{
			Ping: &snakespb.GameMessage_PingMsg{},
		},
	}

	go func() {
		retry := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
		if retry <= 0 {
			retry = 100 * time.Millisecond
		}
		_ = c.sendReliable(msg, c.node.MasterAddr, c.node.MasterID, retry, 1)
	}()
}

func (c *GameClient) handleMasterTimeout() {
	c.log.Printf("master timeout detected, selfRole=%s", c.node.SelfRole.String())

	switch c.node.SelfRole {
	case snakespb.NodeRole_NORMAL, snakespb.NodeRole_VIEWER:
		// (а) NORMAL заметил, что отвалился MASTER -> шлём всё на DEPUTY
		if c.node.DeputyID != 0 && c.node.DeputyAddr != nil {
			c.log.Printf("switching master -> deputy id=%d", c.node.DeputyID)
			c.node.MasterID = c.node.DeputyID
			c.node.MasterAddr = c.node.DeputyAddr
			// дальше всё пойдёт через DEPUTY
		} else {
			c.log.Printf("no deputy, game is considered lost")
			// можно сделать BackToStart() или показать диалог
			if c.view != nil {
				c.view.ShowError("Связь с ведущим потеряна, игра остановлена")
			}
			c.BackToStart()
		}

	case snakespb.NodeRole_DEPUTY:
		// (в) DEPUTY заметил, что отвалился MASTER -> он становится MASTER
		c.promoteToMaster()

	default:
		// MASTER сам себе master timeout не обрабатывает
	}
}

func (c *GameClient) promoteToMaster() {
	c.log.Printf("DEPUTY: promoting to MASTER")

	c.node.SelfRole = snakespb.NodeRole_MASTER
	c.node.MasterID = c.node.SelfID
	c.node.MasterAddr = nil // мы сами центр

	// тут по-хорошему ты уже имеешь локальное состояние игры (через handleState),
	// так что можно запустить игровой цикл:
	if c.game != nil {
		c.StartGame()
	}

	c.chooseNewDeputyAsMaster()
}

func (c *GameClient) makeSnakeZombie(id int32) {
	if c.game == nil {
		return
	}

	// 1. Змея становится зомби, если есть
	if s, ok := c.game.Snakes[id]; ok && s != nil {
		s.State = domain.SnakeZombie
	}

	// 2. Игрок перестаёт быть NORMAL, становится VIEWER
	if p, ok := c.game.Players[id]; ok && p != nil {
		p.Role = snakespb.NodeRole_VIEWER
	}
}

func (c *GameClient) chooseNewDeputyAsMaster() {
	if c.game == nil {
		return
	}

	// 1) выбрать кандидата (следующий NORMAL после мастера)
	candidateID, ok := c.game.ChooseDeputy(c.node.SelfID)
	if !ok {
		c.log.Printf("chooseNewDeputyAsMaster: no NORMAL candidates")
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		return
	}

	// 2) обновить роль в домене
	if p := c.game.Players[candidateID]; p != nil {
		p.Role = snakespb.NodeRole_DEPUTY
	}

	// 3) взять адрес кандидата
	c.playerAddrsMu.RLock()
	addr := c.playersAddr[candidateID]
	c.playerAddrsMu.RUnlock()

	if addr == nil {
		c.log.Printf("chooseNewDeputyAsMaster: no address for player %d", candidateID)
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		return
	}

	c.node.DeputyID = candidateID
	c.node.DeputyAddr = addr

	// 4) RoleChangeMsg кандидату
	senderRole := snakespb.NodeRole_MASTER
	receiverRole := snakespb.NodeRole_DEPUTY

	rc := &snakespb.GameMessage_RoleChangeMsg{
		SenderRole:   &senderRole,
		ReceiverRole: &receiverRole,
	}

	msg := &snakespb.GameMessage{
		SenderId:   proto.Int32(c.node.SelfID),
		ReceiverId: proto.Int32(candidateID),
		Type: &snakespb.GameMessage_RoleChange{
			RoleChange: rc,
		},
	}

	interval := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	maxAttempts := 3

	c.enqueueReliable(msg, addr, candidateID, interval, maxAttempts)
}
