package main

import (
	"/home/art9440/VScodeProjects/GO/Network-lab2/internal/domain/client"
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args))
}

func run(args []string) int {

	if args[1] == "client" {
		if len(args) == 4 {
			if err := client.StartClient(args[3], args[4]); err != nil {
				fmt.Println("Error while using client: ", err)
				return 1
			}
			return 0
		} else {
			fmt.Println("Incorrect amount of arguments. Expected: 3")
			return 1
		}
	} else if args[1] == "server" {
		if len(args) == 3 {
			if err := server.StartServer(args[3]); err != nil {
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
