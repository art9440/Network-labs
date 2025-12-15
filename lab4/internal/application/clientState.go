package application

import (
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) buildStateMessage() *snakespb.GameMessage {
	if c.game == nil {
		return nil
	}

	// 1. Собираем GameState
	gs := &snakespb.GameState{
		StateOrder: proto.Int32(c.nextStateOrder()),
		Players:    c.buildGamePlayers(),
		Snakes:     c.buildGameSnakes(),
		Foods:      c.buildGameFoods(),
	}

	// 2. Оборачиваем в GameMessage_StateMsg
	stateMsg := &snakespb.GameMessage_StateMsg{
		State: gs,
	}

	// 3. Кладём в oneof-обёртку GameMessage_State
	return &snakespb.GameMessage{
		SenderId: proto.Int32(c.node.SelfID),
		Type: &snakespb.GameMessage_State{
			State: stateMsg,
		},
	}
}

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

		c.enqueueReliable(m, addr, id, retryInterval, maxAttempts)
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
		players.Players = append(players.Players, &snakespb.GamePlayer{
			Name:  proto.String(p.Name),
			Id:    proto.Int32(p.ID),
			Role:  &role,
			Type:  &ptype,
			Score: proto.Int32(p.Score),
		})
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
		HeadDirection: (*snakespb.Direction)(&dir),
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

func (c *GameClient) handleState(
	stateMsg *snakespb.GameMessage_StateMsg,
) {

	if stateMsg == nil {
		return
	}
	gs := stateMsg.GetState()
	if gs == nil {
		return
	}

	order := gs.GetStateOrder()

	if order <= c.lastStateOrder {
		c.log.Printf("handleState: ignore state_order=%d, last=%d", order, c.lastStateOrder)
		return
	}
	c.lastStateOrder = order

	if c.game == nil {
		c.log.Printf("handleState: no local game, ignoring")
		return
	}

	c.applyGameState(gs)

	if c.view != nil {
		c.view.RefreshBoard()
		c.view.RefreshRating()
	}
}

func (c *GameClient) applyGameState(gs *snakespb.GameState) {
	g := c.game

	if players := gs.GetPlayers(); players != nil {
		g.Players = make(map[int32]*domain.Player, len(players.GetPlayers()))
		for _, p := range players.GetPlayers() {
			id := p.GetId()
			g.Players[id] = &domain.Player{
				ID:    id,
				Name:  p.GetName(),
				Score: p.GetScore(),
				Role:  p.GetRole(),
			}
		}
	}

	for i := range g.Board.Cells {
		g.Board.Cells[i] = domain.CellEmpty
	}

	for _, f := range gs.GetFoods() {
		x := int(f.GetX())
		y := int(f.GetY())
		idx := g.Board.Idx(x, y)
		g.Board.Cells[idx] = domain.CellFood
	}

	g.Snakes = make(map[int32]*domain.Snake, len(gs.GetSnakes()))
	for _, s := range gs.GetSnakes() {
		snake := decodePbSnake(s) // отдельный helper
		g.Snakes[snake.PlayerID] = snake
		for _, ccoord := range snake.Body {
			idx := g.Board.Idx(ccoord.X, ccoord.Y)
			g.Board.Cells[idx] = domain.CellSnake
		}
	}
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

	dir := domain.Direction(ps.GetHeadDirection()) // через твой тип Direction

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
