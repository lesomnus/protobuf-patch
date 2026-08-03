package patch_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

var sampleDesc = (&sample.Value{}).ProtoReflect().Descriptor()

func fieldOf(t *testing.T, name string) protoreflect.FieldDescriptor {
	t.Helper()
	fd := sampleDesc.Fields().ByName(protoreflect.Name(name))
	if fd == nil {
		t.Fatalf("sample.Value has no field %q", name)
	}
	return fd
}

func pbValue(t *testing.T, v patch.Value) *patchpb.Value {
	t.Helper()
	p := patch.MustNew(mt, patch.Target(patch.Name("x")).Assign(v))
	return p.GetDelta().GetEntries()[0].GetAssign().GetValue()
}

// TestArmIsOneToOne pins the table in value.proto: each protobuf type class
// takes exactly one Value arm. Anything else is an error, never a conversion.
func TestArmIsOneToOne(t *testing.T) {
	// field name -> the one constructor that fits it
	fits := map[string]patch.Value{
		"b_1":    patch.Bool(true),
		"i32_1":  patch.Int32(1),
		"si32_1": patch.Int32(1),
		"sx32_1": patch.Int32(1),
		"i64_1":  patch.Int64(1),
		"si64_1": patch.Int64(1),
		"sx64_1": patch.Int64(1),
		"u32_1":  patch.Uint32(1),
		"ux32_1": patch.Uint32(1),
		"u64_1":  patch.Uint64(1),
		"ux64_1": patch.Uint64(1),
		"f32_1":  patch.Float32(1),
		"f64_1":  patch.Float64(1),
		"s_1":    patch.Str("s"),
		"bs_1":   patch.Bytes([]byte{1}),
		"enum_1": patch.Enum(1),
	}

	all := []struct {
		name string
		v    patch.Value
	}{
		{"bool", patch.Bool(true)},
		{"int32", patch.Int32(1)},
		{"int64", patch.Int64(1)},
		{"uint32", patch.Uint32(1)},
		{"uint64", patch.Uint64(1)},
		{"float32", patch.Float32(1)},
		{"float64", patch.Float64(1)},
		{"string", patch.Str("s")},
		{"bytes", patch.Bytes([]byte{1})},
		{"enum", patch.Enum(1)},
	}

	for field, want := range fits {
		t.Run(field, func(t *testing.T) {
			fd := fieldOf(t, field)
			wantPB := pbValue(t, want)

			if err := patch.CheckArm(wantPB, fd, patch.SiteField, ""); err != nil {
				t.Fatalf("the fitting arm was rejected: %v", err)
			}
			for _, other := range all {
				pb := pbValue(t, other.v)
				if pb.WhichKind() == wantPB.WhichKind() {
					continue
				}
				if err := patch.CheckArm(pb, fd, patch.SiteField, ""); err == nil {
					t.Errorf("%s accepted a %s value; there is no conversion", field, other.name)
				} else if patch.CodeOf(err) != patch.CodeIllegalArm {
					t.Errorf("%s + %s: CodeOf = %v, want CodeIllegalArm", field, other.name, patch.CodeOf(err))
				}
			}
		})
	}
}

func TestArmForContainers(t *testing.T) {
	tests := []struct {
		name  string
		field string
		site  patch.Site
		value patch.Value
		ok    bool
	}{
		{"whole list takes l", "r_s_1", patch.SiteField, patch.List(patch.Str("a")), true},
		{"whole list rejects s", "r_s_1", patch.SiteField, patch.Str("a"), false},
		{"list element takes s", "r_s_1", patch.SiteElement, patch.Str("a"), true},
		{"list element rejects l", "r_s_1", patch.SiteElement, patch.List(patch.Str("a")), false},

		{"whole map takes map", "m_s_s", patch.SiteField, patch.Map(patch.E(patch.MapStr("k"), patch.Str("v"))), true},
		{"whole map rejects s", "m_s_s", patch.SiteField, patch.Str("v"), false},
		{"map value takes s", "m_s_s", patch.SiteMapValue, patch.Str("v"), true},
		{"map value rejects map", "m_s_s", patch.SiteMapValue, patch.Map(), false},

		{"message field takes m", "m_1", patch.SiteField, patch.Msg(), true},
		{"message field rejects s", "m_1", patch.SiteField, patch.Str("v"), false},
		{"message map value takes m", "m_s_m", patch.SiteMapValue, patch.Msg(), true},
		{"repeated message element takes m", "r_m_1", patch.SiteElement, patch.Msg(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := patch.CheckArm(pbValue(t, tt.value), fieldOf(t, tt.field), tt.site, "")
			if tt.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatal("accepted")
				}
				if patch.CodeOf(err) != patch.CodeIllegalArm {
					t.Fatalf("CodeOf = %v, want CodeIllegalArm", patch.CodeOf(err))
				}
			}
		})
	}
}

func TestArmRejectsNoKind(t *testing.T) {
	err := patch.CheckArm(&patchpb.Value{}, fieldOf(t, "s_1"), patch.SiteField, "")
	if patch.CodeOf(err) != patch.CodeMissingOneof {
		t.Fatalf("CodeOf = %v, want CodeMissingOneof", patch.CodeOf(err))
	}
}

func TestClosedEnum(t *testing.T) {
	// A closed enum cannot hold a number it does not declare, so writing one
	// must be refused rather than produce a value that will not round trip.
	closed := fieldOf(t, "closed_1")
	open := fieldOf(t, "enum_1")

	for _, n := range []int32{0, 1, 2} {
		if err := patch.CheckArm(pbValue(t, patch.Enum(n)), closed, patch.SiteField, ""); err != nil {
			t.Errorf("closed enum rejected declared value %d: %v", n, err)
		}
	}
	for _, n := range []int32{3, -1, 999} {
		err := patch.CheckArm(pbValue(t, patch.Enum(n)), closed, patch.SiteField, "")
		if patch.CodeOf(err) != patch.CodeUndeclaredEnumValue {
			t.Errorf("closed enum + %d: CodeOf = %v, want CodeUndeclaredEnumValue", n, patch.CodeOf(err))
		}
		if err := patch.CheckArm(pbValue(t, patch.Enum(n)), open, patch.SiteField, ""); err != nil {
			t.Errorf("open enum rejected %d: %v", n, err)
		}
	}
}

func TestScalarConversion(t *testing.T) {
	tests := []struct {
		field string
		value patch.Value
		want  any
	}{
		{"b_1", patch.Bool(true), true},
		{"i32_1", patch.Int32(-7), int32(-7)},
		{"si32_1", patch.Int32(-7), int32(-7)},
		{"i64_1", patch.Int64(math.MinInt64), int64(math.MinInt64)},
		{"u32_1", patch.Uint32(math.MaxUint32), uint32(math.MaxUint32)},
		{"u64_1", patch.Uint64(math.MaxUint64), uint64(math.MaxUint64)},
		{"f32_1", patch.Float32(1.5), float32(1.5)},
		{"f64_1", patch.Float64(1.5), float64(1.5)},
		{"s_1", patch.Str("hi"), "hi"},
		{"enum_1", patch.Enum(2), protoreflect.EnumNumber(2)},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got, err := patch.Scalar(pbValue(t, tt.value), fieldOf(t, tt.field), patch.SiteField, "")
			if err != nil {
				t.Fatalf("Scalar: %v", err)
			}
			if got.Interface() != tt.want {
				t.Errorf("= %v (%T), want %v (%T)", got.Interface(), got.Interface(), tt.want, tt.want)
			}
		})
	}

	t.Run("bytes", func(t *testing.T) {
		got, err := patch.Scalar(pbValue(t, patch.Bytes([]byte{1, 2})), fieldOf(t, "bs_1"), patch.SiteField, "")
		if err != nil {
			t.Fatalf("Scalar: %v", err)
		}
		if string(got.Bytes()) != "\x01\x02" {
			t.Errorf("= %v", got.Bytes())
		}
	})

	t.Run("extremes survive", func(t *testing.T) {
		// Values are self-describing precisely so that nothing is truncated on
		// the way in. NaN and Inf are ordinary float64 values here.
		for _, f := range []float64{math.Inf(1), math.Inf(-1), math.MaxFloat64} {
			got, err := patch.Scalar(pbValue(t, patch.Float64(f)), fieldOf(t, "f64_1"), patch.SiteField, "")
			if err != nil {
				t.Fatalf("Scalar(%v): %v", f, err)
			}
			if got.Float() != f {
				t.Errorf("= %v, want %v", got.Float(), f)
			}
		}
		nan, err := patch.Scalar(pbValue(t, patch.Float64(math.NaN())), fieldOf(t, "f64_1"), patch.SiteField, "")
		if err != nil {
			t.Fatalf("Scalar(NaN): %v", err)
		}
		if !math.IsNaN(nan.Float()) {
			t.Errorf("= %v, want NaN", nan.Float())
		}
	})
}

func TestMapKeyArmMustMatchDeclaredType(t *testing.T) {
	tests := []struct {
		name  string
		field string
		key   patch.MapKey
		ok    bool
	}{
		{"string map takes s", "m_s_s", patch.MapStr("k"), true},
		{"string map rejects i", "m_s_s", patch.MapInt(1), false},
		{"string map rejects u", "m_s_s", patch.MapUint(1), false},
		{"string map rejects b", "m_s_s", patch.MapBool(true), false},

		{"int32 map takes i", "m_i32_s", patch.MapInt(1), true},
		{"int32 map rejects s", "m_i32_s", patch.MapStr("1"), false},
		{"int64 map takes i", "m_i64_s", patch.MapInt(1), true},
		{"sint64 map takes i", "m_si64_s", patch.MapInt(-1), true},

		{"uint32 map takes u", "m_u32_s", patch.MapUint(1), true},
		{"uint32 map rejects i", "m_u32_s", patch.MapInt(1), false},
		{"uint64 map takes u", "m_u64_s", patch.MapUint(1), true},
		{"fixed32 map takes u", "m_ux32_s", patch.MapUint(1), true},

		{"bool map takes b", "m_b_s", patch.MapBool(true), true},
		{"bool map rejects i", "m_b_s", patch.MapInt(1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pb := keyPB(t, tt.key)
			_, err := patch.MapKeyFor(pb, fieldOf(t, tt.field), "")
			if tt.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatal("accepted; a numeric key must not be stringified onto a string map")
				}
				if patch.CodeOf(err) != patch.CodeIllegalArm {
					t.Fatalf("CodeOf = %v, want CodeIllegalArm", patch.CodeOf(err))
				}
			}
		})
	}
}

func TestMapKeyRange(t *testing.T) {
	// The arm can be legal while the value is not representable. That is an
	// error, never a truncation and never a key that merely fails to match.
	tests := []struct {
		name  string
		field string
		key   patch.MapKey
		ok    bool
	}{
		{"int32 max", "m_i32_s", patch.MapInt(math.MaxInt32), true},
		{"int32 min", "m_i32_s", patch.MapInt(math.MinInt32), true},
		{"int32 overflow", "m_i32_s", patch.MapInt(math.MaxInt32 + 1), false},
		{"int32 underflow", "m_i32_s", patch.MapInt(math.MinInt32 - 1), false},
		{"int64 takes the full range", "m_i64_s", patch.MapInt(math.MinInt64), true},
		{"uint32 max", "m_u32_s", patch.MapUint(math.MaxUint32), true},
		{"uint32 overflow", "m_u32_s", patch.MapUint(math.MaxUint32 + 1), false},
		{"uint64 takes the full range", "m_u64_s", patch.MapUint(math.MaxUint64), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patch.MapKeyFor(keyPB(t, tt.key), fieldOf(t, tt.field), "")
			if tt.ok {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if patch.CodeOf(err) != patch.CodeMapKeyOutOfRange {
				t.Fatalf("CodeOf = %v, want CodeMapKeyOutOfRange (err=%v)", patch.CodeOf(err), err)
			}
		})
	}
}

func TestMapKeyEmptyString(t *testing.T) {
	// The empty string is a legal map key and must be distinguishable from an
	// unset one. MapKey's oneof carries its own presence, so it is.
	k, err := patch.MapKeyFor(keyPB(t, patch.MapStr("")), fieldOf(t, "m_s_s"), "")
	if err != nil {
		t.Fatalf("MapKeyFor: %v", err)
	}
	if k.String() != "" {
		t.Errorf("= %q, want the empty key", k.String())
	}
}

func TestShapeOf(t *testing.T) {
	tests := []struct {
		name  string
		value patch.Value
		want  patch.Shape
	}{
		{"scalar", patch.Str("s"), patch.ShapeScalar},
		{"enum is scalar", patch.Enum(1), patch.ShapeScalar},
		{"message", patch.Msg(), patch.ShapeMessage},
		{"list", patch.List(), patch.ShapeList},
		{"map", patch.Map(), patch.ShapeMap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := patch.ShapeOf(pbValue(t, tt.value)); got != tt.want {
				t.Errorf("= %v, want %v", got, tt.want)
			}
		})
	}
	if got := patch.ShapeOf(nil); got != patch.ShapeNone {
		t.Errorf("ShapeOf(nil) = %v, want ShapeNone", got)
	}
	if got := patch.ShapeOf(&patchpb.Value{}); got != patch.ShapeNone {
		t.Errorf("ShapeOf(empty) = %v, want ShapeNone", got)
	}
}

func keyPB(t *testing.T, k patch.MapKey) *patchpb.MapKey {
	t.Helper()
	p := patch.MustNew(mt, patch.Target(k).Remove())
	return p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetKey().GetMapKey()
}

// readField writes v at field name of a fresh sample.Value and reads it back.
// ValueOf takes a live value off a message, so the fixtures below are produced
// that way rather than assembled by hand.
func readField(t *testing.T, name string, v protoreflect.Value) (protoreflect.Value, protoreflect.FieldDescriptor) {
	t.Helper()
	fd := fieldOf(t, name)
	m := (&sample.Value{}).ProtoReflect()
	m.Set(fd, v)
	return m.Get(fd), fd
}

// readUnset reads a field nobody wrote. This is how a producer meets a kind's
// zero value without having chosen it, and for bytes the zero value read this
// way is a nil slice.
func readUnset(t *testing.T, name string) (protoreflect.Value, protoreflect.FieldDescriptor) {
	t.Helper()
	fd := fieldOf(t, name)
	return (&sample.Value{}).ProtoReflect().Get(fd), fd
}

// buildField fills a container field in place and reads it back. Mutable hands
// back the list, map, or message so that fill can populate it.
func buildField(t *testing.T, name string, fill func(v protoreflect.Value)) (protoreflect.Value, protoreflect.FieldDescriptor) {
	t.Helper()
	fd := fieldOf(t, name)
	m := (&sample.Value{}).ProtoReflect()
	fill(m.Mutable(fd))
	return m.Get(fd), fd
}

// valueOf runs ValueOf and asserts three things at once: it produced the same
// document the equivalent constructor builds, that document fits the field it
// was read from, and it survives the validation an applier performs before
// touching anything.
//
// The last is what makes the assertion worth making. A Value that is merely
// non-nil is not usable; one that Validate refuses is a bug reported against a
// position inside the document instead of against the field that produced it.
func valueOf(t *testing.T, pv protoreflect.Value, fd protoreflect.FieldDescriptor, site patch.Site, want patch.Value) *patchpb.Value {
	t.Helper()
	got, err := patch.ValueOf(pv, fd, site, "")
	if err != nil {
		t.Fatalf("ValueOf: %v", err)
	}
	if w := pbValue(t, want); !proto.Equal(got, w) {
		t.Errorf("= %v, want %v", got, w)
	}
	if err := patch.CheckArm(got, fd, site, ""); err != nil {
		t.Errorf("output does not fit the field it was read from: %v", err)
	}
	if err := patch.Validate(assigning(t, got)); err != nil {
		t.Errorf("Validate refused it: %v", err)
	}
	return got
}

// assigning wraps v in a document so Validate has something to walk. The
// placeholder the builder starts with is overwritten, because there is no
// public constructor that takes a raw patchpb.Value.
func assigning(t *testing.T, v *patchpb.Value) *patchpb.Patch {
	t.Helper()
	p := patch.MustNew(mt, patch.Target(patch.Name("x")).Assign(patch.Bool(false)))
	p.GetDelta().GetEntries()[0].GetAssign().SetValue(v)
	return p
}

// TestValueOfInvertsScalar pins what ValueOf's doc comment claims: what it
// produces from a live value is what Scalar turns back into that value. Every
// scalar kind is listed, including the encodings that share an arm, because
// the arm table is the thing being checked.
func TestValueOfInvertsScalar(t *testing.T) {
	tests := []struct {
		field string
		in    protoreflect.Value
		want  any
	}{
		{"b_1", protoreflect.ValueOfBool(true), true},
		{"i32_1", protoreflect.ValueOfInt32(-7), int32(-7)},
		{"si32_1", protoreflect.ValueOfInt32(-7), int32(-7)},
		{"sx32_1", protoreflect.ValueOfInt32(math.MinInt32), int32(math.MinInt32)},
		{"i64_1", protoreflect.ValueOfInt64(math.MinInt64), int64(math.MinInt64)},
		{"si64_1", protoreflect.ValueOfInt64(-7), int64(-7)},
		{"sx64_1", protoreflect.ValueOfInt64(math.MaxInt64), int64(math.MaxInt64)},
		{"u32_1", protoreflect.ValueOfUint32(math.MaxUint32), uint32(math.MaxUint32)},
		{"ux32_1", protoreflect.ValueOfUint32(1), uint32(1)},
		{"u64_1", protoreflect.ValueOfUint64(math.MaxUint64), uint64(math.MaxUint64)},
		{"ux64_1", protoreflect.ValueOfUint64(1), uint64(1)},
		{"f32_1", protoreflect.ValueOfFloat32(1.5), float32(1.5)},
		{"f64_1", protoreflect.ValueOfFloat64(1.5), float64(1.5)},
		{"s_1", protoreflect.ValueOfString("hi"), "hi"},
		{"enum_1", protoreflect.ValueOfEnum(2), protoreflect.EnumNumber(2)},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			pv, fd := readField(t, tt.field, tt.in)
			v, err := patch.ValueOf(pv, fd, patch.SiteField, "")
			if err != nil {
				t.Fatalf("ValueOf: %v", err)
			}
			got, err := patch.Scalar(v, fd, patch.SiteField, "")
			if err != nil {
				t.Fatalf("Scalar: %v", err)
			}
			if got.Interface() != tt.want {
				t.Errorf("= %v (%T), want %v (%T)", got.Interface(), got.Interface(), tt.want, tt.want)
			}
		})
	}

	t.Run("bs_1", func(t *testing.T) {
		pv, fd := readField(t, "bs_1", protoreflect.ValueOfBytes([]byte{1, 2}))
		v, err := patch.ValueOf(pv, fd, patch.SiteField, "")
		if err != nil {
			t.Fatalf("ValueOf: %v", err)
		}
		got, err := patch.Scalar(v, fd, patch.SiteField, "")
		if err != nil {
			t.Fatalf("Scalar: %v", err)
		}
		if string(got.Bytes()) != "\x01\x02" {
			t.Errorf("= %v", got.Bytes())
		}
	})
}

// TestValueOfMatchesTheConstructors checks the other direction of the same
// table: a value read off a message produces the document a caller would have
// written by hand for it.
func TestValueOfMatchesTheConstructors(t *testing.T) {
	tests := []struct {
		field string
		in    protoreflect.Value
		want  patch.Value
	}{
		{"b_1", protoreflect.ValueOfBool(true), patch.Bool(true)},
		{"i32_1", protoreflect.ValueOfInt32(-7), patch.Int32(-7)},
		{"i64_1", protoreflect.ValueOfInt64(-7), patch.Int64(-7)},
		{"u32_1", protoreflect.ValueOfUint32(7), patch.Uint32(7)},
		{"u64_1", protoreflect.ValueOfUint64(7), patch.Uint64(7)},
		{"f32_1", protoreflect.ValueOfFloat32(1.5), patch.Float32(1.5)},
		{"f64_1", protoreflect.ValueOfFloat64(1.5), patch.Float64(1.5)},
		{"s_1", protoreflect.ValueOfString("hi"), patch.Str("hi")},
		{"bs_1", protoreflect.ValueOfBytes([]byte{1, 2}), patch.Bytes([]byte{1, 2})},
		{"enum_1", protoreflect.ValueOfEnum(2), patch.Enum(2)},
		{"closed_1", protoreflect.ValueOfEnum(1), patch.Enum(1)},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			pv, fd := readField(t, tt.field, tt.in)
			valueOf(t, pv, fd, patch.SiteField, tt.want)
		})
	}
}

// TestValueOfSetsAnArmForEveryZeroValue is the general form of the nil-bytes
// defect. A Value with no arm set says nothing at all, and it is refused deep
// inside the document rather than at the field that produced it, so no kind's
// zero value may leave the arm unnamed.
//
// Every arm but `x` is written through a pointer to a local and so is always
// set; `x` is a []byte the builder skips when nil, which is why it needs the
// normalization the other kinds get for free.
func TestValueOfSetsAnArmForEveryZeroValue(t *testing.T) {
	fields := []string{
		"b_1", "i32_1", "si32_1", "sx32_1", "i64_1", "si64_1", "sx64_1",
		"u32_1", "ux32_1", "u64_1", "ux64_1", "f32_1", "f64_1",
		"s_1", "bs_1", "enum_1", "closed_1",
		// A container read before anything was put in it is empty, not
		// absent: `l` and `map` are still the arms it takes.
		"r_s_1", "r_bs_1", "r_m_1", "m_s_s", "m_s_bs", "m_s_m",
		// An unset message field reads as an empty message, and `m` with no
		// fields is what says so.
		"m_1",
		// Explicit presence changes nothing here. The value is still read,
		// and it still has to name an arm.
		"opt_s", "opt_bs", "opt_m",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			pv, fd := readUnset(t, field)
			v, err := patch.ValueOf(pv, fd, patch.SiteField, "")
			if err != nil {
				t.Fatalf("ValueOf: %v", err)
			}
			if patch.ShapeOf(v) == patch.ShapeNone {
				t.Fatalf("no arm set; the Value says nothing")
			}
			if err := patch.CheckArm(v, fd, patch.SiteField, ""); err != nil {
				t.Errorf("CheckArm: %v", err)
			}
			if err := patch.Validate(assigning(t, v)); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

// TestValueOfNilBytes covers the hole directly. protobuf has no null bytes, so
// a nil slice is an empty one and `x` must be set either way.
//
// ValueOf recurses for list elements, map values, and message subfields, so a
// guard in whatever calls it could only ever cover the top level. Each of the
// sites below is reached through a different one of those recursions.
func TestValueOfNilBytes(t *testing.T) {
	t.Run("field", func(t *testing.T) {
		pv, fd := readUnset(t, "bs_1")
		if pv.Bytes() != nil {
			t.Fatal("fixture is not nil; this test is checking nothing")
		}
		valueOf(t, pv, fd, patch.SiteField, patch.Bytes(nil))
	})

	t.Run("list element", func(t *testing.T) {
		pv, fd := buildField(t, "r_bs_1", func(v protoreflect.Value) {
			v.List().Append(protoreflect.ValueOfBytes(nil))
		})
		if pv.List().Get(0).Bytes() != nil {
			t.Fatal("fixture is not nil; this test is checking nothing")
		}
		valueOf(t, pv.List().Get(0), fd, patch.SiteElement, patch.Bytes(nil))
		valueOf(t, pv, fd, patch.SiteField, patch.List(patch.Bytes(nil)))
	})

	t.Run("map value", func(t *testing.T) {
		k := protoreflect.ValueOfString("k").MapKey()
		pv, fd := buildField(t, "m_s_bs", func(v protoreflect.Value) {
			v.Map().Set(k, protoreflect.ValueOfBytes(nil))
		})
		if pv.Map().Get(k).Bytes() != nil {
			t.Fatal("fixture is not nil; this test is checking nothing")
		}
		valueOf(t, pv.Map().Get(k), fd, patch.SiteMapValue, patch.Bytes(nil))
		valueOf(t, pv, fd, patch.SiteField, patch.Map(patch.E(patch.MapStr("k"), patch.Bytes(nil))))
	})

	t.Run("message subfield", func(t *testing.T) {
		// A singular bytes subfield cannot carry a nil here: Range yields only
		// set fields, and the generated setters normalize. It reaches the
		// subfield through the nested message's own repeated field instead,
		// which is exactly the recursion a top-level guard would miss.
		pv, fd := buildField(t, "m_1", func(v protoreflect.Value) {
			m := v.Message()
			rd := m.Descriptor().Fields().ByName("r_bs_1")
			m.Mutable(rd).List().Append(protoreflect.ValueOfBytes(nil))
		})
		valueOf(t, pv, fd, patch.SiteField, patch.Msg(
			patch.F(patch.Num(1012), patch.List(patch.Bytes(nil))),
		))
	})
}

// TestValueOfNilBytesIsIndistinguishable states the policy the fix rests on:
// nil and empty are the same value, so they must produce the same document.
func TestValueOfNilBytesIsIndistinguishable(t *testing.T) {
	nilv, fd := readField(t, "bs_1", protoreflect.ValueOfBytes(nil))
	emptyv, _ := readField(t, "bs_1", protoreflect.ValueOfBytes([]byte{}))

	a, err := patch.ValueOf(nilv, fd, patch.SiteField, "")
	if err != nil {
		t.Fatalf("ValueOf(nil): %v", err)
	}
	b, err := patch.ValueOf(emptyv, fd, patch.SiteField, "")
	if err != nil {
		t.Fatalf("ValueOf(empty): %v", err)
	}
	if !proto.Equal(a, b) {
		t.Errorf("nil produced %v, empty produced %v", a, b)
	}
}

// TestValueOfContainers walks the three container recursions with values in
// them, so that the element, the map value, and the subfield are each built by
// the recursive call rather than by the top-level switch.
func TestValueOfContainers(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		pv, fd := buildField(t, "r_s_1", func(v protoreflect.Value) {
			v.List().Append(protoreflect.ValueOfString("a"))
			v.List().Append(protoreflect.ValueOfString("b"))
		})
		valueOf(t, pv, fd, patch.SiteField, patch.List(patch.Str("a"), patch.Str("b")))
	})

	t.Run("list of enum", func(t *testing.T) {
		pv, fd := buildField(t, "r_enum_1", func(v protoreflect.Value) {
			v.List().Append(protoreflect.ValueOfEnum(2))
		})
		valueOf(t, pv, fd, patch.SiteField, patch.List(patch.Enum(2)))
	})

	t.Run("list of message", func(t *testing.T) {
		pv, fd := buildField(t, "r_m_1", func(v protoreflect.Value) {
			l := v.List()
			e := l.NewElement()
			e.Message().Set(fieldOf(t, "s_1"), protoreflect.ValueOfString("hi"))
			l.Append(e)
		})
		valueOf(t, pv, fd, patch.SiteField, patch.List(
			patch.Msg(patch.F(patch.Num(109), patch.Str("hi"))),
		))
	})

	t.Run("map", func(t *testing.T) {
		pv, fd := buildField(t, "m_s_s", func(v protoreflect.Value) {
			v.Map().Set(protoreflect.ValueOfString("k").MapKey(), protoreflect.ValueOfString("v"))
		})
		valueOf(t, pv, fd, patch.SiteField, patch.Map(patch.E(patch.MapStr("k"), patch.Str("v"))))
	})

	t.Run("map of message", func(t *testing.T) {
		pv, fd := buildField(t, "m_s_m", func(v protoreflect.Value) {
			mp := v.Map()
			e := mp.NewValue()
			e.Message().Set(fieldOf(t, "s_1"), protoreflect.ValueOfString("hi"))
			mp.Set(protoreflect.ValueOfString("k").MapKey(), e)
		})
		valueOf(t, pv, fd, patch.SiteField, patch.Map(patch.E(
			patch.MapStr("k"), patch.Msg(patch.F(patch.Num(109), patch.Str("hi"))),
		)))
	})

	t.Run("message subfields", func(t *testing.T) {
		// Compared per field rather than as a whole document: messageValueOf
		// emits in Message.Range order, and Range is explicitly unordered.
		pv, fd := buildField(t, "m_1", func(v protoreflect.Value) {
			m := v.Message()
			m.Set(fieldOf(t, "s_1"), protoreflect.ValueOfString("hi"))
			m.Set(fieldOf(t, "i32_1"), protoreflect.ValueOfInt32(7))
		})
		v, err := patch.ValueOf(pv, fd, patch.SiteField, "")
		if err != nil {
			t.Fatalf("ValueOf: %v", err)
		}
		want := map[uint32]*patchpb.Value{
			109: pbValue(t, patch.Str("hi")),
			105: pbValue(t, patch.Int32(7)),
		}
		got := map[uint32]*patchpb.Value{}
		for _, f := range v.GetM().GetFields() {
			got[f.GetKey().GetNumber()] = f.GetValue()
		}
		if len(got) != len(want) {
			t.Fatalf("got %d fields, want %d", len(got), len(want))
		}
		for n, w := range want {
			if !proto.Equal(got[n], w) {
				t.Errorf("field %d = %v, want %v", n, got[n], w)
			}
		}
		if err := patch.Validate(assigning(t, v)); err != nil {
			t.Errorf("Validate refused it: %v", err)
		}
	})

	t.Run("nested message", func(t *testing.T) {
		pv, fd := buildField(t, "m_1", func(v protoreflect.Value) {
			inner := v.Message().Mutable(fieldOf(t, "m_1")).Message()
			inner.Set(fieldOf(t, "s_1"), protoreflect.ValueOfString("deep"))
		})
		valueOf(t, pv, fd, patch.SiteField, patch.Msg(patch.F(patch.Num(111),
			patch.Msg(patch.F(patch.Num(109), patch.Str("deep"))),
		)))
	})
}

// TestValueOfMessageKeysByNumber pins messageValueOf's choice. The number is
// protobuf's stable identity, so a Value built from a live message keeps its
// meaning across a rename; a name would not.
func TestValueOfMessageKeysByNumber(t *testing.T) {
	pv, fd := buildField(t, "m_1", func(v protoreflect.Value) {
		v.Message().Set(fieldOf(t, "s_1"), protoreflect.ValueOfString("hi"))
	})
	v, err := patch.ValueOf(pv, fd, patch.SiteField, "")
	if err != nil {
		t.Fatalf("ValueOf: %v", err)
	}
	key := v.GetM().GetFields()[0].GetKey()
	if got := key.GetNumber(); got != 109 {
		t.Errorf("number = %d, want 109", got)
	}
	if got := key.GetName(); got != "" {
		t.Errorf("name = %q, want it left unpinned", got)
	}
}

// TestValueOfEmptyContainers separates "empty" from "absent". An empty list is
// still a list, so the document has to say `l` rather than say nothing, and the
// same holds for a map and for a message with no fields set.
func TestValueOfEmptyContainers(t *testing.T) {
	tests := []struct {
		field string
		want  patch.Value
	}{
		{"r_s_1", patch.List()},
		{"r_bs_1", patch.List()},
		{"m_s_s", patch.Map()},
		{"m_s_bs", patch.Map()},
		{"m_1", patch.Msg()},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			pv, fd := readUnset(t, tt.field)
			valueOf(t, pv, fd, patch.SiteField, tt.want)
		})
	}
}

// TestValueOfEmptyString is the counterpart to the nil-bytes case, and the
// reason it is not a second instance of the same bug: `s` is written through a
// *string, which carries its own presence, so the empty string names its arm
// without any normalization.
func TestValueOfEmptyString(t *testing.T) {
	pv, fd := readUnset(t, "s_1")
	v := valueOf(t, pv, fd, patch.SiteField, patch.Str(""))
	if v.WhichKind() != patchpb.Value_S_case {
		t.Errorf("arm = %v, want s", v.WhichKind())
	}
}

// TestValueOfClosedEnum checks that a number read off a live closed enum is
// one the enum declares, so ValueOf's output cannot fail the closed-enum rule
// that CheckArm applies to it.
func TestValueOfClosedEnum(t *testing.T) {
	for _, n := range []protoreflect.EnumNumber{0, 1, 2} {
		pv, fd := readField(t, "closed_1", protoreflect.ValueOfEnum(n))
		valueOf(t, pv, fd, patch.SiteField, patch.Enum(int32(n)))
	}
}

// TestValueOfSites checks that the same field yields a different arm depending
// on where the value came from: the list as a whole takes `l`, one element of
// it takes the element's own arm.
func TestValueOfSites(t *testing.T) {
	pv, fd := buildField(t, "r_s_1", func(v protoreflect.Value) {
		v.List().Append(protoreflect.ValueOfString("a"))
	})
	whole, err := patch.ValueOf(pv, fd, patch.SiteField, "")
	if err != nil {
		t.Fatalf("ValueOf(field): %v", err)
	}
	if got := patch.ShapeOf(whole); got != patch.ShapeList {
		t.Errorf("field shape = %v, want ShapeList", got)
	}

	elem, err := patch.ValueOf(pv.List().Get(0), fd, patch.SiteElement, "")
	if err != nil {
		t.Fatalf("ValueOf(element): %v", err)
	}
	if got := elem.WhichKind(); got != patchpb.Value_S_case {
		t.Errorf("element arm = %v, want s", got)
	}
}
