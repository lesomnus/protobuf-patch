package patchstruct

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// cont is a struct, map, or slice that an entry addresses. The reflect.Value
// is always addressable, so a slice can change length in place.
type cont struct {
	v reflect.Value
}

func (c cont) kind() reflect.Kind { return c.v.Kind() }

func (c cont) describe() string { return c.v.Type().String() }

// loc is one resolved position inside a cont.
type loc struct {
	field reflect.StructField // struct
	key   reflect.Value       // map
	idx   int                 // slice

	appendArm bool

	// noSlot marks an address that names no position at all: a field the
	// struct type does not declare, or an index outside the slice.
	noSlot bool

	// emptySlot marks a position that exists but holds nothing: a map key with
	// no entry under it. A struct field is never an empty slot — it always
	// exists, and whether it holds a value is presence.
	emptySlot bool
}

func (l loc) identIn(c cont) any {
	if l.appendArm {
		return "\x00append"
	}
	switch c.kind() {
	case reflect.Struct:
		return l.field.Name
	case reflect.Map:
		return l.key.Interface()
	default:
		return l.idx
	}
}

// lookup is the result of resolving a Field against a struct type.
type lookup struct {
	found bool
	err   error
}

// lookupField resolves a Field against a struct type.
//
// Field.name matches the Go field name and Field.json_name matches the `json`
// tag — the same split protobuf makes, so a Field that pins both must have
// them describe the same field. A number is refused: a hand-written struct has
// no field numbers, and an unverifiable constraint must not be dropped.
//
// Unexported fields are not addressable, so they name no position at all.
// Embedded fields are NOT flattened: encoding/json's promotion brings name
// collisions and depth rules with it, and an embedded field can be addressed
// by its type name like any other.
func lookupField(t reflect.Type, f *patchpb.Field, at patch.At) (reflect.StructField, lookup) {
	if f.HasNumber() {
		return reflect.StructField{}, lookup{err: patch.Errf(patch.CodeIllegalArm, at.Sub("number"),
			"a Go struct has no field numbers, so this constraint cannot be checked")}
	}
	if !f.HasName() && !f.HasJsonName() {
		return reflect.StructField{}, lookup{err: patch.Errf(patch.CodeFieldNoIdentifier, at,
			"at least one of name or json_name is required")}
	}

	// Resolve by one identifier, then verify the rest against what resolved —
	// the same procedure protobuf's Field uses. Requiring each to resolve
	// independently would report a disagreement as a missing field, and a
	// disagreement must never be skippable.
	var found *reflect.StructField
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		if f.HasName() {
			if sf.Name == f.GetName() {
				found = &sf
				break
			}
			continue
		}
		if jsonName(sf) == f.GetJsonName() {
			found = &sf
			break
		}
	}
	if found == nil {
		return reflect.StructField{}, lookup{}
	}
	if f.HasJsonName() && jsonName(*found) != f.GetJsonName() {
		return reflect.StructField{}, lookup{err: patch.Errf(patch.CodeFieldConflict, at,
			"%s has the json name %q, not %q", found.Name, jsonName(*found), f.GetJsonName())}
	}
	return *found, lookup{found: true}
}

// jsonName is the name encoding/json would use for a field.
func jsonName(sf reflect.StructField) string {
	tag, ok := sf.Tag.Lookup("json")
	if !ok {
		return sf.Name
	}
	for i := range len(tag) {
		if tag[i] == ',' {
			tag = tag[:i]
			break
		}
	}
	if tag == "" || tag == "-" {
		return sf.Name
	}
	return tag
}

// present reports whether a position holds a value.
//
// A pointer field spells explicit presence: nil is absent. A plain field has
// no presence of its own, so it counts as absent when it holds its zero value
// — the same reading protobuf gives an implicit-presence field.
func present(v reflect.Value) bool {
	if v.Kind() == reflect.Ptr {
		return !v.IsNil()
	}
	return !v.IsZero()
}

// clear resets a position: a pointer to nil, anything else to its zero value.
func clearValue(v reflect.Value) {
	v.Set(reflect.Zero(v.Type()))
}

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
	l, err := resolveKey(c, k, at)
	if err != nil {
		return cont{}, err
	}
	if l.noSlot || l.emptySlot {
		return cont{}, patch.Errf(patch.CodePathNotReached, at,
			"nothing at that position in %s; a path never creates one", c.describe())
	}

	v, err := at_(c, l, at)
	if err != nil {
		return cont{}, err
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return cont{}, patch.Errf(patch.CodePathNotReached, at,
				"that position is nil; a path never creates a container")
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		return cont{v: v}, nil
	case reflect.Map, reflect.Slice:
		if v.Type() == byteSlice {
			break
		}
		// A nil map or slice is Go's spelling of an empty one, and the format
		// says a map or list field always exists as a container. So it is
		// materialized rather than refused — the same thing patchproto does
		// with Mutable. A struct field or slice element is addressable, so this
		// is possible; a map VALUE is not, and stays refused.
		if v.IsNil() {
			if !v.CanSet() {
				return cont{}, patch.Errf(patch.CodePathNotReached, at,
					"that %s is nil and not addressable here", v.Kind())
			}
			if v.Kind() == reflect.Map {
				v.Set(reflect.MakeMap(v.Type()))
			} else {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
		}
		return cont{v: v}, nil
	}
	return cont{}, patch.Errf(patch.CodePathNotReached, at,
		"%s is not a container", v.Type())
}

// at_ returns an addressable reflect.Value for a position. Map entries are not
// addressable in Go, so a map position is handled by the operations directly
// rather than through this.
func at_(c cont, l loc, at patch.At) (reflect.Value, error) {
	switch c.kind() {
	case reflect.Struct:
		return c.v.FieldByIndex(l.field.Index), nil
	case reflect.Slice:
		return c.v.Index(l.idx), nil
	default:
		v := c.v.MapIndex(l.key)
		if !v.IsValid() {
			return reflect.Value{}, patch.Errf(patch.CodeVacantTarget, at, "no such key")
		}
		// Copy out: a map entry cannot be addressed in place.
		out := reflect.New(c.v.Type().Elem()).Elem()
		out.Set(v)
		return out, nil
	}
}

func expand(c cont, ts *patchpb.Targets, e *patchpb.Entry, at patch.At) ([]loc, error) {
	skip := e.GetOnMissing() == patchpb.OnMissing_ON_MISSING_SKIP
	isTest := e.WhichKind() == patchpb.Entry_Test_case
	wantsContent := needsContent(e.WhichKind())

	var out []loc
	seen := map[any]int{}

	for i, s := range ts.GetSelectors() {
		sat := at.Index("selectors", i)
		locs, err := resolveSelector(c, s, sat)
		if err != nil {
			return nil, err
		}
		for _, l := range locs {
			if miss := l.noSlot || (l.emptySlot && wantsContent); miss && !isTest {
				if skip {
					continue
				}
				return nil, patch.Errf(patch.CodeVacantTarget, sat,
					"nothing at that position in %s; set on_missing to skip it deliberately", c.describe())
			}
			id := l.identIn(c)
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

func needsContent(kind any) bool {
	switch kind {
	case patchpb.Entry_Remove_case, patchpb.Entry_Nest_case:
		return true
	}
	return false
}

func resolveSelector(c cont, s *patchpb.Selector, at patch.At) ([]loc, error) {
	switch s.WhichKind() {
	case patchpb.Selector_Key_case:
		l, err := resolveKey(c, s.GetKey(), at.Sub("key"))
		if err != nil {
			return nil, err
		}
		return []loc{l}, nil

	case patchpb.Selector_Range_case:
		if c.kind() != reflect.Slice {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("range"),
				"a range addresses a slice, and %s is not one", c.describe())
		}
		begin, end := patch.NormalizeRange(s.GetRange(), c.v.Len())
		out := make([]loc, 0, end-begin)
		for i := begin; i < end; i++ {
			out = append(out, loc{idx: i})
		}
		return out, nil

	case patchpb.Selector_Append_case:
		if c.kind() != reflect.Slice {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("append"),
				"append addresses a slice, and %s is not one", c.describe())
		}
		return []loc{{appendArm: true}}, nil

	case patchpb.Selector_EveryEntry_case:
		if c.kind() != reflect.Map {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("every_entry"),
				"every_entry addresses a map, and %s is not one", c.describe())
		}
		keys := c.v.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface())
		})
		out := make([]loc, 0, len(keys))
		for _, k := range keys {
			out = append(out, loc{key: k})
		}
		return out, nil

	case patchpb.Selector_OneofMember_case:
		// Go has no oneof. A hand-written struct may model one — an interface,
		// a tag field, a set of pointers — but the format cannot know which,
		// and picking one would be guessing.
		return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("oneof_member"),
			"a Go struct has no oneof to address")
	}
	return nil, patch.Errf(patch.CodeMissingOneof, at.Sub("kind"), "a selector must name something")
}

func resolveKey(c cont, k *patchpb.Key, at patch.At) (loc, error) {
	switch c.kind() {
	case reflect.Struct:
		if k.WhichKind() != patchpb.Key_Field_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a struct; it takes a field", c.describe())
		}
		sf, res := lookupField(c.v.Type(), k.GetField(), at.Sub("field"))
		if res.err != nil {
			return loc{}, res.err
		}
		if !res.found {
			// No operation can create a field the type does not declare.
			return loc{noSlot: true}, nil
		}
		return loc{field: sf}, nil

	case reflect.Slice:
		if k.WhichKind() != patchpb.Key_Index_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a slice; it takes an index", c.describe())
		}
		i, outside := patch.NormalizeIndex(k.GetIndex(), c.v.Len())
		if outside {
			return loc{noSlot: true}, nil
		}
		return loc{idx: i}, nil

	default:
		if k.WhichKind() != patchpb.Key_MapKey_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"%s is a map; it takes a map key", c.describe())
		}
		mk, err := mapKey(k.GetMapKey(), c.v.Type().Key(), at.Sub("map_key"))
		if err != nil {
			return loc{}, err
		}
		return loc{key: mk, emptySlot: !c.v.MapIndex(mk).IsValid()}, nil
	}
}
