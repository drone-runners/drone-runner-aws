package drivers

import (
	"context"
	"strings"

	"github.com/drone-runners/drone-runner-aws/types"
)

func IsHosted(ctx context.Context) bool {
	isHosted := ctx.Value(types.Hosted)
	if isHosted == nil {
		return false
	}
	return isHosted.(bool)
}

func ShouldPerformDNSLookup(ctx context.Context, os string) bool {
	if strings.EqualFold(os, "windows") && IsHosted(ctx) {
		return true
	}
	return false
}
