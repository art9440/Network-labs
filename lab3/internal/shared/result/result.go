package result

type Result[T any] struct {
	Val T
	Err error
}
