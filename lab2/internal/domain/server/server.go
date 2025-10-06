package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/semaphore"
)

const (
	TYPE = "tcp"
	HOST = "localhost"
)

type Server interface {
	Serve() error
}

type tcpServer struct {
	maxConcurrentConnection int64
	listener                net.Listener
}

type Result struct {
	info string
	err  error
}

func StartServer(port string) error {
	l, err := net.Listen(TYPE, HOST+":"+port)
	if err != nil {
		return err
	}

	s := &tcpServer{maxConcurrentConnection: 10, listener: l}

	return s.Serve()
}

func (s *tcpServer) Serve() error {
	defer s.listener.Close()

	ctx := context.Background()
	sem := semaphore.NewWeighted(s.maxConcurrentConnection)

	resultCh := make(chan Result)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			fmt.Println("Something went wrong while connecting")
			conn.Close()
			continue
		}

		if err := sem.Acquire(ctx, 1); err != nil {
			conn.Close()
			fmt.Println("Something went wrong while acquiring semaphor")
			continue
		}

		go func(c net.Conn) {
			defer sem.Release(1)
			s.handleConn(c, resultCh)
		}(conn)
		result := <-resultCh
		if result.err != nil {
			fmt.Println(result.info, "Something went wrong while handling: %v", err)
			sendAnswer(false, conn)
		} else {
			sendAnswer(true, conn)
		}
	}
}

const (
	StatusSuccess = 0x01
	StatusError   = 0x00
)

func sendAnswer(success bool, conn net.Conn) {
	if success {
		_, err := conn.Write([]byte{StatusSuccess})
		if err != nil {
			fmt.Println("Can`t send answer")
		}
		return
	}

	_, err := conn.Write([]byte{StatusError})

	if err != nil {
		fmt.Println("Can`t send answer")
	}
}

func (s *tcpServer) handleConn(c net.Conn, resultCh chan Result) {
	//reading header
	var header [2]byte

	if _, err := c.Read(header[:]); err != nil {
		resultCh <- Result{info: "While reading header", err: err}
		return
	}

	bytesNameLen := binary.BigEndian.Uint16(header[:])
	fmt.Println(bytesNameLen)
	//reading filename
	bytesName := make([]byte, bytesNameLen)

	if _, err := c.Read(bytesName); err != nil {
		resultCh <- Result{info: "While reading header", err: err}
		return
	}

	fmt.Println(string(bytesName))

	filename := string(bytesName)
	//creating file
	file, err := createFile(filename)
	if err != nil {
		resultCh <- Result{info: "While creating file: " + filename, err: err}
		return
	}

	defer file.Close()
	//reading file size
	var fileSize [8]byte

	if _, err := c.Read(fileSize[:]); err != nil {
		resultCh <- Result{info: "While reading file size", err: err}
		return
	}

	const chunksize = 256
	size := binary.BigEndian.Uint64(fileSize[:])

	var totalRead uint64 = 0
	buff := make([]byte, chunksize)
	//reading file
	for totalRead < size {
		remaining := size - totalRead
		readSize := chunksize
		if remaining < chunksize {
			readSize = int(remaining)
		}

		n, err := io.ReadFull(c, buff[:readSize])
		if err != nil {
			resultCh <- Result{info: "While reading file", err: err}
			return
		}

		_, err = file.Write(buff[:n])
		if err != nil {
			resultCh <- Result{info: "Write to file failed", err: err}
			return
		}

		totalRead += uint64(n)
	}
	fmt.Println(" totalRead ", totalRead, " Size ", size)

	if totalRead == size {
		resultCh <- Result{info: "success", err: nil}
	} else {
		resultCh <- Result{info: "failed", err: fmt.Errorf("size not equals")}
	}
}

func createFile(filename string) (*os.File, error) {
	dir := "/home/art9440/VScodeProjects/GO/network-labs/lab2/internal/domain/server/uploads"

	base, err := safeBaseName(filename)
	if err != nil {
		return nil, err
	}

	full := filepath.Join(dir, base)
	f, err := os.Create(full)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func safeBaseName(name string) (string, error) {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" || base == "." || base == ".." || base != name {
		return "", errors.New("bad filename")
	}
	if strings.ContainsAny(base, `/\`) {
		return "", errors.New("path separators not allowed")
	}
	return base, nil
}
