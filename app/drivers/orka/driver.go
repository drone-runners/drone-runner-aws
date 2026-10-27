package orka

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dchest/uniuri"
	"github.com/drone/runner-go/logger"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ drivers.Driver = (*config)(nil)

// defaultUserData is Orka's lite-engine bootstrap script. Orka always uses this instead of
// falling through to cloudinit's built-in mac_arm64 template, since its once-per-boot LaunchDaemon
// model needs a durable KeepAlive daemon for lite-engine (not a bare `&`-backgrounded process),
// and can't rely on $HOME being /var/root.
//
//go:embed templates/startup.sh.gotmpl
var defaultUserData string

// startupScriptKey is the customMetadata key Orka's baked-in bootstrap LaunchDaemon fetches and
// executes on every boot. It always wins over a customer-supplied key of the same name.
const startupScriptKey = "startup_script"

// primary/fallback reserved host ports for lite-engine. Hardcoded, not customer-configurable.
const (
	primaryPort  = 9079
	fallbackPort = 9080

	// guestLiteEnginePort is the VM-internal port lite-engine listens on, regardless of which
	// host port it's forwarded to.
	guestLiteEnginePort = 9079

	defaultDeployTimeout = 10 * time.Minute // Orka's own default deploy timeout

	// deployTimeoutBuffer gives the server extra room to respond with its own terminal result
	// before the client gives up and treats the call as timed out.
	deployTimeoutBuffer = 30 * time.Second

	vmStatusRunning = "Running"
)

type config struct {
	namespace     string
	controllerURL string
	authToken     string
	vmConfig      string
	image         string
	userData      string
	rootDir       string
	timeoutSec    int
	orkaClient    Client

	// VM deploy-request fields, carried from the pool-spec (command/config/config.go's Orka
	// struct) through to Create()'s vmDeploy body.
	cpu            int
	memory         float64
	vnc            bool
	tag            string
	tagRequired    bool
	node           string
	scheduler      string
	displayWidth   int
	displayHeight  int
	displayDPI     int
	customMetadata map[string]string
}

func New(opts ...Option) (drivers.Driver, error) {
	c := new(config)
	for _, opt := range opts {
		opt(c)
	}
	if c.userData == "" {
		c.userData = defaultUserData
	}
	if c.orkaClient == nil {
		c.orkaClient = NewClient(c.controllerURL, c.authToken, c.namespace)
	}
	return c, nil
}

// ReserveCapacity reserves capacity for a VM. Orka has no capacity-reservation API; unsupported,
// same as ankabuild/anka.
func (c *config) ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	return nil, &ierrors.ErrCapacityReservationNotSupported{Driver: c.DriverName()}
}

// DestroyCapacity destroys capacity for a VM. Unsupported, see ReserveCapacity.
func (c *config) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) (err error) {
	return &ierrors.ErrCapacityReservationNotSupported{Driver: c.DriverName()}
}

// Create deploys an Orka VM: resolves the image/vmConfig, then deploys with a dual-port retry
// (primaryPort, falling back to fallbackPort on a 409 port-reservation conflict). vmDeploy blocks
// server-side until the VM reaches a terminal state or its own "timeout" field elapses, so there's
// no separate getVM polling loop - Create relies on that single call's response, bounded by a
// client-side context deadline slightly longer than the requested timeout. On a client-side
// timeout, or a response that didn't reach Running, the VM is explicitly deleted before returning
// an error - Orka only auto-cleans rejected (409) deploys, not ones that time out or fail after
// being accepted.
func (c *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	startTime := time.Now()
	// Orka validates VM names as RFC 1123 lowercase subdomains; uniuri.NewLen draws from
	// uniuri.StdChars, which includes uppercase letters, so the whole name must be lowered.
	machineName := strings.ToLower(fmt.Sprintf("%s-%s-%s", opts.RunnerName, opts.PoolName, uniuri.NewLen(8))) //nolint:mnd
	logr := logger.FromContext(ctx).
		WithField("cloud", types.Orka).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	vmConfigField, imageField, err := c.resolveDeployImage(ctx, &opts.VMImageConfig)
	if err != nil {
		return nil, fmt.Errorf("orka: failed to resolve image: %w", err)
	}

	userData, err := lehelper.GenerateUserdata(c.userData, opts)
	if err != nil {
		return nil, fmt.Errorf("orka: failed to generate user data: %w", err)
	}

	timeoutSec := c.timeoutSec
	if timeoutSec <= 0 {
		timeoutSec = int(defaultDeployTimeout.Seconds())
	}

	req := &deployVMRequest{
		Name:           machineName,
		VMConfig:       vmConfigField,
		Image:          imageField,
		CPU:            c.cpu,
		Memory:         c.memory,
		VNC:            c.vnc,
		Tag:            c.tag,
		TagRequired:    c.tagRequired,
		Node:           c.node,
		Scheduler:      c.scheduler,
		DisplayWidth:   c.displayWidth,
		DisplayHeight:  c.displayHeight,
		DisplayDPI:     c.displayDPI,
		CustomMetadata: c.buildCustomMetadata(userData),
		Timeout:        deployTimeoutMinutes(timeoutSec),
	}

	deployCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second+deployTimeoutBuffer)
	defer cancel()

	hostPort, deployResp, deployErr := c.deployWithPortRetry(deployCtx, req, logr)
	if deployErr != nil {
		if errors.Is(deployErr, context.DeadlineExceeded) || errors.Is(deployErr, context.Canceled) {
			// ctx may already be done here too (e.g. a canceled stage deadline, not just
			// deployCtx's own timeout), so cleanup needs an independent context - reusing ctx
			// would make DeleteVM fail immediately as well, leaving the VM running forever.
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), defaultRequestTimeout)
			defer cancelCleanup()
			return nil, c.deleteAndWrapErr(cleanupCtx, machineName,
				fmt.Errorf("orka: deploy timed out waiting for VM %q to become Running: %w", machineName, deployErr))
		}
		return nil, deployErr
	}
	if deployResp.Status != vmStatusRunning {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), defaultRequestTimeout)
		defer cancelCleanup()
		return nil, c.deleteAndWrapErr(cleanupCtx, machineName,
			fmt.Errorf("orka: VM %q did not reach Running (status %q)", machineName, deployResp.Status))
	}

	now := time.Now().Unix()
	instance = &types.Instance{
		ID:       machineName,
		Name:     machineName,
		Provider: types.Orka,
		State:    types.StateProvisioning,
		Pool:     opts.PoolName,
		Image:    imageField,
		Platform: opts.Platform,
		Address:  deployResp.IP,
		Port:     int64(hostPort),
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Started:  startTime.Unix(),
		Updated:  now,
	}
	logr.WithField("ip", deployResp.IP).
		WithField("port", hostPort).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Infoln("orka: VM created")
	return instance, nil
}

// resolveDeployImage decides which of the deploy request's mutually-exclusive vmConfig/image
// fields to populate. A configured vm_config always wins (it's a reusable Orka-side template that
// already bundles an image); otherwise image is populated via GetFullyQualifiedImage, which still
// allows a per-build ImageName override.
func (c *config) resolveDeployImage(ctx context.Context, imgConfig *types.VMImageConfig) (vmConfigField, imageField string, err error) {
	if c.vmConfig != "" {
		return c.vmConfig, "", nil
	}
	imageField, err = c.GetFullyQualifiedImage(ctx, imgConfig)
	if err != nil {
		return "", "", err
	}
	return "", imageField, nil
}

// buildCustomMetadata merges the pool-configured customMetadata with the rendered lite-engine
// bootstrap script, keyed under startupScriptKey. Builds a fresh map per call (never mutates
// c.customMetadata) since Create() can run concurrently across multiple VMs. Customer values are
// copied in first so startupScriptKey always wins on collision.
func (c *config) buildCustomMetadata(userData string) map[string]string {
	merged := make(map[string]string, len(c.customMetadata)+1)
	for k, v := range c.customMetadata {
		merged[k] = v
	}
	merged[startupScriptKey] = base64.StdEncoding.EncodeToString([]byte(userData))
	return merged
}

// deployTimeoutMinutes converts a seconds-based timeout to the minutes Orka's deploy "timeout"
// field expects, rounding up. A non-positive value stays 0, which Orka treats as its default.
func deployTimeoutMinutes(timeoutSec int) int {
	if timeoutSec <= 0 {
		return 0
	}
	const secondsPerMinute = 60
	return (timeoutSec + secondsPerMinute - 1) / secondsPerMinute
}

// deployWithPortRetry tries vmDeploy with primaryPort, retrying once with fallbackPort if the
// controller rejects the first attempt because that port range isn't free on the chosen node
// (see IsPortUnavailable). A second attempt always succeeds if the cluster has any capacity at
// all, so no further retries happen.
func (c *config) deployWithPortRetry(ctx context.Context, req *deployVMRequest, logr logger.Logger) (hostPort int, resp *deployVMResponse, err error) {
	ports := []int{primaryPort, fallbackPort}
	for i, port := range ports {
		req.ReservedPorts = fmt.Sprintf("%d:%d", port, guestLiteEnginePort)
		resp, err = c.orkaClient.DeployVM(ctx, c.namespace, req)
		if err == nil {
			return port, resp, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.IsPortUnavailable() {
			return 0, nil, fmt.Errorf("orka: deploy failed: %w", err)
		}
		logr.WithField("port", port).WithError(err).
			Warnln("orka: deploy rejected (port conflict), retrying with fallback port")
		if i == len(ports)-1 {
			return 0, nil, fmt.Errorf("orka: deploy failed after port retry: %w", err)
		}
	}
	return 0, nil, fmt.Errorf("orka: deploy failed: %w", err)
}

// deleteAndWrapErr explicitly deletes the VM before surfacing origErr - required because Orka
// only auto-cleans deploys rejected at request time (409), not ones that time out or fail after
// being accepted.
func (c *config) deleteAndWrapErr(ctx context.Context, name string, origErr error) error {
	if delErr := c.orkaClient.DeleteVM(ctx, c.namespace, name); delErr != nil {
		return fmt.Errorf("%w (additionally failed to delete VM: %v)", origErr, delErr) //nolint:errorlint
	}
	return origErr
}

func (c *config) Destroy(ctx context.Context, instances []*types.Instance) ([]*types.Instance, error) {
	return c.DestroyInstanceAndStorage(ctx, instances, nil)
}

// DestroyInstanceAndStorage deletes each instance's VM. It does not early-exit on failure - all
// instances are attempted, and any that fail are returned in failedInstances for the caller's
// partial-failure tracking. A 404 means the VM is already gone, treated as a successful delete.
func (c *config) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, _ *storage.CleanupType) ([]*types.Instance, error) {
	if len(instances) == 0 {
		return nil, nil
	}
	var failedInstances []*types.Instance
	var lastErr error
	for _, instance := range instances {
		logr := logger.FromContext(ctx).
			WithField("id", instance.ID).
			WithField("driver", types.Orka)

		err := c.orkaClient.DeleteVM(ctx, c.namespace, instance.ID)
		if err != nil {
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.IsNotFound() {
				logr.WithError(err).Warnln("orka: VM not found, treating as already deleted")
				continue
			}
			logr.WithError(err).Errorln("orka: failed to delete VM")
			failedInstances = append(failedInstances, instance)
			lastErr = err
			continue
		}
		logr.Traceln("orka: VM deleted")
	}

	if len(failedInstances) > 0 {
		failedIDs := make([]string, len(failedInstances))
		for i, inst := range failedInstances {
			failedIDs[i] = inst.ID
		}
		return failedInstances, fmt.Errorf("orka: failed to delete %d instance(s): %v: %w",
			len(failedInstances), failedIDs, lastErr)
	}
	return nil, nil
}

func (c *config) Hibernate(_ context.Context, _, _, _ string) error {
	return errors.New("unimplemented")
}

func (c *config) Start(_ context.Context, _ *types.Instance, _ string) (ipAddress string, err error) {
	return "", errors.New("unimplemented")
}

// Ping confirms the configured Orka controller is reachable and authenticated.
func (c *config) Ping(ctx context.Context) error {
	resp, err := c.orkaClient.Version(ctx)
	if err != nil {
		return err
	}
	if resp.APIVersion == "" {
		return errors.New("orka: controller returned an empty apiVersion")
	}
	return nil
}

func (c *config) Logs(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (c *config) SetTags(ctx context.Context, instance *types.Instance, tags map[string]string) error {
	return nil
}

func (c *config) SetLabels(context.Context, *types.Instance, map[string]string) error {
	return nil
}

func (c *config) RootDir() string {
	return c.rootDir
}

func (c *config) DriverName() string {
	return string(types.Orka)
}

func (c *config) CanHibernate() bool {
	return false
}

func (c *config) GetFullyQualifiedImage(_ context.Context, imgConfig *types.VMImageConfig) (string, error) {
	if imgConfig != nil && imgConfig.ImageName != "" {
		return imgConfig.ImageName, nil
	}
	if c.vmConfig != "" {
		return c.vmConfig, nil
	}
	return c.image, nil
}
