package patchjson

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strconv"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// render turns a Value into the JSON value it writes.
//
// The mapping is deliberately NOT injective. JSON has one number type, so
// i32, i64, u32, u64, f32, f64 and e all render as a number; JSON has one
// keyed container, so m and map both render as an object. Against a protobuf
// message those arms are distinguishable and a mismatch is an error; here
// there is nothing to mismatch against, so they collapse. This is the clearest
// way in which a Patch does not mean the same thing to both consumers.
//
// 64-bit integers render as plain JSON numbers, so a magnitude above 2^53 does
// not survive a round trip through a reader that decodes into float64. The
// alternative — ProtoJSON's string form — was rejected because this engine
// exists for documents that never came from protobuf, where writing "5" for a
// number is the more surprising answer of the two.
func render(v *patchpb.Value, at patch.At) (any, error) {
	switch v.WhichKind() {
	case patchpb.Value_B_case:
		return v.GetB(), nil
	case patchpb.Value_S_case:
		return v.GetS(), nil
	case patchpb.Value_X_case:
		return base64.StdEncoding.EncodeToString(v.GetX()), nil

	case patchpb.Value_I32_case:
		return num(strconv.FormatInt(int64(v.GetI32()), 10)), nil
	case patchpb.Value_I64_case:
		return num(strconv.FormatInt(v.GetI64(), 10)), nil
	case patchpb.Value_U32_case:
		return num(strconv.FormatUint(uint64(v.GetU32()), 10)), nil
	case patchpb.Value_U64_case:
		return num(strconv.FormatUint(v.GetU64(), 10)), nil
	case patchpb.Value_E_case:
		return num(strconv.FormatInt(int64(v.GetE()), 10)), nil

	case patchpb.Value_F32_case:
		return float(float64(v.GetF32()), 32, at)
	case patchpb.Value_F64_case:
		return float(v.GetF64(), 64, at)

	case patchpb.Value_M_case:
		obj := map[string]any{}
		for i, fv := range v.GetM().GetFields() {
			fat := at.Sub("m").Index("fields", i)
			k, err := patch.MemberName(fv.GetKey(), fat.Sub("key"))
			if err != nil {
				return nil, err
			}
			if _, dup := obj[k]; dup {
				return nil, patch.Errf(patch.CodeDuplicateTarget, fat.Sub("key"),
					"%q appears twice", k)
			}
			e, err := render(fv.GetValue(), fat.Sub("value"))
			if err != nil {
				return nil, err
			}
			obj[k] = e
		}
		return obj, nil

	case patchpb.Value_Map_case:
		obj := map[string]any{}
		for i, me := range v.GetMap().GetEntries() {
			eat := at.Sub("map").Index("entries", i)
			k, err := patch.MemberKey(me.GetKey(), eat.Sub("key"))
			if err != nil {
				return nil, err
			}
			if _, dup := obj[k]; dup {
				return nil, patch.Errf(patch.CodeDuplicateTarget, eat.Sub("key"),
					"%q appears twice", k)
			}
			e, err := render(me.GetValue(), eat.Sub("value"))
			if err != nil {
				return nil, err
			}
			obj[k] = e
		}
		return obj, nil

	case patchpb.Value_L_case:
		vs := v.GetL().GetValues()
		arr := make([]any, 0, len(vs))
		for i, ev := range vs {
			e, err := render(ev, at.Sub("l").Index("values", i))
			if err != nil {
				return nil, err
			}
			arr = append(arr, e)
		}
		return arr, nil
	}
	return nil, patch.Errf(patch.CodeMissingOneof, at.Sub("kind"),
		"an unset kind is an error, not an implicit clear")
}

// float renders a floating-point value, refusing the ones JSON cannot carry.
//
// JSON has no NaN and no infinity (RFC 8259 §6). ProtoJSON spells them as the
// strings "NaN" and "Infinity", but that only works because a reader with a
// schema knows the field is numeric; here it would be indistinguishable from
// an author writing those strings on purpose. Refusing keeps the engine from
// inventing a value the document cannot hold.
func float(f float64, bits int, at patch.At) (any, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, patch.Errf(patch.CodeIllegalArm, at,
			"JSON has no representation for %v", f)
	}
	return num(strconv.FormatFloat(f, 'g', -1, bits)), nil
}

func num(s string) json.Number { return json.Number(s) }

// equal compares a value read from the document against one the Patch carries.
//
// Numbers compare numerically rather than textually, so 1 and 1.0 are the same
// value, which is what RFC 8259 says they are.
func equal(got any, want *patchpb.Value, at patch.At) (bool, error) {
	w, err := render(want, at)
	if err != nil {
		return false, err
	}
	return sameJSON(got, w), nil
}

func sameJSON(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil

	case bool:
		y, ok := b.(bool)
		return ok && x == y

	case string:
		y, ok := b.(string)
		return ok && x == y

	case json.Number:
		return sameNumber(x, b)

	case float64:
		return sameNumber(json.Number(strconv.FormatFloat(x, 'g', -1, 64)), b)

	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, has := y[k]
			if !has || !sameJSON(v, w) {
				return false
			}
		}
		return true

	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func sameNumber(a json.Number, b any) bool {
	var bs json.Number
	switch y := b.(type) {
	case json.Number:
		bs = y
	case float64:
		bs = json.Number(strconv.FormatFloat(y, 'g', -1, 64))
	default:
		return false
	}
	if a == bs {
		return true
	}
	// Fall back to numeric comparison so that 1 and 1.0 agree, and so that two
	// spellings of the same integer do.
	x, err1 := a.Float64()
	y, err2 := bs.Float64()
	return err1 == nil && err2 == nil && x == y
}

// clone deep-copies a decoded JSON value, so that a failed Apply leaves the
// caller's document untouched.
func clone(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clone(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clone(e)
		}
		return out
	}
	return v
}
