package drivers

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/types"
)

func isHosted(ctx context.Context) bool {
	isHosted := ctx.Value(types.Hosted)
	if isHosted == nil {
		return false
	}
	return isHosted.(bool)
}
