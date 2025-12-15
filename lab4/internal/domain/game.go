package domain

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"math/rand"
	"sort"
	"time"
)

type Coord struct{ X, Y int }

type Game struct {
	cfg          GameConfig
	Tick         int32
	Players      map[int32]*Player
	nextPlayerID int32
	Snakes       map[int32]*Snake

	Board *Board

	Rand *rand.Rand

	JoinOrder []int32
}

func NewGame(cfg GameConfig) *Game {
	src := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &Game{
		cfg:          cfg,
		Tick:         int32(cfg.StateDelayMs),
		Players:      make(map[int32]*Player),
		nextPlayerID: 1, // удобнее с 1
		Snakes:       make(map[int32]*Snake),
		Board: &Board{
			Width:  cfg.Width,
			Height: cfg.Height,
			Cells:  make([]CellType, cfg.Width*cfg.Height),
		},
		Rand: src,
	}
}

func (g *Game) Config() GameConfig {
	return g.cfg
}

func (g *Game) AddFirstPlayer(name string) int32 {
	id := g.nextPlayerID
	g.nextPlayerID++

	p := &Player{ID: id, Name: name, Score: 0, Role: snakespb.NodeRole_MASTER}
	g.Players[id] = p
	g.JoinOrder = append(g.JoinOrder, id)
	return id
}

func (g *Game) AddPlayerWithRole(name string, role snakespb.NodeRole) int32 {
	id := g.nextPlayerID
	g.nextPlayerID++

	g.Players[id] = &Player{ID: id, Name: name, Role: role}
	g.JoinOrder = append(g.JoinOrder, id)
	return id
}

func (g *Game) ChooseDeputy(masterID int32) (int32, bool) {
	for _, id := range g.JoinOrder {
		if id == masterID {
			continue
		}
		p := g.Players[id]
		if p == nil {
			continue
		}
		if p.Role == snakespb.NodeRole_NORMAL {
			return id, true
		}
	}
	return 0, false
}

func (g *Game) isCellFree(x, y int) bool {
	x, y = g.Board.Wrap(x, y)
	idx := g.Board.Idx(x, y)
	return g.Board.Cells[idx] == CellEmpty
}

// нет ли змеи в квадрате 5x5 вокруг (с учётом границ)
func (g *Game) isAreaFree(x, y int) bool {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			xx, yy := g.Board.Wrap(x+dx, y+dy)
			idx := g.Board.Idx(xx, yy)
			if g.Board.Cells[idx] == CellSnake {
				return false
			}
		}
	}
	return true
}

func (g *Game) InitFood() {
	target := g.cfg.FoodStatic + len(g.Snakes)

	for i := 0; i < target; i++ {
		_ = g.SpawnFood()
	}
}

func (g *Game) SpawnFood() error {
	for tries := 0; tries < 1000; tries++ {
		x := g.Rand.Intn(g.Board.Width)
		y := g.Rand.Intn(g.Board.Height)

		if g.isCellFree(x, y) {
			idx := g.Board.Idx(x, y)
			g.Board.Cells[idx] = CellFood
			return nil
		}
	}
	return fmt.Errorf("no place to spawn food")
}

func (g *Game) nextHead(head Coord, dir Direction) Coord {
	x, y := head.X, head.Y
	switch dir {
	case DirUp:
		y--
	case DirDown:
		y++
	case DirLeft:
		x--
	case DirRight:
		x++
	}
	x, y = g.Board.Wrap(x, y)
	return Coord{X: x, Y: y}
}

func oppositeDir(d Direction) Direction {
	switch d {
	case DirUp:
		return DirDown
	case DirDown:
		return DirUp
	case DirLeft:
		return DirRight
	case DirRight:
		return DirLeft
	}
	return d
}

func (g *Game) SetDirection(newDir Direction, id int32) {
	if g.Snakes[id] == nil {
		return
	}
	if newDir == oppositeDir(g.Snakes[id].Direction) {
		// нельзя разворачиваться назад
		return
	}
	g.Snakes[id].Direction = newDir
}

func (g *Game) MoveSnakes() bool {
	if len(g.Snakes) == 0 {
		return false
	}

	// сюда запишем id змеек, которые умерли
	var dead []int32

	for id, s := range g.Snakes {
		if s == nil || len(s.Body) == 0 {
			dead = append(dead, id)
			continue
		}

		head := s.Body[0]
		newHead := g.nextHead(head, s.Direction)
		newIdx := g.Board.Idx(newHead.X, newHead.Y)
		cell := g.Board.Cells[newIdx]

		switch cell {
		case CellEmpty:
			// обычный ход: очищаем хвост, добавляем новую голову
			tail := s.Body[len(s.Body)-1]
			g.Board.Cells[g.Board.Idx(tail.X, tail.Y)] = CellEmpty

			s.Body = append([]Coord{newHead}, s.Body[:len(s.Body)-1]...)
			g.Board.Cells[newIdx] = CellSnake

		case CellFood:
			// съели еду: просто добавляем голову, хвост не убираем
			s.Body = append([]Coord{newHead}, s.Body...)
			g.Board.Cells[newIdx] = CellSnake

			if p, ok := g.Players[s.PlayerID]; ok && p != nil {
				p.Score++
			} else {
				// тут можно залогировать, что не нашли игрока по PlayerID
				// fmt.Println("no player for snake", s.PlayerID)
			}

			_ = g.SpawnFood() // можно обработать ошибку

		case CellSnake:
			// столкновение: змея умирает
			killerID, err := g.findKiller(newHead)
			if err != nil {
				//TODO: добавить логирование о том, что очко не присуждается никому
			} else {
				g.Players[killerID].Score++
			}
			for _, c := range s.Body {
				g.Board.Cells[g.Board.Idx(c.X, c.Y)] = CellEmpty
			}
			dead = append(dead, id)

		default:
			// на всякий случай считаем это смертью
			for _, c := range s.Body {
				g.Board.Cells[g.Board.Idx(c.X, c.Y)] = CellEmpty
			}
			dead = append(dead, id)
		}
	}

	for _, id := range dead {
		delete(g.Snakes, id)
	}

	return len(g.Snakes) > 0
}

var errNoKiller = fmt.Errorf("cant find killer")

func (g *Game) findKiller(collisionCoord Coord) (int32, error) {
	for id, s := range g.Snakes {
		if s == nil || len(s.Body) == 0 {
			continue
		}

		for _, b := range s.Body {
			if b.X == collisionCoord.X && b.Y == collisionCoord.Y {
				return id, nil
			}
		}
	}
	return 0, errNoKiller
}

func (g *Game) OneTick() bool {
	alive := g.MoveSnakes()

	return alive
}

var errNoPlace = fmt.Errorf("no place to spawn snake")

func (g *Game) findSpawnPosition() (head Coord, tail Coord, dir Direction, ok bool) {
	for tries := 0; tries < 1000; tries++ {
		x := g.Rand.Intn(g.Board.Width)
		y := g.Rand.Intn(g.Board.Height)

		if !g.isCellFree(x, y) || !g.isAreaFree(x, y) {
			continue
		}

		dir := Direction(g.Rand.Intn(4))

		tailX, tailY := x, y
		switch dir {
		case DirUp:
			tailY++
		case DirDown:
			tailY--
		case DirLeft:
			tailX++
		case DirRight:
			tailX--
		}
		tailX, tailY = g.Board.Wrap(tailX, tailY)

		if !g.isCellFree(tailX, tailY) {
			continue
		}

		return Coord{X: x, Y: y}, Coord{X: tailX, Y: tailY}, dir, true
	}
	return Coord{}, Coord{}, 0, false
}

func (g *Game) SpawnSnake(id int32) error {
	head, tail, dir, ok := g.findSpawnPosition()
	if !ok {
		return errNoPlace
	}

	s := &Snake{
		PlayerID: id,
		Body: []Coord{
			head,
			tail,
		},
		Direction: dir,
	}

	g.Snakes[id] = s

	for _, c := range s.Body {
		idx := g.Board.Idx(c.X, c.Y)
		g.Board.Cells[idx] = CellSnake
	}

	return nil
}

func (g *Game) CheckCanJoin() bool {
	_, _, _, ok := g.findSpawnPosition()
	return ok
}

func (g *Game) PlayersSnapshot() []PlayerInfo {
	res := make([]PlayerInfo, 0, len(g.Players))
	for _, p := range g.Players {
		res = append(res, PlayerInfo{
			ID:    p.ID,
			Name:  p.Name,
			Score: p.Score,
		})
	}

	sort.Slice(res, func(i, j int) bool {
		if res[i].Score == res[j].Score {
			return res[i].Name < res[j].Name
		}
		return res[i].Score > res[j].Score
	})

	return res
}
