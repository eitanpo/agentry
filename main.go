// Command agentry renders a Claude Code session log to the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/eitanpo/agentry/internal/list"
	"github.com/eitanpo/agentry/internal/locate"
	"github.com/eitanpo/agentry/internal/model"
	"github.com/eitanpo/agentry/internal/parse"
	"github.com/eitanpo/agentry/internal/render"
	"golang.org/x/term"
)

// Version is the base version. `make build`/`make install` override it via -ldflags with a
// UTC build timestamp appended as semver build metadata (e.g. 0.1.0+20260527T131005Z);
// release builds set it from the git tag. Plain `go build`/`go install` use this bare value.
var Version = "0.2.0"

// sysexits.h codes.
const (
	exUsage   = 64 // command-line usage error
	exNoInput = 66 // input (project/session) does not exist
)

// levels maps each verbosity preset to the channels it enables.
var levels = map[string]render.Channels{
	"minimal":  {},
	"standard": {Thinking: true},
	"detailed": {Thinking: true, Tools: true, Metrics: true},
	"full":     {Thinking: true, Tools: true, Subagents: true, Metrics: true},
}

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("agentry", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = usage

	level := fs.String("level", "minimal", "verbosity: minimal|standard|detailed|full")
	noColor := fs.Bool("no-color", false, "disable color (also honors NO_COLOR)")
	showVersion := fs.Bool("version", false, "print version and exit")
	listMode := fs.Bool("list", false, "list this project's sessions instead of rendering one")
	limit := fs.Int("limit", 10, "with --list, cap to N most-recent sessions (0 = no cap)")
	since := fs.String("since", "", "with --list, only sessions active at or after WHEN")
	until := fs.String("until", "", "with --list, only sessions active at or before WHEN")
	include := fs.String("include", "", "with --list, add detail channels (comma-separated): prompts")
	// Channel overrides are presence flags; their value is read via fs.Visit.
	for _, ch := range []string{"thinking", "tools", "subagents", "metrics"} {
		fs.Bool(ch, false, "show "+ch)
		fs.Bool("no-"+ch, false, "hide "+ch)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return exUsage
	}

	if *showVersion {
		fmt.Println("agentry " + Version)
		return 0
	}

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if *listMode {
		return runList(set, *limit, *since, *until, *include, *noColor, fs.NArg())
	}

	channels, ok := levels[*level]
	if !ok {
		fmt.Fprintf(os.Stderr, "agentry: invalid --level %q (want minimal|standard|detailed|full)\n", *level)
		return exUsage
	}
	applyChannel(&channels.Thinking, set, "thinking")
	applyChannel(&channels.Tools, set, "tools")
	applyChannel(&channels.Subagents, set, "subagents")
	applyChannel(&channels.Metrics, set, "metrics")

	var id string
	switch fs.NArg() {
	case 0:
	case 1:
		id = fs.Arg(0)
	default:
		fmt.Fprintln(os.Stderr, "agentry: expected at most one session id")
		return exUsage
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return exNoInput
	}
	path, err := locate.Session(cwd, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return exNoInput
	}

	sess, err := parse.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return exNoInput
	}

	color, width := terminal(*noColor)
	if err := render.Session(os.Stdout, sess, render.Options{
		Width: width, Color: color, Channels: channels,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return 1
	}
	return 0
}

// runList handles --list: resolve the project's sessions, summarize each, filter
// and order them, and print one row per session. When a time filter is set and
// --limit was not given explicitly, the cap is lifted.
func runList(set map[string]bool, limit int, since, until, include string, noColor bool, nargs int) int {
	if nargs > 0 {
		fmt.Fprintln(os.Stderr, "agentry: --list takes no session id")
		return exUsage
	}

	var showPrompts bool
	for _, tok := range strings.Split(include, ",") {
		switch tok = strings.TrimSpace(tok); tok {
		case "": // empty entries (e.g. unset flag) contribute nothing
		case "prompts":
			showPrompts = true
		case "all":
			showPrompts = true
		default:
			fmt.Fprintf(os.Stderr, "agentry: --include: unknown channel %q (want: prompts, all)\n", tok)
			return exUsage
		}
	}

	now := time.Now()
	var sinceT, untilT time.Time
	if since != "" {
		t, err := list.ParseWhen(since, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agentry: --since: %v\n", err)
			return exUsage
		}
		sinceT = t
	}
	if until != "" {
		t, err := list.ParseWhen(until, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agentry: --until: %v\n", err)
			return exUsage
		}
		untilT = t
	}
	// A time filter without an explicit --limit lifts the default cap, so
	// "--since today" shows every session in the window, not just ten.
	if (set["since"] || set["until"]) && !set["limit"] {
		limit = 0
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return exNoInput
	}
	paths, err := locate.Sessions(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return exNoInput
	}

	var sums []model.Summary
	for _, p := range paths {
		s, err := parse.Summarize(p)
		if err != nil {
			continue // skip a session that won't parse, like a malformed line
		}
		sums = append(sums, s)
	}

	color, width := terminal(noColor)
	if err := list.Render(os.Stdout, list.Select(sums, sinceT, untilT, limit), list.Options{
		Width: width, Color: color, Prompts: showPrompts,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "agentry: %v\n", err)
		return 1
	}
	return 0
}

// applyChannel overrides a channel default with explicit --name / --no-name
// flags. --no-name wins if both are somehow present.
func applyChannel(dst *bool, set map[string]bool, name string) {
	if set[name] {
		*dst = true
	}
	if set["no-"+name] {
		*dst = false
	}
}

// terminal reports whether to colorize and the render width. Color is on only
// for a TTY stdout with NO_COLOR unset and --no-color absent.
func terminal(noColor bool) (color bool, width int) {
	fd := int(os.Stdout.Fd())
	isTTY := term.IsTerminal(fd)
	color = isTTY && !noColor && os.Getenv("NO_COLOR") == ""
	if isTTY {
		if w, _, err := term.GetSize(fd); err == nil {
			width = w
		}
	}
	return color, width
}

func usage() {
	fmt.Fprint(os.Stderr, `agentry — render a Claude Code session log to the terminal

Usage:
  agentry [flags] [session-uuid]
  agentry --list [--limit N] [--since WHEN] [--until WHEN] [--include CHANNELS]

With no argument, renders the most recent session (by modification time) for the
current directory's project. With a full UUID, renders that session. With --list,
lists the project's sessions instead so you can pick one.

Flags:
  --level minimal|standard|detailed|full   how much of each turn to show (default minimal)
  --[no-]thinking|tools|subagents|metrics   override a single channel
  --list                                    list this project's sessions instead of rendering
  --limit N                                 with --list, cap to N most-recent (default 10; 0 = no cap)
  --since WHEN                              with --list, sessions active at or after WHEN
  --until WHEN                              with --list, sessions active at or before WHEN
                                            WHEN: today|yesterday, Nh|Nd|Nw, or YYYY-MM-DD
  --include CHANNELS                        with --list, add detail (comma-separated): prompts, all
  --no-color                                disable color (also honors NO_COLOR)
  --version                                 print version
  --help                                    show this help
`)
}
