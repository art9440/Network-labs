package application

import (
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) nextStateOrder() int32 {
	c.stateOrderMu.Lock()
	defer c.stateOrderMu.Unlock()
	c.stateOrder++
	return c.stateOrder
}

func (c *GameClient) buildStateMessage() *snakespb.GameMessage {
	if c.game == nil {
		return nil
	}

	gs := &snakespb.GameState{
		StateOrder: proto.Int32(c.nextStateOrder()),
		Players:    c.buildGamePlayers(),
		Snakes:     c.buildGameSnakes(),
		Foods:      c.buildGameFoods(),
	}

	stateMsg := &snakespb.GameMessage_StateMsg{
		State: gs,
	}

	return &snakespb.GameMessage{
		SenderId: proto.Int32(c.node.SelfID),
		Type: &snakespb.GameMessage_State{
			State: stateMsg,
		},
	}
}

// broadcastState — мастер рассылает State всем игрокам
func (c *GameClient) broadcastState() {
	msg := c.buildStateMessage()
	if msg == nil {
		return
	}

	retryInterval := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
	maxAttempts := 3

	c.playerAddrsMu.RLock()
	defer c.playerAddrsMu.RUnlock()

	for id, addr := range c.playersAddr {
		if addr == nil || id == c.node.SelfID {
			continue
		}

		m := proto.Clone(msg).(*snakespb.GameMessage)
		m.ReceiverId = proto.Int32(id)

		_, _, _ = c.reliableSendAsync(m, addr, id, retryInterval, maxAttempts)
	}
}

func (c *GameClient) buildGameSnakes() []*snakespb.GameState_Snake {
	res := make([]*snakespb.GameState_Snake, 0, len(c.game.Snakes))

	for _, s := range c.game.Snakes {
		if s == nil {
			continue
		}
		res = append(res, buildSnakeState(s))
	}

	return res
}

func (c *GameClient) buildGamePlayers() *snakespb.GamePlayers {
	players := &snakespb.GamePlayers{
		Players: make([]*snakespb.GamePlayer, 0, len(c.game.Players)),
	}

	for _, p := range c.game.Players {
		role := p.Role
		ptype := snakespb.PlayerType_HUMAN

		gp := &snakespb.GamePlayer{
			Name:  proto.String(p.Name),
			Id:    proto.Int32(p.ID),
			Role:  &role,
			Type:  &ptype,
			Score: proto.Int32(p.Score),
		}

		//только MASTER кладёт ip/port
		if c.node.SelfRole == snakespb.NodeRole_MASTER {
			c.playerAddrsMu.RLock()
			addr := c.playersAddr[p.ID]
			c.playerAddrsMu.RUnlock()

			if addr != nil && addr.IP != nil {
				ip := addr.IP.String()
				port := int32(addr.Port)
				gp.IpAddress = proto.String(ip)
				gp.Port = proto.Int32(port)
			}
		}

		players.Players = append(players.Players, gp)
	}
	return players
}

func (c *GameClient) buildGameFoods() []*snakespb.GameState_Coord {
	var foods []*snakespb.GameState_Coord

	board := c.game.Board
	for y := 0; y < board.Height; y++ {
		for x := 0; x < board.Width; x++ {
			if board.Cells[board.Idx(x, y)] == domain.CellFood {
				foods = append(foods, &snakespb.GameState_Coord{
					X: proto.Int32(int32(x)),
					Y: proto.Int32(int32(y)),
				})
			}
		}
	}
	return foods
}

func buildSnakeState(s *domain.Snake) *snakespb.GameState_Snake {
	points := make([]*snakespb.GameState_Coord, 0, len(s.Body))

	// голова — абсолют
	head := s.Body[0]
	points = append(points, &snakespb.GameState_Coord{
		X: proto.Int32(int32(head.X)),
		Y: proto.Int32(int32(head.Y)),
	})

	// остальные — смещения
	for i := 1; i < len(s.Body); i++ {
		prev := s.Body[i-1]
		cur := s.Body[i]
		points = append(points, &snakespb.GameState_Coord{
			X: proto.Int32(int32(cur.X - prev.X)),
			Y: proto.Int32(int32(cur.Y - prev.Y)),
		})
	}

	state := snakespb.GameState_Snake_ALIVE
	if s.State == domain.SnakeZombie {
		state = snakespb.GameState_Snake_ZOMBIE
	}

	dir := toProtoDirection(s.Direction)

	return &snakespb.GameState_Snake{
		PlayerId:      proto.Int32(s.PlayerID),
		Points:        points,
		State:         &state,
		HeadDirection: &dir,
	}
}

func toProtoDirection(d domain.Direction) snakespb.Direction {
	switch d {
	case domain.DirUp:
		return snakespb.Direction_UP
	case domain.DirDown:
		return snakespb.Direction_DOWN
	case domain.DirLeft:
		return snakespb.Direction_LEFT
	case domain.DirRight:
		return snakespb.Direction_RIGHT
	default:
		return snakespb.Direction_UP
	}
}

// ingestPlayersAddrsFromState — клиент восстанавливает адреса игроков из State
// (нужно для ping/timeout и для deputyAddr)
func (c *GameClient) ingestPlayersAddrsFromState(gs *snakespb.GameState) {
	if gs == nil || gs.GetPlayers() == nil {
		return
	}

	for _, p := range gs.GetPlayers().GetPlayers() {
		if p == nil {
			continue
		}
		id := p.GetId()
		if id == 0 || id == c.node.SelfID {
			continue
		}

		ip := p.GetIpAddress()
		port := int(p.GetPort())
		if ip == "" || port == 0 {
			continue
		}

		addr := &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
		c.storePlayerAddress(id, addr)
	}
}

// handleState — обработка входящего State у клиента:
//  1. фиксируем/обновляем мастера по senderID
//  2. фильтруем старые state по StateOrder
//  3. забираем адреса игроков и deputy из state
//  4. применяем состояние в домен и обновляем UI
func (c *GameClient) handleState(
	msg *snakespb.GameMessage,
	stateMsg *snakespb.GameMessage_StateMsg,
	from *net.UDPAddr,
) {
	// если state пришел от другого sender — считаем его текущим мастером (failover)
	senderID := msg.GetSenderId()
	if senderID != 0 && from != nil {
		if c.node.MasterID != senderID {
			old := c.node.MasterID
			c.node.MasterID = senderID
			c.node.MasterAddr = copyUDPAddr(from)

			// если master сменился — pending уже перекидываем
			if old != 0 {
				c.reroutePending(old, c.node.MasterID, c.node.MasterAddr)
			}
		}
	}

	if stateMsg == nil {
		return
	}
	gs := stateMsg.GetState()
	if gs == nil {
		return
	}

	order := gs.GetStateOrder()
	// StateOrder — защита от старых/дублирующихся state
	if order <= c.lastStateOrder {
		c.log.Printf("handleState: ignore state_order=%d, last=%d", order, c.lastStateOrder)
		return
	}
	c.lastStateOrder = order
	// адреса игроков и deputy
	c.ingestPlayersAddrsFromState(gs)
	c.updateDeputyFromState(gs)

	if c.game == nil {
		c.log.Printf("handleState: no local game, ignoring")
		return
	}

	// применяем состояние и обновляем UI
	c.applyGameState(gs)

	if c.view != nil {
		c.view.RefreshBoard()
		c.view.RefreshRating()
	}
}

// applyGameState — полностью пересобираем локальную доменную игру по State:
//   - Players, Foods, Snakes
//   - затем восстанавливаем JoinOrder и nextPlayerID
func (c *GameClient) applyGameState(gs *snakespb.GameState) {
	g := c.game
	if g == nil || gs == nil {
		return
	}

	//Players
	if players := gs.GetPlayers(); players != nil {
		g.Players = make(map[int32]*domain.Player, len(players.GetPlayers()))
		for _, p := range players.GetPlayers() {
			if p == nil {
				continue
			}
			id := p.GetId()
			g.Players[id] = &domain.Player{
				ID:    id,
				Name:  p.GetName(),
				Score: p.GetScore(),
				Role:  p.GetRole(),
			}
		}
	}

	// Board reset
	if g.Board == nil {
		return
	}
	for i := range g.Board.Cells {
		g.Board.Cells[i] = domain.CellEmpty
	}

	// Foods
	for _, f := range gs.GetFoods() {
		if f == nil {
			continue
		}
		x := int(f.GetX())
		y := int(f.GetY())
		idx := g.Board.Idx(x, y)
		g.Board.Cells[idx] = domain.CellFood
	}

	// Snakes
	g.Snakes = make(map[int32]*domain.Snake, len(gs.GetSnakes()))
	for _, s := range gs.GetSnakes() {
		if s == nil {
			continue
		}
		snake := decodePbSnake(s)
		if snake == nil {
			continue
		}
		g.Snakes[snake.PlayerID] = snake
		for _, ccoord := range snake.Body {
			idx := g.Board.Idx(ccoord.X, ccoord.Y)
			g.Board.Cells[idx] = domain.CellSnake
		}
	}

	//восстановить JoinOrder и nextPlayerID
	maxID := int32(0)
	order := make([]int32, 0, len(g.Players))
	for id := range g.Players {
		order = append(order, id)
		if id > maxID {
			maxID = id
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	g.JoinOrder = order

	//nextPlayerID должен быть > любого существующего id
	g.NextPlayerIDFix(maxID + 1)
}

func decodePbSnake(ps *snakespb.GameState_Snake) *domain.Snake {
	pts := ps.GetPoints()
	if len(pts) == 0 {
		return &domain.Snake{PlayerID: ps.GetPlayerId()}
	}

	body := make([]domain.Coord, 0, len(pts))

	// первая точка — абсолютная
	head := pts[0]
	x := int(head.GetX())
	y := int(head.GetY())
	body = append(body, domain.Coord{X: x, Y: y})

	// остальные — смещения
	for i := 1; i < len(pts); i++ {
		dx := int(pts[i].GetX())
		dy := int(pts[i].GetY())
		x += dx
		y += dy
		body = append(body, domain.Coord{X: x, Y: y})
	}

	// правильное направление
	dir, ok := fromProtoDirection(ps.GetHeadDirection())
	if !ok {
		dir = domain.DirUp
	}

	state := domain.SnakeAlive
	if ps.GetState() == snakespb.GameState_Snake_ZOMBIE {
		state = domain.SnakeZombie
	}

	return &domain.Snake{
		PlayerID:  ps.GetPlayerId(),
		Body:      body,
		Direction: dir,
		State:     state,
	}
}

// updateDeputyFromState — найти игрока с ролью DEPUTY и обновить node.DeputyID/Addr
// Адрес берём из playersAddr, а если нет — fallback из ip/port в state.
func (c *GameClient) updateDeputyFromState(gs *snakespb.GameState) {
	if gs == nil || gs.GetPlayers() == nil {
		return
	}

	var depID int32
	//из контейнера с игроками вытаскиваем массив игроков
	for _, p := range gs.GetPlayers().GetPlayers() {
		if p == nil {
			continue
		}
		if p.GetRole() == snakespb.NodeRole_DEPUTY {
			depID = p.GetId()
			break
		}
	}
	if depID == 0 {
		// deputy может отсутствовать
		c.node.DeputyID = 0
		c.node.DeputyAddr = nil
		return
	}

	// всегда обновим deputyID
	c.node.DeputyID = depID

	// адрес из playersAddr (
	c.playerAddrsMu.RLock()
	addr := c.playersAddr[depID]
	c.playerAddrsMu.RUnlock()
	if addr != nil {
		c.node.DeputyAddr = copyUDPAddr(addr)
		return
	}

	// fallback: ip/port из state (если мастер кладёт)
	for _, p := range gs.GetPlayers().GetPlayers() {
		if p == nil || p.GetId() != depID {
			continue
		}
		ip := p.GetIpAddress()
		port := int(p.GetPort())
		if ip != "" && port != 0 {
			c.node.DeputyAddr = &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
		}
		return
	}
}
