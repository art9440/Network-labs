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

// запрет подключаться в игру, в которой ты уже есть
func sameAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.IP.Equal(b.IP) && a.Port == b.Port
}

func (c *GameClient) removeDiscoveredGame(g DiscoveredGame) {
	c.gamesMu.Lock()
	defer c.gamesMu.Unlock()

	for k, dg := range c.games {
		if dg == nil {
			continue
		}
		if dg.GameName == g.GameName && dg.Host == g.Host {
			delete(c.games, k)
		}
	}
}

// Клиент отправляет Join мастеру с надёжной доставкой (ожидает Ack).
func (c *GameClient) JoinGame(playerName string, g DiscoveredGame, mode string) error {
	//нельзя подключиться к игре, в которой уже находишься
	if c.isInGame() && g.GameName == c.GameName {
		return fmt.Errorf("нельзя подключиться к игре, в которой уже находишься")
	}
	//гасим все предыдущие тикеры
	if c.isInGame() {
		c.fullShutdown()
	}
	// уже в этой игре — не даём повторно джойниться
	if c.isInGame() && c.GameName == g.GameName && sameAddr(c.node.MasterAddr, g.Addr) {
		return fmt.Errorf("уже подключен к этой игре")
	}

	c.node.SelfID = 0
	c.node.SelfRole = snakespb.NodeRole_NORMAL
	c.node.MasterID = 0
	c.node.MasterAddr = g.Addr
	c.node.DeputyID = 0
	c.node.DeputyAddr = nil

	c.lastStateOrder = 0

	c.stateOrderMu.Lock()
	c.stateOrder = 0
	c.stateOrderMu.Unlock()

	c.clearAllPending(fmt.Errorf("rejoin"))

	if c.network == nil {
		return fmt.Errorf("no network transport")
	}

	// ВАЖНО: воркеры должны работать ДО надежной отправки join
	c.ensureSendWorkers()

	c.setInGame(true)

	c.startReliabilityLoop(c.reliabilityInterval())

	c.node.MasterAddr = g.Addr
	c.node.MasterID = 0
	c.node.SelfRole = snakespb.NodeRole_NORMAL
	c.GameName = g.GameName

	msg := c.buildJoinMessage(playerName, g, mode)
	if msg == nil {
		c.setInGame(false)
		return fmt.Errorf("cannot build join message")
	}

	retryInterval := 100 * time.Millisecond
	maxAttempts := 2
	wait := 2 * time.Second

	if err := c.reliableSendWait(msg, g.Addr, 0, retryInterval, maxAttempts, wait); err != nil {
		c.removeDiscoveredGame(g)
		if c.view != nil { // чтобы UI сразу обновился
			c.view.RefreshGamesList()
			c.view.RefreshAvailableGames()
		}
		c.fullShutdown()
		return fmt.Errorf("join: %w", err)
	}

	cfg := domain.GameConfig{
		Width:        g.Width,
		Height:       g.Height,
		FoodStatic:   g.FoodStatic,
		StateDelayMs: g.StateDelayMs,
	}
	c.game = domain.NewGame(cfg)
	c.ensureSendWorkers()
	c.setInGame(true)
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
