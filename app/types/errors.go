package types

import "fmt"

type RetryableError struct {
	Msg string
}

func (e *RetryableError) Error() string { return e.Msg }

type InternalError struct {
	Msg string
}

func (e *InternalError) Error() string { return e.Msg }

func NewInternalError(msg string) *InternalError {
	return &InternalError{Msg: msg}
}

type BadRequestError struct {
	Msg string
}

func (e *BadRequestError) Error() string { return e.Msg }

func NewBadRequestError(msg string) *BadRequestError {
	return &BadRequestError{Msg: msg}
}

type NotFoundError struct {
	Msg string
}

func NewNotFoundError(msg string) *NotFoundError {
	return &NotFoundError{Msg: msg}
}

func (e *NotFoundError) Error() string { return e.Msg }

type ErrCapacityReservationNotSupported struct {
	Driver string
}

func (e *ErrCapacityReservationNotSupported) Error() string {
	return fmt.Sprintf("capacity reservation not supported for %s driver", e.Driver)
}
