package domain

import "image/color"

var SnakeColors = []color.RGBA{
	{255, 215, 0, 255},  // жёлтый
	{0, 200, 0, 255},    // зелёный
	{0, 120, 255, 255},  // синий
	{220, 0, 0, 255},    // красный
	{160, 32, 240, 255}, // фиолетовый
	{255, 140, 0, 255},  // оранжевый
}

func ColorForPlayer(id int32) color.RGBA {
	return SnakeColors[int(id)%len(SnakeColors)]
}
