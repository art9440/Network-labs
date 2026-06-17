package domain

type State int

const (
	StateGreeting State = iota
	StateRequest
	StateResolvingDNS
	StateConnectingTarget
	StateRelaying
)

type SessionID uint64

type Session struct {
	ID SessionID

	ClientFD int
	TargetFD int // -1 пока нет

	State State

	// Буферы данных (bounded ring buffers)
	// client -> target
	ToTarget *RingBuffer
	// target -> client
	ToClient *RingBuffer

	ClientReadClosed  bool
	ClientWriteClosed bool
	TargetReadClosed  bool
	TargetWriteClosed bool

	// Для DNS/CONNECT (по необходимости)
	PendingDomain string
	PendingPort   uint16
	DNSQueryID    uint16
	PendingIP4    [4]byte

	// Можно хранить текущие интересы epoll, чтобы не делать MOD лишний раз
	ClientMask uint32
	TargetMask uint32
}

func NewSession(id SessionID, clientFD int, bufCap int) *Session {
	return &Session{
		ID:       id,
		ClientFD: clientFD,
		TargetFD: -1,
		State:    StateGreeting,
		ToTarget: NewRingBuffer(bufCap),
		ToClient: NewRingBuffer(bufCap),
	}
}
