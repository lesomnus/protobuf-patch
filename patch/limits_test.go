package patch_test

import (
	"runtime"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

func sel(name string) *patchpb.Selector {
	return patchpb.Selector_builder{Key: patchpb.Key_builder{
		Field: patchpb.Field_builder{Name: proto.String(name)}.Build(),
	}.Build()}.Build()
}

func wrap(e *patchpb.Entry) *patchpb.Patch {
	return patchpb.Patch_builder{
		Delta: patchpb.Delta_builder{Entries: []*patchpb.Entry{e}}.Build(),
	}.Build()
}

// nestedTo builds a Patch whose deltas are chained n levels deep.
func nestedTo(n int) *patchpb.Patch {
	e := patchpb.Entry_builder{
		Targets: patchpb.Targets_builder{Selectors: []*patchpb.Selector{sel("s_1")}}.Build(),
		Remove:  patchpb.Remove_builder{}.Build(),
	}.Build()
	for range n {
		e = patchpb.Entry_builder{
			Targets: patchpb.Targets_builder{Selectors: []*patchpb.Selector{sel("m_1")}}.Build(),
			Nest: patchpb.Nest_builder{
				Delta: patchpb.Delta_builder{Entries: []*patchpb.Entry{e}}.Build(),
			}.Build(),
		}.Build()
	}
	return wrap(e)
}

// valuedTo builds a Patch with a single entry whose Value nests n levels.
func valuedTo(n int) *patchpb.Patch {
	v := patchpb.Value_builder{S: proto.String("leaf")}.Build()
	for range n {
		v = patchpb.Value_builder{M: patchpb.MessageValue_builder{
			Fields: []*patchpb.FieldValue{patchpb.FieldValue_builder{
				Key:   patchpb.Field_builder{Name: proto.String("m_1")}.Build(),
				Value: v,
			}.Build()},
		}.Build()}.Build()
	}
	return wrap(patchpb.Entry_builder{
		Targets: patchpb.Targets_builder{Selectors: []*patchpb.Selector{sel("m_1")}}.Build(),
		Assign:  patchpb.Assign_builder{Value: v}.Build(),
	}.Build())
}

func TestNestDepthLimit(t *testing.T) {
	lim := patch.DefaultLimits

	if err := patch.Validate(nestedTo(lim.NestDepth)); err != nil {
		t.Errorf("%d levels must be accepted: %v", lim.NestDepth, err)
	}
	err := patch.Validate(nestedTo(lim.NestDepth + 1))
	if got := patch.CodeOf(err); got != patch.CodeTooDeep {
		t.Errorf("%d levels: code = %v, want %v", lim.NestDepth+1, got, patch.CodeTooDeep)
	}
}

func TestValueDepthLimit(t *testing.T) {
	lim := patch.DefaultLimits

	// valuedTo(n) puts the innermost scalar at depth n+1.
	if err := patch.Validate(valuedTo(lim.ValueDepth - 1)); err != nil {
		t.Errorf("%d levels must be accepted: %v", lim.ValueDepth, err)
	}
	err := patch.Validate(valuedTo(lim.ValueDepth))
	if got := patch.CodeOf(err); got != patch.CodeTooDeep {
		t.Errorf("%d levels: code = %v, want %v", lim.ValueDepth+1, got, patch.CodeTooDeep)
	}
}

func TestLimitsAreCallerChosen(t *testing.T) {
	p := nestedTo(20)
	if err := patch.Validate(p); patch.CodeOf(err) != patch.CodeTooDeep {
		t.Fatalf("default should refuse: %v", err)
	}
	if err := patch.ValidateWith(p, patch.Limits{NestDepth: 20}); err != nil {
		t.Errorf("a raised limit should accept: %v", err)
	}
	if err := patch.ValidateWith(p, patch.Limits{NestDepth: 5}); patch.CodeOf(err) != patch.CodeTooDeep {
		t.Errorf("a lowered limit should refuse: %v", err)
	}

	// A zero field falls back rather than refusing everything.
	if err := patch.ValidateWith(nestedTo(3), patch.Limits{}); err != nil {
		t.Errorf("zero Limits should mean the defaults: %v", err)
	}
}

// The bound has to be cheap on the documents it refuses, or refusing is itself
// the denial of service. It bails at the first violation, so the work is
// bounded by the limit rather than by the document.
func TestDeepDocumentIsRefusedCheaply(t *testing.T) {
	for _, p := range []*patchpb.Patch{nestedTo(3000), valuedTo(3000)} {
		b, err := proto.Marshal(p)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		err = patch.Validate(p)
		runtime.ReadMemStats(&after)

		if patch.CodeOf(err) != patch.CodeTooDeep {
			t.Fatalf("code = %v, want %v", patch.CodeOf(err), patch.CodeTooDeep)
		}

		alloc := after.TotalAlloc - before.TotalAlloc
		t.Logf("wire %d B, refused after %d B", len(b), alloc)

		// Before the bound this document made Validate allocate several
		// thousand times its own size, growing quadratically with depth.
		if alloc > uint64(len(b)) {
			t.Errorf("refusing allocated %d B for a %d B document", alloc, len(b))
		}
	}
}
