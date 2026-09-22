package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/eitanpo/agentry/internal/schema"
)

// TestSchemaDescribesEveryRecordTheStreamsEmit is the drift guard, and the
// reason this verb reads the shapes off the Go types rather than off a written
// copy. It runs each verb's stream over a fixture, reads the record types and
// keys the run actually wrote, and holds the description to them.
//
// Two failures it catches: a new Emit with no shape declared for it, which
// leaves a record type nothing describes; and a key written at any depth that
// its shape does not describe, which is how a shape naming the wrong type
// surfaces and how an Omit is caught pruning a key the emitter does write after
// all.
//
// What it does not catch is a shape describing a key nothing writes, since one
// run of one fixture leaves a genuinely optional key absent for honest reasons.
func TestSchemaDescribesEveryRecordTheStreamsEmit(t *testing.T) {
	declared := map[string][]schema.Field{}
	for _, sh := range allShapes() {
		if sh.Kind == "record" {
			declared[sh.Name] = sh.Fields
		}
	}

	const (
		plain    = "sample.jsonl"
		plainID  = "ba6b3ded-475b-4c3a-96fe-99698a557d14"
		nested   = "stitch.jsonl"
		nestedID = "7c1f9a02-3d4e-4b55-8a61-0c2d9e5f4a13"
	)
	runs := []struct {
		what string
		log  string
		id   string
		args []string
	}{
		{"a rendered session", plain, plainID, []string{"view", "--format", "jsonl"}},
		// A session whose delegated calls carry a subagent stream beneath them.
		// Without one, every nested key in an event record is absent for want of
		// data, and the key this verb prunes by hand could come back unnoticed.
		{"a session with a subagent", nested, nestedID, []string{"view", "--format", "jsonl"}},
		{"a search", plain, plainID, []string{"search", "prompt", "--format", "jsonl"}},
		{"a listing", plain, plainID, []string{"list", "--format", "jsonl"}},
		{"the settings report", plain, plainID, []string{"config", "--format", "jsonl"}},
		// Both roll-up forms, because they write disjoint record types: the bare
		// run writes the three scopes and a bucketed one writes the report, its
		// rows and its total. With only one of them here, three of the five cost
		// records were declared and never held to a stream.
		{"a three-scope roll-up", plain, plainID, []string{"cost", "--format", "jsonl"}},
		{"a bucketed roll-up", plain, plainID, []string{"cost", "--by", "month", "--format", "jsonl"}},
		{"this verb itself", plain, plainID, []string{"schema", "--format", "jsonl"}},
	}
	seen := map[string]bool{}
	exercised := map[string]bool{}
	for _, run := range runs {
		exercised[run.args[0]] = true
		t.Run(run.what, func(t *testing.T) {
			searchFixture(t, run.log, run.id)
			code, out, errOut := exec(run.args...)
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatalf("the run wrote nothing, so it pins nothing")
			}
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				var rec struct {
					Type    string         `json:"type"`
					Payload map[string]any `json:"payload"`
				}
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatalf("not a JSON line (%v): %q", err, line)
				}
				seen[rec.Type] = true
				fields, described := declared[rec.Type]
				if !described {
					t.Errorf("the stream wrote a %q record and no shape describes it — declare one in that package's Shapes()", rec.Type)
					continue
				}
				for _, key := range undescribedKeys(rec.Payload, fields, "") {
					t.Errorf("%q record wrote key %q that its shape does not describe", rec.Type, key)
				}
			}
		})
	}

	// Every record declared for a verb these runs exercise has to have been
	// written by one of them, or its description is held to nothing. Derived from
	// the declarations rather than counted against a number: a floor on how many
	// types were seen passes while a particular one goes unexercised, which is
	// exactly what happened to the three cost records above.
	for _, sh := range allShapes() {
		if sh.Kind != "record" || !exercised[sh.Verb] || seen[sh.Name] {
			continue
		}
		t.Errorf("a %q record is described and no run of `agentry %s` wrote one — add the invocation that does to runs", sh.Name, sh.Verb)
	}
}

// undescribedKeys names every key the payload carries that its shape does not
// describe, at any depth. It descends because the keys most at risk are nested:
// Omit prunes one from inside an event's tool object, and a check reading only
// the payload's top level could never see that key come back.
//
// A described key with no keys of its own stops the walk. It is either a leaf or
// a map whose keys are data rather than schema — a cost table keyed by model
// name is the case — and descending into one would report every datum as an
// undescribed key.
func undescribedKeys(payload map[string]any, fields []schema.Field, path string) []string {
	byName := map[string]schema.Field{}
	for _, f := range fields {
		byName[f.Name] = f
	}
	var missing []string
	for key, value := range payload {
		f, described := byName[key]
		if !described {
			missing = append(missing, path+key)
			continue
		}
		// A key described as holding the same shape again ends the walk here, the
		// way the description itself ends there.
		if f.Recursive || len(f.Fields) == 0 {
			continue
		}
		for _, object := range objectsIn(value) {
			missing = append(missing, undescribedKeys(object, f.Fields, path+key+".")...)
		}
	}
	return missing
}

// objectsIn is the objects a decoded value holds: itself where it is one, its
// elements where it is a list of them, and none otherwise.
func objectsIn(value any) []map[string]any {
	switch v := value.(type) {
	case map[string]any:
		return []map[string]any{v}
	case []any:
		var out []map[string]any
		for _, elem := range v {
			if object, ok := elem.(map[string]any); ok {
				out = append(out, object)
			}
		}
		return out
	}
	return nil
}

// TestSchemaOmitsOnlyWhatIsNeverWritten pins the description half of the one
// hand-made claim in this package: the event record's tool object is described
// without the subagent stream, and without losing the siblings beside it.
//
// The other half — that the emitter really does clear that key — is the drift
// guard's, which walks a real stream's nested keys. Reflection cannot see the
// clearing, so neither test alone holds the claim.
func TestSchemaOmitsOnlyWhatIsNeverWritten(t *testing.T) {
	var event schema.Shape
	for _, sh := range allShapes() {
		if sh.Name == "event" {
			event = sh
		}
	}
	if event.Name == "" {
		t.Fatal("no event record is described")
	}
	for _, f := range event.Fields {
		if f.Name != "tool" {
			continue
		}
		for _, inner := range f.Fields {
			if inner.Name == "subagent" {
				t.Error("the event record describes a subagent stream, which the emitter always clears")
			}
		}
		// The rest of the tool object is still described: pruning one key must not
		// prune its siblings.
		if len(f.Fields) < 5 {
			t.Errorf("the tool object lost more than the one key: %d fields left", len(f.Fields))
		}
		return
	}
	t.Error("the event record describes no tool object at all")
}

// TestSchemaNarrows pins both ends a caller arrives from: the command they ran,
// and the record type a stream line said it was.
func TestSchemaNarrows(t *testing.T) {
	code, out, errOut := exec("schema", "hit")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(out, "hit (from `agentry search`)") {
		t.Errorf("narrowing to a record does not name it or its verb: %q", out)
	}
	if strings.Contains(out, "dailyUsage") {
		t.Errorf("narrowing to one record printed another shape's keys: %q", out)
	}
	// A record's description carries the envelope, since a consumer reading
	// `payload` has to know what wraps it.
	if !strings.Contains(out, "ordinal") {
		t.Errorf("a record was described without its envelope: %q", out)
	}

	_, out, _ = exec("schema", "view")
	for _, want := range []string{"agentry view --format json", "meta", "turn", "event"} {
		if !strings.Contains(out, want) {
			t.Errorf("narrowing to a verb omits %q: %q", want, out)
		}
	}
	if strings.Contains(out, "agentry cost") {
		t.Errorf("narrowing to one verb printed another's shapes: %q", out)
	}
}

// TestSchemaMachineFormNamesItsParts pins what a caller piping this verb into jq
// reads: the envelope every stream line carries, the documents, the records, and
// the verb on each shape that they narrow by.
func TestSchemaMachineFormNamesItsParts(t *testing.T) {
	_, out, _ := exec("schema", "--format", "json")
	var report struct {
		Envelope  []schema.Field `json:"envelope"`
		Documents []schema.Shape `json:"documents"`
		Records   []schema.Shape `json:"records"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout is not valid JSON (%v)", err)
	}
	if len(report.Envelope) == 0 {
		t.Error("the machine form describes no envelope")
	}
	if len(report.Documents) == 0 || len(report.Records) == 0 {
		t.Errorf("documents = %d, records = %d, want both", len(report.Documents), len(report.Records))
	}
	// Every shape says which command writes it, which is what a caller narrows by.
	for _, sh := range append(report.Documents, report.Records...) {
		if sh.Verb == "" {
			t.Errorf("shape %q names no verb", sh.Name)
		}
	}
}

// TestSchemaRejectsAnUnknownSelector pins the diagnostic: the offending word,
// the nearest valid one, and no dump of every shape.
func TestSchemaRejectsAnUnknownSelector(t *testing.T) {
	code, out, errOut := exec("schema", "hti")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, `"hti"`) || !strings.Contains(errOut, `"hit"`) {
		t.Errorf("stderr names the token or the suggestion wrong: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}

	code, _, errOut = exec("schema", "nowherenear")
	if code != exUsage {
		t.Errorf("exit = %d, want %d (exUsage)", code, exUsage)
	}
	if !strings.Contains(errOut, "want:") {
		t.Errorf("a token with no near match does not list the choices: %q", errOut)
	}
}
