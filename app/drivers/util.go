package drivers

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/drone-runners/drone-runner-aws/types"
)

// callerPathDepth is the number of path components to keep when shortening file paths.
const callerPathDepth = 3

// getCallerInfo returns the file:line of the caller (skipping the specified number of frames).
func getCallerInfo(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", shortCallerPath(file), line)
}

func shortCallerPath(file string) string {
	// Get just the last path components for readability
	file = strings.ReplaceAll(filepath.ToSlash(file), "\\", "/")
	short := file
	count := 0
	for i := len(file) - 1; i >= 0; i-- {
		if file[i] == '/' {
			count++
			if count == callerPathDepth {
				short = file[i+1:]
				break
			}
		}
	}
	return short
}

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
