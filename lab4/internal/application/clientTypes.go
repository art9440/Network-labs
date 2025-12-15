package application

import (
	"fmt"
	"lab4/internal/domain"
	snakespb "lab4/internal/infrastructure/proto"
	"log"
	"net"
	"sync"
	"time"
)

type NodeState struct {
	SelfID   int32
	SelfRole snakespb.NodeRole

	MasterID   int32
	MasterAddr *net.UDPAddr

	DeputyID   int32
	DeputyAddr *net.UDPAddr
}

type DiscoveredGame struct {
	GameName string
	Host     string // ip:port
	Players  int
	Width    int
	Height   int
	CanJoin  bool

	FoodStatic   int
	StateDelayMs int

	Addr     *net.UDPAddr // куда слать JoinMsg
	LastSeen time.Time
}

type sendJob struct {
	msg           *snakespb.GameMessage
	addr          *net.UDPAddr
	peerID        int32
	retryInterval time.Duration
	maxAttempts   int
	reliable      bool
}

type pendingItem struct {
	seq    int64
	msg    *snakespb.GameMessage
	addr   *net.UDPAddr
	peerID int32

	retryEvery time.Duration
	nextSend   time.Time

	// attempts: -1 = бесконечно (пока peer не выкинем по timeout)
	attemptsLeft int

	done chan error // закрываем ошибкой/успехом
}

type GameClient struct {
	view    View
	game    *domain.Game
	node    NodeState
	network Transport

	playersAddr   map[int32]*net.UDPAddr
	lastHeard     map[int32]time.Time
	lastSent      map[int32]time.Time
	playerAddrsMu sync.RWMutex

	tickerStop   chan struct{}
	announceStop chan struct{}
	monitorStop  chan struct{} // общий монитор-цикл (ping + timeout)

	seq   int64
	seqMu sync.Mutex

	gamesMu  sync.RWMutex
	games    map[string]*DiscoveredGame
	GameName string

	pendingMu sync.Mutex
	pending   map[int64]*pendingItem

	stateOrder   int32
	stateOrderMu sync.Mutex

	lastStateOrder int32

	sendQ    chan sendJob
	sendStop chan struct{}

	log *log.Logger
}

func NewGameClient(view View, logger *log.Logger) *GameClient {
	if logger == nil {
		logger = log.Default()
	}
	c := &GameClient{
		view:    view,
		games:   make(map[string]*DiscoveredGame),
		log:     logger,
		pending: make(map[int64]*pendingItem),

		sendQ:    make(chan sendJob, 1024), // буфер: можно подобрать
		sendStop: make(chan struct{}),
	}

	c.startSendWorkers(4) // <- фиксированное число воркеров
	return c
}

func (c *GameClient) SetView(view View)        { c.view = view }
func (c *GameClient) SetTransport(t Transport) { c.network = t }

func (c *GameClient) nextSeq() int64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	c.seq++
	return c.seq
}

func generateGameName(playerName string) string {
	return fmt.Sprintf("%s-%d", playerName, time.Now().UnixNano())
}

func (c *GameClient) nextStateOrder() int32 {
	c.stateOrderMu.Lock()
	defer c.stateOrderMu.Unlock()
	c.stateOrder++
	return c.stateOrder
}

func (c *GameClient) completePending(seq int64, err error) {
	c.pendingMu.Lock()
	pi, ok := c.pending[seq]
	if ok {
		delete(c.pending, seq)
	}
	c.pendingMu.Unlock()

	if !ok || pi == nil {
		return
	}

	// не close(ch) молча, а сигналим результат
	select {
	case pi.done <- err:
	default:
	}
	close(pi.done)
}
