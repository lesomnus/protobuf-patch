package jsonpatch_test

import (
	"math"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/jsonpatch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// TestConvertInsideAWellKnownType is a regression test.
//
// The converter used to parse a value by wrapping it as {"fieldName": raw} and
// handing that to protojson along with the HOLDER message. When the holder is a
// well-known type, protojson applies the WKT's custom JSON form to the wrapper
// instead of reading a field out of it -- and google.protobuf.Value accepts any
// JSON, so it did not even error. Patching inside a Struct silently wrote 0.
func TestConvertInsideAWellKnownType(t *testing.T) {
	st, err := structpb.NewStruct(map[string]any{"a": 1.0, "keep": "untouched"})
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}

	doc, err := jsonpatch.Unmarshal([]byte(`[{"op":"replace","path":"/fields/a/numberValue","value":2}]`))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	p, err := jsonpatch.Convert(doc, st.ProtoReflect().Descriptor())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	got, err := patchproto.Apply(st, p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Compare by value: protojson's spacing is deliberately unstable.
	if n := got.GetFields()["a"].GetNumberValue(); n != 2 {
		t.Errorf("a = %v, want 2 (0 is what the holder bug produced)", n)
	}
	if s := got.GetFields()["keep"].GetStringValue(); s != "untouched" {
		t.Errorf("keep = %q; a Patch never discards data it did not name", s)
	}
}

// TestScalarFormsFollowProtoJSON pins the forms the converter now parses
// itself, since it no longer borrows protojson's holder parsing for scalars.
func TestScalarFormsFollowProtoJSON(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		check func(*testing.T, *sample.Value)
	}{
		{"int64 as a number", `[{"op":"replace","path":"/i641","value":-9007199254740993}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetI64_1() != -9007199254740993 {
					t.Errorf("= %d", m.GetI64_1())
				}
			}},
		{"int64 as a string", `[{"op":"replace","path":"/i641","value":"-9223372036854775808"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetI64_1() != math.MinInt64 {
					t.Errorf("= %d", m.GetI64_1())
				}
			}},
		{"uint64 at its maximum", `[{"op":"replace","path":"/u641","value":"18446744073709551615"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetU64_1() != math.MaxUint64 {
					t.Errorf("= %d", m.GetU64_1())
				}
			}},
		{"NaN", `[{"op":"replace","path":"/f641","value":"NaN"}]`,
			func(t *testing.T, m *sample.Value) {
				if !math.IsNaN(m.GetF64_1()) {
					t.Errorf("= %v", m.GetF64_1())
				}
			}},
		{"Infinity", `[{"op":"replace","path":"/f641","value":"Infinity"}]`,
			func(t *testing.T, m *sample.Value) {
				if !math.IsInf(m.GetF64_1(), 1) {
					t.Errorf("= %v", m.GetF64_1())
				}
			}},
		{"-Infinity", `[{"op":"replace","path":"/f641","value":"-Infinity"}]`,
			func(t *testing.T, m *sample.Value) {
				if !math.IsInf(m.GetF64_1(), -1) {
					t.Errorf("= %v", m.GetF64_1())
				}
			}},
		{"base64 with padding", `[{"op":"replace","path":"/bs1","value":"AQI="}]`,
			func(t *testing.T, m *sample.Value) {
				if string(m.GetBs_1()) != "\x01\x02" {
					t.Errorf("= %v", m.GetBs_1())
				}
			}},
		{"base64 without padding", `[{"op":"replace","path":"/bs1","value":"AQI"}]`,
			func(t *testing.T, m *sample.Value) {
				if string(m.GetBs_1()) != "\x01\x02" {
					t.Errorf("= %v", m.GetBs_1())
				}
			}},
		{"enum by name", `[{"op":"replace","path":"/enum1","value":"LEVEL_HI"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetEnum_1() != sample.Level_LEVEL_HI {
					t.Errorf("= %v", m.GetEnum_1())
				}
			}},
		{"enum by number", `[{"op":"replace","path":"/enum1","value":2}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetEnum_1() != sample.Level_LEVEL_HI {
					t.Errorf("= %v", m.GetEnum_1())
				}
			}},
		{"a whole map from an object", `[{"op":"replace","path":"/mSS","value":{"a":"1","b":"2"}}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetMSS()["a"] != "1" || m.GetMSS()["b"] != "2" {
					t.Errorf("= %v", m.GetMSS())
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, mustConvertAndApply(t, tt.doc, &sample.Value{}))
		})
	}
}

func TestScalarFormsRejectWhatProtoJSONRejects(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"a non-integral value for an integer field", `[{"op":"replace","path":"/i321","value":1.5}]`},
		{"an int32 out of range", `[{"op":"replace","path":"/i321","value":2147483648}]`},
		{"a uint32 out of range", `[{"op":"replace","path":"/u321","value":4294967296}]`},
		{"a negative value for an unsigned field", `[{"op":"replace","path":"/u641","value":-1}]`},
		{"an unknown enum value name", `[{"op":"replace","path":"/enum1","value":"LEVEL_NOPE"}]`},
		{"a bool for a string field", `[{"op":"replace","path":"/s1","value":true}]`},
		{"an object for a scalar field", `[{"op":"replace","path":"/s1","value":{}}]`},
		{"not base64", `[{"op":"replace","path":"/bs1","value":"!!!!"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := jsonpatch.Unmarshal([]byte(tt.doc))
			if err != nil {
				return
			}
			if _, err := jsonpatch.Convert(d, sampleDesc); err == nil {
				t.Fatal("converted; the value does not fit the field")
			}
		})
	}
}
