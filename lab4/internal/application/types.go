package application

import (
	snakespb "lab4/internal/infrastructure/proto"
	"net"
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
