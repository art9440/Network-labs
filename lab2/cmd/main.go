package main

import (
	"fmt"
	"os"

	"github.com/art9440/Network-labs/lab2/internal/domain/client"

	"github.com/art9440/Network-labs/lab2/internal/domain/server"
)

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {

	switch args[1] {
	case "client":
		if len(args) == 4 {
			if err := client.StartClient(args[2], args[3]); err != nil {
				fmt.Println("Error while using client: ", err)
				return 1
			}
			return 0
		} else {
			fmt.Println("Incorrect amount of arguments. Expected: 3")
			return 1
		}
	case "server":
		if len(args) == 3 {
			if err := server.StartServer(args[2]); err != nil {
				fmt.Println("Error while starting server: ", err)
				return 1
			}
		} else {
			fmt.Println("Incorrect amount of arguments. Expected: 2")
			return 1
		}
	}

	fmt.Println("Incorrect input")
	return 1
}
