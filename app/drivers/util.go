package drivers

import (
	"fmt"
	"runtime"
	"strings"
)

// harnessDownloadBase is the canonical Harness binary download origin. Under
// egress control + proxy, all binaries must be pulled from this single origin so
// the proxy allow-list only needs to permit one destination.
const harnessDownloadBase = "https://app.harness.io/storage/harness-download"

// callerPathDepth is the number of path components to keep when shortening file paths.
const callerPathDepth = 3

// getCallerInfo returns the file:line of the caller (skipping the specified number of frames).
func getCallerInfo(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	// Get just the last path components for readability
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
	return fmt.Sprintf("%s:%d", short, line)
}

func ShouldPerformDNSLookup(hosted bool, os string, warmedUp bool) bool {
	if hosted && (warmedUp || strings.EqualFold(os, "windows")) {
		return true
	}
	return false
}
