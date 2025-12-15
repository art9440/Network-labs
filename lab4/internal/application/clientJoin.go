package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"

	"google.golang.org/protobuf/proto"
)

// Строим только payload JoinMsg, без msg_seq и без обёртки GameMessage.
func (c *GameClient) buildGameJoin(playerName string, gameName string, mode string) *snakespb.GameMessage_JoinMsg {
	ptype := snakespb.PlayerType_HUMAN

	var role snakespb.NodeRole
	switch mode {
	case "viewer":
		role = snakespb.NodeRole_VIEWER
	default:
		role = snakespb.NodeRole_NORMAL
	}

	return &snakespb.GameMessage_JoinMsg{
		PlayerType:    &ptype,
		PlayerName:    proto.String(playerName),
		GameName:      proto.String(gameName),
		RequestedRole: &role,
	}
}

// Оборачиваем JoinMsg в GameMessage, НО НЕ ставим MsgSeq – этим займётся sendReliable.
func (c *GameClient) buildJoinMessage(playerName string, g DiscoveredGame, mode string) *snakespb.GameMessage {
	join := c.buildGameJoin(playerName, g.GameName, mode)
	if join == nil {
		return nil
	}

	return &snakespb.GameMessage{
		Type: &snakespb.GameMessage_Join{
			Join: join,
		},
	}
}

// Клиент отправляет Join мастеру с надёжной доставкой (ожидает Ack).
func (c *GameClient) JoinGame(playerName string, g DiscoveredGame, mode string) error {
	if c.network == nil {
		return fmt.Errorf("no network transport")
	}

	c.node.MasterAddr = g.Addr
	c.node.MasterID = 0
	c.node.SelfRole = snakespb.NodeRole_NORMAL

	msg := c.buildJoinMessage(playerName, g, mode)
	if msg == nil {
		return fmt.Errorf("cannot build join message")
	}

	c.log.Printf("JOIN sending: name=%s game=%s addr=%s",
		playerName, g.GameName, g.Addr)

	// По протоколу: retry interval ~= state_delay_ms / 10.
	// У тебя тут нет delay в DiscoveredGame, поэтому пока жёстко:
	retryInterval := 100 * time.Millisecond
	maxAttempts := 2

	if err := c.sendReliable(msg, g.Addr, 0, retryInterval, maxAttempts); err != nil {
		return fmt.Errorf("join: no ack from master: %w", err)
	}

	cfg := domain.GameConfig{
		Width:        g.Width,
		Height:       g.Height,
		FoodStatic:   g.FoodStatic,
		StateDelayMs: g.StateDelayMs,
	}
	c.game = domain.NewGame(cfg)

	if c.view != nil {
		c.view.ShowGameScreen(c.game)
	}
	//запускаем пингование
	c.startMonitorLoop()

	return nil
}

// Мастер обрабатывает Join, добавляет игрока, создаёт змею (если не viewer) и запоминает адрес.
func (c *GameClient) TryToJoin(
	join *snakespb.GameMessage_JoinMsg,
	addr *net.UDPAddr,
) (int32, error) {

	if c.game == nil {
		return 0, fmt.Errorf("game not started")
	}
	if !c.game.CheckCanJoin() {
		return 0, fmt.Errorf("cannot join now")
	}

	name := join.GetPlayerName()
	role := join.GetRequestedRole()

	id := c.game.AddPlayerWithRole(name, role)

	if role != snakespb.NodeRole_VIEWER {
		if err := c.game.SpawnSnake(id); err != nil {
			return 0, fmt.Errorf("cannot spawn snake: %w", err)
		}
	}

	c.storePlayerAddress(id, addr)

	if c.node.SelfRole == snakespb.NodeRole_MASTER &&
		c.node.DeputyID == 0 &&
		role == snakespb.NodeRole_NORMAL {
		c.chooseNewDeputyAsMaster()
	}

	return id, nil
}

// Мастер хранит соответствие player_id -> UDP-адрес.
func (c *GameClient) storePlayerAddress(id int32, addr *net.UDPAddr) {
	if addr == nil {
		return
	}

	c.playerAddrsMu.Lock()
	defer c.playerAddrsMu.Unlock()

	if c.playersAddr == nil {
		c.playersAddr = make(map[int32]*net.UDPAddr)
	}
	if c.lastHeard == nil {
		c.lastHeard = make(map[int32]time.Time)
	}

	copyAddr := *addr

	c.playersAddr[id] = &copyAddr
	c.lastHeard[id] = time.Now()

	if c.log != nil {
		c.log.Printf("storePlayerAddress: player=%d addr=%s", id, addr.String())
	}
}
