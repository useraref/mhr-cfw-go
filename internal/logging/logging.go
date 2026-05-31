package logging

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

type Logger struct {
	name string
}

var (
	mu         sync.RWMutex
	globalLvl  = Info
	colorOn    = false
	outWriter  io.Writer = os.Stderr
	fileWriter io.Writer
)

func Configure(level string) {
	lvl := Info
	switch strings.ToUpper(level) {
	case "DEBUG":
		lvl = Debug
	case "WARNING", "WARN":
		lvl = Warn
	case "ERROR":
		lvl = Error
	default:
		lvl = Info
	}
	mu.Lock()
	globalLvl = lvl
	outWriter = os.Stderr
	colorOn = supportsColor(os.Stderr)
	mu.Unlock()
}

func ConfigureWithFile(level, logFilePath string) error {
	Configure(level)
	if logFilePath == "" {
		return nil
	}
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	mu.Lock()
	fileWriter = f
	outWriter = io.MultiWriter(os.Stderr, f)
	mu.Unlock()
	return nil
}

func Get(name string) *Logger {
	return &Logger{name: name}
}

func (l *Logger) Debugf(format string, args ...any) {
	l.log(Debug, format, args...)
}

func (l *Logger) Infof(format string, args ...any) {
	l.log(Info, format, args...)
}

func (l *Logger) Warnf(format string, args ...any) {
	l.log(Warn, format, args...)
}

func (l *Logger) Errorf(format string, args ...any) {
	l.log(Error, format, args...)
}

func (l *Logger) log(level Level, format string, args ...any) {
	mu.RLock()
	if level < globalLvl {
		mu.RUnlock()
		return
	}
	out := outWriter
	useColor := colorOn
	mu.RUnlock()

	now := time.Now()
	ts := now.Format("15:04:05")
	levelLabel := levelText(level)
	line := fmt.Sprintf(format, args...)
	component := l.name
	if len(component) > 8 {
		component = component[:8]
	}
	component = fmt.Sprintf("%-8s", component)

	if useColor {
		ts = color("90", ts)
		levelLabel = color(levelColor(level), levelLabel)
		component = color(componentColor(l.name), "["+component+"]")
	} else {
		component = "[" + component + "]"
	}

	fmt.Fprintf(out, "%s  %s  %s  %s\n", ts, levelLabel, component, line)
}

func levelText(level Level) string {
	switch level {
	case Debug:
		return "DBG"
	case Info:
		return "INF"
	case Warn:
		return "WRN"
	case Error:
		return "ERR"
	default:
		return "INF"
	}
}

func levelColor(level Level) string {
	switch level {
	case Debug:
		return "38;5;245"
	case Info:
		return "38;5;39"
	case Warn:
		return "38;5;214"
	case Error:
		return "38;5;203"
	default:
		return "38;5;39"
	}
}

func componentColor(name string) string {
	switch name {
	case "Main":
		return "38;5;81"
	case "Proxy":
		return "38;5;75"
	case "Fronter":
		return "38;5;141"
	case "H2":
		return "38;5;87"
	case "MITM":
		return "38;5;208"
	case "Cert":
		return "38;5;177"
	case "LAN":
		return "38;5;80"
	case "Scanner":
		return "38;5;45"
	default:
		return "38;5;245"
	}
}

func color(code, text string) string {
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func bold(s string) string   { return "\x1b[1m" + s + "\x1b[0m" }
func dim(s string) string    { return "\x1b[2m" + s + "\x1b[0m" }
func teal(s string) string   { return "\x1b[1;38;5;45m" + s + "\x1b[0m" }
func faint(s string) string  { return "\x1b[38;5;250m" + s + "\x1b[0m" }
func amber(s string) string  { return "\x1b[38;5;214m" + s + "\x1b[0m" }
func violet(s string) string { return "\x1b[38;5;141m" + s + "\x1b[0m" }

func supportsColor(stream *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("DFT_NO_COLOR") == "1" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("DFT_FORCE_COLOR") != "" {
		return true
	}
	info, err := stream.Stat()
	if err != nil || (info.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	return true
}

func PrintBanner(version string) {
	title := "MHR-CFW Go Version"
	subtitle := "Domain-Fronted Relay Suite"
	credit := "useraref"
	versionTag := "v" + version
	extraLine := "Internet for everyone or no one"

	innerWidth := max(76, max(len(title), max(len(subtitle), max(len(credit), len(extraLine))))+8)
	line := strings.Repeat("═", innerWidth)
	borderTop := "╔ " + line + " ╗"
	borderMid := "║" + strings.Repeat(" ", innerWidth) + "║"
	borderBot := "╚ " + line + " ╝"

	centerLine := func(text string) string {
		pad := innerWidth - len(text)
		left := pad / 2
		right := pad - left
		return "║" + strings.Repeat(" ", left) + text + strings.Repeat(" ", right) + "║"
	}

	mu.RLock()
	useColor := colorOn
	out := outWriter
	mu.RUnlock()

	fmt.Fprintln(out)
	fmt.Fprintln(out, borderTop)
	fmt.Fprintln(out, borderMid)

	if useColor {
		fmt.Fprintln(out, "║"+bold(teal(centerLine(title)))+"║")
		fmt.Fprintln(out, "║"+faint(centerLine(subtitle))+"║")
		fmt.Fprintln(out, "║"+amber(centerLine(versionTag))+"║")
		fmt.Fprintln(out, "║"+violet(centerLine(credit))+"║")
		// خط اضافی با رنگ معمولی یا faint
		fmt.Fprintln(out, "║"+faint(centerLine(extraLine))+"║")
	} else {
		fmt.Fprintln(out, centerLine(title))
		fmt.Fprintln(out, centerLine(subtitle))
		fmt.Fprintln(out, centerLine(versionTag))
		fmt.Fprintln(out, centerLine(credit))
		fmt.Fprintln(out, centerLine(extraLine))
	}

	fmt.Fprintln(out, borderMid)
	fmt.Fprintln(out, borderBot)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}