package patchproto_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// An extension is data the message really holds, but it is not in
// MessageDescriptor.Fields, so without the extension-range check it looks
// exactly like a field the message does not declare. That mistake is not
// merely a missing feature: it makes the format answer wrongly rather than
// refuse. These are the four ways it showed.
func TestExtensionIsRefusedNotReportedAbsent(t *testing.T) {
	fresh := func() *sample.Extendable {
		m := &sample.Extendable{}
		m.SetS_1("plain")
		proto.SetExtension(m, sample.E_ExtS, "extended")
		return m
	}

	tests := []struct {
		name string
		op   patch.Op
	}{
		{"remove", patch.Target(patch.Num(100)).Remove()},

		// The worst two. Before the check, `Skip()` made the entry a silent
		// no-op, and `exists=false` HELD — the format asserting that a value
		// sitting in the message is not there.
		{"remove, tolerated", patch.Target(patch.Num(100)).Skip().Remove()},
		{"exists=false", patch.Target(patch.Num(100)).Exists(false)},

		{"test against its real value", patch.Target(patch.Num(100)).Test(patch.Str("extended"))},
		{"assign", patch.Target(patch.Num(100)).Assign(patch.Str("other"))},
		{"in a path", patch.Target(patch.Name("s_1")).In(patch.Num(100)).Remove()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := fresh()
			_, err := patchproto.Apply(in, patch.MustNewUntyped(tt.op))
			if got := patch.CodeOf(err); got != patch.CodeExtensionField {
				t.Fatalf("code = %v, want %v (err: %v)", got, patch.CodeExtensionField, err)
			}
			if !proto.HasExtension(in, sample.E_ExtS) {
				t.Error("the extension was disturbed")
			}
		})
	}
}

// A number outside every extension range stays plain vacancy, which is what
// on_missing is for. The new rule must not swallow that.
func TestNonExtensionNumberIsStillVacancy(t *testing.T) {
	in := &sample.Extendable{}
	in.SetS_1("plain")

	if _, err := patchproto.Apply(in, patch.MustNewUntyped(
		patch.Target(patch.Num(99)).Skip().Remove(),
	)); err != nil {
		t.Fatalf("tolerated: %v", err)
	}

	_, err := patchproto.Apply(in, patch.MustNewUntyped(
		patch.Target(patch.Num(99)).Remove(),
	))
	if got := patch.CodeOf(err); got != patch.CodeVacantTarget {
		t.Fatalf("code = %v, want %v", got, patch.CodeVacantTarget)
	}
}

// An extension the reader cannot resolve arrives as an unknown field on the
// target, which the preservation rule already covers. It must survive a patch
// that names something else.
func TestUnresolvedExtensionIsPreserved(t *testing.T) {
	src := &sample.Extendable{}
	src.SetS_1("plain")
	proto.SetExtension(src, sample.E_ExtS, "extended")
	b, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decode without the extension registered, so it lands in unknown fields.
	in := &sample.Extendable{}
	if err := (proto.UnmarshalOptions{Resolver: emptyResolver{}}).Unmarshal(b, in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(in.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("setup: the extension did not land in unknown fields")
	}

	out, err := patchproto.Apply(in, patch.MustNewUntyped(
		patch.Target(patch.Name("s_1")).Assign(patch.Str("rewritten")),
	))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out.GetS_1() != "rewritten" {
		t.Errorf("s_1 = %q", out.GetS_1())
	}
	if len(out.ProtoReflect().GetUnknown()) == 0 {
		t.Error("the unknown extension was discarded")
	}
}

type emptyResolver struct{}

func (emptyResolver) FindExtensionByName(protoreflect.FullName) (protoreflect.ExtensionType, error) {
	return nil, protoregistry.NotFound
}

func (emptyResolver) FindExtensionByNumber(protoreflect.FullName, protoreflect.FieldNumber) (protoreflect.ExtensionType, error) {
	return nil, protoregistry.NotFound
}
