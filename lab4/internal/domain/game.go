package domain

import (
	"fmt"
	snakespb "lab4/internal/infrastructure/proto"
	"math/rand"
	"sort"
	"time"
)

type Coord struct{ X, Y int }

// Game — доменная модель игры
type Game struct {
	cfg          GameConfig        // настройки: размер, еда, задержка
	Tick         int32             // период тика в мс
	Players      map[int32]*Player // все игроки (включая VIEWER)
	nextPlayerID int32             // генератор id для новых игроков
	Snakes       map[int32]*Snake  // змейки по playerID

	Board *Board // поле клеток

	Rand *rand.Rand

	JoinOrder []int32 // порядок входа: нужен для выбора deputy
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

// AddFirstPlayer — добавление первого игрока (он MASTER)
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

// ChooseDeputy — выбрать deputy среди NORMAL игроков (по JoinOrder)
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

// стартовая еда = FoodStatic + число змей
func (g *Game) InitFood() {
	target := g.cfg.FoodStatic + len(g.Snakes)

	for i := 0; i < target; i++ {
		_ = g.SpawnFood()
	}
}

// SpawnFood — ставим еду в случайную пустую клетку
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

// nextHead — куда попадёт голова при движении
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

// oppositeDir — направление “назад” (чтобы запретить разворот)
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

// MoveSnakes — один шаг симуляции змей (двухфазно):
//  1. собираем intent всех змей (куда пойдут)
//  2. определяем смерти (конфликт голов / удар в тело)
//  3. двигаем живых (хвосты/рост)
//  4. мёртвых чистим и с p=0.5 превращаем клетки в еду
func (g *Game) MoveSnakes() bool {
	if len(g.Snakes) == 0 {
		return false
	}

	type intent struct {
		id       int32
		snake    *Snake
		newHead  Coord
		willGrow bool
		tail     Coord
	}

	// снимок поля до хода
	oldCells := make([]CellType, len(g.Board.Cells))
	copy(oldCells, g.Board.Cells)

	// 1) собираем намерения ходов
	intents := make([]intent, 0, len(g.Snakes))
	targetMap := make(map[int]int)     // idx -> count
	targetIDs := make(map[int][]int32) // idx -> ids

	tailsToFree := make(map[int]int32) // idx хвоста -> id змеи (если она НЕ растёт)

	for id, s := range g.Snakes {
		if s == nil || len(s.Body) == 0 {
			continue
		}

		head := s.Body[0]
		newHead := g.nextHead(head, s.Direction)
		newIdx := g.Board.Idx(newHead.X, newHead.Y)

		willGrow := (oldCells[newIdx] == CellFood)
		tail := s.Body[len(s.Body)-1]

		intents = append(intents, intent{
			id:       id,
			snake:    s,
			newHead:  newHead,
			willGrow: willGrow,
			tail:     tail,
		})

		targetMap[newIdx]++
		targetIDs[newIdx] = append(targetIDs[newIdx], id)

		if !willGrow {
			tailsToFree[g.Board.Idx(tail.X, tail.Y)] = id
		}
	}

	// 2) определяем смерти
	dead := make(map[int32]bool)

	// 2.1) конфликт голов: несколько змей в одну клетку => все умирают
	for idx, cnt := range targetMap {
		if cnt > 1 {
			for _, id := range targetIDs[idx] {
				dead[id] = true
			}
		}
	}

	// 2.2) столкновения в тело (с учётом освобождающегося хвоста)
	for _, in := range intents {
		if dead[in.id] {
			continue
		}
		newIdx := g.Board.Idx(in.newHead.X, in.newHead.Y)
		cell := oldCells[newIdx]

		if cell == CellSnake {
			// разрешаем "въезд в хвост", который уедет в этот же тик
			if owner, ok := tailsToFree[newIdx]; ok {
				// если хвост уезжает и его владелец сам не умер — разрешаем
				if !dead[owner] {
					continue
				}
			}
			// иначе смерть
			dead[in.id] = true

			// начисление очка убийце (если нашли)
			killerID, err := g.findKiller(in.newHead)
			if err == nil {
				if p := g.Players[killerID]; p != nil {
					p.Score++
				}
			}
		}
	}

	// 3) применяем движения живых: сначала освобождаем хвосты у тех, кто не растёт и не умер
	for _, in := range intents {
		if dead[in.id] {
			continue
		}
		if !in.willGrow {
			tidx := g.Board.Idx(in.tail.X, in.tail.Y)
			g.Board.Cells[tidx] = CellEmpty
		}
	}

	// затем двигаем живых
	for _, in := range intents {
		if dead[in.id] {
			continue
		}

		newIdx := g.Board.Idx(in.newHead.X, in.newHead.Y)

		if in.willGrow {
			// съели еду: добавляем голову, хвост не трогаем
			in.snake.Body = append([]Coord{in.newHead}, in.snake.Body...)
			g.Board.Cells[newIdx] = CellSnake

			if p := g.Players[in.id]; p != nil {
				p.Score++
			}
			_ = g.SpawnFood()
		} else {
			// обычный ход: добавляем голову, хвост уже очищен выше
			in.snake.Body = append([]Coord{in.newHead}, in.snake.Body[:len(in.snake.Body)-1]...)
			g.Board.Cells[newIdx] = CellSnake
		}
	}

	// 4) обрабатываем мёртвых: очищаем тело, затем 0.5 клеток -> еда
	for _, in := range intents {
		if !dead[in.id] {
			continue
		}
		// очищаем тело
		for _, c := range in.snake.Body {
			g.Board.Cells[g.Board.Idx(c.X, c.Y)] = CellEmpty
		}
		// превращаем в еду с p=0.5
		g.snakeToFoodHalf(in.snake.Body)

		// удаляем змею
		delete(g.Snakes, in.id)
	}

	return len(g.Snakes) > 0
}

var errNoKiller = fmt.Errorf("cant find killer")

// findKiller — кто владел клеткой столкновения (для начисления очка)
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

// OneTick — один тик игры
func (g *Game) OneTick() bool {
	alive := g.MoveSnakes()

	return alive
}

var errNoPlace = fmt.Errorf("no place to spawn snake")

// findSpawnPosition — находим безопасную позицию для спавна змеи
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

// CheckCanJoin — можно ли ещё кого-то заспавнить (есть ли место)
func (g *Game) CheckCanJoin() bool {
	_, _, _, ok := g.findSpawnPosition()
	return ok
}

// PlayersSnapshot — список игроков для рейтинга
func (g *Game) PlayersSnapshot() []PlayerInfo {
	res := make([]PlayerInfo, 0, len(g.Players))
	for _, p := range g.Players {
		if p == nil {
			continue
		}
		// исключаем VIEWER из рейтинга/списков
		if p.Role == snakespb.NodeRole_VIEWER {
			continue
		}
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

// NextPlayerIDFix — поднимаем nextPlayerID до нужного значения
func (g *Game) NextPlayerIDFix(v int32) {
	if v > g.nextPlayerID {
		g.nextPlayerID = v
	}
}

// snakeToFoodHalf — при смерти змеи: каждую клетку тела с p=0.5 превращаем в еду
func (g *Game) snakeToFoodHalf(body []Coord) {
	for _, c := range body {
		if g.Rand.Float64() < 0.5 {
			idx := g.Board.Idx(c.X, c.Y)
			// не затираем змею/еду, ставим еду только в пустую клетку
			if g.Board.Cells[idx] == CellEmpty {
				g.Board.Cells[idx] = CellFood
			}
		}
	}
}

// MasterName — имя текущего ведущего (по роли MASTER), для UI
func (g *Game) MasterName() string {
	if g == nil {
		return ""
	}
	for _, p := range g.Players {
		if p != nil && p.Role == snakespb.NodeRole_MASTER {
			return p.Name
		}
	}
	return ""
}
