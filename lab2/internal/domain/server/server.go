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
	"time"

	"golang.org/x/sync/semaphore"
)

const (
	TYPE = "tcp"
	HOST = "localhost"
)

const (
	StatusSuccess = 0x01
	StatusError   = 0x00
	chunksize     = 256
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
	conn net.Conn
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

	resultCh := make(chan Result, s.maxConcurrentConnection)

	//goroutin for waiting respond from handle goroutin
	go func() {
		for result := range resultCh {
			if result.err != nil {
				fmt.Printf("%s: %v\n", result.info, result.err)
				sendAnswer(false, result.conn)
			} else {
				sendAnswer(true, result.conn)
			}
			result.conn.Close()
		}
	}()

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
		//creating goroutin to handle connection
		go func(c net.Conn) {
			defer sem.Release(1)
			s.handleConn(c, resultCh)
		}(conn)

	}
}

// sending resond
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

// reading file stat
func readInfo(c net.Conn, buf []byte, resultCh chan<- Result) bool {
	if _, err := c.Read(buf); err != nil {
		resultCh <- Result{info: "While reading file info", err: err, conn: c}
		return false
	}

	return true
}

func (s *tcpServer) handleConn(c net.Conn, resultCh chan Result) {
	//reading header
	var header [2]byte

	if !readInfo(c, header[:], resultCh) {
		return
	}

	bytesNameLen := binary.BigEndian.Uint16(header[:])
	fmt.Println(bytesNameLen)
	//reading filename
	bytesName := make([]byte, bytesNameLen)

	if !readInfo(c, bytesName, resultCh) {
		return
	}

	fmt.Println(string(bytesName))

	filename := string(bytesName)
	//creating file
	file, err := createFile(filename)
	if err != nil {
		resultCh <- Result{info: "While creating file: " + filename, err: err, conn: c}
		return
	}

	defer file.Close()

	//reading file size
	var fileSize [8]byte
	if !readInfo(c, fileSize[:], resultCh) {
		return
	}

	size := binary.BigEndian.Uint64(fileSize[:])

	var totalRead uint64 = 0
	buff := make([]byte, chunksize)

	var nowRead int = 0
	startTime := time.Now()
	//init ticker for speed statistics
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	//speed stat
	go func() {
		for range ticker.C {
			instantSpeed := float64(nowRead) / 3.0
			avgSpeed := float64(totalRead) / time.Since(startTime).Seconds()
			fmt.Printf("File: %s. Speed( instant: %.1f | avg: %.1f KB/s)\n", filename, instantSpeed/1024, avgSpeed/1024)

			nowRead = 0
		}
	}()

	//reading file
	for totalRead < size {
		remaining := size - totalRead
		readSize := chunksize
		if remaining < chunksize {
			readSize = int(remaining)
		}

		n, err := io.ReadFull(c, buff[:readSize])
		if err != nil {
			resultCh <- Result{info: "While reading file", err: err, conn: c}
			return
		}

		_, err = file.Write(buff[:n])
		if err != nil {
			resultCh <- Result{info: "Write to file failed", err: err, conn: c}
			return
		}

		nowRead += n
		totalRead += uint64(n)
	}
	fmt.Println(" totalRead ", totalRead, " Size ", size)

	if totalRead == size {
		resultCh <- Result{info: "success", err: nil, conn: c}
	} else {
		resultCh <- Result{info: "failed", err: fmt.Errorf("size not equals"), conn: c}
	}
}

// creating file
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

// validating file name
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
