package gui

import (
	"fmt"
	"lab4/internal/application"
	"lab4/internal/domain"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type FyneView struct {
	app    fyne.App
	window fyne.Window

	client *application.GameClient

	boardWidget *BoardWidget

	games     []application.DiscoveredGame
	gamesList *widget.List

	currentGame   *domain.Game
	players       []domain.PlayerInfo
	ratingList    *widget.List
	gameInfoLabel *widget.Label

	availableGamesList *widget.List
}

var _ application.View = (*FyneView)(nil)

func NewFyneView(a fyne.App, w fyne.Window, client *application.GameClient) *FyneView {

	return &FyneView{
		app:    a,
		window: w,
		client: client,
	}
}

func (v *FyneView) ShowStartMenu() {
	fyne.Do(func() {
		startContent := container.NewVBox(
			widget.NewButton("Создать игру", func() {
				v.client.CreateGame()
			}),
			widget.NewButton("Присоединиться к игре", func() {
				v.client.ShowGameList()
			}),
		)
		v.window.SetContent(startContent)
		v.window.Resize(fyne.NewSize(400, 300))
	})
}

func (v *FyneView) ShowError(msg string) {

}

func (v *FyneView) ShowGames() {

	v.games = v.client.GamesSnapshot()

	v.gamesList = widget.NewList(
		func() int { return len(v.games) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(v.games) {
				return
			}
			g := v.games[i]
			text := fmt.Sprintf(
				"%s [%s]  %d игроков  %dx%d  %s",
				g.GameName,
				g.Host,
				g.Players,
				g.Width,
				g.Height,
				map[bool]string{true: "Можно присоединиться", false: "Только просмотр"}[g.CanJoin],
			)
			o.(*widget.Label).SetText(text)
		},
	)

	v.gamesList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(v.games) {
			return
		}
		selected := v.games[id]
		v.showJoinDialog(selected)
		v.gamesList.Unselect(id)
	}

	backBtn := widget.NewButton("Назад", func() {
		v.client.BackToStart()
	})

	refreshBtn := widget.NewButton("Обновить", func() {
		v.client.ShowGameList()
	})

	gamesScroll := container.NewVScroll(v.gamesList)
	gamesScroll.SetMinSize(fyne.NewSize(260, 200))

	content := container.NewBorder(
		container.NewHBox(widget.NewLabel("Доступные игры"), refreshBtn),
		backBtn,
		nil, nil,
		gamesScroll,
	)

	v.window.SetContent(content)
}

func (v *FyneView) RefreshRating() {
	if v.ratingList == nil || v.currentGame == nil {
		return
	}

	fyne.Do(func() {
		v.players = v.currentGame.PlayersSnapshot()
		v.ratingList.Refresh()
	})
}

func (v *FyneView) RefreshGamesList() {
	if v.gamesList == nil {
		return
	}

	fyne.Do(func() {
		v.games = v.client.GamesSnapshot()
		v.gamesList.Refresh()
	})
}

func (v *FyneView) RefreshAvailableGames() {
	if v.availableGamesList == nil {
		return
	}

	fyne.Do(func() {
		v.games = v.client.GamesSnapshot()
		v.availableGamesList.Refresh()
	})
}

func (v *FyneView) showJoinDialog(g application.DiscoveredGame) {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Имя игрока")

	var selectedMode string = "normal"

	normalRadio := widget.NewRadioGroup([]string{"Игрок (normal)", "Наблюдатель (viewer)"}, func(selected string) {
		switch selected {
		case "Игрок (normal)":
			selectedMode = "normal"
		case "Наблюдатель (viewer)":
			selectedMode = "viewer"
		}
	})
	normalRadio.SetSelected("Игрок (normal)")

	form := widget.NewForm(
		widget.NewFormItem("К игре", widget.NewLabel(g.GameName)),
		widget.NewFormItem("Имя", nameEntry),
		widget.NewFormItem("Режим", normalRadio),
	)

	dialog.ShowCustomConfirm(
		"Присоединиться к игре",
		"OK",
		"Отмена",
		form,
		func(ok bool) {
			if !ok {
				return
			}
			name := nameEntry.Text
			if name == "" {
				dialog.ShowError(fmt.Errorf("введите имя"), v.window)
				return
			}
			if err := v.client.JoinGame(name, g, selectedMode); err != nil {
				dialog.ShowError(err, v.window)
				return
			}
		},
		v.window,
	)
}

func (v *FyneView) ShowConfigMenu() {
	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("Имя игрока")

	widthEntry := widget.NewEntry()
	widthEntry.SetPlaceHolder("Ширина (10-100)")

	heightEntry := widget.NewEntry()
	heightEntry.SetPlaceHolder("Высота (10-100)")

	foodEntry := widget.NewEntry()
	foodEntry.SetPlaceHolder("Еда (0-100)")

	delayEntry := widget.NewEntry()
	delayEntry.SetPlaceHolder("Задержка, мс (100-3000)")

	form := widget.NewForm(
		widget.NewFormItem("Имя игрока", nameEntry),
		widget.NewFormItem("Ширина", widthEntry),
		widget.NewFormItem("Высота", heightEntry),
		widget.NewFormItem("Еда", foodEntry),
		widget.NewFormItem("Задержка хода, мс", delayEntry),
	)

	form.OnSubmit = func() {
		name := nameEntry.Text

		w, err1 := strconv.Atoi(widthEntry.Text)
		h, err2 := strconv.Atoi(heightEntry.Text)
		food, err3 := strconv.Atoi(foodEntry.Text)
		delay, err4 := strconv.Atoi(delayEntry.Text)

		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			dialog.ShowError(fmt.Errorf("неверные числовые параметры"), v.window)
			return
		}

		cfg := domain.GameConfig{
			Width:        w,
			Height:       h,
			FoodStatic:   food,
			StateDelayMs: delay,
		}

		if err := v.client.CreateNewGame(name, cfg); err != nil {
			v.ShowError(err.Error())
			return
		}
	}

	form.OnCancel = func() {
		v.client.BackToStart()
	}

	v.window.SetContent(container.NewVBox(
		widget.NewLabel("Создание игры"),
		form,
	))

}

func (v *FyneView) ShowGameScreen(game *domain.Game) {
	fyne.Do(func() {
		v.currentGame = game

		// слева – поле
		v.boardWidget = NewBoardWidget(game.Board)
		boardContainer := container.NewStack(v.boardWidget)

		// ==== справа – панель ====

		// Рейтинг
		ratingTitle := widget.NewLabel("Рейтинг")

		v.players = v.currentGame.PlayersSnapshot()

		v.ratingList = widget.NewList(
			func() int { return len(v.players) },
			func() fyne.CanvasObject { return widget.NewLabel("") },
			func(i widget.ListItemID, o fyne.CanvasObject) {
				if i < 0 || i >= len(v.players) {
					return
				}
				p := v.players[i]
				o.(*widget.Label).SetText(
					fmt.Sprintf("%d. %s (%d)", i+1, p.Name, p.Score),
				)
			},
		)
		ratingBox := container.NewVBox(ratingTitle, v.ratingList)

		// Текущая игра — реальные значения
		currentGameTitle := widget.NewLabel("Текущая игра")

		cfg := game.Config() // сделай метод Config() в домене, который возвращает GameConfig

		v.gameInfoLabel = widget.NewLabel(
			fmt.Sprintf("Ведущий: ?\nРазмер: %dx%d\nЕда: %d+1x", cfg.Width, cfg.Height, cfg.FoodStatic),
		)
		currentGameBox := container.NewVBox(currentGameTitle, v.gameInfoLabel)

		// Кнопки "Выход" и "Новая игра"
		exitBtn := widget.NewButton("Выход", func() {
			v.client.BackToStart()
		})
		newGameBtn := widget.NewButton("Новая игра", func() {
			v.client.NewGameFromInGame()
		})
		buttonsRow := container.NewHBox(exitBtn, newGameBtn)

		// Список доступных игр (пока можно оставить заглушкой или убрать)
		gamesTitle := widget.NewLabel("Доступные игры")

		v.games = v.client.GamesSnapshot()

		v.availableGamesList = widget.NewList(
			func() int { return len(v.games) },
			func() fyne.CanvasObject { return widget.NewLabel("") },
			func(i widget.ListItemID, o fyne.CanvasObject) {
				if i < 0 || i >= len(v.games) {
					return
				}
				g := v.games[i]
				o.(*widget.Label).SetText(fmt.Sprintf(
					"%s [%s]  %d игроков  %dx%d  %s",
					g.GameName,
					g.Host,
					g.Players,
					g.Width,
					g.Height,
					map[bool]string{true: "Можно присоединиться", false: "Только просмотр"}[g.CanJoin],
				))
			},
		)

		v.availableGamesList.OnSelected = func(id widget.ListItemID) {
			if id < 0 || id >= len(v.games) {
				return
			}
			selected := v.games[id]
			v.showJoinDialog(selected)
			v.availableGamesList.Unselect(id)
		}

		gamesScroll := container.NewVScroll(v.availableGamesList)
		gamesScroll.SetMinSize(fyne.NewSize(260, 200)) // 200px по высоте — уже видно несколько элементов

		gamesBox := container.NewVBox(gamesTitle, gamesScroll)

		// правая колонка
		right := container.NewVBox(
			ratingBox,
			currentGameBox,
			buttonsRow,
			gamesBox,
		)

		content := container.NewBorder(nil, nil, nil, right, boardContainer)
		v.window.SetContent(content)

		v.window.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
			switch ev.Name {
			case fyne.KeyW, fyne.KeyUp:
				v.client.ChangeDirection(domain.DirUp)
			case fyne.KeyS, fyne.KeyDown:
				v.client.ChangeDirection(domain.DirDown)
			case fyne.KeyA, fyne.KeyLeft:
				v.client.ChangeDirection(domain.DirLeft)
			case fyne.KeyD, fyne.KeyRight:
				v.client.ChangeDirection(domain.DirRight)
			}
		})
	})
}
func (v *FyneView) RefreshBoard() {
	if v.boardWidget == nil {
		return
	}

	fyne.Do(func() {
		v.boardWidget.Refresh()
	})

}
