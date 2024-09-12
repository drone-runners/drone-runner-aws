package storage

type CleanupType string

const (
	Detach CleanupType = "DETACH"
	Delete CleanupType = "DELETE"
)
