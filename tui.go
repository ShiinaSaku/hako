package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

type tab int

const (
	tabSearch tab = iota
	tabInstalled
	tabMirrors
	tabStats
)

var tabNames = []string{"Search", "Installed", "Mirrors", "Stats"}

type model struct {
	w, h    int
	tab     tab
	ready   bool
	loading bool
	spin    spinner.Model

	all     []Package
	results []Package

	search  textinput.Model
	sCursor int
	sOffset int

	installed []Package
	iCursor   int
	iOffset   int

	mirrors      []Mirror
	mCursor      int
	mOffset      int
	benchmarking int // remaining benchmark jobs
	benchTotal   int
	prog         progress.Model
	curMirror    string

	cfg *Config

	stats Stats

	showDetail bool
	detail     Detail
	detailVP   viewport.Model
	loadingDet bool

	// executing: a modal shown while an apt action runs in the background.
	// apt's own UI is suppressed (DEBIAN_FRONTEND=noninteractive, captured
	// stdout/stderr) and we present this overlay instead.
	executing bool
	execLabel string

	status  string
	statusC lipgloss.Color
}

// ---------- messages ----------

type loadedMsg struct{ all []Package }
type detailMsg struct{ d Detail }
type statsMsg struct{ s Stats }
type mirrorsMsg struct {
	mirrors []Mirror
	current string
}
type benchOneMsg struct{ m Mirror }
type execDoneMsg struct {
	label  string
	result aptResult
}
type reloadMsg struct{ all []Package }
type clearStatusMsg struct{}

// ---------- commands ----------

func loadCmd() tea.Msg   { return loadedMsg{all: allPackages()} }
func reloadCmd() tea.Msg { return reloadMsg{all: allPackages()} }
func statsCmd() tea.Msg  { return statsMsg{s: computeStats()} }
func detailCmd(name string) tea.Cmd {
	return func() tea.Msg { return detailMsg{d: packageDetail(name)} }
}
func mirrorsCmd() tea.Msg {
	return mirrorsMsg{mirrors: loadMirrors(), current: currentMirror()}
}
func benchCmd(m Mirror, timeout int) tea.Cmd {
	return func() tea.Msg { return benchOneMsg{m: benchmark(m, timeout)} }
}
func flashClear() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })
}

// execPkg runs an apt action in the background, fully headless — apt's own
// progress UI is suppressed (output captured, never shown live). We surface a
// modal overlay in the TUI instead. Returns a tea.Cmd that emits execDoneMsg.
func execPkg(action, name string) (string, tea.Cmd) {
	var label string
	cmd := func() tea.Msg {
		var r aptResult
		switch action {
		case "install":
			runAptQuiet("update")
			r = runAptQuiet("install", name)
		case "remove":
			r = runAptQuiet("remove", name)
		case "reinstall":
			r = runAptQuiet("install", "--reinstall", name)
		case "upgrade":
			runAptQuiet("update")
			r = runAptQuiet("full-upgrade")
		}
		return execDoneMsg{label: label, result: r}
	}
	switch action {
	case "install":
		label = "installing " + name
	case "remove":
		label = "removing " + name
	case "reinstall":
		label = "reinstalling " + name
	case "upgrade":
		label = "upgrading all packages"
	}
	return label, cmd
}

// ---------- init ----------

func initialModel() model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = spinnerStyle

	ti := textinput.New()
	ti.Placeholder = "type to search packages…"
	ti.Prompt = "  "
	ti.PromptStyle = searchPromptStyle
	ti.Focus()
	ti.CharLimit = 64

	pr := progress.New(progress.WithScaledGradient("#7D56F4", "#FF6AC1"))

	return model{
		tab:     tabSearch,
		loading: true,
		spin:    sp,
		search:  ti,
		prog:    pr,
		statusC: cGray,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, loadCmd, mirrorsCmd, statsCmd)
}

// ---------- update ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ready = true
		m.prog.Width = msg.Width - 20
		if m.prog.Width > 60 {
			m.prog.Width = 60
		}
		m.layoutDetail()
		return m, nil

	case progress.FrameMsg:
		pm, cmd := m.prog.Update(msg)
		m.prog = pm.(progress.Model)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case loadedMsg:
		m.all = msg.all
		m.loading = false
		m.recomputeResults()
		m.installed = installedFrom(m.all)
		return m, nil

	case reloadMsg:
		m.all = msg.all
		m.recomputeResults()
		m.installed = installedFrom(m.all)
		return m, statsCmd

	case statsMsg:
		m.stats = msg.s
		return m, nil

	case mirrorsMsg:
		m.mirrors = msg.mirrors
		m.curMirror = msg.current
		return m, nil

	case benchOneMsg:
		for i := range m.mirrors {
			if m.mirrors[i].File == msg.m.File {
				m.mirrors[i] = msg.m
				break
			}
		}
		if m.benchmarking > 0 {
			m.benchmarking--
		}
		done := m.benchTotal - m.benchmarking
		var pcmd tea.Cmd
		if m.benchTotal > 0 {
			pcmd = m.prog.SetPercent(float64(done) / float64(m.benchTotal))
		}
		if m.benchmarking == 0 {
			m.sortMirrors()
			m.setStatus("benchmark complete — sorted by speed", cGreen)
			return m, tea.Batch(pcmd, flashClear())
		}
		return m, pcmd

	case detailMsg:
		m.detail = msg.d
		m.loadingDet = false
		m.renderDetail()
		return m, nil

	case execDoneMsg:
		m.executing = false
		m.showDetail = false
		if msg.result.code == 0 {
			m.setStatus(msg.label+" — done", cGreen)
		} else {
			summary := msg.result.summary()
			if summary != "" {
				m.setStatus(msg.label+" failed: "+summary, cRed)
			} else {
				m.setStatus(msg.label+" failed", cRed)
			}
		}
		return m, tea.Batch(reloadCmd, flashClear())

	case clearStatusMsg:
		m.status = ""
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// global quit
	if k == "ctrl+c" {
		return m, tea.Quit
	}

	// executing overlay absorbs all keys except quit; apt is running headless
	// in the background and we don't want the user driving the tabs underneath.
	if m.executing {
		return m, nil
	}

	// detail modal has priority
	if m.showDetail {
		switch k {
		case "esc", "q":
			m.showDetail = false
			return m, nil
		case "i":
			if !m.detail.Installed {
				return m.startExec("install", m.detail.Name)
			}
		case "r":
			if m.detail.Installed {
				return m.startExec("remove", m.detail.Name)
			}
		case "R":
			if m.detail.Installed {
				return m.startExec("reinstall", m.detail.Name)
			}
		case "up", "k":
			m.detailVP.ScrollUp(1)
		case "down", "j":
			m.detailVP.ScrollDown(1)
		}
		var cmd tea.Cmd
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}

	// tab switching
	switch k {
	case "tab", "shift+tab":
		if k == "tab" {
			m.tab = (m.tab + 1) % 4
		} else {
			m.tab = (m.tab + 3) % 4
		}
		return m, nil
	case "ctrl+r":
		m.loading = true
		return m, tea.Batch(reloadCmd, mirrorsCmd)
	}

	// per-tab keys
	switch m.tab {
	case tabSearch:
		return m.keySearch(msg)
	case tabInstalled:
		return m.keyInstalled(msg)
	case tabMirrors:
		return m.keyMirrors(msg)
	case tabStats:
		if k == "q" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) keySearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up":
		m.moveCursor(&m.sCursor, &m.sOffset, len(m.results), -1)
		return m, nil
	case "down":
		m.moveCursor(&m.sCursor, &m.sOffset, len(m.results), 1)
		return m, nil
	case "pgup":
		m.moveCursor(&m.sCursor, &m.sOffset, len(m.results), -m.bodyHeight())
		return m, nil
	case "pgdown":
		m.moveCursor(&m.sCursor, &m.sOffset, len(m.results), m.bodyHeight())
		return m, nil
	case "enter":
		if m.sCursor < len(m.results) {
			return m.openDetail(m.results[m.sCursor].Name)
		}
		return m, nil
	}
	var cmd tea.Cmd
	prev := m.search.Value()
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prev {
		m.recomputeResults()
	}
	return m, cmd
}

func (m model) keyInstalled(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(&m.iCursor, &m.iOffset, len(m.installed), -1)
	case "down", "j":
		m.moveCursor(&m.iCursor, &m.iOffset, len(m.installed), 1)
	case "pgup":
		m.moveCursor(&m.iCursor, &m.iOffset, len(m.installed), -m.bodyHeight())
	case "pgdown":
		m.moveCursor(&m.iCursor, &m.iOffset, len(m.installed), m.bodyHeight())
	case "enter":
		if m.iCursor < len(m.installed) {
			return m.openDetail(m.installed[m.iCursor].Name)
		}
	case "u":
		return m.startExec("upgrade", "")
	}
	return m, nil
}

func (m model) keyMirrors(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(&m.mCursor, &m.mOffset, len(m.mirrors), -1)
	case "down", "j":
		m.moveCursor(&m.mCursor, &m.mOffset, len(m.mirrors), 1)
	case "pgup":
		m.moveCursor(&m.mCursor, &m.mOffset, len(m.mirrors), -m.bodyHeight())
	case "pgdown":
		m.moveCursor(&m.mCursor, &m.mOffset, len(m.mirrors), m.bodyHeight())
	case "b":
		if m.benchmarking == 0 && len(m.mirrors) > 0 {
			var cmds []tea.Cmd
			timeout := 8
			if m.cfg != nil {
				timeout = m.cfg.BenchTimeout
			}
			for _, mi := range m.mirrors {
				cmds = append(cmds, benchCmd(mi, timeout))
			}
			m.benchmarking = len(m.mirrors)
			m.benchTotal = len(m.mirrors)
			cmds = append(cmds, m.prog.SetPercent(0))
			m.setStatus(fmt.Sprintf("benchmarking %d mirrors…", m.benchmarking), cYellow)
			return m, tea.Batch(cmds...)
		}
	case "enter":
		if m.mCursor < len(m.mirrors) {
			mir := m.mirrors[m.mCursor]
			if err := applyMirror(mir); err != nil {
				m.setStatus("failed to set mirror: "+err.Error(), cRed)
			} else {
				m.curMirror = strings.TrimRight(mir.Main, "/")
				m.setStatus("mirror set → "+mir.Main, cGreen)
			}
			return m, flashClear()
		}
	}
	return m, nil
}

func (m model) openDetail(name string) (tea.Model, tea.Cmd) {
	m.showDetail = true
	m.loadingDet = true
	m.detail = Detail{Name: name}
	m.layoutDetail()
	return m, detailCmd(name)
}

// ---------- helpers ----------

func (m *model) setStatus(s string, c lipgloss.Color) {
	m.status = s
	m.statusC = c
}

// startExec launches an apt action in the background and switches the TUI into
// the executing overlay. apt's own UI never reaches the user; we render our own
// modal and tear it down when execDoneMsg arrives.
func (m model) startExec(action, name string) (tea.Model, tea.Cmd) {
	label, cmd := execPkg(action, name)
	m.executing = true
	m.execLabel = label
	m.showDetail = false
	return m, tea.Batch(m.spin.Tick, cmd)
}

func (m *model) moveCursor(cursor, offset *int, n, delta int) {
	if n == 0 {
		return
	}
	*cursor += delta
	if *cursor < 0 {
		*cursor = 0
	}
	if *cursor >= n {
		*cursor = n - 1
	}
	h := m.bodyHeight()
	if *cursor < *offset {
		*offset = *cursor
	}
	if *cursor >= *offset+h {
		*offset = *cursor - h + 1
	}
}

type nameSource []Package

func (s nameSource) String(i int) string { return s[i].Name }
func (s nameSource) Len() int            { return len(s) }

func (m *model) recomputeResults() {
	q := strings.TrimSpace(m.search.Value())
	if q == "" {
		m.results = m.all
		m.sCursor, m.sOffset = 0, 0
		return
	}
	var res []Package
	if m.cfg == nil || m.cfg.FuzzySearch {
		matches := fuzzy.FindFrom(q, nameSource(m.all))
		res = make([]Package, 0, len(matches))
		for _, mt := range matches {
			res = append(res, m.all[mt.Index])
		}
	} else {
		ql := strings.ToLower(q)
		res = make([]Package, 0)
		for _, p := range m.all {
			if strings.Contains(strings.ToLower(p.Name), ql) {
				res = append(res, p)
			}
		}
	}
	// fallback: substring search in descriptions if name matches are few
	if len(res) < 20 {
		ql := strings.ToLower(q)
		seen := map[string]bool{}
		for _, r := range res {
			seen[r.Name] = true
		}
		for _, p := range m.all {
			if !seen[p.Name] && strings.Contains(strings.ToLower(p.Desc), ql) {
				res = append(res, p)
			}
		}
	}
	m.results = res
	m.sCursor, m.sOffset = 0, 0
}

func installedFrom(all []Package) []Package {
	var out []Package
	for _, p := range all {
		if p.Installed {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SizeKB > out[j].SizeKB })
	return out
}

func (m *model) sortMirrors() {
	sort.SliceStable(m.mirrors, func(i, j int) bool {
		a, b := m.mirrors[i], m.mirrors[j]
		ao := a.Tested && a.LatencyMS >= 0
		bo := b.Tested && b.LatencyMS >= 0
		if ao != bo {
			return ao
		}
		if ao && bo {
			return a.SpeedKBps > b.SpeedKBps
		}
		return false
	})
	m.mCursor, m.mOffset = 0, 0
}

func runTUI(cfg *Config) error {
	m := initialModel()
	m.cfg = cfg
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
