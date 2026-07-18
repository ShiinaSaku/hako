package main

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is persisted at ~/.config/hako/config.toml
type Config struct {
	AutoConfirm     bool   `toml:"auto_confirm"`     // pass -y to apt
	AutoMirror      bool   `toml:"auto_mirror"`      // auto-pick fastest mirror before install/upgrade
	PreferredRegion string `toml:"preferred_region"` // limit auto-benchmark to a region ("" = all)
	Accent          string `toml:"accent"`           // hex accent color for TUI/CLI
	Mirror          string `toml:"mirror"`           // pinned mirror MAIN url ("" = none)
	BenchTimeout    int    `toml:"bench_timeout"`    // per-mirror benchmark timeout (seconds)
	BenchTopN       int    `toml:"bench_top_n"`      // how many fastest to consider for auto
	FuzzySearch     bool   `toml:"fuzzy_search"`     // fuzzy vs substring search in CLI
}

func defaultConfig() Config {
	return Config{
		AutoConfirm:     false,
		AutoMirror:      false,
		PreferredRegion: "",
		Accent:          "#FF6AC1",
		Mirror:          "",
		BenchTimeout:    8,
		BenchTopN:       5,
		FuzzySearch:     true,
	}
}

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "hako")
}

func configPath() string { return filepath.Join(configDir(), "config.toml") }

// loadConfig reads config, creating defaults on first run.
func loadConfig() *Config {
	cfg := defaultConfig()
	path := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		_ = saveConfig(&cfg) // write defaults, ignore error
		return &cfg
	}
	_ = toml.Unmarshal(data, &cfg)
	if cfg.BenchTimeout <= 0 {
		cfg.BenchTimeout = 8
	}
	if cfg.BenchTopN <= 0 {
		cfg.BenchTopN = 5
	}
	if cfg.Accent == "" {
		cfg.Accent = "#FF6AC1"
	}
	return &cfg
}

func saveConfig(cfg *Config) error {
	if err := os.MkdirAll(configDir(), 0755); err != nil {
		return err
	}
	f, err := os.Create(configPath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}
