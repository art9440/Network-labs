package net

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// EventMask — абстрактная маска событий epoll,
// чтобы не работать напрямую с unix.EPOLL*.
type EventMask uint32

const (
	// fd готов к чтению
	EventRead EventMask = 1 << iota
	// fd готов к записи / завершился non-blocking connect
	EventWrite
	// ошибка на fd
	EventError
	// соединение разорвано (HUP)
	EventHup
	// peer закрыл запись (FIN, half-close)
	EventRDHup
)

type Epoller struct {
	fd     int  // epoll instance fd
	closed bool // флаг закрытия
}

func NewEppoller() (*Epoller, error) {
	fd, err := unix.EpollCreate1(0)
	if err != nil {
		return nil, err
	}
	return &Epoller{fd: fd}, nil
}

// Close закрывает epoll instance.
func (e *Epoller) Close() error {
	if e == nil || e.closed {
		return nil
	}
	e.closed = true
	return unix.Close(e.fd)
}

// Add регистрирует fd в epoll с нужными событиями.
func (e *Epoller) Add(fd int, events EventMask) error {
	if e.closed {
		return fmt.Errorf("epoller is closed")
	}
	ev := unix.EpollEvent{
		Events: maskToEpoll(events),
		Fd:     int32(fd),
	}
	return unix.EpollCtl(e.fd, unix.EPOLL_CTL_ADD, fd, &ev)
}

// Mod изменяет набор событий для fd.
func (e *Epoller) Mod(fd int, events EventMask) error {
	if e.closed {
		return fmt.Errorf("epoller is closed")
	}
	ev := unix.EpollEvent{
		Events: maskToEpoll(events),
		Fd:     int32(fd),
	}
	return unix.EpollCtl(e.fd, unix.EPOLL_CTL_MOD, fd, &ev)
}

// Del удаляет fd из epoll.
func (e *Epoller) Del(fd int) error {
	if e.closed {
		return fmt.Errorf("epoller is closed")
	}
	return unix.EpollCtl(e.fd, unix.EPOLL_CTL_DEL, fd, nil)
}

// Wait ждёт события epoll.
func (e *Epoller) Wait(out []unix.EpollEvent, timeoutMs int) (int, error) {
	if e.closed {
		return 0, fmt.Errorf("epoller is closed")
	}
	for {
		n, err := unix.EpollWait(e.fd, out, timeoutMs)
		if err == nil {
			return n, nil
		}
		// повторяем ожидание при сигнале
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return 0, err
	}
}

func maskToEpoll(mask EventMask) uint32 {
	// ошибки и закрытия слушаем всегда
	ev := uint32(unix.EPOLLERR | unix.EPOLLHUP | unix.EPOLLRDHUP)

	if mask&EventRead != 0 {
		ev |= unix.EPOLLIN
	}
	if mask&EventWrite != 0 {
		ev |= unix.EPOLLOUT
	}
	return ev
}

func EpollToMask(ev uint32) EventMask {
	var mask EventMask

	if ev&unix.EPOLLIN != 0 {
		mask |= EventRead
	}
	if ev&unix.EPOLLOUT != 0 {
		mask |= EventWrite
	}
	if ev&unix.EPOLLHUP != 0 {
		mask |= EventHup
	}
	if ev&unix.EPOLLERR != 0 {
		mask |= EventError
	}
	if ev&unix.EPOLLRDHUP != 0 {
		mask |= EventRDHup
	}

	return mask
}
