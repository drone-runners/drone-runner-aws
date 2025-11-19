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

func ShouldPerformDNSLookup(ctx context.Context, os string, warmedUp bool) bool {
	if IsHosted(ctx) && (warmedUp || strings.EqualFold(os, "windows")) {
		return true
	}
	return false
}
