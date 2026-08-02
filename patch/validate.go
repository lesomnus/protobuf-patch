package patch

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Validate reports whether p satisfies every rule of the format that can be
// decided from the document alone, without a target message.
//
// It returns the first violation it finds. The remaining rules — that a key
// resolves, that a Value's arm suits the field it lands on, that a test holds
// — need a descriptor or an instance and belong to the applier.
//
// An applier MUST call Validate (or perform equivalent checks) before touching
// the target. In particular the unknown-field scan below is what actually
// enforces the schema's forward-compatibility rule, so skipping it turns a
// document from a newer revision into a silently partial application.
//
// Validate uses DefaultLimits; ValidateWith takes its own.
func Validate(p *patchpb.Patch) error {
	return ValidateWith(p, DefaultLimits)
}

// ValidateWith is Validate under caller-chosen Limits. A zero field falls back
// to the corresponding DefaultLimits value.
func ValidateWith(p *patchpb.Patch, lim Limits) error {
	if p == nil {
		return Errf(CodeMissingField, "", "nil Patch")
	}

	// Depth before anything else, including the unknown-field scan: every
	// other pass walks the whole document, so each of them is unsafe on a
	// document deep enough to exhaust memory or stack. See checkDepth.
	if err := checkDepth(p, lim.orDefault()); err != nil {
		return err
	}

	// Unknown fields next: until this passes, nothing else observed about the
	// document can be trusted to mean what it appears to mean.
	if at, ok := findUnknown(p.ProtoReflect(), ""); ok {
		return Errf(CodeUnknownField, at,
			"a field from a newer revision, or corrupt input; refusing rather than applying the part that is understood")
	}

	if rev := p.GetMinReaderRevision(); rev > Revision {
		return Errf(CodeReaderRevisionTooOld, "min_reader_revision",
			"document requires revision %d, this build implements %d", rev, Revision)
	}
	if !p.HasDelta() {
		return Errf(CodeMissingField, "delta", "required")
	}
	return validateDelta(p.GetDelta(), At("delta"))
}

// findUnknown reports the position of the first message in m's tree that
// carries unknown fields.
//
// This is the only way to tell an unrecognized oneof arm from an unset one:
// protobuf reports both as "not set", and only the unknown-field set
// distinguishes "the producer omitted it" from "the producer used an arm this
// build does not know".
func findUnknown(m protoreflect.Message, at At) (At, bool) {
	if len(m.GetUnknown()) > 0 {
		return at, true
	}

	var found At
	ok := false
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		name := string(fd.Name())
		switch {
		case fd.IsMap():
			// The patch schema declares no map fields; handled so that this
			// stays correct if one is ever added.
			if fd.MapValue().Kind() != protoreflect.MessageKind {
				return true
			}
			v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
				if a, f := findUnknown(mv.Message(), at.Sub(name)); f {
					found, ok = a, true
				}
				return !ok
			})
		case fd.IsList():
			if fd.Kind() != protoreflect.MessageKind && fd.Kind() != protoreflect.GroupKind {
				return true
			}
			l := v.List()
			for i := range l.Len() {
				if a, f := findUnknown(l.Get(i).Message(), at.Index(name, i)); f {
					found, ok = a, true
					break
				}
			}
		case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
			if a, f := findUnknown(v.Message(), at.Sub(name)); f {
				found, ok = a, true
			}
		}
		return !ok
	})
	return found, ok
}

func validateDelta(d *patchpb.Delta, at At) error {
	if d == nil || len(d.GetEntries()) == 0 {
		return Errf(CodeEmptyCollection, at.Sub("entries"),
			"a Delta must carry at least one entry; a document that does nothing is a test that holds")
	}
	for i, e := range d.GetEntries() {
		if err := validateEntry(e, at.Index("entries", i)); err != nil {
			return err
		}
	}
	return nil
}

func validateEntry(e *patchpb.Entry, at At) error {
	if e == nil {
		return Errf(CodeMissingField, at, "nil Entry")
	}

	if e.HasPath() {
		for i, k := range e.GetPath().GetSegments() {
			if err := validateKey(k, at.Sub("path").Index("segments", i)); err != nil {
				return err
			}
		}
	}

	kind := e.WhichKind()
	if kind == patchpb.Entry_Kind_not_set_case {
		return Errf(CodeMissingOneof, at.Sub("kind"), "an entry must carry exactly one operation")
	}

	switch e.WhichScope() {
	case patchpb.Entry_Targets_case:
		sels := e.GetTargets().GetSelectors()
		if len(sels) == 0 {
			return Errf(CodeEmptyCollection, at.Sub("targets").Sub("selectors"),
				"address the container with `container` instead; a producer that computed zero targets has a bug")
		}
		for i, s := range sels {
			if err := validateSelector(s, kind, at.Sub("targets").Index("selectors", i)); err != nil {
				return err
			}
		}
	case patchpb.Entry_Container_case:
		if kind == patchpb.Entry_Move_case || kind == patchpb.Entry_Copy_case {
			return Errf(CodeIllegalScope, at, "%v onto a container is not defined", kindName(kind))
		}
	default:
		return Errf(CodeMissingOneof, at.Sub("scope"),
			"an entry must say what it applies to; root behavior is not derived from an empty target list")
	}

	switch om := e.GetOnMissing(); om {
	case patchpb.OnMissing_ON_MISSING_UNSPECIFIED, patchpb.OnMissing_ON_MISSING_SKIP:
	default:
		return Errf(CodeUnrecognizedEnum, at.Sub("on_missing"), "OnMissing(%d)", int32(om))
	}
	if kind == patchpb.Entry_Test_case && e.GetOnMissing() != patchpb.OnMissing_ON_MISSING_UNSPECIFIED {
		return Errf(CodeTestNotStrict, at.Sub("on_missing"),
			"a test reads a missing target rather than skipping it; use exists=false to assert absence")
	}

	switch oap := e.GetOnAbsentPath(); oap {
	case patchpb.OnAbsentPath_ON_ABSENT_PATH_UNSPECIFIED, patchpb.OnAbsentPath_ON_ABSENT_PATH_CREATE:
	default:
		return Errf(CodeUnrecognizedEnum, at.Sub("on_absent_path"), "OnAbsentPath(%d)", int32(oap))
	}
	if kind == patchpb.Entry_Test_case && e.GetOnAbsentPath() != patchpb.OnAbsentPath_ON_ABSENT_PATH_UNSPECIFIED {
		return Errf(CodeTestNotStrict, at.Sub("on_absent_path"),
			"creating a container is a mutation, and a test must not mutate")
	}

	return validateKindPayload(e, kind, at)
}

func validateKindPayload(e *patchpb.Entry, kind any, at At) error {
	switch kind {
	case patchpb.Entry_Remove_case:
		return nil

	case patchpb.Entry_Test_case:
		t := e.GetTest()
		if t.WhichWant() == patchpb.Test_Want_not_set_case {
			return Errf(CodeMissingOneof, at.Sub("test").Sub("want"),
				"an unset want is an error, never a passing assertion")
		}
		if t.WhichWant() == patchpb.Test_Value_case {
			return validateValue(t.GetValue(), at.Sub("test").Sub("value"))
		}
		return nil

	case patchpb.Entry_Insert_case:
		if !e.GetInsert().HasValue() {
			return Errf(CodeMissingField, at.Sub("insert").Sub("value"), "required")
		}
		return validateValue(e.GetInsert().GetValue(), at.Sub("insert").Sub("value"))

	case patchpb.Entry_Assign_case:
		if !e.GetAssign().HasValue() {
			return Errf(CodeMissingField, at.Sub("assign").Sub("value"), "required")
		}
		return validateValue(e.GetAssign().GetValue(), at.Sub("assign").Sub("value"))

	case patchpb.Entry_Move_case:
		return validateLocation(e.GetMove().GetFrom(), at.Sub("move").Sub("from"))

	case patchpb.Entry_Copy_case:
		return validateLocation(e.GetCopy().GetFrom(), at.Sub("copy").Sub("from"))

	case patchpb.Entry_Nest_case:
		if !e.GetNest().HasDelta() {
			return Errf(CodeMissingField, at.Sub("nest").Sub("delta"), "required")
		}
		return validateDelta(e.GetNest().GetDelta(), at.Sub("nest").Sub("delta"))
	}
	return nil
}

func validateLocation(l *patchpb.Location, at At) error {
	if l == nil {
		return Errf(CodeMissingField, at, "required")
	}
	switch l.WhichOrigin() {
	case patchpb.Location_Path_case:
		for i, k := range l.GetPath().GetSegments() {
			if err := validateKey(k, at.Sub("path").Index("segments", i)); err != nil {
				return err
			}
		}
	case patchpb.Location_SameContainer_case:
	default:
		return Errf(CodeMissingOneof, at.Sub("origin"),
			"a source must say which container it lives in; \"the entry's own\" is a choice, not an absence")
	}
	if !l.HasKey() {
		return Errf(CodeMissingField, at.Sub("key"), "required")
	}
	return validateKey(l.GetKey(), at.Sub("key"))
}

func validateSelector(s *patchpb.Selector, kind any, at At) error {
	if s == nil {
		return Errf(CodeMissingField, at, "nil Selector")
	}
	switch s.WhichKind() {
	case patchpb.Selector_Key_case:
		return validateKey(s.GetKey(), at.Sub("key"))

	case patchpb.Selector_Range_case:
		return nil

	case patchpb.Selector_EveryEntry_case:
		return nil

	case patchpb.Selector_OneofMember_case:
		if s.GetOneofMember().GetName() == "" {
			return Errf(CodeFieldNoIdentifier, at.Sub("oneof_member").Sub("name"),
				"the empty string is not a oneof name")
		}
		return nil

	case patchpb.Selector_Append_case:
		switch kind {
		case patchpb.Entry_Insert_case, patchpb.Entry_Move_case, patchpb.Entry_Copy_case:
			return nil
		}
		return Errf(CodeIllegalSelector, at.Sub("append"),
			"%v cannot grow a list", kindName(kind))

	default:
		return Errf(CodeMissingOneof, at.Sub("kind"), "a selector must name something")
	}
}

func validateKey(k *patchpb.Key, at At) error {
	if k == nil {
		return Errf(CodeMissingField, at, "nil Key")
	}
	switch k.WhichKind() {
	case patchpb.Key_Field_case:
		return validateField(k.GetField(), at.Sub("field"))
	case patchpb.Key_Index_case:
		return nil
	case patchpb.Key_MapKey_case:
		if k.GetMapKey().WhichKind() == patchpb.MapKey_Kind_not_set_case {
			return Errf(CodeMissingOneof, at.Sub("map_key").Sub("kind"), "a map key must carry a value")
		}
		return nil
	default:
		return Errf(CodeMissingOneof, at.Sub("kind"), "a key must name a location")
	}
}

func validateField(f *patchpb.Field, at At) error {
	if f == nil {
		return Errf(CodeMissingField, at, "nil Field")
	}
	if f.HasName() && f.GetName() == "" {
		return Errf(CodeFieldNoIdentifier, at.Sub("name"), "the empty string is not a field name")
	}
	if f.HasJsonName() && f.GetJsonName() == "" {
		return Errf(CodeFieldNoIdentifier, at.Sub("json_name"), "the empty string is not a field name")
	}
	if f.HasNumber() && f.GetNumber() == 0 {
		return Errf(CodeFieldNoIdentifier, at.Sub("number"), "0 is not a legal field number")
	}
	if !f.HasName() && !f.HasJsonName() && !f.HasNumber() {
		return Errf(CodeFieldNoIdentifier, at, "at least one of name, json_name, or number is required")
	}
	return nil
}

func validateValue(v *patchpb.Value, at At) error {
	if v == nil {
		return Errf(CodeMissingField, at, "required")
	}
	switch v.WhichKind() {
	case patchpb.Value_M_case:
		for i, fv := range v.GetM().GetFields() {
			fat := at.Sub("m").Index("fields", i)
			if !fv.HasKey() {
				return Errf(CodeMissingField, fat.Sub("key"), "required")
			}
			if err := validateField(fv.GetKey(), fat.Sub("key")); err != nil {
				return err
			}
			if !fv.HasValue() {
				return Errf(CodeMissingField, fat.Sub("value"), "required; use remove to clear a field")
			}
			if err := validateValue(fv.GetValue(), fat.Sub("value")); err != nil {
				return err
			}
		}
		return nil

	case patchpb.Value_L_case:
		for i, ev := range v.GetL().GetValues() {
			if err := validateValue(ev, at.Sub("l").Index("values", i)); err != nil {
				return err
			}
		}
		return nil

	case patchpb.Value_Map_case:
		for i, me := range v.GetMap().GetEntries() {
			mat := at.Sub("map").Index("entries", i)
			if !me.HasKey() {
				return Errf(CodeMissingField, mat.Sub("key"), "required")
			}
			if me.GetKey().WhichKind() == patchpb.MapKey_Kind_not_set_case {
				return Errf(CodeMissingOneof, mat.Sub("key").Sub("kind"), "a map key must carry a value")
			}
			if !me.HasValue() {
				return Errf(CodeMissingField, mat.Sub("value"), "required; use remove to delete an entry")
			}
			if err := validateValue(me.GetValue(), mat.Sub("value")); err != nil {
				return err
			}
		}
		return nil

	case patchpb.Value_Kind_not_set_case:
		return Errf(CodeMissingOneof, at.Sub("kind"),
			"an unset kind is an error, not an implicit clear; use remove to clear")

	default:
		return nil
	}
}

func kindName(kind any) string {
	switch kind {
	case patchpb.Entry_Remove_case:
		return "remove"
	case patchpb.Entry_Test_case:
		return "test"
	case patchpb.Entry_Insert_case:
		return "insert"
	case patchpb.Entry_Assign_case:
		return "assign"
	case patchpb.Entry_Move_case:
		return "move"
	case patchpb.Entry_Copy_case:
		return "copy"
	case patchpb.Entry_Nest_case:
		return "nest"
	}
	return "operation"
}
