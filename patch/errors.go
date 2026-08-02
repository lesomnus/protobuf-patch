// Package patch implements the target-independent half of the patch document
// format defined in proto/patch: the error taxonomy, the builders, and every
// rule that can be decided from a Patch and a message DESCRIPTOR without a
// message instance in hand.
//
// Applying a Patch to a live message is patchproto's job. Applying one to
// serialized bytes will be patchwire's. Both consume this package, so that the
// rules they share — what a Field resolves to, how a Range normalizes, which
// Value arm is legal for which field — have exactly one implementation.
package patch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Code identifies which rule of the format was violated.
//
// The schema's FAILURE CONTRACT (proto/patch/patch.proto) enumerates the
// conditions that must abort a Patch. Every one of them maps to a Code here:
// a format whose defining property is that it fails closed is only usable if
// the caller can tell WHICH thing failed.
type Code int

const (
	// CodeOK is the zero value and never appears on a returned Error.
	CodeOK Code = iota

	// --- Document structure. Decidable without a target. ---

	// CodeUnknownField is a field number the reader does not recognize.
	//
	// This is also how an unrecognized oneof ARM is detected: protobuf reports
	// an unknown arm as "not set", so the unknown-field set is the only thing
	// that distinguishes "the producer omitted it" from "the producer used an
	// arm from a newer revision". Refusing on unknown fields is therefore what
	// actually enforces the schema's forward-compatibility rule.
	CodeUnknownField

	// CodeMissingOneof is a required oneof with no arm set: Entry.scope,
	// Entry.kind, Key.kind, MapKey.kind, Selector.kind, Value.kind, Test.want,
	// or Location.origin.
	CodeMissingOneof

	// CodeMissingField is a required singular field left unset:
	// FieldValue.key/value, MapEntry.key/value, Location.key, Patch.delta,
	// Insert.value, Assign.value, Move.from, Copy.from, Nest.delta.
	CodeMissingField

	// CodeEmptyCollection is a repeated field the schema requires to be
	// non-empty: Targets.selectors, Delta.entries.
	CodeEmptyCollection

	// CodeUnrecognizedEnum is an enum value outside the set this file declares
	// — today, an OnMissing that is neither UNSPECIFIED nor SKIP.
	CodeUnrecognizedEnum

	// CodeFieldNoIdentifier is a Field with none of name, json_name, or number
	// set, or with a set-but-empty name or json_name.
	CodeFieldNoIdentifier

	// CodeIllegalSelector is a Selector arm that cannot be used with the
	// operation carrying it — Selector.append with anything but insert, move,
	// or copy.
	CodeIllegalSelector

	// CodeIllegalScope is a scope the operation does not define: move or copy
	// against Entry.container.
	CodeIllegalScope

	// CodeTestNotStrict is a test entry with on_missing set. A test reads
	// vacancy rather than being governed by it, so tolerating a missing target
	// would let the assertion pass without being evaluated.
	CodeTestNotStrict

	// --- Bound to a target descriptor. ---

	// CodeMessageTypeMismatch is a Patch whose message_type does not name the
	// message it is being applied to.
	CodeMessageTypeMismatch

	// CodeReaderRevisionTooOld is a Patch whose min_reader_revision exceeds
	// the revision this build implements.
	CodeReaderRevisionTooOld

	// CodeIllegalArm is a Key, MapKey, or Value arm that is not legal for the
	// container or field it was applied to — an index against a message, a
	// string map key against map<int32,V>, a Value.s against an int field.
	CodeIllegalArm

	// CodeFieldConflict is a Field whose set identifiers do not all describe
	// the same field. This is NOT vacancy: it means the Patch was authored
	// against a different schema, and must never be skipped.
	CodeFieldConflict

	// CodeMapKeyOutOfRange is a MapKey whose arm is legal for the map but
	// whose value falls outside the declared key type's range.
	CodeMapKeyOutOfRange

	// CodeUndeclaredEnumValue is a Value.e the target's CLOSED enum does not
	// declare.
	CodeUndeclaredEnumValue

	// CodeTypeMismatch is a move or copy whose source and targets differ in
	// kind or in declared type.
	CodeTypeMismatch

	// --- Bound to a target instance. ---

	// CodePathNotReached is an Entry.path that does not reach an existing
	// container. on_missing never applies to a path.
	CodePathNotReached

	// CodeVacantTarget is a target that resolves to no location while
	// on_missing is at its default.
	CodeVacantTarget

	// CodeDuplicateTarget is two selectors in one entry resolving to the same
	// location.
	CodeDuplicateTarget

	// CodeIndexOutOfRange is a list index outside [0, len) where the operation
	// requires an existing element.
	CodeIndexOutOfRange

	// CodeOccupied is an insert against a target that already holds a value.
	CodeOccupied

	// CodeNotAContainer is a nest against a scalar.
	CodeNotAContainer

	// CodeSourceUnresolved is a move or copy whose Location is vacant or
	// cannot be reached.
	CodeSourceUnresolved

	// CodeTestFailed is an assertion that does not hold.
	CodeTestFailed

	// CodeTestVacuous is a test entry whose selectors resolve to zero
	// locations. Such a test asserts nothing, so it cannot be allowed to pass.
	CodeTestVacuous
)

var codeNames = map[Code]string{
	CodeOK:                   "ok",
	CodeUnknownField:         "unknown field",
	CodeMissingOneof:         "missing oneof",
	CodeMissingField:         "missing field",
	CodeEmptyCollection:      "empty collection",
	CodeUnrecognizedEnum:     "unrecognized enum value",
	CodeFieldNoIdentifier:    "field has no identifier",
	CodeIllegalSelector:      "illegal selector for operation",
	CodeIllegalScope:         "illegal scope for operation",
	CodeTestNotStrict:        "test must not tolerate a missing target",
	CodeMessageTypeMismatch:  "message type mismatch",
	CodeReaderRevisionTooOld: "reader revision too old",
	CodeIllegalArm:           "illegal arm for target",
	CodeFieldConflict:        "field identifiers disagree",
	CodeMapKeyOutOfRange:     "map key out of range",
	CodeUndeclaredEnumValue:  "undeclared enum value",
	CodeTypeMismatch:         "type mismatch",
	CodePathNotReached:       "path does not reach a container",
	CodeVacantTarget:         "target does not exist",
	CodeDuplicateTarget:      "duplicate target",
	CodeIndexOutOfRange:      "index out of range",
	CodeOccupied:             "target already has a value",
	CodeNotAContainer:        "target is not a container",
	CodeSourceUnresolved:     "source does not resolve",
	CodeTestFailed:           "test failed",
	CodeTestVacuous:          "test asserts nothing",
}

func (c Code) String() string {
	if s, ok := codeNames[c]; ok {
		return s
	}
	return "code(" + strconv.Itoa(int(c)) + ")"
}

// At is a position inside a Patch document, written in Go selector syntax so
// that it can be pasted next to the code that built the Patch — for example
// "delta.entries[2].targets.selectors[0]".
//
// A format with this many failure conditions is only debuggable if the error
// says where in the document the problem is, not merely what it was.
type At string

// Sub returns the position of a singular field within a.
func (a At) Sub(name string) At {
	if a == "" {
		return At(name)
	}
	return a + "." + At(name)
}

// Index returns the position of the i'th element of the repeated field name
// within a.
func (a At) Index(name string, i int) At {
	return a.Sub(name) + At("["+strconv.Itoa(i)+"]")
}

// Error is a violation of one of the format's rules.
type Error struct {
	// Code identifies the rule.
	Code Code
	// At is where in the Patch document the violation is, empty if the
	// violation is not attributable to one position.
	At At
	// Detail explains the specific violation, empty if Code says enough.
	Detail string

	err error
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.At != "" {
		b.WriteString(string(e.At))
		b.WriteString(": ")
	}
	b.WriteString(e.Code.String())
	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}
	if e.err != nil {
		b.WriteString(": ")
		b.WriteString(e.err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.err }

// Is reports whether target is an *Error with the same Code, so that
// errors.Is(err, &patch.Error{Code: patch.CodeVacantTarget}) works. Prefer
// CodeOf for the common case.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// CodeOf reports the Code of the first *Error in err's chain, or CodeOK if
// there is none.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeOK
}

// Errf builds an Error at a document position.
func Errf(code Code, at At, format string, args ...any) *Error {
	e := &Error{Code: code, At: at}
	if format != "" {
		e.Detail = fmt.Sprintf(format, args...)
	}
	return e
}

// Err builds an Error at a document position with no further detail.
func Err(code Code, at At) *Error {
	return &Error{Code: code, At: at}
}

// wrap attaches a cause to an Error.
func (e *Error) wrap(err error) *Error {
	e.err = err
	return e
}

// CodeByName returns the Code whose String is name.
//
// The corpus in conformance/ names expected failures by string so that the
// cases stay data rather than Go code, and any implementation of the format
// can run them.
func CodeByName(name string) (Code, bool) {
	for c, s := range codeNames {
		if s == name {
			return c, true
		}
	}
	return CodeOK, false
}
