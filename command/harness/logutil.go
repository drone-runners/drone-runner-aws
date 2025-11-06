package harness

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/sirupsen/logrus"
)

const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
)

func capitalize(s string) string {
	if len(s) > 0 {
		return string(unicode.ToUpper(rune(s[0]))) + s[1:]
	}
	return s
}

func colorize(s, color string) string { return color + s + colorReset }

func printTitle(log *logrus.Entry, text string) { log.Infoln(colorize(text, colorYellow)) }

func printOK(log *logrus.Entry, text string) { log.Infoln(colorize("✓ "+text, colorGreen)) }

func printError(log *logrus.Entry, text string) { log.Infoln(colorize("✗ "+text, colorRed)) }

func printKV(log *logrus.Entry, key string, value any) {
	log.Infoln(fmt.Sprintf("%s: %v", key, value))
}

type plainFormatter struct{}

func (f *plainFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	msg := entry.Message
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	return []byte(msg), nil
}

func logImageOsVersionInfo(imageConfigName, imageOs string) string {
	if imageConfigName != "" {
		return imageConfigName
	}
	return imageOs + "-latest"
}

func usePlainFormatter(l *logrus.Logger) { l.SetFormatter(&plainFormatter{}) }

// logRequestedMachine prints the standard "Requested machine" block with
// size, OS, Arch, image, and nested virtualization flag.
func logRequestedMachine(logr *logrus.Entry, poolManager drivers.IManager, poolID string, platform *types.Platform, resourceClass, imageVersion string) {
	printTitle(logr, "Requested machine:")
	printKV(logr, "Machine Size", resourceClass)
	printKV(logr, "OS", capitalize(platform.OS))
	printKV(logr, "Arch", capitalize(platform.Arch))
	printKV(logr, "Image Version", logImageOsVersionInfo(imageVersion, platform.OS))
	nvFromConfig := deriveEnableNestedVirtualization(poolManager, poolID)
	printKV(logr, "Hardware Acceleration (Nested Virtualization)", nvFromConfig)
}

// deriveEnableNestedVirtualization reads the nested virtualization flag from the pool YAML config.
// Returns false if not set or not applicable for the provider.
func deriveEnableNestedVirtualization(poolManager drivers.IManager, pool string) bool {
	spec, err := poolManager.GetPoolSpec(pool)
	if err != nil || spec == nil {
		return false
	}
	if g, ok := spec.(*config.Google); ok {
		return g.EnableNestedVirtualization
	}
	return false
}
