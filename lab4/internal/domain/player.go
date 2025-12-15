package domain

import snakespb "lab4/internal/infrastructure/proto"

type Player struct {
	ID    int32
	Name  string
	Score int32
	Role  snakespb.NodeRole
}

func NewPlayer(id int32, name string, score int32, role snakespb.NodeRole) *Player {
	return &Player{ID: id, Name: name, Score: score, Role: role}
}

type PlayerInfo struct {
	ID    int32
	Name  string
	Score int32
}
