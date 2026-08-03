package patch_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/internal/x"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// list builds a sample.Value whose r_i32_1 holds vs.
func list(vs ...int32) *sample.Value {
	return sample.Value_builder{RI32_1: vs}.Build()
}

// elems reads r_i32_1 back out, so that a mismatch prints as numbers rather
// than as a message dump.
func elems(v *sample.Value) []int32 { return v.GetRI32_1() }

func TestNormalizeMovesARemovePastTheIndexThatFollowsIt(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
	)

	got, err := patch.Normalize(p)
	x.NoError(t, err)

	want := patch.MustNewUntyped(
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
	)
	x.PbEq(t, want, got)
}

func TestNormalizeOrdersTwoRemovesFromTheFarEndInward(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Remove(),
	)

	got, err := patch.Normalize(p)
	x.NoError(t, err)

	want := patch.MustNewUntyped(
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
	)
	x.PbEq(t, want, got)
}

// TestNormalizeStacksTwoInsertsAtTheSamePosition covers the one place the
// far-end-inward ordering is not strict, which is the rule in this file most
// likely to look like an off-by-one.
//
// An insert names the GAP in front of an element rather than the element, so an
// insert at the position another insert names leaves that one naming the same
// gap, and the two stack in the order they are written. A remove at the same
// position would not: the gap in front of an element that is gone is the
// position one past the end whenever that element was the last one, and whether
// it was is a fact about the row.
func TestNormalizeStacksTwoInsertsAtTheSamePosition(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Insert(patch.Int32(88)),
		patch.Target(patch.Index(3)).In(patch.Name("r_i32_1")).Insert(patch.Int32(99)),
	)

	got, err := patch.Normalize(p)
	x.NoError(t, err)

	want := patch.MustNewUntyped(
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Insert(patch.Int32(99)),
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Insert(patch.Int32(88)),
	)
	x.PbEq(t, want, got)

	start := list(10, 20, 30, 40)
	vP, err := patchproto.Apply(start, p)
	x.NoError(t, err)
	vQ, err := patchproto.Apply(start, got)
	x.NoError(t, err)
	x.Eq(t, []int32{10, 20, 88, 99, 30, 40}, elems(vP))
	x.Eq(t, elems(vP), elems(vQ))
}

func TestNormalizeKeepsARemovalThatFailsFailing(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
	)
	q, err := patch.Normalize(p)
	x.NoError(t, err)

	_, errP := patchproto.Apply(list(10, 20, 30), p)
	_, errQ := patchproto.Apply(list(10, 20, 30), q)
	x.Eq(t, patch.CodeVacantTarget, patch.CodeOf(errP))
	x.Eq(t, patch.CodeVacantTarget, patch.CodeOf(errQ))
}

func TestNormalizeLeavesTheDocumentAloneWhenItCannotProveTheArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name string
		why  string
		p    *patchpb.Patch
	}{
		{
			name: "an append before an index reference",
			why:  "where the appended element lands depends on the length",
			p: patch.MustNewUntyped(
				patch.Target(patch.Append()).In(patch.Name("r_i32_1")).Insert(patch.Int32(7)),
				patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "a skipping remove before an index reference",
			why:  "whether the list shifted at all depends on the row",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Skip().Remove(),
				patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "a negative index",
			why:  "it counts from the end, so it needs the length",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Index(-1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "a range",
			why:  "both of its bounds normalize against the length",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.SpanFrom(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "a test",
			why:  "an entry observes what came before it",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Name("s_1")).Test(patch.Str("x")),
				patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "an index reference to an element the insert created",
			why:  "the earlier coordinates have no name for it",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Insert(patch.Int32(7)),
				patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			),
		},
		{
			name: "a write to the list as a whole",
			why:  "it replaces the list rather than a position in it",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Name("r_i32_1")).Assign(patch.List(patch.Int32(1))),
			),
		},
		{
			name: "a container-scoped entry on the list",
			why:  "it empties the list by an amount that depends on what is in it",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Container().In(patch.Name("r_i32_1")).Remove(),
			),
		},
		{
			name: "a move out of the list",
			why:  "it clears its source, which is a length change bound to a write elsewhere",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Name("i32_1")).Move(patch.From(patch.Index(1), patch.Name("r_i32_1"))),
			),
		},
		{
			name: "a field named two ways",
			why:  "a name against a number cannot be shown to be a different field",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Index(1)).In(patch.Num(1005)).Assign(patch.Int32(99)),
			),
		},
		{
			name: "every_entry over a map",
			why:  "it resolves against the keys the map holds, which are data",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.EveryEntry()).In(patch.Name("m_s_s")).Assign(patch.Str("v")),
			),
		},
		{
			name: "a oneof member",
			why:  "it resolves to whichever member is set, which is not in the document",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Oneof("o")).Remove(),
			),
		},
		{
			name: "a path that creates the container the list op needs",
			why:  "running it first makes the list exist, which changes what is refused",
			p: patch.MustNewUntyped(
				patch.Target(patch.Index(0)).In(patch.Name("m_1"), patch.Name("r_i32_1")).Remove(),
				patch.Target(patch.Name("s_2")).InOrCreate(patch.Name("m_1")).Assign(patch.Str("deep")),
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := proto.Clone(tc.p).(*patchpb.Patch)
			got, err := patch.Normalize(tc.p)
			x.NoError(t, err)
			if !proto.Equal(before, got) {
				t.Fatalf("%s: expected the document back unchanged, got\n%v", tc.why, got)
			}
		})
	}
}

func TestNormalizeDoesNotTouchItsInput(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
	)
	before := proto.Clone(p).(*patchpb.Patch)

	_, err := patch.Normalize(p)
	x.NoError(t, err)
	x.PbEq(t, before, p)
}

func TestNormalizeRefusesADocumentItCannotRead(t *testing.T) {
	_, err := patch.Normalize(nil)
	x.Error(t, err)
	x.Eq(t, patch.CodeMissingField, patch.CodeOf(err))

	// An entry with no operation: structurally invalid, and reordering a
	// document that does not apply would be reordering guesswork.
	p := patchpb.Patch_builder{
		Delta: patchpb.Delta_builder{Entries: []*patchpb.Entry{
			patchpb.Entry_builder{Container: &patchpb.Container{}}.Build(),
		}}.Build(),
	}.Build()
	_, err = patch.Normalize(p)
	x.Eq(t, patch.CodeMissingOneof, patch.CodeOf(err))
}

func TestNormalizeReordersInsideANestedDelta(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
			patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
		),
	)

	got, err := patch.Normalize(p)
	x.NoError(t, err)

	want := patch.MustNewUntyped(
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
			patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		),
	)
	x.PbEq(t, want, got)
}

func TestNormalizeCrossesAnEntryOnAnUnrelatedField(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.MapStr("k")).In(patch.Name("m_s_s")).Assign(patch.Str("v")),
		patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
	)

	got, err := patch.Normalize(p)
	x.NoError(t, err)

	want := patch.MustNewUntyped(
		patch.Target(patch.MapStr("k")).In(patch.Name("m_s_s")).Assign(patch.Str("v")),
		patch.Target(patch.Index(2)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
		patch.Target(patch.Index(0)).In(patch.Name("r_i32_1")).Remove(),
	)
	x.PbEq(t, want, got)
}

// ---------------------------------------------------------------- normal form

// inStartCoordinates reports whether every index a delta names into one list of
// the root message still names what it would name against that list as it stood
// before the delta began. That is the coordinate system a single-statement
// backend evaluates its existence guards in, so it is what the normal form has
// to mean.
//
// It is not the narrower "no index appears after any length change". Two removes
// cannot satisfy that reading at all — one of them must come second — and they
// do not need to: ordered from the far end inward, neither disturbs the index
// the other names. What matters is the coordinates, not the adjacency.
//
// The arithmetic is written out again here rather than borrowed from the
// implementation. A normal-form check sharing the arithmetic it checks would
// agree with a wrong answer.
func inStartCoordinates(p *patchpb.Patch, field string, startLen int) bool {
	// Which element sits at each index, named by the position it held before
	// the delta began; -1 for one an insert put there.
	origin := make([]int, startLen)
	for i := range origin {
		origin[i] = i
	}

	// names reports whether index i still means what it meant at the start. An
	// index past the end of the list as it now stands has to be past the end of
	// the list as it started too — a guard evaluated against the starting row
	// would otherwise find an element where the document means none. A negative
	// index means the same element only while the length is untouched.
	names := func(i int) bool {
		switch {
		case i < 0:
			return len(origin) == startLen
		case i >= len(origin):
			return i >= startLen
		default:
			return origin[i] == i
		}
	}

	for _, e := range p.GetDelta().GetEntries() {
		segs := e.GetPath().GetSegments()
		named := []int{}
		length := patchpb.Entry_Kind_not_set_case

		switch {
		case len(segs) == 1 && isField(segs[0], field):
			appends := 0
			for _, s := range e.GetTargets().GetSelectors() {
				switch s.WhichKind() {
				case patchpb.Selector_Append_case:
					appends++
				case patchpb.Selector_Key_case:
					if k := s.GetKey(); k.WhichKind() == patchpb.Key_Index_case {
						named = append(named, int(k.GetIndex()))
					}
				}
			}
			if len(named) > 0 {
				switch e.WhichKind() {
				case patchpb.Entry_Remove_case, patchpb.Entry_Insert_case:
					length = e.WhichKind()
				}
			}
			// An append shifts nothing below it, so every index that named an
			// element goes on naming it. The one it moves is the index one past
			// the end, which named nothing before and names the new element
			// after — and which index that is, is the length.
			if e.WhichKind() == patchpb.Entry_Insert_case {
				for range appends {
					origin = append(origin, -1)
				}
			}

		case len(segs) == 2 && isField(segs[0], field) && segs[1].WhichKind() == patchpb.Key_Index_case:
			// The index is in the path rather than in a selector; whatever the
			// entry then does happens inside that element.
			named = append(named, int(segs[1].GetIndex()))

		default:
			continue
		}

		for _, i := range named {
			if !names(i) {
				return false
			}
		}

		next := []int{}
		switch length {
		case patchpb.Entry_Remove_case:
			for i, o := range origin {
				if !slices.Contains(named, i) {
					next = append(next, o)
				}
			}
			origin = next
		case patchpb.Entry_Insert_case:
			for i, o := range origin {
				if slices.Contains(named, i) {
					next = append(next, -1)
				}
				next = append(next, o)
			}
			origin = next
		}
	}
	return true
}

func isField(k *patchpb.Key, name string) bool {
	return k.WhichKind() == patchpb.Key_Field_case && k.GetField().GetName() == name
}

// TestNormalizeReachesTheNormalFormWhereItCan runs on the one shape the form is
// always reachable for: single-target entries on a list that only shrinks.
//
// Nothing wider is claimed, and the two tests below are why. An insert can name
// an element it created itself, and two length changes can name positions that
// interleave; neither has an order that puts every index in start coordinates.
func TestNormalizeReachesTheNormalFormWhereItCan(t *testing.T) {
	const startLen = 6
	seed := rand.Uint64()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewPCG(seed, 0x5eed))

	reordered := 0
	for range 400 {
		p := genPatch(rng, shrinking)
		q, err := patch.Normalize(p)
		x.NoError(t, err)
		if !inStartCoordinates(q, "r_i32_1", startLen) {
			t.Fatalf("seed %d: not in start coordinates:\nfrom %v\nto   %v", seed, p, q)
		}
		if !proto.Equal(p, q) {
			reordered++
		}
	}
	if reordered == 0 {
		t.Fatalf("seed %d: nothing was reordered, so the check proved nothing", seed)
	}
	t.Logf("%d of 400 documents were reordered", reordered)
}

// TestNormalizeCannotOrderTwoRemovesWhosePositionsInterleave pins the limit the
// doc comment claims, because it is the one a reader is most likely to doubt.
//
// remove{0,4} then remove(1) — the element the list started with at 2. Put the
// single removal first and it is remove(2), but then {0,4} runs against a list
// that has lost an element below 4, so 4 no longer names what it named. Put it
// second and 1 is a coordinate the first removal produced. There is no third
// order, so the document is left as it was written.
func TestNormalizeCannotOrderTwoRemovesWhosePositionsInterleave(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(0), patch.Index(4)).In(patch.Name("r_i32_1")).Remove(),
		patch.Target(patch.Index(1)).In(patch.Name("r_i32_1")).Remove(),
	)
	q, err := patch.Normalize(p)
	x.NoError(t, err)
	x.PbEq(t, p, q)

	if inStartCoordinates(q, "r_i32_1", 6) {
		t.Fatal("the second removal names a coordinate the first one produced")
	}

	// Left alone it still means what it meant: 10 20 30 40 50 60 loses the
	// first and the fifth, and then the one that started third.
	v, err := patchproto.Apply(list(10, 20, 30, 40, 50, 60), q)
	x.NoError(t, err)
	x.Eq(t, []int32{20, 40, 60}, elems(v))
}

// TestNormalizeLeavesAnUnreachableFormShort is the other side of it: a document
// whose arithmetic cannot be proved comes back in the order it was written,
// rather than in an order that only looks normalized.
//
// Index 6 against a list of six is the position an append moves and no other:
// it named nothing before the append and names the new element after it. Which
// index that is, is the length, which is why the case cannot be decided from
// the document.
func TestNormalizeLeavesAnUnreachableFormShort(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Append()).In(patch.Name("r_i32_1")).Insert(patch.Int32(7)),
		patch.Target(patch.Index(6)).In(patch.Name("r_i32_1")).Assign(patch.Int32(99)),
	)
	q, err := patch.Normalize(p)
	x.NoError(t, err)
	x.PbEq(t, p, q)

	if inStartCoordinates(q, "r_i32_1", 6) {
		t.Fatal("a list of six is exactly the length that defeats this document")
	}
	if !inStartCoordinates(q, "r_i32_1", 3) {
		t.Fatal("a list of three is not, and the check is supposed to tell them apart")
	}
}

// ---------------------------------------------------------------- the property

// genMode selects which operations a generated document is allowed to carry.
type genMode int

const (
	// shrinking keeps every entry on r_i32_1 and never inserts, which is the
	// shape that can always be brought to the normal form: shifting a
	// reference back past a removal always has an answer, while shifting one
	// back past an insert may not.
	shrinking genMode = iota
	// listOnly adds inserts, so the arithmetic runs in both directions.
	listOnly
	// everything also writes maps, scalars, and a nested message, so that the
	// crossing rules are exercised as well as the arithmetic.
	everything
	// multiTarget lets one entry name two positions at once, which is a length
	// change of two and the shape that makes two changes interleave.
	multiTarget
	// elements works on a list of MESSAGES, reached through a path segment
	// rather than a target selector, and nests deltas inside the elements.
	elements
	// hazards mixes the constructs Normalize must leave alone — a test, a
	// skipping remove, a negative index, an append, a path that creates — in
	// among ordinary list operations, on two lists at different depths. A
	// document is normalized in part or not at all, and the part that moves
	// must not disturb the part that stays.
	hazards
)

// genPatch builds a document out of random list operations, deliberately
// including indices that will not resolve: a transform that preserved the
// successes and swallowed the failures would be worse than none.
func genPatch(rng *rand.Rand, mode genMode) *patchpb.Patch {
	n := 1 + rng.IntN(10)
	ops := make([]patch.Op, 0, n)
	for range n {
		ops = append(ops, genOp(rng, mode))
	}
	return patch.MustNewUntyped(ops[0], ops[1:]...)
}

// genOp builds one entry.
//
// Outside hazards, everything it builds off the list either applies or is
// skipped and never fails on its own account. An entry carrying a second way to
// fail would turn the case into a question of which failure is reported first,
// which is the one thing reordering does not preserve — see
// TestNormalizeMayReportADifferentFailureOfATwiceFailingDocument.
//
// hazards drops that rule deliberately, since a test that can fail is one of
// the constructs it is there to cover. soleDefect is what keeps the property
// honest under it.
func genOp(rng *rand.Rand, mode genMode) patch.Op {
	if mode == everything && rng.IntN(3) == 0 {
		key := patch.MapStr(string(rune('a' + rng.IntN(3))))
		switch rng.IntN(4) {
		case 0:
			return patch.Target(patch.Name("s_1")).Assign(patch.Str(fmt.Sprint(rng.IntN(4))))
		case 1:
			return patch.Target(key).In(patch.Name("m_s_s")).Assign(patch.Str("v"))
		case 2:
			return patch.Target(key).In(patch.Name("m_s_s")).Skip().Remove()
		default:
			return patch.Target(patch.Name("s_2")).InOrCreate(patch.Name("m_1")).Assign(patch.Str("deep"))
		}
	}

	// Indices reach one past the longest list the property test starts from,
	// so that a fifth or so of the entries address nothing.
	i := int64(rng.IntN(7))

	if mode == hazards {
		// Half the entries go to m_1.r_i32_1, which is a list under a field
		// that is not always set, so the entries that create it run against
		// both a present and an absent container.
		in := []patch.Keyer{patch.Name("r_i32_1")}
		if rng.IntN(2) == 0 {
			in = []patch.Keyer{patch.Name("m_1"), patch.Name("r_i32_1")}
		}
		// Barriers stop everything around them, so they are the minority: a
		// document that is all barrier proves only that nothing moved.
		switch rng.IntN(12) {
		case 0:
			return patch.Target(patch.Index(i)).In(in...).Skip().Remove()
		case 1:
			return patch.Target(patch.Index(-1 - i%3)).In(in...).Assign(patch.Int32(int32(700 + i)))
		case 2:
			return patch.Target(patch.Append()).In(in...).Insert(patch.Int32(int32(800 + i)))
		case 3:
			return patch.Target(patch.Index(i)).In(in...).Exists(i%2 == 0)
		case 4:
			return patch.Target(patch.Name("s_2")).InOrCreate(patch.Name("m_1")).Assign(patch.Str("deep"))
		case 5, 6:
			return patch.Target(patch.Index(i)).In(in...).Remove()
		case 7, 8:
			return patch.Target(patch.Index(i)).In(in...).Insert(patch.Int32(int32(100 + i)))
		default:
			return patch.Target(patch.Index(i)).In(in...).Assign(patch.Int32(int32(900 + i)))
		}
	}

	if mode == elements {
		switch rng.IntN(4) {
		case 0:
			return patch.Target(patch.Index(i)).In(patch.Name("r_m_1")).Remove()
		case 1:
			return patch.Target(patch.Index(i)).In(patch.Name("r_m_1")).
				Insert(patch.Msg(patch.F(patch.Name("s_1"), patch.Str("new"))))
		case 2:
			// The index is in the entry's PATH rather than in a selector.
			return patch.Target(patch.Name("s_1")).In(patch.Name("r_m_1"), patch.Index(i)).
				Assign(patch.Str(fmt.Sprintf("at %d", i)))
		default:
			return patch.Target(patch.Index(i)).In(patch.Name("r_m_1")).
				Nest(patch.Target(patch.Name("s_2")).Assign(patch.Str(fmt.Sprintf("in %d", i))))
		}
	}

	target := patch.Target(patch.Index(i)).In(patch.Name("r_i32_1"))
	if mode == multiTarget && rng.IntN(2) == 0 {
		if j := int64(rng.IntN(7)); j != i {
			target = patch.Target(patch.Index(i), patch.Index(j)).In(patch.Name("r_i32_1"))
		}
	}

	switch rng.IntN(4) {
	case 0:
		return target.Remove()
	case 1:
		if mode == shrinking {
			return target.Remove()
		}
		return target.Insert(patch.Int32(int32(100 + i)))
	default:
		return target.Assign(patch.Int32(int32(900 + i)))
	}
}

// soleDefect reports whether exactly one entry of p is defeated by m: p fails,
// and it stops failing once that entry is taken out.
//
// It is the condition under which the reason for a refusal is preserved.
// Reordering cannot promise which rule a document with TWO defeated entries is
// refused for — both orders refuse, and the one reached first is the one named.
// A document with one has to be refused for the same reason wherever that entry
// lands, and that is what the property test holds Normalize to.
func soleDefect(p *patchpb.Patch, m *sample.Value) bool {
	es := p.GetDelta().GetEntries()

	at := -1
	for i := range es {
		if _, err := patchproto.Apply(m, withEntries(p, es[:i+1])); err != nil {
			at = i
			break
		}
	}
	if at < 0 {
		return false
	}
	if len(es) == 1 {
		// Taking it out would leave an empty delta, which is not a document.
		return true
	}

	rest := append(append([]*patchpb.Entry{}, es[:at]...), es[at+1:]...)
	_, err := patchproto.Apply(m, withEntries(p, rest))
	return err == nil
}

func withEntries(p *patchpb.Patch, es []*patchpb.Entry) *patchpb.Patch {
	q := proto.Clone(p).(*patchpb.Patch)
	q.GetDelta().SetEntries(es)
	return q
}

func TestNormalizePreservesWhatTheDocumentMeans(t *testing.T) {
	for _, mode := range []struct {
		name string
		mode genMode
	}{
		{"over one list that only shrinks", shrinking},
		{"over one list that grows and shrinks", listOnly},
		{"over several fields at once", everything},
		{"over entries that name two positions at once", multiTarget},
		{"over a list of messages, indexed through the path", elements},
		{"over documents mixing what it can move with what it cannot", hazards},
	} {
		t.Run(mode.name, func(t *testing.T) {
			seed := rand.Uint64()
			t.Logf("seed %d", seed)
			rng := rand.New(rand.NewPCG(seed, 0xf00d))

			starts := []*sample.Value{
				list(),
				list(10),
				list(10, 20, 30),
				list(10, 20, 30, 40, 50, 60),
			}
			for i, s := range starts {
				s.SetS_1("before")
				s.SetMSS(map[string]string{"a": "one", "b": "two"})
				ms := make([]*sample.Value, len(s.GetRI32_1()))
				for i := range ms {
					ms[i] = sample.Value_builder{S_1: fmt.Sprintf("e%d", i)}.Build()
				}
				s.SetRM_1(ms)

				// m_1 is left unset on every other one, so that an entry
				// creating it, and an entry needing it, are each seen both ways.
				if i%2 == 1 {
					s.SetM_1(list(1, 2, 3, 4))
				}
			}

			agreed, reblamed, differed := 0, 0, 0
			for range 400 {
				p := genPatch(rng, mode.mode)
				q, err := patch.Normalize(p)
				x.NoError(t, err)
				if !proto.Equal(p, q) {
					differed++
				}

				for _, start := range starts {
					wantV, wantErr := patchproto.Apply(start, p)
					gotV, gotErr := patchproto.Apply(start, q)

					switch {
					case (wantErr == nil) != (gotErr == nil):
						t.Fatalf("seed %d on %v: one applies and the other does not:\n  %v\n    -> %v\n  %v\n    -> %v",
							seed, elems(start), p, wantErr, q, gotErr)

					case wantErr == nil:
						if !proto.Equal(wantV, gotV) {
							t.Fatalf("seed %d on %v: %v gives %v, %v gives %v",
								seed, elems(start), p, elems(wantV), q, elems(gotV))
						}
						agreed++

					case patch.CodeOf(wantErr) == patch.CodeOf(gotErr):
						agreed++

					case soleDefect(p, start):
						t.Fatalf("seed %d on %v: one entry is defective and the two disagree on which rule it breaks:\n  %v\n    -> %v\n  %v\n    -> %v",
							seed, elems(start), p, wantErr, q, gotErr)

					default:
						reblamed++
					}
				}
			}
			if differed == 0 {
				t.Fatalf("seed %d: nothing was reordered, so the check proved nothing", seed)
			}
			t.Logf("%d of 400 documents were reordered; %d applications agreed, %d were refused for another of their own defects",
				differed, agreed, reblamed)
		})
	}
}

// ---------------------------------------------------------------- diagnosis

// TestNormalizeMayReportADifferentFailureOfATwiceFailingDocument pins the one
// guarantee reordering does not make. Both documents fail and neither writes
// anything, but which rule is named depends on which entry is reached first.
func TestNormalizeMayReportADifferentFailureOfATwiceFailingDocument(t *testing.T) {
	p := patch.MustNewUntyped(
		// Nothing at index 9: CodeVacantTarget.
		patch.Target(patch.Index(9)).In(patch.Name("r_i32_1")).Remove(),
		// A string into an int32 field: CodeIllegalArm.
		patch.Target(patch.Name("i32_1")).Assign(patch.Str("no")),
	)
	q, err := patch.Normalize(p)
	x.NoError(t, err)

	_, errP := patchproto.Apply(list(10, 20, 30), p)
	_, errQ := patchproto.Apply(list(10, 20, 30), q)
	x.Eq(t, patch.CodeVacantTarget, patch.CodeOf(errP))
	x.Eq(t, patch.CodeIllegalArm, patch.CodeOf(errQ))

	if !strings.Contains(errQ.Error(), "i32_1") {
		t.Fatalf("expected the second entry to be the one blamed, got %v", errQ)
	}
}

// TestNormalizeMayReblameAnIndexAsAPathRatherThanATarget is the same limit in
// the shape that reads as a contradiction, so it is written down.
//
// Both entries fail for the very same reason — the list is one element long and
// neither index 3 nor index 5 is in it — and yet the two documents name
// different rules. An index in a `path` is refused as a path that reaches no
// container, an index in `targets` as a target that does not exist, and the
// format draws that line deliberately: a path never tolerates a miss, while a
// target's miss is what on_missing governs.
//
// Whichever entry is reached first decides which of the two is reported. That is
// the whole of the divergence: both documents refuse, and neither writes.
func TestNormalizeMayReblameAnIndexAsAPathRatherThanATarget(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Index(3)).In(patch.Name("r_m_1")).Remove(),
		patch.Target(patch.Name("s_1")).In(patch.Name("r_m_1"), patch.Index(5)).Assign(patch.Str("at 5")),
	)
	q, err := patch.Normalize(p)
	x.NoError(t, err)

	one := sample.Value_builder{RM_1: []*sample.Value{list(10)}}.Build()
	_, errP := patchproto.Apply(one, p)
	_, errQ := patchproto.Apply(one, q)
	x.Eq(t, patch.CodeVacantTarget, patch.CodeOf(errP))
	x.Eq(t, patch.CodePathNotReached, patch.CodeOf(errQ))

	// On a list long enough for both — seven, since the removal has to leave
	// index 5 behind it — they agree on the value, which is what makes this a
	// question of blame and not of meaning.
	seven := sample.Value_builder{RM_1: []*sample.Value{
		list(10), list(20), list(30), list(40), list(50), list(60), list(70),
	}}.Build()
	vP, err := patchproto.Apply(seven, p)
	x.NoError(t, err)
	vQ, err := patchproto.Apply(seven, q)
	x.NoError(t, err)
	x.PbEq(t, vP, vQ)
}
