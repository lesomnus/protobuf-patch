package patchstruct_test

import (
	"reflect"
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchstruct"
)

const mt = "example.Config"

type Inner struct {
	Note string `json:"note"`
}

type Base struct {
	Shared string `json:"shared"`
}

// Config exercises the surface a hand-written struct actually has: json tags
// that differ from the Go names, a pointer for presence, the narrow integer
// widths, a byte slice, maps with several key types, and an embedded struct.
type Config struct {
	Name    string  `json:"name"`
	Retries int32   `json:"retries"`
	Big     int64   `json:"big"`
	Small   int8    `json:"small"`
	Count   uint32  `json:"count"`
	Ratio   float64 `json:"ratio"`
	Enabled bool    `json:"enabled"`
	Token   []byte  `json:"token"`

	Opt *string `json:"opt"`

	Tags   []string          `json:"tags"`
	Labels map[string]string `json:"labels"`
	Ports  map[int32]string  `json:"ports"`
	Flags  map[bool]string   `json:"flags"`

	Sub  Inner   `json:"sub"`
	Subs []Inner `json:"subs"`

	Base

	hidden string
}

func apply(t *testing.T, in Config, ops ...patch.Op) (Config, error) {
	t.Helper()
	p, err := patch.New(mt, ops[0], ops[1:]...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return patchstruct.Apply(in, p)
}

func mustApply(t *testing.T, in Config, ops ...patch.Op) Config {
	t.Helper()
	got, err := apply(t, in, ops...)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return got
}

func wantErr(t *testing.T, in Config, code patch.Code, ops ...patch.Op) {
	t.Helper()
	before := deepEqualCopy(in)
	_, err := apply(t, in, ops...)
	if got := patch.CodeOf(err); got != code {
		t.Fatalf("CodeOf = %v, want %v (err = %v)", got, code, err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Error("a failed Apply modified its input; the contract is atomic")
	}
}

func deepEqualCopy(c Config) Config {
	out := c
	out.Tags = append([]string(nil), c.Tags...)
	out.Subs = append([]Inner(nil), c.Subs...)
	out.Labels = map[string]string{}
	for k, v := range c.Labels {
		out.Labels[k] = v
	}
	if c.Labels == nil {
		out.Labels = nil
	}
	return out
}

// ------------------------------------------------------------ addressing

func TestFieldNamingFollowsProtobufsSplit(t *testing.T) {
	// Field.name is the Go field name and Field.json_name is the json tag, the
	// same split protobuf makes. A Field pinning both requires them to describe
	// the same field, which is what keeps Field a constraint rather than a
	// fallback.
	t.Run("by Go name", func(t *testing.T) {
		got := mustApply(t, Config{}, patch.Target(patch.Name("Name")).Assign(patch.Str("v")))
		if got.Name != "v" {
			t.Errorf("= %q", got.Name)
		}
	})
	t.Run("by json tag", func(t *testing.T) {
		got := mustApply(t, Config{}, patch.Target(patch.JSONName("name")).Assign(patch.Str("v")))
		if got.Name != "v" {
			t.Errorf("= %q", got.Name)
		}
	})
	t.Run("both, agreeing", func(t *testing.T) {
		got := mustApply(t, Config{},
			patch.Target(patch.Name("Name").JSONName("name")).Assign(patch.Str("v")))
		if got.Name != "v" {
			t.Errorf("= %q", got.Name)
		}
	})
	t.Run("both, naming different fields", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeFieldConflict,
			patch.Target(patch.Name("Name").JSONName("note")).Assign(patch.Str("v")))
	})
	t.Run("a field number cannot be checked", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeIllegalArm,
			patch.Target(patch.Num(1)).Assign(patch.Str("v")))
	})
	t.Run("an unexported field names nothing", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeVacantTarget,
			patch.Target(patch.Name("hidden")).Assign(patch.Str("v")))
	})
	t.Run("an embedded field is addressed by its type name", func(t *testing.T) {
		// Not flattened: encoding/json's promotion brings name collisions and
		// depth rules, and Base is reachable as an ordinary field.
		got := mustApply(t, Config{},
			patch.Target(patch.Name("Shared")).In(patch.Name("Base")).Assign(patch.Str("v")))
		if got.Base.Shared != "v" {
			t.Errorf("= %q", got.Base.Shared)
		}
		wantErr(t, Config{}, patch.CodeVacantTarget,
			patch.Target(patch.Name("Shared")).Assign(patch.Str("v")))
	})
}

// ------------------------------------------------------------ types

// TestArmsMatchGoTypes is what a struct backend can do and a JSON one cannot:
// Go's static types make the format's arm table checkable.
func TestArmsMatchGoTypes(t *testing.T) {
	fits := map[string]patch.Value{
		"Name":    patch.Str("v"),
		"Retries": patch.Int32(1),
		"Big":     patch.Int64(1),
		"Small":   patch.Int32(1),
		"Count":   patch.Uint32(1),
		"Ratio":   patch.Float64(1),
		"Enabled": patch.Bool(true),
		"Token":   patch.Bytes([]byte{1}),
	}
	all := []patch.Value{
		patch.Str("v"), patch.Int32(1), patch.Int64(1), patch.Uint32(1),
		patch.Uint64(1), patch.Float32(1), patch.Float64(1), patch.Bool(true),
		patch.Bytes([]byte{1}),
	}

	for field, want := range fits {
		t.Run(field, func(t *testing.T) {
			if _, err := apply(t, Config{}, patch.Target(patch.Name(field)).Assign(want)); err != nil {
				t.Fatalf("the fitting arm was rejected: %v", err)
			}
			fitting := patch.MustNew(mt, patch.Target(patch.Name(field)).Assign(want)).
				GetDelta().GetEntries()[0].GetAssign().GetValue().WhichKind()

			for _, other := range all {
				p := patch.MustNew(mt, patch.Target(patch.Name(field)).Assign(other))
				if p.GetDelta().GetEntries()[0].GetAssign().GetValue().WhichKind() == fitting {
					continue
				}
				if _, err := patchstruct.Apply(Config{}, p); err == nil {
					t.Errorf("%s accepted a different arm; there is no conversion", field)
				}
			}
		})
	}
}

func TestNarrowIntegersAreRangeChecked(t *testing.T) {
	// Nothing is truncated: an i32 outside int8 is an error, not a wrap.
	got := mustApply(t, Config{}, patch.Target(patch.Name("Small")).Assign(patch.Int32(127)))
	if got.Small != 127 {
		t.Errorf("= %d", got.Small)
	}
	wantErr(t, Config{}, patch.CodeIllegalArm, patch.Target(patch.Name("Small")).Assign(patch.Int32(128)))
	wantErr(t, Config{}, patch.CodeIllegalArm, patch.Target(patch.Name("Count")).Assign(patch.Uint64(1<<40)))
}

func TestEnumIsRefused(t *testing.T) {
	// A Go type carries no set of declared values, so the closed-enum rule
	// could not be checked and the arm is refused rather than written as a
	// bare number.
	wantErr(t, Config{}, patch.CodeIllegalArm, patch.Target(patch.Name("Retries")).Assign(patch.Enum(1)))
}

func TestMapKeyTypesAreChecked(t *testing.T) {
	// The declared key type is visible here, which is the other thing JSON
	// cannot do.
	t.Run("string keys", func(t *testing.T) {
		got := mustApply(t, Config{Labels: map[string]string{}},
			patch.Target(patch.MapStr("k")).In(patch.Name("Labels")).Assign(patch.Str("v")))
		if got.Labels["k"] != "v" {
			t.Errorf("= %v", got.Labels)
		}
	})
	t.Run("int keys", func(t *testing.T) {
		got := mustApply(t, Config{Ports: map[int32]string{}},
			patch.Target(patch.MapInt(80)).In(patch.Name("Ports")).Assign(patch.Str("http")))
		if got.Ports[80] != "http" {
			t.Errorf("= %v", got.Ports)
		}
	})
	t.Run("bool keys", func(t *testing.T) {
		got := mustApply(t, Config{Flags: map[bool]string{}},
			patch.Target(patch.MapBool(true)).In(patch.Name("Flags")).Assign(patch.Str("on")))
		if got.Flags[true] != "on" {
			t.Errorf("= %v", got.Flags)
		}
	})
	t.Run("a string key against an int map", func(t *testing.T) {
		wantErr(t, Config{Ports: map[int32]string{}}, patch.CodeIllegalArm,
			patch.Target(patch.MapStr("80")).In(patch.Name("Ports")).Assign(patch.Str("http")))
	})
	t.Run("an int key out of the declared range", func(t *testing.T) {
		wantErr(t, Config{Ports: map[int32]string{}}, patch.CodeMapKeyOutOfRange,
			patch.Target(patch.MapInt(1<<40)).In(patch.Name("Ports")).Assign(patch.Str("x")))
	})
}

// ------------------------------------------------------------ presence

func TestPresenceFollowsThePointer(t *testing.T) {
	// A pointer field spells explicit presence; a plain field has none, so it
	// reads as absent at its zero value. The same reading protobuf gives.
	s := "held"

	t.Run("a nil pointer is absent", func(t *testing.T) {
		mustApply(t, Config{}, patch.Target(patch.Name("Opt")).Exists(false))
	})
	t.Run("a set pointer is present", func(t *testing.T) {
		mustApply(t, Config{Opt: &s}, patch.Target(patch.Name("Opt")).Exists(true))
	})
	t.Run("a zero plain field is absent", func(t *testing.T) {
		mustApply(t, Config{}, patch.Target(patch.Name("Name")).Exists(false))
	})
	t.Run("a non-zero plain field is present", func(t *testing.T) {
		mustApply(t, Config{Name: "x"}, patch.Target(patch.Name("Name")).Exists(true))
	})
	t.Run("insert refuses an occupied pointer", func(t *testing.T) {
		wantErr(t, Config{Opt: &s}, patch.CodeOccupied,
			patch.Target(patch.Name("Opt")).Insert(patch.Str("v")))
	})
	t.Run("insert fills a nil pointer", func(t *testing.T) {
		got := mustApply(t, Config{}, patch.Target(patch.Name("Opt")).Insert(patch.Str("v")))
		if got.Opt == nil || *got.Opt != "v" {
			t.Errorf("= %v", got.Opt)
		}
	})
	t.Run("remove nils a pointer", func(t *testing.T) {
		got := mustApply(t, Config{Opt: &s}, patch.Target(patch.Name("Opt")).Remove())
		if got.Opt != nil {
			t.Errorf("= %v", *got.Opt)
		}
	})
}

// ------------------------------------------------------------ containers

func TestSliceOps(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		op   patch.Op
		want []string
	}{
		{"remove one", []string{"a", "b", "c"},
			patch.Target(patch.Index(1)).In(patch.Name("Tags")).Remove(), []string{"a", "c"}},
		{"remove two, resolved pre-entry", []string{"a", "b", "c"},
			patch.Target(patch.Index(0), patch.Index(2)).In(patch.Name("Tags")).Remove(), []string{"b"}},
		{"negative index", []string{"a", "b"},
			patch.Target(patch.Index(-1)).In(patch.Name("Tags")).Assign(patch.Str("Z")), []string{"a", "Z"}},
		{"insert at two indices", []string{"a", "b", "c"},
			patch.Target(patch.Index(0), patch.Index(2)).In(patch.Name("Tags")).Insert(patch.Str("Z")),
			[]string{"Z", "a", "b", "Z", "c"}},
		{"append", []string{"a"},
			patch.Target(patch.Append()).In(patch.Name("Tags")).Insert(patch.Str("Z")), []string{"a", "Z"}},
		{"range", []string{"a", "b", "c", "d", "e"},
			patch.Target(patch.Span(1, 4)).In(patch.Name("Tags")).Remove(), []string{"a", "e"}},
		{"whole slice assign", []string{"a"},
			patch.Target(patch.Name("Tags")).Assign(patch.List(patch.Str("x"), patch.Str("y"))),
			[]string{"x", "y"}},
		{"a nil slice can be filled", nil,
			patch.Target(patch.Name("Tags")).Assign(patch.List(patch.Str("first"))),
			[]string{"first"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustApply(t, Config{Tags: tt.in}, tt.op)
			if !reflect.DeepEqual(got.Tags, tt.want) {
				t.Errorf("= %v, want %v", got.Tags, tt.want)
			}
		})
	}
}

func TestNestedStructs(t *testing.T) {
	t.Run("through a path", func(t *testing.T) {
		got := mustApply(t, Config{},
			patch.Target(patch.Name("Note")).In(patch.Name("Sub")).Assign(patch.Str("v")))
		if got.Sub.Note != "v" {
			t.Errorf("= %q", got.Sub.Note)
		}
	})
	t.Run("nest into a struct field", func(t *testing.T) {
		got := mustApply(t, Config{},
			patch.Target(patch.Name("Sub")).Nest(patch.Target(patch.Name("Note")).Assign(patch.Str("v"))))
		if got.Sub.Note != "v" {
			t.Errorf("= %q", got.Sub.Note)
		}
	})
	t.Run("through a slice element", func(t *testing.T) {
		got := mustApply(t, Config{Subs: []Inner{{Note: "was"}}},
			patch.Target(patch.Name("Note")).In(patch.Name("Subs"), patch.Index(0)).Assign(patch.Str("now")))
		if got.Subs[0].Note != "now" {
			t.Errorf("= %v", got.Subs)
		}
	})
	t.Run("assign a whole struct", func(t *testing.T) {
		got := mustApply(t, Config{},
			patch.Target(patch.Name("Sub")).Assign(patch.Msg(patch.F(patch.Name("Note"), patch.Str("v")))))
		if got.Sub.Note != "v" {
			t.Errorf("= %v", got.Sub)
		}
	})
	t.Run("a value naming an undeclared field", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeVacantTarget,
			patch.Target(patch.Name("Sub")).Assign(patch.Msg(patch.F(patch.Name("Nope"), patch.Str("v")))))
	})
}

// ------------------------------------------------------------ contract

func TestApplyIsAtomic(t *testing.T) {
	in := Config{Name: "orig", Tags: []string{"a"}}
	_, err := apply(t, in,
		patch.Target(patch.Name("Name")).Assign(patch.Str("changed")),
		patch.Target(patch.Name("Retries")).Test(patch.Int32(42)),
	)
	if patch.CodeOf(err) != patch.CodeTestFailed {
		t.Fatalf("CodeOf = %v, want CodeTestFailed", patch.CodeOf(err))
	}
	if in.Name != "orig" {
		t.Errorf("input changed: %q", in.Name)
	}
}

func TestTheResultDoesNotShareState(t *testing.T) {
	in := Config{Tags: []string{"a"}, Labels: map[string]string{"k": "v"}, Subs: []Inner{{Note: "n"}}}
	got := mustApply(t, in, patch.Target(patch.Name("Name")).Assign(patch.Str("x")))

	got.Tags[0] = "mutated"
	got.Labels["k"] = "mutated"
	got.Subs[0].Note = "mutated"

	if in.Tags[0] != "a" || in.Labels["k"] != "v" || in.Subs[0].Note != "n" {
		t.Errorf("the copy aliased the input: %v", in)
	}
}

func TestUnexportedFieldsSurvive(t *testing.T) {
	// A Patch never disturbs what it cannot name.
	in := Config{Name: "x", hidden: "keep"}
	got := mustApply(t, in, patch.Container().Remove())
	if got.hidden != "keep" {
		t.Errorf("hidden = %q; a container remove reached what a Patch cannot address", got.hidden)
	}
	if got.Name != "" {
		t.Error("the exported field was not cleared")
	}
}

func TestMissingTargets(t *testing.T) {
	t.Run("an undeclared field fails", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeVacantTarget, patch.Target(patch.Name("Nope")).Remove())
	})
	t.Run("and is skippable when asked", func(t *testing.T) {
		mustApply(t, Config{}, patch.Target(patch.Name("Nope")).Skip().Remove())
	})
	t.Run("removing an absent map key fails", func(t *testing.T) {
		wantErr(t, Config{Labels: map[string]string{}}, patch.CodeVacantTarget,
			patch.Target(patch.MapStr("gone")).In(patch.Name("Labels")).Remove())
	})
	t.Run("assigning an absent map key creates it", func(t *testing.T) {
		got := mustApply(t, Config{Labels: map[string]string{}},
			patch.Target(patch.MapStr("new")).In(patch.Name("Labels")).Assign(patch.Str("v")))
		if got.Labels["new"] != "v" {
			t.Errorf("= %v", got.Labels)
		}
	})
	t.Run("removing a declared but zero field succeeds", func(t *testing.T) {
		// A struct field always exists as a position, which is exactly what
		// patchjson cannot tell and this engine can.
		mustApply(t, Config{}, patch.Target(patch.Name("Name")).Remove())
	})
}

func TestMoveAndCopy(t *testing.T) {
	t.Run("move clears the source", func(t *testing.T) {
		got := mustApply(t, Config{Name: "v"},
			patch.Target(patch.Name("Shared")).In(patch.Name("Base")).Move(patch.From(patch.Name("Name"))))
		if got.Base.Shared != "v" || got.Name != "" {
			t.Errorf("= %+v", got)
		}
	})
	t.Run("types must match", func(t *testing.T) {
		wantErr(t, Config{Name: "v"}, patch.CodeTypeMismatch,
			patch.Target(patch.Name("Retries")).Copy(patch.Here(patch.Name("Name"))))
	})
	t.Run("same type moves", func(t *testing.T) {
		got := mustApply(t, Config{Name: "v"},
			patch.Target(patch.Name("Shared")).In(patch.Name("Base")).Copy(patch.From(patch.Name("Name"))))
		if got.Base.Shared != "v" || got.Name != "v" {
			t.Errorf("= %+v", got)
		}
	})
	t.Run("moving onto itself is a no-op", func(t *testing.T) {
		got := mustApply(t, Config{Name: "v"},
			patch.Target(patch.Name("Name")).Move(patch.Here(patch.Name("Name"))))
		if got.Name != "v" {
			t.Errorf("= %q", got.Name)
		}
	})
	t.Run("an absent source fails", func(t *testing.T) {
		wantErr(t, Config{}, patch.CodeSourceUnresolved,
			patch.Target(patch.Name("Big")).Move(patch.Here(patch.Name("Nope"))))
	})
}

func TestExpectType(t *testing.T) {
	p := patch.MustNew("example.Config", patch.Target(patch.Name("Name")).Remove())
	if _, err := patchstruct.Apply(Config{}, p); err != nil {
		t.Fatalf("unchecked by default: %v", err)
	}
	if _, err := patchstruct.Apply(Config{}, p, patchstruct.ExpectType("example.Config")); err != nil {
		t.Fatalf("with the right name: %v", err)
	}
	_, err := patchstruct.Apply(Config{}, p, patchstruct.ExpectType("other.Thing"))
	if patch.CodeOf(err) != patch.CodeMessageTypeMismatch {
		t.Fatalf("CodeOf = %v, want CodeMessageTypeMismatch", patch.CodeOf(err))
	}
}
