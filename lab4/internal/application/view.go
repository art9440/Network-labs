package application

import "lab4/internal/domain"

type View interface {
	ShowStartMenu()
	ShowError(msg string)
	ShowConfigMenu()
	ShowGameScreen(game *domain.Game)
	RefreshBoard()
	ShowGames()
	RefreshGamesList()
	RefreshRating()
	RefreshAvailableGames()
}
