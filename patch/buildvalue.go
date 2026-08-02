package patch

import (
	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Value is a literal carried by test, insert, and assign.
//
// There is one constructor per protobuf type class and no conversion between
// them: a Value built with Str never applies to an integer field, and the
// mismatch is an error rather than a coerced write. Pick the constructor that
// matches the target field's kind.
//
// There is no null. To clear a field use Remove; to assert absence use
// Exists(false).
type Value struct {
	pb  *patchpb.Value
	err error
}

// Bool builds a value for a bool field.
func Bool(v bool) Value {
	return Value{pb: patchpb.Value_builder{B: proto.Bool(v)}.Build()}
}

// Int32 builds a value for an int32, sint32, or sfixed32 field.
func Int32(v int32) Value {
	return Value{pb: patchpb.Value_builder{I32: proto.Int32(v)}.Build()}
}

// Int64 builds a value for an int64, sint64, or sfixed64 field.
func Int64(v int64) Value {
	return Value{pb: patchpb.Value_builder{I64: proto.Int64(v)}.Build()}
}

// Uint32 builds a value for a uint32 or fixed32 field.
func Uint32(v uint32) Value {
	return Value{pb: patchpb.Value_builder{U32: proto.Uint32(v)}.Build()}
}

// Uint64 builds a value for a uint64 or fixed64 field.
func Uint64(v uint64) Value {
	return Value{pb: patchpb.Value_builder{U64: proto.Uint64(v)}.Build()}
}

// Float32 builds a value for a float field.
func Float32(v float32) Value {
	return Value{pb: patchpb.Value_builder{F32: proto.Float32(v)}.Build()}
}

// Float64 builds a value for a double field.
func Float64(v float64) Value {
	return Value{pb: patchpb.Value_builder{F64: proto.Float64(v)}.Build()}
}

// Str builds a value for a string field.
func Str(v string) Value {
	return Value{pb: patchpb.Value_builder{S: proto.String(v)}.Build()}
}

// Bytes builds a value for a bytes field.
func Bytes(v []byte) Value {
	if v == nil {
		v = []byte{}
	}
	return Value{pb: patchpb.Value_builder{X: v}.Build()}
}

// Enum builds a value for an enum field, carrying the value's number. Enum
// numbers are int32 and may be negative. Against a closed enum the number must
// be one the enum declares.
func Enum(number int32) Value {
	return Value{pb: patchpb.Value_builder{E: proto.Int32(number)}.Build()}
}

// Msg builds a value for a message field from its fields. A field not listed
// is not set.
//
// At most one member of any single oneof of the target message may appear.
func Msg(fields ...FieldValue) Value {
	fvs := make([]*patchpb.FieldValue, 0, len(fields))
	for _, f := range fields {
		if f.err != nil {
			return Value{err: f.err}
		}
		fvs = append(fvs, f.pb)
	}
	return Value{pb: patchpb.Value_builder{
		M: patchpb.MessageValue_builder{Fields: fvs}.Build(),
	}.Build()}
}

// List builds a value for a repeated field. Every element must match the
// field's element kind.
func List(values ...Value) Value {
	vs := make([]*patchpb.Value, 0, len(values))
	for _, v := range values {
		if v.err != nil {
			return Value{err: v.err}
		}
		if v.pb == nil {
			return Value{err: Errf(CodeMissingOneof, "", "list element has no value")}
		}
		vs = append(vs, v.pb)
	}
	return Value{pb: patchpb.Value_builder{
		L: patchpb.ListValue_builder{Values: vs}.Build(),
	}.Build()}
}

// Map builds a value for a map field. Keys must be unique.
func Map(entries ...MapEntry) Value {
	es := make([]*patchpb.MapEntry, 0, len(entries))
	for _, e := range entries {
		if e.err != nil {
			return Value{err: e.err}
		}
		es = append(es, e.pb)
	}
	return Value{pb: patchpb.Value_builder{
		Map: patchpb.MapValue_builder{Entries: es}.Build(),
	}.Build()}
}

func (v Value) value() (*patchpb.Value, error) {
	if v.err != nil {
		return nil, v.err
	}
	if v.pb == nil {
		return nil, Errf(CodeMissingOneof, "", "value has no kind")
	}
	return v.pb, nil
}

// FieldValue pairs a message field with the value to put in it, for Msg.
type FieldValue struct {
	pb  *patchpb.FieldValue
	err error
}

// F pairs a field with a value.
func F(key Field, value Value) FieldValue {
	if key.err != nil {
		return FieldValue{err: key.err}
	}
	pb, err := value.value()
	if err != nil {
		return FieldValue{err: err}
	}
	return FieldValue{pb: patchpb.FieldValue_builder{Key: key.pb, Value: pb}.Build()}
}

// MapEntry pairs a map key with the value to put under it, for Map.
type MapEntry struct {
	pb  *patchpb.MapEntry
	err error
}

// E pairs a map key with a value.
func E(key MapKey, value Value) MapEntry {
	if key.err != nil {
		return MapEntry{err: key.err}
	}
	pb, err := value.value()
	if err != nil {
		return MapEntry{err: err}
	}
	return MapEntry{pb: patchpb.MapEntry_builder{Key: key.pb, Value: pb}.Build()}
}
