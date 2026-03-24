// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"testing"

	"github.com/harness/lite-engine/engine/spec"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestDeepCopySetupParams(t *testing.T) {
	tests := []struct {
		name   string
		params *types.SetupInstanceParams
		isNil  bool
	}{
		{
			name:   "nil params returns nil",
			params: nil,
			isNil:  true,
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
		},
		{
			name: "conversion with zones - deep copy",
			params: &types.SetupInstanceParams{
				MachineType:   "t3.medium",
				ResourceClass: "small",
				Zones:         []string{"us-east-1a", "us-east-1b"},
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := deepCopySetupParams(tt.params)

			if tt.isNil {
				if result != nil {
					t.Errorf("Expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected non-nil result, got nil")
				return
			}

			if result.MachineType != tt.params.MachineType {
				t.Errorf("MachineType: expected %s, got %s", tt.params.MachineType, result.MachineType)
			}
			if result.NestedVirtualization != tt.params.NestedVirtualization {
				t.Errorf("NestedVirtualization: expected %v, got %v", tt.params.NestedVirtualization, result.NestedVirtualization)
			}
			if result.Hibernate != tt.params.Hibernate {
				t.Errorf("Hibernate: expected %v, got %v", tt.params.Hibernate, result.Hibernate)
			}
			if result.VariantID != tt.params.VariantID {
				t.Errorf("VariantID: expected %s, got %s", tt.params.VariantID, result.VariantID)
			}
			if result.ResourceClass != tt.params.ResourceClass {
				t.Errorf("ResourceClass: expected %s, got %s", tt.params.ResourceClass, result.ResourceClass)
			}
			if result.DiskSize != tt.params.DiskSize {
				t.Errorf("DiskSize: expected %d, got %d", tt.params.DiskSize, result.DiskSize)
			}
			if result.DiskType != tt.params.DiskType {
				t.Errorf("DiskType: expected %s, got %s", tt.params.DiskType, result.DiskType)
			}
			if result.ImageName != tt.params.ImageName {
				t.Errorf("ImageName: expected %s, got %s", tt.params.ImageName, result.ImageName)
			}

			if len(result.Zones) != len(tt.params.Zones) {
				t.Errorf("Zones length: expected %d, got %d", len(tt.params.Zones), len(result.Zones))
			} else {
				for i := range result.Zones {
					if result.Zones[i] != tt.params.Zones[i] {
						t.Errorf("Zones[%d]: expected %s, got %s", i, tt.params.Zones[i], result.Zones[i])
					}
				}
			}
		})
	}
}

func TestVMImageConfigFromSetupParams(t *testing.T) {
	tests := []struct {
		name     string
		params   *types.SetupInstanceParams
		expected *spec.VMImageConfig
	}{
		{
			name:     "nil params returns nil",
			params:   nil,
			expected: nil,
		},
		{
			name: "empty image name returns nil",
			params: &types.SetupInstanceParams{
				ImageName: "",
			},
			expected: nil,
		},
		{
			name: "image name creates VMImageConfig",
			params: &types.SetupInstanceParams{
				ImageName: "ubuntu-22.04",
			},
			expected: &spec.VMImageConfig{
				ImageName: "ubuntu-22.04",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := vmImageConfigFromSetupParams(tt.params)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("Expected nil, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Errorf("Expected non-nil, got nil")
				return
			}

			if result.ImageName != tt.expected.ImageName {
				t.Errorf("ImageName: expected %s, got %s", tt.expected.ImageName, result.ImageName)
			}
		})
	}
}

func TestDeepCopySetupParams_ZonesImmutability(t *testing.T) {
	// Test that modifying the result's Zones slice doesn't affect the original params
	params := &types.SetupInstanceParams{
		Zones: []string{"zone-a", "zone-b"},
	}

	result := deepCopySetupParams(params)

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
