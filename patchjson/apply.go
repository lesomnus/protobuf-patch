// Package patchjson applies a patch.Patch to a JSON document that has no
// schema.
//
// It is NOT a second reading of the same document as patchproto. The format
// distinguishes a message from a map, an int32 from an int64, and a declared
// field from an undeclared one; none of those distinctions exist in schema-less
// JSON, so a Patch cannot mean the same thing to both. Do not expect
// patchjson.Apply on protojson output to match patchproto.Apply on the message
// — where the two can differ, this package says so.
//
// What it does guarantee is the part that survives without a schema: the
// failure contract, atomicity, the addressing model, and refusal of every
// construct it cannot interpret. It never guesses.
//
// The rules it cannot honor, all of which are REFUSED rather than approximated:
//
//	Field.number             a schema-less document has no field numbers
//	MapKey.i / .u / .b       a JSON object has string keys
//	NaN and infinity         JSON has no representation for them (RFC 8259 §6)
//
// And two the format asks for that this package cannot check at all, noted here
// because silence would be worse:
//
//	Patch.message_type       a JSON document does not name its own type; pass
//	                         ExpectType to check it against a name you know
//	move/copy declared type  there are no declared types to compare
package patchjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Option configures Apply.
type Option func(*options)

type options struct {
	expect string
}

// ExpectType makes Apply check the Patch's message_type against name.
//
// Without it the field is not checked, because a JSON document does not carry
// a type to check it against. Supply one whenever you know it: it is the
// format's only guard against a document authored for something else.
func ExpectType(name string) Option {
	return func(o *options) { o.expect = name }
}

// Apply returns doc with p applied to it.
//
// Numbers are decoded as json.Number, so a number the Patch does not touch
// keeps its exact text — a Patch never disturbs data it did not name. Object
// member ORDER is not preserved: JSON objects are unordered (RFC 8259 §4), and
// Go marshals a map in sorted key order.
func Apply(doc []byte, p *patchpb.Patch, opts ...Option) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("patchjson: %w", err)
	}

	got, err := ApplyValue(v, p, opts...)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(got); err != nil {
		return nil, fmt.Errorf("patchjson: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ApplyValue returns v with p applied to it, where v is a decoded JSON value.
//
// It is ATOMIC: p is applied to a deep copy which is returned only on success,
// so a failure leaves v untouched.
func ApplyValue(v any, p *patchpb.Patch, opts ...Option) (any, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	if err := patch.Validate(p); err != nil {
		return nil, err
	}
	if o.expect != "" && p.GetMessageType() != o.expect {
		return nil, patch.Errf(patch.CodeMessageTypeMismatch, "message_type",
			"the document was authored against %s, not %s", p.GetMessageType(), o.expect)
	}

	root := clone(v)
	c, err := rootCont(&root)
	if err != nil {
		return nil, err
	}
	if err := applyDelta(&c, p.GetDelta(), patch.At("delta")); err != nil {
		return nil, err
	}
	return root, nil
}

func rootCont(holder *any) (cont, error) {
	switch x := (*holder).(type) {
	case map[string]any:
		return objCont(x), nil
	case []any:
		return arrCont(x, func(a []any) { *holder = a }), nil
	}
	return cont{}, patch.Errf(patch.CodeNotAContainer, "",
		"the document root is a scalar; a Patch addresses a container")
}

func applyDelta(c *cont, d *patchpb.Delta, at patch.At) error {
	for i, e := range d.GetEntries() {
		if err := applyEntry(c, e, at.Index("entries", i)); err != nil {
			return err
		}
	}
	return nil
}

func applyEntry(base *cont, e *patchpb.Entry, at patch.At) error {
	c, err := navigate(*base, e.GetPath(), at.Sub("path"))
	if err != nil {
		return err
	}

	if e.WhichScope() == patchpb.Entry_Container_case {
		return applyToContainer(&c, e, at)
	}

	locs, err := expand(c, e.GetTargets(), e, at.Sub("targets"))
	if err != nil {
		return err
	}
	if e.WhichKind() == patchpb.Entry_Test_case && len(locs) == 0 {
		return patch.Errf(patch.CodeTestVacuous, at,
			"the selectors named no location, so the assertion says nothing")
	}
	if len(locs) == 0 {
		return nil
	}
	return applyToTargets(base, &c, locs, e, at)
}

func applyToTargets(base, c *cont, locs []loc, e *patchpb.Entry, at patch.At) error {
	switch e.WhichKind() {
	case patchpb.Entry_Remove_case:
		return removeAt(c, locs)
	case patchpb.Entry_Test_case:
		return testAt(c, locs, e.GetTest(), at.Sub("test"))
	case patchpb.Entry_Insert_case:
		return insertAt(c, locs, e.GetInsert().GetValue(), at.Sub("insert").Sub("value"))
	case patchpb.Entry_Assign_case:
		return assignAt(c, locs, e.GetAssign().GetValue(), at.Sub("assign").Sub("value"))
	case patchpb.Entry_Move_case:
		return relocate(base, c, locs, e.GetMove().GetFrom(), true, at.Sub("move"))
	case patchpb.Entry_Copy_case:
		return relocate(base, c, locs, e.GetCopy().GetFrom(), false, at.Sub("copy"))
	case patchpb.Entry_Nest_case:
		return nestAt(c, locs, e.GetNest().GetDelta(), at.Sub("nest").Sub("delta"))
	}
	return nil
}

// ------------------------------------------------------------ remove

func removeAt(c *cont, locs []loc) error {
	if !c.isArr {
		for _, l := range locs {
			delete(c.obj, l.key)
		}
		return nil
	}
	// Descending, so each removal leaves the remaining pre-entry indices valid.
	idxs := make([]int, 0, len(locs))
	for _, l := range locs {
		if !l.appendArm && !l.noSlot {
			idxs = append(idxs, l.idx)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(idxs)))
	arr := c.arr
	for _, i := range idxs {
		arr = append(arr[:i], arr[i+1:]...)
	}
	c.setArr(arr)
	return nil
}

// ------------------------------------------------------------ test

func testAt(c *cont, locs []loc, t *patchpb.Test, at patch.At) error {
	for _, l := range locs {
		present, got := probe(c, l)
		if t.WhichWant() == patchpb.Test_Exists_case {
			if present != t.GetExists() {
				return patch.Errf(patch.CodeTestFailed, at, "at %s", describeLoc(c, l))
			}
			continue
		}
		if !present {
			return patch.Errf(patch.CodeTestFailed, at, "nothing at %s", describeLoc(c, l))
		}
		ok, err := equal(got, t.GetValue(), at.Sub("value"))
		if err != nil {
			return err
		}
		if !ok {
			return patch.Errf(patch.CodeTestFailed, at, "at %s", describeLoc(c, l))
		}
	}
	return nil
}

func probe(c *cont, l loc) (bool, any) {
	if c.isArr {
		if l.noSlot || l.appendArm {
			return false, nil
		}
		return true, c.arr[l.idx]
	}
	v, present := c.obj[l.key]
	return present, v
}

// ------------------------------------------------------------ assign

func assignAt(c *cont, locs []loc, v *patchpb.Value, at patch.At) error {
	for _, l := range locs {
		e, err := render(v, at)
		if err != nil {
			return err
		}
		if !c.isArr {
			c.obj[l.key] = e
			continue
		}
		if l.appendArm {
			return patch.Errf(patch.CodeIllegalSelector, at, "assign cannot grow an array")
		}
		c.arr[l.idx] = e
	}
	return nil
}

// ------------------------------------------------------------ insert

func insertAt(c *cont, locs []loc, v *patchpb.Value, at patch.At) error {
	if !c.isArr {
		for _, l := range locs {
			if _, present := c.obj[l.key]; present {
				return patch.Errf(patch.CodeOccupied, at, "%q is already there", l.key)
			}
			e, err := render(v, at)
			if err != nil {
				return err
			}
			c.obj[l.key] = e
		}
		return nil
	}
	return spliceInto(c, locs, v, at)
}

// spliceInto inserts before each target index and appends once per append
// selector, rebuilding the array in one pass.
//
// Every index was resolved against the pre-entry array, so inserting at [0, 2]
// of [a b c] puts a copy before the ORIGINAL elements 0 and 2 — [Z a b Z c] —
// rather than letting the first insertion shift the second.
func spliceInto(c *cont, locs []loc, v *patchpb.Value, at patch.At) error {
	before := map[int]int{}
	appends := 0
	for _, l := range locs {
		if l.appendArm {
			appends++
			continue
		}
		before[l.idx]++
	}

	out := make([]any, 0, len(c.arr)+len(locs))
	for i, u := range c.arr {
		for range before[i] {
			e, err := render(v, at)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		out = append(out, u)
	}
	for range appends {
		e, err := render(v, at)
		if err != nil {
			return err
		}
		out = append(out, e)
	}
	c.setArr(out)
	return nil
}

// ------------------------------------------------------------ move, copy

// relocate reads the source once, writes it to every target, and — for move —
// clears the source afterwards.
//
// The format also requires the source and targets to share a declared type.
// There are no declared types here, so that check does not happen: a move can
// put a string where a number was. A schema-less document has no way to object.
func relocate(base, c *cont, locs []loc, from *patchpb.Location, clear bool, at patch.At) error {
	src, srcLoc, err := resolveSource(base, c, from, at.Sub("from"))
	if err != nil {
		return err
	}
	present, got := probe(src, srcLoc)
	if !present {
		return patch.Errf(patch.CodeSourceUnresolved, at.Sub("from"),
			"nothing at %s", describeLoc(src, srcLoc))
	}

	sameAsSource := false
	for _, l := range locs {
		if sameLoc(c, src, l, srcLoc) {
			sameAsSource = true
			continue
		}
		if err := writeRaw(c, l, clone(got), at); err != nil {
			return err
		}
	}
	if clear && !sameAsSource {
		return removeAt(src, []loc{srcLoc})
	}
	return nil
}

func resolveSource(base, c *cont, from *patchpb.Location, at patch.At) (*cont, loc, error) {
	src := c
	if from.WhichOrigin() == patchpb.Location_Path_case {
		s, err := navigate(*base, from.GetPath(), at.Sub("path"))
		if err != nil {
			return nil, loc{}, err
		}
		src = &s
	}
	l, err := resolveKey(*src, from.GetKey(), at.Sub("key"))
	if err != nil {
		return nil, loc{}, err
	}
	if l.noSlot || l.emptySlot {
		return nil, loc{}, patch.Errf(patch.CodeSourceUnresolved, at.Sub("key"),
			"nothing at that position in the %s", src.describe())
	}
	return src, l, nil
}

func sameLoc(a, b *cont, x, y loc) bool {
	if a.isArr != b.isArr {
		return false
	}
	if a.isArr {
		return sameArray(a.arr, b.arr) && !x.appendArm && !y.appendArm && x.idx == y.idx
	}
	return sameObject(a.obj, b.obj) && x.key == y.key
}

func sameArray(a, b []any) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func sameObject(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	// Maps are reference types, so identity is what matters; comparing them
	// directly is not allowed, so probe through a sentinel.
	const probeKey = "\x00patchjson\x00"
	_, had := a[probeKey]
	a[probeKey] = struct{}{}
	_, same := b[probeKey]
	if !had {
		delete(a, probeKey)
	}
	return same
}

func writeRaw(c *cont, l loc, v any, at patch.At) error {
	if !c.isArr {
		c.obj[l.key] = v
		return nil
	}
	if l.appendArm {
		c.setArr(append(c.arr, v))
		return nil
	}
	c.arr[l.idx] = v
	return nil
}

// ------------------------------------------------------------ nest

func nestAt(c *cont, locs []loc, d *patchpb.Delta, at patch.At) error {
	for _, l := range locs {
		inner, err := containerAt(c, l, at)
		if err != nil {
			return err
		}
		if err := applyDelta(&inner, d, at); err != nil {
			return err
		}
	}
	return nil
}

func containerAt(c *cont, l loc, at patch.At) (cont, error) {
	var child any
	var sync func([]any)
	if c.isArr {
		child = c.arr[l.idx]
		idx, arr := l.idx, c.arr
		sync = func(a []any) { arr[idx] = a }
	} else {
		child = c.obj[l.key]
		key, obj := l.key, c.obj
		sync = func(a []any) { obj[key] = a }
	}

	switch v := child.(type) {
	case map[string]any:
		return objCont(v), nil
	case []any:
		return arrCont(v, sync), nil
	}
	return cont{}, patch.Errf(patch.CodeNotAContainer, at,
		"%s holds a scalar", describeLoc(c, l))
}

// ------------------------------------------------------------ helpers

func describeLoc(c *cont, l loc) string {
	if c.isArr {
		if l.appendArm {
			return "[-]"
		}
		return fmt.Sprintf("[%d]", l.idx)
	}
	return fmt.Sprintf("%q", l.key)
}
