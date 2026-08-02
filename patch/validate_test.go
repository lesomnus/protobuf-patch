package patch_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// setUnknown gives m a field number it does not declare, standing in for a
// document written by a newer revision of the schema.
func setUnknown(m proto.Message, num protowire.Number) {
	b := protowire.AppendTag(nil, num, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	m.ProtoReflect().SetUnknown(protoreflect.RawFields(b))
}

func TestValidateAcceptsWhatTheBuilderProduces(t *testing.T) {
	tests := []struct {
		name string
		p    *patchpb.Patch
	}{
		{"assign", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Assign(patch.Str("hi")))},
		{"remove at container", patch.MustNew(mt, patch.Container().Remove())},
		{"path", patch.MustNew(mt, patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Remove())},
		{"span", patch.MustNew(mt, patch.Target(patch.Span(-2, -1)).Remove())},
		{"span open", patch.MustNew(mt, patch.Target(patch.SpanAll()).Remove())},
		{"append insert", patch.MustNew(mt, patch.Target(patch.Append()).Insert(patch.Str("z")))},
		{"skip", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Skip().Remove())},
		{"test value", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Test(patch.Str("v")))},
		{"test exists", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Exists(false))},
		{"move here", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Move(patch.Here(patch.Name("s_2"))))},
		{"copy from", patch.MustNew(mt, patch.Target(patch.Name("s_1")).Copy(patch.From(patch.Name("s_2"), patch.Name("m_1"))))},
		{
			"nest",
			patch.MustNew(mt, patch.Target(patch.Name("m_1")).Nest(
				patch.Target(patch.Name("s_1")).Assign(patch.Str("deep")),
			)),
		},
		{
			"nested containers",
			patch.MustNew(mt, patch.Container().In(patch.Name("m_s_m"), patch.MapStr("k")).Assign(
				patch.Msg(patch.F(patch.Name("s_1"), patch.Str("x"))),
			)),
		},
		{
			"every value kind",
			patch.MustNew(mt,
				patch.Target(patch.Name("b_1")).Assign(patch.Bool(true)),
				patch.Target(patch.Name("i32_1")).Assign(patch.Int32(1)),
				patch.Target(patch.Name("e_1")).Assign(patch.Enum(-3)),
				patch.Target(patch.Name("bs_1")).Assign(patch.Bytes(nil)),
				patch.Target(patch.Name("r_s_1")).Assign(patch.List(patch.Str("a"))),
				patch.Target(patch.Name("m_s_s")).Assign(patch.Map(patch.E(patch.MapStr("k"), patch.Str("v")))),
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := patch.Validate(tt.p); err != nil {
				t.Fatalf("Validate rejected a well-formed Patch: %v", err)
			}
		})
	}
}

// TestValidateRefusesUnknownFields is the load-bearing test of the whole
// package. protobuf reports an unrecognized oneof arm as "not set", so without
// this scan a document from a newer revision is silently applied in part —
// and for Value in particular, an arm this build cannot read would otherwise
// look like no value at all.
func TestValidateRefusesUnknownFields(t *testing.T) {
	t.Run("on the Patch itself", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		setUnknown(p, 99)
		assertCode(t, patch.Validate(p), patch.CodeUnknownField)
	})

	t.Run("on a nested Entry", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		setUnknown(p.GetDelta().GetEntries()[0], 20)
		assertCode(t, patch.Validate(p), patch.CodeUnknownField)
	})

	t.Run("on an empty payload message", func(t *testing.T) {
		// Remove{} carries no fields today and exists so that the operation can
		// gain options later. A v1 reader must refuse those options, not drop
		// them and apply the bare operation.
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		setUnknown(p.GetDelta().GetEntries()[0].GetRemove(), 1)
		assertCode(t, patch.Validate(p), patch.CodeUnknownField)
	})

	t.Run("a future Value arm is not read as no value", func(t *testing.T) {
		// Value reserves 14-15 for future arms. One of them arriving must abort
		// the document -- never be mistaken for an unset kind, which the schema
		// deliberately made an error precisely so this cannot become "clear".
		v := &patchpb.Value{}
		setUnknown(v, 14)
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		p.GetDelta().GetEntries()[0].SetAssign(patchpb.Assign_builder{Value: v}.Build())

		assertCode(t, patch.Validate(p), patch.CodeUnknownField)
	})

	t.Run("deep inside a nested delta", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("m_2")).Nest(
				patch.Target(patch.Name("s_1")).Assign(patch.Str("deep")),
			),
		))
		deep := p.GetDelta().GetEntries()[0].
			GetNest().GetDelta().GetEntries()[0].
			GetNest().GetDelta().GetEntries()[0]
		setUnknown(deep.GetAssign().GetValue(), 14)

		err := patch.Validate(p)
		assertCode(t, err, patch.CodeUnknownField)

		want := "delta.entries[0].nest.delta.entries[0].nest.delta.entries[0].assign.value"
		if got := errAt(t, err); got != want {
			t.Errorf("At = %q, want %q", got, want)
		}
	})

	t.Run("inside a repeated field element", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1"), patch.Name("s_2")).Remove())
		sels := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()
		setUnknown(sels[1], 9)

		err := patch.Validate(p)
		assertCode(t, err, patch.CodeUnknownField)
		if got := errAt(t, err); got != "delta.entries[0].targets.selectors[1]" {
			t.Errorf("At = %q, want the second selector", got)
		}
	})

	t.Run("survives a wire round trip", func(t *testing.T) {
		// The realistic path: a v2 producer marshals, a v1 reader unmarshals.
		// The unknown field must still be there to be refused.
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		setUnknown(p.GetDelta().GetEntries()[0], 20)
		b, err := proto.Marshal(p)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var got patchpb.Patch
		if err := proto.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		assertCode(t, patch.Validate(&got), patch.CodeUnknownField)
	})
}

func TestValidateDocumentLevel(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assertCode(t, patch.Validate(nil), patch.CodeMissingField)
	})

	t.Run("no message_type", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		p.ClearMessageType()
		assertCode(t, patch.Validate(p), patch.CodeMissingField)
	})

	t.Run("no delta", func(t *testing.T) {
		p := patchpb.Patch_builder{MessageType: proto.String(mt)}.Build()
		assertCode(t, patch.Validate(p), patch.CodeMissingField)
	})

	t.Run("empty delta", func(t *testing.T) {
		p := patchpb.Patch_builder{
			MessageType: proto.String(mt),
			Delta:       &patchpb.Delta{},
		}.Build()
		assertCode(t, patch.Validate(p), patch.CodeEmptyCollection)
	})

	t.Run("revision from the future", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		p.SetMinReaderRevision(patch.Revision + 1)
		assertCode(t, patch.Validate(p), patch.CodeReaderRevisionTooOld)
	})

	t.Run("revision this build implements", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		p.SetMinReaderRevision(patch.Revision)
		if err := patch.Validate(p); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}

func TestValidateEntry(t *testing.T) {
	// These states are unreachable through the builder by construction, so they
	// are assembled from patchpb directly -- which is exactly the population
	// that needs validating.
	base := func() *patchpb.Entry {
		return patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove()).GetDelta().GetEntries()[0]
	}
	wrap := func(e *patchpb.Entry) *patchpb.Patch {
		return patchpb.Patch_builder{
			MessageType: proto.String(mt),
			Delta:       patchpb.Delta_builder{Entries: []*patchpb.Entry{e}}.Build(),
		}.Build()
	}

	tests := []struct {
		name string
		make func() *patchpb.Entry
		want patch.Code
	}{
		{
			"no scope",
			func() *patchpb.Entry {
				e := base()
				e.ClearTargets()
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"no kind",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"empty selectors",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(&patchpb.Targets{})
				return e
			},
			patch.CodeEmptyCollection,
		},
		{
			"unrecognized on_missing",
			func() *patchpb.Entry {
				e := base()
				e.SetOnMissing(patchpb.OnMissing(7))
				return e
			},
			patch.CodeUnrecognizedEnum,
		},
		{
			"test that tolerates a missing target",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetTest(patchpb.Test_builder{Exists: proto.Bool(false)}.Build())
				e.SetOnMissing(patchpb.OnMissing_ON_MISSING_SKIP)
				return e
			},
			patch.CodeTestNotStrict,
		},
		{
			"test with no want",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetTest(&patchpb.Test{})
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"move onto a container",
			func() *patchpb.Entry {
				e := base()
				e.ClearTargets()
				e.SetContainer(&patchpb.Container{})
				e.ClearRemove()
				e.SetMove(patchpb.Move_builder{
					From: patchpb.Location_builder{
						SameContainer: &patchpb.SameContainer{},
						Key:           patchpb.Key_builder{Field: patchpb.Field_builder{Name: proto.String("s_2")}.Build()}.Build(),
					}.Build(),
				}.Build())
				return e
			},
			patch.CodeIllegalScope,
		},
		{
			"append with remove",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{
						patchpb.Selector_builder{Append: &patchpb.Append{}}.Build(),
					},
				}.Build())
				return e
			},
			patch.CodeIllegalSelector,
		},
		{
			"selector with no arm",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{{}},
				}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"key with no arm",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{
						patchpb.Selector_builder{Key: &patchpb.Key{}}.Build(),
					},
				}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"field with no identifier",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{
						patchpb.Selector_builder{
							Key: patchpb.Key_builder{Field: &patchpb.Field{}}.Build(),
						}.Build(),
					},
				}.Build())
				return e
			},
			patch.CodeFieldNoIdentifier,
		},
		{
			"field number zero",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{
						patchpb.Selector_builder{
							Key: patchpb.Key_builder{
								Field: patchpb.Field_builder{Number: proto.Uint32(0)}.Build(),
							}.Build(),
						}.Build(),
					},
				}.Build())
				return e
			},
			patch.CodeFieldNoIdentifier,
		},
		{
			"map key with no arm",
			func() *patchpb.Entry {
				e := base()
				e.SetTargets(patchpb.Targets_builder{
					Selectors: []*patchpb.Selector{
						patchpb.Selector_builder{
							Key: patchpb.Key_builder{MapKey: &patchpb.MapKey{}}.Build(),
						}.Build(),
					},
				}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"assign with no value",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetAssign(&patchpb.Assign{})
				return e
			},
			patch.CodeMissingField,
		},
		{
			"assign a value with no kind",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetAssign(patchpb.Assign_builder{Value: &patchpb.Value{}}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"nest with no delta",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetNest(&patchpb.Nest{})
				return e
			},
			patch.CodeMissingField,
		},
		{
			"nest with an empty delta",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetNest(patchpb.Nest_builder{Delta: &patchpb.Delta{}}.Build())
				return e
			},
			patch.CodeEmptyCollection,
		},
		{
			"location with no origin",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetCopy(patchpb.Copy_builder{
					From: patchpb.Location_builder{
						Key: patchpb.Key_builder{
							Field: patchpb.Field_builder{Name: proto.String("s_2")}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"location with no key",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetCopy(patchpb.Copy_builder{
					From: patchpb.Location_builder{SameContainer: &patchpb.SameContainer{}}.Build(),
				}.Build())
				return e
			},
			patch.CodeMissingField,
		},
		{
			"list element with no kind",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetAssign(patchpb.Assign_builder{
					Value: patchpb.Value_builder{
						L: patchpb.ListValue_builder{Values: []*patchpb.Value{{}}}.Build(),
					}.Build(),
				}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
		{
			"map entry with no value",
			func() *patchpb.Entry {
				e := base()
				e.ClearRemove()
				e.SetAssign(patchpb.Assign_builder{
					Value: patchpb.Value_builder{
						Map: patchpb.MapValue_builder{
							Entries: []*patchpb.MapEntry{
								patchpb.MapEntry_builder{
									Key: patchpb.MapKey_builder{S: proto.String("k")}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				return e
			},
			patch.CodeMissingField,
		},
		{
			"path segment with no arm",
			func() *patchpb.Entry {
				e := base()
				e.SetPath(patchpb.Path_builder{Segments: []*patchpb.Key{{}}}.Build())
				return e
			},
			patch.CodeMissingOneof,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertCode(t, patch.Validate(wrap(tt.make())), tt.want)
		})
	}
}

func assertCode(t *testing.T, err error, want patch.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want %v", want)
	}
	if got := patch.CodeOf(err); got != want {
		t.Fatalf("CodeOf = %v, want %v (err = %v)", got, want, err)
	}
}

func errAt(t *testing.T, err error) string {
	t.Helper()
	var e *patch.Error
	if !errors.As(err, &e) {
		t.Fatalf("not a *patch.Error: %v", err)
	}
	return string(e.At)
}
