package application

import (
	"encoding/binary"
	"errors"
	"fmt"
	"lab5/internal/domain"
	"lab5/internal/infrastructure/net"
	"lab5/internal/logging"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// SOCKS5 version (RFC 1928)
	socksVer5 = 0x05

	// Методы аутентификации из greeting:
	// 0x00 — "No Authentication Required"
	// 0xFF — "No acceptable methods"
	socksNoAuth   = 0x00
	socksNoAccept = 0xFF

	// Команда SOCKS5: 0x01 — CONNECT (устанавливаем TCP соединение до target)
	cmdConnect = 0x01

	// Типы адреса (ATYP) в запросе CONNECT:
	// 0x01 — IPv4 (4 байта)
	// 0x03 — DOMAINNAME (1 байт длины + bytes домена)
	atypIPv4   = 0x01
	atypDomain = 0x03

	// Коды ответа (REP) в reply на CONNECT (RFC 1928)
	repSucceeded          = 0x00
	repGeneralFailure     = 0x01
	repHostUnreachable    = 0x04
	repCommandNotSupport  = 0x07
	repAddrTypeNotSupport = 0x08
)

// означает, что сессию уже закрыли внутри обработчика
var errSessionClosed = errors.New("session closed")

func stateName(st domain.State) string {
	switch st {
	case domain.StateGreeting:
		return "Greeting"
	case domain.StateRequest:
		return "Request"
	case domain.StateResolvingDNS:
		return "ResolvingDNS"
	case domain.StateConnectingTarget:
		return "ConnectingTarget"
	case domain.StateRelaying:
		return "Relaying"
	default:
		return fmt.Sprintf("State(%d)", int(st))
	}
}

type SocksServer struct {
	ep         *net.Epoller // epoll instance
	serverPort int

	listenFD int          // TCP listen socket (accept)
	dns      *DNSResolver // UDP DNS клиент + таблица ожиданий
	sm       *SessionManager
}

func NewSocksServer(port int) (*SocksServer, error) {
	// Создаём epoll
	epoller, err := net.NewEppoller()
	if err != nil {
		return nil, err
	}

	// Создаём неблокирующий TCP listener.
	listenFD, err := net.CreateTCPListener(port)
	if err != nil {
		_ = epoller.Close()
		return nil, err
	}

	// Создаём неблокирующий UDP socket для DNS.
	dnsFD, err := net.CreateUDPSocket()
	if err != nil {
		_ = net.Close(listenFD)
		_ = epoller.Close()
		return nil, err
	}

	// Регистрируем listenFD на чтение:
	// EPOLLIN на listen socket для accept()
	if err := epoller.Add(listenFD, net.EventRead); err != nil {
		_ = epoller.Close()
		_ = net.Close(listenFD)
		_ = net.Close(dnsFD)
		return nil, err
	}

	// Регистрируем dnsFD на чтение:
	// EPOLLIN на UDP означает, что можно читать ответы DNS.
	if err := epoller.Add(dnsFD, net.EventRead); err != nil {
		_ = epoller.Close()
		_ = net.Close(listenFD)
		_ = net.Close(dnsFD)
		return nil, err
	}

	dnsResolver := NewDNSResolver(
		dnsFD,
		unix.SockaddrInet4{
			Port: 53,
			Addr: [4]byte{8, 8, 8, 8},
		},
	)

	return &SocksServer{
		ep:         epoller,
		serverPort: port,
		listenFD:   listenFD,
		dns:        dnsResolver,
		sm:         NewSessionManager(),
	}, nil
}

func (s *SocksServer) Run() error {
	logging.Info("SOCKS5 proxy listening", "port", s.serverPort)

	events := make([]unix.EpollEvent, 128)

	for {
		// Ждём событий бесконечно (timeout = -1).
		// Это гарантирует отсутствие холостых циклов.
		n, err := s.ep.Wait(events, -1)
		if err != nil {
			return err
		}

		// Обрабатываем ровно n событий, которые вернул epoll.
		for i := 0; i < n; i++ {
			ev := events[i]
			fd := int(ev.Fd)

			mask := net.EpollToMask(ev.Events)

			switch {
			// Новые входящие TCP подключения к прокси (listen socket).
			case fd == s.listenFD && mask&net.EventRead != 0:
				s.acceptLoop()

			// Пришли ответы DNS по UDP.
			case fd == s.dns.GetFd() && mask&net.EventRead != 0:
				results := s.dns.OnReadable()

				// DNSResolver возвращает пачку результатов (0..N), пока UDP сокет не опустеет.
				for _, r := range results {
					// Находим SOCKS сессию, которая ждала dnsID=r.ID.
					sess, ok := s.sm.ByDNS(r.ID)
					if !ok {
						continue
					}

					if !r.OK {
						// DNS не дал A-запись -> отвечаем клиенту ошибкой и закрываем.
						logging.Warn("dns resolve failed",
							"sid", sess.ID,
							"domain", sess.PendingDomain,
							"port", sess.PendingPort,
						)
						s.replyAndClose(sess, repHostUnreachable)
						continue
					}

					// Есть IP -> продолжаем CONNECT как для IPv4.
					logging.Info("dns resolved",
						"sid", sess.ID,
						"domain", sess.PendingDomain,
						"ip", r.IP,
						"port", sess.PendingPort,
					)

					sess.PendingIP4 = r.IP
					s.startConnectIPv4(sess, r.IP, sess.PendingPort)

					// startConnectIPv4 мог закрыть сессию —
					// поэтому проверяем "жива ли" сессия перед Mod().
					if _, alive := s.sm.ByFD(sess.ClientFD); alive {
						s.updateInterests(sess)
					}
				}

			// это либо clientFD, либо targetFD.
			default:
				s.handleSessionEvent(fd, ev.Events)
			}
		}
	}
}

// acceptLoop принимает всех клиентов, которые уже готовы в очереди accept().
// В неблокирующем режиме accept возвращает EAGAIN, когда очередь пуста.
func (s *SocksServer) acceptLoop() {
	for {
		clientFd, addr, err := net.Accept(s.listenFD)
		if err != nil {
			//больше клиентов нет.
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				return
			}

			logging.Error("accept error", "error", err)
			return
		}

		// Добавляем clientFd в epoll.
		// Read — чтобы получать greeting/request/relay данные.
		// Error/Hup/RDHup — чтобы корректно отлавливать разрывы и half-close.
		if err := s.ep.Add(clientFd, net.EventRead|net.EventError|net.EventHup|net.EventRDHup); err != nil {
			logging.Error("epoll add client error", "fd", clientFd, "error", err)
			_ = net.Close(clientFd)
			return
		}

		// Создаём сессию для клиента: state = Greeting
		session := s.sm.NewClient(clientFd)

		logging.Info("client accepted",
			"sid", session.ID,
			"fd", clientFd,
			"addr", addr,
		)
	}
}

// единый вход для событий на clientFD/targetFD.
// fd нам даёт epoll; по fd мы находим Session через SessionManager.
func (s *SocksServer) handleSessionEvent(fd int, epollEvents uint32) {

	sess, ok := s.sm.ByFD(fd)
	if !ok {
		_ = s.ep.Del(fd)
		_ = net.Close(fd)
		return
	}

	mask := net.EpollToMask(epollEvents)

	// EPOLLERR: ошибка на сокете.
	if mask&net.EventError != 0 {
		if fd == sess.ClientFD {
			s.handleClientError(sess)
		} else if fd == sess.TargetFD {
			s.handleTargetError(sess)
		} else {
			//закрываем на всякий случай.
			s.failAndCloseNow(sess)
		}
		return
	}

	// HUP / RDHUP: соединение или направление записи закрыто peer'ом.
	// RDHUP — peer закрыл свою запись (half-close), это важно для корректного shutdown().
	if mask&(net.EventHup|net.EventRDHup) != 0 {
		if fd == sess.ClientFD {
			s.onClientEOF(sess)
		} else if fd == sess.TargetFD {
			s.onTargetEOF(sess)
		}
	}

	// обработка read/write по конкретной стороне.
	if fd == sess.ClientFD {
		if s.handleClientEvent(sess, mask) {
			return
		}
	} else if fd == sess.TargetFD {
		if s.handleTargetEvent(sess, mask) {
			return
		}
	}

	// Внутри обработчиков сессия могла быть закрыта
	if _, alive := s.sm.ByFD(sess.ClientFD); !alive {
		return
	}

	// обновляем интересы epoll,
	// чтобы не ловить лишние события и не крутить холостой цикл.
	s.updateInterests(sess)

	// Проверяем, можно ли закрыть сессию полностью (оба EOF + буферы пусты).
	s.tryCloseIfDone(sess)
}

// обработка EPOLLERR на клиентском сокете.
func (s *SocksServer) handleClientError(sess *domain.Session) {
	soErr, _ := unix.GetsockoptInt(sess.ClientFD, unix.SOL_SOCKET, unix.SO_ERROR)

	logging.Warn("client socket error",
		"sid", sess.ID,
		"state", stateName(sess.State),
		"soErr", soErr,
	)

	if soErr != 0 {
		s.failSession(sess, repGeneralFailure, syscall.Errno(soErr))
	} else {
		//на всякий случай, вдруг SO_ERROR=0
		s.failSession(sess, repGeneralFailure, errors.New("EPOLLERR but SO_ERROR=0"))
	}
}

// обработка EPOLLERR на target сокете.
func (s *SocksServer) handleTargetError(sess *domain.Session) {
	if sess.TargetFD < 0 {
		s.failAndCloseNow(sess)
		return
	}

	soErr, _ := unix.GetsockoptInt(sess.TargetFD, unix.SOL_SOCKET, unix.SO_ERROR)

	logging.Warn("target socket error",
		"sid", sess.ID,
		"state", stateName(sess.State),
		"soErr", soErr,
	)

	if soErr != 0 {
		s.failSession(sess, repGeneralFailure, syscall.Errno(soErr))
	} else {
		s.failSession(sess, repGeneralFailure, errors.New("EPOLLERR but SO_ERROR=0"))
	}
}

// обрабатывает read/write события на clientFD.
// return true, если сессию уже закрыли и дальше её трогать нельзя.
func (s *SocksServer) handleClientEvent(sess *domain.Session, mask net.EventMask) bool {
	// читаем входящие байты в буфер ToTarget, а затем двигаем FSM (Greeting/Request/Relay).
	if mask&net.EventRead != 0 {
		if err := s.handleClientReadable(sess); err != nil {
			if errors.Is(err, errSessionClosed) {
				return true
			}
			s.failSession(sess, repGeneralFailure, err)
			return true
		}
	}

	// если в ToClient лежат данные, пытаемся их выписать.
	if mask&net.EventWrite != 0 {
		if err := s.flushToClient(sess); err != nil {
			s.failSession(sess, repGeneralFailure, err)
			return true
		}
	}

	//если закрыли сессию выше
	if _, ok := s.sm.ByFD(sess.ClientFD); !ok {
		return true
	}
	return false
}

// обрабатывает read/write события на targetFD.
// return true, если сессию закрыли.
func (s *SocksServer) handleTargetEvent(sess *domain.Session, mask net.EventMask) bool {
	// В состоянии ConnectingTarget EPOLLOUT означает "connect завершился".
	if sess.State == domain.StateConnectingTarget && mask&net.EventWrite != 0 {
		if err := s.finishTargetConnect(sess); err != nil {
			s.failSession(sess, repHostUnreachable, err)
			return true
		}
	}

	// В состоянии Relaying мы перекачиваем данные
	if sess.State == domain.StateRelaying {
		if mask&net.EventRead != 0 {
			if err := s.handleTargetReadable(sess); err != nil {
				s.failSession(sess, repGeneralFailure, err)
				return true
			}
		}
		if mask&net.EventWrite != 0 {
			if err := s.flushToTarget(sess); err != nil {
				s.failSession(sess, repGeneralFailure, err)
				return true
			}
		}
	}

	if _, ok := s.sm.ByFD(sess.ClientFD); !ok {
		return true
	}
	return false
}

// ---------- EOF handling ----------

// вызывается, когда клиент закрыл запись или epoll дал HUP/RDHUP.
// перестаём читать от клиента и (после слива буфера) закрываем запись в target.
func (s *SocksServer) onClientEOF(sess *domain.Session) {
	if sess.ClientReadClosed {
		return
	}

	//больше не читаем из clientFD.
	sess.ClientReadClosed = true

	// Закрываем read-направление на clientFD
	_ = unix.Shutdown(sess.ClientFD, unix.SHUT_RD)

	logging.Info("client EOF", "sid", sess.ID, "state", stateName(sess.State))

	// Если target есть — закрываем его write-направление:
	//сообщаем target что данных от клиента больше не будет (half-close).
	if sess.TargetFD >= 0 && !sess.TargetWriteClosed {
		_ = unix.Shutdown(sess.TargetFD, unix.SHUT_WR)
		sess.TargetWriteClosed = true
	}
}

// вызывается, когда target закрыл запись.
// перестаём читать от target и закрываем запись клиенту.
func (s *SocksServer) onTargetEOF(sess *domain.Session) {
	if sess.TargetReadClosed || sess.TargetFD < 0 {
		return
	}

	sess.TargetReadClosed = true
	_ = unix.Shutdown(sess.TargetFD, unix.SHUT_RD)

	logging.Info("target EOF", "sid", sess.ID)

	// Если target больше не шлёт данные — клиенту тоже больше нечего писать.
	if !sess.ClientWriteClosed {
		_ = unix.Shutdown(sess.ClientFD, unix.SHUT_WR)
		sess.ClientWriteClosed = true
	}
}

// ---------- client read path ----------

// читает данные от клиента в ToTarget,
// после чего, парсит SOCKS handshake или делает relay.
func (s *SocksServer) handleClientReadable(sess *domain.Session) error {
	// Читаем из clientFD в буфер ToTarget.
	_, eof, err := sess.ToTarget.ReadFromFD(sess.ClientFD)
	if err != nil {
		//Если EAGAIN/EWOULDBLOCK, перестаем читать, данных нет.
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return err
	}

	// EOF: клиент закрыл запись.
	if eof {
		s.onClientEOF(sess)
		return nil
	}

	//на стадии greeting/request парсим протокол SOCKS5,
	//на стадии relaying пытаемся слить буфер в target.
	switch sess.State {
	case domain.StateGreeting:
		return s.stepGreeting(sess)
	case domain.StateRequest:
		return s.stepRequest(sess)
	case domain.StateRelaying:
		// Если target есть и есть данные — пробуем записать.
		if sess.TargetFD >= 0 && !sess.ToTarget.Empty() {
			return s.flushToTarget(sess)
		}
	}
	return nil
}

// snapshotToTarget делает копию текущего содержимого ToTarget (до max байт).
// Нужно, чтобы удобно парсить SOCKS handshake, т.к. ring buffer может быть "разрезан" на два сегмента.
func (s *SocksServer) snapshotToTarget(sess *domain.Session, max int) []byte {
	p1, p2 := sess.ToTarget.PeekRead()

	if len(p1)+len(p2) > max {
		return nil
	}

	// Склеиваем два сегмента в один []byte.
	b := make([]byte, 0, len(p1)+len(p2))
	b = append(b, p1...)
	b = append(b, p2...)
	return b
}

// парсит SOCKS5 greeting:
// клиент присылает VER, NMETHODS, METHODS
// сервер отвечает выбранным методом и переходит в StateRequest.
func (s *SocksServer) stepGreeting(sess *domain.Session) error {
	buf := s.snapshotToTarget(sess, MaxHandshakeIn)
	if buf == nil {
		return errors.New("handshake too large")
	}

	//минимум 2 байта: VER + NMETHODS.
	if len(buf) < 2 {
		return nil
	}

	ver := buf[0]
	nMethods := int(buf[1])

	if ver != socksVer5 {
		return errors.New("unsupported SOCKS version")
	}

	// Ждём, пока придёт весь список методов.
	if len(buf) < 2+nMethods {
		return nil
	}

	// Проверяем, поддерживает ли клиент NOAUTH (0x00).
	supportsNoAuth := false
	for _, m := range buf[2 : 2+nMethods] {
		if m == socksNoAuth {
			supportsNoAuth = true
			break
		}
	}

	if !supportsNoAuth {
		// Если NOAUTH нет — отвечаем 0xFF и закрываем соединение.
		_ = s.ep.Del(sess.ClientFD)
		_, _ = net.Write(sess.ClientFD, []byte{socksVer5, socksNoAccept})
		_ = net.Close(sess.ClientFD)
		s.sm.Remove(sess.ID)
		logging.Warn("greeting rejected (no auth)", "sid", sess.ID)
		return errSessionClosed
	}

	// Успех: кладём ответ в буфер ToClient.
	// Отправится на ближайшем EPOLLOUT клиента.
	s.enqueueToClient(sess, []byte{socksVer5, socksNoAuth})

	//чистим ToTarget от разобранных байтов
	sess.ToTarget.Consume(2 + nMethods)

	// Переходим к чтению SOCKS request.
	sess.State = domain.StateRequest

	logging.Info("greeting OK", "sid", sess.ID)
	return nil
}

// парсит SOCKS5 CONNECT request.
// Поддерживаем только CMD=CONNECT и ATYP=IPv4/Domain.
func (s *SocksServer) stepRequest(sess *domain.Session) error {
	buf := s.snapshotToTarget(sess, MaxHandshakeIn)
	if buf == nil {
		return errors.New("request too large")
	}

	// Минимум 4 байта: VER CMD RSV ATYP.
	if len(buf) < 4 {
		return nil
	}

	ver := buf[0]
	cmd := buf[1]
	atyp := buf[3]

	if ver != socksVer5 {
		return errors.New("invalid VER in request")
	}

	// Поддерживаем только CONNECT (0x01).
	if cmd != cmdConnect {
		logging.Warn("unsupported cmd", "sid", sess.ID, "cmd", cmd)
		s.replyAndClose(sess, repCommandNotSupport)
		return errSessionClosed
	}

	// Смещение после VER CMD RSV ATYP.
	off := 4

	switch atyp {
	case atypIPv4:
		// Формат: 4 байта IPv4 + 2 байта port.
		if len(buf) < off+4+2 {
			return nil
		}

		var ip [4]byte
		copy(ip[:], buf[off:off+4])
		off += 4

		port := binary.BigEndian.Uint16(buf[off : off+2])
		off += 2

		// Удаляем request из ToTarget.
		sess.ToTarget.Consume(off)

		logging.Info("connect request IPv4",
			"sid", sess.ID,
			"ip", ip,
			"port", port,
		)

		// Начинаем неблокирующий connect до target.
		s.startConnectIPv4(sess, ip, port)
		return nil

	case atypDomain:
		// Формат: 1 байт длина + domain bytes + 2 байта port.
		if len(buf) < off+1 {
			return nil
		}

		ln := int(buf[off])
		off++

		if len(buf) < off+ln+2 {
			return nil
		}

		host := string(buf[off : off+ln])
		off += ln

		port := binary.BigEndian.Uint16(buf[off : off+2])
		off += 2

		// Удаляем request из ToTarget.
		sess.ToTarget.Consume(off)

		// Сохраняем домен/порт
		sess.PendingDomain = host
		sess.PendingPort = port

		// Генерим dnsID, связываем его с сессией.
		dnsID, err := s.dns.NewQueryID()
		if err != nil {
			// слишком много pending DNS — отвечаем ошибкой и закрываем
			s.replyAndClose(sess, repGeneralFailure) // или repHostUnreachable
			return errSessionClosed
		}
		s.sm.BindDNS(sess.ID, dnsID)
		sess.DNSQueryID = dnsID

		// Переходим в состояние ожидания DNS.
		sess.State = domain.StateResolvingDNS

		logging.Info("connect request domain",
			"sid", sess.ID,
			"domain", host,
			"port", port,
			"dnsID", dnsID,
		)

		// Запускаем неблокирующий DNS запрос по UDP.
		if err := s.dns.StartResolve(sess.ID, dnsID, host); err != nil {
			logging.Error("dns start failed", "sid", sess.ID, "error", err)
			s.replyAndClose(sess, repHostUnreachable)
			return errSessionClosed
		}
		return nil

	default:
		logging.Warn("addr type not supported", "sid", sess.ID, "atyp", atyp)
		s.replyAndClose(sess, repAddrTypeNotSupport)
		return errSessionClosed
	}
}

// ---------- connect & relay ----------

// создаёт неблокирующее TCP подключение к target.
// завершение connect ловим через EPOLLOUT и проверку SO_ERROR.
func (s *SocksServer) startConnectIPv4(sess *domain.Session, ip [4]byte, port uint16) {
	// Создаём сокет и инициируем non-blocking connect.
	tfd, err := net.ConnectTcp4(ip, int(port))
	if err != nil {
		logging.Error("connect tcp4 failed", "sid", sess.ID, "ip", ip, "port", port, "error", err)
		s.replyAndClose(sess, repHostUnreachable)
		return
	}

	// Подписываем targetFD на EPOLLOUT (завершение connect).
	if err := s.ep.Add(tfd, net.EventWrite|net.EventError|net.EventHup|net.EventRDHup); err != nil {
		logging.Error("epoll add target failed", "sid", sess.ID, "tfd", tfd, "error", err)
		_ = net.Close(tfd)
		s.replyAndClose(sess, repGeneralFailure)
		return
	}

	// Привязываем targetFD к сессии.
	s.sm.AttachTarget(sess.ID, tfd)

	sess.PendingIP4 = ip

	// Переходим в состояние "ждём завершения connect".
	sess.State = domain.StateConnectingTarget

	logging.Info("target connect started", "sid", sess.ID, "tfd", tfd, "ip", ip, "port", port)
}

// вызывается, когда targetFD стал writable в состоянии ConnectingTarget.
// EPOLLOUT не гарантирует успех.
func (s *SocksServer) finishTargetConnect(sess *domain.Session) error {
	fd := sess.TargetFD

	// Проверяем, чем завершился connect()
	nerr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
	if err != nil {
		return err
	}
	if nerr != 0 {
		return syscall.Errno(nerr)
	}

	// Для SOCKS reply нужно вернуть BND.ADDR/BND.PORT — локальный адрес сокета target.
	bndAddr, bndPort, err := net.GetLocalIp4Addr(fd)
	if err != nil {
		return err
	}

	// Формируем SOCKS5 reply (10 байт для IPv4):
	// VER, REP, RSV, ATYP, BND.ADDR(4), BND.PORT(2).
	resp := make([]byte, 10)
	resp[0] = socksVer5
	resp[1] = repSucceeded
	resp[2] = 0x00
	resp[3] = atypIPv4
	copy(resp[4:8], bndAddr[:])
	binary.BigEndian.PutUint16(resp[8:10], uint16(bndPort))

	// Отправляем reply напрямую в клиентский сокет.
	if _, err := net.Write(sess.ClientFD, resp); err != nil {
		return err
	}

	// CONNECT успешен — переходим в режим relaying.
	sess.State = domain.StateRelaying

	logging.Info("connect established", "sid", sess.ID, "bndAddr", bndAddr, "bndPort", bndPort)
	return nil
}

// читаем данные от target в буфер ToClient,
// и пытаемся сразу сделать flush клиенту.
func (s *SocksServer) handleTargetReadable(sess *domain.Session) error {
	_, eof, err := sess.ToClient.ReadFromFD(sess.TargetFD)
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return err
	}

	// target закрыл запись -> инициируем half-close на клиент.
	if eof {
		s.onTargetEOF(sess)
		return nil
	}

	if !sess.ToClient.Empty() {
		return s.flushToClient(sess)
	}
	return nil
}

// сливаем накопленные client->target байты в targetFD.
func (s *SocksServer) flushToTarget(sess *domain.Session) error {
	if sess.TargetFD < 0 {
		return nil
	}
	_, err := sess.ToTarget.WriteToFD(sess.TargetFD)
	return err
}

// сливаем накопленные target->client байты в clientFD.
func (s *SocksServer) flushToClient(sess *domain.Session) error {
	_, err := sess.ToClient.WriteToFD(sess.ClientFD)
	return err
}

// ---------- epoll interests ----------

// выставляет EPOLLIN/EPOLLOUT для clientFD и targetFD.
func (s *SocksServer) updateInterests(sess *domain.Session) {
	cm := net.EventHup | net.EventRDHup | net.EventError

	// Чтение с клиента включаем только если:
	//клиент ещё не закрыл запись,
	// ToTarget не полон ,
	// в состоянии, где чтение от клиента полезно (Greeting/Request/Relaying).
	if !sess.ClientReadClosed &&
		!sess.ToTarget.Full() &&
		(sess.State == domain.StateGreeting || sess.State == domain.StateRequest || sess.State == domain.StateRelaying) {
		cm |= net.EventRead
	}

	// Запись клиенту нужна только когда ToClient не пуст.
	if !sess.ToClient.Empty() {
		cm |= net.EventWrite
	}

	// epoll.Mod делаем только если маска реально изменилась
	if uint32(cm) != sess.ClientMask {
		_ = s.ep.Mod(sess.ClientFD, cm)
		sess.ClientMask = uint32(cm)
	}

	// Если targetFD ещё нет
	if sess.TargetFD < 0 {
		return
	}

	tm := net.EventHup | net.EventRDHup | net.EventError

	switch sess.State {
	case domain.StateConnectingTarget:
		// При connect ждём EPOLLOUT (connect завершился).
		tm |= net.EventWrite

	case domain.StateRelaying:
		// Чтение с target включаем, если target не EOF и ToClient не полон.
		if !sess.TargetReadClosed && !sess.ToClient.Full() {
			tm |= net.EventRead
		}
		// Запись в target нужна, когда есть данные в ToTarget.
		if !sess.ToTarget.Empty() {
			tm |= net.EventWrite
		}
	}

	if uint32(tm) != sess.TargetMask {
		_ = s.ep.Mod(sess.TargetFD, tm)
		sess.TargetMask = uint32(tm)
	}
}

// ---------- close logic ----------

// принимает решение, можно ли полностью закрыть сессию.
// Закрываем только когда это безопасно (нет данных в буферах и обе стороны дошли до EOF).
func (s *SocksServer) tryCloseIfDone(sess *domain.Session) {
	// Если target ещё не создан, нельзя закрывать сессию просто потому что "буферы пустые".
	// Иначе убьём соединение после greeting и до request.
	if sess.TargetFD < 0 {
		// Закрываем только если клиент реально закрылся.
		if sess.ClientReadClosed || sess.ClientWriteClosed {
			s.closeSession(sess)
		}
		return
	}

	// В relay закрываем только когда:
	//обе стороны закрыли запись (мы получили EOF),
	//и оба буфера пусты.
	if sess.ClientReadClosed && sess.TargetReadClosed && sess.ToClient.Empty() && sess.ToTarget.Empty() {
		s.closeSession(sess)
		return
	}

	// Если клиент закрыл запись, и мы уже слили все client->target данные,
	// то закрываем write-направление на target (half-close).
	if sess.ClientReadClosed && sess.ToTarget.Empty() && !sess.TargetWriteClosed {
		_ = unix.Shutdown(sess.TargetFD, unix.SHUT_WR)
		sess.TargetWriteClosed = true
	}
}

// аварийное завершение сессии:
func (s *SocksServer) failSession(sess *domain.Session, repCode byte, err error) {
	logging.Error("session failed",
		"sid", sess.ID,
		"state", stateName(sess.State),
		"rep", repCode,
		"error", err,
	)

	// Reply формата IPv4 с нулевым BND.ADDR/BND.PORT.
	resp := []byte{socksVer5, repCode, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}

	_ = s.ep.Del(sess.ClientFD)
	if sess.TargetFD >= 0 {
		_ = s.ep.Del(sess.TargetFD)
	}

	// Пытаемся отправить reply об ошибке.
	_, _ = net.Write(sess.ClientFD, resp)

	_ = net.Close(sess.ClientFD)
	if sess.TargetFD >= 0 {
		_ = net.Close(sess.TargetFD)
	}

	//удаление сессии из всех индексов (byFD/byDNS)
	s.sm.Remove(sess.ID)
}

// нормальное закрытие: удаляем fd из epoll, закрываем fd, удаляем сессию.
func (s *SocksServer) closeSession(sess *domain.Session) {
	logging.Info("session closed", "sid", sess.ID, "cfd", sess.ClientFD, "tfd", sess.TargetFD)

	_ = s.ep.Del(sess.ClientFD)
	_ = net.Close(sess.ClientFD)

	if sess.TargetFD >= 0 {
		_ = s.ep.Del(sess.TargetFD)
		_ = net.Close(sess.TargetFD)
	}

	s.sm.Remove(sess.ID)
}

// отправить клиенту SOCKS reply (успех/ошибка) и сразу закрыть сессию.
// когда дальше продолжать бесполезно (unsupported cmd/atyp, DNS fail, etc.).
func (s *SocksServer) replyAndClose(sess *domain.Session, repCode byte) {
	logging.Warn("reply and close",
		"sid", sess.ID,
		"state", stateName(sess.State),
		"rep", repCode,
	)

	rep := []byte{socksVer5, repCode, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}

	_ = s.ep.Del(sess.ClientFD)
	if sess.TargetFD >= 0 {
		_ = s.ep.Del(sess.TargetFD)
	}

	_, _ = net.Write(sess.ClientFD, rep)

	_ = net.Close(sess.ClientFD)
	if sess.TargetFD >= 0 {
		_ = net.Close(sess.TargetFD)
	}

	s.sm.Remove(sess.ID)
}

// помещаем байты в буфер ToClient, к примеру для greeting
func (s *SocksServer) enqueueToClient(sess *domain.Session, b []byte) {
	if len(b) > sess.ToClient.Writable() {
		logging.Error("enqueue overflow", "sid", sess.ID, "need", len(b), "writable", sess.ToClient.Writable())
		s.failAndCloseNow(sess)
		return
	}

	// Копируем байты в ring buffer.
	sess.ToClient.WriteBytes(b)
}

// экстренно закрытие сессии
func (s *SocksServer) failAndCloseNow(sess *domain.Session) {
	logging.Error("fail and close now", "sid", sess.ID, "state", stateName(sess.State))
	s.closeSession(sess)
}
