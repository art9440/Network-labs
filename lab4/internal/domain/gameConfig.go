package domain

type GameConfig struct {
	Width        int
	Height       int
	FoodStatic   int
	StateDelayMs int
}

func NewGameConfig(w int, h int, f int, delay int) *GameConfig {
	return &GameConfig{Width: w, Height: h, FoodStatic: f, StateDelayMs: delay}
}
