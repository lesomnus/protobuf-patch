package patchproto

import (
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// cont is a message, list, or map that an entry addresses. Exactly one of the
// three is set.
type cont struct {
	msg  protoreflect.Message
	list protoreflect.List
	mp   protoreflect.Map

	// fd is the field the list or map came from, which carries the element or
	// value kind. It is nil for a message.
	fd protoreflect.FieldDescriptor
}

func msgCont(m protoreflect.Message) cont { return cont{msg: m} }
func listCont(l protoreflect.List, fd protoreflect.FieldDescriptor) cont {
	return cont{list: l, fd: fd}
}
func mapCont(m protoreflect.Map, fd protoreflect.FieldDescriptor) cont {
	return cont{mp: m, fd: fd}
}

func (c cont) isMsg() bool  { return c.msg != nil }
func (c cont) isList() bool { return c.list != nil }
func (c cont) isMap() bool  { return c.mp != nil }

func (c cont) describe() string {
	switch {
	case c.isMsg():
		return string(c.msg.Descriptor().FullName())
	case c.isList():
		return string(c.fd.FullName()) + " (list)"
	default:
		return string(c.fd.FullName()) + " (map)"
	}
}

// loc is one resolved position inside a cont.
type loc struct {
	// exactly one of these is meaningful, per the cont it came from
	fd  protoreflect.FieldDescriptor
	idx int
	key protoreflect.MapKey

	// appendArm marks the position one past the last list element, which has
	// no index until the moment it is written.
	appendArm bool

	// noSlot marks an address that names no position at all: a field the
	// message does not declare, or an index outside the list. No operation can
	// act on one, so it is always a missing target.
	noSlot bool

	// emptySlot marks a position that exists but holds nothing — a map key
	// with no entry under it. Writing operations fill it, which is how a map
	// entry is created at all; remove and nest need content and treat it as a
	// missing target.
	//
	// A declared message field is never an empty slot: it always exists, and
	// whether it is set is presence, which is what `exists` reports.
	emptySlot bool

	// od marks the oneof itself, with no member set. Only a test ever sees
	// one: for every other kind an unset oneof resolves to no location at all,
	// because there is nothing to act on and no way to say which member a
	// value would be for. A test needs the position so that exists=false has
	// something to report about.
	od protoreflect.OneofDescriptor
}

// key returns a comparable identity for duplicate detection.
func (l loc) ident() any {
	switch {
	case l.od != nil:
		return l.od.FullName()
	case l.fd != nil:
		return l.fd.Number()
	case l.appendArm:
		return "append"
	case l.key.IsValid():
		return l.key.Interface()
	default:
		return l.idx
	}
}

// navigate walks p from c, returning the container it reaches.
//
// A path never creates anything and never tolerates a miss: a vacant key or an
// unset singular message field along the way is CodePathNotReached, regardless
// of on_missing.
func navigate(c cont, p *patchpb.Path, at patch.At) (cont, error) {
	for i, k := range p.GetSegments() {
		next, err := descend(c, k, at.Index("segments", i))
		if err != nil {
			return cont{}, err
		}
		c = next
	}
	return c, nil
}

func descend(c cont, k *patchpb.Key, at patch.At) (cont, error) {
	switch {
	case c.isMsg():
		return descendMessage(c.msg, k, at)
	case c.isList():
		return descendList(c.list, c.fd, k, at)
	default:
		return descendMap(c.mp, c.fd, k, at)
	}
}

func descendMessage(m protoreflect.Message, k *patchpb.Key, at patch.At) (cont, error) {
	if k.WhichKind() != patchpb.Key_Field_case {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s is a message; a path into it takes a field", m.Descriptor().FullName())
	}
	fd, vacant, err := patch.ResolveField(m.Descriptor(), k.GetField(), at.Sub("field"))
	if err != nil {
		return cont{}, err
	}
	if vacant {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s declares no such field", m.Descriptor().FullName())
	}

	switch {
	case fd.IsMap():
		// A map field has no presence: it always exists as a container, and an
		// empty one is simply a container with nothing in it.
		return mapCont(m.Mutable(fd).Map(), fd), nil
	case fd.IsList():
		return listCont(m.Mutable(fd).List(), fd), nil
	case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
		// A singular message field does have presence, and descending into an
		// unset one would have to create it.
		if !m.Has(fd) {
			return cont{}, patch.Errf(patch.CodePathNotReached, at,
				"%s is not set; a path never creates a container", fd.FullName())
		}
		return msgCont(m.Mutable(fd).Message()), nil
	default:
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s is a %v, not a container", fd.FullName(), fd.Kind())
	}
}

func descendList(l protoreflect.List, fd protoreflect.FieldDescriptor, k *patchpb.Key, at patch.At) (cont, error) {
	if k.WhichKind() != patchpb.Key_Index_case {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s is a list; a path into it takes an index", fd.FullName())
	}
	i, vacant := patch.NormalizeIndex(k.GetIndex(), l.Len())
	if vacant {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"index %d is outside [0, %d)", k.GetIndex(), l.Len())
	}
	if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"elements of %s are %v, not containers", fd.FullName(), fd.Kind())
	}
	return msgCont(l.Get(i).Message()), nil
}

func descendMap(mp protoreflect.Map, fd protoreflect.FieldDescriptor, k *patchpb.Key, at patch.At) (cont, error) {
	if k.WhichKind() != patchpb.Key_MapKey_case {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s is a map; a path into it takes a map key", fd.FullName())
	}
	mk, err := patch.MapKeyFor(k.GetMapKey(), fd, at.Sub("map_key"))
	if err != nil {
		return cont{}, err
	}
	if !mp.Has(mk) {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"%s has no such key; a path never creates one", fd.FullName())
	}
	if fd.MapValue().Kind() != protoreflect.MessageKind && fd.MapValue().Kind() != protoreflect.GroupKind {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"values of %s are %v, not containers", fd.FullName(), fd.MapValue().Kind())
	}
	return msgCont(mp.Get(mk).Message()), nil
}

// expand resolves every selector against c as it stands BEFORE the entry runs,
// so that the result does not depend on the order the selectors appear in and
// an earlier write cannot shift a later target.
//
// Vacant positions are dropped when the entry tolerates them and reported
// otherwise. Two selectors resolving to the same location is always an error:
// the schema calls targets a set, and a set with a duplicate is a mistake, not
// a repetition.
func expand(c cont, ts *patchpb.Targets, e *patchpb.Entry, at patch.At) ([]loc, error) {
	skip := e.GetOnMissing() == patchpb.OnMissing_ON_MISSING_SKIP
	isTest := e.WhichKind() == patchpb.Entry_Test_case

	var out []loc
	seen := map[any]int{}

	for i, s := range ts.GetSelectors() {
		sat := at.Index("selectors", i)
		locs, err := resolveSelector(c, s, isTest, sat)
		if err != nil {
			return nil, err
		}
		for _, l := range locs {
			// A test reads a missing target rather than being governed by it,
			// which is what makes exists=false satisfiable everywhere.
			if miss := l.noSlot || (l.emptySlot && needsContent(e.WhichKind())); miss && !isTest {
				if skip {
					continue
				}
				return nil, patch.Errf(patch.CodeVacantTarget, sat,
					"nothing at that position in %s; set on_missing to skip it deliberately", c.describe())
			}
			id := l.ident()
			if prev, dup := seen[id]; dup {
				return nil, patch.Errf(patch.CodeDuplicateTarget, sat,
					"already selected by selectors[%d]", prev)
			}
			seen[id] = i
			out = append(out, l)
		}
	}
	return out, nil
}

// needsContent reports whether the operation requires something to already be
// at the position. remove has nothing to delete and nest has nothing to
// descend into; the writing operations fill the slot instead, which is how a
// map entry comes into existence.
func needsContent(kind any) bool {
	switch kind {
	case patchpb.Entry_Remove_case, patchpb.Entry_Nest_case:
		return true
	}
	return false
}

// resolveSelector turns one selector into the positions it names.
//
// A Range that matches nothing returns no locations rather than an absent one:
// an empty range is a defined answer, not a miss, so on_missing never applies
// to it.
func resolveSelector(c cont, s *patchpb.Selector, isTest bool, at patch.At) ([]loc, error) {
	switch s.WhichKind() {
	case patchpb.Selector_Key_case:
		l, err := resolveKey(c, s.GetKey(), at.Sub("key"))
		if err != nil {
			return nil, err
		}
		return []loc{l}, nil

	case patchpb.Selector_Range_case:
		if !c.isList() {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("range"),
				"a range addresses a list, and %s is not one", c.describe())
		}
		begin, end := patch.NormalizeRange(s.GetRange(), c.list.Len())
		out := make([]loc, 0, end-begin)
		for i := begin; i < end; i++ {
			out = append(out, loc{idx: i})
		}
		return out, nil

	case patchpb.Selector_Append_case:
		if !c.isList() {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("append"),
				"append addresses a list, and %s is not one", c.describe())
		}
		return []loc{{appendArm: true}}, nil

	case patchpb.Selector_EveryEntry_case:
		if !c.isMap() {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("every_entry"),
				"every_entry addresses a map, and %s is not one", c.describe())
		}
		out := make([]loc, 0, c.mp.Len())
		c.mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
			out = append(out, loc{key: k})
			return true
		})
		// Map iteration order is unspecified and targets are an unordered set,
		// so the result is the same either way; sorting only makes the position
		// named in an error deterministic.
		sort.Slice(out, func(i, j int) bool { return out[i].key.String() < out[j].key.String() })
		return out, nil

	case patchpb.Selector_OneofMember_case:
		return resolveOneof(c, s.GetOneofMember(), isTest, at.Sub("oneof_member"))
	}
	return nil, patch.Errf(patch.CodeMissingOneof, at.Sub("kind"), "a selector must name something")
}

func resolveKey(c cont, k *patchpb.Key, at patch.At) (loc, error) {
	switch {
	case c.isMsg():
		if k.WhichKind() != patchpb.Key_Field_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a message; it takes a field", c.describe())
		}
		fd, undeclared, err := patch.ResolveField(c.msg.Descriptor(), k.GetField(), at.Sub("field"))
		if err != nil {
			return loc{}, err
		}
		if undeclared {
			// No operation can create a field the descriptor does not have.
			return loc{noSlot: true}, nil
		}
		// A declared field always exists as a position, set or not.
		return loc{fd: fd}, nil

	case c.isList():
		if k.WhichKind() != patchpb.Key_Index_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a list; it takes an index", c.describe())
		}
		i, outside := patch.NormalizeIndex(k.GetIndex(), c.list.Len())
		if outside {
			// A list has no slot beyond its length; growing it is what
			// Selector.append is for.
			return loc{noSlot: true}, nil
		}
		return loc{idx: i}, nil

	default:
		if k.WhichKind() != patchpb.Key_MapKey_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a map; it takes a map key", c.describe())
		}
		mk, err := patch.MapKeyFor(k.GetMapKey(), c.fd, at.Sub("map_key"))
		if err != nil {
			return loc{}, err
		}
		// Any key of the right type names a valid slot, whether or not an
		// entry is in it. That is what lets assign create one.
		return loc{key: mk, emptySlot: !c.mp.Has(mk)}, nil
	}
}

// resolveOneof turns a oneof selector into the member currently set.
//
// It names zero or one location, which is the whole reason it is a Selector:
// as a Key it would have had to promise exactly one, and then `assign` would
// have needed the Value to say which member it was for — undecidable as soon
// as two members share a type.
//
// An unset oneof resolves to nothing for every kind but `test`, which reads
// the oneof itself so that exists=false is satisfiable. That is the carve-out
// `test` already has for a missing target, applied here.
func resolveOneof(c cont, o *patchpb.Oneof, isTest bool, at patch.At) ([]loc, error) {
	if !c.isMsg() {
		return nil, patch.Errf(patch.CodeIllegalArm, at,
			"a oneof lives in a message, and %s is not one", c.describe())
	}

	md := c.msg.Descriptor()
	od := md.Oneofs().ByName(protoreflect.Name(o.GetName()))
	if od == nil {
		// Like a field the message does not declare.
		return []loc{{noSlot: true}}, nil
	}
	if od.IsSynthetic() {
		// protobuf generates one of these per proto3 `optional` field.
		// Addressing it would be a second way to spell what Key.field already
		// says, and the format keeps one spelling per capability.
		return nil, patch.Errf(patch.CodeIllegalArm, at,
			"%s is the synthetic oneof of an optional field; address the field itself", od.FullName())
	}

	if fd := c.msg.WhichOneof(od); fd != nil {
		return []loc{{fd: fd}}, nil
	}
	if isTest {
		return []loc{{od: od}}, nil
	}
	return nil, nil
}
