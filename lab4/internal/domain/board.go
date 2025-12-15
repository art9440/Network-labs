package domain

type CellType int

const (
	CellEmpty CellType = iota
	CellFood
	CellSnake
)

type Board struct {
	Width, Height int
	Cells         []CellType
}

func (b *Board) Idx(x, y int) int {
	return y*b.Width + x
}

func (b *Board) Wrap(x, y int) (int, int) {
	if x < 0 {
		x = b.Width - 1
	} else if x >= b.Width {
		x = 0
	}
	if y < 0 {
		y = b.Height - 1
	} else if y >= b.Height {
		y = 0
	}
	return x, y
}
