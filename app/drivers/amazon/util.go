// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package amazon

import (
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	drtypes "github.com/drone-runners/drone-runner-aws/types"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// helper function converts an array of tags in string
// format to an array of ec2 tags.
func convertTags(in map[string]string) []types.Tag {
	var out []types.Tag
	for k, v := range in {
		out = append(out, types.Tag{
			Key:   aws.String(k),
			Value: aws.String(v),
		})
	}
	return out
}

// buildHarnessTags creates common harness tags from InstanceCreateOpts
func buildHarnessTags(opts *drtypes.InstanceCreateOpts) map[string]string {
	if opts == nil {
		return map[string]string{}
	}
	return map[string]string{
		"harness-account-id":     opts.AccountID,
		"harness-pool-name":      opts.PoolName,
		"harness-runner-name":    opts.RunnerName,
		"harness-resource-class": opts.ResourceClass,
		"harness-platform-os":    opts.Platform.OS,
		"harness-platform-arch":  opts.Platform.Arch,
	}
}

// helper function returns the base temporary directory based on the target platform.
func tempdir(inputOS string) string {
	const dir = "aws"

	switch inputOS {
	case oshelp.OSWindows:
		return oshelp.JoinPaths(inputOS, "C:\\Windows\\Temp", dir)
	default:
		return oshelp.JoinPaths(inputOS, "/tmp", dir)
	}
}
