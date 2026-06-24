package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"github.com/harness/lite-engine/engine/spec"
)

type InstanceState string
type DriverType string
type CapacityReservationState string

type ContextKey string

const (
	Hosted = ContextKey("hosted")
)

// Value converts the value to a sql string.
func (s InstanceState) Value() (driver.Value, error) {
	return string(s), nil
}

func (s DriverType) Value() (driver.Value, error) {
	return string(s), nil
}

func (s CapacityReservationState) Value() (driver.Value, error) {
	return string(s), nil
}

const (
	Amazon       = DriverType("amazon")
	Anka         = DriverType("anka")
	AnkaBuild    = DriverType("ankabuild")
	Azure        = DriverType("azure")
	DigitalOcean = DriverType("digitalocean")
	Google       = DriverType("google")
	VMFusion     = DriverType("vmfusion")
	Noop         = DriverType("noop")
	Nomad        = DriverType("nomad")
)

// InstanceState type enumeration.
const (
	StateCreated      = InstanceState("created")
	StateInUse        = InstanceState("inuse")
	StateHibernating  = InstanceState("hibernating")
	StateTerminating  = InstanceState("terminating")
	StateProvisioning = InstanceState("provisioning") // VM created but not yet ready for use
)

// InstanceSource represents who created an instance.
type InstanceSource string

const (
	InstanceSourceUnknown   InstanceSource = "unknown"
	InstanceSourcePool      InstanceSource = "pool"
	InstanceSourcePredictor InstanceSource = "predictor"
	InstanceSourceOnDemand  InstanceSource = "ondemand"
)

// CapacityReservationState type enumeration.
const (
	CapacityReservationStateCreated     = CapacityReservationState("created")
	CapacityReservationStateInUse       = CapacityReservationState("inuse")
	CapacityReservationStateTerminating = CapacityReservationState("terminating")
)

// CapacityReservationQueryParams holds query parameters for capacity reservation operations.
type CapacityReservationQueryParams struct {
	StageID         string // If set, query by specific stage ID
	PoolName        string
	CreatedAtBefore int64 // Unix timestamp - only return reservations created before this time
	Limit           int
}

type Instance struct {
	ID                         string        `db:"instance_id" json:"id"`
	NodeID                     string        `db:"instance_node_id" json:"node_id"`
	Name                       string        `db:"instance_name" json:"name"`
	Address                    string        `db:"instance_address" json:"address"`
	Provider                   DriverType    `db:"instance_provider" json:"provider"` // this is driver, though its the old legacy name of provider
	State                      InstanceState `db:"instance_state" json:"state"`
	Pool                       string        `db:"instance_pool" json:"pool"`
	Image                      string        `db:"instance_image" json:"image"`
	Region                     string        `db:"instance_region" json:"region"`
	Zone                       string        `db:"instance_zone" json:"zone"`
	Size                       string        `db:"instance_size" json:"size"`
	OwnerID                    string        `db:"instance_owner_id" json:"owner_id"`
	Platform                   `json:"platform"`
	CAKey                      []byte         `db:"instance_ca_key" json:"ca_key"`
	CACert                     []byte         `db:"instance_ca_cert" json:"ca_cert"`
	TLSKey                     []byte         `db:"instance_tls_key" json:"tls_key"`
	TLSCert                    []byte         `db:"instance_tls_cert" json:"tls_cert"`
	Stage                      string         `db:"instance_stage" json:"stage"`
	Updated                    int64          `db:"instance_updated" json:"updated"`
	Started                    int64          `db:"instance_started" json:"started"`
	IsHibernated               bool           `db:"is_hibernated" json:"is_hibernated"`
	Port                       int64          `db:"instance_port" json:"port"`
	RunnerName                 string         `db:"runner_name" json:"runner_name"`
	GitspacePortMappings       map[int]int    `json:"gitspaces_port_mappings"`
	StorageIdentifier          string         `db:"instance_storage_identifier" json:"storage_identifier"`
	Labels                     []byte         `db:"instance_labels" json:"instance_labels"`
	EnableNestedVirtualization bool           `db:"enable_nested_virtualization" json:"enable_nested_virtualization"`
	VariantID                  string         `db:"variant_id" json:"variant_id"`
	GPU                        bool           `db:"instance_gpu" json:"gpu"`
	Source                     InstanceSource `db:"instance_source" json:"source"`
	Network                    string         `db:"instance_network" json:"network"`
}

// Passwords holds sensitive data.
type Passwords struct {
	AnkaToken          string
	Tart               string
	TartMachine        string
	NomadToken         string
	AWSAccessKeyID     string
	AWSAccessKeySecret string
	AWSSessionToken    string
}

type RunnerConfig struct {
	HealthCheckHotpoolTimeout       time.Duration
	HealthCheckHibernatedTimeout    time.Duration
	HealthCheckColdstartTimeout     time.Duration
	HealthCheckWindowsTimeout       time.Duration
	HealthCheckConnectivityDuration time.Duration
	SetupTimeout                    time.Duration
	StartStepTimeout                time.Duration
	HA                              bool
}

type NomadConfig struct {
	ClientDisconnectTimeout time.Duration
	ResourceJobTimeout      time.Duration
	InitTimeout             time.Duration
	ByoiInitTimeout         time.Duration
	DestroyTimeout          time.Duration
	GlobalAccount           string
	DestroyRetryAttempts    int
	MinNomadCPUMhz          int
	MinNomadMemoryMb        int
	MachineFrequencyMhz     int
	LargeBaremetalClass     string
	GlobalAccountMac        string
	MacMachineFrequencyMhz  int
}

type Tmate struct {
	Enabled bool
	Image   string
	Server  string
	Port    string
	RSA     string
	ED25519 string
}

type InstanceCreateOpts struct {
	CAKey          []byte
	CACert         []byte
	TLSKey         []byte
	TLSCert        []byte
	LiteEnginePath string
	Platform
	PoolName                     string
	RunnerName                   string
	Limit                        int
	Pool                         int
	HarnessTestBinaryURI         string
	PluginBinaryURI              string
	Tmate                        Tmate
	AccountID                    string
	IsHosted                     bool
	EgressControl                bool
	TPAAddress                   string
	TPAPort                      string
	EgressProxyEnabled           bool
	EgressProxyURL               string
	EgressNoProxy                string
	EgressCACert                 string
	EnableLEDiagnostics          bool
	ResourceClass                string
	GitspaceOpts                 GitspaceOpts
	StorageOpts                  StorageOpts
	AutoInjectionBinaryURI       string
	AnnotationsBinaryURI         string
	AnnotationsBinaryFallbackURI string
	InternalLabels               map[string]string
	VMLabels                     map[string]string
	Zones                        []string
	MachineType                  string
	LiteEngineFallbackPath       string
	PluginBinaryFallbackURI      string
	VMImageConfig                VMImageConfig
	DriverName                   string
	Timeout                      int64
	EnableC4D                    bool
	CapacityReservation          *CapacityReservation
	CapacityReservationTTL       int64 // seconds; GCP auto-deletes reservation after this duration
	NestedVirtualization         bool
	GPU                          bool
	EnvmanBinaryURI              string
	EnvmanBinaryFallbackURI      string
	TmateBinaryURI               string
	TmateBinaryFallbackURI       string
	ShutdownScript               string
}

// Platform defines the target platform.
type Platform struct {
	OS      string `json:"os,omitempty" db:"instance_os" default:"linux"`
	Arch    string `json:"arch,omitempty" db:"instance_arch" default:"amd64"`
	Variant string `json:"variant,omitempty" yaml:"variant,omitempty" db:"instance_variant"`
	Version string `json:"version,omitempty" yaml:"version,omitempty" db:"instance_version"`
	OSName  string `json:"os_name,omitempty" yaml:"os_name,omitempty" db:"instance_os_name"`
}

type QueryParams struct {
	Status               InstanceState
	Stage                string
	Platform             *Platform
	RunnerName           string
	MatchLabels          map[string]string
	PoolName             string
	InstanceID           string
	ImageName            string
	MachineType          string
	NestedVirtualization bool
	GPU                  bool
	VariantID            string
	FilterSource         InstanceSource
}

type StageOwner struct {
	StageID  string `db:"stage_id" json:"stage_id"`
	PoolName string `db:"pool_name" json:"pool_name"`
}

type CapacityReservation struct {
	StageID          string                   `db:"stage_id" json:"stage_id"`
	PoolName         string                   `db:"pool_name" json:"pool_name"`
	InstanceID       string                   `db:"instance_id" json:"instance_id"`
	ReservationID    string                   `db:"reservation_id" json:"reservation_id"`
	CreatedAt        int64                    `db:"created_at" json:"created_at"`
	ReservationState CapacityReservationState `db:"reservation_state" json:"reservation_state"`
	Zone             *string                  `db:"zone" json:"zone,omitempty"`
}

// GetZone returns the zone string, or empty string if Zone is nil.
func (c *CapacityReservation) GetZone() string {
	if c.Zone != nil {
		return *c.Zone
	}
	return ""
}

// StringPtr returns a pointer to the given string.
func StringPtr(s string) *string {
	return &s
}

type GitspaceOpts struct {
	GitspaceConfigIdentifier string
	Secret                   string // Deprecated: VMInitScript should be used to send the whole script
	AccessToken              string // Deprecated: VMInitScript should be used to send the whole script
	Ports                    []int
	VMInitScript             string
}

type StorageOpts struct {
	CephPoolIdentifier string
	Identifier         string
	Size               string
	Type               string
	BootDiskType       string
	BootDiskSize       string
}

type GitspaceAgentConfig struct {
	Secret                   string `json:"secret"`       // Deprecated: VMInitScript should be used to send the whole script
	AccessToken              string `json:"access_token"` // Deprecated: VMInitScript should be used to send the whole script
	Ports                    []int  `json:"ports"`
	VMInitScript             string `json:"vm_init_script"`
	ShutdownScript           string `json:"shutdown_script,omitempty"`
	GitspaceConfigIdentifier string `json:"gitspace_config_identifier"`
}

type StorageConfig struct {
	CephPoolIdentifier string `json:"ceph_pool_identifier"`
	Identifier         string `json:"identifier"`
	Size               string `json:"size"`
	Type               string `json:"type" default:"pd-balanced"`
	BootDiskSize       string `json:"boot_disk_size"`
	BootDiskType       string `json:"boot_disk_type"`
}

// Firewall rule states.
const (
	FirewallStateProvisioning = "provisioning"
	FirewallStateActive       = "active"
)

// FirewallRule represents a cloud firewall rule stored in the DB for cleanup tracking.
type FirewallRule struct {
	ID            int64  `db:"id" json:"id"`
	StageID       string `db:"stage_id" json:"stage_id"`
	InstanceID    string `db:"instance_id" json:"instance_id"`
	ResourceID    string `db:"resource_id" json:"resource_id"`
	CloudProvider string `db:"cloud_provider" json:"cloud_provider"`
	ProjectID     string `db:"project_id" json:"project_id"`
	State         string `db:"state" json:"state"`
	CreatedAt     int64  `db:"created_at" json:"created_at"`
}

// ProjectFromNetwork extracts the GCP project from a fully qualified network path
// (e.g. "projects/<project>/global/networks/<name>"). Falls back to defaultProject.
func ProjectFromNetwork(network, defaultProject string) string {
	if strings.HasPrefix(network, "projects/") {
		parts := strings.SplitN(network, "/", 3) //nolint:mnd
		if len(parts) >= 2 && parts[1] != "" {
			return parts[1]
		}
	}
	return defaultProject
}

// EgressPolicy defines the egress/outbound traffic restriction policy for a VM.
type EgressPolicy struct {
	Enabled    bool     `json:"enabled"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

type VMImageConfig struct {
	ImageVersion string
	ImageName    string
	Username     string
	Password     string
	VMImageAuth  VMImageAuth
}

type VMImageAuth struct {
	Registry string
	Username string
	Password string
}

// OutboxJobStatus represents the status of an outbox job
type OutboxJobStatus string

const (
	OutboxJobStatusPending = OutboxJobStatus("pending")
	OutboxJobStatusRunning = OutboxJobStatus("running")
)

// OutboxJobType represents the type of outbox job
type OutboxJobType string

const (
	OutboxJobTypeSetupInstance = OutboxJobType("setup_instance")
	OutboxJobTypeScale         = OutboxJobType("scale")
)

// OutboxJob represents a job in the outbox queue
type OutboxJob struct {
	ID           int64            `db:"id" json:"id"`
	PoolName     string           `db:"pool_name" json:"pool_name"`
	RunnerName   string           `db:"runner_name" json:"runner_name"`
	JobType      OutboxJobType    `db:"job_type" json:"job_type"`
	JobParams    *json.RawMessage `db:"job_params" json:"job_params"`
	CreatedAt    int64            `db:"created_at" json:"created_at"`
	ProcessedAt  *int64           `db:"processed_at" json:"processed_at"`
	Status       OutboxJobStatus  `db:"status" json:"status"`
	ErrorMessage *string          `db:"error_message" json:"error_message"`
	RetryCount   int              `db:"retry_count" json:"retry_count"`
}

// SetupInstanceParams represents the additional parameters for setting up an instance asynchronously
type SetupInstanceParams struct {
	ImageName            string         `json:"image_name,omitempty" yaml:"image_name,omitempty"`
	NestedVirtualization bool           `json:"enable_nested_virtualization,omitempty" yaml:"enable_nested_virtualization,omitempty"`
	MachineType          string         `json:"machine_type,omitempty" yaml:"machine_type,omitempty"`
	Hibernate            bool           `json:"hibernate,omitempty" yaml:"hibernate,omitempty"`
	Zones                []string       `json:"zones,omitempty" yaml:"zones,omitempty"`
	VariantID            string         `json:"variant_id,omitempty" yaml:"variant_id,omitempty"`
	DiskSize             int64          `json:"disk_size,omitempty" yaml:"disk_size,omitempty"`
	DiskType             string         `json:"disk_type,omitempty" yaml:"disk_type,omitempty"`
	ResourceClass        string         `json:"resource_class,omitempty" yaml:"resource_class,omitempty"`
	GPU                  bool           `json:"gpu,omitempty" yaml:"gpu,omitempty"`
	Source               InstanceSource `json:"source,omitempty" yaml:"source,omitempty"`
}

// ScaleJobParams represents the parameters for a scaling job.
// Note: PoolName is stored in OutboxJob.PoolName, not duplicated here.
type ScaleJobParams struct {
	WindowStart int64 `json:"window_start"` // Unix timestamp of window start
	WindowEnd   int64 `json:"window_end"`   // Unix timestamp of window end
}

// ScalerConfig contains configuration for the pool scaler
type ScalerConfig struct {
	// WindowDuration is the duration of each scaling window (default: 30 minutes)
	WindowDuration time.Duration
	// LeadTime is how long before a window starts to scale for it (default: 5 minutes)
	LeadTime time.Duration
	// Enabled controls whether scaling is active
	Enabled bool
	// DryRun when enabled, only records metrics without performing actual scale up/down operations
	DryRun bool
	// DisabledPools is a list of pool names that should be skipped during scaling
	DisabledPools []string
	// ActiveImageLookbackDays is how many days to look back when discovering active images (default: 2)
	ActiveImageLookbackDays int
	// RecentUsageLookbackDays is how many days to look back when checking for recent usage
	// to determine if a variant/image with zero prediction should still have a minimum pool.
	// Default: 7
	RecentUsageLookbackDays int
	// RecentUsageMinInstances is the minimum number of instances to maintain for a variant/image
	// combination that has zero prediction but had usage within the lookback window.
	// This is applied on top of minSize: if RecentUsageMinInstances=3 and minSize=2,
	// only 1 additional instance is added. Default: 0 (disabled)
	RecentUsageMinInstances int
	// ScalePercent is the percentage of additional instances to pre-provision as a hibernated
	// buffer above the predicted live demand. It is only applied to positive scale-up deltas.
	// A value of 100 (or below) disables buffering; 115 adds a 15% hibernated buffer on top
	// of the live scale-up. Default: 100 (disabled).
	ScalePercent float64
}

// InstanceCount holds an instance count grouped by pool, variant, image, and GPU flag.
type InstanceCount struct {
	Pool      string `db:"pool"`
	VariantID string `db:"variant_id"`
	ImageName string `db:"image_name"`
	Count     int    `db:"count"`
}

// ProvisionParams holds parameters from the incoming provision request.
// These represent what the API caller requested for instance provisioning.
type ProvisionParams struct {
	VMImageConfig        *spec.VMImageConfig
	NestedVirtualization bool
	ResourceClass        string
	Zones                []string
}

// ToSetupInstanceParams converts request-level ProvisionParams to internal SetupInstanceParams.
func (p *ProvisionParams) ToSetupInstanceParams() *SetupInstanceParams {
	if p == nil {
		return nil
	}
	s := &SetupInstanceParams{
		NestedVirtualization: p.NestedVirtualization,
		ResourceClass:        p.ResourceClass,
	}
	if len(p.Zones) > 0 {
		s.Zones = make([]string, len(p.Zones))
		copy(s.Zones, p.Zones)
	}
	return s
}

// GetVMImageConfig returns the VMImageConfig from ProvisionParams, or nil if ProvisionParams is nil.
func (p *ProvisionParams) GetVMImageConfig() *spec.VMImageConfig {
	if p == nil {
		return nil
	}
	return p.VMImageConfig
}

type PoolVariant struct {
	Pool  int `json:"pool" yaml:"pool"`
	Limit int `json:"limit" yaml:"limit"`
	SetupInstanceParams
}

type UtilizationRecord struct {
	ID             int64  `db:"id" json:"id"`
	Pool           string `db:"pool_name" json:"pool"`
	VariantID      string `db:"variant_id" json:"variant_id"`
	ImageName      string `db:"image_name" json:"image_name"`
	InUseInstances int    `db:"in_use_instances" json:"in_use_instances"`
	RecordedAt     int64  `db:"recorded_at" json:"recorded_at"`
}
