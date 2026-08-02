package patchstruct

import (
	"reflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

var byteSlice = reflect.TypeOf([]byte(nil))

// arm reports the Value arm a Go type accepts, or ShapeNone when the type is
// not addressable by this engine.
//
// Go's static types answer more of the format's questions than JSON can: a
// struct is not a map, an int32 is not an int64, and a map's key type is
// declared. So patchstruct checks the arm table where patchjson has to let it
// go.
func shapeOf(t reflect.Type) patch.Shape {
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return patch.ShapeScalar
	case reflect.Struct:
		return patch.ShapeMessage
	case reflect.Map:
		return patch.ShapeMap
	case reflect.Slice:
		if t == byteSlice {
			return patch.ShapeScalar
		}
		return patch.ShapeList
	}
	return patch.ShapeNone
}

// deref strips a single pointer, which is how a hand-written struct spells
// presence. Deeper indirection is refused rather than guessed at.
func deref(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		return t.Elem()
	}
	return t
}

// convert turns a Value into a reflect.Value assignable to t.
//
// Arms and types must correspond exactly, and nothing is truncated: an i32
// carrying a value outside int8 is an error rather than a wrapped write. The
// format has no conversions and neither does this.
func convert(v *patchpb.Value, t reflect.Type, at patch.At) (reflect.Value, error) {
	wantPtr := t.Kind() == reflect.Ptr
	base := deref(t)
	if base.Kind() == reflect.Ptr {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"%s is a pointer to a pointer, which this engine does not address", t)
	}

	out, err := convertBase(v, base, at)
	if err != nil {
		return reflect.Value{}, err
	}
	if !wantPtr {
		return out, nil
	}
	p := reflect.New(base)
	p.Elem().Set(out)
	return p, nil
}

func convertBase(v *patchpb.Value, t reflect.Type, at patch.At) (reflect.Value, error) {
	mismatch := func() (reflect.Value, error) {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"%s does not take %s", t, armName(v))
	}

	switch v.WhichKind() {
	case patchpb.Value_B_case:
		if t.Kind() != reflect.Bool {
			return mismatch()
		}
		return valueOf(t, func(r reflect.Value) { r.SetBool(v.GetB()) }), nil

	case patchpb.Value_S_case:
		if t.Kind() != reflect.String {
			return mismatch()
		}
		return valueOf(t, func(r reflect.Value) { r.SetString(v.GetS()) }), nil

	case patchpb.Value_X_case:
		if t != byteSlice {
			return mismatch()
		}
		return reflect.ValueOf(v.GetX()).Convert(t), nil

	case patchpb.Value_I32_case:
		return signed(int64(v.GetI32()), t, at, narrowSigned, mismatch)
	case patchpb.Value_I64_case:
		return signed(v.GetI64(), t, at, wideSigned, mismatch)
	case patchpb.Value_U32_case:
		return unsigned(uint64(v.GetU32()), t, at, narrowUnsigned, mismatch)
	case patchpb.Value_U64_case:
		return unsigned(v.GetU64(), t, at, wideUnsigned, mismatch)

	case patchpb.Value_F32_case:
		if t.Kind() != reflect.Float32 {
			return mismatch()
		}
		return valueOf(t, func(r reflect.Value) { r.SetFloat(float64(v.GetF32())) }), nil
	case patchpb.Value_F64_case:
		if t.Kind() != reflect.Float64 {
			return mismatch()
		}
		return valueOf(t, func(r reflect.Value) { r.SetFloat(v.GetF64()) }), nil

	case patchpb.Value_E_case:
		// A named integer type carries no set of declared values that reflect
		// can see, so the format's closed-enum rule could not be checked. It is
		// refused rather than written as a bare number.
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"a Go type carries no enum declaration, so an enum value cannot be checked")

	case patchpb.Value_M_case:
		if t.Kind() != reflect.Struct {
			return mismatch()
		}
		out := reflect.New(t).Elem()
		if err := fillStruct(out, v.GetM(), at.Sub("m")); err != nil {
			return reflect.Value{}, err
		}
		return out, nil

	case patchpb.Value_L_case:
		if t.Kind() != reflect.Slice || t == byteSlice {
			return mismatch()
		}
		vs := v.GetL().GetValues()
		out := reflect.MakeSlice(t, 0, len(vs))
		for i, ev := range vs {
			e, err := convert(ev, t.Elem(), at.Sub("l").Index("values", i))
			if err != nil {
				return reflect.Value{}, err
			}
			out = reflect.Append(out, e)
		}
		return out, nil

	case patchpb.Value_Map_case:
		if t.Kind() != reflect.Map {
			return mismatch()
		}
		out := reflect.MakeMap(t)
		seen := map[any]int{}
		for i, me := range v.GetMap().GetEntries() {
			eat := at.Sub("map").Index("entries", i)
			k, err := mapKey(me.GetKey(), t.Key(), eat.Sub("key"))
			if err != nil {
				return reflect.Value{}, err
			}
			if prev, dup := seen[k.Interface()]; dup {
				return reflect.Value{}, patch.Errf(patch.CodeDuplicateTarget, eat.Sub("key"),
					"entries[%d] already carries that key", prev)
			}
			seen[k.Interface()] = i
			e, err := convert(me.GetValue(), t.Elem(), eat.Sub("value"))
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(k, e)
		}
		return out, nil
	}

	return reflect.Value{}, patch.Errf(patch.CodeMissingOneof, at.Sub("kind"),
		"an unset kind is an error, not an implicit clear")
}

func valueOf(t reflect.Type, set func(reflect.Value)) reflect.Value {
	r := reflect.New(t).Elem()
	set(r)
	return r
}

// Each integer arm names a WIDTH CLASS, and only Go types in that class accept
// it: i32 never lands in an int64 and i64 never lands in an int32. A narrower
// type within the class is accepted with a range check, so nothing is
// truncated — the format has no conversions and neither does this.
var (
	narrowSigned   = []reflect.Kind{reflect.Int8, reflect.Int16, reflect.Int32}
	wideSigned     = []reflect.Kind{reflect.Int, reflect.Int64}
	narrowUnsigned = []reflect.Kind{reflect.Uint8, reflect.Uint16, reflect.Uint32}
	wideUnsigned   = []reflect.Kind{reflect.Uint, reflect.Uint64}
)

func inClass(k reflect.Kind, class []reflect.Kind) bool {
	for _, c := range class {
		if k == c {
			return true
		}
	}
	return false
}

func signed(n int64, t reflect.Type, at patch.At, class []reflect.Kind, mismatch func() (reflect.Value, error)) (reflect.Value, error) {
	if !inClass(t.Kind(), class) {
		return mismatch()
	}
	r := reflect.New(t).Elem()
	if r.OverflowInt(n) {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"%d is outside the range of %s; nothing is truncated here", n, t)
	}
	r.SetInt(n)
	return r, nil
}

func unsigned(n uint64, t reflect.Type, at patch.At, class []reflect.Kind, mismatch func() (reflect.Value, error)) (reflect.Value, error) {
	if !inClass(t.Kind(), class) {
		return mismatch()
	}
	r := reflect.New(t).Elem()
	if r.OverflowUint(n) {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"%d is outside the range of %s; nothing is truncated here", n, t)
	}
	r.SetUint(n)
	return r, nil
}

// mapKey converts a MapKey against the map's DECLARED key type — a check JSON
// cannot make and this engine can.
func mapKey(k *patchpb.MapKey, t reflect.Type, at patch.At) (reflect.Value, error) {
	bad := func() (reflect.Value, error) {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"a map with %s keys does not take %s", t, mapArmName(k))
	}

	switch t.Kind() {
	case reflect.String:
		if k.WhichKind() != patchpb.MapKey_S_case {
			return bad()
		}
		return valueOf(t, func(r reflect.Value) { r.SetString(k.GetS()) }), nil

	case reflect.Bool:
		if k.WhichKind() != patchpb.MapKey_B_case {
			return bad()
		}
		return valueOf(t, func(r reflect.Value) { r.SetBool(k.GetB()) }), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if k.WhichKind() != patchpb.MapKey_I_case {
			return bad()
		}
		r := reflect.New(t).Elem()
		if r.OverflowInt(k.GetI()) {
			return reflect.Value{}, patch.Errf(patch.CodeMapKeyOutOfRange, at,
				"%d is outside the range of %s", k.GetI(), t)
		}
		r.SetInt(k.GetI())
		return r, nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if k.WhichKind() != patchpb.MapKey_U_case {
			return bad()
		}
		r := reflect.New(t).Elem()
		if r.OverflowUint(k.GetU()) {
			return reflect.Value{}, patch.Errf(patch.CodeMapKeyOutOfRange, at,
				"%d is outside the range of %s", k.GetU(), t)
		}
		r.SetUint(k.GetU())
		return r, nil
	}
	return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
		"%s is not a key type this engine addresses", t)
}

// fillStruct sets out's fields from a MessageValue, leaving the rest zero.
func fillStruct(out reflect.Value, mv *patchpb.MessageValue, at patch.At) error {
	for i, fv := range mv.GetFields() {
		fat := at.Index("fields", i)
		sf, ok := lookupField(out.Type(), fv.GetKey(), fat.Sub("key"))
		if !ok.found {
			if ok.err != nil {
				return ok.err
			}
			return patch.Errf(patch.CodeVacantTarget, fat.Sub("key"),
				"%s declares no such field", out.Type())
		}
		e, err := convert(fv.GetValue(), sf.Type, fat.Sub("value"))
		if err != nil {
			return err
		}
		out.FieldByIndex(sf.Index).Set(e)
	}
	return nil
}

func armName(v *patchpb.Value) string {
	switch v.WhichKind() {
	case patchpb.Value_B_case:
		return "b"
	case patchpb.Value_I32_case:
		return "i32"
	case patchpb.Value_I64_case:
		return "i64"
	case patchpb.Value_U32_case:
		return "u32"
	case patchpb.Value_U64_case:
		return "u64"
	case patchpb.Value_F32_case:
		return "f32"
	case patchpb.Value_F64_case:
		return "f64"
	case patchpb.Value_S_case:
		return "s"
	case patchpb.Value_X_case:
		return "x"
	case patchpb.Value_E_case:
		return "e"
	case patchpb.Value_M_case:
		return "m"
	case patchpb.Value_L_case:
		return "l"
	case patchpb.Value_Map_case:
		return "map"
	}
	return "nothing"
}

func mapArmName(k *patchpb.MapKey) string {
	switch k.WhichKind() {
	case patchpb.MapKey_S_case:
		return "a string key"
	case patchpb.MapKey_I_case:
		return "a signed key"
	case patchpb.MapKey_U_case:
		return "an unsigned key"
	case patchpb.MapKey_B_case:
		return "a bool key"
	}
	return "nothing"
}
