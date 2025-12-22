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
	// Логгер приложения
	logger := log.New(os.Stdout, "[snake] ", log.LstdFlags|log.Lmicroseconds|log.Lshortfile)

	// GUI-приложение и главное окно
	a := fyneapp.New()
	w := a.NewWindow("TapeWorm")

	// Центральный клиент игры (логика + сеть)
	client := application.NewGameClient(nil, logger)

	// UDP-транспорт + multicast для discover/announcement
	tr, err := network.NewTransport(client, "239.192.0.4", 9192, logger)
	if err != nil {
		log.Fatal(err)
	}

	// Связываем клиент с сетью
	client.SetTransport(tr)

	// Создаём и подключаем GUI
	view := gui.NewFyneView(a, w, client)
	client.SetView(view)

	// Старт логики клиента
	client.Run()

	// Запуск GUI-цикла
	w.ShowAndRun()

	logger.Println("MAIN END")

}
