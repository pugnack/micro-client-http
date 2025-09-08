package http

import "fmt"

type Error struct {
	err any
}

func (err *Error) Error() string {
	return fmt.Sprintf("%+v", err.err)
}
