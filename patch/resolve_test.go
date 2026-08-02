package patch_test

import (
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

func fieldPB(t *testing.T, f patch.Field) *patchpb.Field {
	t.Helper()
	p := patch.MustNew(mt, patch.Target(f).Remove())
	return p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetKey().GetField()
}

func TestResolveField(t *testing.T) {
	// s_1 is field 109 with JSON name s1; s_2 is field 209.
	tests := []struct {
		name  string
		field patch.Field
		want  string
	}{
		{"by name", patch.Name("s_1"), "s_1"},
		{"by number", patch.Num(109), "s_1"},
		{"by json name", patch.JSONName("s1"), "s_1"},
		{"name and number agree", patch.Name("s_1").Num(109), "s_1"},
		{"all three agree", patch.Name("s_1").Num(109).JSONName("s1"), "s_1"},
		{"number wins the lookup", patch.Num(209).Name("s_2"), "s_2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd, vacant, err := patch.ResolveField(sampleDesc, fieldPB(t, tt.field), "")
			if err != nil {
				t.Fatalf("ResolveField: %v", err)
			}
			if vacant {
				t.Fatal("vacant, want resolved")
			}
			if got := string(fd.Name()); got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveFieldConflict is the integrity check the format is built around:
// pinning both a name and a number is how a stored Patch refuses to apply to a
// message whose fields moved underneath it. A conflict must never be reported
// as vacancy, because vacancy can be skipped.
func TestResolveFieldConflict(t *testing.T) {
	tests := []struct {
		name  string
		field patch.Field
	}{
		{"name names another field", patch.Num(109).Name("s_2")},
		{"number names another field", patch.Name("s_1").Num(209)},
		{"name does not exist at all", patch.Num(109).Name("gone")},
		{"json name disagrees", patch.Num(109).JSONName("s2")},
		{"json name does not exist", patch.Name("s_1").JSONName("nope")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, vacant, err := patch.ResolveField(sampleDesc, fieldPB(t, tt.field), "")
			if vacant {
				t.Fatal("reported as vacant; a conflict must not be skippable")
			}
			if patch.CodeOf(err) != patch.CodeFieldConflict {
				t.Fatalf("CodeOf = %v, want CodeFieldConflict (err=%v)", patch.CodeOf(err), err)
			}
		})
	}
}

func TestResolveFieldVacant(t *testing.T) {
	tests := []struct {
		name  string
		field patch.Field
	}{
		{"unknown name", patch.Name("no_such_field")},
		{"unknown number", patch.Num(999999)},
		{"unknown json name", patch.JSONName("noSuchField")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd, vacant, err := patch.ResolveField(sampleDesc, fieldPB(t, tt.field), "")
			if err != nil {
				t.Fatalf("ResolveField: %v", err)
			}
			if !vacant {
				t.Fatalf("resolved to %v, want vacant", fd)
			}
		})
	}
}

func TestResolveFieldRejectsNoIdentifier(t *testing.T) {
	_, _, err := patch.ResolveField(sampleDesc, &patchpb.Field{}, "")
	if patch.CodeOf(err) != patch.CodeFieldNoIdentifier {
		t.Fatalf("CodeOf = %v, want CodeFieldNoIdentifier", patch.CodeOf(err))
	}
}

func TestNormalizeIndex(t *testing.T) {
	tests := []struct {
		name   string
		i      int64
		length int
		want   int
		vacant bool
	}{
		{"first", 0, 3, 0, false},
		{"last", 2, 3, 2, false},
		{"negative one is the last", -1, 3, 2, false},
		{"negative counts from the end", -3, 3, 0, false},
		{"past the end is vacant", 3, 3, 0, true},
		{"before the start is vacant", -4, 3, 0, true},
		{"empty list", 0, 0, 0, true},
		{"negative on an empty list", -1, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, vacant := patch.NormalizeIndex(tt.i, tt.length)
			if vacant != tt.vacant {
				t.Fatalf("vacant = %v, want %v", vacant, tt.vacant)
			}
			if !vacant && got != tt.want {
				t.Errorf("= %d, want %d", got, tt.want)
			}
		})
	}
}

// TestNormalizeRangeMatchesTheSchema walks the worked examples in path.proto's
// Range comment. They are the specification, so they are the test.
func TestNormalizeRangeMatchesTheSchema(t *testing.T) {
	const length = 5
	tests := []struct {
		name       string
		sel        patch.Selector
		begin, end int
	}{
		{"[_, _) is all", patch.SpanAll(), 0, 5},
		{"[-2, _) is the last two", patch.SpanFrom(-2), 3, 5},
		{"[_, -2) is all but the last two", patch.SpanTo(-2), 0, 3},
		{"[1, 4) is the middle three", patch.Span(1, 4), 1, 4},
		{"[3, 2) is empty", patch.Span(3, 2), 0, 0},
		{"[9, _) clamps to empty", patch.SpanFrom(9), 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			begin, end := patch.NormalizeRange(rangePB(t, tt.sel), length)
			if begin != tt.begin || end != tt.end {
				t.Errorf("= [%d, %d), want [%d, %d)", begin, end, tt.begin, tt.end)
			}
		})
	}
}

// TestNormalizeRangeReadsPresence is the regression the previous
// implementation could not survive: it branched on `end <= 0`, so an explicit
// [0, 0) selected the entire list. Presence is what separates "open" from
// "zero", and the schema keys on presence.
func TestNormalizeRangeReadsPresence(t *testing.T) {
	const length = 5

	begin, end := patch.NormalizeRange(rangePB(t, patch.Span(0, 0)), length)
	if begin != 0 || end != 0 {
		t.Errorf("explicit [0, 0) = [%d, %d), want empty", begin, end)
	}

	begin, end = patch.NormalizeRange(rangePB(t, patch.SpanFrom(0)), length)
	if begin != 0 || end != length {
		t.Errorf("[0, _) = [%d, %d), want the whole list", begin, end)
	}

	begin, end = patch.NormalizeRange(rangePB(t, patch.SpanTo(0)), length)
	if begin != 0 || end != 0 {
		t.Errorf("[_, 0) = [%d, %d), want empty", begin, end)
	}
}

func TestNormalizeRangeNeverWraps(t *testing.T) {
	// The previous schema's comment claimed [-2, 2) selected the last two AND
	// the first two. No single rule produces that, and the interval model does
	// not admit a union -- express that as two selectors instead.
	begin, end := patch.NormalizeRange(rangePB(t, patch.Span(-2, 2)), 5)
	if begin != 0 || end != 0 {
		t.Errorf("[-2, 2) on a list of 5 = [%d, %d), want empty (3 >= 2)", begin, end)
	}
}

func TestNormalizeRangeEmptyList(t *testing.T) {
	for _, sel := range []patch.Selector{
		patch.SpanAll(), patch.SpanFrom(0), patch.SpanFrom(-1), patch.Span(0, 1), patch.SpanTo(3),
	} {
		begin, end := patch.NormalizeRange(rangePB(t, sel), 0)
		if begin != 0 || end != 0 {
			t.Errorf("on an empty list = [%d, %d), want empty", begin, end)
		}
	}
}

func TestNormalizeRangeNilIsAll(t *testing.T) {
	// A Range with neither bound set is the "all elements" case, and a nil one
	// reads the same way through the generated getters.
	begin, end := patch.NormalizeRange(&patchpb.Range{}, 4)
	if begin != 0 || end != 4 {
		t.Errorf("= [%d, %d), want [0, 4)", begin, end)
	}
}

func rangePB(t *testing.T, s patch.Selector) *patchpb.Range {
	t.Helper()
	p := patch.MustNew(mt, patch.Target(s).Remove())
	r := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetRange()
	if r == nil {
		t.Fatal("selector is not a range")
	}
	return r
}
