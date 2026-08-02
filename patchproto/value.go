package patchproto

import (
	"bytes"
	"math"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// singular converts v into the value for one position — a singular field, a
// list element, or a map value — allocating through alloc when v is a message.
//
// The arm must match the target's kind exactly; there is no conversion, so
// nothing is widened, narrowed, or truncated on the way in.
func singular(
	v *patchpb.Value,
	fd protoreflect.FieldDescriptor,
	site patch.Site,
	alloc func() protoreflect.Value,
	at patch.At,
) (protoreflect.Value, error) {
	if err := patch.CheckArm(v, fd, site, at); err != nil {
		return protoreflect.Value{}, err
	}
	if patch.ShapeOf(v) != patch.ShapeMessage {
		return patch.Scalar(v, fd, site, at)
	}
	pv := alloc()
	if err := fillMessage(pv.Message(), v.GetM(), at.Sub("m")); err != nil {
		return protoreflect.Value{}, err
	}
	return pv, nil
}

// fillMessage replaces m's contents with mv.
//
// Fields mv does not mention are left as they are; the caller clears first when
// the operation is a wholesale replacement. A field that mv names but the
// message does not declare is vacant, and a path or value position never
// tolerates vacancy — silently dropping it is how the previous implementation
// turned a container assign into partial data loss.
func fillMessage(m protoreflect.Message, mv *patchpb.MessageValue, at patch.At) error {
	seenOneof := map[protoreflect.FullName]int{}

	for i, fv := range mv.GetFields() {
		fat := at.Index("fields", i)
		fd, vacant, err := patch.ResolveField(m.Descriptor(), fv.GetKey(), fat.Sub("key"))
		if err != nil {
			return err
		}
		if vacant {
			return patch.Errf(patch.CodeVacantTarget, fat.Sub("key"),
				"%s declares no such field", m.Descriptor().FullName())
		}
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			if prev, dup := seenOneof[od.FullName()]; dup {
				return patch.Errf(patch.CodeDuplicateTarget, fat.Sub("key"),
					"fields[%d] already set a member of oneof %s, and setting one clears the other", prev, od.Name())
			}
			seenOneof[od.FullName()] = i
		}

		if err := setField(m, fd, fv.GetValue(), fat.Sub("value")); err != nil {
			return err
		}
	}
	return nil
}

// setField writes v into m's field fd, whatever its cardinality.
func setField(m protoreflect.Message, fd protoreflect.FieldDescriptor, v *patchpb.Value, at patch.At) error {
	if err := patch.CheckArm(v, fd, patch.SiteField, at); err != nil {
		return err
	}
	switch {
	case fd.IsMap():
		mp := m.Mutable(fd).Map()
		clearMap(mp)
		return fillMap(mp, fd, v.GetMap(), at.Sub("map"))
	case fd.IsList():
		l := m.Mutable(fd).List()
		l.Truncate(0)
		return fillList(l, fd, v.GetL(), at.Sub("l"))
	default:
		pv, err := singular(v, fd, patch.SiteField, func() protoreflect.Value {
			return m.NewField(fd)
		}, at)
		if err != nil {
			return err
		}
		m.Set(fd, pv)
		return nil
	}
}

// fillList appends lv's elements to l.
func fillList(l protoreflect.List, fd protoreflect.FieldDescriptor, lv *patchpb.ListValue, at patch.At) error {
	for i, ev := range lv.GetValues() {
		pv, err := singular(ev, fd, patch.SiteElement, l.NewElement, at.Index("values", i))
		if err != nil {
			return err
		}
		l.Append(pv)
	}
	return nil
}

// fillMap writes mv's entries into mp. Keys must be unique after resolution:
// two entries naming the same key is a mistake, not a later-wins merge.
func fillMap(mp protoreflect.Map, fd protoreflect.FieldDescriptor, mv *patchpb.MapValue, at patch.At) error {
	seen := map[any]int{}
	for i, me := range mv.GetEntries() {
		eat := at.Index("entries", i)
		mk, err := patch.MapKeyFor(me.GetKey(), fd, eat.Sub("key"))
		if err != nil {
			return err
		}
		if prev, dup := seen[mk.Interface()]; dup {
			return patch.Errf(patch.CodeDuplicateTarget, eat.Sub("key"),
				"entries[%d] already carries that key", prev)
		}
		seen[mk.Interface()] = i

		pv, err := singular(me.GetValue(), fd, patch.SiteMapValue, mp.NewValue, eat.Sub("value"))
		if err != nil {
			return err
		}
		mp.Set(mk, pv)
	}
	return nil
}

func clearMap(mp protoreflect.Map) {
	var keys []protoreflect.MapKey
	mp.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	for _, k := range keys {
		mp.Clear(k)
	}
}

// clearMessage clears every declared field of m while leaving anything the
// descriptor does not declare untouched. A Patch never discards data it did
// not name, and unknown fields are not addressable by any Key.
func clearMessage(m protoreflect.Message) {
	var fds []protoreflect.FieldDescriptor
	m.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fds = append(fds, fd)
		return true
	})
	for _, fd := range fds {
		m.Clear(fd)
	}
}

// equalValue compares a target value against a Value literal.
func equalValue(got protoreflect.Value, want *patchpb.Value, fd protoreflect.FieldDescriptor, site patch.Site, alloc func() protoreflect.Value, at patch.At) (bool, error) {
	switch patch.ShapeOf(want) {
	case patch.ShapeList:
		if err := patch.CheckArm(want, fd, site, at); err != nil {
			return false, err
		}
		return equalList(got.List(), fd, want.GetL(), at)
	case patch.ShapeMap:
		if err := patch.CheckArm(want, fd, site, at); err != nil {
			return false, err
		}
		return equalMap(got.Map(), fd, want.GetMap(), at)
	}

	pv, err := singular(want, fd, site, alloc, at)
	if err != nil {
		return false, err
	}
	return equalScalarOrMessage(got, pv, fd, site), nil
}

func equalScalarOrMessage(a, b protoreflect.Value, fd protoreflect.FieldDescriptor, site patch.Site) bool {
	kind := fd.Kind()
	if site == patch.SiteMapValue {
		kind = fd.MapValue().Kind()
	}
	return equalOfKind(kind, a, b)
}

// equalOfKind is the schema's EQUALITY rule for one value of a known kind.
func equalOfKind(kind protoreflect.Kind, a, b protoreflect.Value) bool {
	switch kind {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return equalMessage(a.Message(), b.Message())
	case protoreflect.BytesKind:
		return bytes.Equal(a.Bytes(), b.Bytes())
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return equalFloat(a.Float(), b.Float())
	default:
		return a.Interface() == b.Interface()
	}
}

// equalFloat implements the schema's rule that NaN equals NaN.
//
// A test asserts what a document holds, not an arithmetic predicate. Under
// IEEE inequality a patch could write a NaN and then be unable to assert the
// value it had just written, and the answer would additionally depend on
// whether the float sat at the top level or inside a message — proto.Equal
// already treats NaN as equal, so the two paths disagreed.
//
// -0.0 and +0.0 stay equal, which == gives.
func equalFloat(x, y float64) bool {
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.IsNaN(x) && math.IsNaN(y)
	}
	return x == y
}

// equalMessage implements the schema's rule that unknown fields on the target
// take no part in equality.
//
// proto.Equal cannot be used: it compares the unknown-field set, and the
// format promises to preserve unknown fields precisely because a Value has no
// way to mention one. Together those made `test` unsatisfiable — a message
// carrying any unknown field could never be asserted equal to anything, and
// there was no way to write a Value that would have passed.
//
// Equality is otherwise EXACT. Both messages must have the same fields set,
// which is what makes a Value absent from `fields` an assertion of absence
// rather than a wildcard.
func equalMessage(a, b protoreflect.Message) bool {
	if a.Descriptor().FullName() != b.Descriptor().FullName() {
		return false
	}
	if countSet(a) != countSet(b) {
		return false
	}

	eq := true
	a.Range(func(fd protoreflect.FieldDescriptor, av protoreflect.Value) bool {
		if !b.Has(fd) || !equalField(fd, av, b.Get(fd)) {
			eq = false
		}
		return eq
	})
	return eq
}

func countSet(m protoreflect.Message) int {
	n := 0
	m.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
		n++
		return true
	})
	return n
}

func equalField(fd protoreflect.FieldDescriptor, a, b protoreflect.Value) bool {
	switch {
	case fd.IsMap():
		return equalMapField(fd, a.Map(), b.Map())
	case fd.IsList():
		return equalListField(fd, a.List(), b.List())
	default:
		return equalOfKind(fd.Kind(), a, b)
	}
}

func equalListField(fd protoreflect.FieldDescriptor, a, b protoreflect.List) bool {
	if a.Len() != b.Len() {
		return false
	}
	for i := range a.Len() {
		if !equalOfKind(fd.Kind(), a.Get(i), b.Get(i)) {
			return false
		}
	}
	return true
}

func equalMapField(fd protoreflect.FieldDescriptor, a, b protoreflect.Map) bool {
	if a.Len() != b.Len() {
		return false
	}
	kind := fd.MapValue().Kind()
	eq := true
	a.Range(func(k protoreflect.MapKey, av protoreflect.Value) bool {
		if !b.Has(k) || !equalOfKind(kind, av, b.Get(k)) {
			eq = false
		}
		return eq
	})
	return eq
}

func equalList(l protoreflect.List, fd protoreflect.FieldDescriptor, want *patchpb.ListValue, at patch.At) (bool, error) {
	vs := want.GetValues()
	if l.Len() != len(vs) {
		return false, nil
	}
	for i, ev := range vs {
		pv, err := singular(ev, fd, patch.SiteElement, l.NewElement, at.Sub("l").Index("values", i))
		if err != nil {
			return false, err
		}
		if !equalScalarOrMessage(l.Get(i), pv, fd, patch.SiteElement) {
			return false, nil
		}
	}
	return true, nil
}

func equalMap(mp protoreflect.Map, fd protoreflect.FieldDescriptor, want *patchpb.MapValue, at patch.At) (bool, error) {
	es := want.GetEntries()
	seen := map[any]struct{}{}
	for i, me := range es {
		eat := at.Sub("map").Index("entries", i)
		mk, err := patch.MapKeyFor(me.GetKey(), fd, eat.Sub("key"))
		if err != nil {
			return false, err
		}
		if _, dup := seen[mk.Interface()]; dup {
			return false, patch.Errf(patch.CodeDuplicateTarget, eat.Sub("key"), "repeated key")
		}
		seen[mk.Interface()] = struct{}{}

		if !mp.Has(mk) {
			return false, nil
		}
		pv, err := singular(me.GetValue(), fd, patch.SiteMapValue, mp.NewValue, eat.Sub("value"))
		if err != nil {
			return false, err
		}
		if !equalScalarOrMessage(mp.Get(mk), pv, fd, patch.SiteMapValue) {
			return false, nil
		}
	}
	// Compare sizes only after deduplicating, so that a Value with a repeated
	// key cannot make an otherwise-equal map look different.
	return mp.Len() == len(seen), nil
}
