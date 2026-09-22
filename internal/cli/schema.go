package cli

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eitanpo/agentry/internal/cost"
	"github.com/eitanpo/agentry/internal/jsonl"
	"github.com/eitanpo/agentry/internal/list"
	"github.com/eitanpo/agentry/internal/render"
	"github.com/eitanpo/agentry/internal/schema"
)

// newSchemaCmd prints the shape of agentry's machine-readable output. The shapes
// were documented only in PRODUCT.md, which an agent piping `--format json` is
// never inside: the keys had to be guessed from a sample, one jq call at a time.
//
// Every shape is read off the Go types the emitters pass, so this cannot drift
// from what a caller receives — which a hand-written schema would, silently, the
// first time a field was added.
func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema [verb|record]",
		Short: "the shape of --format json and --format jsonl output",
		Args:  cobra.MaximumNArgs(1),
		Example: "  agentry schema\n" +
			"  agentry schema view\n" +
			"  agentry schema hit\n" +
			"  agentry schema --format json | jq '.records[] | .name'",
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return schemaSelectors(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchema(cmd, args)
		},
	}
	addFormatFlag(cmd)
	return cmd
}

// allShapes is every document and record agentry emits, each named by the
// package that writes it. Ordered by the verb a caller reaches it through rather
// than alphabetically, so the entry they came for is beside the command they
// typed.
func allShapes() []schema.Shape {
	var out []schema.Shape
	out = append(out, render.Shapes()...)
	out = append(out, list.Shapes()...)
	out = append(out, cost.Shapes()...)
	out = append(out,
		schema.Document("config", "agentry config --format json", configReport{}),
		schema.Record("config", "config", configReport{}),
		// This verb describes its own output too. Leaving it out would reproduce
		// one level up the gap the verb exists to close: a caller piping this into
		// jq would have to guess these keys from a sample, which is the errand
		// that sent them here.
		schema.Document("schema", "agentry schema --format json", schemaReport{}),
		schema.Record("schema", "schema", schemaReport{}),
	)
	return out
}

// schemaReport is what `agentry schema --format json` writes. The envelope is
// described beside the records rather than left implicit: every stream line
// carries it, and a consumer reading `payload` has to know what wraps it.
type schemaReport struct {
	Envelope  []schema.Field `json:"envelope"`
	Documents []schema.Shape `json:"documents"`
	Records   []schema.Shape `json:"records"`
}

// schemaSelectors is every word `agentry schema` narrows by: each verb that
// writes machine-readable output, then each record type by name. Built from the
// shapes themselves, so a new record is narrowable and completable the moment it
// is declared.
func schemaSelectors() []string {
	var out []string
	for _, sh := range allShapes() {
		if !slices.Contains(out, sh.Verb) {
			out = append(out, sh.Verb)
		}
	}
	for _, sh := range allShapes() {
		if sh.Kind == "record" && !slices.Contains(out, sh.Name) {
			out = append(out, sh.Name)
		}
	}
	return out
}

// narrow keeps the shapes a selector names: a verb keeps everything that verb
// writes, a record name keeps that one record. Both are offered because a caller
// arrives from either end — they know which command they ran, or they know which
// record type a stream line said it was.
func narrow(shapes []schema.Shape, selector string) []schema.Shape {
	var out []schema.Shape
	for _, sh := range shapes {
		if sh.Verb == selector || (sh.Kind == "record" && sh.Name == selector) {
			out = append(out, sh)
		}
	}
	return out
}

func runSchema(cmd *cobra.Command, args []string) error {
	format, err := parseFormat(cmd)
	if err != nil {
		return err
	}
	shapes := allShapes()
	if len(args) == 1 {
		shapes = narrow(shapes, args[0])
		if len(shapes) == 0 {
			if g := nearest(args[0], schemaSelectors()); g != "" {
				return usageErr("unknown shape %q — did you mean %q?", args[0], g)
			}
			return usageErr("unknown shape %q (want: %s)", args[0], strings.Join(schemaSelectors(), ", "))
		}
	}
	report := schemaReport{Envelope: jsonl.EnvelopeFields()}
	for _, sh := range shapes {
		if sh.Kind == "document" {
			report.Documents = append(report.Documents, sh)
			continue
		}
		report.Records = append(report.Records, sh)
	}

	out := cmd.OutOrStdout()
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	case "jsonl":
		// One record, which is a well-formed stream — the rule `config` already
		// follows. The shapes arrive together and mean nothing apart.
		if err := jsonl.New(out).Emit("schema", "", time.Time{}, report); err != nil {
			return &exitError{code: 1, err: err}
		}
		return nil
	}
	_, err = fmt.Fprint(out, schemaText(report))
	return err
}

// schemaText lays the shapes out as an indented tree: one key per line, its type
// beside it, its own keys beneath it. A tree rather than JSON Schema because the
// question this answers is what a key is called and what it holds — which is
// what a jq expression needs — where a validation document answers a question
// nobody asked here at five times the length.
func schemaText(r schemaReport) string {
	var b strings.Builder
	// Unconditional: every verb declares at least one record and a document has no
	// name to narrow by, so no invocation reaches this function without one.
	b.WriteString("Every --format jsonl line is an envelope around a payload:\n")
	writeFields(&b, r.Envelope, 1)
	b.WriteString("\n")
	if len(r.Documents) > 0 {
		b.WriteString("Documents — what one --format json run writes:\n")
		for _, sh := range r.Documents {
			fmt.Fprintf(&b, "\n  %s  %s\n", sh.Name, sh.Type)
			writeFields(&b, sh.Fields, 2)
		}
		b.WriteString("\n")
	}
	if len(r.Records) > 0 {
		b.WriteString("Records — one --format jsonl line's payload, by its envelope type:\n")
		for _, sh := range r.Records {
			fmt.Fprintf(&b, "\n  %s (from `agentry %s`)  %s\n", sh.Name, sh.Verb, sh.Type)
			writeFields(&b, sh.Fields, 2)
		}
	}
	return b.String()
}

// writeFields prints one key per line at the given depth, two spaces per level.
// The type column is aligned across the whole block rather than per level, so a
// reader scans types down one edge instead of chasing a ragged one — the nesting
// is already carried by the indent, and letting it move the types too makes the
// indent pay twice.
func writeFields(b *strings.Builder, fields []schema.Field, depth int) {
	width := keyWidth(fields, depth)
	writeFieldsAligned(b, fields, depth, width)
}

// keyWidth is the widest indented key anywhere in the block, which is what the
// type column is aligned to. Length is counted in bytes, which equals display
// width here because every key is a Go struct tag and those are ASCII; a
// non-ASCII key would ragged the column this aligns.
func keyWidth(fields []schema.Field, depth int) int {
	widest := 0
	for _, f := range fields {
		if w := depth*2 + len(f.Name); w > widest {
			widest = w
		}
		if w := keyWidth(f.Fields, depth+1); w > widest {
			widest = w
		}
	}
	return widest
}

func writeFieldsAligned(b *strings.Builder, fields []schema.Field, depth, width int) {
	for _, f := range fields {
		key := strings.Repeat("  ", depth) + f.Name
		note := ""
		if f.Optional {
			note = "  (absent when empty)"
		}
		if f.Recursive {
			note += "  (holds the same shape again)"
		}
		fmt.Fprintf(b, "%-*s  %s%s\n", width, key, f.Type, note)
		writeFieldsAligned(b, f.Fields, depth+1, width)
	}
}
