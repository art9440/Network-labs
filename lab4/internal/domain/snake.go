package domain

type Snake struct {
	PlayerID  int32
	Body      []Coord    // [0] – голова, дальше тело до хвоста
	Direction Direction  // куда сейчас смотрит голова
	State     SnakeState // ALIVE/ZOMBIE
}
