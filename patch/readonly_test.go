package patch_test

import (
	"errors"
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
)

func check(t *testing.T, ro *patch.ReadOnly, ops ...patch.Op) error {
	t.Helper()
	return ro.Check(patch.MustNewUntyped(ops[0], ops[1:]...))
}

func TestReadOnly(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "s_1", "m_1.s_2", "m_s_s")

	tests := []struct {
		name    string
		op      patch.Op
		refused bool
	}{
		{"an untouched field", patch.Target(patch.Name("s_2")).Assign(patch.Str("v")), false},
		{"the protected field itself", patch.Target(patch.Name("s_1")).Assign(patch.Str("v")), true},
		{"removing it", patch.Target(patch.Name("s_1")).Remove(), true},

		// A test only reads, so asserting about a protected field is fine —
		// which is what makes optimistic concurrency on an etag possible while
		// the etag itself stays server-owned.
		{"asserting its value", patch.Target(patch.Name("s_1")).Test(patch.Str("v")), false},
		{"asserting its absence", patch.Target(patch.Name("s_1")).Exists(false), false},

		// Reading it into somewhere else is fine; moving it is not, because a
		// move empties the source.
		{"copying out of it", patch.Target(patch.Name("s_2")).Copy(patch.Here(patch.Name("s_1"))), false},
		{"moving out of it", patch.Target(patch.Name("s_2")).Move(patch.Here(patch.Name("s_1"))), true},
		{"moving into it", patch.Target(patch.Name("s_1")).Move(patch.Here(patch.Name("s_2"))), true},

		// A nested leaf is protected on both sides of the boundary.
		{"the nested leaf", patch.Target(patch.Name("s_2")).In(patch.Name("m_1")).Assign(patch.Str("v")), true},
		{"its sibling", patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("v")), false},
		{"the message that holds it", patch.Target(patch.Name("m_1")).Assign(patch.Msg()), true},
		{"clearing the message that holds it", patch.Target(patch.Name("m_1")).Remove(), true},

		// A map is protected as a whole: the analysis elides keys, so a policy
		// on m_s_s covers every entry of it.
		{"one entry of it", patch.Target(patch.MapStr("k")).In(patch.Name("m_s_s")).Assign(patch.Str("v")), true},
		{"every entry of it", patch.Target(patch.EveryEntry()).In(patch.Name("m_s_s")).Remove(), true},

		// Emptying the whole message takes the protected fields with it.
		{"emptying the root", patch.Container().Remove(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, ro, tt.op)
			if got := err != nil; got != tt.refused {
				t.Fatalf("refused = %v (%v), want %v", got, err, tt.refused)
			}
			if err != nil && patch.CodeOf(err) != patch.CodeReadOnly {
				t.Errorf("code = %v, want read-only", patch.CodeOf(err))
			}
		})
	}
}

// TestReadOnlyThroughNest: the protection does not stop at a nest boundary,
// which is the obvious way to try to sneak past a check that only looked at
// top-level entries.
func TestReadOnlyThroughNest(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "m_1.m_1.s_1")

	err := check(t, ro, patch.Target(patch.Name("m_1")).Nest(
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
		),
	))
	if patch.CodeOf(err) != patch.CodeReadOnly {
		t.Fatalf("= %v, want read-only", err)
	}
	// The position is the innermost entry, spelled the same way patchproto
	// spells it when that entry fails, so it can be pasted next to the code
	// that built the document.
	want := "delta.entries[0].nest.delta.entries[0].nest.delta.entries[0].targets.selectors[0]"
	if got := errAt(t, err); got != want {
		t.Errorf("At = %q, want %q", got, want)
	}
}

// TestReadOnlyOneofIsRefusedForAnyMember is the payoff of the oneof
// over-approximation: protecting one member protects the oneof selector, which
// could have resolved to it.
func TestReadOnlyOneofIsRefusedForAnyMember(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "src_i32")

	if err := check(t, ro, patch.Target(patch.Oneof("source")).Remove()); patch.CodeOf(err) != patch.CodeReadOnly {
		t.Fatalf("= %v, want read-only", err)
	}
	// Naming the member directly is still allowed when it is not the protected
	// one, so the widening does not spill onto unrelated members.
	if err := check(t, ro, patch.Target(patch.Name("src_s")).Assign(patch.Str("v"))); err != nil {
		t.Fatalf("= %v, want allowed", err)
	}
}

// TestReadOnlyReportsWhichWayItOverlaps: naming only the protected field is
// unhelpful when the entry the author wrote names something else, so the
// detail says which of the three relationships was found.
func TestReadOnlyReportsWhichWayItOverlaps(t *testing.T) {
	tests := []struct {
		name     string
		readonly string
		op       patch.Op
		want     string
	}{
		{
			"exactly",
			"m_1.s_1",
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("v")),
			"m_1.s_1 is read-only",
		},
		{
			"an ancestor is replaced",
			"m_1.s_1",
			patch.Target(patch.Name("m_1")).Assign(patch.Msg()),
			"m_1.s_1 is read-only, and this replaces m_1, which holds it",
		},
		{
			"a descendant is written",
			"m_1",
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1"), patch.Name("m_1")).Assign(patch.Str("v")),
			"m_1 is read-only, and this writes m_1.m_1.s_1 inside it",
		},
		{
			"the root",
			"",
			patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
			"the message itself is read-only, and this writes s_1 inside it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := check(t, patch.MustNewReadOnly(sampleDesc, tt.readonly), tt.op)
			if err == nil {
				t.Fatal("allowed, want refused")
			}
			if got := errDetail(t, err); got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadOnlyRefusesAnUnreadableDocument: a check whose job is to say the
// document respects the policy must not pass one it could not read.
func TestReadOnlyRefusesAnUnreadableDocument(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "s_1")

	p := patch.MustNewUntyped(patch.Target(patch.Name("s_2")).Assign(patch.Str("v")))
	setUnknown(p.GetDelta().GetEntries()[0], 9999)
	if got := patch.CodeOf(ro.Check(p)); got != patch.CodeUnknownField {
		t.Errorf("unknown field: = %v", got)
	}

	q := patch.MustNew("other.Message", patch.Target(patch.Name("s_2")).Assign(patch.Str("v")))
	if got := patch.CodeOf(ro.Check(q)); got != patch.CodeMessageTypeMismatch {
		t.Errorf("wrong message type: = %v", got)
	}

	if got := patch.CodeOf(ro.Check(nil)); got != patch.CodeMissingField {
		t.Errorf("nil patch: = %v", got)
	}
}

// TestNewReadOnlyRefusesABadPath makes a typo in a policy a startup failure
// rather than a check that quietly protects nothing.
func TestNewReadOnlyRefusesABadPath(t *testing.T) {
	if _, err := patch.NewReadOnly(sampleDesc, "s_1", "nope"); patch.CodeOf(err) != patch.CodeVacantTarget {
		t.Fatalf("= %v, want vacant target", err)
	}
	if _, err := patch.NewReadOnly(nil, "s_1"); patch.CodeOf(err) != patch.CodeMissingField {
		t.Fatalf("nil descriptor: = %v", err)
	}
}

// TestReadOnlyRoot: the empty path names the message itself, so nothing may be
// written at all.
func TestReadOnlyRoot(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "")
	if err := check(t, ro, patch.Target(patch.Name("s_2")).Assign(patch.Str("v"))); patch.CodeOf(err) != patch.CodeReadOnly {
		t.Fatalf("= %v, want read-only", err)
	}
	// A test still reads.
	if err := check(t, ro, patch.Target(patch.Name("s_2")).Exists(true)); err != nil {
		t.Fatalf("= %v, want allowed", err)
	}
}

func TestReadOnlyEmptyPolicyAllowsEverything(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc)
	if err := check(t, ro, patch.Container().Remove()); err != nil {
		t.Fatalf("= %v, want allowed", err)
	}
}

// errDetail is errAt's sibling: the Detail of the first *patch.Error in the
// chain. See validate_test.go for errAt.
func errDetail(t *testing.T, err error) string {
	t.Helper()
	var e *patch.Error
	if !errors.As(err, &e) {
		t.Fatalf("not a *patch.Error: %v", err)
	}
	return e.Detail
}

// TestReadOnlyAllowsCreatingThroughAProtectedAncestor is the counterpart to
// TestReadOnlyThroughNest. Creating a container cannot drop what is under it,
// so a policy on a leaf must not refuse every nest and every InOrCreate that
// merely passes through an ancestor of that leaf. Without the distinction the
// check would be unusable on any deeply nested message.
func TestReadOnlyAllowsCreatingThroughAProtectedAncestor(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "m_1.m_1.s_1")

	// Nesting through m_1 to write a different field.
	if err := check(t, ro, patch.Target(patch.Name("m_1")).Nest(
		patch.Target(patch.Name("s_2")).Assign(patch.Str("v")),
	)); err != nil {
		t.Errorf("nest through m_1: = %v, want allowed", err)
	}

	// Creating m_1.m_1 on the way to a sibling of the protected leaf.
	if err := check(t, ro, patch.Target(patch.Name("s_2")).
		InOrCreate(patch.Name("m_1"), patch.Name("m_1")).Assign(patch.Str("v"))); err != nil {
		t.Errorf("create through m_1.m_1: = %v, want allowed", err)
	}

	// Assigning m_1 outright still drops the leaf, so that stays refused.
	if err := check(t, ro, patch.Target(patch.Name("m_1")).Assign(patch.Msg())); patch.CodeOf(err) != patch.CodeReadOnly {
		t.Errorf("assign m_1: = %v, want read-only", err)
	}
}

// TestReadOnlyRefusesCreatingTheProtectedContainerItself: the other side of
// the same coin. If the protected field IS the container, bringing it into
// existence changes it.
func TestReadOnlyRefusesCreatingTheProtectedContainerItself(t *testing.T) {
	ro := patch.MustNewReadOnly(sampleDesc, "m_1")

	if err := check(t, ro, patch.Target(patch.Name("m_1")).Nest(
		patch.Target(patch.Name("s_1")).Exists(true),
	)); patch.CodeOf(err) != patch.CodeReadOnly {
		t.Errorf("nest that only asserts: = %v, want read-only", err)
	}
	if err := check(t, ro, patch.Target(patch.Name("s_1")).
		InOrCreate(patch.Name("m_1")).Assign(patch.Str("v"))); patch.CodeOf(err) != patch.CodeReadOnly {
		t.Errorf("create m_1: = %v, want read-only", err)
	}
}
