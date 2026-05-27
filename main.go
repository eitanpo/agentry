// Command ase renders a Claude Code session log to the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/eitanpo/ase/internal/locate"
	"github.com/eitanpo/ase/internal/parse"
	"github.com/eitanpo/ase/internal/render"
	"golang.org/x/term"
)

// Version is the base version. `make build`/`make install` override it via -ldflags with a
// UTC build timestamp appended as semver build metadata (e.g. 0.1.0+20260527T131005Z);
// release builds set it from the git tag. Plain `go build`/`go install` use this bare value.
var Version = "0.1.0"

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
	fs := flag.NewFlagSet("ase", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = usage

	level := fs.String("level", "detailed", "verbosity: minimal|standard|detailed|full")
	noColor := fs.Bool("no-color", false, "disable color (also honors NO_COLOR)")
	showVersion := fs.Bool("version", false, "print version and exit")
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
		fmt.Println("ase " + Version)
		return 0
	}

	channels, ok := levels[*level]
	if !ok {
		fmt.Fprintf(os.Stderr, "ase: invalid --level %q (want minimal|standard|detailed|full)\n", *level)
		return exUsage
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
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
		fmt.Fprintln(os.Stderr, "ase: expected at most one session id")
		return exUsage
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ase: %v\n", err)
		return exNoInput
	}
	path, err := locate.Session(cwd, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ase: %v\n", err)
		return exNoInput
	}

	sess, err := parse.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ase: %v\n", err)
		return exNoInput
	}

	color, width := terminal(*noColor)
	if err := render.Session(os.Stdout, sess, render.Options{
		Width: width, Color: color, Channels: channels,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ase: %v\n", err)
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
	fmt.Fprint(os.Stderr, `ase — render a Claude Code session log to the terminal

Usage:
  ase [flags] [session-uuid]

With no argument, renders the most recent session (by modification time) for the
current directory's project. With a full UUID, renders that session.

Flags:
  --level minimal|standard|detailed|full   how much of each turn to show (default detailed)
  --[no-]thinking|tools|subagents|metrics   override a single channel
  --no-color                                disable color (also honors NO_COLOR)
  --version                                 print version
  --help                                    show this help
`)
}
