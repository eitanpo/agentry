package schema

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type inner struct {
	Deep string `json:"deep"`
}

type embedded struct {
	Shared string `json:"shared"`
}

type node struct {
	Name     string `json:"name"`
	Children []node `json:"children,omitempty"`
	Parent   *node  `json:"parent,omitempty"`
	Ignored  string `json:"-"`
	unseen   string //nolint:unused // an unexported field is not serialized
	Untagged int
	When     time.Time      `json:"when"`
	Count    int            `json:"count"`
	Ratio    float64        `json:"ratio"`
	Flag     bool           `json:"flag"`
	Nested   inner          `json:"nested"`
	List     []string       `json:"list,omitempty"`
	Lookup   map[string]int `json:"lookup,omitempty"`
	embedded
}

func fieldNamed(t *testing.T, fields []Field, name string) Field {
	t.Helper()
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %q in %v", name, names(fields))
	return Field{}
}

func names(fields []Field) []string {
	var out []string
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

// TestFieldsReadTheSerializedNames pins that the description uses the names a
// consumer sees rather than the Go ones. A description in Go names would be
// wrong at every key a tag renames, which is most of them.
func TestFieldsReadTheSerializedNames(t *testing.T) {
	fields := Document("v", "n", node{}).Fields
	for _, want := range []string{"name", "when", "count", "ratio", "flag", "nested", "shared"} {
		fieldNamed(t, fields, want)
	}
	// An embedded struct with no name of its own contributes its keys at this
	// level, which is where a consumer finds them.
	if got := fieldNamed(t, fields, "shared").Type; got != "string" {
		t.Errorf("embedded field type = %q, want string", got)
	}
	// A field with no tag keeps its Go name, the encoder's own rule.
	fieldNamed(t, fields, "Untagged")
}

// TestFieldsOmitWhatIsNotSerialized pins that a key absent from the output is
// absent from its description. A described key that never appears is worse than
// no description: a consumer writes a query against it and gets nothing back.
func TestFieldsOmitWhatIsNotSerialized(t *testing.T) {
	for _, gone := range []string{"-", "Ignored", "unseen"} {
		for _, f := range Document("v", "n", node{}).Fields {
			if f.Name == gone {
				t.Errorf("%q is described but not serialized", gone)
			}
		}
	}
}

// TestOptionalMarksTheKeysThatVanish pins the one thing a list of key names
// cannot say on its own: which keys a consumer has to branch on the absence of.
func TestOptionalMarksTheKeysThatVanish(t *testing.T) {
	fields := Document("v", "n", node{}).Fields
	if f := fieldNamed(t, fields, "children"); !f.Optional {
		t.Error("children is tagged omitempty and is not marked optional")
	}
	if f := fieldNamed(t, fields, "name"); f.Optional {
		t.Error("name is always present and is marked optional")
	}
}

// TestRecursionStopsAndSaysSo pins the walk's termination. A type that holds
// itself — a tool call's subagent stream holds the events its parent holds —
// would otherwise descend until the stack gave out.
func TestRecursionStopsAndSaysSo(t *testing.T) {
	fields := Document("v", "n", node{}).Fields
	children := fieldNamed(t, fields, "children")
	if !children.Recursive {
		t.Error("a node's children are nodes and the field is not marked recursive")
	}
	if len(children.Fields) != 0 {
		t.Errorf("the walk descended into a recursive field: %v", names(children.Fields))
	}
	if p := fieldNamed(t, fields, "parent"); !p.Recursive {
		t.Error("a pointer back to the same type is not marked recursive")
	}
	// A different struct is still descended into: stopping on any struct would
	// make the description one level deep and useless.
	nested := fieldNamed(t, fields, "nested")
	if len(nested.Fields) != 1 || nested.Fields[0].Name != "deep" {
		t.Errorf("a non-recursive struct was not described: %v", names(nested.Fields))
	}
}

// TestTypeNamesSpeakTheFormat pins that types are named in JSON's vocabulary
// rather than Go's. A consumer reading "int64" learns nothing they can act on,
// and a timestamp called "string" hides that it parses.
func TestTypeNamesSpeakTheFormat(t *testing.T) {
	fields := Document("v", "n", node{}).Fields
	cases := map[string]string{
		"count":  "integer",
		"ratio":  "number",
		"flag":   "boolean",
		"name":   "string",
		"when":   "string (RFC 3339)",
		"nested": "object",
		"list":   "array of string",
		"lookup": "object of integer",
	}
	for key, want := range cases {
		if got := fieldNamed(t, fields, key).Type; got != want {
			t.Errorf("%s: type = %q, want %q", key, got, want)
		}
	}
	// A timestamp is a leaf, not an object with unexported innards.
	if f := fieldNamed(t, fields, "when"); len(f.Fields) != 0 {
		t.Errorf("a timestamp was walked into: %v", names(f.Fields))
	}
}

// TestKindAndVerbAreCarried pins the two labels a caller narrows by: the command
// that writes a shape, and whether it is a whole document or one stream line.
func TestKindAndVerbAreCarried(t *testing.T) {
	doc := Document("view", "agentry view --format json", node{})
	if doc.Kind != "document" || doc.Verb != "view" {
		t.Errorf("document = %+v, want kind document and verb view", doc)
	}
	rec := Record("search", "hit", node{})
	if rec.Kind != "record" || rec.Verb != "search" {
		t.Errorf("record = %+v, want kind record and verb search", rec)
	}
	// A document that is a list of objects says so, since a consumer indexes it
	// rather than reading keys off it.
	if got := Document("list", "n", []node{}).Type; got != "array of object" {
		t.Errorf("array document type = %q, want array of object", got)
	}
}

// TestOmitDropsOneKeyAndKeepsItsSiblings pins the pruner used where a type
// permits a key the emitter always clears.
func TestOmitDropsOneKeyAndKeepsItsSiblings(t *testing.T) {
	sh := Record("v", "n", node{}, Omit("nested.deep"))
	nested := fieldNamed(t, sh.Fields, "nested")
	for _, f := range nested.Fields {
		if f.Name == "deep" {
			t.Error("the omitted key is still described")
		}
	}
	// Its siblings and its parent survive: pruning one key must not prune the
	// object it sat in.
	fieldNamed(t, sh.Fields, "name")
	fieldNamed(t, sh.Fields, "count")

	top := Record("v", "n", node{}, Omit("count"))
	for _, f := range top.Fields {
		if f.Name == "count" {
			t.Error("a top-level omission did not apply")
		}
	}
	fieldNamed(t, top.Fields, "name")
}

// TestOmitPanicsOnAPathThatMatchesNothing pins a stale omission as a bug rather
// than a silent no-op. A path that stops matching means the field was renamed or
// removed, so the claim it was guarding is either stale or now wrong — and a
// pruner that quietly does nothing would leave the description overstating what
// a record carries, which is the failure this package exists to avoid.
func TestOmitPanicsOnAPathThatMatchesNothing(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a path matching nothing was accepted")
		}
		if !strings.Contains(fmt.Sprint(r), "no field") {
			t.Errorf("panic %v does not say what went wrong", r)
		}
	}()
	Record("v", "n", node{}, Omit("nested.gone"))
}
