package egress

import (
	"github.com/drone-runners/drone-runner-aws/types"
)

// DefaultEgressPolicy returns an EgressPolicy with the allowlist from DRONE_EGRESS_DEFAULT_IPS.
func DefaultEgressPolicy(egressDefaultIPs []string) *types.EgressPolicy {
	return &types.EgressPolicy{
		Enabled:    true,
		AllowedIPs: egressDefaultIPs,
	}
}
