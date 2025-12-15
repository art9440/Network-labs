package main

import (
	"lab4/internal/application"
	"lab4/internal/infrastructure/gui"
	"lab4/internal/infrastructure/network"
	"log"
	"os"

	fyneapp "fyne.io/fyne/v2/app"
)

func main() {

	logger := log.New(os.Stdout, "[snake] ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)

	a := fyneapp.New()
	w := a.NewWindow("TapeWorm")
	client := application.NewGameClient(nil, logger)
	tr, err := network.NewTransport(client, "239.192.0.4", 9192, logger)
	if err != nil {
		log.Fatal(err)
	}

	client.SetTransport(tr)
	view := gui.NewFyneView(a, w, client)
	client.SetView(view)
	client.Run()
	w.ShowAndRun()

	logger.Println("MAIN END")

}
