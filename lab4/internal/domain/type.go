package domain

type Direction int

const (
	DirUp Direction = iota
	DirDown
	DirLeft
	DirRight
)

type SnakeState int

const (
	SnakeAlive SnakeState = iota
	SnakeZombie
)
