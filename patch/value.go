package patch

import (
	"math"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// Shape is the container form a Value carries. An applier dispatches on it
// before it needs a message instance.
type Shape int

const (
	// ShapeNone is a Value with no arm set, which is an error rather than an
	// implicit clear.
	ShapeNone Shape = iota
	// ShapeScalar is b, i32, i64, u32, u64, f32, f64, s, x, or e.
	ShapeScalar
	// ShapeMessage is m.
	ShapeMessage
	// ShapeList is l.
	ShapeList
	// ShapeMap is map.
	ShapeMap
)

// ShapeOf reports the container form v carries.
func ShapeOf(v *patchpb.Value) Shape {
	if v == nil {
		return ShapeNone
	}
	switch v.WhichKind() {
	case patchpb.Value_Kind_not_set_case:
		return ShapeNone
	case patchpb.Value_M_case:
		return ShapeMessage
	case patchpb.Value_L_case:
		return ShapeList
	case patchpb.Value_Map_case:
		return ShapeMap
	default:
		return ShapeScalar
	}
}

// Site says which part of a field a Value is destined for. The same field
// descriptor takes different Value arms depending on the site: a repeated
// string field takes `l` as a whole and `s` per element.
type Site int

const (
	// SiteField is the field itself, whatever its cardinality.
	SiteField Site = iota + 1
	// SiteElement is one element of a repeated field.
	SiteElement
	// SiteMapValue is one value of a map field.
	SiteMapValue
)

// elemKind returns the descriptor whose Kind governs the value at site.
//
// It also reports whether the site is a whole container, in which case the
// Value must carry `l` or `map` rather than a scalar or `m`.
func siteKind(fd protoreflect.FieldDescriptor, site Site) (kind protoreflect.Kind, whole Shape) {
	switch site {
	case SiteElement:
		return fd.Kind(), ShapeNone
	case SiteMapValue:
		return fd.MapValue().Kind(), ShapeNone
	default:
		switch {
		case fd.IsMap():
			return fd.MapValue().Kind(), ShapeMap
		case fd.IsList():
			return fd.Kind(), ShapeList
		default:
			return fd.Kind(), ShapeNone
		}
	}
}

// CheckArm reports whether v's arm is legal for fd at site.
//
// The mapping is total and one-to-one: each protobuf type class takes exactly
// one arm. There is no conversion, so a Value that does not match is an error
// rather than a widened, narrowed, or truncated write.
func CheckArm(v *patchpb.Value, fd protoreflect.FieldDescriptor, site Site, at At) error {
	shape := ShapeOf(v)
	if shape == ShapeNone {
		return Errf(CodeMissingOneof, at,
			"an unset kind is an error, not an implicit clear; use remove to clear")
	}

	kind, whole := siteKind(fd, site)
	if whole != ShapeNone {
		if shape != whole {
			return Errf(CodeIllegalArm, at, "%s takes %s, got %s",
				describeField(fd), armNameOf(whole), armName(v))
		}
		return nil
	}
	if shape == ShapeList || shape == ShapeMap {
		return Errf(CodeIllegalArm, at, "%s is not addressed as a whole container here, got %s",
			describeField(fd), armName(v))
	}

	want := armForKind(kind)
	if got := v.WhichKind(); got != want {
		return Errf(CodeIllegalArm, at, "%s takes %s, got %s",
			describeField(fd), armNameForKind(kind), armName(v))
	}

	if kind == protoreflect.EnumKind {
		return checkEnum(v.GetE(), fd, site, at)
	}
	return nil
}

// Scalar converts a scalar-armed Value into the protoreflect.Value for fd at
// site. The caller must have checked the arm; message, list, and map values
// need an instance to allocate into and belong to the applier.
func Scalar(v *patchpb.Value, fd protoreflect.FieldDescriptor, site Site, at At) (protoreflect.Value, error) {
	if err := CheckArm(v, fd, site, at); err != nil {
		return protoreflect.Value{}, err
	}
	kind, _ := siteKind(fd, site)
	switch kind {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(v.GetB()), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(v.GetI32()), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(v.GetI64()), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(v.GetU32()), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(v.GetU64()), nil
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(v.GetF32()), nil
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(v.GetF64()), nil
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(v.GetS()), nil
	case protoreflect.BytesKind:
		return protoreflect.ValueOfBytes(v.GetX()), nil
	case protoreflect.EnumKind:
		return protoreflect.ValueOfEnum(protoreflect.EnumNumber(v.GetE())), nil
	}
	return protoreflect.Value{}, Errf(CodeIllegalArm, at, "%s is not a scalar", describeField(fd))
}

// checkEnum enforces the closed-enum rule: a closed enum cannot hold a number
// it does not declare, so writing one is an error rather than a value that
// will not survive a round trip.
func checkEnum(number int32, fd protoreflect.FieldDescriptor, site Site, at At) error {
	ed := fd.Enum()
	if site == SiteMapValue {
		ed = fd.MapValue().Enum()
	}
	if ed == nil || !ed.IsClosed() {
		return nil
	}
	if ed.Values().ByNumber(protoreflect.EnumNumber(number)) == nil {
		return Errf(CodeUndeclaredEnumValue, at, "%s does not declare %d", ed.FullName(), number)
	}
	return nil
}

// MapKeyFor converts k into the protoreflect.MapKey for the map field fd.
//
// The arm must match the map's declared key type and is never coerced, so a
// numeric key does not address a string-keyed map. The value must also lie in
// the declared type's range: a key outside it is an error, never truncated and
// never a merely vacant key that on_missing could skip.
func MapKeyFor(k *patchpb.MapKey, fd protoreflect.FieldDescriptor, at At) (protoreflect.MapKey, error) {
	if !fd.IsMap() {
		return protoreflect.MapKey{}, Errf(CodeIllegalArm, at, "%s is not a map", describeField(fd))
	}
	kd := fd.MapKey()
	bad := func() (protoreflect.MapKey, error) {
		return protoreflect.MapKey{}, Errf(CodeIllegalArm, at,
			"%s has %v keys, got %s", describeField(fd), kd.Kind(), mapArmName(k))
	}

	switch kd.Kind() {
	case protoreflect.StringKind:
		if k.WhichKind() != patchpb.MapKey_S_case {
			return bad()
		}
		return protoreflect.ValueOfString(k.GetS()).MapKey(), nil

	case protoreflect.BoolKind:
		if k.WhichKind() != patchpb.MapKey_B_case {
			return bad()
		}
		return protoreflect.ValueOfBool(k.GetB()).MapKey(), nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if k.WhichKind() != patchpb.MapKey_I_case {
			return bad()
		}
		v := k.GetI()
		if v < math.MinInt32 || v > math.MaxInt32 {
			return protoreflect.MapKey{}, Errf(CodeMapKeyOutOfRange, at,
				"%d is outside the range of %v", v, kd.Kind())
		}
		return protoreflect.ValueOfInt32(int32(v)).MapKey(), nil

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if k.WhichKind() != patchpb.MapKey_I_case {
			return bad()
		}
		return protoreflect.ValueOfInt64(k.GetI()).MapKey(), nil

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if k.WhichKind() != patchpb.MapKey_U_case {
			return bad()
		}
		v := k.GetU()
		if v > math.MaxUint32 {
			return protoreflect.MapKey{}, Errf(CodeMapKeyOutOfRange, at,
				"%d is outside the range of %v", v, kd.Kind())
		}
		return protoreflect.ValueOfUint32(uint32(v)).MapKey(), nil

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if k.WhichKind() != patchpb.MapKey_U_case {
			return bad()
		}
		return protoreflect.ValueOfUint64(k.GetU()).MapKey(), nil
	}
	return protoreflect.MapKey{}, Errf(CodeIllegalArm, at,
		"%v is not a legal map key type", kd.Kind())
}

func armForKind(kind protoreflect.Kind) any {
	switch kind {
	case protoreflect.BoolKind:
		return patchpb.Value_B_case
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return patchpb.Value_I32_case
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return patchpb.Value_I64_case
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return patchpb.Value_U32_case
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return patchpb.Value_U64_case
	case protoreflect.FloatKind:
		return patchpb.Value_F32_case
	case protoreflect.DoubleKind:
		return patchpb.Value_F64_case
	case protoreflect.StringKind:
		return patchpb.Value_S_case
	case protoreflect.BytesKind:
		return patchpb.Value_X_case
	case protoreflect.EnumKind:
		return patchpb.Value_E_case
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return patchpb.Value_M_case
	}
	return patchpb.Value_Kind_not_set_case
}

func armNameForKind(kind protoreflect.Kind) string {
	switch kind {
	case protoreflect.BoolKind:
		return "b"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return "i32"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return "i64"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "u32"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "u64"
	case protoreflect.FloatKind:
		return "f32"
	case protoreflect.DoubleKind:
		return "f64"
	case protoreflect.StringKind:
		return "s"
	case protoreflect.BytesKind:
		return "x"
	case protoreflect.EnumKind:
		return "e"
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return "m"
	}
	return "?"
}

func armNameOf(shape Shape) string {
	switch shape {
	case ShapeList:
		return "l"
	case ShapeMap:
		return "map"
	case ShapeMessage:
		return "m"
	}
	return "?"
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
		return "s"
	case patchpb.MapKey_I_case:
		return "i"
	case patchpb.MapKey_U_case:
		return "u"
	case patchpb.MapKey_B_case:
		return "b"
	}
	return "nothing"
}

func describeField(fd protoreflect.FieldDescriptor) string {
	return string(fd.FullName())
}
