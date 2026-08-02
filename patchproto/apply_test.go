package patchproto_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

const mt = "sample.Value"

func v(mods ...func(*sample.Value)) *sample.Value {
	m := &sample.Value{}
	for _, f := range mods {
		f(m)
	}
	return m
}

func apply(t *testing.T, in *sample.Value, ops ...patch.Op) (*sample.Value, error) {
	t.Helper()
	p, err := patch.New(mt, ops[0], ops[1:]...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return patchproto.Apply(in, p)
}

func mustApply(t *testing.T, in *sample.Value, ops ...patch.Op) *sample.Value {
	t.Helper()
	got, err := apply(t, in, ops...)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return got
}

func wantErr(t *testing.T, in *sample.Value, code patch.Code, ops ...patch.Op) {
	t.Helper()
	before := proto.Clone(in)
	_, err := apply(t, in, ops...)
	if got := patch.CodeOf(err); got != code {
		t.Fatalf("CodeOf = %v, want %v (err = %v)", got, code, err)
	}
	if !proto.Equal(in, before) {
		t.Error("a failed Apply modified its input; the contract is atomic")
	}
}

// ------------------------------------------------------------ atomicity

// TestApplyIsAtomic is the contract the whole applier is shaped around. Full
// pre-validation cannot deliver it, because an entry's legality can depend on
// what an earlier entry produced -- so Apply works on a copy and publishes it
// only on success.
func TestApplyIsAtomic(t *testing.T) {
	t.Run("a later failure undoes earlier entries", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("orig") })
		before := proto.Clone(in)

		_, err := apply(t, in,
			patch.Target(patch.Name("s_1")).Assign(patch.Str("changed")),
			patch.Target(patch.Name("i64_1")).Test(patch.Int64(42)),
		)
		if patch.CodeOf(err) != patch.CodeTestFailed {
			t.Fatalf("CodeOf = %v, want CodeTestFailed", patch.CodeOf(err))
		}
		if !proto.Equal(in, before) {
			t.Errorf("input changed to %v; the guard fired but the mutation survived", in)
		}
	})

	t.Run("the result is not the input", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("orig") })
		got := mustApply(t, in, patch.Target(patch.Name("s_1")).Assign(patch.Str("new")))

		if in.GetS_1() != "orig" {
			t.Errorf("input mutated in place: %q", in.GetS_1())
		}
		if got.GetS_1() != "new" {
			t.Errorf("result = %q, want new", got.GetS_1())
		}
	})

	t.Run("a test-only patch never mutates", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("x") })
		got := mustApply(t, in,
			patch.Target(patch.Name("s_1")).Test(patch.Str("x")),
			patch.Target(patch.Name("s_2")).Exists(false),
		)
		if !proto.Equal(in, got) {
			t.Errorf("a patch of only assertions changed the message: %v", got)
		}
	})
}

func TestMessageTypeMustMatch(t *testing.T) {
	p := patch.MustNew("some.other.Message", patch.Target(patch.Name("s_1")).Remove())
	_, err := patchproto.Apply(&sample.Value{}, p)
	if patch.CodeOf(err) != patch.CodeMessageTypeMismatch {
		t.Fatalf("CodeOf = %v, want CodeMessageTypeMismatch", patch.CodeOf(err))
	}
}

// ------------------------------------------------------------ message fields

func TestMessageFieldOps(t *testing.T) {
	t.Run("remove", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("x"); m.SetS_2("y") })
		got := mustApply(t, in, patch.Target(patch.Name("s_1")).Remove())
		if got.GetS_1() != "" || got.GetS_2() != "y" {
			t.Errorf("= %v", got)
		}
	})

	t.Run("assign overwrites", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("old") })
		got := mustApply(t, in, patch.Target(patch.Name("s_1")).Assign(patch.Str("new")))
		if got.GetS_1() != "new" {
			t.Errorf("= %q", got.GetS_1())
		}
	})

	t.Run("insert creates", func(t *testing.T) {
		got := mustApply(t, v(), patch.Target(patch.Name("s_1")).Insert(patch.Str("new")))
		if got.GetS_1() != "new" {
			t.Errorf("= %q", got.GetS_1())
		}
	})

	t.Run("insert refuses an occupied field", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("held") })
		wantErr(t, in, patch.CodeOccupied, patch.Target(patch.Name("s_1")).Insert(patch.Str("new")))
	})

	t.Run("multiple targets", func(t *testing.T) {
		got := mustApply(t, v(),
			patch.Target(patch.Name("s_1"), patch.Name("s_2"), patch.Name("s_3")).Assign(patch.Str("z")),
		)
		if got.GetS_1() != "z" || got.GetS_2() != "z" || got.GetS_3() != "z" {
			t.Errorf("= %v", got)
		}
	})

	t.Run("a vacant field fails by default", func(t *testing.T) {
		wantErr(t, v(), patch.CodeVacantTarget, patch.Target(patch.Name("nope")).Remove())
	})

	t.Run("skip tolerates a vacant field", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("x") })
		got := mustApply(t, in, patch.Target(patch.Name("nope")).Skip().Remove())
		if got.GetS_1() != "x" {
			t.Errorf("= %v", got)
		}
	})

	t.Run("a duplicate target is an error", func(t *testing.T) {
		wantErr(t, v(), patch.CodeDuplicateTarget,
			patch.Target(patch.Name("s_1"), patch.Num(109)).Assign(patch.Str("z")),
		)
	})

	t.Run("a field conflict is never skipped", func(t *testing.T) {
		// on_missing tolerates vacancy, not a Patch written against another
		// schema. s_1 is field 109, so this pairing cannot be right.
		wantErr(t, v(), patch.CodeFieldConflict,
			patch.Target(patch.Name("s_1").Num(209)).Skip().Remove(),
		)
	})
}

func TestMessageValueAssign(t *testing.T) {
	in := v(func(m *sample.Value) { m.SetS_1("outer") })
	got := mustApply(t, in,
		patch.Target(patch.Name("m_1")).Assign(patch.Msg(
			patch.F(patch.Name("s_1"), patch.Str("inner")),
			patch.F(patch.Name("i32_1"), patch.Int32(7)),
		)),
	)
	if got.GetM_1().GetS_1() != "inner" || got.GetM_1().GetI32_1() != 7 {
		t.Errorf("= %v", got.GetM_1())
	}
	if got.GetS_1() != "outer" {
		t.Errorf("outer field disturbed: %q", got.GetS_1())
	}
}

func TestArmMismatchIsRefused(t *testing.T) {
	wantErr(t, v(), patch.CodeIllegalArm, patch.Target(patch.Name("s_1")).Assign(patch.Int32(1)))
	wantErr(t, v(), patch.CodeIllegalArm, patch.Target(patch.Name("i32_1")).Assign(patch.Str("1")))
	wantErr(t, v(), patch.CodeIllegalArm, patch.Target(patch.Name("i32_1")).Assign(patch.Int64(1)))
}

// ------------------------------------------------------------ lists

func list(ss ...string) *sample.Value {
	return v(func(m *sample.Value) { m.SetRS_1(ss) })
}

func TestListOps(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		op   patch.Op
		want []string
	}{
		{"remove one", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(1)).Remove()),
			[]string{"a", "c"}},
		{"remove several, resolved before the entry", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(0), patch.Index(2)).Remove()),
			[]string{"b"}},
		{"remove the last", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(-1)).Remove()),
			[]string{"a", "b"}},
		{"assign in place", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(-1)).Assign(patch.Str("Z"))),
			[]string{"a", "b", "Z"}},
		{"insert before", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(0)).Insert(patch.Str("Z"))),
			[]string{"Z", "a", "b", "c"}},
		{"insert at two indices", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(0), patch.Index(2)).Insert(patch.Str("Z"))),
			[]string{"Z", "a", "b", "Z", "c"}},
		{"append", []string{"a", "b", "c"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Append()).Insert(patch.Str("Z"))),
			[]string{"a", "b", "c", "Z"}},
		{"range removes a span", []string{"a", "b", "c", "d", "e"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Span(1, 4)).Remove()),
			[]string{"a", "e"}},
		{"open range from the end", []string{"a", "b", "c", "d", "e"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.SpanFrom(-2)).Remove()),
			[]string{"a", "b", "c"}},
		{"an empty range selects nothing", []string{"a", "b"},
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Span(1, 1)).Remove()),
			[]string{"a", "b"}},
		{"whole list assign", []string{"a"},
			patch.Target(patch.Name("r_s_1")).Assign(patch.List(patch.Str("x"), patch.Str("y"))),
			[]string{"x", "y"}},
		{"whole list at container scope", []string{"a"},
			patch.Container().In(patch.Name("r_s_1")).Assign(patch.List(patch.Str("x"))),
			[]string{"x"}},
		{"container insert appends", []string{"a"},
			patch.Container().In(patch.Name("r_s_1")).Insert(patch.List(patch.Str("b"))),
			[]string{"a", "b"}},
		{"container remove empties", []string{"a", "b"},
			patch.Container().In(patch.Name("r_s_1")).Remove(),
			nil},
		{"an empty list can still be addressed", nil,
			patch.Container().In(patch.Name("r_s_1")).Assign(patch.List(patch.Str("first"))),
			[]string{"first"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustApply(t, list(tt.in...), tt.op)
			if !equalStrings(got.GetRS_1(), tt.want) {
				t.Errorf("= %v, want %v", got.GetRS_1(), tt.want)
			}
		})
	}
}

func TestListIndexOutOfRange(t *testing.T) {
	wantErr(t, list("a"), patch.CodeVacantTarget,
		patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(5)).Remove()),
	)
}

// ------------------------------------------------------------ maps

func strMap(kv map[string]string) *sample.Value {
	return v(func(m *sample.Value) { m.SetMSS(kv) })
}

func TestMapOps(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		op   patch.Op
		want map[string]string
	}{
		{"assign creates", map[string]string{},
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Assign(patch.Str("v"))),
			map[string]string{"k": "v"}},
		{"assign overwrites", map[string]string{"k": "old"},
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Assign(patch.Str("new"))),
			map[string]string{"k": "new"}},
		{"remove", map[string]string{"k": "v", "j": "w"},
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Remove()),
			map[string]string{"j": "w"}},
		{"insert creates", map[string]string{},
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Insert(patch.Str("v"))),
			map[string]string{"k": "v"}},
		{"whole map assign", map[string]string{"old": "x"},
			patch.Target(patch.Name("m_s_s")).Assign(patch.Map(patch.E(patch.MapStr("a"), patch.Str("1")))),
			map[string]string{"a": "1"}},
		{"container remove empties", map[string]string{"a": "1"},
			patch.Container().In(patch.Name("m_s_s")).Remove(),
			map[string]string{}},
		{"the empty string is a key", map[string]string{},
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("")).Assign(patch.Str("v"))),
			map[string]string{"": "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mustApply(t, strMap(tt.in), tt.op)
			if !equalMaps(got.GetMSS(), tt.want) {
				t.Errorf("= %v, want %v", got.GetMSS(), tt.want)
			}
		})
	}
}

func TestMapInsertRefusesOccupied(t *testing.T) {
	wantErr(t, strMap(map[string]string{"k": "v"}), patch.CodeOccupied,
		patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Insert(patch.Str("w"))),
	)
}

func TestMapMissingKey(t *testing.T) {
	wantErr(t, strMap(map[string]string{}), patch.CodeVacantTarget,
		patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Remove()),
	)
}

// ------------------------------------------------------------ test

func TestAssertions(t *testing.T) {
	held := v(func(m *sample.Value) {
		m.SetS_1("x")
		m.SetMSS(map[string]string{"k": "v"})
		m.SetRS_1([]string{"a"})
	})

	t.Run("value holds", func(t *testing.T) {
		mustApply(t, held, patch.Target(patch.Name("s_1")).Test(patch.Str("x")))
	})
	t.Run("value fails", func(t *testing.T) {
		wantErr(t, held, patch.CodeTestFailed, patch.Target(patch.Name("s_1")).Test(patch.Str("y")))
	})

	// The vacancy cases: exists=false has to be satisfiable for every container
	// kind, which is what the schema's vacant-location rule exists to allow.
	t.Run("absent message field", func(t *testing.T) {
		mustApply(t, held, patch.Target(patch.Name("s_2")).Exists(false))
	})
	t.Run("absent map key", func(t *testing.T) {
		mustApply(t, held,
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("gone")).Exists(false)),
		)
	})
	t.Run("present map key", func(t *testing.T) {
		mustApply(t, held,
			patch.Container().In(patch.Name("m_s_s")).Nest(patch.Target(patch.MapStr("k")).Exists(true)),
		)
	})
	t.Run("out-of-range index", func(t *testing.T) {
		mustApply(t, held,
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Index(9)).Exists(false)),
		)
	})
	t.Run("a vacant field is absent, not an error", func(t *testing.T) {
		mustApply(t, held, patch.Target(patch.Name("no_such_field")).Exists(false))
	})

	t.Run("container emptiness", func(t *testing.T) {
		mustApply(t, held, patch.Container().In(patch.Name("m_s_s")).Exists(true))
		wantErr(t, held, patch.CodeTestFailed, patch.Container().In(patch.Name("m_s_s")).Exists(false))
	})

	t.Run("a test whose selectors name nothing cannot pass", func(t *testing.T) {
		// An empty range selects zero locations, so the assertion asserts
		// nothing -- which must not be reported as holding.
		wantErr(t, held, patch.CodeTestVacuous,
			patch.Container().In(patch.Name("r_s_1")).Nest(patch.Target(patch.Span(1, 1)).Exists(true)),
		)
	})
}

// ------------------------------------------------------------ move and copy

func TestMoveAndCopy(t *testing.T) {
	t.Run("move clears the source", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("val") })
		got := mustApply(t, in, patch.Target(patch.Name("s_2")).Move(patch.Here(patch.Name("s_1"))))
		if got.GetS_2() != "val" || got.GetS_1() != "" {
			t.Errorf("s_1=%q s_2=%q", got.GetS_1(), got.GetS_2())
		}
	})

	t.Run("copy leaves the source", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("val") })
		got := mustApply(t, in, patch.Target(patch.Name("s_2")).Copy(patch.Here(patch.Name("s_1"))))
		if got.GetS_2() != "val" || got.GetS_1() != "val" {
			t.Errorf("s_1=%q s_2=%q", got.GetS_1(), got.GetS_2())
		}
	})

	t.Run("moving onto itself is a no-op", func(t *testing.T) {
		// RFC 6902 4.4. The previous implementation deleted the field instead.
		in := v(func(m *sample.Value) { m.SetS_1("val") })
		got := mustApply(t, in, patch.Target(patch.Name("s_1")).Move(patch.Here(patch.Name("s_1"))))
		if got.GetS_1() != "val" {
			t.Errorf("s_1 = %q, want it left alone", got.GetS_1())
		}
	})

	t.Run("an absent source is an error", func(t *testing.T) {
		// Not a silent clear of the destination, which is what the previous
		// implementation did.
		in := v(func(m *sample.Value) { m.SetS_3("keep") })
		wantErr(t, in, patch.CodeSourceUnresolved,
			patch.Target(patch.Name("s_3")).Move(patch.Here(patch.Name("s_1"))),
		)
	})

	t.Run("order does not change the outcome", func(t *testing.T) {
		// The source is read before any target is written and cleared after
		// all of them, so both permutations agree.
		mk := func(first, second patch.Keyer) *sample.Value {
			in := v(func(m *sample.Value) { m.SetS_1("A") })
			return mustApply(t, in, patch.Target(first, second).Move(patch.Here(patch.Name("s_1"))))
		}
		a := mk(patch.Name("s_2"), patch.Name("s_3"))
		b := mk(patch.Name("s_3"), patch.Name("s_2"))
		if !proto.Equal(a, b) {
			t.Errorf("permutations disagree:\n %v\n %v", a, b)
		}
		if a.GetS_1() != "" || a.GetS_2() != "A" || a.GetS_3() != "A" {
			t.Errorf("= %v", a)
		}
	})

	t.Run("across containers", func(t *testing.T) {
		in := v(func(m *sample.Value) {
			m.SetS_1("val")
			m.SetM_1(&sample.Value{})
		})
		got := mustApply(t, in,
			patch.Target(patch.Name("s_2")).In(patch.Name("m_1")).Copy(patch.From(patch.Name("s_1"))),
		)
		if got.GetM_1().GetS_2() != "val" {
			t.Errorf("= %v", got.GetM_1())
		}
	})

	t.Run("kind must match", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetS_1("val") })
		wantErr(t, in, patch.CodeTypeMismatch,
			patch.Target(patch.Name("i32_1")).Copy(patch.Here(patch.Name("s_1"))),
		)
	})

	t.Run("copying a message does not alias it", func(t *testing.T) {
		in := v(func(m *sample.Value) {
			inner := &sample.Value{}
			inner.SetS_1("deep")
			m.SetM_1(inner)
		})
		got := mustApply(t, in,
			patch.Target(patch.Name("m_2")).Copy(patch.Here(patch.Name("m_1"))),
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("changed")),
		)
		if got.GetM_2().GetS_1() != "deep" {
			t.Errorf("the copy aliased the source: %v", got.GetM_2())
		}
	})
}

// ------------------------------------------------------------ nest and paths

func TestNestAndPaths(t *testing.T) {
	t.Run("nest into a message field", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetM_1(&sample.Value{}) })
		got := mustApply(t, in,
			patch.Target(patch.Name("m_1")).Nest(patch.Target(patch.Name("s_1")).Assign(patch.Str("deep"))),
		)
		if got.GetM_1().GetS_1() != "deep" {
			t.Errorf("= %v", got.GetM_1())
		}
	})

	t.Run("path into a message field", func(t *testing.T) {
		in := v(func(m *sample.Value) { m.SetM_1(&sample.Value{}) })
		got := mustApply(t, in,
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("deep")),
		)
		if got.GetM_1().GetS_1() != "deep" {
			t.Errorf("= %v", got.GetM_1())
		}
	})

	t.Run("a path never creates a message field", func(t *testing.T) {
		wantErr(t, v(), patch.CodePathNotReached,
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("deep")),
		)
	})

	t.Run("on_missing does not reach a path", func(t *testing.T) {
		wantErr(t, v(), patch.CodePathNotReached,
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Skip().Assign(patch.Str("deep")),
		)
	})

	t.Run("path through a map value", func(t *testing.T) {
		inner := &sample.Value{}
		inner.SetS_1("was")
		in := v(func(m *sample.Value) { m.SetMSM(map[string]*sample.Value{"k": inner}) })

		got := mustApply(t, in,
			patch.Target(patch.Name("s_1")).In(patch.Name("m_s_m"), patch.MapStr("k")).Assign(patch.Str("now")),
		)
		if got.GetMSM()["k"].GetS_1() != "now" {
			t.Errorf("= %v", got.GetMSM())
		}
	})
}

// ------------------------------------------------------------ helpers

func TestUnknownFieldsOnTheTargetSurvive(t *testing.T) {
	// A Patch never discards data it did not name, and unknown fields are not
	// addressable by any Key -- so a container-scope remove must leave them.
	in := v(func(m *sample.Value) { m.SetS_1("x") })
	setUnknownOn(in, 4242)

	got := mustApply(t, in, patch.Container().Remove())
	if len(got.ProtoReflect().GetUnknown()) == 0 {
		t.Error("clearing the container discarded unknown fields")
	}
	if got.GetS_1() != "" {
		t.Error("the declared field was not cleared")
	}
}

func setUnknownOn(m proto.Message, num int32) {
	b := []byte{byte(num<<3|0) & 0x7f}
	_ = b
	// 4242 needs a multi-byte tag; build it properly.
	tag := uint64(num)<<3 | 0
	var raw []byte
	for tag >= 0x80 {
		raw = append(raw, byte(tag)|0x80)
		tag >>= 7
	}
	raw = append(raw, byte(tag), 1)
	m.ProtoReflect().SetUnknown(raw)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

var _ = patchpb.Patch{}
