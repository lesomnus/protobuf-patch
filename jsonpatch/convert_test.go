package jsonpatch_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/jsonpatch"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

var sampleDesc = (&sample.Value{}).ProtoReflect().Descriptor()

// convertAndApply is the only test the conversion really needs: a document
// that survives the trip and produces the message RFC 6902 says it should.
func convertAndApply(t *testing.T, doc string, in *sample.Value) (*sample.Value, error) {
	t.Helper()
	d, err := jsonpatch.Unmarshal([]byte(doc))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	p, err := jsonpatch.Convert(d, sampleDesc)
	if err != nil {
		return nil, err
	}
	if err := patch.Validate(p); err != nil {
		t.Fatalf("the conversion produced a document that does not validate: %v", err)
	}
	return patchproto.Apply(in, p)
}

func mustConvertAndApply(t *testing.T, doc string, in *sample.Value) *sample.Value {
	t.Helper()
	got, err := convertAndApply(t, doc, in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return got
}

func TestConvertOps(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		in   *sample.Value
		want *sample.Value
	}{
		{
			"replace",
			`[{"op":"replace","path":"/s1","value":"new"}]`,
			str("old"), str("new"),
		},
		{
			"add on an object member replaces",
			// RFC 6902 4.1: add on an existing member replaces it. insert
			// would refuse, so this has to map to assign.
			`[{"op":"add","path":"/s1","value":"new"}]`,
			str("held"), str("new"),
		},
		{
			"add on an absent member creates",
			`[{"op":"add","path":"/s1","value":"new"}]`,
			&sample.Value{}, str("new"),
		},
		{
			"remove",
			`[{"op":"remove","path":"/s1"}]`,
			str("x"), &sample.Value{},
		},
		{
			"proto field names work too",
			`[{"op":"replace","path":"/s_1","value":"new"}]`,
			str("old"), str("new"),
		},
		{
			"add on an array index inserts before it",
			`[{"op":"add","path":"/rS1/1","value":"Z"}]`,
			list("a", "b"), list("a", "Z", "b"),
		},
		{
			"the dash token appends",
			`[{"op":"add","path":"/rS1/-","value":"Z"}]`,
			list("a", "b"), list("a", "b", "Z"),
		},
		{
			"remove an array element",
			`[{"op":"remove","path":"/rS1/0"}]`,
			list("a", "b"), list("b"),
		},
		{
			"replace an array element",
			`[{"op":"replace","path":"/rS1/1","value":"Z"}]`,
			list("a", "b"), list("a", "Z"),
		},
		{
			"nested object member",
			`[{"op":"replace","path":"/m1/s1","value":"deep"}]`,
			nested("was"), nested("deep"),
		},
		{
			"several ops in order",
			`[{"op":"add","path":"/s1","value":"a"},{"op":"add","path":"/s2","value":"b"}]`,
			&sample.Value{},
			func() *sample.Value {
				m := &sample.Value{}
				m.SetS_1("a")
				m.SetS_2("b")
				return m
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustConvertAndApply(t, tt.doc, tt.in)
			if !proto.Equal(got, tt.want) {
				t.Errorf("\n got %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestConvertTypesComeFromTheDescriptor(t *testing.T) {
	// JSON has one number type and protobuf has ten. The descriptor is what
	// decides which arm a value lands in, which is why Convert needs one.
	tests := []struct {
		name  string
		doc   string
		check func(*testing.T, *sample.Value)
	}{
		{"int32", `[{"op":"replace","path":"/i321","value":-7}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetI32_1() != -7 {
					t.Errorf("= %v", m.GetI32_1())
				}
			}},
		{"uint64 as a string", `[{"op":"replace","path":"/u641","value":"18446744073709551615"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetU64_1() != 1<<64-1 {
					t.Errorf("= %v", m.GetU64_1())
				}
			}},
		{"double", `[{"op":"replace","path":"/f641","value":1.5}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetF64_1() != 1.5 {
					t.Errorf("= %v", m.GetF64_1())
				}
			}},
		{"bool", `[{"op":"replace","path":"/b1","value":true}]`,
			func(t *testing.T, m *sample.Value) {
				if !m.GetB_1() {
					t.Error("= false")
				}
			}},
		{"bytes as base64", `[{"op":"replace","path":"/bs1","value":"AQI="}]`,
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
		{"message from an object", `[{"op":"replace","path":"/m1","value":{"s1":"inner"}}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetM_1().GetS_1() != "inner" {
					t.Errorf("= %v", m.GetM_1())
				}
			}},
		{"whole list from an array", `[{"op":"replace","path":"/rS1","value":["x","y"]}]`,
			func(t *testing.T, m *sample.Value) {
				if len(m.GetRS_1()) != 2 || m.GetRS_1()[0] != "x" {
					t.Errorf("= %v", m.GetRS_1())
				}
			}},
		{"map entry", `[{"op":"add","path":"/mSS/k","value":"v"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetMSS()["k"] != "v" {
					t.Errorf("= %v", m.GetMSS())
				}
			}},
		{"int-keyed map entry", `[{"op":"add","path":"/mI32S/7","value":"v"}]`,
			func(t *testing.T, m *sample.Value) {
				if m.GetMI32S()[7] != "v" {
					t.Errorf("= %v", m.GetMI32S())
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, mustConvertAndApply(t, tt.doc, &sample.Value{}))
		})
	}
}

// TestConvertIsTotal covers what the previous implementation had to reject.
func TestConvertIsTotal(t *testing.T) {
	t.Run("test is converted, not dropped", func(t *testing.T) {
		// The previous implementation silently discarded test ops, turning a
		// guarded document into an unguarded one.
		_, err := convertAndApply(t,
			`[{"op":"test","path":"/s1","value":"expected"},{"op":"replace","path":"/s2","value":"written"}]`,
			str("actual"))
		if patch.CodeOf(err) != patch.CodeTestFailed {
			t.Fatalf("CodeOf = %v, want CodeTestFailed (err = %v)", patch.CodeOf(err), err)
		}
	})

	t.Run("a passing test lets the rest through", func(t *testing.T) {
		got := mustConvertAndApply(t,
			`[{"op":"test","path":"/s1","value":"x"},{"op":"replace","path":"/s2","value":"written"}]`,
			str("x"))
		if got.GetS_2() != "written" {
			t.Errorf("= %v", got)
		}
	})

	t.Run("cross-container move", func(t *testing.T) {
		// Location carries its own path, so this is expressible now; the
		// previous implementation rejected it as "cross-container".
		in := str("val")
		in.SetM_1(&sample.Value{})
		got := mustConvertAndApply(t, `[{"op":"move","from":"/s1","path":"/m1/s2"}]`, in)
		if got.GetM_1().GetS_2() != "val" || got.GetS_1() != "" {
			t.Errorf("= %v", got)
		}
	})

	t.Run("cross-container copy to the end of a list", func(t *testing.T) {
		// Three restrictions used to compose into inexpressibility here:
		// append was insert-only, assign could not grow a list, and the source
		// had to be a sibling.
		in := str("val")
		got := mustConvertAndApply(t, `[{"op":"copy","from":"/s1","path":"/rS1/-"}]`, in)
		if len(got.GetRS_1()) != 1 || got.GetRS_1()[0] != "val" {
			t.Errorf("= %v", got.GetRS_1())
		}
	})

	t.Run("null clears rather than writing a null", func(t *testing.T) {
		// The format has no null, so the operation a null write meant is
		// remove.
		got := mustConvertAndApply(t, `[{"op":"replace","path":"/s1","value":null}]`, str("x"))
		if got.GetS_1() != "" {
			t.Errorf("= %q", got.GetS_1())
		}
	})

	t.Run("test null asserts absence", func(t *testing.T) {
		if _, err := convertAndApply(t, `[{"op":"test","path":"/s1","value":null}]`, &sample.Value{}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		_, err := convertAndApply(t, `[{"op":"test","path":"/s1","value":null}]`, str("x"))
		if patch.CodeOf(err) != patch.CodeTestFailed {
			t.Errorf("CodeOf = %v, want CodeTestFailed", patch.CodeOf(err))
		}
	})
}

func TestConvertRejectsWhatItCannotType(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"unknown field", `[{"op":"replace","path":"/nope","value":"x"}]`},
		{"a non-numeric index", `[{"op":"replace","path":"/rS1/x","value":"y"}]`},
		{"descending into a scalar", `[{"op":"replace","path":"/s1/deeper","value":"y"}]`},
		{"the root", `[{"op":"replace","path":"","value":{}}]`},
		{"an unknown op", `[{"op":"frobnicate","path":"/s1","value":"y"}]`},
		{"a value of the wrong shape", `[{"op":"replace","path":"/i321","value":"not a number"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := jsonpatch.Unmarshal([]byte(tt.doc))
			if err != nil {
				return // rejected at parse, which is also fine
			}
			if _, err := jsonpatch.Convert(d, sampleDesc); err == nil {
				t.Fatal("converted; the descriptor cannot type this")
			}
		})
	}
}

func str(s string) *sample.Value {
	m := &sample.Value{}
	m.SetS_1(s)
	return m
}

func list(ss ...string) *sample.Value {
	m := &sample.Value{}
	m.SetRS_1(ss)
	return m
}

func nested(s string) *sample.Value {
	inner := &sample.Value{}
	inner.SetS_1(s)
	m := &sample.Value{}
	m.SetM_1(inner)
	return m
}
