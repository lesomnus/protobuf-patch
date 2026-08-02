package jsonpatch

import (
	"encoding/json"
	"fmt"
	"strconv"

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
//
// It goes through protojson rather than reading the JSON directly, so that
// base64 bytes, enum names, 64-bit integers written as strings, and the
// well-known types all mean here what they mean everywhere else in protobuf.
func valueAt(c cursor, s seg, raw json.RawMessage) (*patchpb.Value, error) {
	var (
		holder protoreflect.MessageDescriptor
		fd     protoreflect.FieldDescriptor
		site   patch.Site
	)
	switch {
	case s.field != nil:
		holder, fd, site = c.msg, s.field, patch.SiteField
	case s.list:
		holder, fd, site = c.fd.ContainingMessage(), c.fd, patch.SiteElement
	default:
		holder, fd, site = c.fd.ContainingMessage(), c.fd, patch.SiteMapValue
	}

	// Wrap the value so protojson parses it in the shape the field expects.
	var wrapped []byte
	switch site {
	case patch.SiteElement:
		wrapped = []byte(fmt.Sprintf(`{%q:[%s]}`, fd.JSONName(), raw))
	case patch.SiteMapValue:
		// ProtoJSON always writes a map key as a JSON string, but it must
		// still parse as the declared key type.
		wrapped = []byte(fmt.Sprintf(`{%q:{%q:%s}}`, fd.JSONName(), placeholderKey(fd), raw))
	default:
		wrapped = []byte(fmt.Sprintf(`{%q:%s}`, fd.JSONName(), raw))
	}

	holderMsg := dynamicpb.NewMessage(holder)
	if err := (protojson.UnmarshalOptions{}).Unmarshal(wrapped, holderMsg); err != nil {
		return nil, fmt.Errorf("value for %s: %w", fd.FullName(), err)
	}

	got := holderMsg.Get(fd)
	switch site {
	case patch.SiteElement:
		if got.List().Len() != 1 {
			return nil, fmt.Errorf("value for %s did not parse as one element", fd.FullName())
		}
		got = got.List().Get(0)
	case patch.SiteMapValue:
		var found protoreflect.Value
		got.Map().Range(func(_ protoreflect.MapKey, v protoreflect.Value) bool {
			found = v
			return false
		})
		if !found.IsValid() {
			return nil, fmt.Errorf("value for %s did not parse", fd.FullName())
		}
		got = found
	}

	return patch.ValueOf(got, fd, site, "")
}

// placeholderKey is a key of the map's declared type, used only to give
// protojson a well-formed entry to parse the VALUE out of.
func placeholderKey(fd protoreflect.FieldDescriptor) string {
	switch fd.MapKey().Kind() {
	case protoreflect.StringKind:
		return "k"
	case protoreflect.BoolKind:
		return "false"
	default:
		return "0"
	}
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
