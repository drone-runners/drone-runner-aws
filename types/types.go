package types

import (
	"database/sql/driver"
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
	StateCreated = InstanceState("created")
	StateInUse   = InstanceState("inuse")
	StateStopped = InstanceState("stopped")
)

type Instance struct {
	ID       string        `db:"instance_id" json:"id"`
	Name     string        `db:"instance_name" json:"name"`
	Address  string        `db:"instance_address" json:"address"`
	Provider ProviderType  `db:"instance_provider" json:"provider"`
	State    InstanceState `db:"instance_state" json:"state"`
	Pool     string        `db:"instance_pool" json:"pool"`
	Image    string        `db:"instance_image" json:"image"`
	Region   string        `db:"instance_region" json:"region"`
	Zone     string        `db:"instance_zone" json:"zone"`
	Size     string        `db:"instance_size" json:"size"`
	Platform string        `db:"instance_platform" json:"platform"`
	CAKey    []byte        `db:"instance_ca_key" json:"ca_key"`
	CACert   []byte        `db:"instance_ca_cert" json:"ca_cert"`
	TLSKey   []byte        `db:"instance_tls_key" json:"tls_key"`
	TLSCert  []byte        `db:"instance_tls_cert" json:"tls_cert"`
	Created  string        `db:"instance_created" json:"created"`
	Started  int64         `db:"instance_started" json:"started"`
}

type InstanceCreateOpts struct {
	Name           string
	CAKey          []byte
	CACert         []byte
	TLSKey         []byte
	TLSCert        []byte
	LiteEnginePath string
}
