package application

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"

	"google.golang.org/protobuf/proto"
)

// startMonitorLoop — запускает общий монитор-цикл:
//   - ping игроков
//   - timeout игроков
//   - failover (master/deputy)
func (c *GameClient) startMonitorLoop() {
	if c.game == nil {
		return
	}

	if c.monitorStop != nil {
		close(c.monitorStop)
	}
	stop := make(chan struct{})
	c.monitorStop = stop

	// базовый интервал берём от state_delay
	delayMs := c.game.Config().StateDelayMs
	if delayMs <= 0 {
		delayMs = 1000
	}

	interval := time.Duration(delayMs/10) * time.Millisecond
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}

	// фоновая горутина мониторинга
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

// monitorTick — один тик мониторинга:
//   - выбираем режим: master или client
func (c *GameClient) monitorTick(pingInterval time.Duration) {
	if c.game == nil {
		return
	}

	cfg := c.game.Config()
	// timeout = 0.8 * state_delay
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
		// неизвестная роль — игнор
	}
}

// monitorAsMaster — мониторинг, когда я MASTER:
//   - пингуем игроков
//   - детектим timeout
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

// handleNodeTimeoutAsMaster — мастер обнаружил, что игрок отвалился
func (c *GameClient) handleNodeTimeoutAsMaster(id int32) {
	c.log.Printf("master: node id=%d timeout", id)

	// отменяем все pending этому peer’у
	c.dropPendingForPeer(id, fmt.Errorf("peer %d timed out", id))
	// убираем сетевые данные
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
		// мастер заметил, что отвалился DEPUTY
		c.log.Printf("master: deputy id=%d timed out, choosing new deputy", id)
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		c.chooseNewDeputyAsMaster()

	case snakespb.NodeRole_NORMAL:
		// игрок умер -> его змею делаем ZOMBIE
		c.makeSnakeZombie(id)
	case snakespb.NodeRole_MASTER:
		// старый мастер умер
		c.makeSnakeZombie(id)

	case snakespb.NodeRole_VIEWER:
		// просто забываем
		delete(c.game.Players, id)

	default:
		delete(c.game.Players, id)
	}
}

func (c *GameClient) dropPendingForPeer(peerID int32, reason error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	for seq, pi := range c.pending {
		if pi == nil {
			continue
		}
		if pi.peerID == peerID {
			delete(c.pending, seq)
			select {
			case pi.done <- reason:
			default:
			}
			close(pi.done)
		}
	}
}

// monitorAsClient — мониторинг, когда я НЕ мастер:
//   - слежу только за мастером
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

// мастера больше нет
func (c *GameClient) handleMasterTimeout() {
	c.log.Printf("master timeout detected, selfRole=%s", c.node.SelfRole.String())

	oldMasterID := c.node.MasterID

	switch c.node.SelfRole {
	case snakespb.NodeRole_NORMAL, snakespb.NodeRole_VIEWER:
		// если deputy известен — НЕ выходим, переключаемся и ждём state
		if c.node.DeputyID != 0 {
			newMasterID := c.node.DeputyID
			newAddr := c.node.DeputyAddr

			// fallback: вдруг DeputyAddr не установлен, но адрес есть в playersAddr
			if newAddr == nil {
				c.playerAddrsMu.RLock()
				newAddr = c.playersAddr[newMasterID]
				c.playerAddrsMu.RUnlock()
			}

			c.log.Printf("MASTER TIMEOUT: switch to deputy as master id=%d addr=%v", newMasterID, newAddr)

			c.node.MasterID = newMasterID
			c.node.MasterAddr = copyUDPAddr(newAddr) // может остаться nil — это ОК

			// deputy теперь становится master, чистим deputy поля
			c.node.DeputyID = 0
			c.node.DeputyAddr = nil

			// pending перекидываем только если адрес известен
			if oldMasterID != 0 && c.node.MasterAddr != nil {
				c.reroutePending(oldMasterID, c.node.MasterID, c.node.MasterAddr)
			}

			// ВАЖНО: НЕ BackToStart. Просто ждём первый State от нового мастера.
			// handleState сам обновит MasterAddr по senderID + from.
			return
		}

		// deputy реально нет — тогда да, игра потеряна
		if c.view != nil {
			c.view.ShowError("Связь с ведущим потеряна (deputy нет) — игра остановлена")
		}
		c.BackToStart()
		return

	case snakespb.NodeRole_DEPUTY:
		if oldMasterID != 0 {
			c.makeSnakeZombie(oldMasterID) // змея мастера остаётся как zombie
			c.reroutePending(oldMasterID, c.node.SelfID, c.network.LocalAddr())
		}
		c.promoteToMaster()
		return

	default:
		// MASTER сам себя не обрабатывает
		return
	}
}

// promoteToMaster — deputy становится новым master
func (c *GameClient) promoteToMaster() {
	c.log.Printf("DEPUTY: promoting to MASTER due to master timeout")

	// deputy -> master
	c.node.SelfRole = snakespb.NodeRole_MASTER
	c.node.MasterID = c.node.SelfID
	if c.network != nil {
		c.node.MasterAddr = c.network.LocalAddr()
	}

	if c.game != nil {
		if p := c.game.Players[c.node.SelfID]; p != nil {
			p.Role = snakespb.NodeRole_MASTER
		}
	}

	// сразу обновим UI
	if c.view != nil {
		c.view.RefreshRating()
		c.view.RefreshBoard()
	}

	// считаем всех живыми "сейчас", чтобы не было ложных timeout
	now := time.Now()
	c.playerAddrsMu.Lock()
	if c.lastHeard == nil {
		c.lastHeard = make(map[int32]time.Time)
	}
	for id := range c.playersAddr {
		if id == c.node.SelfID {
			continue
		}
		c.lastHeard[id] = now
	}
	c.playerAddrsMu.Unlock()

	// state_order должен продолжаться
	c.stateOrderMu.Lock()
	if c.stateOrder < c.lastStateOrder {
		c.stateOrder = c.lastStateOrder
	}
	c.stateOrderMu.Unlock()

	// мы теперь должны рассылать анонсы
	c.startAnnouncement()

	// запускаем игровой цикл мастера (тикаем и рассылаем state)
	if c.game != nil {
		c.StartGame()
	}

	// выбираем нового deputy и сообщаем ему (RoleChange только ему)
	c.chooseNewDeputyAsMaster()

	//сообщаем каждому игроку, что теперь мастер — я
	c.broadcastNewMasterRoleChange()
}

func (c *GameClient) broadcastNewMasterRoleChange() {
	if c.game == nil {
		return
	}

	senderRole := snakespb.NodeRole_MASTER

	c.playerAddrsMu.RLock()
	ids := make([]int32, 0, len(c.playersAddr))
	addrs := make(map[int32]*net.UDPAddr, len(c.playersAddr))
	for id, a := range c.playersAddr {
		if id == c.node.SelfID || a == nil {
			continue
		}
		ids = append(ids, id)
		addrs[id] = copyUDPAddr(a)
	}
	c.playerAddrsMu.RUnlock()

	retry := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
	if retry <= 0 {
		retry = 100 * time.Millisecond
	}

	for _, id := range ids {
		// receiverRole оставим “как есть” (по домену), чтобы не ломать логику
		receiverRole := snakespb.NodeRole_NORMAL
		if p := c.game.Players[id]; p != nil {
			receiverRole = p.Role
		}

		rc := &snakespb.GameMessage_RoleChangeMsg{
			SenderRole:   &senderRole,
			ReceiverRole: &receiverRole,
		}

		msg := &snakespb.GameMessage{
			SenderId:   proto.Int32(c.node.SelfID),
			ReceiverId: proto.Int32(id),
			Type: &snakespb.GameMessage_RoleChange{
				RoleChange: rc,
			},
		}

		// надежно, но не блокируемся — через async/очередь
		_, _, _ = c.reliableSendAsync(msg, addrs[id], id, retry, 3)
	}
}

// chooseNewDeputyAsMaster — мастер выбирает нового deputy из NORMAL игроков
func (c *GameClient) chooseNewDeputyAsMaster() {
	if c.game == nil {
		return
	}

	candidateID, ok := c.game.ChooseDeputy(c.node.SelfID)
	if !ok {
		c.log.Printf("chooseNewDeputyAsMaster: no NORMAL candidates")
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		return
	}

	p := c.game.Players[candidateID]
	if p == nil || p.Role != snakespb.NodeRole_NORMAL {
		c.log.Printf("chooseNewDeputyAsMaster: candidate %d invalid role", candidateID)
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		return
	}

	// назначаем deputy в домене
	p.Role = snakespb.NodeRole_DEPUTY

	// взять адрес кандидата
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

	// RoleChangeMsg кандидату
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
