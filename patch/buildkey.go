package patch

import (
	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Selectorer names zero or more locations inside a container. Every Keyer is
// one, plus Range and Append.
type Selectorer interface {
	selector() (*patchpb.Selector, error)
}

// Keyer names exactly one location inside a container. Field, MapKey, and Key
// implement it, so any of them may be used where the schema expects a Key.
//
// Keyer embeds Selectorer because the schema's Selector is exactly Key plus
// the multi-valued arms: anything that names one location also names "zero or
// more". The converse does not hold, which is why Path takes only a Keyer — a
// path must reach exactly one container.
type Keyer interface {
	Selectorer

	key() (*patchpb.Key, error)
}

// Field identifies a message field by name, ProtoJSON name, and/or number.
//
// Setting more than one is a CONSTRAINT, not a fallback: they must all
// describe the same field, and a disagreement is CodeFieldConflict rather than
// a silent resolution by whichever identifier happened to work. Pinning both a
// name and a number is how a stored Patch refuses to apply to a message whose
// fields were renumbered underneath it.
type Field struct {
	pb  *patchpb.Field
	err error
}

// Name identifies a field by its declared name.
func Name(name string) Field {
	if name == "" {
		return Field{err: Errf(CodeFieldNoIdentifier, "", "empty name")}
	}
	return Field{pb: patchpb.Field_builder{Name: proto.String(name)}.Build()}
}

// JSONName identifies a field by its ProtoJSON name.
func JSONName(name string) Field {
	if name == "" {
		return Field{err: Errf(CodeFieldNoIdentifier, "", "empty json_name")}
	}
	return Field{pb: patchpb.Field_builder{JsonName: proto.String(name)}.Build()}
}

// Num identifies a field by its number. Field numbers start at 1.
func Num(number uint32) Field {
	if number == 0 {
		return Field{err: Errf(CodeFieldNoIdentifier, "", "field number 0 is not legal")}
	}
	return Field{pb: patchpb.Field_builder{Number: proto.Uint32(number)}.Build()}
}

// Name adds a declared-name constraint to f.
func (f Field) Name(name string) Field { return f.with(Name(name)) }

// JSONName adds a ProtoJSON-name constraint to f.
func (f Field) JSONName(name string) Field { return f.with(JSONName(name)) }

// Num adds a field-number constraint to f.
func (f Field) Num(number uint32) Field { return f.with(Num(number)) }

func (f Field) with(g Field) Field {
	if f.err != nil {
		return f
	}
	if g.err != nil {
		return g
	}
	b := patchpb.Field_builder{}
	if f.pb.HasName() {
		b.Name = proto.String(f.pb.GetName())
	}
	if g.pb.HasName() {
		b.Name = proto.String(g.pb.GetName())
	}
	if f.pb.HasJsonName() {
		b.JsonName = proto.String(f.pb.GetJsonName())
	}
	if g.pb.HasJsonName() {
		b.JsonName = proto.String(g.pb.GetJsonName())
	}
	if f.pb.HasNumber() {
		b.Number = proto.Uint32(f.pb.GetNumber())
	}
	if g.pb.HasNumber() {
		b.Number = proto.Uint32(g.pb.GetNumber())
	}
	return Field{pb: b.Build()}
}

func (f Field) key() (*patchpb.Key, error) {
	if f.err != nil {
		return nil, f.err
	}
	return patchpb.Key_builder{Field: f.pb}.Build(), nil
}

func (f Field) selector() (*patchpb.Selector, error) { return keySelector(f) }

// MapKey identifies a map entry. The arm must match the map's declared key
// type; it is never coerced, so a numeric key does not address a string-keyed
// map.
type MapKey struct {
	pb  *patchpb.MapKey
	err error
}

// MapStr addresses an entry of a map with string keys. The empty string is a
// legal key.
func MapStr(k string) MapKey {
	return MapKey{pb: patchpb.MapKey_builder{S: proto.String(k)}.Build()}
}

// MapInt addresses an entry of a map with signed integer keys (int32, sint32,
// sfixed32, int64, sint64, sfixed64).
func MapInt(k int64) MapKey {
	return MapKey{pb: patchpb.MapKey_builder{I: proto.Int64(k)}.Build()}
}

// MapUint addresses an entry of a map with unsigned integer keys (uint32,
// fixed32, uint64, fixed64).
func MapUint(k uint64) MapKey {
	return MapKey{pb: patchpb.MapKey_builder{U: proto.Uint64(k)}.Build()}
}

// MapBool addresses an entry of a map with bool keys.
func MapBool(k bool) MapKey {
	return MapKey{pb: patchpb.MapKey_builder{B: proto.Bool(k)}.Build()}
}

func (m MapKey) key() (*patchpb.Key, error) {
	if m.err != nil {
		return nil, m.err
	}
	return patchpb.Key_builder{MapKey: m.pb}.Build(), nil
}

func (m MapKey) selector() (*patchpb.Selector, error) { return keySelector(m) }

// Key names one location. Index produces one directly; Field and MapKey
// convert to one implicitly.
type Key struct {
	pb  *patchpb.Key
	err error
}

// Index addresses a list element. Negative values count from the end, in every
// operation alike — the effective index is len+i. To address the position one
// past the last element, use Append.
func Index(i int64) Key {
	return Key{pb: patchpb.Key_builder{Index: proto.Int64(i)}.Build()}
}

func (k Key) key() (*patchpb.Key, error) {
	if k.err != nil {
		return nil, k.err
	}
	return k.pb, nil
}

func (k Key) selector() (*patchpb.Selector, error) { return keySelector(k) }

func keySelector(k Keyer) (*patchpb.Selector, error) {
	pb, err := k.key()
	if err != nil {
		return nil, err
	}
	return patchpb.Selector_builder{Key: pb}.Build(), nil
}

// Selector names zero or more locations. Range and Append produce one; every
// Keyer converts to one implicitly.
type Selector struct {
	pb  *patchpb.Selector
	err error

	// appendArm records that this selector is Append, which only insert, move,
	// and copy accept.
	appendArm bool
}

func (s Selector) selector() (*patchpb.Selector, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.pb, nil
}

// Span selects the list elements in [begin, end). Negative bounds count from
// the end; bounds are clamped; begin >= end selects nothing, which is a
// defined answer rather than a failure.
//
// Use SpanFrom or SpanTo to leave a side open — an unset bound means 0 or the
// collection length respectively, which is not the same as passing 0.
func Span(begin, end int64) Selector {
	return rangeSelector(patchpb.Range_builder{
		Begin: proto.Int64(begin),
		End:   proto.Int64(end),
	}.Build())
}

// SpanFrom selects the list elements from begin to the end of the list.
func SpanFrom(begin int64) Selector {
	return rangeSelector(patchpb.Range_builder{Begin: proto.Int64(begin)}.Build())
}

// SpanTo selects the list elements from the start of the list up to end.
func SpanTo(end int64) Selector {
	return rangeSelector(patchpb.Range_builder{End: proto.Int64(end)}.Build())
}

// SpanAll selects every element of a list.
func SpanAll() Selector {
	return rangeSelector(patchpb.Range_builder{}.Build())
}

func rangeSelector(r *patchpb.Range) Selector {
	return Selector{pb: patchpb.Selector_builder{Range: r}.Build()}
}

// Append selects the position one past the last element of a list — the RFC
// 6901 "-" token. Only insert, move, and copy accept it; the operations that
// cannot grow a list reject it.
func Append() Selector {
	return Selector{
		pb:        patchpb.Selector_builder{Append: &patchpb.Append{}}.Build(),
		appendArm: true,
	}
}

// Location names the source of a move or copy.
type Location struct {
	pb  *patchpb.Location
	err error
}

// Here names a source in the same container as the entry's targets.
func Here(k Keyer) Location {
	pb, err := k.key()
	if err != nil {
		return Location{err: err}
	}
	return Location{pb: patchpb.Location_builder{
		SameContainer: &patchpb.SameContainer{},
		Key:           pb,
	}.Build()}
}

// From names the source at k inside the container reached by path, where path
// is relative to the enclosing Delta's container. An empty path names that
// container itself.
//
// This is what makes a cross-container move or copy expressible: the source
// need not live where the targets do.
func From(k Keyer, path ...Keyer) Location {
	pb, err := k.key()
	if err != nil {
		return Location{err: err}
	}
	p, err := buildPath(path)
	if err != nil {
		return Location{err: err}
	}
	return Location{pb: patchpb.Location_builder{Path: p, Key: pb}.Build()}
}

func buildPath(keys []Keyer) (*patchpb.Path, error) {
	segs := make([]*patchpb.Key, 0, len(keys))
	for _, k := range keys {
		pb, err := k.key()
		if err != nil {
			return nil, err
		}
		segs = append(segs, pb)
	}
	return patchpb.Path_builder{Segments: segs}.Build(), nil
}
