package net

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func CreateTCPListener(port int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return -1, err
	}

	ok := false
	defer func() {
		if !ok {
			unix.Close(fd)
		}
	}()
	//повторное использование сокета
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); err != nil {
		return -1, err
	}
	//перевод в неблокирующийся режим
	if err := unix.SetNonblock(fd, true); err != nil {
		return -1, err
	}
	addr := &unix.SockaddrInet4{
		Port: port,
		Addr: [4]byte{0, 0, 0, 0},
	}
	//привязываем сокет к адресу и порту
	if err := unix.Bind(fd, addr); err != nil {
		return -1, err
	}
	//перевод сокета в режим прослушивания
	if err := unix.Listen(fd, unix.SOMAXCONN); err != nil {
		return -1, err
	}
	ok = true
	return fd, nil

}

func Accept(fd int) (int, *unix.SockaddrInet4, error) {
	nfd, sa, err := unix.Accept(fd)
	if err != nil {
		return -1, nil, err
	}

	if err := unix.SetNonblock(nfd, true); err != nil {
		_ = unix.Close(nfd)
		return -1, nil, err
	}

	var inet4 *unix.SockaddrInet4
	if sa4, ok := sa.(*unix.SockaddrInet4); ok {
		inet4 = sa4
	}

	return nfd, inet4, nil
}

func Close(fd int) error {
	return unix.Close(fd)
}

func Read(fd int, buf []byte) (int, error) {
	n, err := unix.Read(fd, buf)
	if err != nil {
		return -1, err
	}
	return n, nil
}

func Write(fd int, buf []byte) (int, error) {
	return unix.Write(fd, buf)
}

func ConnectTcp4(ip [4]byte, port int) (int, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		return -1, err
	}

	ok := false
	defer func() {
		if !ok {
			unix.Close(fd)
		}
	}()

	if err := unix.SetNonblock(fd, true); err != nil {
		return -1, err
	}

	addr := &unix.SockaddrInet4{
		Port: port,
		Addr: ip,
	}
	errConn := unix.Connect(fd, addr)
	if errConn != nil && !errors.Is(errConn, unix.EINPROGRESS) {
		return -1, errConn
	}
	ok = true
	return fd, nil
}

func GetLocalIp4Addr(fd int) ([4]byte, int, error) {
	var zero [4]byte
	sa, err := unix.Getsockname(fd)
	if err != nil {
		return zero, 0, err
	}
	sa4, ok := sa.(*unix.SockaddrInet4)
	if !ok {
		return zero, 0, fmt.Errorf("GetLocalIp4Addr: not a SockaddrInet4")
	}
	return sa4.Addr, sa4.Port, nil
}
