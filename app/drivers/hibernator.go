package drivers

import (
	"context"
	"fmt"
	"time"

	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	itypes "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	defaultConnectivityTimeout = 15 * time.Minute
)

// setupInstanceWithHibernate sets up an instance and then hibernates it.
func (m *Manager) setupInstanceWithHibernate(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID string,
	machineConfig *types.MachineConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	platform *types.Platform,
) (*types.Instance, error) {
	inst, _, err := m.setupInstance(ctx,
		pool,
		tlsServerName,
		ownerID,
		machineConfig,
		false,
		agentConfig,
		storageConfig,
		timeout,
		platform,
		nil,
		false)
	if err != nil {
		return nil, err
	}
	go func() {
		ctx := m.globalCtx

		// Step 1: Wait for instance connectivity
		if !m.waitForInstanceConnectivity(ctx, tlsServerName, inst.ID) {
			logrus.WithField("instanceID", inst.ID).Errorln("connectivity check failed, destroying instance")
			if derr := m.Destroy(ctx, pool.Name, inst.ID, inst, nil); derr != nil {
				logrus.WithError(derr).WithField("instanceID", inst.ID).Errorln("failed to cleanup instance after connectivity failure")
			}
			return
		}

		// Step 2: Connectivity successful - update state to Created (VM is ready for use)
		inst.State = types.StateCreated
		if updateErr := m.instanceStore.Update(ctx, inst); updateErr != nil {
			logrus.WithError(updateErr).WithField("instanceID", inst.ID).Errorln("failed to update instance state to created")
			return
		}
		logrus.WithField("instanceID", inst.ID).Infoln("instance connectivity verified, state updated to created")

		// Step 3: Attempt to hibernate the instance
		herr := m.hibernateOrStopWithRetries(ctx, pool.Name, inst, false)
		if herr != nil {
			logrus.WithError(herr).Errorln("failed to hibernate the vm")
		}
	}()
	return inst, nil
}

// hibernateOrStopWithRetries attempts to hibernate an instance with retries.
func (m *Manager) hibernateOrStopWithRetries(
	ctx context.Context,
	poolName string,
	instance *types.Instance,
	fallbackStop bool,
) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if !pool.Driver.CanHibernate() && !fallbackStop {
		return nil
	}

	retryCount := 1
	const maxRetries = 3
	for {
		err := m.hibernate(ctx, instance.ID, poolName, pool)
		if err == nil {
			logrus.WithField("instanceID", instance.ID).Infoln("hibernate complete")
			return nil
		}

		logrus.WithError(err).WithField("retryCount", retryCount).Warnln("failed to hibernate the vm")
		var re *itypes.RetryableError
		if !errors.As(err, &re) {
			return err
		}

		if retryCount >= maxRetries {
			return err
		}

		time.Sleep(time.Minute)
		retryCount++
	}
}

// hibernate hibernates an instance.
func (m *Manager) hibernate(ctx context.Context, instanceID, poolName string, pool *poolEntry) error {
	pool.Lock()
	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	if inst.State == types.StateInUse {
		pool.Unlock()
		return nil
	}
	inst.State = types.StateHibernating
	if err = m.instanceStore.Update(ctx, inst); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to update instance in db %s of %q pool: %w", instanceID, poolName, err)
	}
	pool.Unlock()

	logrus.WithField("instanceID", instanceID).Infoln("Hibernating vm")
	if err = pool.Driver.Hibernate(ctx, instanceID, poolName, inst.Zone); err != nil {
		if uerr := m.updateInstState(ctx, pool, instanceID, types.StateCreated); uerr != nil {
			logrus.WithError(err).WithField("instanceID", instanceID).Errorln("failed to update state for failed hibernation")
		}
		return fmt.Errorf("hibernate: failed to hibernated an instance %s of %q pool: %w", instanceID, poolName, err)
	}

	pool.Lock()
	if inst, err = m.Find(ctx, instanceID); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	inst.IsHibernated = true
	inst.State = types.StateCreated
	if err = m.instanceStore.Update(ctx, inst); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to update instance in db %s of %q pool: %w", instanceID, poolName, err)
	}
	pool.Unlock()
	return nil
}

// updateInstState updates the state of an instance.
func (m *Manager) updateInstState(ctx context.Context, pool *poolEntry, instanceID string, state types.InstanceState) error {
	pool.Lock()
	defer pool.Unlock()

	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("update state: failed to find the instance in db %s of %q pool: %w", instanceID, pool.Name, err)
	}

	inst.State = state
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return fmt.Errorf("update state: failed to update instance in db %s of %q pool: %w", instanceID, pool.Name, err)
	}
	return nil
}

// waitForInstanceConnectivity waits for an instance to become reachable.
func (m *Manager) waitForInstanceConnectivity(ctx context.Context, tlsServerName, instanceID string) bool {
	instance, err := m.Find(ctx, instanceID)
	if err != nil {
		logrus.WithError(err).WithField("instanceID", instanceID).Errorln("connectivity check: failed to find instance in db")
		return false
	}

	if instance.Address == "" {
		logrus.WithField("instanceID", instanceID).Errorln("connectivity check: instance has not received IP address")
		return false
	}

	endpoint := fmt.Sprintf("https://%s:9079/", instance.Address)
	client, err := lehttp.NewHTTPClient(endpoint, tlsServerName, string(instance.CACert), string(instance.TLSCert), string(instance.TLSKey))
	if err != nil {
		logrus.WithError(err).WithField("instanceID", instanceID).Errorln("connectivity check: failed to create client")
		return false
	}

	_, err = client.RetryHealth(ctx, &api.HealthRequest{
		PerformDNSLookup:                ShouldPerformDNSLookup(ctx, instance.Platform.OS, false),
		Timeout:                         defaultConnectivityTimeout,
		HealthCheckConnectivityDuration: m.GetHealthCheckConnectivityDuration(),
	})
	if err != nil {
		logrus.WithError(err).WithField("instanceID", instanceID).Errorln("connectivity check: health check failed")
		return false
	}
	return true
}

// findOrCreateInstance finds or creates an instance in the store.
func (m *Manager) findOrCreateInstance(ctx context.Context, pool *poolEntry, instance *types.Instance) (*types.Instance, error) {
	pool.Lock()
	defer pool.Unlock()

	if instance == nil {
		return nil, fmt.Errorf("instance is nil")
	}

	instanceFromStore, err := m.Find(ctx, instance.ID)
	if err != nil || instanceFromStore == nil {
		logrus.WithField("instanceID", instance.ID).Infoln("Instance not found in db, creating a new entry")
		if err := m.instanceStore.Create(ctx, instance); err != nil {
			return nil, fmt.Errorf("failed to create instance in db %s: %w", instance.ID, err)
		}
	} else {
		instance = instanceFromStore
	}

	instance.State = types.StateCreated
	if err := m.instanceStore.Update(ctx, instance); err != nil {
		return nil, fmt.Errorf(
			"failed to update instance in db %s of %q pool: %w",
			instance.ID,
			pool.Name,
			err,
		)
	}

	return instance, nil
}
