package application

import "lab5/internal/domain"

const (
	MaxHandshakeIn = 8 * 1024 // максимум данных на этапе handshake
)

type SessionManager struct {
	nextID domain.SessionID // генератор id сессий

	// Основные индексы для быстрого поиска
	byID  map[domain.SessionID]*domain.Session // id -> session
	byFD  map[int]domain.SessionID             // fd (client/target) -> session id
	byDNS map[uint16]domain.SessionID          // dns query id -> session id
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		byID:  make(map[domain.SessionID]*domain.Session),
		byFD:  make(map[int]domain.SessionID),
		byDNS: make(map[uint16]domain.SessionID),
	}
}

// NewClient регистрирует нового клиента и создаёт сессию.
func (m *SessionManager) NewClient(clientFD int) *domain.Session {
	m.nextID++
	id := m.nextID

	// создаём сессию в состоянии Greeting
	s := domain.NewSession(id, clientFD, 4096)

	m.byID[id] = s
	m.byFD[clientFD] = id

	return s
}

// AttachTarget привязывает target-сокет к существующей сессии.
func (m *SessionManager) AttachTarget(id domain.SessionID, targetFD int) {
	s := m.byID[id]
	s.TargetFD = targetFD
	m.byFD[targetFD] = id
}

// BindDNS связывает DNS-запрос с сессией.
func (m *SessionManager) BindDNS(id domain.SessionID, dnsID uint16) {
	s := m.byID[id]
	s.DNSQueryID = dnsID
	m.byDNS[dnsID] = id
}

// ByFD ищет сессию по файловому дескриптору (client или target).
func (m *SessionManager) ByFD(fd int) (*domain.Session, bool) {
	id, ok := m.byFD[fd]
	if !ok {
		return nil, false
	}
	s, ok := m.byID[id]
	return s, ok
}

// ByDNS ищет сессию по ID DNS-запроса.
func (m *SessionManager) ByDNS(dnsID uint16) (*domain.Session, bool) {
	id, ok := m.byDNS[dnsID]
	if !ok {
		return nil, false
	}
	s, ok := m.byID[id]
	return s, ok
}

// Remove полностью удаляет сессию и все связанные индексы.
func (m *SessionManager) Remove(id domain.SessionID) {
	s, ok := m.byID[id]
	if !ok {
		return
	}

	delete(m.byID, id)
	delete(m.byFD, s.ClientFD)

	// если target уже был подключён — убираем и его
	if s.TargetFD >= 0 {
		delete(m.byFD, s.TargetFD)
	}

	// если был DNS-запрос — убираем привязку
	if s.DNSQueryID != 0 {
		delete(m.byDNS, s.DNSQueryID)
	}
}
