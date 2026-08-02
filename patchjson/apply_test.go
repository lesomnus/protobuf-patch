package patchjson_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchjson"
)

const mt = "anything"

func apply(t *testing.T, doc string, ops ...patch.Op) (string, error) {
	t.Helper()
	p, err := patch.New(mt, ops[0], ops[1:]...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := patchjson.Apply([]byte(doc), p)
	return string(out), err
}

func mustApply(t *testing.T, doc string, ops ...patch.Op) string {
	t.Helper()
	got, err := apply(t, doc, ops...)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return got
}

// same compares two JSON texts by value, so member order and spacing do not
// enter into it.
func same(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func wantErr(t *testing.T, doc string, code patch.Code, ops ...patch.Op) {
	t.Helper()
	_, err := apply(t, doc, ops...)
	if got := patch.CodeOf(err); got != code {
		t.Fatalf("CodeOf = %v, want %v (err = %v)", got, code, err)
	}
}

// ------------------------------------------------------------ objects

func TestObjectIsAddressedLikeAMap(t *testing.T) {
	// With no schema every key is a valid position, so assign creates one and
	// insert requires it empty -- the map rules, not the message rules. This
	// single collapse is what makes the engine definable at all.
	t.Run("assign creates a member", func(t *testing.T) {
		same(t, mustApply(t, `{}`, patch.Target(patch.Name("a")).Assign(patch.Str("v"))),
			`{"a":"v"}`)
	})
	t.Run("assign overwrites a member", func(t *testing.T) {
		same(t, mustApply(t, `{"a":"old"}`, patch.Target(patch.Name("a")).Assign(patch.Str("new"))),
			`{"a":"new"}`)
	})
	t.Run("insert creates", func(t *testing.T) {
		same(t, mustApply(t, `{}`, patch.Target(patch.Name("a")).Insert(patch.Str("v"))),
			`{"a":"v"}`)
	})
	t.Run("insert refuses an occupied member", func(t *testing.T) {
		wantErr(t, `{"a":"held"}`, patch.CodeOccupied, patch.Target(patch.Name("a")).Insert(patch.Str("v")))
	})
	t.Run("remove", func(t *testing.T) {
		same(t, mustApply(t, `{"a":1,"b":2}`, patch.Target(patch.Name("a")).Remove()),
			`{"b":2}`)
	})
	t.Run("a map key addresses a member too", func(t *testing.T) {
		// A message field and a map entry are the same thing here, so refusing
		// one spelling would only make the author guess.
		same(t, mustApply(t, `{}`, patch.Target(patch.MapStr("a")).Assign(patch.Str("v"))),
			`{"a":"v"}`)
	})
}

// TestRemovingWhatIsNotThere is the case the author asked about: is removing
// something already absent a failure?
func TestRemovingWhatIsNotThere(t *testing.T) {
	t.Run("fails by default", func(t *testing.T) {
		wantErr(t, `{"a":1}`, patch.CodeVacantTarget, patch.Target(patch.Name("gone")).Remove())
	})
	t.Run("succeeds when the author asked for tolerance", func(t *testing.T) {
		same(t, mustApply(t, `{"a":1}`, patch.Target(patch.Name("gone")).Skip().Remove()),
			`{"a":1}`)
	})
	t.Run("tolerance does not extend to what cannot be interpreted", func(t *testing.T) {
		// on_missing forgives absence, not a construct this engine cannot read.
		wantErr(t, `{"a":1}`, patch.CodeIllegalArm, patch.Target(patch.Num(7)).Skip().Remove())
	})
}

// ------------------------------------------------------------ arrays

func TestArrayOps(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		op   patch.Op
		want string
	}{
		{"remove one", `["a","b","c"]`, patch.Target(patch.Index(1)).Remove(), `["a","c"]`},
		{"remove two, resolved pre-entry", `["a","b","c"]`,
			patch.Target(patch.Index(0), patch.Index(2)).Remove(), `["b"]`},
		{"negative index is the last", `["a","b","c"]`,
			patch.Target(patch.Index(-1)).Assign(patch.Str("Z")), `["a","b","Z"]`},
		{"insert before", `["a","b"]`,
			patch.Target(patch.Index(0)).Insert(patch.Str("Z")), `["Z","a","b"]`},
		{"insert at two indices", `["a","b","c"]`,
			patch.Target(patch.Index(0), patch.Index(2)).Insert(patch.Str("Z")),
			`["Z","a","b","Z","c"]`},
		{"append", `["a"]`, patch.Target(patch.Append()).Insert(patch.Str("Z")), `["a","Z"]`},
		{"range", `["a","b","c","d","e"]`, patch.Target(patch.Span(1, 4)).Remove(), `["a","e"]`},
		{"open range from the end", `["a","b","c","d","e"]`,
			patch.Target(patch.SpanFrom(-2)).Remove(), `["a","b","c"]`},
		{"an explicit empty range selects nothing", `["a","b"]`,
			patch.Target(patch.Span(0, 0)).Remove(), `["a","b"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			same(t, mustApply(t, tt.doc, tt.op), tt.want)
		})
	}
}

func TestArrayIndexOutOfRange(t *testing.T) {
	// An array has no slot beyond its length, so this is a missing target
	// rather than something assign could create.
	wantErr(t, `["a"]`, patch.CodeVacantTarget, patch.Target(patch.Index(5)).Assign(patch.Str("z")))
}

// ------------------------------------------------------------ values

func TestValueRendering(t *testing.T) {
	tests := []struct {
		name  string
		value patch.Value
		want  string
	}{
		{"bool", patch.Bool(true), `{"a":true}`},
		{"string", patch.Str("s"), `{"a":"s"}`},
		{"int32", patch.Int32(-7), `{"a":-7}`},
		{"int64", patch.Int64(-9007199254740993), `{"a":-9007199254740993}`},
		{"uint64", patch.Uint64(18446744073709551615), `{"a":18446744073709551615}`},
		{"double", patch.Float64(1.5), `{"a":1.5}`},
		{"enum is a number", patch.Enum(2), `{"a":2}`},
		{"bytes are base64", patch.Bytes([]byte{1, 2}), `{"a":"AQI="}`},
		{"object from m", patch.Msg(patch.F(patch.Name("x"), patch.Str("v"))), `{"a":{"x":"v"}}`},
		{"object from map", patch.Map(patch.E(patch.MapStr("x"), patch.Str("v"))), `{"a":{"x":"v"}}`},
		{"array", patch.List(patch.Str("p"), patch.Str("q")), `{"a":["p","q"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			same(t, mustApply(t, `{}`, patch.Target(patch.Name("a")).Assign(tt.value)), tt.want)
		})
	}
}

// TestBigIntegersSurviveAsWritten pins the decision to render 64-bit values as
// plain JSON numbers rather than ProtoJSON's string form.
func TestBigIntegersSurviveAsWritten(t *testing.T) {
	got := mustApply(t, `{}`, patch.Target(patch.Name("a")).Assign(patch.Int64(-9007199254740993)))
	same(t, got, `{"a":-9007199254740993}`)

	// The exact digits are in the output, not a float approximation of them.
	if got != `{"a":-9007199254740993}` {
		t.Errorf("= %s", got)
	}
}

func TestUntouchedNumbersKeepTheirText(t *testing.T) {
	// A Patch never disturbs data it did not name, which for JSON includes the
	// spelling of a number it never looked at.
	got := mustApply(t, `{"big":12345678901234567890,"exp":1e3,"a":1}`,
		patch.Target(patch.Name("a")).Assign(patch.Int32(2)))
	same(t, got, `{"big":12345678901234567890,"exp":1e3,"a":2}`)
}

func TestNaNAndInfinityAreRefused(t *testing.T) {
	// JSON has no representation for them (RFC 8259 §6), and ProtoJSON's
	// string spelling would be indistinguishable from an author writing those
	// strings on purpose.
	for _, v := range []patch.Value{
		patch.Float64(mathNaN()), patch.Float64(mathInf(1)), patch.Float32(float32(mathInf(-1))),
	} {
		wantErr(t, `{}`, patch.CodeIllegalArm, patch.Target(patch.Name("a")).Assign(v))
	}
}

// ------------------------------------------------------------ refusals

func TestRefusesWhatItCannotInterpret(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
	}{
		{"a field by number", patch.Target(patch.Num(109)).Remove()},
		{"a number alongside a name", patch.Target(patch.Name("a").Num(109)).Remove()},
		{"an integer map key", patch.Target(patch.MapInt(7)).Remove()},
		{"a bool map key", patch.Target(patch.MapBool(true)).Remove()},
		{"an index against an object", patch.Target(patch.Index(0)).Remove()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr(t, `{"a":1}`, patch.CodeIllegalArm, tt.op)
		})
	}

	t.Run("name and json_name disagreeing", func(t *testing.T) {
		wantErr(t, `{"a":1}`, patch.CodeFieldConflict,
			patch.Target(patch.Name("a").JSONName("b")).Remove())
	})
	t.Run("a range against an object", func(t *testing.T) {
		wantErr(t, `{"a":1}`, patch.CodeIllegalArm, patch.Target(patch.SpanAll()).Remove())
	})
	t.Run("a scalar root", func(t *testing.T) {
		wantErr(t, `42`, patch.CodeNotAContainer, patch.Target(patch.Name("a")).Remove())
	})
}

// ------------------------------------------------------------ contract

func TestApplyIsAtomic(t *testing.T) {
	doc := `{"a":"orig"}`
	_, err := apply(t, doc,
		patch.Target(patch.Name("a")).Assign(patch.Str("changed")),
		patch.Target(patch.Name("b")).Test(patch.Str("nope")),
	)
	if patch.CodeOf(err) != patch.CodeTestFailed {
		t.Fatalf("CodeOf = %v, want CodeTestFailed", patch.CodeOf(err))
	}

	// The caller's decoded value must also be untouched.
	var v any
	if err := json.Unmarshal([]byte(`{"a":"orig","n":[1,2]}`), &v); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(v)
	p := patch.MustNew(mt,
		patch.Target(patch.Name("a")).Assign(patch.Str("changed")),
		patch.Target(patch.Name("b")).Exists(true),
	)
	if _, err := patchjson.ApplyValue(v, p); err == nil {
		t.Fatal("expected a failure")
	}
	after, _ := json.Marshal(v)
	if string(before) != string(after) {
		t.Errorf("input changed:\n before %s\n after  %s", before, after)
	}
}

func TestAssertions(t *testing.T) {
	doc := `{"a":"x","n":[1,2],"o":{"k":"v"}}`

	t.Run("value holds", func(t *testing.T) {
		mustApply(t, doc, patch.Target(patch.Name("a")).Test(patch.Str("x")))
	})
	t.Run("value fails", func(t *testing.T) {
		wantErr(t, doc, patch.CodeTestFailed, patch.Target(patch.Name("a")).Test(patch.Str("y")))
	})
	t.Run("numbers compare numerically", func(t *testing.T) {
		mustApply(t, `{"a":1}`, patch.Target(patch.Name("a")).Test(patch.Float64(1.0)))
		mustApply(t, `{"a":1.0}`, patch.Target(patch.Name("a")).Test(patch.Int32(1)))
	})
	t.Run("absence", func(t *testing.T) {
		mustApply(t, doc, patch.Target(patch.Name("gone")).Exists(false))
		wantErr(t, doc, patch.CodeTestFailed, patch.Target(patch.Name("a")).Exists(false))
	})
	t.Run("an out-of-range index is absent", func(t *testing.T) {
		mustApply(t, doc, patch.Target(patch.Index(9)).In(patch.Name("n")).Exists(false))
	})
	t.Run("a test that names nothing cannot pass", func(t *testing.T) {
		wantErr(t, doc, patch.CodeTestVacuous,
			patch.Target(patch.Span(1, 1)).In(patch.Name("n")).Exists(true))
	})
	t.Run("a whole object", func(t *testing.T) {
		mustApply(t, doc, patch.Container().In(patch.Name("o")).Test(
			patch.Msg(patch.F(patch.Name("k"), patch.Str("v")))))
	})
}

func TestMoveAndCopy(t *testing.T) {
	t.Run("move clears the source", func(t *testing.T) {
		same(t, mustApply(t, `{"a":"v"}`, patch.Target(patch.Name("b")).Move(patch.Here(patch.Name("a")))),
			`{"b":"v"}`)
	})
	t.Run("copy leaves it", func(t *testing.T) {
		same(t, mustApply(t, `{"a":"v"}`, patch.Target(patch.Name("b")).Copy(patch.Here(patch.Name("a")))),
			`{"a":"v","b":"v"}`)
	})
	t.Run("moving onto itself is a no-op", func(t *testing.T) {
		same(t, mustApply(t, `{"a":"v"}`, patch.Target(patch.Name("a")).Move(patch.Here(patch.Name("a")))),
			`{"a":"v"}`)
	})
	t.Run("an absent source fails", func(t *testing.T) {
		wantErr(t, `{"b":"keep"}`, patch.CodeSourceUnresolved,
			patch.Target(patch.Name("b")).Move(patch.Here(patch.Name("a"))))
	})
	t.Run("across containers", func(t *testing.T) {
		same(t, mustApply(t, `{"a":"v","o":{}}`,
			patch.Target(patch.Name("x")).In(patch.Name("o")).Copy(patch.From(patch.Name("a")))),
			`{"a":"v","o":{"x":"v"}}`)
	})
	t.Run("a copied object does not alias its source", func(t *testing.T) {
		same(t, mustApply(t, `{"o":{"k":"v"}}`,
			patch.Target(patch.Name("p")).Copy(patch.Here(patch.Name("o"))),
			patch.Target(patch.Name("k")).In(patch.Name("o")).Assign(patch.Str("changed")),
		), `{"o":{"k":"changed"},"p":{"k":"v"}}`)
	})
	t.Run("types are not compared", func(t *testing.T) {
		// The format asks source and target to share a declared type. There
		// are no declared types here, so a string can land where a number was.
		same(t, mustApply(t, `{"s":"text","n":1}`,
			patch.Target(patch.Name("n")).Copy(patch.Here(patch.Name("s")))),
			`{"s":"text","n":"text"}`)
	})
}

func TestContainerScope(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		op   patch.Op
		want string
	}{
		{"remove empties an object", `{"a":1,"b":2}`, patch.Container().Remove(), `{}`},
		{"remove empties an array", `{"n":[1,2]}`,
			patch.Container().In(patch.Name("n")).Remove(), `{"n":[]}`},
		{"assign replaces an object", `{"a":1,"b":2}`,
			patch.Container().Assign(patch.Msg(patch.F(patch.Name("c"), patch.Str("only")))),
			`{"c":"only"}`},
		{"assign replaces an array", `{"n":[1]}`,
			patch.Container().In(patch.Name("n")).Assign(patch.List(patch.Str("x"))),
			`{"n":["x"]}`},
		{"insert appends to an array", `{"n":["a"]}`,
			patch.Container().In(patch.Name("n")).Insert(patch.List(patch.Str("b"))),
			`{"n":["a","b"]}`},
		{"insert fills only absent members", `{"a":1}`,
			patch.Container().Insert(patch.Msg(patch.F(patch.Name("b"), patch.Int32(2)))),
			`{"a":1,"b":2}`},
		{"an empty array can still be addressed", `{"n":[]}`,
			patch.Container().In(patch.Name("n")).Assign(patch.List(patch.Str("first"))),
			`{"n":["first"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			same(t, mustApply(t, tt.doc, tt.op), tt.want)
		})
	}

	t.Run("insert refuses an occupied member", func(t *testing.T) {
		wantErr(t, `{"a":1}`, patch.CodeOccupied,
			patch.Container().Insert(patch.Msg(patch.F(patch.Name("a"), patch.Int32(2)))))
	})
	t.Run("assign fails before clearing", func(t *testing.T) {
		// A value this engine cannot interpret must leave the container as it
		// was, not empty it and then fail.
		wantErr(t, `{"a":"keep"}`, patch.CodeIllegalArm, patch.Container().Assign(patch.Str("scalar")))
		same(t, mustApply(t, `{"a":"keep"}`, patch.Target(patch.Name("a")).Test(patch.Str("keep"))),
			`{"a":"keep"}`)
	})
}

func TestNestAndPaths(t *testing.T) {
	t.Run("nest into an object", func(t *testing.T) {
		same(t, mustApply(t, `{"o":{}}`,
			patch.Target(patch.Name("o")).Nest(patch.Target(patch.Name("k")).Assign(patch.Str("v")))),
			`{"o":{"k":"v"}}`)
	})
	t.Run("path into an object", func(t *testing.T) {
		same(t, mustApply(t, `{"o":{}}`,
			patch.Target(patch.Name("k")).In(patch.Name("o")).Assign(patch.Str("v"))),
			`{"o":{"k":"v"}}`)
	})
	t.Run("a path never creates a container", func(t *testing.T) {
		wantErr(t, `{}`, patch.CodePathNotReached,
			patch.Target(patch.Name("k")).In(patch.Name("o")).Assign(patch.Str("v")))
	})
	t.Run("on_missing does not reach a path", func(t *testing.T) {
		wantErr(t, `{}`, patch.CodePathNotReached,
			patch.Target(patch.Name("k")).In(patch.Name("o")).Skip().Assign(patch.Str("v")))
	})
	t.Run("through an array element", func(t *testing.T) {
		same(t, mustApply(t, `{"n":[{"k":"was"}]}`,
			patch.Target(patch.Name("k")).In(patch.Name("n"), patch.Index(0)).Assign(patch.Str("now"))),
			`{"n":[{"k":"now"}]}`)
	})
	t.Run("nested arrays sync back to their parent", func(t *testing.T) {
		same(t, mustApply(t, `{"n":[["a"]]}`,
			patch.Target(patch.Append()).In(patch.Name("n"), patch.Index(0)).Insert(patch.Str("b"))),
			`{"n":[["a","b"]]}`)
	})
}

func TestExpectType(t *testing.T) {
	p := patch.MustNew("example.v1.User", patch.Target(patch.Name("a")).Remove())

	// Unchecked by default: a JSON document does not name its own type.
	if _, err := patchjson.Apply([]byte(`{"a":1}`), p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := patchjson.Apply([]byte(`{"a":1}`), p, patchjson.ExpectType("example.v1.User")); err != nil {
		t.Fatalf("Apply with the right name: %v", err)
	}
	_, err := patchjson.Apply([]byte(`{"a":1}`), p, patchjson.ExpectType("something.Else"))
	if patch.CodeOf(err) != patch.CodeMessageTypeMismatch {
		t.Fatalf("CodeOf = %v, want CodeMessageTypeMismatch", patch.CodeOf(err))
	}
}

func TestUnknownArmIsRefused(t *testing.T) {
	// The forward-compatibility rule survives without a schema: a document from
	// a newer revision is refused rather than partly applied.
	p := patch.MustNew(mt, patch.Target(patch.Name("a")).Remove())
	setUnknown(p.GetDelta().GetEntries()[0], 20)
	_, err := patchjson.Apply([]byte(`{"a":1}`), p)
	if patch.CodeOf(err) != patch.CodeUnknownField {
		t.Fatalf("CodeOf = %v, want CodeUnknownField", patch.CodeOf(err))
	}
}
