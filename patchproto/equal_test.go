package patchproto_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// unknownField is a field number no sample message declares, encoded as a
// varint so that it survives as an unknown field.
var unknownField = protoreflect.RawFields([]byte{0xF8, 0x7F, 0x2A}) // field 255 = 42

// The format promises to preserve unknown fields on the target precisely
// because a Value has no way to mention one. Counting them in equality made
// `test` unsatisfiable against any message carrying one: no Value could be
// written that would pass.
func TestEqualityIgnoresUnknownFields(t *testing.T) {
	withUnknown := func() *sample.Value {
		inner := &sample.Value{}
		inner.SetS_1("v")
		inner.ProtoReflect().SetUnknown(unknownField)
		m := &sample.Value{}
		m.SetM_1(inner)
		return m
	}

	holds := patch.MustNew(mt, patch.Target(patch.Name("m_1")).Test(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("v"))),
	))
	if _, err := patchproto.Apply(withUnknown(), holds); err != nil {
		t.Fatalf("test should hold: %v", err)
	}

	// Ignoring them must not weaken the rest: equality stays exact.
	fails := patch.MustNew(mt, patch.Target(patch.Name("m_1")).Test(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("other"))),
	))
	if got := patch.CodeOf(mustFail(t, withUnknown(), fails)); got != patch.CodeTestFailed {
		t.Errorf("code = %v, want %v", got, patch.CodeTestFailed)
	}

	// ... and they survive the patch, as the preservation rule requires.
	in := withUnknown()
	out, err := patchproto.Apply(in, patch.MustNew(mt,
		patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("rewritten")),
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out.GetM_1().ProtoReflect().GetUnknown()) == 0 {
		t.Error("the unknown field was discarded")
	}
}

// A list element and a map value are compared by the same rule as a field.
func TestEqualityIgnoresUnknownFieldsInCollections(t *testing.T) {
	el := &sample.Value{}
	el.SetS_1("v")
	el.ProtoReflect().SetUnknown(unknownField)

	in := &sample.Value{}
	in.SetRM_1([]*sample.Value{el})

	p := patch.MustNew(mt, patch.Target(patch.Index(0)).In(patch.Name("r_m_1")).Test(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("v"))),
	))
	if _, err := patchproto.Apply(in, p); err != nil {
		t.Fatalf("test should hold: %v", err)
	}
}

// NaN equals NaN, and it must not depend on whether the float sits at the top
// level or inside a message — those went through different code paths and gave
// different answers.
func TestEqualityOfFloats(t *testing.T) {
	nan, negZero := math.NaN(), math.Copysign(0, -1)

	tests := []struct {
		name  string
		set   func(*sample.Value)
		want  patch.Value
		holds bool
	}{
		{"NaN equals NaN", func(m *sample.Value) { m.SetOptF64(nan) }, patch.Float64(nan), true},
		{"NaN does not equal 1", func(m *sample.Value) { m.SetOptF64(nan) }, patch.Float64(1), false},
		{"1 does not equal NaN", func(m *sample.Value) { m.SetOptF64(1) }, patch.Float64(nan), false},
		{"-0.0 equals +0.0", func(m *sample.Value) { m.SetOptF64(negZero) }, patch.Float64(0), true},
		{"ordinary values", func(m *sample.Value) { m.SetOptF64(1.5) }, patch.Float64(1.5), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// at the top level
			in := &sample.Value{}
			tt.set(in)
			_, err := patchproto.Apply(in, patch.MustNew(mt,
				patch.Target(patch.Name("opt_f64")).Test(tt.want),
			))
			if (err == nil) != tt.holds {
				t.Errorf("scalar: holds = %v, want %v (err: %v)", err == nil, tt.holds, err)
			}

			// and nested one level down, which must agree
			inner := &sample.Value{}
			tt.set(inner)
			deep := &sample.Value{}
			deep.SetM_1(inner)
			_, err = patchproto.Apply(deep, patch.MustNew(mt,
				patch.Target(patch.Name("m_1")).Test(patch.Msg(
					patch.F(patch.Name("opt_f64"), tt.want),
				)),
			))
			if (err == nil) != tt.holds {
				t.Errorf("nested: holds = %v, want %v (err: %v)", err == nil, tt.holds, err)
			}
		})
	}

	// float32 too, since it takes a different arm
	in := &sample.Value{}
	in.SetOptF32(float32(math.NaN()))
	if _, err := patchproto.Apply(in, patch.MustNew(mt,
		patch.Target(patch.Name("opt_f32")).Test(patch.Float32(float32(math.NaN()))),
	)); err != nil {
		t.Errorf("f32 NaN: %v", err)
	}
}

// Equality on a message is exact: a field absent from the Value asserts that
// the target does not have it set. Only unknown fields are exempt.
func TestEqualityOfMessageIsExact(t *testing.T) {
	inner := &sample.Value{}
	inner.SetS_1("v")
	inner.SetS_2("also here")
	in := &sample.Value{}
	in.SetM_1(inner)

	subset := patch.MustNew(mt, patch.Target(patch.Name("m_1")).Test(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("v"))),
	))
	if got := patch.CodeOf(mustFail(t, in, subset)); got != patch.CodeTestFailed {
		t.Errorf("a subset must not pass: code = %v", got)
	}

	full := patch.MustNew(mt, patch.Target(patch.Name("m_1")).Test(
		patch.Msg(
			patch.F(patch.Name("s_1"), patch.Str("v")),
			patch.F(patch.Name("s_2"), patch.Str("also here")),
		),
	))
	if _, err := patchproto.Apply(in, full); err != nil {
		t.Errorf("the exact value must pass: %v", err)
	}
}

func mustFail(t *testing.T, in *sample.Value, p *patchpb.Patch) error {
	t.Helper()
	_, err := patchproto.Apply(in, p)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}
