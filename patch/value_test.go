package patch_test

import (
	"math"
	"testing"

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
