package application

import (
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type GameClient struct {
	view    View         // GUI
	game    *domain.Game // локальное состояние игры
	node    NodeState    // моя роль/ID + кто master/deputy
	network Transport    // UDP транспорт (unicast + multicast)

	//Адреса игроков и тайминги для мониторинга
	playersAddr   map[int32]*net.UDPAddr // playerID -> UDP адрес
	lastHeard     map[int32]time.Time    // когда последний раз получили сообщение от игрока (для timeout)
	lastSent      map[int32]time.Time    // когда последний раз отправили сообщение игроку (ping)
	playerAddrsMu sync.RWMutex

	//stop-каналы фоновых циклов
	tickerStop   chan struct{} // стоп игрового тика master’а (OneTick + broadcastState)
	announceStop chan struct{} // стоп анонсов (multicast announcement)
	monitorStop  chan struct{} // стоп мониторинга (ping + timeout / failover)

	//MsgSeq для протокола (уникальные номера сообщений)
	seq   int64
	seqMu sync.Mutex

	//Список обнаруженных игр
	gamesMu  sync.RWMutex
	games    map[string]*DiscoveredGame
	GameName string

	//Надёжная доставка (pending + ack)
	pendingMu sync.Mutex
	pending   map[int64]*pendingItem // msg_seq -> ожидание Ack/ретраи

	//Порядок состояний (StateOrder), чтобы клиенты не принимали старые состояния
	stateOrder     int32 // счётчик stateOrder у мастера
	stateOrderMu   sync.Mutex
	lastStateOrder int32 // последний принятый stateOrder на клиенте

	// Очередь отправки
	sendQ    chan sendJob  // очередь сообщений на отправку
	sendStop chan struct{} // стоп воркеров отправки

	//Steer: буфер поворотов на тик
	steerMu      sync.Mutex
	pendingSteer map[int32]domain.Direction // накопленные повороты на следующий тик
	lastSteerSeq map[int32]int64            // защита от старых steer

	//Фоновый цикл ретраев pending сообщений
	reliabilityStop chan struct{}

	//Флаг “мы сейчас в игре” (чтобы игнорить пакеты вне игры)
	inGameMu sync.RWMutex
	inGame   bool

	log *log.Logger
}

// NewGameClient — создаём клиент и запускаем воркеры отправки
func NewGameClient(view View, logger *log.Logger) *GameClient {
	if logger == nil {
		logger = log.Default()
	}
	c := &GameClient{
		view:    view,
		games:   make(map[string]*DiscoveredGame),
		log:     logger,
		pending: make(map[int64]*pendingItem),

		sendQ:    make(chan sendJob, 1024),
		sendStop: make(chan struct{}),
	}

	// фиксированное число воркеров, которые читают sendQ и шлют UDP
	c.startSendWorkers(4)

	return c
}

func (c *GameClient) SetView(view View) { c.view = view }

func (c *GameClient) SetTransport(t Transport) { c.network = t }

func (c *GameClient) setInGame(v bool) {
	c.inGameMu.Lock()
	c.inGame = v
	c.inGameMu.Unlock()
}

func (c *GameClient) isInGame() bool {
	c.inGameMu.RLock()
	defer c.inGameMu.RUnlock()
	return c.inGame
}

func (c *GameClient) ChangeDirection(dir domain.Direction) {
	if c.game == nil || !c.isInGame() {
		return
	}

	switch c.node.SelfRole {
	case snakespb.NodeRole_MASTER:

		c.game.SetDirection(dir, c.node.SelfID)

	case snakespb.NodeRole_NORMAL, snakespb.NodeRole_DEPUTY:

		if c.node.MasterAddr == nil || c.node.MasterID == 0 {
			return
		}

		msg := c.buildSteerMessage(dir)
		if msg == nil {
			return
		}

		msg.ReceiverId = proto.Int32(c.node.MasterID)

		// ретраи state_delay/10
		retry := time.Duration(c.game.Config().StateDelayMs/10) * time.Millisecond
		if retry <= 0 {
			retry = 100 * time.Millisecond
		}

		// отправляем с ожиданием Ack (через pending + reliability loop)
		_, _, _ = c.reliableSendAsync(msg, c.node.MasterAddr, c.node.MasterID, retry, 3)

	default:
		// viewer не управляет
	}
}
