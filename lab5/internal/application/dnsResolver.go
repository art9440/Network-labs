package application

import (
	"errors"
	"lab5/internal/domain"
	"lab5/internal/infrastructure/net"
	"lab5/internal/logging"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/sys/unix"
)

// DNSResolved — результат одного DNS-запроса.
type DNSResolved struct {
	ID uint16
	IP [4]byte
	OK bool
}

var errNoFreeDNSID = errors.New("no free DNS query id (pending map is full)")

// DNSResolver — неблокирующий DNS-резолвер поверх UDP.
type DNSResolver struct {
	fd int
	to unix.SockaddrInet4

	// pending: dnsID -> SessionID
	pending map[uint16]domain.SessionID

	// локальный генератор DNS ID
	nextID uint16
}

func NewDNSResolver(fd int, addrResolve unix.SockaddrInet4) *DNSResolver {
	return &DNSResolver{
		fd:      fd,
		to:      addrResolve,
		pending: make(map[uint16]domain.SessionID),
		nextID:  1,
	}
}

func (d *DNSResolver) GetFd() int { return d.fd }

// NewQueryID выдаёт свободный dnsID, который не занят в pending.
// Возвращает ошибку, если все ID заняты
func (d *DNSResolver) NewQueryID() (uint16, error) {
	if len(d.pending) >= 0xFFFF {
		return 0, errNoFreeDNSID
	}

	// Пробуем найти свободный ID.
	// В обычной ситуации мы найдём его сразу или за пару шагов.
	for i := 0; i < 0xFFFF; i++ {
		d.nextID++
		if d.nextID == 0 { // 0 запрещаем
			d.nextID++
		}

		// Если такого ID нет в pending — он свободен.
		if _, busy := d.pending[d.nextID]; !busy {
			return d.nextID, nil
		}
	}

	return 0, errNoFreeDNSID
}

func (d *DNSResolver) StartResolve(sessID domain.SessionID, dnsID uint16, host string) error {
	if len(host) == 0 {
		return errors.New("empty host")
	}
	if host[len(host)-1] != '.' {
		host += "."
	}

	name, err := dnsmessage.NewName(host)
	if err != nil {
		return err
	}

	msg := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               dnsID,
			RecursionDesired: true,
		},
		Questions: []dnsmessage.Question{{
			Name:  name,
			Type:  dnsmessage.TypeA,
			Class: dnsmessage.ClassINET,
		}},
	}

	packed, err := msg.Pack()
	if err != nil {
		return err
	}

	// Регистрируем запрос ДО sendto, чтобы быстрый ответ не потерялся.
	d.pending[dnsID] = sessID

	if err := net.SendToIPv4(d.fd, d.to.Addr, d.to.Port, packed); err != nil {
		delete(d.pending, dnsID)
		return err
	}

	return nil
}

func (d *DNSResolver) OnReadable() []DNSResolved {
	out := make([]DNSResolved, 0, 8)
	buf := make([]byte, 2048)

	for {
		n, err := net.RecvFromIPv4(d.fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				return out
			}
			logging.Error("dns recvfrom error", "error", err)
			return out
		}

		var p dnsmessage.Parser
		h, err := p.Start(buf[:n])
		if err != nil {
			continue
		}

		_, ok := d.pending[h.ID]
		if !ok {
			continue
		}
		delete(d.pending, h.ID)

		if err := p.SkipAllQuestions(); err != nil {
			out = append(out, DNSResolved{ID: h.ID, OK: false})
			continue
		}

		var ip [4]byte
		got := false

		for {
			ah, err := p.AnswerHeader()
			if err == dnsmessage.ErrSectionDone {
				break
			}
			if err != nil {
				break
			}

			if ah.Type == dnsmessage.TypeA {
				a, err := p.AResource()
				if err == nil {
					ip = a.A
					got = true
					break
				}
				_ = p.SkipAnswer()
				continue
			}

			_ = p.SkipAnswer()
		}

		out = append(out, DNSResolved{ID: h.ID, IP: ip, OK: got})
	}
}
