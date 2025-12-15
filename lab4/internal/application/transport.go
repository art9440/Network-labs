package application

import (
	snakespb "lab4/internal/infrastructure/proto"
	"net"
)

type Transport interface {
	SendTo(msg *snakespb.GameMessage, addr *net.UDPAddr) error
	SendToMulticast(msg *snakespb.GameMessage) error
	LocalAddr() *net.UDPAddr
}
