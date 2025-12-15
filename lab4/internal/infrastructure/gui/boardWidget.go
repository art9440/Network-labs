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
	Board    *domain.Board
	CellSize float32
}

func NewBoardWidget(board *domain.Board) *BoardWidget {
	b := &BoardWidget{
		Board:    board,
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
			r.rects[i].FillColor = color.RGBA{R: 255, G: 255, B: 0, A: 255} // змея
		}
		canvas.Refresh(r.rects[i])
	}
}

func (r *boardRenderer) BackgroundColor() color.Color { return color.Black }
func (r *boardRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *boardRenderer) Destroy()                     {}
