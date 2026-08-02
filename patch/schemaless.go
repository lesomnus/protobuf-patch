package patch

import (
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// This file holds the rules a consumer can apply with NO descriptor at all —
// one that patches a schema-less document such as arbitrary JSON.
//
// Such a consumer cannot honor the whole format. It must therefore REFUSE the
// constructs it cannot interpret rather than guess at them, which is what
// these functions do: they return an error for anything whose meaning is a
// property of a protobuf schema.
//
// A Patch does NOT mean the same thing to a schema-less consumer as it does to
// patchproto. It cannot: the format distinguishes a message from a map, an
// int32 from an int64, and a declared field from an undeclared one, and none
// of those distinctions survive in a document that has no schema. What these
// rules guarantee is narrower and still worth having — that two schema-less
// consumers agree with each other, and that neither silently does something
// the document did not ask for.

// MemberName returns the member a Field addresses in a schema-less document.
//
// Only name and json_name can mean anything without a schema, and if both are
// set they must agree, since Field is a constraint rather than a fallback.
//
// A Field carrying a number is REFUSED. A schema-less document has no field
// numbers, so the constraint the author wrote could not be checked — and an
// unverifiable constraint must not be quietly dropped. This is a capability
// gap, not a missing value, so it is an error even when Entry.on_missing would
// tolerate one.
func MemberName(f *patchpb.Field, at At) (string, error) {
	if err := validateField(f, at); err != nil {
		return "", err
	}
	if f.HasNumber() {
		return "", Errf(CodeIllegalArm, at.Sub("number"),
			"a document with no schema has no field numbers, so this constraint cannot be checked")
	}

	name := f.GetName()
	if f.HasJsonName() {
		if f.HasName() && f.GetJsonName() != name {
			return "", Errf(CodeFieldConflict, at,
				"name is %q and json_name is %q; without a schema there is no derivation between them",
				name, f.GetJsonName())
		}
		name = f.GetJsonName()
	}
	return name, nil
}

// MemberKey returns the member a MapKey addresses in a schema-less document.
//
// Only the string arm is accepted. A JSON object's keys are strings, and the
// format requires a MapKey's arm to match the map's DECLARED key type — a
// declaration a schema-less document does not have, so a numeric or bool key
// cannot be shown to be correct and is refused rather than stringified. The
// previous design stringified them, which is how `dpb.Field("7")` and
// `dpb.FieldNum(7)` came to address the same entry.
func MemberKey(k *patchpb.MapKey, at At) (string, error) {
	switch k.WhichKind() {
	case patchpb.MapKey_S_case:
		return k.GetS(), nil
	case patchpb.MapKey_Kind_not_set_case:
		return "", Errf(CodeMissingOneof, at.Sub("kind"), "a map key must carry a value")
	default:
		return "", Errf(CodeIllegalArm, at,
			"a document with no schema has string keys only, so a %s key cannot be shown to be correct",
			mapArmName(k))
	}
}
