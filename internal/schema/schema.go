// Package schema describes agentry's machine-readable output by reading the Go
// types that produce it.
//
// It exists because the shapes were documented only in PRODUCT.md, which an
// agent piping `--format json` is never inside: the keys had to be guessed from
// a sample, one jq call at a time. Describing them from the types rather than
// from a second written copy is what keeps the answer true — a field added to a
// struct appears here without anyone remembering to say so, which a hand-written
// schema and a hand-maintained example document both fail at silently.
package schema

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Field is one key in a shape: the name it is serialized under, what a consumer
// will find in it, whether it can be absent, and its own keys where it holds an
// object.
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Optional marks a key that is absent rather than empty when the program has
	// no value for it — the `omitempty` half of a struct tag. A consumer has to
	// branch on its absence, which is the one thing a list of key names cannot
	// say on its own.
	Optional bool `json:"optional,omitempty"`
	// Recursive marks a key whose type is one already open further up this shape,
	// where the walk stops rather than descending forever. A tool call's subagent
	// stream is the case: it holds the same events its parent does.
	Recursive bool    `json:"recursive,omitempty"`
	Fields    []Field `json:"fields,omitempty"`
}

// Shape is one whole output: a document a `--format json` run emits, or one
// record type a `--format jsonl` stream carries.
type Shape struct {
	// Verb is the command a caller reaches this shape through. It is carried
	// because a record's own name does not say which verb writes it — nothing in
	// "turn" names the render path — so without it a caller could only narrow to
	// shapes they could already name.
	Verb string `json:"verb"`
	Name string `json:"name"`
	// Kind is "document" or "record". A document is what one run writes; a record
	// is one line of a stream, of which a run writes many.
	Kind   string  `json:"kind"`
	Type   string  `json:"type"`
	Fields []Field `json:"fields,omitempty"`
}

// Omit drops one key from a shape's description, named by its dotted path
// ("tool.subagent"). It exists because a type can permit a key the emitter
// always clears: the stream's event record carries a tool object whose nested
// subagent stream is nilled out before the line is written, those events
// following as records of their own. Reflection cannot see that, and a described
// key that never appears is worse than an undescribed one — a consumer writes a
// query against it and gets nothing back, which is the errand this package
// exists to spare them.
//
// A path that matches nothing is a programming error rather than a silent
// no-op: it means the field was renamed or removed, and the omission it was
// guarding is either stale or now wrong.
func Omit(path string) func(*Shape) error {
	return func(sh *Shape) error {
		fields, dropped := without(sh.Fields, strings.Split(path, "."))
		if !dropped {
			return fmt.Errorf("schema: %s has no field %q to omit", sh.Name, path)
		}
		sh.Fields = fields
		return nil
	}
}

// without returns the fields with the one at path removed, and whether it found
// it. The walk is by serialized name, the same names the description prints, so
// a path reads as the jq expression a consumer would have written.
func without(fields []Field, path []string) ([]Field, bool) {
	dropped := false
	var out []Field
	for _, f := range fields {
		if f.Name != path[0] {
			out = append(out, f)
			continue
		}
		if len(path) == 1 {
			dropped = true
			continue
		}
		inner, found := without(f.Fields, path[1:])
		f.Fields = inner
		dropped = dropped || found
		out = append(out, f)
	}
	return out, dropped
}

// Document describes what one `--format json` run writes. verb is the command
// that writes it and name is the invocation, as a caller types it.
func Document(verb, name string, v any, opts ...func(*Shape) error) Shape {
	return shapeOf(verb, name, "document", v, opts...)
}

// Record describes one `--format jsonl` line's payload. name is the record's own
// type — the `type` field on its envelope, which is how a consumer tells the
// lines of a stream apart.
func Record(verb, name string, v any, opts ...func(*Shape) error) Shape {
	return shapeOf(verb, name, "record", v, opts...)
}

// Fields describes one type's keys without wrapping them in a Shape, for a
// structure that is neither a document nor a record. The stream's envelope is
// the case: every record rides inside it, and the package that owns the format
// keeps its type unexported, so it describes it here rather than letting a
// second hand-written copy of the same keys drift from it.
func Fields(v any) []Field {
	t := reflect.TypeOf(v)
	// The root type is opened before the walk starts. Left out, a type that holds
	// itself directly gets described one level deeper than it should be, and the
	// extra level reads as real nesting rather than as the same shape again.
	return fieldsOf(t, []reflect.Type{structOf(t)})
}

func shapeOf(verb, name, kind string, v any, opts ...func(*Shape) error) Shape {
	sh := Shape{Verb: verb, Name: name, Kind: kind, Type: typeName(reflect.TypeOf(v)), Fields: Fields(v)}
	for _, opt := range opts {
		if err := opt(&sh); err != nil {
			// A stale omission is a bug in this package's callers, not a condition a
			// caller of agentry can act on, and returning it would put an error
			// path on every shape declaration. Panicking fails the test that walks
			// every shape, which is where it belongs.
			panic(err)
		}
	}
	return sh
}

// structOf reduces a type to the struct a shape is described from, seeing
// through pointers and through the slice a document of many objects is.
func structOf(t reflect.Type) reflect.Type {
	t = deref(t)
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = deref(t.Elem())
	}
	return t
}

// timeType is matched by identity rather than by walking its fields, which are
// unexported and would describe none of what a consumer actually receives.
var timeType = reflect.TypeOf(time.Time{})

// fieldsOf walks a struct's exported, serialized fields. open is the chain of
// struct types already being described, which is what stops a type that holds
// itself from descending forever.
func fieldsOf(t reflect.Type, open []reflect.Type) []Field {
	t = deref(t)
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = deref(t.Elem())
	}
	if t.Kind() != reflect.Struct || t == timeType {
		return nil
	}
	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		// An embedded struct's exported keys are promoted to this level by the
		// encoder, and it promotes them whether or not the embedded type itself is
		// exported — so this is tested before the export check below, which would
		// otherwise drop every key of an embedded lowercase type without a word.
		// An embedded field carrying a json tag is a named object instead and
		// falls through to the ordinary path.
		if _, tagged := sf.Tag.Lookup("json"); sf.Anonymous && !tagged && deref(sf.Type).Kind() == reflect.Struct {
			out = append(out, fieldsOf(sf.Type, append(open, structOf(sf.Type)))...)
			continue
		}
		if !sf.IsExported() {
			continue
		}
		name, optional, ok := jsonName(sf)
		if !ok {
			continue
		}
		f := Field{Name: name, Type: typeName(sf.Type), Optional: optional}
		inner := deref(sf.Type)
		if inner.Kind() == reflect.Slice || inner.Kind() == reflect.Array {
			inner = deref(inner.Elem())
		}
		switch {
		case inner.Kind() != reflect.Struct || inner == timeType:
			// A leaf: its type name is the whole description.
		case containsType(open, inner):
			f.Recursive = true
		default:
			f.Fields = fieldsOf(sf.Type, append(open, inner))
		}
		out = append(out, f)
	}
	return out
}

// jsonName reads the serialized name and whether the key is dropped when empty.
// A field tagged "-" is not serialized at all and is reported as absent, which
// is how Summary.Replies stays out of every description of a listing.
func jsonName(sf reflect.StructField) (name string, optional, serialized bool) {
	tag, has := sf.Tag.Lookup("json")
	if !has {
		return sf.Name, false, true
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, false
	}
	name = parts[0]
	if name == "" && !sf.Anonymous {
		name = sf.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			optional = true
		}
	}
	return name, optional, true
}

// typeName is what a consumer will find under a key, in the vocabulary of the
// format rather than of Go: the four JSON scalar names, "object", and "array of
// …" for a list. A timestamp is called out as a string in RFC 3339, since
// "string" alone would not say a consumer can parse it.
func typeName(t reflect.Type) string {
	t = deref(t)
	if t == timeType {
		return "string (RFC 3339)"
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "array of " + typeName(t.Elem())
	case reflect.Map:
		return "object of " + typeName(t.Elem())
	case reflect.Struct:
		return "object"
	case reflect.Interface:
		// The one interface-typed key agentry writes is a stream envelope's
		// payload, which always holds a record object. Reporting the Go kind here
		// would name a language feature where the consumer needs the shape.
		return "object"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	default:
		return t.Kind().String()
	}
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func containsType(types []reflect.Type, t reflect.Type) bool {
	for _, other := range types {
		if other == t {
			return true
		}
	}
	return false
}
