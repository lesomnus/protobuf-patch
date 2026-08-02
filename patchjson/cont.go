package patchjson

import (
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// cont is a JSON object or array that an entry addresses.
//
// JSON has two containers where the format has three. An object stands in for
// BOTH a message and a map, and it behaves like the map: with no schema, every
// key is a valid position whether or not anything is under it. That single
// rule is what makes the engine definable — the format's "a declared field
// always exists, an undeclared one never can" has no JSON shadow, and without
// a schema it does not need one.
type cont struct {
	obj map[string]any
	arr []any

	isArr bool

	// sync writes a replacement array back to wherever this one came from.
	// Objects mutate in place; arrays change length, so the parent has to be
	// told. nil for the object case.
	sync func([]any)
}

func objCont(m map[string]any) cont { return cont{obj: m} }

func arrCont(a []any, sync func([]any)) cont {
	return cont{arr: a, isArr: true, sync: sync}
}

func (c cont) describe() string {
	if c.isArr {
		return "array"
	}
	return "object"
}

func (c *cont) setArr(a []any) {
	c.arr = a
	if c.sync != nil {
		c.sync(a)
	}
}

// loc is one resolved position inside a cont.
type loc struct {
	key string
	idx int

	appendArm bool

	// noSlot marks an address that names no position: an index outside the
	// array. Every operation treats it as a missing target.
	noSlot bool

	// emptySlot marks a position that exists but holds nothing: an object key
	// with no member under it. Writing operations fill it; remove and nest
	// need content and treat it as missing.
	emptySlot bool
}

func (l loc) identIn(c cont) any {
	if l.appendArm {
		return "\x00append"
	}
	if c.isArr {
		return l.idx
	}
	return l.key
}

// navigate walks p from c. A path never creates anything and never tolerates a
// miss, whatever on_missing says.
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
			"nothing at that position in the %s; a path never creates one", c.describe())
	}

	var child any
	var sync func([]any)
	if c.isArr {
		child = c.arr[l.idx]
		idx := l.idx
		arr := c.arr
		sync = func(a []any) { arr[idx] = a }
	} else {
		child = c.obj[l.key]
		key := l.key
		obj := c.obj
		sync = func(a []any) { obj[key] = a }
	}

	switch v := child.(type) {
	case map[string]any:
		return objCont(v), nil
	case []any:
		return arrCont(v, sync), nil
	}
	return cont{}, patch.Errf(patch.CodePathNotReached, at,
		"that position holds a scalar, not a container")
}

// expand resolves every selector against the container as it stood BEFORE the
// entry ran, so the outcome does not depend on selector order.
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
					"nothing at that position in the %s; set on_missing to skip it deliberately", c.describe())
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
		if !c.isArr {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("range"),
				"a range addresses an array, and this is an object")
		}
		begin, end := patch.NormalizeRange(s.GetRange(), len(c.arr))
		out := make([]loc, 0, end-begin)
		for i := begin; i < end; i++ {
			out = append(out, loc{idx: i})
		}
		return out, nil

	case patchpb.Selector_Append_case:
		if !c.isArr {
			return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("append"),
				"append addresses an array, and this is an object")
		}
		return []loc{{appendArm: true}}, nil

	case patchpb.Selector_OneofMember_case:
		// A oneof is a protobuf declaration. A JSON object has none, and
		// guessing which of its members is "set" would be inventing a schema.
		return nil, patch.Errf(patch.CodeIllegalArm, at.Sub("oneof_member"),
			"a oneof is declared by a schema, and a JSON document has none")
	}
	return nil, patch.Errf(patch.CodeMissingOneof, at.Sub("kind"), "a selector must name something")
}

// resolveKey turns a Key into a position.
//
// Both `field` and `map_key` address an object member: with no schema there is
// no difference between a message field and a map entry, so refusing one of
// them would only make the author guess which spelling this engine wanted.
func resolveKey(c cont, k *patchpb.Key, at patch.At) (loc, error) {
	if c.isArr {
		if k.WhichKind() != patchpb.Key_Index_case {
			return loc{}, patch.Errf(patch.CodeIllegalArm, at,
				"an array takes an index")
		}
		i, outside := patch.NormalizeIndex(k.GetIndex(), len(c.arr))
		if outside {
			return loc{noSlot: true}, nil
		}
		return loc{idx: i}, nil
	}

	var (
		name string
		err  error
	)
	switch k.WhichKind() {
	case patchpb.Key_Field_case:
		name, err = patch.MemberName(k.GetField(), at.Sub("field"))
	case patchpb.Key_MapKey_case:
		name, err = patch.MemberKey(k.GetMapKey(), at.Sub("map_key"))
	default:
		return loc{}, patch.Errf(patch.CodeIllegalArm, at,
			"an object takes a field or a map key, not an index")
	}
	if err != nil {
		return loc{}, err
	}

	_, present := c.obj[name]
	return loc{key: name, emptySlot: !present}, nil
}
