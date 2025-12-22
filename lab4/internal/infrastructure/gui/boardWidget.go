package gui

import (
	"image/color"
	"lab4/internal/domain"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

type BoardWidget struct {
	widget.BaseWidget
	Game     *domain.Game
	Board    *domain.Board
	CellSize float32
}

func NewBoardWidget(g *domain.Game) *BoardWidget {
	b := &BoardWidget{
		Board:    g.Board,
		Game:     g,
		CellSize: 20,
	}
	b.ExtendBaseWidget(b)
	return b
}

func (b *BoardWidget) CreateRenderer() fyne.WidgetRenderer {
	w, h := b.Board.Width, b.Board.Height
	rects := make([]*canvas.Rectangle, w*h)
	objects := make([]fyne.CanvasObject, w*h)
	for i := range rects {
		r := canvas.NewRectangle(color.Black)
		rects[i] = r
		objects[i] = r
	}
	return &boardRenderer{
		boardWidget: b,
		rects:       rects,
		objects:     objects,
	}
}

type boardRenderer struct {
	boardWidget *BoardWidget
	rects       []*canvas.Rectangle
	objects     []fyne.CanvasObject
}

func (r *boardRenderer) Layout(size fyne.Size) {
	b := r.boardWidget.Board
	cs := r.boardWidget.CellSize
	for y := 0; y < b.Height; y++ {
		for x := 0; x < b.Width; x++ {
			idx := b.Idx(x, y)
			rect := r.rects[idx]
			rect.Resize(fyne.NewSize(cs, cs))
			rect.Move(fyne.NewPos(float32(x)*cs, float32(y)*cs))
		}
	}
}

func (r *boardRenderer) MinSize() fyne.Size {
	b := r.boardWidget.Board
	cs := r.boardWidget.CellSize
	return fyne.NewSize(float32(b.Width)*cs, float32(b.Height)*cs)
}

func (r *boardRenderer) Refresh() {
	b := r.boardWidget.Board

	for i := range r.rects {
		switch b.Cells[i] {
		case domain.CellEmpty:
			r.rects[i].FillColor = color.Black
		case domain.CellFood:
			r.rects[i].FillColor = color.RGBA{R: 180, G: 0, B: 200, A: 255} // еда
		case domain.CellSnake:
			if pid, zombie, ok := snakeOwnerAtIndex(r.boardWidget.Game, i); ok {
				if zombie {
					r.rects[i].FillColor = color.RGBA{R: 120, G: 120, B: 120, A: 255} // зомби серые
				} else {
					r.rects[i].FillColor = domain.ColorForPlayer(pid)
				}
			} else {
				// fallback, если вдруг не нашли
				r.rects[i].FillColor = color.RGBA{R: 255, G: 255, B: 0, A: 255}
			}
		}
		canvas.Refresh(r.rects[i])
	}
}

func snakeOwnerAtIndex(g *domain.Game, idx int) (playerID int32, isZombie bool, ok bool) {
	if g == nil || g.Board == nil {
		return 0, false, false
	}
	w := g.Board.Width
	x := idx % w
	y := idx / w

	for pid, s := range g.Snakes {
		if s == nil {
			continue
		}
		for _, c := range s.Body {
			if c.X == x && c.Y == y {
				return pid, s.State == domain.SnakeZombie, true
			}
		}
	}
	return 0, false, false
}

func (r *boardRenderer) BackgroundColor() color.Color { return color.Black }
func (r *boardRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *boardRenderer) Destroy()                     {}
