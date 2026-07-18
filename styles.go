package main

import "github.com/charmbracelet/lipgloss"

var (
	cPurple = lipgloss.Color("#7D56F4")
	cPink   = lipgloss.Color("#FF6AC1")
	cGreen  = lipgloss.Color("#3ED598")
	cYellow = lipgloss.Color("#FFB454")
	cRed    = lipgloss.Color("#FF5555")
	cBlue   = lipgloss.Color("#5AC8FA")
	cGray   = lipgloss.Color("#626262")
	cFaint  = lipgloss.Color("#3A3A3A")
	cFg     = lipgloss.Color("#EEEEEE")
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(cPurple).
			Padding(0, 1)

	tabStyle = lipgloss.NewStyle().
			Foreground(cGray).
			Padding(0, 2)

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(cPink).
			Padding(0, 2)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(cGray)

	helpStyle = lipgloss.NewStyle().
			Foreground(cFaint)

	installedBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(cGreen)

	selItemStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cPink)

	itemStyle = lipgloss.NewStyle().
			Foreground(cFg)

	descStyle = lipgloss.NewStyle().
			Foreground(cGray)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cPurple).
			Padding(0, 1)

	keyStyle = lipgloss.NewStyle().
			Foreground(cBlue)

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(cYellow)

	spinnerStyle = lipgloss.NewStyle().Foreground(cPink)

	searchPromptStyle = lipgloss.NewStyle().Foreground(cPink).Bold(true)
)

// applyTheme overrides the accent colour from config and rebuilds the
// accent-dependent styles.
func applyTheme(accent string) {
	if accent == "" {
		return
	}
	cPink = lipgloss.Color(accent)
	activeTabStyle = activeTabStyle.Background(cPink)
	selItemStyle = selItemStyle.Foreground(cPink)
	spinnerStyle = spinnerStyle.Foreground(cPink)
	searchPromptStyle = searchPromptStyle.Foreground(cPink)
}
