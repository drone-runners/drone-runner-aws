// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"testing"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestSetupInstanceParamsToMachineConfig(t *testing.T) {
	tests := []struct {
		name     string
		params   *types.SetupInstanceParams
		expected *types.MachineConfig
	}{
		{
			name:     "nil params returns nil",
			params:   nil,
			expected: nil,
		},
		{
			name: "basic conversion without zones",
			params: &types.SetupInstanceParams{
				MachineType:          "n2-standard-4",
				NestedVirtualization: true,
				Hibernate:            false,
				VariantID:            "v1",
				ResourceClass:        "medium",
				DiskSize:             100,
				DiskType:             "pd-ssd",
			},
			expected: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType:          "n2-standard-4",
					NestedVirtualization: true,
					Hibernate:            false,
					VariantID:            "v1",
					ResourceClass:        "medium",
					DiskSize:             100,
					DiskType:             "pd-ssd",
				},
			},
		},
		{
			name: "conversion with zones - deep copy",
			params: &types.SetupInstanceParams{
				MachineType:   "t3.medium",
				ResourceClass: "small",
				Zones:         []string{"us-east-1a", "us-east-1b"},
			},
			expected: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					MachineType:   "t3.medium",
					ResourceClass: "small",
					Zones:         []string{"us-east-1a", "us-east-1b"},
				},
			},
		},
		{
			name: "conversion with image name creates VMImageConfig",
			params: &types.SetupInstanceParams{
				ImageName:     "ubuntu-22.04",
				MachineType:   "c2-standard-8",
				ResourceClass: "large",
			},
			expected: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ImageName:     "ubuntu-22.04",
					MachineType:   "c2-standard-8",
					ResourceClass: "large",
				},
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "ubuntu-22.04",
				},
			},
		},
		{
			name: "conversion with all fields populated",
			params: &types.SetupInstanceParams{
				ImageName:            "custom-image",
				NestedVirtualization: true,
				MachineType:          "e2-highcpu-16",
				Hibernate:            true,
				Zones:                []string{"europe-west1-a", "europe-west1-b", "europe-west1-c"},
				VariantID:            "premium-variant",
				DiskSize:             500,
				DiskType:             "pd-extreme",
				ResourceClass:        "xlarge",
			},
			expected: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ImageName:            "custom-image",
					NestedVirtualization: true,
					MachineType:          "e2-highcpu-16",
					Hibernate:            true,
					Zones:                []string{"europe-west1-a", "europe-west1-b", "europe-west1-c"},
					VariantID:            "premium-variant",
					DiskSize:             500,
					DiskType:             "pd-extreme",
					ResourceClass:        "xlarge",
				},
				VMImageConfig: &spec.VMImageConfig{
					ImageName: "custom-image",
				},
			},
		},
		{
			name: "empty image name does not create VMImageConfig",
			params: &types.SetupInstanceParams{
				ImageName:     "",
				MachineType:   "t2.micro",
				ResourceClass: "tiny",
			},
			expected: &types.MachineConfig{
				SetupInstanceParams: types.SetupInstanceParams{
					ImageName:     "",
					MachineType:   "t2.micro",
					ResourceClass: "tiny",
				},
				VMImageConfig: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{}
			result := m.setupInstanceParamsToMachineConfig(tt.params)

			// Check nil case
			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected non-nil result, got nil")
				return
			}

			// Check SetupInstanceParams fields
			if result.MachineType != tt.expected.MachineType {
				t.Errorf("MachineType: expected %s, got %s", tt.expected.MachineType, result.MachineType)
			}
			if result.NestedVirtualization != tt.expected.NestedVirtualization {
				t.Errorf("NestedVirtualization: expected %v, got %v", tt.expected.NestedVirtualization, result.NestedVirtualization)
			}
			if result.Hibernate != tt.expected.Hibernate {
				t.Errorf("Hibernate: expected %v, got %v", tt.expected.Hibernate, result.Hibernate)
			}
			if result.VariantID != tt.expected.VariantID {
				t.Errorf("VariantID: expected %s, got %s", tt.expected.VariantID, result.VariantID)
			}
			if result.ResourceClass != tt.expected.ResourceClass {
				t.Errorf("ResourceClass: expected %s, got %s", tt.expected.ResourceClass, result.ResourceClass)
			}
			if result.DiskSize != tt.expected.DiskSize {
				t.Errorf("DiskSize: expected %d, got %d", tt.expected.DiskSize, result.DiskSize)
			}
			if result.DiskType != tt.expected.DiskType {
				t.Errorf("DiskType: expected %s, got %s", tt.expected.DiskType, result.DiskType)
			}
			if result.ImageName != tt.expected.ImageName {
				t.Errorf("ImageName: expected %s, got %s", tt.expected.ImageName, result.ImageName)
			}

			// Check Zones (deep copy)
			if len(result.Zones) != len(tt.expected.Zones) {
				t.Errorf("Zones length: expected %d, got %d", len(tt.expected.Zones), len(result.Zones))
			} else {
				for i := range result.Zones {
					if result.Zones[i] != tt.expected.Zones[i] {
						t.Errorf("Zones[%d]: expected %s, got %s", i, tt.expected.Zones[i], result.Zones[i])
					}
				}
			}

			// Check VMImageConfig
			if tt.expected.VMImageConfig == nil {
				if result.VMImageConfig != nil {
					t.Errorf("VMImageConfig: expected nil, got %+v", result.VMImageConfig)
				}
			} else {
				if result.VMImageConfig == nil {
					t.Errorf("VMImageConfig: expected non-nil, got nil")
				} else if result.VMImageConfig.ImageName != tt.expected.VMImageConfig.ImageName {
					t.Errorf("VMImageConfig.ImageName: expected %s, got %s", tt.expected.VMImageConfig.ImageName, result.VMImageConfig.ImageName)
				}
			}
		})
	}
}

func TestSetupInstanceParamsToMachineConfig_ZonesImmutability(t *testing.T) {
	// Test that modifying the result's Zones slice doesn't affect the original params
	params := &types.SetupInstanceParams{
		Zones: []string{"zone-a", "zone-b"},
	}

	m := &Manager{}
	result := m.setupInstanceParamsToMachineConfig(params)

	// Modify result's zones
	if len(result.Zones) > 0 {
		result.Zones[0] = "modified-zone"
	}

	// Original should be unchanged
	if params.Zones[0] != "zone-a" {
		t.Errorf("Original params.Zones was modified! Expected 'zone-a', got '%s'", params.Zones[0])
	}

	// Result should have the modification
	if result.Zones[0] != "modified-zone" {
		t.Errorf("Result.Zones was not modified! Expected 'modified-zone', got '%s'", result.Zones[0])
	}
}
