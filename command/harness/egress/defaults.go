package egress

import (
	"strings"

	"github.com/drone-runners/drone-runner-aws/types"
)

// DefaultEgressPolicy returns an EgressPolicy with the allowlist from DRONE_EGRESS_DEFAULT_IPS.
func DefaultEgressPolicy(egressDefaultIPs string) *types.EgressPolicy {
	var ips []string

	if egressDefaultIPs != "" {
		for _, ip := range strings.Split(egressDefaultIPs, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				ips = append(ips, ip)
			}
		}
	}

	return &types.EgressPolicy{
		Enabled:    true,
		AllowedIPs: ips,
	}
}
