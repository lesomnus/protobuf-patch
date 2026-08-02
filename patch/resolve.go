package patch

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// ResolveField resolves f against md.
//
// It reports vacancy — a field the message does not declare — separately from
// error, because the two must be treated differently and only the caller knows
// which position f came from. In Entry.targets a vacant field is governed by
// on_missing; in a Path or a Location it is unconditional. If vacancy were
// folded into error, `test` could not report absence at all.
//
// Resolution order is number, then name, then json_name — number first because
// the field number is protobuf's stable identity, while names may be edited
// without a wire break. Every OTHER identifier that is set must then agree with
// the resolved field. Disagreement is CodeFieldConflict, NOT vacancy: it means
// the Patch was authored against a different schema, and skipping that would
// defeat the only integrity check the format has.
//
// A number in one of md's extension ranges is CodeExtensionField, also not
// vacancy — see the comment on that code.
func ResolveField(md protoreflect.MessageDescriptor, f *patchpb.Field, at At) (protoreflect.FieldDescriptor, bool, error) {
	if err := validateField(f, at); err != nil {
		return nil, false, err
	}

	fields := md.Fields()
	var fd protoreflect.FieldDescriptor
	switch {
	case f.HasNumber():
		fd = fields.ByNumber(protoreflect.FieldNumber(f.GetNumber()))
	case f.HasName():
		fd = fields.ByName(protoreflect.Name(f.GetName()))
	default:
		fd = fields.ByJSONName(f.GetJsonName())
	}
	if fd == nil {
		// MessageDescriptor.Fields never contains extensions, so an extension
		// number lands here looking exactly like a field the message does not
		// declare. Reporting it as vacancy would be a lie about data that is
		// really there, so it is separated out before vacancy is returned.
		if n := protoreflect.FieldNumber(f.GetNumber()); f.HasNumber() && md.ExtensionRanges().Has(n) {
			return nil, false, Errf(CodeExtensionField, at,
				"%d is in an extension range of %s; the format cannot address extensions, and will not report one as absent",
				n, md.FullName())
		}
		return nil, true, nil
	}

	if f.HasNumber() && uint32(fd.Number()) != f.GetNumber() {
		return nil, false, Errf(CodeFieldConflict, at,
			"%s is field %d, not %d", fd.FullName(), fd.Number(), f.GetNumber())
	}
	if f.HasName() && string(fd.Name()) != f.GetName() {
		return nil, false, Errf(CodeFieldConflict, at,
			"field %d of %s is named %q, not %q", fd.Number(), md.FullName(), fd.Name(), f.GetName())
	}
	if f.HasJsonName() && fd.JSONName() != f.GetJsonName() {
		return nil, false, Errf(CodeFieldConflict, at,
			"%s has JSON name %q, not %q", fd.FullName(), fd.JSONName(), f.GetJsonName())
	}
	return fd, false, nil
}

// NormalizeIndex resolves a list index against a list of the given length.
//
// A negative index counts from the end — the effective index is length+i — in
// every operation alike. An index that lands outside [0, length) is vacant.
func NormalizeIndex(i int64, length int) (int, bool) {
	if i < 0 {
		i += int64(length)
	}
	if i < 0 || i >= int64(length) {
		return 0, true
	}
	return int(i), false
}

// NormalizeRange resolves r against a list of the given length, returning the
// half-open interval [begin, end) of indices it selects.
//
// Normalization is exactly the schema's three steps: an unset bound is 0 or
// length, a negative bound is length+v, and both are clamped to [0, length].
// If begin >= end after that, the range selects nothing — a defined answer,
// not a failure, so a Range never produces vacancy and on_missing never
// applies to one.
//
// The bounds are read through HasBegin and HasEnd rather than compared against
// zero. Branching on the value is what made the previous implementation read an
// explicit [0, 0) as "the whole list".
func NormalizeRange(r *patchpb.Range, length int) (int, int) {
	begin := int64(0)
	if r.HasBegin() {
		begin = r.GetBegin()
		if begin < 0 {
			begin += int64(length)
		}
	}
	end := int64(length)
	if r.HasEnd() {
		end = r.GetEnd()
		if end < 0 {
			end += int64(length)
		}
	}

	begin = clamp(begin, length)
	end = clamp(end, length)
	if begin >= end {
		return 0, 0
	}
	return int(begin), int(end)
}

func clamp(v int64, length int) int64 {
	if v < 0 {
		return 0
	}
	if v > int64(length) {
		return int64(length)
	}
	return v
}
