package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func RunInteractiveWizard(configPath string) error {
	cfg := loadBaseConfig()

	reader := bufio.NewReader(os.Stdin)
	ui := newWizardUI()

	ui.Space()
	ui.Title("mhr-cfw setup")
	ui.Subtitle("Guided configuration for the local relay proxy")
	ui.Space()

	if _, err := os.Stat(configPath); err == nil {
		if !promptYesNo(reader, ui, "config.json already exists. Overwrite?", false) {
			ui.Muted("Nothing changed.")
			return nil
		}
	}

	ui.Section("Shared password")
	ui.Muted("Must match AUTH_KEY inside apps_script/Code.gs")
	cfg["auth_key"] = prompt(reader, ui, "auth_key", randomAuthKey(32))

	cfg = configureAppsScript(reader, cfg, ui)
	cfg = configureNetwork(reader, cfg, ui)

	if err := writeConfig(configPath, cfg, ui); err != nil {
		return err
	}

	ui.Space()
	ui.Ok("wrote " + filepath.Base(configPath))
	ui.Space()
	ui.Section("Next step")
	ui.Code("mhr-cfw")
	ui.Space()
	ui.Warn("AUTH_KEY inside apps_script/Code.gs must match the auth_key you entered")
	return nil
}

type wizardUI struct {
	color bool
}

func newWizardUI() *wizardUI {
	color := supportsColor()
	return &wizardUI{color: color}
}

func (w *wizardUI) Space() {
	fmt.Println()
}

func (w *wizardUI) Title(text string) {
	line := strings.Repeat("─", max(48, len(text)+12))
	if w.color {
		fmt.Println(dim(line))
		fmt.Println(bold(cyan("  " + text + "  ")))
		fmt.Println(dim(line))
		return
	}
	fmt.Println(line)
	fmt.Println("  " + text)
	fmt.Println(line)
}

func (w *wizardUI) Subtitle(text string) {
	if w.color {
		fmt.Println(dim(text))
		return
	}
	fmt.Println(text)
}

func (w *wizardUI) Section(text string) {
	if w.color {
		fmt.Println(bold(cyan(text)))
		return
	}
	fmt.Println(text)
}

func (w *wizardUI) Step(n int, text string) {
	label := fmt.Sprintf("%d.", n)
	if w.color {
		fmt.Println(dim(label), text)
		return
	}
	fmt.Println(label, text)
}

func (w *wizardUI) Code(text string) {
	if w.color {
		fmt.Println(dim("  $"), bold(text))
		return
	}
	fmt.Println("  $", text)
}

func (w *wizardUI) Prompt(question, hint string) {
	if hint != "" {
		if w.color {
			fmt.Printf("%s %s %s: ", cyan("?"), question, dim("["+hint+"]"))
			return
		}
		fmt.Printf("? %s [%s]: ", question, hint)
		return
	}
	if w.color {
		fmt.Printf("%s %s: ", cyan("?"), question)
		return
	}
	fmt.Printf("? %s: ", question)
}

func (w *wizardUI) Ok(text string) {
	if w.color {
		fmt.Println(green("[OK]"), text)
		return
	}
	fmt.Println("[OK]", text)
}

func (w *wizardUI) Warn(text string) {
	if w.color {
		fmt.Println(yellow("!"), text)
		return
	}
	fmt.Println("!", text)
}

func (w *wizardUI) Error(text string) {
	if w.color {
		fmt.Println(red("!"), text)
		return
	}
	fmt.Println("!", text)
}

func (w *wizardUI) Muted(text string) {
	if w.color {
		fmt.Println(dim(text))
		return
	}
	fmt.Println(text)
}

func supportsColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("DFT_NO_COLOR") == "1" {
		return false
	}
	if !isTTY(os.Stdout) {
		return false
	}
	return true
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func bold(s string) string { return "\x1b[1m" + s + "\x1b[0m" }
func dim(s string) string { return "\x1b[2m" + s + "\x1b[0m" }
func cyan(s string) string { return "\x1b[36m" + s + "\x1b[0m" }
func green(s string) string { return "\x1b[32m" + s + "\x1b[0m" }
func yellow(s string) string { return "\x1b[33m" + s + "\x1b[0m" }
func red(s string) string { return "\x1b[31m" + s + "\x1b[0m" }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func loadBaseConfig() map[string]any {
	return map[string]any{
		"mode":                        "apps_script",
		"google_ip":                   "216.239.38.120",
		"front_domain":                "www.google.com",
		"listen_host":                 "127.0.0.1",
		"listen_port":                 8085,
		"socks5_enabled":              true,
		"socks5_port":                 1080,
		"log_level":                   "INFO",
		"verify_ssl":                  true,
		"lan_sharing":                 false,
		"relay_timeout":               25,
		"tls_connect_timeout":         15,
		"tcp_connect_timeout":         10,
		"max_response_body_bytes":     200 * 1024 * 1024,
		"chunked_download_min_size":   5 * 1024 * 1024,
		"chunked_download_chunk_size": 512 * 1024,
		"chunked_download_max_parallel": 8,
		"chunked_download_max_chunks":   256,
		"hosts":                       map[string]string{},
	}
}

func configureAppsScript(r *bufio.Reader, cfg map[string]any, ui *wizardUI) map[string]any {
	ui.Section("Google Apps Script setup")
	ui.Step(1, "Open https://script.google.com -> New project")
	ui.Step(2, "Paste apps_script/Code.gs from this repo into the editor")
	ui.Step(3, "Set AUTH_KEY in Code.gs to the password above")
	ui.Step(4, "Deploy -> New deployment -> Web app")
	ui.Step(5, "Execute as: Me   |   Who has access: Anyone")
	ui.Step(6, "Copy the Deployment ID and paste it here")
	ui.Space()

	idsRaw := prompt(r, ui, "Deployment ID(s) - comma-separated for load balancing", "")
	ids := []string{}
	for _, v := range strings.Split(idsRaw, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			ids = append(ids, v)
		}
	}
	if len(ids) == 1 {
		cfg["script_id"] = ids[0]
		delete(cfg, "script_ids")
	} else if len(ids) > 1 {
		cfg["script_ids"] = ids
		delete(cfg, "script_id")
	}
	return cfg
}

func configureNetwork(r *bufio.Reader, cfg map[string]any, ui *wizardUI) map[string]any {
	ui.Section("Network settings")
	ui.Muted("Press enter to accept defaults")
	ui.Space()

	lanSharing := promptYesNo(r, ui, "Enable LAN sharing?", boolVal(cfg["lan_sharing"]))
	cfg["lan_sharing"] = lanSharing

	defaultHost := strVal(cfg["listen_host"])
	if lanSharing && defaultHost == "127.0.0.1" {
		defaultHost = "0.0.0.0"
	}
	cfg["listen_host"] = prompt(r, ui, "Listen host", defaultHost)

	port := prompt(r, ui, "HTTP proxy port", fmt.Sprintf("%v", cfg["listen_port"]))
	cfg["listen_port"] = toInt(port, 8085)

	socks := promptYesNo(r, ui, "Enable SOCKS5 proxy?", boolVal(cfg["socks5_enabled"]))
	cfg["socks5_enabled"] = socks
	if socks {
		sport := prompt(r, ui, "SOCKS5 port", fmt.Sprintf("%v", cfg["socks5_port"]))
		cfg["socks5_port"] = toInt(sport, 1080)
	}
	return cfg
}

func writeConfig(path string, cfg map[string]any, ui *wizardUI) error {
	if _, err := os.Stat(path); err == nil {
		backup := strings.TrimSuffix(path, ".json") + ".json.bak"
		_ = copyFile(path, backup)
		ui.Muted("existing config.json backed up to " + filepath.Base(backup))
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

func prompt(r *bufio.Reader, ui *wizardUI, question, def string) string {
	for {
		if def != "" {
			ui.Prompt(question, def)
		} else {
			ui.Prompt(question, "")
		}
		raw, _ := r.ReadString('\n')
		raw = strings.TrimSpace(raw)
		if raw == "" && def != "" {
			return def
		}
		if raw != "" {
			return raw
		}
		ui.Error("value required")
	}
}

func promptYesNo(r *bufio.Reader, ui *wizardUI, question string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	for {
		ui.Prompt(question, hint)
		raw, _ := r.ReadString('\n')
		raw = strings.TrimSpace(strings.ToLower(raw))
		if raw == "" {
			return def
		}
		if raw == "y" || raw == "yes" {
			return true
		}
		if raw == "n" || raw == "no" {
			return false
		}
	}
}

func randomAuthKey(length int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	seed := time.Now().UnixNano()
	for i := range out {
		seed = (seed*1664525 + 1013904223) & 0x7fffffff
		out[i] = alphabet[int(seed)%len(alphabet)]
	}
	return string(out)
}

func boolVal(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(s string, def int) int {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return i
}
