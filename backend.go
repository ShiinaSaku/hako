package main

import (
	"bufio"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Package is a lightweight record used in list views.
type Package struct {
	Name      string
	Desc      string
	Version   string
	SizeKB    int64 // installed size in KB (0 if unknown)
	Installed bool
}

// Detail holds rich metadata for a single package.
type Detail struct {
	Name        string
	Version     string
	Section     string
	SizeKB      int64
	Homepage    string
	Description string
	Depends     []string
	RDepends    []string
	Installed   bool
	InstVersion string
}

// run executes a command and returns stdout (trimmed of trailing newline).
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// installedMap returns installed package name -> Package (version, size).
func installedMap() map[string]Package {
	m := make(map[string]Package)
	out, err := run("dpkg-query", "-W",
		"-f", "${Package}\t${Version}\t${Installed-Size}\t${db:Status-Abbrev}\n")
	if err != nil {
		return m
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 4 {
			continue
		}
		// Status-Abbrev like "ii " means properly installed.
		if !strings.HasPrefix(f[3], "ii") {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64)
		m[f[0]] = Package{
			Name:      f[0],
			Version:   f[1],
			SizeKB:    size,
			Installed: true,
		}
	}
	return m
}

// allPackages loads every available package (name + description) and marks
// which ones are installed. Loaded once at startup for instant local search.
func allPackages() []Package {
	inst := installedMap()
	out, err := run("apt-cache", "search", ".")
	pkgs := make([]Package, 0, 4096)
	if err == nil {
		sc := bufio.NewScanner(strings.NewReader(out))
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			name, desc, _ := strings.Cut(line, " - ")
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			p := Package{Name: name, Desc: strings.TrimSpace(desc)}
			if ip, ok := inst[name]; ok {
				p.Installed = true
				p.Version = ip.Version
				p.SizeKB = ip.SizeKB
			}
			pkgs = append(pkgs, p)
		}
	}
	// Include installed packages not present in the cache list (rare).
	seen := make(map[string]bool, len(pkgs))
	for _, p := range pkgs {
		seen[p.Name] = true
	}
	for name, ip := range inst {
		if !seen[name] {
			pkgs = append(pkgs, ip)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs
}

// installedList returns only installed packages, sorted by size desc.
func installedList() []Package {
	m := installedMap()
	list := make([]Package, 0, len(m))
	for _, p := range m {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].SizeKB > list[j].SizeKB })
	return list
}

// packageDetail gathers rich metadata via apt-cache.
func packageDetail(name string) Detail {
	d := Detail{Name: name}
	if out, err := run("apt-cache", "show", name); err == nil {
		parseShow(out, &d)
	}
	if out, err := run("apt-cache", "depends", "--no-recommends", "--no-suggests", name); err == nil {
		d.Depends = parseDepends(out)
	}
	if out, err := run("apt-cache", "rdepends", name); err == nil {
		d.RDepends = parseRDepends(out)
	}
	// installed version
	if out, err := run("dpkg-query", "-W", "-f", "${Version}\t${db:Status-Abbrev}", name); err == nil {
		f := strings.Split(out, "\t")
		if len(f) == 2 && strings.HasPrefix(f[1], "ii") {
			d.Installed = true
			d.InstVersion = f[0]
		}
	}
	return d
}

func parseShow(out string, d *Detail) {
	sc := bufio.NewScanner(strings.NewReader(out))
	inDesc := false
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break // end of first stanza
		}
		if inDesc && strings.HasPrefix(line, " ") {
			d.Description += "\n" + strings.TrimSpace(line)
			continue
		}
		inDesc = false
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Version":
			if d.Version == "" {
				d.Version = val
			}
		case "Section":
			d.Section = val
		case "Homepage":
			d.Homepage = val
		case "Installed-Size":
			d.SizeKB, _ = strconv.ParseInt(strings.Fields(val)[0], 10, 64)
		case "Description":
			d.Description = val
			inDesc = true
		}
	}
}

func parseDepends(out string) []string {
	var deps []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "Depends:") {
			deps = append(deps, strings.TrimSpace(strings.TrimPrefix(line, "Depends:")))
		}
	}
	return dedupe(deps)
}

func parseRDepends(out string) []string {
	var deps []string
	sc := bufio.NewScanner(strings.NewReader(out))
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first { // header "pkg:"
			first = false
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Reverse Depends:") {
			continue
		}
		deps = append(deps, line)
	}
	return dedupe(deps)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Stats summarises installed footprint.
type Stats struct {
	Count   int
	TotalKB int64
	Largest []Package
}

func computeStats() Stats {
	list := installedList() // already sorted by size desc
	var total int64
	for _, p := range list {
		total += p.SizeKB
	}
	top := list
	if len(top) > 15 {
		top = top[:15]
	}
	return Stats{Count: len(list), TotalKB: total, Largest: top}
}

func humanKB(kb int64) string {
	f := float64(kb)
	switch {
	case f >= 1024*1024:
		return strconv.FormatFloat(f/1024/1024, 'f', 1, 64) + " GB"
	case f >= 1024:
		return strconv.FormatFloat(f/1024, 'f', 1, 64) + " MB"
	default:
		return strconv.FormatInt(kb, 10) + " KB"
	}
}
