package conformance_test

import (
	"testing"

	"github.com/lesomnus/protobuf-patch/conformance"
	"github.com/lesomnus/protobuf-patch/conformance/conformancepb"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// TestCorpusIsWellFormed checks the cases themselves. A case with a typo in an
// expected error name, or one that forgot to say what should happen, would
// otherwise pass silently in every implementation that runs the corpus.
func TestCorpusIsWellFormed(t *testing.T) {
	cases, err := conformance.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("the corpus is empty")
	}

	for _, c := range cases {
		t.Run(c.GetName(), func(t *testing.T) {
			if c.GetDoc() == "" {
				t.Error("no doc; a case should say which rule it pins")
			}
			if !c.HasPatch() {
				t.Fatal("no patch")
			}

			switch c.WhichWant() {
			case conformancepb.Case_Error_case:
				if _, ok := patch.CodeByName(c.GetError()); !ok {
					t.Errorf("expects %q, which is not a patch.Code name", c.GetError())
				}
			case conformancepb.Case_Output_case:
				// A case that expects success must carry a document that is
				// structurally valid, or it is testing the validator by
				// accident rather than the applier on purpose.
				if err := patch.Validate(c.GetPatch()); err != nil {
					t.Errorf("expects success but the patch does not validate: %v", err)
				}
			default:
				t.Error("says nothing about what should happen")
			}
		})
	}
}

// TestCorpusCoversTheOperations fails when a kind has no case at all, so that
// adding an operation to the schema cannot leave the shared corpus silent
// about it.
func TestCorpusCoversTheOperations(t *testing.T) {
	cases, err := conformance.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	kinds := []struct {
		name string
		arm  any
	}{
		{"remove", patchpb.Entry_Remove_case},
		{"test", patchpb.Entry_Test_case},
		{"insert", patchpb.Entry_Insert_case},
		{"assign", patchpb.Entry_Assign_case},
		{"move", patchpb.Entry_Move_case},
		{"copy", patchpb.Entry_Copy_case},
	}

	seen := map[any]bool{}
	var walk func(*patchpb.Delta)
	walk = func(d *patchpb.Delta) {
		for _, e := range d.GetEntries() {
			seen[any(e.WhichKind())] = true
			if e.WhichKind() == patchpb.Entry_Nest_case {
				walk(e.GetNest().GetDelta())
			}
		}
	}
	for _, c := range cases {
		walk(c.GetPatch().GetDelta())
	}

	for _, k := range kinds {
		if !seen[k.arm] {
			t.Errorf("no case exercises %s", k.name)
		}
	}
}
