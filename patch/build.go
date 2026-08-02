package patch

import (
	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Revision is the schema revision this build implements. A Patch whose
// min_reader_revision exceeds it must be refused.
const Revision uint32 = 0

// New assembles a Patch for messages of type messageType, which must be the
// fully-qualified name of the message the operations address. An applier
// refuses it against anything else.
//
// Use NewUntyped when the operations are meant to apply to more than one
// message type.
//
// At least one operation is required: a Patch that does nothing is expressible
// as a test that holds, which says so explicitly.
//
// The builders make a structurally invalid Patch impossible to express — an
// entry with no scope, empty targets, a test that tolerates a missing target,
// a move onto a container — so the error returned here is only for the few
// mistakes the type system cannot catch, such as an Append selector given to
// an operation that cannot grow a list.
func New(messageType string, first Op, rest ...Op) (*patchpb.Patch, error) {
	d, err := delta(first, rest...)
	if err != nil {
		return nil, err
	}
	if messageType == "" {
		return nil, Errf(CodeMessageTypeMismatch, "message_type",
			"empty; use NewUntyped to say that on purpose")
	}
	return patchpb.Patch_builder{
		MessageType: proto.String(messageType),
		Delta:       d,
	}.Build(), nil
}

// NewUntyped assembles a Patch that names no message type, so no applier
// checks one.
//
// This is for operations that address fields several message types share — a
// resource's name, its etag, its labels. Naming a type would force a copy of
// the document per resource, and rewriting the field before each apply would
// mean altering a stored document to make it applicable.
//
// The per-field integrity check is unaffected: a Field that pins a name
// against a number still refuses a message whose fields moved.
func NewUntyped(first Op, rest ...Op) (*patchpb.Patch, error) {
	d, err := delta(first, rest...)
	if err != nil {
		return nil, err
	}
	return patchpb.Patch_builder{Delta: d}.Build(), nil
}

// MustNewUntyped is NewUntyped, panicking on error.
func MustNewUntyped(first Op, rest ...Op) *patchpb.Patch {
	p, err := NewUntyped(first, rest...)
	if err != nil {
		panic(err)
	}
	return p
}

// MustNew is New, panicking on error. Intended for tests and for patches built
// from constants.
func MustNew(messageType string, first Op, rest ...Op) *patchpb.Patch {
	p, err := New(messageType, first, rest...)
	if err != nil {
		panic(err)
	}
	return p
}

func delta(first Op, rest ...Op) (*patchpb.Delta, error) {
	entries := make([]*patchpb.Entry, 0, len(rest)+1)
	for _, op := range append([]Op{first}, rest...) {
		if op.err != nil {
			return nil, op.err
		}
		entries = append(entries, op.pb)
	}
	return patchpb.Delta_builder{Entries: entries}.Build(), nil
}

// Op is one built operation — an Entry, ready to be handed to New or Nest.
type Op struct {
	pb  *patchpb.Entry
	err error
}

// draft accumulates an entry's path, scope, and tolerance before a terminal
// method fixes its operation.
type draft struct {
	path      []Keyer
	targets   []Selectorer
	container bool
	skip      bool
	hasAppend bool
	err       error
}

// Target begins an entry that applies to the listed locations. At least one
// selector is required, which is why the first is a separate parameter: an
// empty target list would otherwise be indistinguishable from addressing the
// container, and the schema gives that state the most destructive meaning
// available.
func Target(first Selectorer, rest ...Selectorer) *TargetScope {
	d := &draft{targets: append([]Selectorer{first}, rest...)}
	for _, s := range d.targets {
		if sel, ok := s.(Selector); ok && sel.appendArm {
			d.hasAppend = true
		}
	}
	return &TargetScope{d}
}

// Container begins an entry that applies to the container reached by the
// entry's path as a whole — the root message when there is no path.
//
// The returned builder offers no Move or Copy: relocating a value onto a
// container is not defined.
func Container() *ContainerScope {
	return &ContainerScope{&draft{container: true}}
}

func (d *draft) build(set func(*patchpb.Entry_builder)) Op {
	if d.err != nil {
		return Op{err: d.err}
	}

	b := patchpb.Entry_builder{}
	if len(d.path) > 0 {
		p, err := buildPath(d.path)
		if err != nil {
			return Op{err: err}
		}
		b.Path = p
	}
	if d.container {
		b.Container = &patchpb.Container{}
	} else {
		sels := make([]*patchpb.Selector, 0, len(d.targets))
		for _, s := range d.targets {
			pb, err := s.selector()
			if err != nil {
				return Op{err: err}
			}
			sels = append(sels, pb)
		}
		b.Targets = patchpb.Targets_builder{Selectors: sels}.Build()
	}
	if d.skip {
		om := patchpb.OnMissing_ON_MISSING_SKIP
		b.OnMissing = &om
	}
	set(&b)
	return Op{pb: b.Build()}
}

// rejectAppend records that the operation about to be built cannot grow a
// list, so an Append selector is illegal for it.
func (d *draft) rejectAppend(op string) {
	if d.hasAppend && d.err == nil {
		d.err = Errf(CodeIllegalSelector, "targets", "append is not legal with %s", op)
	}
}

// TargetScope builds an entry that applies to specific locations.
type TargetScope struct{ d *draft }

// In navigates into a nested container before the operation applies. Every
// container along the path must already exist; a Patch never creates them.
func (b *TargetScope) In(path ...Keyer) *TargetScope {
	b.d.path = path
	return b
}

// Skip tolerates a target that resolves to no location, instead of failing.
//
// The returned builder offers no Test or Exists: an assertion that can be
// skipped is an assertion that can pass without being evaluated.
func (b *TargetScope) Skip() *TolerantScope {
	b.d.skip = true
	return &TolerantScope{b.d}
}

// Remove deletes the targets.
func (b *TargetScope) Remove() Op { return removeOp(b.d) }

// Test asserts that every target equals value.
func (b *TargetScope) Test(value Value) Op { return testOp(b.d, value) }

// Exists asserts that every target is present, or absent when want is false.
func (b *TargetScope) Exists(want bool) Op { return existsOp(b.d, want) }

// Insert creates value at the targets without overwriting. A target that
// already holds a value is an error.
func (b *TargetScope) Insert(value Value) Op { return insertOp(b.d, value) }

// Assign sets the targets to value, overwriting whatever is there.
func (b *TargetScope) Assign(value Value) Op { return assignOp(b.d, value) }

// Move relocates the value at from to the targets, clearing the source.
func (b *TargetScope) Move(from Location) Op { return moveOp(b.d, from) }

// Copy writes the value at from to the targets, leaving the source in place.
func (b *TargetScope) Copy(from Location) Op { return copyOp(b.d, from) }

// Nest applies the operations to each target, which must be a container.
func (b *TargetScope) Nest(first Op, rest ...Op) Op { return nestOp(b.d, first, rest...) }

// TolerantScope builds an entry that skips targets which do not exist. It is
// TargetScope minus the assertions, which must never be skippable.
type TolerantScope struct{ d *draft }

// In navigates into a nested container before the operation applies.
func (b *TolerantScope) In(path ...Keyer) *TolerantScope {
	b.d.path = path
	return b
}

// Remove deletes the targets that exist.
func (b *TolerantScope) Remove() Op { return removeOp(b.d) }

// Insert creates value at the targets that exist and are empty.
func (b *TolerantScope) Insert(value Value) Op { return insertOp(b.d, value) }

// Assign sets the targets that exist to value.
func (b *TolerantScope) Assign(value Value) Op { return assignOp(b.d, value) }

// Move relocates the value at from to the targets that exist.
func (b *TolerantScope) Move(from Location) Op { return moveOp(b.d, from) }

// Copy writes the value at from to the targets that exist.
func (b *TolerantScope) Copy(from Location) Op { return copyOp(b.d, from) }

// Nest applies the operations to each target that exists.
func (b *TolerantScope) Nest(first Op, rest ...Op) Op { return nestOp(b.d, first, rest...) }

// ContainerScope builds an entry that applies to a whole container. It is
// TargetScope minus Move and Copy, which a container does not define, and
// minus Skip, since a container scope has no targets to be missing.
type ContainerScope struct{ d *draft }

// In navigates into a nested container, which then becomes the subject of the
// operation. With no path the subject is the root message.
func (b *ContainerScope) In(path ...Keyer) *ContainerScope {
	b.d.path = path
	return b
}

// Remove empties the container: every field cleared, every element or entry
// dropped.
func (b *ContainerScope) Remove() Op { return removeOp(b.d) }

// Test asserts that the container equals value.
func (b *ContainerScope) Test(value Value) Op { return testOp(b.d, value) }

// Exists asserts that the container is non-empty, or empty when want is false.
func (b *ContainerScope) Exists(want bool) Op { return existsOp(b.d, want) }

// Insert fills only what the container does not already have.
func (b *ContainerScope) Insert(value Value) Op { return insertOp(b.d, value) }

// Assign replaces the container wholesale.
func (b *ContainerScope) Assign(value Value) Op { return assignOp(b.d, value) }

// Nest applies the operations to the container.
func (b *ContainerScope) Nest(first Op, rest ...Op) Op { return nestOp(b.d, first, rest...) }

func removeOp(d *draft) Op {
	d.rejectAppend("remove")
	return d.build(func(b *patchpb.Entry_builder) { b.Remove = &patchpb.Remove{} })
}

func testOp(d *draft, value Value) Op {
	d.rejectAppend("test")
	pb, err := value.value()
	if err != nil {
		return Op{err: err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Test = patchpb.Test_builder{Value: pb}.Build()
	})
}

func existsOp(d *draft, want bool) Op {
	d.rejectAppend("test")
	return d.build(func(b *patchpb.Entry_builder) {
		b.Test = patchpb.Test_builder{Exists: proto.Bool(want)}.Build()
	})
}

func insertOp(d *draft, value Value) Op {
	pb, err := value.value()
	if err != nil {
		return Op{err: err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Insert = patchpb.Insert_builder{Value: pb}.Build()
	})
}

func assignOp(d *draft, value Value) Op {
	d.rejectAppend("assign")
	pb, err := value.value()
	if err != nil {
		return Op{err: err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Assign = patchpb.Assign_builder{Value: pb}.Build()
	})
}

func moveOp(d *draft, from Location) Op {
	if from.err != nil {
		return Op{err: from.err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Move = patchpb.Move_builder{From: from.pb}.Build()
	})
}

func copyOp(d *draft, from Location) Op {
	if from.err != nil {
		return Op{err: from.err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Copy = patchpb.Copy_builder{From: from.pb}.Build()
	})
}

func nestOp(d *draft, first Op, rest ...Op) Op {
	d.rejectAppend("nest")
	inner, err := delta(first, rest...)
	if err != nil {
		return Op{err: err}
	}
	return d.build(func(b *patchpb.Entry_builder) {
		b.Nest = patchpb.Nest_builder{Delta: inner}.Build()
	})
}
