package patch

import (
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Limits bound the recursion a reader will follow into a document.
//
// The schema requires every implementation to have such a bound and to fail
// closed past it, and deliberately fixes no number: the right one depends on
// the reader's stack and on what its producers legitimately send. These are
// this implementation's numbers.
//
// The bound is not a nicety. A Patch recurses in two independent places, and
// both are cheap to write and expensive to read: at 2000 levels of nest a
// 47 KB document made validation allocate 862 MB, four times more for every
// doubling of depth. Nothing in the format's own rules would have refused it.
type Limits struct {
	// NestDepth is how many Deltas may be chained through `nest`. The
	// outermost Delta is depth 0.
	//
	// `nest` shares a path prefix among several entries, and anything it
	// expresses can be flattened into per-entry paths, so a document has no
	// reason to go deep. Ten is already far past what a hand-written patch
	// uses.
	NestDepth int

	// ValueDepth is how far a single Value literal may nest through `m`, `l`,
	// and `map`. A scalar is depth 1.
	//
	// This one has to be more generous than NestDepth, because a literal
	// mirrors the shape of the message it is assigned to rather than the
	// author's convenience. A google.protobuf.Struct in particular spends
	// roughly three levels per level of the JSON it carries, so a tight bound
	// here would refuse ordinary documents.
	ValueDepth int
}

// DefaultLimits is what Validate and every engine's Apply use when the caller
// does not say otherwise.
var DefaultLimits = Limits{
	NestDepth:  10,
	ValueDepth: 32,
}

func (l Limits) orDefault() Limits {
	if l.NestDepth <= 0 {
		l.NestDepth = DefaultLimits.NestDepth
	}
	if l.ValueDepth <= 0 {
		l.ValueDepth = DefaultLimits.ValueDepth
	}
	return l
}

// checkDepth refuses a document that nests deeper than lim allows.
//
// It runs before every other check, including the unknown-field scan. That is
// a deliberate exception to "unknown fields first": a depth check interprets
// nothing, it only counts recognized structure, so it cannot be misled by a
// field this reader does not understand — and every other pass walks the whole
// tree, so each of them is unsafe until this one has passed.
//
// It bails at the first violation rather than measuring the true depth, so the
// work it does is bounded by the limit and not by the document.
func checkDepth(p *patchpb.Patch, lim Limits) error {
	return checkDeltaDepth(p.GetDelta(), 0, lim, At("delta"))
}

func checkDeltaDepth(d *patchpb.Delta, depth int, lim Limits, at At) error {
	for i, e := range d.GetEntries() {
		eat := at.Index("entries", i)
		switch e.WhichKind() {
		case patchpb.Entry_Test_case:
			if e.GetTest().WhichWant() == patchpb.Test_Value_case {
				if err := checkValueDepth(e.GetTest().GetValue(), 1, lim, eat.Sub("test").Sub("value")); err != nil {
					return err
				}
			}

		case patchpb.Entry_Insert_case:
			if err := checkValueDepth(e.GetInsert().GetValue(), 1, lim, eat.Sub("insert").Sub("value")); err != nil {
				return err
			}

		case patchpb.Entry_Assign_case:
			if err := checkValueDepth(e.GetAssign().GetValue(), 1, lim, eat.Sub("assign").Sub("value")); err != nil {
				return err
			}

		case patchpb.Entry_Nest_case:
			nat := eat.Sub("nest").Sub("delta")
			if depth+1 > lim.NestDepth {
				return Errf(CodeTooDeep, nat,
					"nested deltas go deeper than %d, which is as far as this reader follows", lim.NestDepth)
			}
			if err := checkDeltaDepth(e.GetNest().GetDelta(), depth+1, lim, nat); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkValueDepth(v *patchpb.Value, depth int, lim Limits, at At) error {
	if depth > lim.ValueDepth {
		return Errf(CodeTooDeep, at,
			"a value nests deeper than %d, which is as far as this reader follows", lim.ValueDepth)
	}
	switch v.WhichKind() {
	case patchpb.Value_M_case:
		for i, fv := range v.GetM().GetFields() {
			if err := checkValueDepth(fv.GetValue(), depth+1, lim, at.Sub("m").Index("fields", i).Sub("value")); err != nil {
				return err
			}
		}
	case patchpb.Value_L_case:
		for i, ev := range v.GetL().GetValues() {
			if err := checkValueDepth(ev, depth+1, lim, at.Sub("l").Index("values", i)); err != nil {
				return err
			}
		}
	case patchpb.Value_Map_case:
		for i, me := range v.GetMap().GetEntries() {
			if err := checkValueDepth(me.GetValue(), depth+1, lim, at.Sub("map").Index("entries", i).Sub("value")); err != nil {
				return err
			}
		}
	}
	return nil
}
