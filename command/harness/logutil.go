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

func useNonEmpty(ImageConfigName, PoolConfigImage string) string {
	if ImageConfigName != "" {
		return ImageConfigName
	}
	return lastPathSegment(PoolConfigImage)
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
