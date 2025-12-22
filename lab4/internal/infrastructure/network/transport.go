package network

import (
	"fmt"
	"lab4/internal/application"
	snakespb "lab4/internal/infrastructure/proto"
	"log"
	"net"

	"google.golang.org/protobuf/proto"
)

// Handler — куда транспорт отдаёт уже распарсенные GameMessage
type Handler interface {
	HandlePacket(msg *snakespb.GameMessage, addr *net.UDPAddr)
}

// Transport — низкоуровневая сеть: UDP unicast + UDP multicast
type Transport struct {
	mcGroup string
	mcPort  int

	mcConn *net.UDPConn // только приём multicast
	conn   *net.UDPConn // всё остальное (unicast + отправка multicast)

	handler Handler

	log *log.Logger
}

// NewTransport — поднимаем 2 UDP-сокета и запускаем 2 горутины чтения
func NewTransport(h Handler, mcGroup string, mcPort int, logger *log.Logger) (*Transport, error) {
	if logger == nil {
		logger = log.Default()
	}
	t := &Transport{
		mcGroup: mcGroup,
		mcPort:  mcPort,
		handler: h,
		log:     logger,
	}

	// 1) слушаем multicast-группу (приём discover/announcement)
	var err error
	t.mcConn, err = listenMulticast(mcGroup, mcPort)
	if err != nil {
		return nil, fmt.Errorf("multicast listen: %w", err)
	}

	// 2) обычный UDP-сокет для unicast (и им же отправляем multicast)
	t.conn, err = listenUnicastAnyPort()
	if err != nil {
		return nil, fmt.Errorf("unicast listen: %w", err)
	}

	// отдельные циклы чтения для multicast и unicast
	go t.readMulticastLoop()
	go t.readUnicastLoop()

	return t, nil
}

// сокет №1 — только приём multicast
func listenMulticast(group string, port int) (*net.UDPConn, error) {
	addr := &net.UDPAddr{
		IP:   net.ParseIP(group),
		Port: port,
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadBuffer(64 * 1024); err != nil {
		return nil, err
	}
	return conn, nil
}

// сокет №2 — обычный UDP, порт выбирает ОС (Port = 0)
func listenUnicastAnyPort() (*net.UDPConn, error) {
	addr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: 0,
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadBuffer(64 * 1024); err != nil {
		return nil, err
	}
	return conn, nil
}

// readMulticastLoop — бесконечно читаем multicast и передаём в общий обработчик
func (t *Transport) readMulticastLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, src, err := t.mcConn.ReadFromUDP(buf)
		if err != nil {

			return
		}
		t.handleRawPacket(buf[:n], src)
	}
}

// readUnicastLoop — бесконечно читаем unicast и передаём в общий обработчик
func (t *Transport) readUnicastLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, src, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		t.handleRawPacket(buf[:n], src)
	}
}

// handleRawPacket — общий разбор пакета: self-check -> proto.Unmarshal -> handler.HandlePacket
func (t *Transport) handleRawPacket(data []byte, src *net.UDPAddr) {
	if t.handler == nil {
		return
	}

	// защита от “эхо”
	if t.IsSelfPacketUnicast(src) {
		t.log.Printf("RECV from=%s (self-packet) — ignored", src)
		return
	}

	// декодируем protobuf GameMessage
	var msg snakespb.GameMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		t.log.Printf("RECV from=%s unmarshal error: %v", src, err)
		return
	}

	t.log.Printf("RECV from=%s bytes=%d seq=%d type=%s",
		src, len(data), msg.GetMsgSeq(), application.MsgTypeName(&msg))
	t.handler.HandlePacket(&msg, src)
}

func (t *Transport) IsSelfPacketUnicast(src *net.UDPAddr) bool {
	if t.conn == nil || src == nil {
		return false
	}

	local := t.conn.LocalAddr().(*net.UDPAddr)

	if local.IP.IsUnspecified() {
		return src.Port == local.Port
	}

	return src.IP.Equal(local.IP) && src.Port == local.Port
}

// SendTo — отправка unicast UDP пакета
func (t *Transport) SendTo(msg *snakespb.GameMessage, addr *net.UDPAddr) error {
	data, err := proto.Marshal(msg)
	if err != nil {

		return err
	}
	n, err := t.conn.WriteToUDP(data, addr)
	if err != nil {
		t.log.Printf("SEND to=%s seq=%d type=%s ERROR: %v",
			addr, msg.GetMsgSeq(), application.MsgTypeName(msg), err)
		return err
	}

	t.log.Printf("SEND to=%s bytes=%d seq=%d type=%s",
		addr, n, msg.GetMsgSeq(), application.MsgTypeName(msg))
	return nil
}

// SendToMulticast — отправка анонсов на multicast-группу
func (t *Transport) SendToMulticast(msg *snakespb.GameMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	dst := &net.UDPAddr{
		IP:   net.ParseIP(t.mcGroup),
		Port: t.mcPort,
	}
	n, err := t.conn.WriteToUDP(data, dst)
	if err != nil {
		t.log.Printf("SEND-MC to=%s seq=%d type=%s ERROR: %v",
			dst, msg.GetMsgSeq(), application.MsgTypeName(msg), err)
		return err
	}
	t.log.Printf("SEND-MC to=%s bytes=%d seq=%d type=%s",
		dst, n, msg.GetMsgSeq(), application.MsgTypeName(msg))
	return nil
}

// LocalAddr — чтобы знать, на каком порту сидит наш unicast-сокет
func (t *Transport) LocalAddr() *net.UDPAddr {
	if t.conn == nil {
		return nil
	}
	return t.conn.LocalAddr().(*net.UDPAddr)
}
