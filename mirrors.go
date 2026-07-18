package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	mirrorBaseDir = "/data/data/com.termux/files/usr/etc/termux/mirrors"
	sourcesList   = "/data/data/com.termux/files/usr/etc/apt/sources.list"
)

// Mirror describes a Termux mirror definition file.
type Mirror struct {
	File   string // absolute path to the definition file
	Name   string // human comment/name
	Region string // parent dir (asia, europe, default, ...)
	Main   string // MAIN url
	Weight int

	// benchmark results
	LatencyMS int     // TTFB ms, -1 = unreachable
	SpeedKBps float64 // measured throughput
	Tested    bool
}

// loadMirrors walks the mirror directory tree and parses every definition.
func loadMirrors() []Mirror {
	var mirrors []Mirror
	filepath.Walk(mirrorBaseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasSuffix(base, ".dpkg-old") || strings.HasSuffix(base, ".dpkg-new") || strings.HasSuffix(base, "~") {
			return nil
		}
		m := parseMirror(path)
		if m.Main != "" {
			region := filepath.Base(filepath.Dir(path))
			if region == "mirrors" {
				region = "default"
			}
			m.Region = region
			mirrors = append(mirrors, m)
		}
		return nil
	})
	return mirrors
}

func parseMirror(path string) Mirror {
	m := Mirror{File: path, Weight: 1, LatencyMS: -1}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "# Mirror by") {
			m.Name = strings.TrimSpace(strings.TrimPrefix(line, "# Mirror by"))
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		switch key {
		case "MAIN":
			m.Main = val
		case "WEIGHT":
			m.Weight, _ = strconv.Atoi(val)
		}
	}
	if m.Name == "" {
		m.Name = filepath.Base(path)
	}
	return m
}

// benchmark measures real latency + download speed by fetching the Packages
// index (or Release as fallback). Returns an updated copy of m.
func benchmark(m Mirror, timeout int) Mirror {
	if timeout <= 0 {
		timeout = 8
	}
	base := strings.TrimRight(m.Main, "/")
	arch := detectArch()
	urls := []string{
		base + "/dists/stable/main/binary-" + arch + "/Packages.gz",
		base + "/dists/stable/Release",
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	for _, u := range urls {
		lat, speed, ok := timedGet(client, u, timeout)
		if ok {
			m.LatencyMS = lat
			m.SpeedKBps = speed
			m.Tested = true
			return m
		}
	}
	m.LatencyMS = -1
	m.Tested = true
	return m
}

// benchAll benchmarks mirrors concurrently, invoking progress(done,total)
// after each result. Returns mirrors sorted fastest-first.
func benchAll(mirrors []Mirror, timeout, concurrency int, progress func(done, total int)) []Mirror {
	if concurrency <= 0 {
		concurrency = 12
	}
	type job struct {
		idx int
		m   Mirror
	}
	jobs := make(chan job)
	results := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				j.m = benchmark(j.m, timeout)
				results <- j
			}
		}()
	}
	go func() {
		for i, m := range mirrors {
			jobs <- job{idx: i, m: m}
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	out := make([]Mirror, len(mirrors))
	copy(out, mirrors)
	done := 0
	total := len(mirrors)
	for r := range results {
		out[r.idx] = r.m
		done++
		if progress != nil {
			progress(done, total)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ao := a.LatencyMS >= 0
		bo := b.LatencyMS >= 0
		if ao != bo {
			return ao
		}
		if ao && bo {
			return a.SpeedKBps > b.SpeedKBps
		}
		return false
	})
	return out
}

func timedGet(client *http.Client, url string, timeout int) (latencyMS int, speedKBps float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("User-Agent", "hako-mirror-bench/1.0")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, 0, false
	}
	ttfb := time.Since(start)
	// Read up to 1.5 MB to gauge throughput.
	const limit = 1_500_000
	n, _ := io.CopyN(io.Discard, resp.Body, limit)
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	kbps := (float64(n) / 1024.0) / elapsed
	return int(ttfb.Milliseconds()), kbps, true
}

func detectArch() string {
	out, err := run("dpkg", "--print-architecture")
	if err != nil {
		return "aarch64"
	}
	return strings.TrimSpace(out)
}

// currentMirror reads the active MAIN url from sources.list.
func currentMirror() string {
	f, err := os.Open(sourcesList)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "deb ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

// applyMirror writes the chosen mirror into sources.list.
func applyMirror(m Mirror) error {
	content := "# Managed by hako\ndeb " + strings.TrimRight(m.Main, "/") + " stable main\n"
	return os.WriteFile(sourcesList, []byte(content), 0644)
}
