package patch

import (
	"sort"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Normalize returns a document with the same effect as p, in a form a backend
// that applies the whole delta at once can honor.
//
// The format says entries apply in order and each observes the effects of
// those before it. A backend that compiles a whole document into one
// statement cannot honor that for list indices: the edits nest correctly
// inside the statement, but the existence guards it has to emit are evaluated
// against the row as it stood BEFORE the statement, so an index an earlier
// entry shifted is asked about at the wrong position. Normalize rewrites the
// document so that every index it names is an index into the list as it stood
// before the document began, which is the coordinate system those guards are
// evaluated in.
//
// It does that by moving the entries that CHANGE a list's length later, and
// rewriting the indices of the entries that jump over them. A remove at j
// shifts every later reference above j down by one, so an entry moved in front
// of it addresses one higher: `remove(0); assign(1)` becomes
// `assign(2); remove(0)`. Two length changes that end up next to each other are
// ordered from the far end of the list inward, so that neither disturbs the
// other's indices.
//
// It is TOTAL over well-formed documents. It never refuses one for being
// un-normalizable: it rewrites what it can prove and leaves the rest exactly
// where it was, because deciding whether the remainder is acceptable belongs to
// the backend and not here. The error is for a document that is not
// well-formed — Validate's rules, including the unknown-field scan, which must
// pass before anything is rewritten for the same reason Writes requires it: a
// document carrying an arm from a newer revision must not be reordered as
// though the part understood were the whole.
//
// p is not modified; the result is a fresh document.
//
// # What it guarantees
//
// Against any message, the returned document applies to the same value as p,
// and is refused whenever p is refused. A failure is never swallowed and a
// success is never invented.
//
// What it does NOT guarantee is WHICH rule a refusal names. Where a document
// has more than one entry the message defeats, both orders refuse and the one
// reached first is blamed; the barriers below keep that from happening for the
// ordinary single-defect document, but they do not make it impossible. An entry
// naming SEVERAL positions can be reported through a different one of its own
// targets after being moved, so the reason can shift even when only one entry
// is at fault.
//
// That is the deliberate limit of this transform. Preserving which rule fires
// would mean never moving anything past an entry that could fail, and almost
// every index reference can be out of range, so it would mean moving nothing.
// A caller who needs the exact reason should apply p, not Normalize(p).
//
// # What it leaves alone
//
// Each of these is a place where the arithmetic would have to guess, so the
// entry stays where the author put it. See docs/normalize.md for the full list
// with reasons.
//
//   - a negative index, an append, or a range: all three are resolved against
//     the list's LENGTH, which the document does not carry
//   - a length change carrying on_missing = SKIP: whether the shift happened
//     at all depends on the row
//   - an index reference to an element an insert created: it has no name in
//     the earlier coordinates
//   - container scope, move, copy, every_entry, oneof_member: each reads or
//     rewrites the container as a whole
//   - on_absent_path = CREATE: the entry may materialize a container a later
//     entry needs, so its position decides which of the two is refused
//   - two length changes whose positions interleave: no order of them leaves
//     the others' indices untouched
//
// nest is the one compound construct that IS crossed. It only descends, so its
// inner delta reaches nothing but the container the outer entry hands it — and
// an index naming that container is rewritten like any other, which keeps it
// naming the same element. Its own target is a site like any other, so a nest
// that materializes the list, or a container holding it, is refused by the same
// rule that refuses a write to it.
//
// # Reordering across a test
//
// Nothing is ever moved across a test, in either direction, whatever it
// addresses.
//
// The format says an entry observes the effects of those before it, so a test
// is the one place a document reads. Moving a write past a test on the same
// path plainly changes what the test sees — asserting a list is non-empty
// before rather than after the remove that empties it is a different
// assertion. A test on a provably unrelated path could be crossed without
// changing whether it holds, and is still not crossed: a document that fails
// must fail the same way, and a test is what a failing document is most likely
// to fail on, so moving one changes which failure gets reported.
func Normalize(p *patchpb.Patch) (*patchpb.Patch, error) {
	return NormalizeWith(p, DefaultLimits)
}

// NormalizeWith is [Normalize] against limits of the caller's choosing.
//
// A caller who raised a limit for their applier has to raise it here too, or
// the documents their own backend accepts would be refused on the way in.
func NormalizeWith(p *patchpb.Patch, lim Limits) (*patchpb.Patch, error) {
	if err := ValidateWith(p, lim); err != nil {
		return nil, err
	}

	out := proto.Clone(p).(*patchpb.Patch)
	normalizeDelta(out.GetDelta())
	return out, nil
}

// normalizeDelta reorders one delta's entries in place, and recurses into the
// deltas under nest.
//
// A nested delta is normalized on its own, against the state its container is
// in when the nest entry runs. Its entries are never interleaved with the
// outer ones, because the format gives a nested delta its own container and no
// way out of it.
func normalizeDelta(d *patchpb.Delta) {
	es := d.GetEntries()
	for _, e := range es {
		if e.WhichKind() == patchpb.Entry_Nest_case {
			normalizeDelta(e.GetNest().GetDelta())
		}
	}

	in := make([]*entryFacts, len(es))
	for i, e := range es {
		in[i] = examine(e)
	}

	// A bubble sort whose comparison is "may these two swap, and does swapping
	// help": every individual swap is meaning-preserving, so stopping early
	// leaves a document that is partly normalized rather than a wrong one.
	// n passes is the standard bound, and a pass that swaps nothing ends it.
	for pass := 0; pass < len(es); pass++ {
		moved := false
		for i := 0; i+1 < len(es); i++ {
			if trySwap(in[i], in[i+1]) {
				es[i], es[i+1] = es[i+1], es[i]
				in[i], in[i+1] = in[i+1], in[i]
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	d.SetEntries(es)
}

// trySwap exchanges a and b when a changes a list's length, b is in the way of
// that change moving later, and the exchange can be shown to preserve the
// document's meaning. It rewrites b's indices into the earlier coordinates as
// part of the swap, so a failed attempt must leave b untouched — which is why
// the rewrite is computed in full before any of it is applied.
func trySwap(a, b *entryFacts) bool {
	if a.barrier || b.barrier || a.change == nil {
		return false
	}

	la := a.change.list
	refs := b.refsInto(la)

	// Moving a past an entry that has nothing to do with its list gains
	// ground — a's list may be referenced further on — but moving it past
	// another length change gains nothing unless that change is one of the
	// references being put back into the earlier coordinates. Without that
	// rule two changes on unrelated lists would swap forever.
	if len(refs) == 0 && b.change != nil {
		return false
	}

	// b must touch a's list only through indices into it. Anything that
	// reaches the list itself, or a container holding it, is a rewrite of the
	// whole list rather than of a position in it.
	for _, s := range b.sites {
		if pathsDistinct(s, la) {
			continue
		}
		if len(s) > len(la) && pathsEqual(s[:len(la)], la) && s[len(la)].WhichKind() == patchpb.Key_Index_case {
			continue
		}
		return false
	}

	shifted := make([]int64, len(refs))
	for i, k := range refs {
		v, ok := a.change.op.shiftBack(k.GetIndex())
		if !ok {
			// The position is one a created, and the earlier coordinates have
			// no name for it.
			return false
		}
		shifted[i] = v
	}

	if b.change != nil {
		if !pathsEqual(b.change.list, la) {
			return false
		}
		if !a.change.op.leaves(b.change.op, shifted) {
			return false
		}
	}

	for i, k := range refs {
		k.SetIndex(shifted[i])
	}
	if b.change != nil {
		b.change.op.idx = sortedCopy(shifted)
	}
	return true
}

// ---------------------------------------------------------------- coordinates

// listOp is what one entry does to the length of one list: the positions it
// removes, or the positions it inserts before, in the coordinates that entry
// saw.
type listOp struct {
	insert bool
	idx    []int64 // ascending
}

// shiftBack maps an index in the coordinates AFTER o back into the ones before
// it.
//
// It reports false for a position o brought into existence. Such a position is
// not addressable before o runs — "the element I just inserted" has no index in
// the earlier list — and an entry naming one therefore cannot be moved in front
// of o.
func (o listOp) shiftBack(i int64) (int64, bool) {
	if !o.insert {
		// Each removal at or below the result pushes the original position one
		// higher. Ascending order is what makes a single pass enough.
		r := i
		for _, j := range o.idx {
			if r < j {
				break
			}
			r++
		}
		return r, true
	}

	// An insert puts the original element at position p at p + (the number of
	// insertions at or before p). That map is strictly increasing, so at most
	// one p answers to i, and trying each possible insertion count finds it or
	// proves i is one of the inserted positions.
	for c := 0; c <= len(o.idx); c++ {
		p := int64(i) - int64(c)
		n := 0
		for _, j := range o.idx {
			if j <= p {
				n++
			}
		}
		if n == c {
			return p, true
		}
	}
	return 0, false
}

// leaves reports whether o still names the same elements, at indices that are
// still addressable, once other has run in front of it at the positions js.
//
// It is what decides whether two length changes may be exchanged: after the
// exchange o runs second and its indices are NOT rewritten, so they have to
// still mean what they meant.
//
// Anything happening strictly above o is invisible to it, which is the whole
// rule. Two inserts are the one exception: an insert names the gap in front of
// an element rather than the element, so an insert at o's own position leaves
// o naming the same gap and the two stack in the order they are written.
//
// The exception does not extend to an insert whose position a REMOVE took: the
// gap in front of an element that is no longer there is the position one past
// the last element whenever the element was the last one, and that position has
// no index — Selector.append is what addresses it. Whether it was the last one
// is a fact about the row.
func (o listOp) leaves(other listOp, js []int64) bool {
	if len(js) == 0 {
		return true
	}
	lo := js[0]
	for _, j := range js {
		if j < lo {
			lo = j
		}
	}
	hi := o.idx[len(o.idx)-1]
	if o.insert && other.insert {
		return lo >= hi
	}
	return lo > hi
}

// ---------------------------------------------------------------- the entries

// entryFacts is what the reordering needs to know about one entry: whether it
// may be touched at all, which positions it reaches, and what it does to a
// list's length.
type entryFacts struct {
	// barrier marks an entry that is never moved and that nothing is moved
	// across. Everything this build cannot reason about lands here, so a
	// construct added to the schema without teaching this file about it stops
	// the reordering rather than being reordered wrongly.
	barrier bool

	// sites are the positions the entry reaches, each a path from the delta's
	// own container. A write at a position also changes the containers holding
	// it and everything under it, which is why the comparison against them is
	// prefix-wise.
	sites [][]*patchpb.Key

	// index are the Keys inside the entry that name a list index, paired with
	// the list they index. They point into the document, and rewriting one is
	// how an entry is moved into earlier coordinates.
	index []indexRef

	// change is the entry's effect on a list's length, nil when it has none.
	change *listChange
}

type indexRef struct {
	list []*patchpb.Key
	key  *patchpb.Key
}

type listChange struct {
	list []*patchpb.Key
	op   listOp
}

// refsInto returns the Keys of e that index the list at path.
func (f *entryFacts) refsInto(path []*patchpb.Key) []*patchpb.Key {
	var out []*patchpb.Key
	for _, r := range f.index {
		if pathsEqual(r.list, path) {
			out = append(out, r.key)
		}
	}
	return out
}

// examine reads one entry. Validate has already run, so every arm it looks at
// is one this build declares.
func examine(e *patchpb.Entry) *entryFacts {
	f := &entryFacts{}
	stop := func() *entryFacts { return &entryFacts{barrier: true} }

	switch e.WhichKind() {
	case patchpb.Entry_Remove_case, patchpb.Entry_Insert_case,
		patchpb.Entry_Assign_case, patchpb.Entry_Nest_case:
	default:
		// test is never crossed, by rule. move and copy read a source whose
		// value is whatever the entries before them left, and a move clears
		// that source, which for a list element is a length change bundled
		// with a write somewhere else.
		return stop()
	}
	if e.WhichScope() != patchpb.Entry_Targets_case {
		// Container scope empties, fills, or replaces a whole list, by an
		// amount that depends on what is in it.
		return stop()
	}
	if e.GetOnAbsentPath() == patchpb.OnAbsentPath_ON_ABSENT_PATH_CREATE {
		// The entry may MATERIALIZE the containers along its path, which
		// decides whether a later entry reaches one at all. Moving it changes
		// which of the two is refused when the container is absent: run first
		// it creates an empty list and the list operation is refused for the
		// position, run second it is the list operation that is refused for the
		// path. Both refuse — but naming a different rule for a document with
		// one thing wrong with it is a worse answer than not reordering.
		return stop()
	}

	path := e.GetPath().GetSegments()
	for i, seg := range path {
		if seg.WhichKind() != patchpb.Key_Index_case {
			continue
		}
		if seg.GetIndex() < 0 {
			// Counts from the end, so it names a different element under every
			// length.
			return stop()
		}
		f.index = append(f.index, indexRef{list: path[:i:i], key: seg})
	}

	var idxs []int64
	for _, s := range e.GetTargets().GetSelectors() {
		if s.WhichKind() != patchpb.Selector_Key_case {
			// range and append normalize against the length; every_entry and
			// oneof_member resolve against the container's contents.
			return stop()
		}
		k := s.GetKey()
		if k.WhichKind() == patchpb.Key_Index_case {
			if k.GetIndex() < 0 {
				return stop()
			}
			idxs = append(idxs, k.GetIndex())
			f.index = append(f.index, indexRef{list: path, key: k})
		}
		f.sites = append(f.sites, append(path[:len(path):len(path)], k))
	}

	// A remove or an insert against list positions is what changes a length.
	// Against a field or a map key it changes no index, and a nest only
	// descends, so it can never change the length of the list it reaches
	// through.
	grows := e.WhichKind() == patchpb.Entry_Insert_case
	if grows || e.WhichKind() == patchpb.Entry_Remove_case {
		if len(idxs) > 0 {
			if len(idxs) != len(e.GetTargets().GetSelectors()) {
				// Indices mixed with field or map keys: the container cannot be
				// both, so the document does not apply and is not this pass's
				// to interpret.
				return stop()
			}
			if e.GetOnMissing() == patchpb.OnMissing_ON_MISSING_SKIP {
				// Whether the list shifted at all then depends on the row, so
				// no coordinate after it is known.
				return stop()
			}
			f.change = &listChange{list: path, op: listOp{insert: grows, idx: sortedCopy(idxs)}}
		}
	}
	return f
}

// ---------------------------------------------------------------- addressing

// pathsEqual reports whether two paths are written identically, which is the
// only way to be sure they name the same container: the same field spelled by
// number in one entry and by name in another is one container under two names,
// and nothing here can tell which.
func pathsEqual(a, b []*patchpb.Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// pathsDistinct reports whether a and b provably name locations neither of
// which contains the other. It is one-sided: "not distinct" includes both
// "overlapping" and "cannot tell", which is the direction that refuses.
func pathsDistinct(a, b []*patchpb.Key) bool {
	n := min(len(a), len(b))
	for i := range n {
		if keysDistinct(a[i], b[i]) {
			return true
		}
	}
	return false
}

// keysDistinct reports whether a and b provably name different locations in
// one container.
//
// Two indices are never distinct here, however far apart they look. A length
// change between them moves one relative to the other, and that is the whole
// subject of this file; the index arithmetic is what settles them, not this.
func keysDistinct(a, b *patchpb.Key) bool {
	if a.WhichKind() != b.WhichKind() {
		// One of the two is wrong for whatever container this is, so the
		// document does not apply. That is not a distinctness claim.
		return false
	}
	switch a.WhichKind() {
	case patchpb.Key_Field_case:
		return fieldsDistinct(a.GetField(), b.GetField())
	case patchpb.Key_MapKey_case:
		return mapKeysDistinct(a.GetMapKey(), b.GetMapKey())
	}
	return false
}

// fieldsDistinct compares only the identifiers both Fields set. A name against
// a number is two spellings that may well be the same field, and Normalize has
// no descriptor to ask.
func fieldsDistinct(a, b *patchpb.Field) bool {
	if a.HasNumber() && b.HasNumber() && a.GetNumber() != b.GetNumber() {
		return true
	}
	if a.HasName() && b.HasName() && a.GetName() != b.GetName() {
		return true
	}
	if a.HasJsonName() && b.HasJsonName() && a.GetJsonName() != b.GetJsonName() {
		return true
	}
	return false
}

// mapKeysDistinct compares two map keys of the same arm. Arms are never
// coerced, so two different arms against one map is a document that does not
// apply rather than two different entries.
func mapKeysDistinct(a, b *patchpb.MapKey) bool {
	if a.WhichKind() != b.WhichKind() {
		return false
	}
	switch a.WhichKind() {
	case patchpb.MapKey_S_case:
		return a.GetS() != b.GetS()
	case patchpb.MapKey_I_case:
		return a.GetI() != b.GetI()
	case patchpb.MapKey_U_case:
		return a.GetU() != b.GetU()
	case patchpb.MapKey_B_case:
		return a.GetB() != b.GetB()
	}
	return false
}

func sortedCopy(v []int64) []int64 {
	out := make([]int64, len(v))
	copy(out, v)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
