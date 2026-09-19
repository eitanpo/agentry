// Package jsonl writes agentry's line-oriented machine format: one JSON value
// per line, each an envelope around a payload.
//
// The envelope is the part a consumer reads without knowing which tool produced
// the line; the payload is agentry's own. That split exists for a format whose
// point is that two streams concatenate — a flat record has one namespace, and a
// second producer needs two.
package jsonl

import (
	"encoding/json"
	"io"
	"time"
)

// Tool names the agent whose sessions these records describe. It rides every
// record rather than a header alone: after `cat`, a stream filtered to one
// record type has discarded whatever header carried its provenance.
const Tool = "claude-code"

// Schema is the envelope's version, written on the first record of a stream
// only. It is constant for a run, and a concatenated stream keeps each source's
// first record in front of its own lines. Unlike Tool it does not survive a
// filter to one record type: a consumer that greps for events alone reads no
// version, which is accepted because a version is only actionable to a consumer
// reading the stream from its start.
const Schema = 1

type envelope struct {
	Type string `json:"type"`
	Tool string `json:"tool"`
	// SessionID is omitted on records that belong to no single session — a cost
	// bucket aggregates many. Tool still separates such records after a merge.
	SessionID string `json:"sessionId,omitempty"`
	// Ordinal is stream-wide, starts at 1, and is present on every record, which
	// is what makes a merged stream sortable. Codex's equivalent is optional, so
	// a shared schema cannot require one from every producer — agentry can still
	// guarantee its own.
	Ordinal int `json:"ordinal"`
	// Time is the record's own timestamp where it has one, omitted otherwise.
	Time    *time.Time `json:"time,omitempty"`
	Schema  int        `json:"schema,omitempty"`
	Payload any        `json:"payload"`
}

// Encoder writes one stream. Its zero value is not usable; call New.
type Encoder struct {
	enc     *json.Encoder
	ordinal int
}

// New returns an Encoder writing to w.
func New(w io.Writer) *Encoder {
	enc := json.NewEncoder(w)
	// Go's encoder escapes <, > and & by default, which is a defence for HTML
	// embedding and pure noise in a stream read by jq or another program. Prompts
	// and shell commands carry those characters constantly.
	enc.SetEscapeHTML(false)
	return &Encoder{enc: enc}
}

// Emit writes one record. session is the session the record belongs to, empty
// for one that belongs to none; at is its timestamp, zero for one with none.
func (e *Encoder) Emit(recType, session string, at time.Time, payload any) error {
	e.ordinal++
	env := envelope{
		Type:      recType,
		Tool:      Tool,
		SessionID: session,
		Ordinal:   e.ordinal,
		Payload:   payload,
	}
	if !at.IsZero() {
		env.Time = &at
	}
	if e.ordinal == 1 {
		env.Schema = Schema
	}
	return e.enc.Encode(env)
}
