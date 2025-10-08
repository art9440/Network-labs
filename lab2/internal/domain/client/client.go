package client

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"

	"net"
	"os"
	"path/filepath"
)

const (
	chunksize   = 256
	maxFileSize = 1_099_511_627_776
)

type Client interface {
	UploadingFile(conn net.Conn) error
	ConnectingServer() error
}

type tcpClient struct {
	ipAddr     string
	serverPort string
}

func StartClient(serverIP string, serverPort string) error {
	var client Client = tcpClient{serverIP, serverPort}
	if err := client.ConnectingServer(); err != nil {
		return err
	}

	return nil
}

func (c tcpClient) UploadingFile(conn net.Conn) error {

	var path string
	fmt.Println("Write the path to the uploading file.")
	fmt.Scan(&path)
	name := filepath.Base(path)

	//перевели строку в слайс байтов
	bytesName := []byte(name)

	if len(bytesName) > 4096 {
		return fmt.Errorf("filename is too long: %d", len(bytesName))
	}

	//засовываем длину имени в 2 байта
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(bytesName)))
	fmt.Println(uint16(len(bytesName)))
	//отправили header
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}

	//отправили имя файла
	if _, err := conn.Write(bytesName); err != nil {
		return err
	}

	//открываем файл для чтения
	file, err := os.OpenFile(path, os.O_RDONLY, 0666)
	if err != nil {
		return err
	}

	defer file.Close()

	//отправка размера файла
	fi, err := file.Stat()
	if err != nil {
		return err
	}

	if fi.Size() > maxFileSize {
		return fmt.Errorf("file size is bigger then 1tb. size: %d", fi.Size())
	}

	var sz [8]byte
	binary.BigEndian.PutUint64(sz[:], uint64(fi.Size()))
	if _, err := conn.Write(sz[:]); err != nil {
		return err
	}

	//отправка самого файла
	data := make([]byte, chunksize)
	for {
		_, err := file.Read(data)

		if err := sendTo(conn, data); err != nil {
			return err
		}

		if err == io.EOF {
			break
		}
	}
	waitingRespond(conn)

	return nil
}

func waitingRespond(conn net.Conn) {
	statusBuf := make([]byte, 1)

	_, err := conn.Read(statusBuf)
	if err != nil {
		fmt.Println("Can`t get respond")
	}

	status := statusBuf[0]

	switch status {
	case 0x01:
		fmt.Println("Success!")
	case 0x00:
		fmt.Println("Fail")
	default:
		fmt.Printf("Unknown status: %x\n", status)
	}

}

func sendTo(conn net.Conn, chunk []byte) error {
	for len(chunk) > 0 {
		n, err := conn.Write(chunk)
		if err != nil {
			return err
		}
		chunk = chunk[n:]
	}
	return nil
}

func (c tcpClient) ConnectingServer() error {
	conn, err := net.Dial("tcp", net.JoinHostPort(c.ipAddr, c.serverPort))
	if err != nil {
		return err
	}

	defer conn.Close()

	if err := c.UploadingFile(conn); err != nil {
		return err
	}

	return nil
}
