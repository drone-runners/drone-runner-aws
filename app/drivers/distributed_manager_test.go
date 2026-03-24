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
func (m *mockDriver) Destroy(ctx context.Context, instances []*types.Instance) ([]*types.Instance, error) {
	return nil, nil
}
func (m *mockDriver) Start(ctx context.Context, instance *types.Instance, poolName string) (string, error) {
	return "", nil
}
func (m *mockDriver) Hibernate(ctx context.Context, instanceID, poolName, zone string) error {
	return nil
}
func (m *mockDriver) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) ([]*types.Instance, error) {
	return nil, nil
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
func (m *mockDriver) RootDir() string {
	return "/tmp"
}
func (m *mockDriver) DriverName() string {
	return "mock"
}
func (m *mockDriver) CanHibernate() bool {
	return false
}

func TestFilterVariants(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	log.Logger.SetLevel(logrus.FatalLevel) // Suppress logs during tests
	ctx := logger.WithContext(context.Background(), logger.Logrus(log))

	tests := []struct {
		name            string
		variants        []types.PoolVariant
		provisionParams *types.ProvisionParams
		expectedIDs     []string // empty slice means nil expected
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
			provisionParams: &types.ProvisionParams{
				ResourceClass: "small",
			},
			expectedIDs: []string{"variant-1"},
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
			provisionParams: &types.ProvisionParams{
				ResourceClass: "large",
			},
			expectedIDs: nil,
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
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "medium",
				NestedVirtualization: true,
			},
			expectedIDs: []string{"variant-2"},
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
			provisionParams: &types.ProvisionParams{
				ResourceClass: "large",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
			expectedIDs: []string{"variant-2"},
		},
		{
			name: "image filter no match falls back to resource class and nested virt matches",
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
			provisionParams: &types.ProvisionParams{
				ResourceClass: "xlarge",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "centos-7",
				},
			},
			expectedIDs: []string{"variant-1", "variant-2"}, // Falls back to step 1 results
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
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "premium",
				NestedVirtualization: true,
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "custom-image",
				},
			},
			expectedIDs: []string{"variant-1"},
		},
		{
			name: "returns all matching variants in order",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "medium",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-2",
						ResourceClass: "medium",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-3",
						ResourceClass: "medium",
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
			},
			expectedIDs: []string{"variant-1", "variant-2", "variant-3"},
		},
		{
			name: "resource class matches but nested virt mismatch returns nil",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-1",
						ResourceClass:        "medium",
						NestedVirtualization: false,
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "medium",
				NestedVirtualization: true,
			},
			expectedIDs: nil,
		},
		{
			name: "resource class and nested virt match without image filter returns all",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-1",
						ResourceClass:        "large",
						NestedVirtualization: true,
						ImageName:            "ubuntu-20.04",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:            "variant-2",
						ResourceClass:        "large",
						NestedVirtualization: true,
						ImageName:            "ubuntu-22.04",
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "large",
				NestedVirtualization: true,
			},
			expectedIDs: []string{"variant-1", "variant-2"},
		},
		{
			name: "image filter matches subset of step 1 candidates",
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
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-3",
						ResourceClass: "large",
						ImageName:     "ubuntu-22.04",
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass: "large",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
			expectedIDs: []string{"variant-2", "variant-3"},
		},
		{
			name: "variant with empty image name excluded from image matches but included in fallback",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "medium",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-2",
						ResourceClass: "medium",
						ImageName:     "ubuntu-22.04",
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
			expectedIDs: []string{"variant-2"},
		},
		{
			name: "all variants have empty image name with image filter falls back to step 1",
			variants: []types.PoolVariant{
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-1",
						ResourceClass: "small",
					},
				},
				{
					SetupInstanceParams: types.SetupInstanceParams{
						VariantID:     "variant-2",
						ResourceClass: "small",
					},
				},
			},
			provisionParams: &types.ProvisionParams{
				ResourceClass: "small",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
			expectedIDs: []string{"variant-1", "variant-2"},
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
			results := dm.filterVariants(ctx, pool, tt.provisionParams)

			if tt.expectedIDs == nil {
				if results != nil {
					t.Errorf("Expected nil result, got %d variants", len(results))
				}
				return
			}

			if len(results) != len(tt.expectedIDs) {
				resultIDs := make([]string, len(results))
				for i, r := range results {
					resultIDs[i] = r.VariantID
				}
				t.Fatalf("Expected %d variants %v, got %d variants %v", len(tt.expectedIDs), tt.expectedIDs, len(results), resultIDs)
			}

			for i, expectedID := range tt.expectedIDs {
				if results[i].VariantID != expectedID {
					t.Errorf("Variant[%d]: expected %s, got %s", i, expectedID, results[i].VariantID)
				}
			}
		})
	}
}

func TestApplyVariantToSetupParams(t *testing.T) {
	tests := []struct {
		name           string
		initialParams  *types.SetupInstanceParams
		variant        *types.PoolVariant
		expectedParams *types.SetupInstanceParams
	}{
		{
			name: "apply machine type",
			initialParams: &types.SetupInstanceParams{
				MachineType: "",
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v1",
					MachineType: "n2-standard-4",
				},
			},
			expectedParams: &types.SetupInstanceParams{
				MachineType: "n2-standard-4",
				VariantID:   "v1",
			},
		},
		{
			name: "zones not copied from variant (zones handled at network config level)",
			initialParams: &types.SetupInstanceParams{
				Zones: nil,
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID: "v2",
					Zones:     []string{"us-east1-a", "us-east1-b"},
				},
			},
			expectedParams: &types.SetupInstanceParams{
				VariantID: "v2",
			},
		},
		{
			name: "apply disk configuration",
			initialParams: &types.SetupInstanceParams{
				DiskSize: 0,
				DiskType: "",
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID: "v3",
					DiskSize:  100,
					DiskType:  "pd-ssd",
				},
			},
			expectedParams: &types.SetupInstanceParams{
				DiskSize:  100,
				DiskType:  "pd-ssd",
				VariantID: "v3",
			},
		},
		{
			name:          "apply all variant fields except zones",
			initialParams: &types.SetupInstanceParams{},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v4",
					MachineType: "c2-standard-8",
					Zones:       []string{"europe-west1-b"},
					DiskSize:    200,
					DiskType:    "pd-balanced",
				},
			},
			expectedParams: &types.SetupInstanceParams{
				MachineType: "c2-standard-8",
				DiskSize:    200,
				DiskType:    "pd-balanced",
				VariantID:   "v4",
			},
		},
		{
			name: "don't override with empty variant values",
			initialParams: &types.SetupInstanceParams{
				MachineType: "existing-type",
				DiskSize:    50,
			},
			variant: &types.PoolVariant{
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID:   "v5",
					MachineType: "", // Empty, should not override
					DiskSize:    0,
				},
			},
			expectedParams: &types.SetupInstanceParams{
				MachineType: "existing-type", // Preserved
				DiskSize:    50,              // Preserved
				VariantID:   "v5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyVariantToSetupParams(tt.initialParams, tt.variant)

			if tt.initialParams.MachineType != tt.expectedParams.MachineType {
				t.Errorf("MachineType: expected %s, got %s", tt.expectedParams.MachineType, tt.initialParams.MachineType)
			}
			if tt.initialParams.VariantID != tt.expectedParams.VariantID {
				t.Errorf("VariantID: expected %s, got %s", tt.expectedParams.VariantID, tt.initialParams.VariantID)
			}
			if tt.initialParams.DiskSize != tt.expectedParams.DiskSize {
				t.Errorf("DiskSize: expected %d, got %d", tt.expectedParams.DiskSize, tt.initialParams.DiskSize)
			}
			if tt.initialParams.DiskType != tt.expectedParams.DiskType {
				t.Errorf("DiskType: expected %s, got %s", tt.expectedParams.DiskType, tt.initialParams.DiskType)
			}
			if len(tt.initialParams.Zones) != len(tt.expectedParams.Zones) {
				t.Errorf("Zones length: expected %d, got %d", len(tt.expectedParams.Zones), len(tt.initialParams.Zones))
			} else {
				for i := range tt.initialParams.Zones {
					if tt.initialParams.Zones[i] != tt.expectedParams.Zones[i] {
						t.Errorf("Zones[%d]: expected %s, got %s", i, tt.expectedParams.Zones[i], tt.initialParams.Zones[i])
					}
				}
			}
		})
	}
}
