package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// aptStdout is captured while an apt process runs. It is only written to by the
// running apt command and read after completion, so no extra locking is needed
// for sequential CLI usage. The TUI's background runner uses a per-run buffer.
type aptResult struct {
	cmd    string
	args   []string
	stdout string
	stderr string
	code   int
}

// runAptQuiet executes apt non-interactively with no progress UI. Output is
// captured (never shown live). DEBIAN_FRONTEND=noninteractive + -y suppress
// prompts; DPKG_HEADLESS=1 and the env block below kill the progress bars.
func runAptQuiet(args ...string) aptResult {
	full := append([]string{"-y"}, args...)
	cmd := exec.Command("apt", full...)
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"DEBIAN_PRIORITY=critical",
		"DPKG_HEADLESS=1",
		"APT_LISTCHANGES_FRONTEND=none",
		"APT_LISTBUGS_FRONTEND=none",
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	r := aptResult{cmd: "apt", args: args, stdout: out.String(), stderr: errb.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			r.code = ee.ExitCode()
		} else {
			r.code = 1
		}
	}
	return r
}

// runDpkgQuiet runs dpkg non-interactively (used by remove/purge fallback and
// any future direct dpkg call). Same headless env as apt.
func runDpkgQuiet(args ...string) aptResult {
	cmd := exec.Command("dpkg", args...)
	cmd.Env = append(os.Environ(),
		"DEBIAN_FRONTEND=noninteractive",
		"DEBIAN_PRIORITY=critical",
		"DPKG_HEADLESS=1",
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	r := aptResult{cmd: "dpkg", args: args, stdout: out.String(), stderr: errb.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			r.code = ee.ExitCode()
		} else {
			r.code = 1
		}
	}
	return r
}

// aptSummary extracts the meaningful tail of an apt run for display. apt's
// useful information (what it did, errors, broken packages) is always in the
// last few lines; the rest is progress noise that we now suppress entirely.
func (r aptResult) summary() string {
	if r.code == 0 {
		return ""
	}
	// Prefer stderr lines that look like real errors; fall back to stdout.
	src := r.stderr
	if strings.TrimSpace(src) == "" {
		src = r.stdout
	}
	lines := strings.Split(strings.TrimSpace(src), "\n")
	var keep []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "Progress") || strings.HasPrefix(t, "Reading") {
			continue
		}
		keep = append(keep, t)
	}
	if len(keep) > 6 {
		keep = keep[len(keep)-6:]
	}
	return strings.Join(keep, "\n")
}

// ---------- live ticker for CLI ----------

// cliSpinner prints a compact animated line while a long op runs. Created once
// per CLI action and stopped with .finish(), which clears the line and prints a
// final ✓/✗ line via the caller.
type cliSpinner struct {
	msg     string
	tty     bool
	stop    chan struct{}
	done    chan struct{}
	mu      sync.Mutex
	running bool
}

func newCLISpinner(msg string) *cliSpinner {
	return &cliSpinner{msg: msg, tty: isTTY()}
}

func (s *cliSpinner) start() {
	if !s.tty {
		fmt.Println("→ " + s.msg)
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.running = true
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	go func() {
		defer close(s.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K")
				return
			case <-t.C:
				fr := frames[i%len(frames)]
				fmt.Printf("\r\033[K %s %s", accentSty.Render(fr), s.msg)
				i++
			}
		}
	}()
}

func (s *cliSpinner) finish() {
	if !s.tty {
		return
	}
	if s.running {
		close(s.stop)
		<-s.done
		s.running = false
	}
}

// runAptWithSpinner runs apt quietly while showing a CLI spinner, then prints a
// clean result line. Returns the apt exit code.
func runAptWithSpinner(msg string, args ...string) int {
	sp := newCLISpinner(msg)
	sp.start()
	r := runAptQuiet(args...)
	sp.finish()
	if r.code == 0 {
		ok("%s", msg)
		return 0
	}
	fail("%s failed", msg)
	if sum := r.summary(); sum != "" {
		fmt.Fprintln(os.Stderr, dimStyle.Render(sum))
	}
	return r.code
}
