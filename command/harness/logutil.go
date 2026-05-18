package harness

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
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

func useNonEmpty(imageConfigName, poolConfigImage string) string {
	if imageConfigName != "" {
		return imageConfigName
	}
	return lastPathSegment(poolConfigImage)
}

func lastPathSegment(s string) string {
	if s == "" {
		return s
	}
	s = strings.TrimSuffix(s, "/")
	idx := strings.LastIndex(s, "/")
	if idx == -1 {
		return s
	}
	return s[idx+1:]
}

func usePlainFormatter(l *logrus.Logger) { l.SetFormatter(&plainFormatter{}) }

// logRequestedMachine prints the standard "Requested machine" block with
// size, OS, Arch, image, and nested virtualization flag.
//
// The image shown follows this priority:
//  1. imageVersion (from VMImageConfig.ImageVersion)
//  2. imageName    (from VMImageConfig.ImageName)
//  3. pool image   (derived from the pool YAML config)
func logRequestedMachine(logr *logrus.Entry, poolManager drivers.IManager, poolID string, platform *types.Platform, resourceClass, imageVersion, imageName, stageRuntimeID string, isNested bool) {
	printTitle(logr, "Requested machine:")
	printKV(logr, "Machine Size", resourceClass)
	printKV(logr, "OS", capitalize(platform.OS))
	printKV(logr, "Arch", capitalize(platform.Arch))
	printKV(logr, "Image Version", resolveImageForLog(poolManager, poolID, imageVersion, imageName))
	printKV(logr, "Hardware Acceleration (Nested Virtualization)", isNested)
	printKV(logr, "Stage Runtime ID", stageRuntimeID)
}

// resolveImageForLog picks the image identifier to log, preferring the
// explicit imageVersion, then imageName, then the pool's configured image
// (with its last path segment extracted for readability).
func resolveImageForLog(poolManager drivers.IManager, poolID, imageVersion, imageName string) string {
	if imageVersion != "" {
		return imageVersion
	}
	if imageName != "" {
		return imageName
	}
	return lastPathSegment(derivePoolImageForLog(poolManager, poolID))
}

// derivePoolImageForLog extracts an image identifier from the pool YAML config for logging purposes.
// It returns an empty string if the pool config is missing or does not match a known driver type.
func derivePoolImageForLog(poolManager drivers.IManager, pool string) string {
	spec, err := poolManager.GetPoolSpec(pool)
	if err != nil || spec == nil {
		return ""
	}
	if g, ok := spec.(*config.Google); ok {
		return g.Image
	}
	if d, ok := spec.(*config.DigitalOcean); ok {
		return d.Image
	}
	if az, ok := spec.(*config.Azure); ok {
		if az.Image.Version != "" {
			return az.Image.Version
		} else if az.Image.SKU != "" {
			return az.Image.SKU
		} else if az.Image.Offer != "" {
			return az.Image.Offer
		}
		return ""
	}
	if a, ok := spec.(*config.Amazon); ok {
		return a.AMI
	}
	if n, ok := spec.(*config.Nomad); ok {
		return n.VM.Image
	}
	return ""
}
