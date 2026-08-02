package jsonpatch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Convert turns an RFC 6902 JSON Patch document into a patch.Patch for
// messages of type md.
//
// A descriptor is required, and not merely to fill in message_type. JSON Patch
// is untyped: a pointer segment "3" is a list index, a map key, or a field
// name depending on what it addresses, and a JSON number is an int32, a
// uint64, or a double depending on the field it lands on. The patch format
// asks for exactly one arm per position, so the types have to come from
// somewhere, and the descriptor is the only place they exist.
//
// The conversion is TOTAL over valid RFC 6902 documents that address the given
// message: every operation, including move and copy across containers and
// "add" at the end of an array, has an encoding.
//
// Mappings worth knowing:
//
//	add on an object member  -> assign  (RFC 6902 replaces an existing member,
//	                                     and insert would refuse one)
//	add on an array index    -> insert  (RFC 6902 inserts before it)
//	add with the "-" token   -> insert at Selector.append
//	replace                  -> assign
//	test                     -> test    (never dropped)
//	a JSON null              -> remove  (the format has no null, and clearing
//	                                     is what a null write meant)
func Convert(doc Doc, md protoreflect.MessageDescriptor) (*patchpb.Patch, error) {
	if md == nil {
		return nil, fmt.Errorf("jsonpatch: a target descriptor is required")
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("jsonpatch: an empty document has no encoding; a Patch must carry at least one entry")
	}

	entries := make([]*patchpb.Entry, 0, len(doc))
	for i, op := range doc {
		e, err := convertOp(op, md)
		if err != nil {
			return nil, fmt.Errorf("jsonpatch: doc[%d] %q %q: %w", i, op.Op, op.Path, err)
		}
		entries = append(entries, e)
	}

	name := string(md.FullName())
	return patchpb.Patch_builder{
		MessageType: &name,
		Delta:       patchpb.Delta_builder{Entries: entries}.Build(),
	}.Build(), nil
}

func convertOp(op Op, md protoreflect.MessageDescriptor) (*patchpb.Entry, error) {
	segs := split(op.Path)
	if len(segs) == 0 {
		return nil, fmt.Errorf("the whole document is not addressable; use a path")
	}

	parent, last, err := walk(md, segs)
	if err != nil {
		return nil, err
	}

	b := patchpb.Entry_builder{}
	if len(segs) > 1 {
		p, err := pathOf(md, segs[:len(segs)-1])
		if err != nil {
			return nil, err
		}
		b.Path = p
	}

	sel, err := selectorOf(parent, last)
	if err != nil {
		return nil, err
	}
	b.Targets = patchpb.Targets_builder{Selectors: []*patchpb.Selector{sel}}.Build()

	switch op.Op {
	case "remove":
		b.Remove = &patchpb.Remove{}

	case "add", "replace", "test":
		if isJSONNull(op.Value) {
			// The format has no null. Writing one meant "clear", which is what
			// remove spells; asserting one meant "absent", which is exists.
			if op.Op == "test" {
				b.Test = patchpb.Test_builder{Exists: proto.Bool(false)}.Build()
			} else {
				b.Remove = &patchpb.Remove{}
			}
			break
		}
		v, err := valueAt(parent, last, op.Value)
		if err != nil {
			return nil, err
		}
		switch {
		case op.Op == "test":
			b.Test = patchpb.Test_builder{Value: v}.Build()
		case op.Op == "add" && last.isIndex():
			// RFC 6902 add on an array index inserts before it.
			b.Insert = patchpb.Insert_builder{Value: v}.Build()
		default:
			// RFC 6902 add on an object member replaces an existing one, which
			// is assign. insert would refuse it.
			b.Assign = patchpb.Assign_builder{Value: v}.Build()
		}

	case "move", "copy":
		from := split(op.From)
		if len(from) == 0 {
			return nil, fmt.Errorf("%q must name a source", op.Op)
		}
		loc, err := locationOf(md, from)
		if err != nil {
			return nil, fmt.Errorf("from %q: %w", op.From, err)
		}
		if op.Op == "move" {
			b.Move = patchpb.Move_builder{From: loc}.Build()
		} else {
			b.Copy = patchpb.Copy_builder{From: loc}.Build()
		}

	default:
		return nil, fmt.Errorf("unknown op")
	}

	return b.Build(), nil
}

// seg is one JSON Pointer segment resolved against the container it addresses.
type seg struct {
	raw string

	// exactly one of these describes what the segment names
	field protoreflect.FieldDescriptor // a message field
	index int64                        // a list element
	list  bool
	appnd bool            // the "-" token
	key   *patchpb.MapKey // a map entry
}

func (s seg) isIndex() bool { return s.list }

// cursor is the container a segment is resolved against.
type cursor struct {
	msg protoreflect.MessageDescriptor
	fd  protoreflect.FieldDescriptor // set when the container is a list or map
	// kind of container
	isList bool
	isMap  bool
}

// walk resolves every segment, returning the container that holds the last one.
func walk(md protoreflect.MessageDescriptor, segs []string) (cursor, seg, error) {
	c := cursor{msg: md}
	var last seg
	for i, raw := range segs {
		s, err := resolve(c, raw)
		if err != nil {
			return cursor{}, seg{}, fmt.Errorf("segment %d (%q): %w", i, raw, err)
		}
		if i == len(segs)-1 {
			return c, s, nil
		}
		next, err := descend(c, s)
		if err != nil {
			return cursor{}, seg{}, fmt.Errorf("segment %d (%q): %w", i, raw, err)
		}
		c = next
		last = s
	}
	return c, last, nil
}

func resolve(c cursor, raw string) (seg, error) {
	switch {
	case c.isList:
		if raw == "-" {
			return seg{raw: raw, list: true, appnd: true}, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return seg{}, fmt.Errorf("a list takes an index")
		}
		return seg{raw: raw, list: true, index: n}, nil

	case c.isMap:
		k, err := mapKeyFromString(raw, c.fd)
		if err != nil {
			return seg{}, err
		}
		return seg{raw: raw, key: k}, nil

	default:
		fd := c.msg.Fields().ByJSONName(raw)
		if fd == nil {
			fd = c.msg.Fields().ByName(protoreflect.Name(raw))
		}
		if fd == nil {
			return seg{}, fmt.Errorf("%s declares no such field", c.msg.FullName())
		}
		return seg{raw: raw, field: fd}, nil
	}
}

func descend(c cursor, s seg) (cursor, error) {
	switch {
	case s.field != nil:
		fd := s.field
		switch {
		case fd.IsMap():
			return cursor{fd: fd, isMap: true}, nil
		case fd.IsList():
			return cursor{fd: fd, isList: true}, nil
		case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
			return cursor{msg: fd.Message()}, nil
		}
		return cursor{}, fmt.Errorf("%s is a %v, not a container", fd.FullName(), fd.Kind())

	case s.list:
		if c.fd.Kind() != protoreflect.MessageKind && c.fd.Kind() != protoreflect.GroupKind {
			return cursor{}, fmt.Errorf("elements of %s are not containers", c.fd.FullName())
		}
		return cursor{msg: c.fd.Message()}, nil

	default:
		if c.fd.MapValue().Kind() != protoreflect.MessageKind && c.fd.MapValue().Kind() != protoreflect.GroupKind {
			return cursor{}, fmt.Errorf("values of %s are not containers", c.fd.FullName())
		}
		return cursor{msg: c.fd.MapValue().Message()}, nil
	}
}

func keyOf(s seg) (*patchpb.Key, error) {
	switch {
	case s.field != nil:
		n := uint32(s.field.Number())
		return patchpb.Key_builder{
			Field: patchpb.Field_builder{Number: &n}.Build(),
		}.Build(), nil
	case s.list:
		if s.appnd {
			return nil, fmt.Errorf(`the "-" token names no existing element`)
		}
		i := s.index
		return patchpb.Key_builder{Index: &i}.Build(), nil
	default:
		return patchpb.Key_builder{MapKey: s.key}.Build(), nil
	}
}

func selectorOf(c cursor, s seg) (*patchpb.Selector, error) {
	if s.appnd {
		return patchpb.Selector_builder{Append: &patchpb.Append{}}.Build(), nil
	}
	k, err := keyOf(s)
	if err != nil {
		return nil, err
	}
	return patchpb.Selector_builder{Key: k}.Build(), nil
}

func pathOf(md protoreflect.MessageDescriptor, segs []string) (*patchpb.Path, error) {
	c := cursor{msg: md}
	keys := make([]*patchpb.Key, 0, len(segs))
	for i, raw := range segs {
		s, err := resolve(c, raw)
		if err != nil {
			return nil, fmt.Errorf("segment %d (%q): %w", i, raw, err)
		}
		k, err := keyOf(s)
		if err != nil {
			return nil, fmt.Errorf("segment %d (%q): %w", i, raw, err)
		}
		keys = append(keys, k)
		if c, err = descend(c, s); err != nil {
			return nil, fmt.Errorf("segment %d (%q): %w", i, raw, err)
		}
	}
	return patchpb.Path_builder{Segments: keys}.Build(), nil
}

// locationOf builds the source of a move or copy. Because Location carries its
// own path, a cross-container relocation -- which the previous implementation
// had to reject -- is expressible.
func locationOf(md protoreflect.MessageDescriptor, segs []string) (*patchpb.Location, error) {
	parent, last, err := walk(md, segs)
	if err != nil {
		return nil, err
	}
	k, err := keyOf(last)
	if err != nil {
		return nil, err
	}
	p, err := pathOf(md, segs[:len(segs)-1])
	if err != nil {
		return nil, err
	}
	_ = parent
	return patchpb.Location_builder{Path: p, Key: k}.Build(), nil
}

// valueAt converts a JSON value for the position the last segment names.
func valueAt(c cursor, s seg, raw json.RawMessage) (*patchpb.Value, error) {
	switch {
	case s.field != nil:
		return parseValue(raw, s.field, patch.SiteField)
	case s.list:
		return parseValue(raw, c.fd, patch.SiteElement)
	default:
		return parseValue(raw, c.fd, patch.SiteMapValue)
	}
}

// parseValue reads a JSON value as ProtoJSON defines it for fd at site.
//
// It parses against the FIELD's own type, never against a holder message. An
// earlier version wrapped the value as {"fieldName": raw} and handed that to
// protojson, which is wrong whenever the holder is a well-known type: protojson
// applies the WKT's custom JSON form to the wrapper, so patching inside a
// google.protobuf.Struct silently produced a zero instead of the value written.
// Message values still go through protojson, but against their own descriptor,
// where the WKT form is the correct one to apply.
func parseValue(raw json.RawMessage, fd protoreflect.FieldDescriptor, site patch.Site) (*patchpb.Value, error) {
	if site == patch.SiteField && fd.IsList() {
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return nil, fmt.Errorf("%s takes an array: %w", fd.FullName(), err)
		}
		vs := make([]*patchpb.Value, 0, len(elems))
		for i, e := range elems {
			v, err := parseValue(e, fd, patch.SiteElement)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			vs = append(vs, v)
		}
		return patchpb.Value_builder{
			L: patchpb.ListValue_builder{Values: vs}.Build(),
		}.Build(), nil
	}

	if site == patch.SiteField && fd.IsMap() {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			return nil, fmt.Errorf("%s takes an object: %w", fd.FullName(), err)
		}
		keys := make([]string, 0, len(members))
		for k := range members {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		entries := make([]*patchpb.MapEntry, 0, len(members))
		for _, k := range keys {
			mk, err := mapKeyFromString(k, fd)
			if err != nil {
				return nil, err
			}
			v, err := parseValue(members[k], fd, patch.SiteMapValue)
			if err != nil {
				return nil, fmt.Errorf("[%q]: %w", k, err)
			}
			entries = append(entries, patchpb.MapEntry_builder{Key: mk, Value: v}.Build())
		}
		return patchpb.Value_builder{
			Map: patchpb.MapValue_builder{Entries: entries}.Build(),
		}.Build(), nil
	}

	d := fd
	if site == patch.SiteMapValue {
		d = fd.MapValue()
	}
	if d.Kind() == protoreflect.MessageKind || d.Kind() == protoreflect.GroupKind {
		m := dynamicpb.NewMessage(d.Message())
		if err := (protojson.UnmarshalOptions{}).Unmarshal(raw, m); err != nil {
			return nil, fmt.Errorf("%s: %w", d.Message().FullName(), err)
		}
		return patch.ValueOf(protoreflect.ValueOfMessage(m), fd, site, "")
	}
	return parseScalar(raw, d)
}

// parseScalar implements ProtoJSON's scalar forms: 64-bit integers may be
// written as strings, floats accept "NaN"/"Infinity"/"-Infinity", bytes are
// base64, and an enum is a value name or a number.
func parseScalar(raw json.RawMessage, d protoreflect.FieldDescriptor) (*patchpb.Value, error) {
	b := patchpb.Value_builder{}
	kind := d.Kind()

	switch kind {
	case protoreflect.BoolKind:
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s takes a bool: %w", d.FullName(), err)
		}
		b.B = &v

	case protoreflect.StringKind:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s takes a string: %w", d.FullName(), err)
		}
		b.S = &v

	case protoreflect.BytesKind:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s takes base64: %w", d.FullName(), err)
		}
		x, err := decodeBase64(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.FullName(), err)
		}
		b.X = x

	case protoreflect.EnumKind:
		n, err := parseEnum(raw, d.Enum())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.FullName(), err)
		}
		b.E = &n

	case protoreflect.FloatKind, protoreflect.DoubleKind:
		f, err := parseFloat(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.FullName(), err)
		}
		if kind == protoreflect.FloatKind {
			v := float32(f)
			b.F32 = &v
		} else {
			b.F64 = &f
		}

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := parseInt(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.FullName(), err)
		}
		if kind == protoreflect.Int32Kind || kind == protoreflect.Sint32Kind || kind == protoreflect.Sfixed32Kind {
			if n < math.MinInt32 || n > math.MaxInt32 {
				return nil, fmt.Errorf("%s: %d is outside the range of %v", d.FullName(), n, kind)
			}
			v := int32(n)
			b.I32 = &v
		} else {
			b.I64 = &n
		}

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := parseUint(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.FullName(), err)
		}
		if kind == protoreflect.Uint32Kind || kind == protoreflect.Fixed32Kind {
			if n > math.MaxUint32 {
				return nil, fmt.Errorf("%s: %d is outside the range of %v", d.FullName(), n, kind)
			}
			v := uint32(n)
			b.U32 = &v
		} else {
			b.U64 = &n
		}

	default:
		return nil, fmt.Errorf("%s is a %v, which has no JSON form here", d.FullName(), kind)
	}
	return b.Build(), nil
}

func unquoted(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func parseInt(raw json.RawMessage) (int64, error) {
	if s, ok := unquoted(raw); ok {
		return strconv.ParseInt(s, 10, 64)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("takes an integer, got %s", raw)
	}
	return n, nil
}

func parseUint(raw json.RawMessage) (uint64, error) {
	if s, ok := unquoted(raw); ok {
		return strconv.ParseUint(s, 10, 64)
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("takes an unsigned integer, got %s", raw)
	}
	return n, nil
}

func parseFloat(raw json.RawMessage) (float64, error) {
	if s, ok := unquoted(raw); ok {
		switch s {
		case "NaN":
			return math.NaN(), nil
		case "Infinity":
			return math.Inf(1), nil
		case "-Infinity":
			return math.Inf(-1), nil
		}
		return strconv.ParseFloat(s, 64)
	}
	return strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
}

func parseEnum(raw json.RawMessage, ed protoreflect.EnumDescriptor) (int32, error) {
	if s, ok := unquoted(raw); ok {
		if ed == nil {
			return 0, fmt.Errorf("no enum type")
		}
		v := ed.Values().ByName(protoreflect.Name(s))
		if v == nil {
			return 0, fmt.Errorf("%s declares no value named %q", ed.FullName(), s)
		}
		return int32(v.Number()), nil
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("takes a value name or a number, got %s", raw)
	}
	return int32(n), nil
}

// decodeBase64 accepts both alphabets, with or without padding, as ProtoJSON
// requires.
func decodeBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not base64")
}

func mapKeyFromString(raw string, fd protoreflect.FieldDescriptor) (*patchpb.MapKey, error) {
	b := patchpb.MapKey_builder{}
	switch fd.MapKey().Kind() {
	case protoreflect.StringKind:
		s := raw
		b.S = &s
	case protoreflect.BoolKind:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s has bool keys", fd.FullName())
		}
		b.B = &v
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s has %v keys", fd.FullName(), fd.MapKey().Kind())
		}
		b.I = &v
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s has %v keys", fd.FullName(), fd.MapKey().Kind())
		}
		b.U = &v
	default:
		return nil, fmt.Errorf("%v is not a legal map key type", fd.MapKey().Kind())
	}
	return b.Build(), nil
}

func split(p string) []string {
	var out []string
	for s := range Location(p).Seq() {
		out = append(out, s)
	}
	return out
}

func isJSONNull(raw json.RawMessage) bool {
	s := string(raw)
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	return s == "null"
}
