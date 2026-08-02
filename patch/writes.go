package patch

import (
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// FieldPath is a chain of fields from a root message down to a position, with
// list indices and map keys elided.
//
// The elision is deliberate. A policy about a field — "this one is read-only",
// "this one is server-owned" — is stated about the FIELD, not about element 3
// of it, and a patch that rewrites one element of a read-only list has changed
// the read-only field just as surely as one that replaces the whole list. So
// r_m_1[0].s_1 and r_m_1[7].s_1 are both FieldPath{r_m_1, s_1}: the same
// answer to the only question this type is meant to answer.
//
// The cost is that a policy cannot be stated about one key of a map. That is a
// real limit, and the right shape for it is a separate mechanism rather than a
// finer FieldPath, because the analysis that produces one cannot know a key
// that a runtime Selector.range or every_entry would have picked.
type FieldPath []protoreflect.FieldDescriptor

// String writes the path in dotted form — "m_1.s_1". The empty path, which
// names the root message itself, is the empty string.
func (p FieldPath) String() string {
	var b strings.Builder
	for i, fd := range p {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(string(fd.Name()))
	}
	return b.String()
}

// Equal reports whether p and q name the same field.
func (p FieldPath) Equal(q FieldPath) bool {
	if len(p) != len(q) {
		return false
	}
	return p.isPrefixOf(q)
}

// HasPrefix reports whether q names p or an ancestor of it.
func (p FieldPath) HasPrefix(q FieldPath) bool {
	return len(q) <= len(p) && q.isPrefixOf(p)
}

// Overlaps reports whether a change at p can be a change at q — that is,
// whether either path is a prefix of the other.
//
// Both directions matter and they are different statements:
//
//   - q is a prefix of p: writing spec.created_at changes spec, because a
//     message IS its fields. A policy that freezes spec is not honored by a
//     patch that only reaches inside it.
//   - p is a prefix of q: assigning spec replaces spec.created_at along with
//     everything else it holds, whether or not the document mentions it.
//
// Anything else is disjoint: spec.name and spec.created_at cannot affect each
// other.
func (p FieldPath) Overlaps(q FieldPath) bool {
	if len(p) > len(q) {
		p, q = q, p
	}
	return p.isPrefixOf(q)
}

// isPrefixOf compares by full name rather than by descriptor identity: the
// same field reached through two descriptor sets — a compiled-in one and one
// built by protodesc from a FileDescriptorSet — is not the same pointer.
func (p FieldPath) isPrefixOf(q FieldPath) bool {
	for i, fd := range p {
		if fd.FullName() != q[i].FullName() {
			return false
		}
	}
	return true
}

func (p FieldPath) with(fd protoreflect.FieldDescriptor) FieldPath {
	// Always a fresh backing array: two sibling targets under one path would
	// otherwise append into the same spare capacity and overwrite each other.
	out := make(FieldPath, len(p)+1)
	copy(out, p)
	out[len(p)] = fd
	return out
}

// ParseFieldPath resolves a dotted field path against md — "m_1.s_1", or the
// empty string for the root message itself.
//
// A segment that names a repeated or map field descends into its element or
// value type, matching the elision FieldPath performs: "r_m_1.s_1" is the
// field s_1 of every element of r_m_1.
func ParseFieldPath(md protoreflect.MessageDescriptor, s string) (FieldPath, error) {
	if s == "" {
		return nil, nil
	}

	names := strings.Split(s, ".")
	out := make(FieldPath, 0, len(names))
	for i, name := range names {
		if md == nil {
			return nil, Errf(CodeNotAContainer, "",
				"%s is not a message, so %q cannot continue past it",
				FieldPath(out).String(), s)
		}
		fd := md.Fields().ByName(protoreflect.Name(name))
		if fd == nil {
			return nil, Errf(CodeVacantTarget, "",
				"%s declares no field named %q", md.FullName(), name)
		}
		out = append(out, fd)

		if i == len(names)-1 {
			break
		}
		md = messageUnder(fd)
	}
	return out, nil
}

// singularMessage reports whether fd is a message field with presence — the
// one shape that comes into existence merely by being descended into.
func singularMessage(fd protoreflect.FieldDescriptor) bool {
	if fd.IsMap() || fd.IsList() {
		return false
	}
	return fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind
}

// messageUnder returns the message a field's positions hold, or nil if they
// hold scalars.
func messageUnder(fd protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if fd.IsMap() {
		fd = fd.MapValue()
	}
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return fd.Message()
	}
	return nil
}

// Write is one modification a Patch may make, and the position in the document
// that makes it.
type Write struct {
	// Path is the field the write lands on, from the root of the message the
	// Patch was analyzed against.
	Path FieldPath
	// At is where in the Patch document the write comes from, so that a caller
	// rejecting it can say which entry to change.
	At At

	// Materializes marks a write that only brings Path into existence as an
	// EMPTY container, replacing nothing that was already under it: path
	// creation under on_absent_path, and the target of a nest, which protobuf
	// populates merely by being descended into.
	//
	// The distinction is not cosmetic, it is what keeps the analysis usable.
	// Assigning m_1 drops m_1.m_1.s_1 along with everything else m_1 held, so
	// a policy protecting that leaf must refuse it. Creating m_1 cannot drop
	// it — a container that did not exist held nothing — so refusing every
	// nest through m_1 on those grounds would make nesting unusable under any
	// policy on a field beneath it. See Affects.
	Materializes bool
}

// Affects reports whether w can change the field at p.
func (w Write) Affects(p FieldPath) bool {
	if w.Materializes {
		// Changes w.Path, and the containers that hold it, and nothing below.
		return w.Path.HasPrefix(p)
	}
	return w.Path.Overlaps(p)
}

// Writes reports every field p may modify when applied to a message of type
// md, using DefaultLimits.
//
// It is an OVER-approximation, and deliberately so: a caller uses it to refuse
// a document, and a refusal that is occasionally too strict is a policy
// decision, while one that is occasionally too lax is a hole. Three things are
// widened rather than left undecided:
//
//   - Selector.oneof_member names whichever member is set, which no descriptor
//     can know. Every member of the oneof is reported.
//   - Selector.range, Selector.append, and Selector.every_entry may match
//     nothing at all against a given instance. The field is reported anyway.
//   - on_missing = SKIP may drop a target. The target is reported anyway.
//
// It is not an under-approximation anywhere, which is the property that makes
// it usable as an authorization check. Two things guarantee that. Writes runs
// Validate first, so a document carrying a field from a NEWER revision of the
// schema — an operation or a selector this build cannot interpret — is refused
// rather than analyzed as though the part understood were the whole. And every
// switch over an arm is exhaustive with an erroring default, so a new arm
// added to this build without teaching Writes about it fails to compile a
// silent "modifies nothing".
//
// `test` contributes nothing: it reads, and Validate has already refused a
// test carrying on_absent_path. `copy` reports its targets and not its source,
// for the same reason. `move` reports BOTH, because it clears the source — the
// case a reviewer of a read-only policy is most likely to miss.
//
// A `nest` reports its target even when the delta inside only asserts, because
// descending into an unset singular message field populates it. That write is
// flagged Materializes, which keeps it from being read as a replacement of
// everything beneath — see Write.Materializes.
//
// A path or a target that cannot resolve against md is an error, not an empty
// result, because it is the same error Apply would raise; the exception is a
// field md does not declare at a target position, which no operation can
// create and which therefore contributes no write.
//
// The same field may be reported more than once, once per position in the
// document that writes it. Order follows the document.
func Writes(md protoreflect.MessageDescriptor, p *patchpb.Patch) ([]Write, error) {
	return WritesWith(md, p, DefaultLimits)
}

// WritesWith is Writes under caller-chosen Limits, which bound the Validate it
// performs. A zero field falls back to the corresponding DefaultLimits value.
func WritesWith(md protoreflect.MessageDescriptor, p *patchpb.Patch, lim Limits) ([]Write, error) {
	if md == nil {
		return nil, Errf(CodeMissingField, "", "nil descriptor")
	}
	if err := ValidateWith(p, lim); err != nil {
		return nil, err
	}
	if p.HasMessageType() {
		if want, got := protoreflect.FullName(p.GetMessageType()), md.FullName(); want != got {
			return nil, Errf(CodeMessageTypeMismatch, "message_type",
				"the document was authored against %s, the analysis is against %s", want, got)
		}
	}

	s := &scan{}
	if err := s.delta(place{msg: md}, p.GetDelta(), At("delta")); err != nil {
		return nil, err
	}
	return s.out, nil
}

// place is a container the analysis has reached, and the field path that got
// there. It is the descriptor-level twin of patchproto's cont: exactly one of
// msg, list, and mp is set.
type place struct {
	msg  protoreflect.MessageDescriptor
	list protoreflect.FieldDescriptor
	mp   protoreflect.FieldDescriptor

	path FieldPath
}

func (c place) isMsg() bool  { return c.msg != nil }
func (c place) isList() bool { return c.list != nil }
func (c place) isMap() bool  { return c.mp != nil }

func (c place) describe() string {
	switch {
	case c.isMsg():
		return string(c.msg.FullName())
	case c.isList():
		return string(c.list.FullName()) + " (list)"
	default:
		return string(c.mp.FullName()) + " (map)"
	}
}

type scan struct{ out []Write }

func (s *scan) add(p FieldPath, at At) {
	s.out = append(s.out, Write{Path: p, At: at})
}

// materialize records a write that only brings p into existence. See
// Write.Materializes for why it is not the same as writing p.
func (s *scan) materialize(p FieldPath, at At) {
	s.out = append(s.out, Write{Path: p, At: at, Materializes: true})
}

func (s *scan) delta(base place, d *patchpb.Delta, at At) error {
	for i, e := range d.GetEntries() {
		if err := s.entry(base, e, at.Index("entries", i)); err != nil {
			return err
		}
	}
	return nil
}

func (s *scan) entry(base place, e *patchpb.Entry, at At) error {
	kind := e.WhichKind()
	if kind == patchpb.Entry_Test_case {
		// The one operation that cannot change anything. Its path is navigated
		// without creating, and Validate has already refused a test carrying
		// on_absent_path or on_missing.
		return nil
	}

	cur, err := s.navigate(base, e.GetPath(), at.Sub("path"))
	if err != nil {
		return err
	}

	if e.GetOnAbsentPath() == patchpb.OnAbsentPath_ON_ABSENT_PATH_CREATE {
		// Creation is a write in its own right: the containers along the path
		// come into existence even if every target is then skipped. Only the
		// deepest is recorded — a materialization already implies the ones
		// that hold it, which is what Write.Affects compares against.
		s.materialize(cur.path, at.Sub("on_absent_path"))
	}

	if kind == patchpb.Entry_Move_case {
		// A move empties its source. Validate has already refused move against
		// a container scope, so this is reached only alongside targets.
		mat := at.Sub("move").Sub("from")
		src, ok, err := s.location(base, cur, e.GetMove().GetFrom(), mat)
		if err != nil {
			return err
		}
		if ok {
			s.add(src, mat)
		}
	}

	if e.WhichScope() == patchpb.Entry_Container_case {
		if kind == patchpb.Entry_Nest_case {
			return s.delta(cur, e.GetNest().GetDelta(), at.Sub("nest").Sub("delta"))
		}
		// remove empties the container, assign replaces it, insert fills it.
		s.add(cur.path, at)
		return nil
	}

	for i, sel := range e.GetTargets().GetSelectors() {
		sat := at.Sub("targets").Index("selectors", i)
		fds, err := s.selector(cur, sel, sat)
		if err != nil {
			return err
		}
		for _, fd := range fds {
			p := cur.path
			if fd != nil {
				p = cur.path.with(fd)
			}
			if kind != patchpb.Entry_Nest_case {
				s.add(p, sat)
				continue
			}
			// Descending into an unset singular message field MATERIALIZES it,
			// so a nest is a write to its target even when the delta inside
			// only asserts. Verified against patchproto: nesting a bare
			// exists=false into an unset m_1 leaves m_1 present and empty.
			//
			// A list or map field materializes too, but into an empty
			// collection, which no reader can distinguish from an unset one —
			// so those are left out rather than reported as a change nothing
			// can observe.
			if cur.isMsg() && singularMessage(fd) {
				s.materialize(p, sat)
			}
			inner, err := s.inner(cur, fd, p, sat)
			if err != nil {
				return err
			}
			if err := s.delta(inner, e.GetNest().GetDelta(), at.Sub("nest").Sub("delta")); err != nil {
				return err
			}
		}
	}
	return nil
}

// navigate walks a Path from c. It mirrors patchproto's navigate with the
// instance removed: which positions exist is unknowable here, and does not
// change which FIELD a write lands on.
func (s *scan) navigate(c place, p *patchpb.Path, at At) (place, error) {
	for i, k := range p.GetSegments() {
		next, err := s.descend(c, k, at.Index("segments", i))
		if err != nil {
			return place{}, err
		}
		c = next
	}
	return c, nil
}

func (s *scan) descend(c place, k *patchpb.Key, at At) (place, error) {
	switch {
	case c.isMsg():
		if k.WhichKind() != patchpb.Key_Field_case {
			return place{}, Errf(CodePathNotReached, at,
				"%s is a message; a path into it takes a field", c.msg.FullName())
		}
		fd, vacant, err := ResolveField(c.msg, k.GetField(), at.Sub("field"))
		if err != nil {
			return place{}, err
		}
		if vacant {
			return place{}, Errf(CodePathNotReached, at,
				"%s declares no such field", c.msg.FullName())
		}
		return s.enter(c.path.with(fd), fd, at)

	case c.isList():
		if k.WhichKind() != patchpb.Key_Index_case {
			return place{}, Errf(CodePathNotReached, at,
				"%s is a list; a path into it takes an index", c.list.FullName())
		}
		md := messageUnder(c.list)
		if md == nil {
			return place{}, Errf(CodePathNotReached, at,
				"elements of %s are %v, not containers", c.list.FullName(), c.list.Kind())
		}
		return place{msg: md, path: c.path}, nil

	default:
		if k.WhichKind() != patchpb.Key_MapKey_case {
			return place{}, Errf(CodePathNotReached, at,
				"%s is a map; a path into it takes a map key", c.mp.FullName())
		}
		md := messageUnder(c.mp)
		if md == nil {
			return place{}, Errf(CodePathNotReached, at,
				"values of %s are %v, not containers", c.mp.FullName(), c.mp.MapValue().Kind())
		}
		return place{msg: md, path: c.path}, nil
	}
}

// enter turns a message field into the container it holds.
func (s *scan) enter(path FieldPath, fd protoreflect.FieldDescriptor, at At) (place, error) {
	switch {
	case fd.IsMap():
		return place{mp: fd, path: path}, nil
	case fd.IsList():
		return place{list: fd, path: path}, nil
	case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
		return place{msg: fd.Message(), path: path}, nil
	}
	return place{}, Errf(CodePathNotReached, at,
		"%s is a %v, not a container", fd.FullName(), fd.Kind())
}

// selector reports, for each position the selector names, the message field
// that position sits in — nil when the container is a list or a map, where the
// position is inside the field the container already is.
//
// The switch is exhaustive with an erroring default. A selector arm this build
// does not know must never resolve to "writes nothing": that is the difference
// between a check that fails closed and one that can be walked past.
func (s *scan) selector(c place, sel *patchpb.Selector, at At) ([]protoreflect.FieldDescriptor, error) {
	switch sel.WhichKind() {
	case patchpb.Selector_Key_case:
		return s.key(c, sel.GetKey(), at.Sub("key"))

	case patchpb.Selector_Range_case:
		if !c.isList() {
			return nil, Errf(CodeIllegalArm, at.Sub("range"),
				"a range addresses a list, and %s is not one", c.describe())
		}
		// The interval may be empty against a given list; the field is
		// reported regardless, per the over-approximation Writes documents.
		return []protoreflect.FieldDescriptor{nil}, nil

	case patchpb.Selector_Append_case:
		if !c.isList() {
			return nil, Errf(CodeIllegalArm, at.Sub("append"),
				"append addresses a list, and %s is not one", c.describe())
		}
		return []protoreflect.FieldDescriptor{nil}, nil

	case patchpb.Selector_EveryEntry_case:
		if !c.isMap() {
			return nil, Errf(CodeIllegalArm, at.Sub("every_entry"),
				"every_entry addresses a map, and %s is not one", c.describe())
		}
		return []protoreflect.FieldDescriptor{nil}, nil

	case patchpb.Selector_OneofMember_case:
		return s.oneof(c, sel.GetOneofMember(), at.Sub("oneof_member"))
	}
	return nil, Errf(CodeMissingOneof, at.Sub("kind"), "a selector must name something")
}

// oneof reports every member of the oneof.
//
// Which one a Selector.oneof_member resolves to is whichever is SET, which is
// a property of the instance. A descriptor-level analysis can only name them
// all, and naming them all is the safe direction: the member that would have
// been hit is certainly among them.
func (s *scan) oneof(c place, o *patchpb.Oneof, at At) ([]protoreflect.FieldDescriptor, error) {
	if !c.isMsg() {
		return nil, Errf(CodeIllegalArm, at,
			"a oneof lives in a message, and %s is not one", c.describe())
	}
	od := c.msg.Oneofs().ByName(protoreflect.Name(o.GetName()))
	if od == nil {
		// Like a field the message does not declare: nothing to write to.
		return nil, nil
	}
	if od.IsSynthetic() {
		return nil, Errf(CodeIllegalArm, at,
			"%s is the synthetic oneof of an optional field; address the field itself", od.FullName())
	}

	fields := od.Fields()
	out := make([]protoreflect.FieldDescriptor, 0, fields.Len())
	for i := range fields.Len() {
		out = append(out, fields.Get(i))
	}
	return out, nil
}

// key resolves a single Key against c, returning zero or one field.
func (s *scan) key(c place, k *patchpb.Key, at At) ([]protoreflect.FieldDescriptor, error) {
	switch {
	case c.isMsg():
		if k.WhichKind() != patchpb.Key_Field_case {
			return nil, Errf(CodeIllegalArm, at, "%s is a message; it takes a field", c.describe())
		}
		fd, vacant, err := ResolveField(c.msg, k.GetField(), at.Sub("field"))
		if err != nil {
			return nil, err
		}
		if vacant {
			// No operation can create a field the descriptor does not have, so
			// there is no write here whatever on_missing says.
			return nil, nil
		}
		return []protoreflect.FieldDescriptor{fd}, nil

	case c.isList():
		if k.WhichKind() != patchpb.Key_Index_case {
			return nil, Errf(CodeIllegalArm, at, "%s is a list; it takes an index", c.describe())
		}
		return []protoreflect.FieldDescriptor{nil}, nil

	default:
		if k.WhichKind() != patchpb.Key_MapKey_case {
			return nil, Errf(CodeIllegalArm, at, "%s is a map; it takes a map key", c.describe())
		}
		return []protoreflect.FieldDescriptor{nil}, nil
	}
}

// inner is the container a nest descends into, given the container it was
// applied in and the field the target resolved to. It mirrors patchproto's
// containerAt.
func (s *scan) inner(c place, fd protoreflect.FieldDescriptor, path FieldPath, at At) (place, error) {
	if c.isMsg() {
		p, err := s.enter(path, fd, at)
		if err != nil {
			return place{}, Errf(CodeNotAContainer, at, "%s is a %v", fd.FullName(), fd.Kind())
		}
		return p, nil
	}

	held := c.list
	if !c.isList() {
		held = c.mp
	}
	md := messageUnder(held)
	if md == nil {
		return place{}, Errf(CodeNotAContainer, at, "%s does not hold containers", held.FullName())
	}
	return place{msg: md, path: path}, nil
}

// location resolves a move or copy source. The bool is false when the source
// names a field the message does not declare, which Apply refuses outright and
// which writes nothing either way.
func (s *scan) location(base, cur place, from *patchpb.Location, at At) (FieldPath, bool, error) {
	src := cur
	if from.WhichOrigin() == patchpb.Location_Path_case {
		// A Location path starts at the delta's base container, not at the
		// entry's own path.
		var err error
		src, err = s.navigate(base, from.GetPath(), at.Sub("path"))
		if err != nil {
			return nil, false, err
		}
	}

	fds, err := s.key(src, from.GetKey(), at.Sub("key"))
	if err != nil {
		return nil, false, err
	}
	if len(fds) == 0 {
		return nil, false, nil
	}
	if fds[0] == nil {
		return src.path, true, nil
	}
	return src.path.with(fds[0]), true, nil
}
