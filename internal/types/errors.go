package types

type RetryableError struct {
	Msg string
}

func (e *RetryableError) Error() string { return e.Msg }

type internalError struct {
	Msg string
}

func (e *internalError) Error() string { return e.Msg }

func InternalError(msg string) *internalError {
	return &internalError{Msg: msg}
}

type badRequestError struct {
	Msg string
}

func (e *badRequestError) Error() string { return e.Msg }

func BadRequestError(msg string) *badRequestError {
	return &badRequestError{Msg: msg}
}

type notFoundError struct {
	Msg string
}

func NotFoundError(msg string) *notFoundError {
	return &notFoundError{Msg: msg}
}
