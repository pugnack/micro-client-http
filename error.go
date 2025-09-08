package http

import "fmt"

type Error struct {
	err any
}

func (e *Error) Error() string {
	return fmt.Sprintf("%+v", e.err)
}
