package core

import (
	"context"
	"database/sql/driver"
	"errors"
	"time"
)

var (
	ErrInstanceNotFound = errors.New("instance not found")
)

type InstanceState string
type ProviderType string

// Value converts the value to a sql string.
func (s InstanceState) Value() (driver.Value, error) {
	return string(s), nil
}

func (s ProviderType) Value() (driver.Value, error) {
	return string(s), nil
}

const (
	ProviderAmazon = ProviderType("amazon")
	ProviderGoogle = ProviderType("google")
)

// InstanceState type enumeration.
const (
	StatePending  = InstanceState("pending")
	StateCreating = InstanceState("creating")
	StateCreated  = InstanceState("created")
	StateStaging  = InstanceState("staging") // starting
	StateRunning  = InstanceState("running")
	StateShutdown = InstanceState("shutdown")
	StateStopping = InstanceState("stopping")
	StateStopped  = InstanceState("stopped")
	StateError    = InstanceState("error")
)

type InstanceCreateOpts struct {
	Name           string
	CAKey          []byte
	CACert         []byte
	TLSKey         []byte
	TLSCert        []byte
	LiteEnginePath string
}

type Instance struct {
	ID        string        `json:"id"`
	IP        string        `json:"ip"`
	Provider  ProviderType  `json:"provider"`
	State     InstanceState `json:"state"`
	Pool      string        `json:"pool_name"`
	Name      string        `json:"name"`
	Image     string        `json:"image"`
	Region    string        `json:"region"`
	Zone      string        `json:"zone"`
	Size      string        `json:"size"`
	Platform  string        `json:"platform"`
	Capacity  int           `json:"capacity"`
	CAKey     []byte        `json:"ca_key"`
	CACert    []byte        `json:"ca_cert"`
	TLSKey    []byte        `json:"tls_key"`
	TLSCert   []byte        `json:"tls_cert"`
	Created   string        `json:"created"`
	Updated   int64         `json:"updated"`
	Started   int64         `json:"started"`
	Stopped   int64         `json:"stopped"`
	StartedAt time.Time     `json:"started_at"`
}

type InstanceStore interface {
	Find(context.Context, string) (*Instance, error)
	List(context.Context) ([]*Instance, error)
	Create(context.Context, *Instance) error
	Delete(context.Context, string) error
	Update(context.Context, *Instance) error
}
