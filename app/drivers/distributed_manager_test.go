// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/engine/spec"

	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

// mockDriver implements minimal Driver interface for testing
type mockDriver struct{}

func (m *mockDriver) GetFullyQualifiedImage(ctx context.Context, config *types.VMImageConfig) (string, error) {
	if config == nil || config.ImageName == "" {
		return "", nil
	}
	// Simple mock: just return the image name as-is
	return config.ImageName, nil
}

// Implement other required Driver interface methods as no-ops for testing
func (m *mockDriver) Create(ctx context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) {
	return nil, nil
}
func (m *mockDriver) Destroy(ctx context.Context, instances []*types.Instance) error { return nil }
func (m *mockDriver) Start(ctx context.Context, instance *types.Instance, poolName string) (string, error) {
	return "", nil
}
func (m *mockDriver) Hibernate(ctx context.Context, instanceID, poolName, zone string) error {
	return nil
}
func (m *mockDriver) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) error {
	return nil
}
func (m *mockDriver) Logs(ctx context.Context, instanceID string) (string, error) {
	return "", nil
}
func (m *mockDriver) Ping(ctx context.Context) error { return nil }
func (m *mockDriver) ReserveCapacity(ctx context.Context, opts *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	return nil, nil
}
func (m *mockDriver) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) error {
	return nil
}
func (m *mockDriver) SetTags(ctx context.Context, instance *types.Instance, tags map[string]string) error {
	return nil
}
func (m *mockDriver) RootDir() string        { return "/tmp" }
func (m *mockDriver) DriverName() string     { return "mock" }
func (m *mockDriver) CanHibernate() bool     { return false }

func TestFilterVariant(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	log.Logger.SetLevel(logrus.FatalLevel) // Suppress logs during tests
	ctx := logger.WithContext(context.Background(), logger.Logrus(log))

	tests := []struct {
		name          string
		variants      []types.PoolVariant
		machineConfig *types.MachineConfig
		expectedID    string // empty string means nil expected
	}{
		{
			name: "single variant matching resource class",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "small",
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass: "small",
				},
			},
			expectedID: "variant-1",
		},
		{
			name: "no variant matching resource class returns nil",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "small",
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass: "large",
				},
			},
			expectedID: "",
		},
		{
			name: "multiple variants, filter by resource class and nested virt",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-1",
						ResourceClass:        "medium",
						NestedVirtualization: false,
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-2",
						ResourceClass:        "medium",
						NestedVirtualization: true,
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass:        "medium",
					NestedVirtualization: true,
				},
			},
			expectedID: "variant-2",
		},
		{
			name: "multiple variants, filter by resource class and image name",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "large",
						ImageName:     "ubuntu-20.04",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-2",
						ResourceClass: "large",
						ImageName:     "ubuntu-22.04",
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass: "large",
				},
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
			expectedID: "variant-2",
		},
		{
			name: "multiple variants match resource class, no refined match, returns first",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "xlarge",
						ImageName:     "debian-11",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-2",
						ResourceClass: "xlarge",
						ImageName:     "debian-12",
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass: "xlarge",
				},
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "centos-7",
				},
			},
			expectedID: "variant-1", // Falls back to first resource class match
		},
		{
			name: "exact match with all criteria",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-1",
						ResourceClass:        "premium",
						ImageName:            "custom-image",
						NestedVirtualization: true,
					},
				},
			},
			machineConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ResourceClass:        "premium",
					NestedVirtualization: true,
				},
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "custom-image",
				},
			},
			expectedID: "variant-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &poolEntry{
				Pool: Pool{
					Name:         "test-pool",
					PoolVariants: tt.variants,
					Driver:       &mockDriver{},
				},
			}

			dm := &DistributedManager{}
			result := dm.filterVariant(ctx, pool, tt.machineConfig)

			if tt.expectedID == "" {
				if result != nil {
					t.Errorf("Expected nil result, got variant %s", result.VariantID)
				}
			} else {
				if result == nil {
					t.Errorf("Expected variant %s, got nil", tt.expectedID)
				} else if result.VariantID != tt.expectedID {
					t.Errorf("Expected variant %s, got %s", tt.expectedID, result.VariantID)
				}
			}
		})
	}
}

func TestApplyVariantToMachineConfig(t *testing.T) {
	tests := []struct {
		name           string
		initialConfig  *types.MachineConfig
		variant        *types.PoolVariant
		expectedConfig *types.MachineConfig
	}{
		{
			name: "apply machine type",
			initialConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType: "",
				},
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v1",
					MachineType: "n2-standard-4",
				},
			},
			expectedConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType: "n2-standard-4",
					VariantID:   "v1",
				},
			},
		},
		{
			name: "apply zones",
			initialConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					Zones: nil,
				},
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID: "v2",
					Zones:     []string{"us-east1-a", "us-east1-b"},
				},
			},
			expectedConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					Zones:     []string{"us-east1-a", "us-east1-b"},
					VariantID: "v2",
				},
			},
		},
		{
			name: "apply disk configuration",
			initialConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					DiskSize: "",
					DiskType: "",
				},
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID: "v3",
					DiskSize:  "100GB",
					DiskType:  "pd-ssd",
				},
			},
			expectedConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					DiskSize:  "100GB",
					DiskType:  "pd-ssd",
					VariantID: "v3",
				},
			},
		},
		{
			name: "apply all variant fields",
			initialConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{},
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v4",
					MachineType: "c2-standard-8",
					Zones:       []string{"europe-west1-b"},
					DiskSize:    "200GB",
					DiskType:    "pd-balanced",
				},
			},
			expectedConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType: "c2-standard-8",
					Zones:       []string{"europe-west1-b"},
					DiskSize:    "200GB",
					DiskType:    "pd-balanced",
					VariantID:   "v4",
				},
			},
		},
		{
			name: "don't override with empty variant values",
			initialConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType: "existing-type",
					DiskSize:    "50GB",
				},
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v5",
					MachineType: "", // Empty, should not override
					DiskSize:    "",
				},
			},
			expectedConfig: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType: "existing-type", // Preserved
					DiskSize:    "50GB",           // Preserved
					VariantID:   "v5",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dm := &DistributedManager{}
			dm.applyVariantToMachineConfig(tt.initialConfig, tt.variant)

			// Check all relevant fields
			if tt.initialConfig.MachineType != tt.expectedConfig.MachineType {
				t.Errorf("MachineType: expected %s, got %s", tt.expectedConfig.MachineType, tt.initialConfig.MachineType)
			}
			if tt.initialConfig.VariantID != tt.expectedConfig.VariantID {
				t.Errorf("VariantID: expected %s, got %s", tt.expectedConfig.VariantID, tt.initialConfig.VariantID)
			}
			if tt.initialConfig.DiskSize != tt.expectedConfig.DiskSize {
				t.Errorf("DiskSize: expected %s, got %s", tt.expectedConfig.DiskSize, tt.initialConfig.DiskSize)
			}
			if tt.initialConfig.DiskType != tt.expectedConfig.DiskType {
				t.Errorf("DiskType: expected %s, got %s", tt.expectedConfig.DiskType, tt.initialConfig.DiskType)
			}
			if len(tt.initialConfig.Zones) != len(tt.expectedConfig.Zones) {
				t.Errorf("Zones length: expected %d, got %d", len(tt.expectedConfig.Zones), len(tt.initialConfig.Zones))
			} else {
				for i := range tt.initialConfig.Zones {
					if tt.initialConfig.Zones[i] != tt.expectedConfig.Zones[i] {
						t.Errorf("Zones[%d]: expected %s, got %s", i, tt.expectedConfig.Zones[i], tt.initialConfig.Zones[i])
					}
				}
			}
		})
	}
}
