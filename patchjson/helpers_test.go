package patchjson_test

import (
	"math"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func mathNaN() float64         { return math.NaN() }
func mathInf(sign int) float64 { return math.Inf(sign) }

// setUnknown gives m a field number it does not declare, standing in for a
// document written by a newer revision of the schema.
func setUnknown(m proto.Message, num protowire.Number) {
	b := protowire.AppendTag(nil, num, protowire.VarintType)
	b = protowire.AppendVarint(b, 1)
	m.ProtoReflect().SetUnknown(protoreflect.RawFields(b))
}
