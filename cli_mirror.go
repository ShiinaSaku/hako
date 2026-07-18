package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ---------- CLI progress bar ----------

type progBar struct {
	total int
	label string
	width int
	tty   bool
}

func newProgBar(total int, label string) *progBar {
	return &progBar{total: total, label: label, width: 30, tty: isTTY()}
}

func (b *progBar) update(done int) {
	if !b.tty || b.total == 0 {
		return
	}
	ratio := float64(done) / float64(b.total)
	filled := int(ratio * float64(b.width))
	if filled > b.width {
		filled = b.width
	}
	bar := lipgloss.NewStyle().Foreground(cPink).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(cFaint).Render(strings.Repeat("░", b.width-filled))
	pct := fmt.Sprintf(" %3.0f%% (%d/%d)", ratio*100, done, b.total)
	fmt.Printf("\r%s %s%s", b.label, bar, dimStyle.Render(pct))
}

func (b *progBar) finish() {
	if b.tty {
		fmt.Print("\r\033[K")
	}
}

// ---------- mirror command ----------

func cmdMirror(args []string, cfg *Config) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "current", "status":
		cur := currentMirror()
		if cur == "" {
			warn("no mirror configured")
			return 1
		}
		fmt.Printf("%s %s\n", labelStyle.Render("current mirror:"), keyStyle.Render(cur))
		return 0
	case "list", "ls":
		mirrors := loadMirrors()
		cur := currentMirror()
		for _, m := range mirrors {
			mark := " "
			if strings.TrimRight(m.Main, "/") == strings.TrimRight(cur, "/") {
				mark = installedBadge.Render("★")
			}
			fmt.Printf("%s %s %s %s\n", mark,
				accentSty.Render(padRight(truncate(m.Name, 30), 30)),
				dimStyle.Render(padRight(m.Region, 16)),
				dimStyle.Render(m.Main))
		}
		return 0
	case "bench", "benchmark", "test":
		mirrors := benchmarkAllCLI(cfg)
		printMirrorTable(mirrors)
		return 0
	case "auto":
		return autoSelectMirror(cfg)
	case "set", "use":
		if len(args) < 2 {
			fail("usage: hako mirror set <name|url>")
			return 1
		}
		return setMirror(args[1], cfg)
	default:
		fail("unknown mirror subcommand %q", sub)
		return 1
	}
}

func benchmarkAllCLI(cfg *Config) []Mirror {
	mirrors := loadMirrors()
	if cfg.PreferredRegion != "" {
		var filtered []Mirror
		for _, m := range mirrors {
			if m.Region == cfg.PreferredRegion || m.Region == "default" {
				filtered = append(filtered, m)
			}
		}
		if len(filtered) > 0 {
			mirrors = filtered
		}
	}
	step("benchmarking %d mirrors (timeout %ds)…", len(mirrors), cfg.BenchTimeout)
	bar := newProgBar(len(mirrors), "  ")
	result := benchAll(mirrors, cfg.BenchTimeout, 12, func(done, total int) {
		bar.update(done)
	})
	bar.finish()
	return result
}

func printMirrorTable(mirrors []Mirror) {
	shown := 0
	for _, m := range mirrors {
		if m.LatencyMS < 0 {
			continue
		}
		shown++
		rank := dimStyle.Render(padLeft(fmt.Sprintf("%d.", shown), 3))
		name := accentSty.Render(padRight(truncate(m.Name, 30), 30))
		speed := speedColor(m.SpeedKBps).Render(fmt.Sprintf("%8.0f KB/s", m.SpeedKBps))
		lat := dimStyle.Render(fmt.Sprintf("%5dms", m.LatencyMS))
		fmt.Printf("%s %s %s %s\n", rank, name, speed, lat)
	}
	if shown == 0 {
		warn("no mirrors reachable")
	}
}

func autoSelectMirror(cfg *Config) int {
	mirrors := benchmarkAllCLI(cfg)
	var best *Mirror
	for i := range mirrors {
		if mirrors[i].LatencyMS >= 0 {
			best = &mirrors[i]
			break
		}
	}
	if best == nil {
		fail("no reachable mirrors found")
		return 1
	}
	if err := applyMirror(*best); err != nil {
		fail("failed to write sources.list: %v", err)
		return 1
	}
	cfg.Mirror = strings.TrimRight(best.Main, "/")
	_ = saveConfig(cfg)
	ok("fastest mirror set → %s  (%.0f KB/s, %dms)", best.Main, best.SpeedKBps, best.LatencyMS)
	return 0
}

func setMirror(arg string, cfg *Config) int {
	var chosen *Mirror
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		chosen = &Mirror{Name: arg, Main: arg}
	} else {
		mirrors := loadMirrors()
		la := strings.ToLower(arg)
		for i := range mirrors {
			if strings.Contains(strings.ToLower(mirrors[i].Name), la) ||
				strings.Contains(strings.ToLower(mirrors[i].Main), la) {
				chosen = &mirrors[i]
				break
			}
		}
	}
	if chosen == nil {
		fail("no mirror matching %q", arg)
		return 1
	}
	if err := applyMirror(*chosen); err != nil {
		fail("failed to write sources.list: %v", err)
		return 1
	}
	cfg.Mirror = strings.TrimRight(chosen.Main, "/")
	_ = saveConfig(cfg)
	ok("mirror set → %s", chosen.Main)
	return 0
}

// ---------- config command ----------

func cmdConfig(args []string, cfg *Config) int {
	sub := "get"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "path":
		fmt.Println(configPath())
		return 0
	case "get", "show", "list":
		printConfig(cfg)
		return 0
	case "edit":
		editor := "nano"
		return sh(editor + " " + configPath())
	case "set":
		if len(args) < 3 {
			fail("usage: hako config set <key> <value>")
			return 1
		}
		return setConfigKey(cfg, args[1], strings.Join(args[2:], " "))
	default:
		fail("unknown config subcommand %q", sub)
		return 1
	}
}

func printConfig(cfg *Config) {
	fmt.Println(dimStyle.Render(configPath()))
	fmt.Println()
	kv := func(k, v string) { fmt.Printf("  %s %s\n", labelStyle.Render(padRight(k, 18)), keyStyle.Render(v)) }
	kv("auto_confirm", fmt.Sprint(cfg.AutoConfirm))
	kv("auto_mirror", fmt.Sprint(cfg.AutoMirror))
	kv("preferred_region", cfg.PreferredRegion)
	kv("accent", cfg.Accent)
	kv("mirror", cfg.Mirror)
	kv("bench_timeout", fmt.Sprint(cfg.BenchTimeout))
	kv("bench_top_n", fmt.Sprint(cfg.BenchTopN))
	kv("fuzzy_search", fmt.Sprint(cfg.FuzzySearch))
}

func setConfigKey(cfg *Config, key, val string) int {
	parseBool := func() bool { return val == "true" || val == "1" || val == "yes" || val == "on" }
	switch key {
	case "auto_confirm":
		cfg.AutoConfirm = parseBool()
	case "auto_mirror":
		cfg.AutoMirror = parseBool()
	case "preferred_region":
		cfg.PreferredRegion = val
	case "accent":
		cfg.Accent = val
	case "mirror":
		cfg.Mirror = val
	case "bench_timeout":
		fmt.Sscanf(val, "%d", &cfg.BenchTimeout)
	case "bench_top_n":
		fmt.Sscanf(val, "%d", &cfg.BenchTopN)
	case "fuzzy_search":
		cfg.FuzzySearch = parseBool()
	default:
		fail("unknown config key %q", key)
		return 1
	}
	if err := saveConfig(cfg); err != nil {
		fail("failed to save config: %v", err)
		return 1
	}
	ok("%s = %s", key, val)
	return 0
}
