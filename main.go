package main

import (
	"os"
)

func main() {
	cfg := loadConfig()
	applyTheme(cfg.Accent)

	args := os.Args[1:]
	if len(args) == 0 {
		if err := runTUI(cfg); err != nil {
			fail("%v", err)
			fail("no interactive terminal — try 'hako help' for CLI usage")
			os.Exit(1)
		}
		return
	}
	os.Exit(dispatch(args, cfg))
}
