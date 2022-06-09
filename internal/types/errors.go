package types

type RetryableError struct {
	Msg string
}

func (e *RetryableError) Error() string { return e.Msg }
