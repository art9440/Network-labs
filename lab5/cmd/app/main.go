package main

import (
	"fmt"
	"lab5/internal/application"
	"lab5/internal/logging"
	"os"
	"strconv"
)

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: <bin> <port>")
		os.Exit(1)
	}

	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		fmt.Fprintln(os.Stderr, "invalid port:", os.Args[1])
		os.Exit(1)
	}

	logging.Init()
	defer logging.Log.Sync()

	socksServer, err := application.NewSocksServer(port)
	if err != nil {
		logging.Error("failed to init socks server", "error", err)
		os.Exit(1)
	}

	if err := socksServer.Run(); err != nil {
		logging.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}
