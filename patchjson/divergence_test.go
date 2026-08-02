package patchjson_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/lesomnus/protobuf-patch/conformance"
	"github.com/lesomnus/protobuf-patch/conformance/conformancepb"
	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchjson"
)

// A Patch does not mean the same thing to patchjson as to patchproto — it
// cannot, since the format's distinctions between a message and a map, an
// int32 and an int64, and a declared field and an undeclared one do not exist
// in schema-less JSON.
//
// The danger is not that they differ. It is that they differ SILENTLY, which
// is how the previous design's three backends ended up applying the same
// document three different ways. So: every case in the shared corpus is run
// through this engine too, and any case that does not agree must be listed
// here with a reason. A new divergence cannot appear without someone writing
// down why.
// The disagreements all reduce to four facts about a document with no schema.
// Naming the cause rather than the case keeps the list from becoming a pile of
// excuses: a new entry has to fit one of these, or it is a bug.
const (
	// A descriptor says which fields exist. JSON does not, so patchproto can
	// tell "declared but unset" from "not a field at all" and patchjson cannot
	// — every member name is equally valid, and an absent one is an empty slot.
	causeDeclared = "'declared' is a property of a descriptor and has no JSON shadow"

	// protobuf has no presence for repeated and map fields, so an empty one
	// still exists as a container. ProtoJSON omits it entirely, so in the JSON
	// document it is simply not there — a path cannot descend into it, and a
	// list emptied by a patch stays as [] rather than disappearing.
	causeEmptyContainer = "an empty list or map exists in protobuf and is absent in JSON"

	// move and copy must preserve the declared type. There are no declared
	// types here, so the check cannot happen and a string may land where a
	// number was.
	causeNoTypes = "there are no declared types to compare"

	// Field.number and non-string map keys are constraints a schema-less
	// consumer cannot check, so it refuses them — earlier, and for a different
	// reason than the case is pinning.
	causeNoNumbers = "a field number cannot be checked without a schema, so it is refused first"

	// RFC 8259 has no NaN literal, so a JSON document cannot hold one at all.
	// ProtoJSON spells it as the string "NaN", which is a string and not a
	// number, so a float arm cannot match it. The schema's rule that NaN
	// equals NaN is therefore unreachable here rather than contradicted.
	causeNoNaN = "JSON has no NaN; ProtoJSON writes it as the string \"NaN\", which no float arm matches"

	// A oneof is a protobuf declaration. A JSON object has none, so there is
	// no way to know which member is "set" — and inventing one would be
	// inventing a schema. Note that two of the oneof cases agree with
	// patchproto anyway, for the wrong reason: they are errors on both sides.
	causeNoOneof = "a oneof is declared by a schema, and a JSON document has none"

	// every_entry is map-only, and an object is this engine's map. So it
	// accepts one where patchproto, which can see that the container is a
	// message, refuses.
	causeObjectIsAMap = "an object stands in for both a message and a map, and behaves like the map"

	// Creating a container has to decide whether to make an object or an
	// array, and with no schema there is nothing to decide it with. It makes
	// an object, so a path that then descends by index fails there rather
	// than where patchproto fails.
	causeCreateShape = "creating a container cannot know whether to make an object or an array"

	// A JSON document does not name its own type, so there is nothing to
	// compare Patch.message_type against unless the caller supplies a name
	// with ExpectType. The corpus runner does not, since it is testing the
	// operations rather than the routing.
	causeNoTypeName = "a JSON document carries no type name to check message_type against"
)

var knownDivergences = map[string]string{
	"a_list_index_is_never_created":        causeCreateShape,
	"an_undeclared_field_is_never_created": causeDeclared,

	"every_entry_removes_all_of_them":             causeEmptyContainer,
	"every_entry_on_an_empty_map_selects_nothing": causeEmptyContainer,
	"every_entry_still_checks_the_arm":            causeNoTypes,
	"every_entry_needs_a_map_not_a_message":       causeObjectIsAMap,
	"a_test_over_an_empty_map_asserts_nothing":    causeEmptyContainer,

	"remove_clears_whichever_member_is_set":          causeNoOneof,
	"remove_on_a_clear_oneof_is_a_no_op":             causeNoOneof,
	"assign_overwrites_the_member_that_is_set":       causeNoOneof,
	"test_exists_false_holds_on_a_clear_oneof":       causeNoOneof,
	"test_exists_true_holds_when_a_member_is_set":    causeNoOneof,
	"test_value_fails_on_a_clear_oneof":              causeNoOneof,
	"nest_descends_into_the_member_that_is_set":      causeNoOneof,
	"insert_refuses_an_occupied_oneof":               causeNoOneof,
	"an_undeclared_oneof_name_is_a_missing_target":   causeNoOneof,
	"an_undeclared_oneof_name_can_be_skipped":        causeNoOneof,
	"a_oneof_and_its_set_member_are_the_same_target": causeNoOneof,

	"nan_equals_nan":                     causeNoNaN,
	"nan_nested_in_a_message_equals_nan": causeNoNaN,

	"remove_clears_a_declared_but_unset_field": causeDeclared,
	"container_assign_fails_before_clearing":   causeDeclared,

	"assign_creates_a_map_entry":         causeEmptyContainer,
	"insert_creates_a_map_entry":         causeEmptyContainer,
	"remove_needs_an_existing_map_entry": causeEmptyContainer,
	"container_assign_replaces_a_list":   causeEmptyContainer,
	"range_all":                          causeEmptyContainer,
	"range_open_from_zero_is_everything": causeEmptyContainer,

	"relocation_requires_the_same_kind": causeNoTypes,

	"an_empty_message_type_is_not_the_same_as_none": causeNoTypeName,
	"an_untyped_patch_still_checks_the_field":       causeNoNumbers,

	"duplicate_target_is_an_error":    causeNoNumbers,
	"field_conflict_is_never_skipped": causeNoNumbers,
}

// TestDivergenceFromPatchproto runs the shared corpus through the JSON engine
// and requires every disagreement to be declared above.
func TestDivergenceFromPatchproto(t *testing.T) {
	cases, err := conformance.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// UseProtoNames so that the keys match the field names the corpus writes.
	marshal := protojson.MarshalOptions{UseProtoNames: true}

	agreed := 0
	for _, c := range cases {
		t.Run(c.GetName(), func(t *testing.T) {
			in := c.GetInput()
			if in == nil {
				in = &sample.Value{}
			}
			doc, err := marshal.Marshal(in)
			if err != nil {
				t.Fatalf("rendering the input: %v", err)
			}

			got, applyErr := patchjson.Apply(doc, c.GetPatch())
			same, why := agrees(t, marshal, c, got, applyErr)

			reason, declared := knownDivergences[c.GetName()]
			switch {
			case same && !declared:
				agreed++
			case same && declared:
				t.Errorf("this case is listed as a divergence but the engines agree; "+
					"remove it from knownDivergences\n  listed reason: %s", reason)
			case !same && declared:
				t.Logf("declared divergence: %s", reason)
			default:
				t.Errorf("undeclared divergence: %s\n"+
					"add it to knownDivergences with a reason, or fix the engine", why)
			}
		})
	}

	if agreed == 0 {
		t.Error("no case agreed; the corpus is not exercising the shared semantics at all")
	}
	t.Logf("%d of %d cases agree with patchproto", agreed, len(cases))
}

// agrees reports whether the JSON engine produced what the corpus expects of
// the proto engine.
func agrees(t *testing.T, marshal protojson.MarshalOptions, c *conformancepb.Case, got []byte, gotErr error) (bool, string) {
	t.Helper()

	if c.WhichWant() == conformancepb.Case_Error_case {
		want, _ := patch.CodeByName(c.GetError())
		if gotErr == nil {
			return false, "the corpus expects " + want.String() + ", the JSON engine succeeded"
		}
		if code := patch.CodeOf(gotErr); code != want {
			return false, "the corpus expects " + want.String() + ", the JSON engine reported " + code.String()
		}
		return true, ""
	}

	if gotErr != nil {
		return false, "the corpus expects success, the JSON engine reported " + patch.CodeOf(gotErr).String()
	}
	wantDoc, err := marshal.Marshal(c.GetOutput())
	if err != nil {
		t.Fatalf("rendering the output: %v", err)
	}
	if !sameJSONText(got, wantDoc) {
		return false, "outputs differ:\n  json  " + string(got) + "\n  proto " + string(wantDoc)
	}
	return true, ""
}

func sameJSONText(a, b []byte) bool {
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
