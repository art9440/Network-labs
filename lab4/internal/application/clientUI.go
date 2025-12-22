package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
)

// Run — старт клиента: показываем главное меню
func (c *GameClient) Run() {
	c.view.ShowStartMenu()
}

// CreateGame — переход к экрану создания новой игры
func (c *GameClient) CreateGame() {
	c.view.ShowConfigMenu()
}

// NewGameFromInGame — начать новую игру, находясь уже в игре
func (c *GameClient) NewGameFromInGame() {
	c.StopGame()
	c.CreateGame()
}

// ShowGameList — запросить список игр по multicast и показать их
func (c *GameClient) ShowGameList() {
	if c.network != nil {
		// Discover-сообщение для поиска активных игр
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

// BackToStart — полный выход в стартовое меню
func (c *GameClient) BackToStart() {
	c.fullShutdown()
	c.view.ShowStartMenu()
}

// LeaveGame — выход из текущей игры (для кнопки "Выход")
func (c *GameClient) LeaveGame() {
	c.fullShutdown()
	if c.view != nil {
		c.view.ShowStartMenu()
	}
}

// fullShutdown — полный сброс состояния клиента и сети
func (c *GameClient) fullShutdown() {
	c.StopGame()

	// перестаём быть игровым узлом
	c.node = NodeState{}

	// очищаем данные о других игроках
	c.playerAddrsMu.Lock()
	c.playersAddr = nil
	c.lastHeard = nil
	c.lastSent = nil
	c.playerAddrsMu.Unlock()
}

// generateGameName — уникальное имя игры
func generateGameName(playerName string) string {
	return fmt.Sprintf("%s-%d", playerName, time.Now().UnixNano())
}

// CreateNewGame — создание новой игры и становление MASTER
func (c *GameClient) CreateNewGame(playerName string, cfg domain.GameConfig) error {
	// создаём доменную игру
	game := domain.NewGame(cfg)

	// добавляем первого игрока (master)
	id := game.AddFirstPlayer(playerName)

	c.game = game
	c.ensureSendWorkers()
	c.setInGame(true)

	// инициализация состояния узла
	c.GameName = generateGameName(playerName)
	c.node.SelfID = id
	c.node.SelfRole = snakespb.NodeRole_MASTER
	c.node.MasterID = id

	// создаём змейку мастера
	if err := c.game.SpawnSnake(id); err != nil {
		return fmt.Errorf("spawn snake: %w", err)
	}

	// начальная еда
	c.game.InitFood()

	// мастер знает свой сетевой адрес
	if c.network != nil {
		c.node.MasterAddr = c.network.LocalAddr()
	}

	// запуск игры и сервисных циклов
	c.StartGame()
	c.startAnnouncement()
	c.startReliabilityLoop(c.reliabilityInterval())

	c.log.Printf("CreateNewGame: name=%s gameName=%s cfg=%+v",
		playerName, c.GameName, cfg)
	return nil
}

func (c *GameClient) gamesTTL() time.Duration {
	if c.isInGame() {
		return 2 * time.Second // внутри игры — только реально живые анонсы
	}
	return 5 * time.Second // в меню можно дольше
}

func (c *GameClient) pruneGamesLocked(now time.Time) {
	ttl := c.gamesTTL()
	for k, g := range c.games {
		if g == nil || now.Sub(g.LastSeen) > ttl {
			delete(c.games, k)
		}
	}
}

// GamesSnapshot — актуальный список доступных игр (для UI)
func (c *GameClient) GamesSnapshot() []DiscoveredGame {
	c.gamesMu.RLock()
	defer c.gamesMu.RUnlock()

	now := time.Now()
	c.pruneGamesLocked(now)
	out := make([]DiscoveredGame, 0, len(c.games))

	for _, g := range c.games {
		if g == nil {
			continue
		}

		ttl := c.gamesTTL()

		if now.Sub(g.LastSeen) > ttl {
			continue
		}

		// 1) не показываем игру, в которой мы сейчас находимся
		if c.isInGame() && g.GameName == c.GameName {
			continue
		}

		// 2) показываем только то, куда можно подключиться (опционально)
		if c.isInGame() && !g.CanJoin {
			continue
		}

		out = append(out, *g)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].GameName == out[j].GameName {
			return out[i].Host < out[j].Host
		}
		return out[i].GameName < out[j].GameName
	})

	return out
}
