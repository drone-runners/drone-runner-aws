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
			name: "image filter no match returns nil when all variants pin a different image",
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
			expectedIDs: nil,
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

// googleMockDriver simulates the real Google driver behavior:
// - Empty ImageName → returns the pool's default image (not "")
// - Full image path (projects/...) → normalizes by stripping "projects/" prefix
// This is critical because the real Google driver always resolves to a concrete image.
type googleMockDriver struct {
	defaultImage string // pool-level default image (e.g., "projects/cie-hosted-vm-paid-prod/global/images/hosted-vm-64-kernel-6-8-0-1043-gcp")
}

func (g *googleMockDriver) GetFullyQualifiedImage(_ context.Context, config *types.VMImageConfig) (string, error) {
	if config == nil || config.ImageName == "" {
		return normalizeImage(g.defaultImage), nil
	}
	return normalizeImage(config.ImageName), nil
}

// normalizeImage strips the "projects/" prefix to mimic Google driver's normalizeImagePath
func normalizeImage(img string) string {
	parts := splitImage(img)
	if len(parts) == 5 && parts[0] == "projects" {
		return parts[1] + "/" + parts[2] + "/" + parts[3] + "/" + parts[4]
	}
	return img
}

func splitImage(img string) []string {
	var parts []string
	for _, p := range splitBySlash(img) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func splitBySlash(s string) []string {
	result := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func (g *googleMockDriver) Create(context.Context, *types.InstanceCreateOpts) (*types.Instance, error) {
	return nil, nil
}
func (g *googleMockDriver) Destroy(context.Context, []*types.Instance) ([]*types.Instance, error) {
	return nil, nil
}
func (g *googleMockDriver) Start(context.Context, *types.Instance, string) (string, error) {
	return "", nil
}
func (g *googleMockDriver) Hibernate(context.Context, string, string, string) error { return nil }
func (g *googleMockDriver) DestroyInstanceAndStorage(context.Context, []*types.Instance, *storage.CleanupType) ([]*types.Instance, error) {
	return nil, nil
}
func (g *googleMockDriver) Logs(context.Context, string) (string, error) { return "", nil }
func (g *googleMockDriver) Ping(context.Context) error                   { return nil }
func (g *googleMockDriver) ReserveCapacity(context.Context, *types.InstanceCreateOpts) (*types.CapacityReservation, error) {
	return nil, nil
}
func (g *googleMockDriver) DestroyCapacity(context.Context, *types.CapacityReservation) error {
	return nil
}
func (g *googleMockDriver) SetTags(context.Context, *types.Instance, map[string]string) error {
	return nil
}
func (g *googleMockDriver) RootDir() string    { return "/tmp" }
func (g *googleMockDriver) DriverName() string { return "google" }
func (g *googleMockDriver) CanHibernate() bool { return true }

// TestFilterVariants_Prod_LinuxAmd64 tests variant selection using real prod1 linux-amd64 pool config
// with a Google-driver-accurate mock that returns the pool default image for empty input.
func TestFilterVariants_Prod_LinuxAmd64(t *testing.T) {
	log := logrus.NewEntry(logrus.New())
	log.Logger.SetLevel(logrus.FatalLevel)
	ctx := logger.WithContext(context.Background(), logger.Logrus(log))

	const (
		poolDefaultImage = "projects/cie-hosted-vm-paid-prod/global/images/hosted-vm-64-kernel-6-8-0-1043-gcp"
		ubuntu24Image    = "projects/cie-hosted-vm-paid-prod/global/images/hosted-vm-ubuntu-2404-noble-amd64-v20250530"
	)

	// Real prod1 linux-amd64 pool variants (from runner-values.yaml)
	prodVariants := []types.PoolVariant{
		// --- flex ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex", ResourceClass: "flex", MachineType: "c4d-standard-8"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex_ubuntu24", ResourceClass: "flex", MachineType: "c4d-standard-8", ImageName: ubuntu24Image}},
		// --- small ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_small", ResourceClass: "small", MachineType: "c4d-standard-8"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_small_ubuntu24", ResourceClass: "small", MachineType: "c4d-standard-8", ImageName: ubuntu24Image}},
		// --- medium ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_medium", ResourceClass: "medium", MachineType: "c4d-standard-8"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_medium_ubuntu24", ResourceClass: "medium", MachineType: "c4d-standard-8", ImageName: ubuntu24Image}},
		// --- large ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", ResourceClass: "large", MachineType: "c4d-standard-16"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large_ubuntu24", ResourceClass: "large", MachineType: "c4d-standard-16", ImageName: ubuntu24Image}},
		// --- xlarge ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xlarge", ResourceClass: "xlarge", MachineType: "c4d-standard-32"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xlarge_ubuntu24", ResourceClass: "xlarge", MachineType: "c4d-standard-32", ImageName: ubuntu24Image}},
		// --- xxlarge ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxlarge", ResourceClass: "xxlarge", MachineType: "c4d-standard-64"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxlarge_ubuntu24", ResourceClass: "xxlarge", MachineType: "c4d-standard-64", ImageName: ubuntu24Image}},
		// --- xxxlarge ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxxlarge", ResourceClass: "xxxlarge", MachineType: "c4d-standard-96"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxxlarge_ubuntu24", ResourceClass: "xxxlarge", MachineType: "c4d-standard-96", ImageName: ubuntu24Image}},
		// --- flex_hw (nested virt) ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex_hw", ResourceClass: "flex", MachineType: "c2-standard-16", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex_hw_ubuntu24", ResourceClass: "flex", MachineType: "c2-standard-16", NestedVirtualization: true, ImageName: ubuntu24Image}},
		// --- small_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_small_hw", ResourceClass: "small", MachineType: "c2-standard-16", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_small_hw_ubuntu24", ResourceClass: "small", MachineType: "c2-standard-16", NestedVirtualization: true, ImageName: ubuntu24Image}},
		// --- medium_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_medium_hw", ResourceClass: "medium", MachineType: "c2-standard-16", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_medium_hw_ubuntu24", ResourceClass: "medium", MachineType: "c2-standard-16",
			NestedVirtualization: true, ImageName: ubuntu24Image,
		}},
		// --- large_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large_hw", ResourceClass: "large", MachineType: "c2-standard-30", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large_hw_ubuntu24", ResourceClass: "large", MachineType: "c2-standard-30", NestedVirtualization: true, ImageName: ubuntu24Image}},
		// --- xlarge_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xlarge_hw", ResourceClass: "xlarge", MachineType: "c2-standard-60", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_xlarge_hw_ubuntu24", ResourceClass: "xlarge", MachineType: "c2-standard-60",
			NestedVirtualization: true, ImageName: ubuntu24Image,
		}},
		// --- xxlarge_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxlarge_hw", ResourceClass: "xxlarge", MachineType: "c4-standard-96", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_xxlarge_hw_ubuntu24", ResourceClass: "xxlarge", MachineType: "c4-standard-96",
			NestedVirtualization: true, ImageName: ubuntu24Image,
		}},
		// --- xxxlarge_hw ---
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xxxlarge_hw", ResourceClass: "xxxlarge", MachineType: "c4-standard-96", NestedVirtualization: true}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_xxxlarge_hw_ubuntu24", ResourceClass: "xxxlarge", MachineType: "c4-standard-96",
			NestedVirtualization: true, ImageName: ubuntu24Image,
		}},
	}

	tests := []struct {
		name            string
		provisionParams *types.ProvisionParams
		expectedIDs     []string
		description     string // what happens and why
	}{
		// ===== NO IMAGE IN REQUEST (VMImageConfig is nil) =====
		{
			name: "medium, no image, no nested virt",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
			},
			// Google driver: empty ImageName → returns pool default image (hosted-vm-64-kernel...)
			// Step 2: fullyQualifiedImageName = pool default (non-empty!)
			// variant_medium has no image_name → noImageName bucket
			// variant_medium_ubuntu24 image ≠ pool default → not matched
			// matched=[], use noImageName → [variant_medium]
			// HOTPOOL: variant_medium with ImageName=pool_default
			expectedIDs: []string{"variant_medium"},
			description: "Only the no-image variant selected; hotpool query uses pool default image",
		},
		{
			name: "large, no image, nested virt",
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "large",
				NestedVirtualization: true,
			},
			expectedIDs: []string{"variant_large_hw"},
			description: "Only HW variant without image; hotpool query uses pool default image",
		},

		// ===== REQUEST WITH UBUNTU24 IMAGE =====
		{
			name: "medium, ubuntu24 image, no nested virt",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: ubuntu24Image,
				},
			},
			// Step 2: fullyQualifiedImageName = normalized ubuntu24
			// variant_medium has no image → noImageName
			// variant_medium_ubuntu24 image matches ubuntu24 → matched
			// matched=[variant_medium_ubuntu24] → use matched (preferred over noImageName)
			// HOTPOOL: variant_medium_ubuntu24 with ImageName=ubuntu24
			expectedIDs: []string{"variant_medium_ubuntu24"},
			description: "Ubuntu24 variant selected; hotpool query uses ubuntu24 image",
		},
		{
			name: "xlarge, ubuntu24 image, nested virt",
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "xlarge",
				NestedVirtualization: true,
				VMImageConfig: &spec.VMImageConfig{
					ImageName: ubuntu24Image,
				},
			},
			expectedIDs: []string{"variant_xlarge_hw_ubuntu24"},
			description: "HW ubuntu24 variant selected; hotpool query uses ubuntu24 image",
		},

		// ===== REQUEST WITH POOL DEFAULT IMAGE EXPLICITLY =====
		{
			name: "medium, explicit pool default image",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: poolDefaultImage,
				},
			},
			// Step 2: fullyQualifiedImageName = normalized pool default
			// variant_medium has no image → noImageName
			// variant_medium_ubuntu24 FQ ≠ pool default → not matched
			// matched=[], use noImageName → [variant_medium]
			expectedIDs: []string{"variant_medium"},
			description: "Explicit default image behaves same as no image; selects no-image variant",
		},

		// ===== REQUEST WITH UNKNOWN/THIRD-PARTY IMAGE =====
		{
			name: "medium, unknown third-party image (full path)",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "medium",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "projects/debian-cloud/global/images/debian-11-bullseye-v20250101",
				},
			},
			// Step 2: fullyQualifiedImageName = "debian-cloud/global/images/debian-11-bullseye-v20250101"
			// variant_medium has no image → noImageName
			// variant_medium_ubuntu24 FQ ≠ debian → not matched
			// matched=[], use noImageName → [variant_medium]
			// HOTPOOL: variant_medium with ImageName=debian image
			expectedIDs: []string{"variant_medium"},
			description: "Unknown image falls back to no-image variant; hotpool uses the requested image",
		},

		// ===== REQUEST WITH EMPTY VMImageConfig (ImageName="") =====
		{
			name: "small, empty VMImageConfig",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "small",
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "",
				},
			},
			// vmImageConfig is non-nil but ImageName is ""
			// imageConfig.ImageName = "" → Google driver returns pool default
			// Same as no-image case
			expectedIDs: []string{"variant_small"},
			description: "Empty image in VMImageConfig behaves same as nil VMImageConfig",
		},

		// ===== ALL RESOURCE CLASSES (no image, no nested virt) =====
		{
			name:            "flex, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "flex"},
			expectedIDs:     []string{"variant_flex"},
			description:     "Flex → variant_flex (hotpool with default image)",
		},
		{
			name:            "small, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "small"},
			expectedIDs:     []string{"variant_small"},
			description:     "Small → variant_small (hotpool with default image)",
		},
		{
			name:            "large, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "large"},
			expectedIDs:     []string{"variant_large"},
			description:     "Large → variant_large (hotpool with default image)",
		},
		{
			name:            "xlarge, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "xlarge"},
			expectedIDs:     []string{"variant_xlarge"},
			description:     "Xlarge → variant_xlarge (hotpool with default image)",
		},
		{
			name:            "xxlarge, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "xxlarge"},
			expectedIDs:     []string{"variant_xxlarge"},
			description:     "XXlarge → variant_xxlarge (hotpool with default image)",
		},
		{
			name:            "xxxlarge, no image",
			provisionParams: &types.ProvisionParams{ResourceClass: "xxxlarge"},
			expectedIDs:     []string{"variant_xxxlarge"},
			description:     "XXXlarge → variant_xxxlarge (hotpool with default image)",
		},

		// ===== NON-EXISTENT RESOURCE CLASS =====
		{
			name: "non-existent resource class",
			provisionParams: &types.ProvisionParams{
				ResourceClass: "micro",
			},
			expectedIDs: nil,
			description: "No matching variant → nil → uses default variant",
		},

		// ===== ALL HW RESOURCE CLASSES with ubuntu24 =====
		{
			name: "flex_hw, ubuntu24",
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "flex",
				NestedVirtualization: true,
				VMImageConfig:        &spec.VMImageConfig{ImageName: ubuntu24Image},
			},
			expectedIDs: []string{"variant_flex_hw_ubuntu24"},
			description: "Flex HW ubuntu24 → variant_flex_hw_ubuntu24",
		},
		{
			name: "medium_hw, ubuntu24",
			provisionParams: &types.ProvisionParams{
				ResourceClass:        "medium",
				NestedVirtualization: true,
				VMImageConfig:        &spec.VMImageConfig{ImageName: ubuntu24Image},
			},
			expectedIDs: []string{"variant_medium_hw_ubuntu24"},
			description: "Medium HW ubuntu24 → variant_medium_hw_ubuntu24",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &poolEntry{
				Pool: Pool{
					Name:         "linux-amd64",
					PoolVariants: prodVariants,
					Driver: &googleMockDriver{
						defaultImage: poolDefaultImage,
					},
				},
			}

			dm := &DistributedManager{}
			results := dm.filterVariants(ctx, pool, tt.provisionParams)

			if tt.expectedIDs == nil {
				if results != nil {
					t.Errorf("Expected nil result, got %d variants: %v", len(results), variantIDList(results))
				}
				t.Logf("→ %s", tt.description)
				return
			}

			if len(results) != len(tt.expectedIDs) {
				t.Fatalf("Expected %d variants %v, got %d variants %v\n→ %s",
					len(tt.expectedIDs), tt.expectedIDs, len(results), variantIDList(results), tt.description)
			}

			for i, expectedID := range tt.expectedIDs {
				if results[i].VariantID != expectedID {
					t.Errorf("Variant[%d]: expected %s, got %s", i, expectedID, results[i].VariantID)
				}
			}
			t.Logf("→ %s", tt.description)
		})
	}
}

func variantIDList(variants []*types.PoolVariant) []string {
	ids := make([]string, len(variants))
	for i, v := range variants {
		ids[i] = v.VariantID
	}
	return ids
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

func TestShouldReplenishInstance(t *testing.T) {
	tests := []struct {
		name   string
		source types.InstanceSource
		want   bool
	}{
		{
			name:   "pool source should replenish",
			source: types.InstanceSourcePool,
			want:   true,
		},
		{
			name:   "predictor source should not replenish",
			source: types.InstanceSourcePredictor,
			want:   false,
		},
		{
			name:   "ondemand source should not replenish",
			source: types.InstanceSourceOnDemand,
			want:   false,
		},
		{
			name:   "empty source should not replenish",
			source: "",
			want:   false,
		},
		{
			name:   "unknown source should not replenish",
			source: types.InstanceSourceUnknown,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := &types.Instance{Source: tt.source}
			got := shouldReplenishInstance(inst)
			if got != tt.want {
				t.Errorf("shouldReplenishInstance() = %v, want %v", got, tt.want)
			}
		})
	}
}
