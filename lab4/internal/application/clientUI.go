package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) Run() {
	c.view.ShowStartMenu()
}

func (c *GameClient) CreateGame() {
	c.view.ShowConfigMenu()

}

func (c *GameClient) NewGameFromInGame() {
	// если по роли мы MASTER — мы уже перестаём слать Announcement
	// (stopAnnouncements это делает), дальше по уму здесь же потом
	// отправишь RoleChangeMsg остальным.
	c.StopGame()
	c.CreateGame() // показать экран создания новой игры
}

func (c *GameClient) ShowGameList() {
	if c.network != nil {
		msg := &snakespb.GameMessage{
			MsgSeq: proto.Int64(c.nextSeq()),
			Type: &snakespb.GameMessage_Discover{
				Discover: &snakespb.GameMessage_DiscoverMsg{},
			},
		}
		_ = c.network.SendToMulticast(msg)
	}

	c.view.ShowGames()
}

func (c *GameClient) BackToStart() {
	c.StopGame()
	c.view.ShowStartMenu()
}

func (c *GameClient) CreateNewGame(playerName string, cfg domain.GameConfig) error {
	game := domain.NewGame(cfg)
	id := game.AddFirstPlayer(playerName)
	c.game = game
	c.GameName = generateGameName(playerName)
	c.node.SelfID = id
	c.node.SelfRole = snakespb.NodeRole_MASTER
	c.node.MasterID = id
	if err := c.game.SpawnSnake(id); err != nil {
		return fmt.Errorf("spawn snake: %w", err)
	}

	c.game.InitFood()
	if c.network != nil {
		c.node.MasterAddr = c.network.LocalAddr()
	}

	c.StartGame()
	c.startAnnouncement()

	c.log.Printf("CreateNewGame: name=%s gameName=%s cfg=%+v", playerName, c.GameName, cfg)
	return nil
}

func (c *GameClient) GamesSnapshot() []DiscoveredGame {
	c.gamesMu.RLock()
	defer c.gamesMu.RUnlock()

	now := time.Now()

	tmp := make([]*DiscoveredGame, 0, len(c.games))
	for _, g := range c.games {
		if now.Sub(g.LastSeen) > 5*time.Second {
			continue
		}
		tmp = append(tmp, g)
	}

	sort.Slice(tmp, func(i, j int) bool {
		if tmp[i].GameName == tmp[j].GameName {
			return tmp[i].Host < tmp[j].Host
		}
		return tmp[i].GameName < tmp[j].GameName
	})

	out := make([]DiscoveredGame, 0, len(tmp))
	for _, g := range tmp {
		out = append(out, *g)
	}

	return out
}
