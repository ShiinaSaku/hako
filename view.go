package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

const (
	headerLines = 2 // title/tab row + search/blank row
	footerLines = 2 // status + help
)

func (m model) bodyHeight() int {
	h := m.h - headerLines - footerLines - 1
	if h < 3 {
		h = 3
	}
	return h
}

func (m *model) layoutDetail() {
	w := m.w - 8
	h := m.h - 8
	if w < 20 {
		w = 20
	}
	if h < 6 {
		h = 6
	}
	m.detailVP = viewport.New(w, h)
}

func (m model) View() string {
	if !m.ready {
		return "\n  starting hako…"
	}
	if m.executing {
		return m.viewExecuting()
	}
	if m.showDetail {
		return m.viewDetail()
	}

	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.subHeader())
	b.WriteString("\n")

	var body string
	switch m.tab {
	case tabSearch:
		body = m.viewSearch()
	case tabInstalled:
		body = m.viewList(m.installed, m.iCursor, m.iOffset, true)
	case tabMirrors:
		body = m.viewMirrors()
	case tabStats:
		body = m.viewStats()
	}
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m model) header() string {
	title := titleStyle.Render(" 箱 hako ")
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.tab {
			tabs = append(tabs, activeTabStyle.Render(name))
		} else {
			tabs = append(tabs, tabStyle.Render(name))
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Center, title, "  ", strings.Join(tabs, " "))
	return row
}

func (m model) subHeader() string {
	switch m.tab {
	case tabSearch:
		count := descStyle.Render(fmt.Sprintf(" %d/%d ", len(m.results), len(m.all)))
		if m.loading {
			return m.spin.View() + " loading package index…"
		}
		return m.search.View() + count
	case tabInstalled:
		return descStyle.Render(fmt.Sprintf("  %d packages installed", len(m.installed)))
	case tabMirrors:
		cur := m.curMirror
		if cur == "" {
			cur = "unknown"
		}
		s := descStyle.Render("  current: ") + keyStyle.Render(cur)
		if m.benchmarking > 0 {
			done := m.benchTotal - m.benchmarking
			s = "  " + m.prog.View() + descStyle.Render(fmt.Sprintf("  %d/%d", done, m.benchTotal))
		}
		return s
	case tabStats:
		return descStyle.Render("  installed footprint")
	}
	return ""
}

func (m model) viewSearch() string {
	if m.loading {
		return "\n  " + m.spin.View() + " indexing…"
	}
	return m.viewList(m.results, m.sCursor, m.sOffset, false)
}

func (m model) viewList(items []Package, cursor, offset int, sizeMode bool) string {
	h := m.bodyHeight()
	if len(items) == 0 {
		return descStyle.Render("\n  no packages")
	}
	var lines []string
	end := offset + h
	if end > len(items) {
		end = len(items)
	}
	nameW := 26
	for i := offset; i < end; i++ {
		p := items[i]
		cur := "  "
		nameStyle := itemStyle
		if i == cursor {
			cur = selItemStyle.Render("▎ ")
			nameStyle = selItemStyle
		}
		name := truncate(p.Name, nameW)
		name = nameStyle.Render(padRight(name, nameW))

		var meta string
		if sizeMode {
			meta = labelStyle.Render(padLeft(humanKB(p.SizeKB), 9)) + " " + descStyle.Render(truncate(p.Version, 18))
		} else {
			badge := "  "
			if p.Installed {
				badge = installedBadge.Render("● ")
			}
			meta = badge + descStyle.Render(truncate(p.Desc, m.w-nameW-8))
		}
		lines = append(lines, cur+name+" "+meta)
	}
	// scrollbar hint
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m model) viewMirrors() string {
	h := m.bodyHeight()
	if len(m.mirrors) == 0 {
		return descStyle.Render("\n  no mirrors found")
	}
	var lines []string
	end := m.mOffset + h
	if end > len(m.mirrors) {
		end = len(m.mirrors)
	}
	for i := m.mOffset; i < end; i++ {
		mi := m.mirrors[i]
		cur := "  "
		style := itemStyle
		if i == m.mCursor {
			cur = selItemStyle.Render("▎ ")
			style = selItemStyle
		}
		active := " "
		if strings.TrimRight(mi.Main, "/") == strings.TrimRight(m.curMirror, "/") {
			active = installedBadge.Render("★")
		}
		name := style.Render(padRight(truncate(mi.Name, 28), 28))
		region := descStyle.Render(padRight(mi.Region, 16))
		var perf string
		if !mi.Tested {
			perf = descStyle.Render("—")
		} else if mi.LatencyMS < 0 {
			perf = lipgloss.NewStyle().Foreground(cRed).Render("unreachable")
		} else {
			speed := speedColor(mi.SpeedKBps).Render(fmt.Sprintf("%7.0f KB/s", mi.SpeedKBps))
			lat := descStyle.Render(fmt.Sprintf("%4dms", mi.LatencyMS))
			perf = speed + "  " + lat
		}
		lines = append(lines, fmt.Sprintf("%s%s %s%s %s", cur, active, name, region, perf))
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func speedColor(kbps float64) lipgloss.Style {
	switch {
	case kbps >= 2000:
		return lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	case kbps >= 500:
		return lipgloss.NewStyle().Foreground(cYellow)
	default:
		return lipgloss.NewStyle().Foreground(cRed)
	}
}

func (m model) viewStats() string {
	s := m.stats
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + labelStyle.Render("Packages installed: ") + keyStyle.Render(fmt.Sprintf("%d", s.Count)) + "\n")
	b.WriteString("  " + labelStyle.Render("Total disk usage:   ") + keyStyle.Render(humanKB(s.TotalKB)) + "\n\n")
	b.WriteString("  " + labelStyle.Render("Largest packages") + "\n")
	maxKB := int64(1)
	if len(s.Largest) > 0 {
		maxKB = s.Largest[0].SizeKB
	}
	barW := 24
	for _, p := range s.Largest {
		filled := int(float64(p.SizeKB) / float64(maxKB) * float64(barW))
		if filled < 1 {
			filled = 1
		}
		bar := lipgloss.NewStyle().Foreground(cPurple).Render(strings.Repeat("█", filled)) +
			lipgloss.NewStyle().Foreground(cFaint).Render(strings.Repeat("░", barW-filled))
		b.WriteString(fmt.Sprintf("  %s %s %s\n",
			padRight(truncate(p.Name, 22), 22),
			bar,
			labelStyle.Render(padLeft(humanKB(p.SizeKB), 9))))
	}
	return b.String()
}

func (m model) viewDetail() string {
	title := titleStyle.Render(" " + m.detail.Name + " ")
	content := m.detailVP.View()
	panel := panelStyle.Width(m.detailVP.Width).Render(content)
	help := helpStyle.Render(m.detailHelp())
	block := lipgloss.JoinVertical(lipgloss.Left, title, panel, help)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, block)
}

// viewExecuting is the overlay shown while an apt action runs in the background.
// apt itself produces no on-screen output (DEBIAN_FRONTEND=noninteractive, captured
// stdio); this modal is the only feedback the user gets until execDoneMsg lands.
func (m model) viewExecuting() string {
	spin := spinnerStyle.Render(m.spin.View())
	label := selItemStyle.Render(m.execLabel)
	body := titleStyle.Render(" 箱 working ") + "\n\n" + spin + " " + label + "\n\n" + helpStyle.Render("apt is running headless — press ctrl+c to abort")
	panel := panelStyle.Width(max(m.w-16, 30)).Render(body)
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, panel)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m *model) renderDetail() {
	d := m.detail
	var b strings.Builder
	line := func(label, val string) {
		if val == "" {
			return
		}
		b.WriteString(labelStyle.Render(padRight(label, 12)) + val + "\n")
	}
	status := lipgloss.NewStyle().Foreground(cRed).Render("not installed")
	if d.Installed {
		status = installedBadge.Render("installed " + d.InstVersion)
	}
	line("Status", status)
	line("Version", d.Version)
	line("Section", d.Section)
	if d.SizeKB > 0 {
		line("Size", humanKB(d.SizeKB))
	}
	line("Homepage", keyStyle.Render(d.Homepage))
	b.WriteString("\n")
	if d.Description != "" {
		b.WriteString(labelStyle.Render("Description") + "\n")
		b.WriteString(descStyle.Render(wrap(d.Description, m.detailVP.Width-2)) + "\n\n")
	}
	if len(d.Depends) > 0 {
		b.WriteString(labelStyle.Render(fmt.Sprintf("Depends (%d)", len(d.Depends))) + "\n")
		b.WriteString(descStyle.Render("  "+strings.Join(d.Depends, ", ")) + "\n\n")
	}
	if len(d.RDepends) > 0 {
		show := d.RDepends
		if len(show) > 30 {
			show = show[:30]
		}
		b.WriteString(labelStyle.Render(fmt.Sprintf("Required by (%d)", len(d.RDepends))) + "\n")
		b.WriteString(descStyle.Render("  "+strings.Join(show, ", ")) + "\n")
	}
	if m.loadingDet {
		m.detailVP.SetContent(m.spin.View() + " loading…")
	} else {
		m.detailVP.SetContent(b.String())
	}
}

func (m model) detailHelp() string {
	var parts []string
	if m.detail.Installed {
		parts = append(parts, keyStyle.Render("r")+" remove", keyStyle.Render("R")+" reinstall")
	} else {
		parts = append(parts, keyStyle.Render("i")+" install")
	}
	parts = append(parts, keyStyle.Render("↑/↓")+" scroll", keyStyle.Render("esc")+" close")
	return strings.Join(parts, "   ")
}

func (m model) footer() string {
	var status string
	if m.status != "" {
		status = lipgloss.NewStyle().Foreground(m.statusC).Render("  " + m.status)
	}
	var help string
	switch m.tab {
	case tabSearch:
		help = keys("↑/↓", "move") + keys("enter", "details") + keys("tab", "switch") + keys("ctrl+c", "quit")
	case tabInstalled:
		help = keys("j/k", "move") + keys("enter", "details") + keys("u", "upgrade all") + keys("tab", "switch")
	case tabMirrors:
		help = keys("j/k", "move") + keys("b", "benchmark") + keys("enter", "select") + keys("tab", "switch")
	case tabStats:
		help = keys("ctrl+r", "refresh") + keys("tab", "switch") + keys("q", "quit")
	}
	if status != "" {
		return status + "\n" + helpStyle.Render("  "+help)
	}
	return "\n" + helpStyle.Render("  "+help)
}

func keys(k, desc string) string {
	return keyStyle.Render(k) + " " + helpStyle.Render(desc) + "   "
}

// ---------- string utils ----------

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func padRight(s string, n int) string {
	l := lipgloss.Width(s)
	if l >= n {
		return s
	}
	return s + strings.Repeat(" ", n-l)
}

func padLeft(s string, n int) string {
	l := lipgloss.Width(s)
	if l >= n {
		return s
	}
	return strings.Repeat(" ", n-l) + s
}

func wrap(s string, w int) string {
	if w < 10 {
		w = 10
	}
	var out strings.Builder
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		ll := 0
		for _, word := range words {
			if ll+len(word)+1 > w && ll > 0 {
				out.WriteString("\n")
				ll = 0
			}
			if ll > 0 {
				out.WriteString(" ")
				ll++
			}
			out.WriteString(word)
			ll += len(word)
		}
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}
