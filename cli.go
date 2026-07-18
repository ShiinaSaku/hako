package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/sahilm/fuzzy"
)

var (
	okStyle   = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(cRed).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(cYellow)
	dimStyle  = lipgloss.NewStyle().Foreground(cGray)
	accentSty = lipgloss.NewStyle().Foreground(cPink).Bold(true)
)

func isTTY() bool { return isatty.IsTerminal(os.Stdout.Fd()) }

func ok(format string, a ...any) {
	fmt.Println(okStyle.Render("✓") + " " + fmt.Sprintf(format, a...))
}
func fail(format string, a ...any) {
	fmt.Fprintln(os.Stderr, errStyle.Render("✗")+" "+fmt.Sprintf(format, a...))
}
func step(format string, a ...any) {
	fmt.Println(accentSty.Render("→") + " " + fmt.Sprintf(format, a...))
}
func warn(format string, a ...any) {
	fmt.Println(warnStyle.Render("!") + " " + fmt.Sprintf(format, a...))
}

// dispatch runs the CLI and returns an exit code.
func dispatch(args []string, cfg *Config) int {
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "install", "i", "add", "in":
		return cmdInstall(rest, cfg)
	case "remove", "rm", "uninstall", "un", "del", "r":
		return cmdRemove(rest, cfg, false)
	case "purge":
		return cmdRemove(rest, cfg, true)
	case "reinstall", "ri":
		return cmdReinstall(rest, cfg)
	case "upgrade", "up", "u", "full-upgrade":
		return cmdUpgrade(cfg)
	case "update", "upd":
		return runAptWithSpinner("updating package lists", "update")
	case "search", "s", "se", "find":
		return cmdSearch(rest, cfg)
	case "show", "info", "sh":
		return cmdShow(rest)
	case "list", "ls", "l":
		return cmdList(rest)
	case "files", "f":
		if len(rest) == 0 {
			fail("usage: hako files <package>")
			return 1
		}
		return sh("dpkg -L " + strings.Join(rest, " "))
	case "mirror", "m", "mirrors":
		return cmdMirror(rest, cfg)
	case "stats", "st":
		return cmdStats()
	case "clean":
		return sh("apt clean")
	case "autoclean":
		return sh("apt autoclean")
	case "config", "cfg":
		return cmdConfig(rest, cfg)
	case "tui", "ui":
		if err := runTUI(cfg); err != nil {
			fail("%v", err)
			return 1
		}
		return 0
	case "help", "-h", "--help", "h":
		printHelp()
		return 0
	case "version", "-v", "--version", "v":
		fmt.Println("hako " + version + "  (箱 — a fast Termux package manager)")
		return 0
	default:
		// treat unknown first arg as a search query for convenience
		return cmdSearch(args, cfg)
	}
}

const version = "1.0.0"

// ---------- apt exec helpers ----------

// sh runs an arbitrary shell command with stdio wired through. Kept for the few
// commands that need to hand the terminal to a real process (dpkg -L, apt clean,
// config edit). Package install/remove/upgrade go through runApt* instead, which
// suppresses apt's progress bars and presents our own UI.
func sh(cmdstr string) int {
	c := exec.Command("bash", "-lc", cmdstr)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 1
	}
	return 0
}

// ---------- package actions ----------

func cmdInstall(pkgs []string, cfg *Config) int {
	if len(pkgs) == 0 {
		fail("usage: hako install <packages...>")
		return 1
	}
	if cfg.AutoMirror {
		autoSelectMirror(cfg)
	}
	if code := runAptWithSpinner("updating package lists", "update"); code != 0 {
		warn("apt update returned %d, continuing", code)
	}
	msg := fmt.Sprintf("installing %s", strings.Join(pkgs, ", "))
	return runAptWithSpinner(msg, append([]string{"install"}, pkgs...)...)
}

func cmdRemove(pkgs []string, cfg *Config, purge bool) int {
	if len(pkgs) == 0 {
		fail("usage: hako remove <packages...>")
		return 1
	}
	action := "remove"
	if purge {
		action = "purge"
	}
	msg := fmt.Sprintf("%sing %s", action, strings.Join(pkgs, ", "))
	return runAptWithSpinner(msg, append([]string{action}, pkgs...)...)
}

func cmdReinstall(pkgs []string, cfg *Config) int {
	if len(pkgs) == 0 {
		fail("usage: hako reinstall <packages...>")
		return 1
	}
	msg := fmt.Sprintf("reinstalling %s", strings.Join(pkgs, ", "))
	return runAptWithSpinner(msg, append([]string{"install", "--reinstall"}, pkgs...)...)
}

func cmdUpgrade(cfg *Config) int {
	if cfg.AutoMirror {
		autoSelectMirror(cfg)
	}
	if code := runAptWithSpinner("updating package lists", "update"); code != 0 {
		warn("apt update returned %d, continuing", code)
	}
	return runAptWithSpinner("upgrading all packages", "full-upgrade")
}

// ---------- search / show / list ----------

func cmdSearch(args []string, cfg *Config) int {
	if len(args) == 0 {
		fail("usage: hako search <query>")
		return 1
	}
	query := strings.Join(args, " ")
	all := allPackages()
	var results []Package
	if cfg == nil || cfg.FuzzySearch {
		matches := fuzzy.FindFrom(query, nameSource(all))
		for _, mt := range matches {
			results = append(results, all[mt.Index])
		}
	} else {
		ql := strings.ToLower(query)
		for _, p := range all {
			if strings.Contains(strings.ToLower(p.Name), ql) {
				results = append(results, p)
			}
		}
	}
	if len(results) < 15 {
		ql := strings.ToLower(query)
		seen := map[string]bool{}
		for _, r := range results {
			seen[r.Name] = true
		}
		for _, p := range all {
			if !seen[p.Name] && strings.Contains(strings.ToLower(p.Desc), ql) {
				results = append(results, p)
			}
		}
	}
	if len(results) == 0 {
		warn("no packages match %q", query)
		return 1
	}
	for _, p := range results {
		badge := "  "
		if p.Installed {
			badge = installedBadge.Render("● ")
		}
		name := accentSty.Render(padRight(truncate(p.Name, 28), 28))
		fmt.Printf("%s%s %s\n", badge, name, dimStyle.Render(truncate(p.Desc, 60)))
	}
	fmt.Println(dimStyle.Render(fmt.Sprintf("\n%d results", len(results))))
	return 0
}

func cmdShow(args []string) int {
	if len(args) == 0 {
		fail("usage: hako show <package>")
		return 1
	}
	d := packageDetail(args[0])
	if d.Version == "" && !d.Installed {
		fail("package %q not found", args[0])
		return 1
	}
	line := func(label, val string) {
		if val != "" {
			fmt.Printf("%s %s\n", labelStyle.Render(padRight(label+":", 13)), val)
		}
	}
	fmt.Println(titleStyle.Render(" " + d.Name + " "))
	if d.Installed {
		line("Status", okStyle.Render("installed "+d.InstVersion))
	} else {
		line("Status", warnStyle.Render("not installed"))
	}
	line("Version", d.Version)
	line("Section", d.Section)
	if d.SizeKB > 0 {
		line("Size", humanKB(d.SizeKB))
	}
	line("Homepage", keyStyle.Render(d.Homepage))
	if d.Description != "" {
		fmt.Println("\n" + labelStyle.Render("Description"))
		fmt.Println(dimStyle.Render(wrap(d.Description, 72)))
	}
	if len(d.Depends) > 0 {
		fmt.Println("\n" + labelStyle.Render(fmt.Sprintf("Depends (%d)", len(d.Depends))))
		fmt.Println(dimStyle.Render(wrap(strings.Join(d.Depends, ", "), 72)))
	}
	if len(d.RDepends) > 0 {
		fmt.Println("\n" + labelStyle.Render(fmt.Sprintf("Required by (%d)", len(d.RDepends))))
	}
	return 0
}

func cmdList(args []string) int {
	all := false
	for _, a := range args {
		if a == "--all" || a == "-a" {
			all = true
		}
	}
	if all {
		for _, p := range allPackages() {
			badge := "  "
			if p.Installed {
				badge = installedBadge.Render("● ")
			}
			fmt.Printf("%s%s %s\n", badge, accentSty.Render(padRight(truncate(p.Name, 28), 28)), dimStyle.Render(truncate(p.Desc, 55)))
		}
		return 0
	}
	list := installedList()
	for _, p := range list {
		fmt.Printf("%s %s %s\n",
			accentSty.Render(padRight(truncate(p.Name, 28), 28)),
			labelStyle.Render(padLeft(humanKB(p.SizeKB), 9)),
			dimStyle.Render(truncate(p.Version, 24)))
	}
	fmt.Println(dimStyle.Render(fmt.Sprintf("\n%d installed", len(list))))
	return 0
}

func cmdStats() int {
	s := computeStats()
	fmt.Println(titleStyle.Render(" 箱 hako — stats "))
	fmt.Printf("%s %s\n", labelStyle.Render("Installed packages:"), keyStyle.Render(strconv.Itoa(s.Count)))
	fmt.Printf("%s %s\n\n", labelStyle.Render("Total disk usage:  "), keyStyle.Render(humanKB(s.TotalKB)))
	fmt.Println(labelStyle.Render("Largest packages"))
	maxKB := int64(1)
	if len(s.Largest) > 0 {
		maxKB = s.Largest[0].SizeKB
	}
	barW := 28
	for _, p := range s.Largest {
		filled := int(float64(p.SizeKB) / float64(maxKB) * float64(barW))
		if filled < 1 {
			filled = 1
		}
		bar := lipgloss.NewStyle().Foreground(cPurple).Render(strings.Repeat("█", filled)) +
			lipgloss.NewStyle().Foreground(cFaint).Render(strings.Repeat("░", barW-filled))
		fmt.Printf("  %s %s %s\n", padRight(truncate(p.Name, 22), 22), bar, labelStyle.Render(padLeft(humanKB(p.SizeKB), 9)))
	}
	return 0
}

func printHelp() {
	fmt.Println(titleStyle.Render(" 箱 hako ") + "  " + dimStyle.Render("a fast, fuzzy, intelligent Termux package manager"))
	fmt.Println()
	sec := func(s string) { fmt.Println(labelStyle.Render(s)) }
	cmd := func(name, desc string) {
		fmt.Printf("  %s %s\n", accentSty.Render(padRight(name, 22)), dimStyle.Render(desc))
	}
	fmt.Println(dimStyle.Render("Usage: ") + "hako [command] [args]   " + dimStyle.Render("(no command → launch TUI)"))
	fmt.Println()
	sec("Packages")
	cmd("install, i <pkgs>", "install packages")
	cmd("remove, rm <pkgs>", "remove packages")
	cmd("purge <pkgs>", "remove packages + config")
	cmd("reinstall <pkgs>", "reinstall packages")
	cmd("upgrade, up", "update lists + upgrade all")
	cmd("update", "refresh package lists")
	fmt.Println()
	sec("Discover")
	cmd("search, s <query>", "fuzzy search packages")
	cmd("show, info <pkg>", "detailed package info")
	cmd("list, ls [-a]", "list installed (or --all)")
	cmd("files <pkg>", "files installed by a package")
	cmd("stats", "disk usage breakdown")
	fmt.Println()
	sec("Mirrors")
	cmd("mirror", "show current mirror")
	cmd("mirror bench", "benchmark all mirrors")
	cmd("mirror auto", "pick + set fastest mirror")
	cmd("mirror set <name|url>", "set a mirror")
	fmt.Println()
	sec("Misc")
	cmd("config [get|set|path]", "manage settings (~/.config/hako)")
	cmd("clean / autoclean", "clear apt cache")
	cmd("tui", "launch the interactive TUI")
	cmd("version", "print version")
}
