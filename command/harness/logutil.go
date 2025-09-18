package harness

import (
	"fmt"
	"strings"
	"unicode"

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

func usePlainFormatter(l *logrus.Logger) { l.SetFormatter(&plainFormatter{}) }

func defaultString(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func defaultOsImages(osName, arch string) (string, string) {
	osName = strings.ToLower(osName)
	arch = strings.ToLower(arch)

	if osName == "linux" && (arch == "amd64" || arch == "arm64") {
		return "ubuntu-latest", "ubuntu-22"
	} else if osName == "windows" && arch == "amd64" {
		return "windows-latest", "windows-19"
	} else if osName == "darwin" && arch == "arm64" {
		return "mac-latest", "sonoma-latest"
	}
	return "", ""
}
