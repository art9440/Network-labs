package application

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"net"
	"time"

	"google.golang.org/protobuf/proto"
)

func (c *GameClient) startAnnouncement() {
	if c.network == nil {
		return
	}

	// перезапуск: гасим старый цикл
	if c.announceStop != nil {
		close(c.announceStop)
	}
	stop := make(chan struct{})
	c.announceStop = stop

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// жёсткие условия "игра жива"
				if !c.isInGame() || c.game == nil || c.GameName == "" {
					continue
				}
				// анонсы шлёт только мастер
				if c.node.SelfRole != snakespb.NodeRole_MASTER {
					continue
				}

				msg := c.buildAnnouncementMessage()
				if msg == nil {
					continue
				}
				_ = c.network.SendToMulticast(msg)

			case <-stop:
				return
			}
		}
	}()
}

func (c *GameClient) stopAnnouncement() {
	if c.announceStop != nil {
		close(c.announceStop)
		c.announceStop = nil
	}
}

func (c *GameClient) buildAnnouncementMessage() *snakespb.GameMessage {
	ann := c.buildGameAnnouncement()
	if ann == nil {
		return nil
	}

	annMsg := &snakespb.GameMessage_AnnouncementMsg{
		Games: []*snakespb.GameAnnouncement{ann},
	}

	msg := &snakespb.GameMessage{
		MsgSeq: proto.Int64(c.nextSeq()),
		Type: &snakespb.GameMessage_Announcement{
			Announcement: annMsg,
		},
	}

	return msg
}

func (c *GameClient) buildGameAnnouncement() *snakespb.GameAnnouncement {
	if c.game == nil {
		return nil
	}

	players := &snakespb.GamePlayers{
		Players: make([]*snakespb.GamePlayer, 0, len(c.game.Players)),
	}

	for _, p := range c.game.Players {
		role := p.Role
		ptype := snakespb.PlayerType_HUMAN
		gp := &snakespb.GamePlayer{
			Name: proto.String(p.Name),
			Id:   proto.Int32(p.ID),

			Role:  &role,
			Type:  &ptype,
			Score: proto.Int32(p.Score),
		}
		players.Players = append(players.Players, gp)
	}

	cfg := c.game.Config()

	cfgPb := &snakespb.GameConfig{
		Width:        proto.Int32(int32(cfg.Width)),
		Height:       proto.Int32(int32(cfg.Height)),
		FoodStatic:   proto.Int32(int32(cfg.FoodStatic)),
		StateDelayMs: proto.Int32(int32(cfg.StateDelayMs)),
	}

	return &snakespb.GameAnnouncement{
		Players:  players,
		Config:   cfgPb,
		CanJoin:  proto.Bool(c.game.CheckCanJoin()),
		GameName: proto.String(c.GameName),
	}
}

func (c *GameClient) handleAnnouncement(ann *snakespb.GameMessage_AnnouncementMsg, addr *net.UDPAddr) {
	if ann == nil {
		return
	}

	c.gamesMu.Lock()
	defer c.gamesMu.Unlock()

	for _, g := range ann.Games {
		if g == nil {
			continue
		}

		name := g.GetGameName()
		if name == "" {
			name = "unnamed"
		}

		if c.node.SelfRole == snakespb.NodeRole_MASTER && name == c.GameName {
			continue
		}

		if c.node.MasterAddr != nil && addr.IP.Equal(c.node.MasterAddr.IP) && addr.Port == c.node.MasterAddr.Port {
			continue
		}

		key := fmt.Sprintf("%s@%s", name, addr.String())

		cfg := g.GetConfig()
		players := g.GetPlayers().GetPlayers()
		game := &DiscoveredGame{
			GameName:     name,
			Host:         addr.String(),
			Players:      len(players),
			Width:        int(cfg.GetWidth()),
			Height:       int(cfg.GetHeight()),
			CanJoin:      g.GetCanJoin(),
			FoodStatic:   int(cfg.GetFoodStatic()),
			StateDelayMs: int(cfg.GetStateDelayMs()),
			Addr:         addr,
			LastSeen:     time.Now(),
		}
		c.games[key] = game
	}
}
