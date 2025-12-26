package net

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func CreateUDPSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return -1, err
	}

	ok := false
	defer func() {
		if !ok {
			unix.Close(fd)
		}
	}()

	// Неблокирующий режим
	if err := unix.SetNonblock(fd, true); err != nil {
		return -1, err
	}

	ok = true
	return fd, nil
}

func SendToIPv4(fd int, ip [4]byte, port int, data []byte) error {
	addr := &unix.SockaddrInet4{Port: port,
		Addr: ip,
	}
	if err := unix.Sendto(fd, data, 0, addr); err != nil {
		return err
	}
	return nil
}

func RecvFromIPv4(fd int, buf []byte) (n int, err error) {
	n, sa, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		return 0, err
	}
	_, ok := sa.(*unix.SockaddrInet4)
	if !ok {
		return 0, fmt.Errorf("invalid socket address")
	}
	return n, nil
}
