# AGENTS.md

Guide for agents working in the **hako** (箱) codebase — a single-binary Go CLI + TUI that wraps `apt`/`dpkg` on Termux (Android).

## Build & Run

```sh
go build -o hako            # build the binary (a committed ~11MB `hako` binary also exists in-repo)
./hako                      # no args → launch TUI (requires a real TTY)
./hako <command> [args]     # CLI mode
./hako help                 # full command list
./hako version              # prints "hako 1.0.0"
```

- Module: `hako`, declared in `go.mod` with `go 1.26.5` (a very recent Go toolchain is required).
- **No tests, no Makefile, no lint config, no CI.** Do not invent `make`/`npm test`/`pytest` commands — there are none. Verification = `go build ./...` and running the binary.
- All source is a single `package main` split across files in the repo root. There are no subpackages.

## Architecture / Control Flow

```
main.go          loadConfig() → applyTheme(cfg.Accent) → runTUI(cfg) | dispatch(args, cfg)
   │
   ├── config.go      Config struct, TOML at ~/.config/hako/config.toml (auto-created on first run)
   ├── styles.go      package-global lipgloss colors/styles; applyTheme() mutates them at startup
   ├── apt.go         headless apt/dpkg execution: runAptQuiet, CLI spinner, aptResult.summary()
   ├── backend.go     data layer: Package / Detail / Stats; shells out to apt-cache, dpkg-query
   ├── mirrors.go     mirror discovery + concurrent HTTP benchmark; reads/writes Termux sources.list
   ├── cli.go         dispatch() switch + all package/search/show/list/stats commands + sh() helper
   ├── cli_mirror.go  `mirror` and `config` subcommands + CLI progress bar
   ├── tui.go         Bubble Tea model: messages, commands, key handlers for 4 tabs + executing overlay
   └── view.go        TUI rendering, layout math, string utils (truncate/padRight/padLeft/wrap)
```

Two entry modes share the same backend + config + styles:

- **CLI** (`dispatch` in `cli.go`): a `switch` on `args[0]` with many aliases (`install`/`i`/`add`/`in`, `remove`/`rm`/`un`/`del`/`r`, etc.). **Unknown commands fall through to `cmdSearch(args, cfg)`** — any unrecognized first arg is treated as a search query.
- **TUI** (`tui.go`): Bubble Tea `model` with four tabs (`tabSearch`, `tabInstalled`, `tabMirrors`, `tabStats`, in that order — `Tab` cycles +1, `Shift+Tab` +3). Detail view is a modal overlay (`showDetail` bool) that intercepts keys before tab handlers. The executing overlay (`executing` bool) is shown while apt runs headless in the background and absorbs all keys except `ctrl+c`.

### How apt actually gets invoked (headless — no apt UI ever reaches the user)

apt's progress bars and interactive prompts are suppressed entirely. hako owns the UI.

- **`runAptQuiet(args...)`** (`apt.go`): executes `apt -y <args>` with `DEBIAN_FRONTEND=noninteractive`, `DEBIAN_PRIORITY=critical`, `DPKG_HEADLESS=1`, `APT_LISTCHANGES_FRONTEND=none`, `APT_LISTBUGS_FRONTEND=none`, capturing stdout/stderr into an `aptResult` (never printed live). Returns exit code via `aptResult.code`.
- **`runDpkgQuiet(args...)`** (`apt.go`): same pattern for direct `dpkg` calls.
- **`runAptWithSpinner(msg, args...)`** (`apt.go`): wraps `runAptQuiet` with a CLI braille spinner (`cliSpinner`, 80ms frames, only animates on a TTY), then prints `✓ <msg>` on success or `✗ <msg> failed` + a trimmed `aptResult.summary()` on failure. **This is what every CLI package action uses.**
- **`execPkg(action, name)`** (`tui.go`): returns `(label, tea.Cmd)`. The cmd runs `runAptQuiet` (no spinner — the TUI renders its own `viewExecuting` overlay) and emits `execDoneMsg{label, result}`. `model.startExec` batches the cmd with `spin.Tick` and sets `m.executing = true`. On `execDoneMsg`, the overlay is torn down, a status flash is set (green on success, red + `summary()` on failure), and `reloadCmd` refreshes the package index.
- **`sh(cmdstr)`** (`cli.go`): still wires stdio through to `bash -lc`, but is now reserved for the few commands that genuinely hand the terminal to a real process (`dpkg -L`, `apt clean`, `apt autoclean`, `config edit`). **Do not use `sh` for install/remove/upgrade** — use `runAptWithSpinner` so apt stays headless.
- **Backend reads**: `run(name, args...)` in `backend.go:35` uses `exec.Command(...).Output()` (captures stdout only). Used for `apt-cache search/show/depends/rdepends` and `dpkg-query`.

### `aptResult.summary()` (`apt.go`)
On non-zero exit, extracts the meaningful tail of stderr (falls back to stdout), drops `Progress:`/`Reading:` noise lines, and keeps the last 6 lines. Used by both `runAptWithSpinner` (CLI failure output) and the TUI's `execDoneMsg` handler (status flash).

## Config

`Config` (`config.go`) is TOML at `~/.config/hako/config.toml`. `loadConfig()` **writes defaults on first run** (any read error → save defaults and return them). Keys: `auto_confirm`, `auto_mirror`, `preferred_region`, `accent` (hex), `mirror` (pinned MAIN url), `bench_timeout` (sec, default 8), `bench_top_n` (default 5), `fuzzy_search` (default true).

`config set <key> <value)` parses booleans as `true|1|yes|on`. `config edit` hardcodes `nano` as the editor.

## Gotchas & Non-Obvious Patterns

### Hardcoded Termux paths (`mirrors.go:17-20`)
```go
mirrorBaseDir = "/data/data/com.termux/files/usr/etc/termux/mirrors"
sourcesList   = "/data/data/com.termux/files/usr/etc/apt/sources.list"
```
The mirror loader walks that directory tree; the region is derived from the parent dir name (`asia`, `europe`, `default`, …). **This binary will not function outside a Termux environment.** `detectArch()` shells out to `dpkg --print-architecture` and falls back to `aarch64`.

### `applyTheme` mutates package globals (`styles.go`)
`cPink`, `activeTabStyle`, `selItemStyle`, `spinnerStyle`, `searchPromptStyle`, etc. are package-level `lipgloss` values reassigned at startup. **`applyTheme(cfg.Accent)` must run before any render call** — `main.go` does this immediately after `loadConfig()`.

### Search honors the `fuzzy_search` config flag
Both CLI (`cmdSearch` in `cli.go`) and TUI (`recomputeResults` in `tui.go`) consult `cfg.FuzzySearch`: when true (default) they use `sahilm/fuzzy` name matching; when false they fall back to substring name matching. Both then append description-substring matches when name hits are few (CLI: `< 15`, TUI: `< 20`).

### `parseShow` reads only the first stanza (`backend.go`)
`break` on the first empty line means alternate package versions in `apt-cache show` output are ignored; only the first version's metadata is captured. `Version` is only set if still empty.

### `installedMap` status filter (`backend.go`)
Uses `dpkg-query -f '${db:Status-Abbrev}'` and keeps only entries whose status starts with `ii` (properly installed). Other states (held, half-configured, etc.) are dropped.

### Mirror benchmark sorting
`benchAll` (`mirrors.go`) uses `sort.SliceStable`: reachable mirrors first (`LatencyMS >= 0`), then by `SpeedKBps` descending. Unreachable ones keep their input order at the bottom. `LatencyMS = -1` is the sentinel for "unreachable" — check with `>= 0`, never `== 0`.

`autoSelectMirror(cfg)` (no `quiet` parameter) picks the first reachable mirror from the sorted list (i.e. fastest). `PreferredRegion` filtering also keeps `default`-region mirrors (`cli_mirror.go`).

### `mirror set` argument forms (`cli_mirror.go`)
Accepts either a full URL (`http://`/`https://` prefix → used verbatim, name = url) or a fuzzy substring match against mirror `Name`/`Main` (case-insensitive, first match wins).

### `AutoMirror` side effect
When `cfg.AutoMirror` is true, `cmdInstall` and `cmdUpgrade` call `autoSelectMirror(cfg)` **before** running apt.

### `dispatch` unknown-arg fallback (`cli.go`)
`default:` in the switch calls `cmdSearch(args, cfg)` with the **full original args**, so `hako ffmpeg` runs a search for "ffmpeg". Add new commands as explicit cases or they'll be treated as search queries.

### TUI executing overlay (`tui.go`)
While `m.executing` is true, `handleKey` absorbs every key except `ctrl+c` (quit) — the user cannot drive tabs/detail underneath while apt runs. `viewExecuting` (`view.go`) renders a centered panel with the spinner + action label. The overlay is cleared in the `execDoneMsg` handler, which also sets a status flash and triggers `reloadCmd`.

### TUI detail modal key handling (`tui.go`)
When `showDetail` is true (and not executing), keys are handled before tab switching. `i`/`r`/`R` go through `startExec` (which sets `executing` and dismisses the detail modal); `esc`/`q` close the modal. `j`/`k`/`↑`/`↓` scroll the detail viewport.

### Layout constants (`view.go`)
`headerLines = 2`, `footerLines = 2`; `bodyHeight()` = `h - headerLines - footerLines - 1`, clamped to a minimum of 3. The detail viewport is sized `w-8` × `h-8` (clamped to 20×6) in `layoutDetail()`. The executing panel width is `max(w-16, 30)`.

## Naming & Style Conventions

- **File naming**: `cli.go` for general CLI, `cli_mirror.go` for the `mirror`/`config` subcommands, `view.go` for TUI rendering — split a file when one gets large (~300+ lines).
- **Function prefixes**: `cmd*` = CLI command handlers (`cmdInstall`, `cmdSearch`, `cmdMirror`); `key*` = TUI per-tab key handlers (`keySearch`, `keyInstalled`, `keyMirrors`); `view*` = TUI render methods; `parse*` = output parsers; `*Cmd` (suffix) = tea.Cmd factories (`loadCmd`, `detailCmd`, `benchCmd`).
- **Messages**: lowercase struct names ending in `Msg` (`loadedMsg`, `benchOneMsg`, `execDoneMsg`, `clearStatusMsg`).
- **Styles**: `c<Color>` for `lipgloss.Color` globals (`cPink`, `cGreen`, …), `<name>Style` for `lipgloss.Style` globals (`titleStyle`, `accentSty`). Short aliases (`okStyle`, `errStyle`, `accentSty`) are declared in `cli.go`; the full set lives in `styles.go`.
- **Output helpers** (`cli.go`): `ok`/`fail`/`step`/`warn` print prefixed, colored lines (`✓`/`✗`/`→`/`!`). `fail` writes to stderr; the others to stdout. Use these for all user-facing CLI messages.
- **String utils** (`view.go`): `truncate` (rune-safe, appends `…`), `padRight`/`padLeft` (use `lipgloss.Width` for display width, not rune count), `wrap` (word-wrap respecting existing newlines), `humanKB` (KB/MB/GB formatting). Reuse these instead of writing new ones.
- No comments unless a non-obvious *why* needs explaining; the existing code follows this.

## Adding a New CLI Command

1. Add a `case "name", "alias":` to the `switch` in `dispatch` (`cli.go:34`) — **before the `default:`** or it becomes a search query.
2. Write `func cmdYourCmd(args []string, cfg *Config) int` returning an exit code. Use `ok`/`fail`/`step`/`warn` for output. Use `sh("apt ...")` to run apt and return its exit code.
3. Add a line to `printHelp` (`cli.go:295`) in the appropriate section.
4. If it needs new config, add the field to `Config` (`config.go`), a `case` to `setConfigKey` (`cli_mirror.go:227`), and a line to `printConfig` (`cli_mirror.go:213`).

## Adding a TUI Tab

1. Add a constant to the `tab` iota and a name to `tabNames` (`tui.go:21-28`) — order matters for `Tab`/`Shift+Tab` cycling.
2. Add a `case` to the `m.tab` switch in `View()` (`view.go:51`), a `subHeader` case (`view.go:82`), a `footer` help case (`view.go:306`), and a per-tab key handler dispatched from `handleKey` (`tui.go:291`).
3. Add cursor/offset fields to `model` and reuse `moveCursor` (`tui.go:415`) for navigation.
4. Load data via a `*Cmd` tea.Cmd factory and a corresponding `*Msg` handler in `Update`.
