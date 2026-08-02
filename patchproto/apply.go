// Package patchproto applies a patch.Patch to a proto.Message.
//
// The rules that do not need a message instance — validation, field
// resolution, range normalization, Value arm legality — live in package patch
// and are shared with any other applier, so that two implementations cannot
// drift into reading the same document differently.
package patchproto

import (
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Apply returns m with p applied to it.
//
// It is ATOMIC: p is applied to a private copy which is returned only on
// success, so a failure leaves m untouched. Full pre-validation would not be
// enough — an entry's legality can depend on state an earlier entry produced —
// so copy-and-swap is the only way to honor the schema's contract, and there
// is deliberately no in-place variant.
func Apply[T proto.Message](m T, p *patchpb.Patch, opts ...Option) (T, error) {
	var zero T

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	if err := patch.ValidateWith(p, o.limits); err != nil {
		return zero, err
	}
	if m.ProtoReflect().IsValid() == false && m.ProtoReflect().Descriptor() == nil {
		return zero, patch.Errf(patch.CodeMissingField, "", "nil target")
	}

	// The check happens only when the document makes the assertion. A Patch
	// with no message_type declares itself type-agnostic, which is how one
	// document addresses the fields several resource types share.
	if p.HasMessageType() {
		want := protoreflect.FullName(p.GetMessageType())
		if got := m.ProtoReflect().Descriptor().FullName(); got != want {
			return zero, patch.Errf(patch.CodeMessageTypeMismatch, "message_type",
				"the document was authored against %s, the target is %s", want, got)
		}
	}

	draft := proto.Clone(m)
	root := msgCont(draft.ProtoReflect())
	if err := applyDelta(root, p.GetDelta(), patch.At("delta")); err != nil {
		return zero, err
	}
	return draft.(T), nil
}

// Option configures Apply. None are defined yet; the parameter exists so that
// adding one is not a breaking change.
type Option func(*options)

type options struct{ limits patch.Limits }

// WithLimits bounds how deep into a document this call will recurse.
//
// The schema requires a bound and leaves the number to the implementation;
// patch.DefaultLimits is what applies when this option is absent. Raise it for
// a producer that legitimately sends deep literals, lower it to be stricter
// with documents from somewhere you do not control.
func WithLimits(l patch.Limits) Option {
	return func(o *options) { o.limits = l }
}

func applyDelta(base cont, d *patchpb.Delta, at patch.At) error {
	for i, e := range d.GetEntries() {
		if err := applyEntry(base, e, at.Index("entries", i)); err != nil {
			return err
		}
	}
	return nil
}

func applyEntry(base cont, e *patchpb.Entry, at patch.At) error {
	c, err := navigate(base, e.GetPath(), at.Sub("path"))
	if err != nil {
		return err
	}

	if e.WhichScope() == patchpb.Entry_Container_case {
		return applyToContainer(c, e, at)
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
	return applyToTargets(base, c, locs, e, at)
}

// applyToTargets runs the operation against each resolved position. Every
// index and key was resolved against the container as it stood before the
// entry began, so the outcome does not depend on selector order.
func applyToTargets(base, c cont, locs []loc, e *patchpb.Entry, at patch.At) error {
	switch e.WhichKind() {
	case patchpb.Entry_Remove_case:
		return removeAt(c, locs, at)
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

// ---------------------------------------------------------------- remove

func removeAt(c cont, locs []loc, at patch.At) error {
	switch {
	case c.isMsg():
		for _, l := range locs {
			c.msg.Clear(l.fd)
		}
	case c.isList():
		// Descending order so that each removal leaves the remaining
		// pre-entry indices valid.
		idxs := indicesOf(locs)
		sort.Sort(sort.Reverse(sort.IntSlice(idxs)))
		for _, i := range idxs {
			removeListElement(c.list, i)
		}
	default:
		for _, l := range locs {
			c.mp.Clear(l.key)
		}
	}
	return nil
}

func removeListElement(l protoreflect.List, i int) {
	for j := i; j < l.Len()-1; j++ {
		l.Set(j, l.Get(j+1))
	}
	l.Truncate(l.Len() - 1)
}

// ---------------------------------------------------------------- test

func testAt(c cont, locs []loc, t *patchpb.Test, at patch.At) error {
	for _, l := range locs {
		ok, err := holdsAt(c, l, t, at)
		if err != nil {
			return err
		}
		if !ok {
			return patch.Errf(patch.CodeTestFailed, at, "at %s", describeLoc(c, l))
		}
	}
	return nil
}

func holdsAt(c cont, l loc, t *patchpb.Test, at patch.At) (bool, error) {
	present, got, fd, site := probe(c, l)

	if t.WhichWant() == patchpb.Test_Exists_case {
		return present == t.GetExists(), nil
	}
	if !present {
		return false, nil
	}
	return equalValue(got, t.GetValue(), fd, site, allocFor(c, l), at.Sub("value"))
}

// probe reports whether the position holds anything and, if so, what.
func probe(c cont, l loc) (bool, protoreflect.Value, protoreflect.FieldDescriptor, patch.Site) {
	switch {
	case c.isMsg():
		if l.noSlot || l.fd == nil {
			return false, protoreflect.Value{}, l.fd, patch.SiteField
		}
		return c.msg.Has(l.fd), c.msg.Get(l.fd), l.fd, patch.SiteField
	case c.isList():
		if l.noSlot || l.appendArm {
			return false, protoreflect.Value{}, c.fd, patch.SiteElement
		}
		return true, c.list.Get(l.idx), c.fd, patch.SiteElement
	default:
		if l.noSlot || !c.mp.Has(l.key) {
			return false, protoreflect.Value{}, c.fd, patch.SiteMapValue
		}
		return true, c.mp.Get(l.key), c.fd, patch.SiteMapValue
	}
}

// ---------------------------------------------------------------- assign

func assignAt(c cont, locs []loc, v *patchpb.Value, at patch.At) error {
	switch {
	case c.isMsg():
		for _, l := range locs {
			if err := setField(c.msg, l.fd, v, at); err != nil {
				return err
			}
		}
		return nil
	case c.isList():
		for _, l := range locs {
			if l.appendArm {
				return patch.Errf(patch.CodeIllegalSelector, at, "assign cannot grow a list")
			}
			pv, err := singular(v, c.fd, patch.SiteElement, c.list.NewElement, at)
			if err != nil {
				return err
			}
			c.list.Set(l.idx, pv)
		}
		return nil
	default:
		for _, l := range locs {
			pv, err := singular(v, c.fd, patch.SiteMapValue, c.mp.NewValue, at)
			if err != nil {
				return err
			}
			c.mp.Set(l.key, pv)
		}
		return nil
	}
}

// ---------------------------------------------------------------- insert

// insertAt creates without overwriting. An occupied position is an error,
// which is exactly what separates insert from assign.
func insertAt(c cont, locs []loc, v *patchpb.Value, at patch.At) error {
	switch {
	case c.isMsg():
		for _, l := range locs {
			if occupied(c.msg, l.fd) {
				return patch.Errf(patch.CodeOccupied, at, "%s is already set", l.fd.FullName())
			}
			if err := setField(c.msg, l.fd, v, at); err != nil {
				return err
			}
		}
		return nil

	case c.isList():
		return spliceInto(c, locs, v, at)

	default:
		for _, l := range locs {
			if c.mp.Has(l.key) {
				return patch.Errf(patch.CodeOccupied, at, "%s already has that key", c.fd.FullName())
			}
			pv, err := singular(v, c.fd, patch.SiteMapValue, c.mp.NewValue, at)
			if err != nil {
				return err
			}
			c.mp.Set(l.key, pv)
		}
		return nil
	}
}

// occupied reports whether a field already holds something, counting a sibling
// member of the same oneof: setting one clears the other, so inserting into an
// "absent" member would overwrite after all.
func occupied(m protoreflect.Message, fd protoreflect.FieldDescriptor) bool {
	if m.Has(fd) {
		return true
	}
	if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
		return m.WhichOneof(od) != nil
	}
	return false
}

// spliceInto inserts v before each target index and appends once per append
// selector, rebuilding the list in one pass.
//
// Every index was resolved against the pre-entry list, so inserting at [0, 2]
// of [a b c] puts a copy before the original elements 0 and 2 — [Z a b Z c] —
// rather than letting the first insertion shift the second.
func spliceInto(c cont, locs []loc, v *patchpb.Value, at patch.At) error {
	before := map[int]int{}
	appends := 0
	for _, l := range locs {
		if l.appendArm {
			appends++
			continue
		}
		before[l.idx]++
	}

	l := c.list
	old := make([]protoreflect.Value, l.Len())
	for i := range old {
		old[i] = l.Get(i)
	}

	newValue := func() (protoreflect.Value, error) {
		return singular(v, c.fd, patch.SiteElement, l.NewElement, at)
	}

	l.Truncate(0)
	for i, u := range old {
		for range before[i] {
			pv, err := newValue()
			if err != nil {
				return err
			}
			l.Append(pv)
		}
		l.Append(u)
	}
	for range appends {
		pv, err := newValue()
		if err != nil {
			return err
		}
		l.Append(pv)
	}
	return nil
}

// ---------------------------------------------------------------- move, copy

// relocate reads the source once, writes it to every target, and — for move —
// clears the source afterwards.
//
// Reading before any write is what makes the result independent of target
// order. A target that resolves to the source is a no-op for that target and
// leaves the source alone, per RFC 6902 §4.4.
func relocate(base, c cont, locs []loc, from *patchpb.Location, clear bool, at patch.At) error {
	src, srcLoc, err := resolveSource(base, c, from, at.Sub("from"))
	if err != nil {
		return err
	}

	present, got, srcFD, srcSite := probe(src, srcLoc)
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
		dstFD, dstSite := siteOf(c, l)
		if err := checkSameType(srcFD, srcSite, dstFD, dstSite, at); err != nil {
			return err
		}
		if err := writeRaw(c, l, got, at); err != nil {
			return err
		}
	}

	if clear && !sameAsSource {
		return removeAt(src, []loc{srcLoc}, at)
	}
	return nil
}

func resolveSource(base, c cont, from *patchpb.Location, at patch.At) (cont, loc, error) {
	src := c
	if from.WhichOrigin() == patchpb.Location_Path_case {
		var err error
		src, err = navigate(base, from.GetPath(), at.Sub("path"))
		if err != nil {
			return cont{}, loc{}, err
		}
	}
	l, err := resolveKey(src, from.GetKey(), at.Sub("key"))
	if err != nil {
		return cont{}, loc{}, err
	}
	if l.noSlot || l.emptySlot {
		return cont{}, loc{}, patch.Errf(patch.CodeSourceUnresolved, at.Sub("key"),
			"nothing at that position in %s", src.describe())
	}
	return src, l, nil
}

func sameLoc(a, b cont, x, y loc) bool {
	if a.msg != nil && b.msg != nil {
		return a.msg == b.msg && x.fd == y.fd
	}
	if a.list != nil && b.list != nil {
		return a.fd == b.fd && !x.appendArm && !y.appendArm && x.idx == y.idx
	}
	if a.mp != nil && b.mp != nil {
		return a.fd == b.fd && x.key.IsValid() && y.key.IsValid() &&
			x.key.Interface() == y.key.Interface()
	}
	return false
}

func siteOf(c cont, l loc) (protoreflect.FieldDescriptor, patch.Site) {
	switch {
	case c.isMsg():
		return l.fd, patch.SiteField
	case c.isList():
		return c.fd, patch.SiteElement
	default:
		return c.fd, patch.SiteMapValue
	}
}

// checkSameType enforces that a relocation preserves both kind and declared
// type. Matching kinds are not enough: two enums are both kind `e`, and moving
// a number between unrelated enums would silently change what it means.
func checkSameType(srcFD protoreflect.FieldDescriptor, srcSite patch.Site, dstFD protoreflect.FieldDescriptor, dstSite patch.Site, at patch.At) error {
	sk, sm, se := typeOf(srcFD, srcSite)
	dk, dm, de := typeOf(dstFD, dstSite)

	if sk != dk {
		return patch.Errf(patch.CodeTypeMismatch, at,
			"source is %v, target is %v; there is no conversion", sk, dk)
	}
	if sm != dm {
		return patch.Errf(patch.CodeTypeMismatch, at, "source is %s, target is %s", sm, dm)
	}
	if se != de {
		return patch.Errf(patch.CodeTypeMismatch, at,
			"source enum is %s, target enum is %s; an enum number does not carry across types", se, de)
	}
	return nil
}

func typeOf(fd protoreflect.FieldDescriptor, site patch.Site) (protoreflect.Kind, protoreflect.FullName, protoreflect.FullName) {
	d := fd
	kind := fd.Kind()
	if site == patch.SiteMapValue {
		d = fd.MapValue()
		kind = d.Kind()
	}
	var msg, enum protoreflect.FullName
	if md := d.Message(); md != nil {
		msg = md.FullName()
	}
	if ed := d.Enum(); ed != nil {
		enum = ed.FullName()
	}
	return kind, msg, enum
}

// writeRaw puts an already-resolved protoreflect value at a position.
func writeRaw(c cont, l loc, v protoreflect.Value, at patch.At) error {
	switch {
	case c.isMsg():
		c.msg.Set(l.fd, cloneValue(v, l.fd, patch.SiteField))
		return nil
	case c.isList():
		e := cloneValue(v, c.fd, patch.SiteElement)
		if l.appendArm {
			c.list.Append(e)
			return nil
		}
		c.list.Set(l.idx, e)
		return nil
	default:
		c.mp.Set(l.key, cloneValue(v, c.fd, patch.SiteMapValue))
		return nil
	}
}

// cloneValue copies a message value so that a move or copy does not alias the
// source, which would make clearing the source empty the destination too.
func cloneValue(v protoreflect.Value, fd protoreflect.FieldDescriptor, site patch.Site) protoreflect.Value {
	kind := fd.Kind()
	if site == patch.SiteMapValue {
		kind = fd.MapValue().Kind()
	}
	if kind != protoreflect.MessageKind && kind != protoreflect.GroupKind {
		return v
	}
	return protoreflect.ValueOfMessage(proto.Clone(v.Message().Interface()).ProtoReflect())
}

// ---------------------------------------------------------------- nest

func nestAt(c cont, locs []loc, d *patchpb.Delta, at patch.At) error {
	for _, l := range locs {
		inner, err := containerAt(c, l, at)
		if err != nil {
			return err
		}
		if err := applyDelta(inner, d, at); err != nil {
			return err
		}
	}
	return nil
}

func containerAt(c cont, l loc, at patch.At) (cont, error) {
	switch {
	case c.isMsg():
		fd := l.fd
		switch {
		case fd.IsMap():
			return mapCont(c.msg.Mutable(fd).Map(), fd), nil
		case fd.IsList():
			return listCont(c.msg.Mutable(fd).List(), fd), nil
		case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
			return msgCont(c.msg.Mutable(fd).Message()), nil
		}
		return cont{}, patch.Errf(patch.CodeNotAContainer, at,
			"%s is a %v", fd.FullName(), fd.Kind())

	case c.isList():
		if c.fd.Kind() != protoreflect.MessageKind && c.fd.Kind() != protoreflect.GroupKind {
			return cont{}, patch.Errf(patch.CodeNotAContainer, at,
				"elements of %s are %v", c.fd.FullName(), c.fd.Kind())
		}
		return msgCont(c.list.Get(l.idx).Message()), nil

	default:
		if c.fd.MapValue().Kind() != protoreflect.MessageKind && c.fd.MapValue().Kind() != protoreflect.GroupKind {
			return cont{}, patch.Errf(patch.CodeNotAContainer, at,
				"values of %s are %v", c.fd.FullName(), c.fd.MapValue().Kind())
		}
		return msgCont(c.mp.Get(l.key).Message()), nil
	}
}

// ---------------------------------------------------------------- helpers

func indicesOf(locs []loc) []int {
	out := make([]int, 0, len(locs))
	for _, l := range locs {
		if !l.appendArm {
			out = append(out, l.idx)
		}
	}
	return out
}

func allocFor(c cont, l loc) func() protoreflect.Value {
	switch {
	case c.isMsg():
		return func() protoreflect.Value { return c.msg.NewField(l.fd) }
	case c.isList():
		return c.list.NewElement
	default:
		return c.mp.NewValue
	}
}

func describeLoc(c cont, l loc) string {
	switch {
	case c.isMsg():
		if l.fd == nil {
			return c.describe()
		}
		return string(l.fd.FullName())
	case c.isList():
		if l.appendArm {
			return c.describe() + "[-]"
		}
		return c.describe() + "[" + itoa(l.idx) + "]"
	default:
		return c.describe() + "[" + l.key.String() + "]"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
