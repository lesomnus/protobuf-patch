package patch_test

import (
	"fmt"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// The examples build patches against sample.Value, whose relevant fields are:
//
//	string s_1 = 109;   string s_2 = 209;   string s_3 = 309;
//	int32  i32_1 = 105;
//	Value  m_1 = 111;
//	repeated string     r_s_1 = 1009;
//	map<string, string> m_s_s = 10909;
//	map<string, Value>  m_s_m = 10911;
//	oneof source { string src_s = 121; ...; int32 src_i32 = 123; }

const messageType = "sample.Value"

func ExampleNew() {
	p, err := patch.New(messageType,
		patch.Target(patch.Name("s_1")).Assign(patch.Str("hello")),
	)
	if err != nil {
		panic(err)
	}

	in := &sample.Value{}
	in.SetS_1("old")

	out, err := patchproto.Apply(in, p)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.GetS_1())
	// Output: hello
}

// A patch that names no message type applies to any message. Use it for
// operations that address fields several resource types share.
func ExampleNewUntyped() {
	p := patch.MustNewUntyped(
		patch.Target(patch.Name("s_1")).Assign(patch.Str("shared")),
	)

	out, _ := patchproto.Apply(&sample.Value{}, p)
	fmt.Println(out.GetS_1())
	// Output: shared
}

// Target begins an entry that applies to specific locations. It takes its first
// selector as a separate argument, so an empty target list cannot be written.
func ExampleTarget() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_1"), patch.Name("s_2"), patch.Name("s_3")).
			Assign(patch.Str("z")),
	)

	out, _ := patchproto.Apply(&sample.Value{}, p)
	fmt.Println(out.GetS_1(), out.GetS_2(), out.GetS_3())
	// Output: z z z
}

// Container begins an entry that applies to a whole container rather than to
// positions inside it. It offers no Move or Copy, which a container does not
// define.
func ExampleContainer() {
	p := patch.MustNew(messageType,
		patch.Container().Assign(patch.Msg(
			patch.F(patch.Name("s_3"), patch.Str("only")),
		)),
	)

	in := &sample.Value{}
	in.SetS_1("gone")
	in.SetS_2("also gone")

	out, _ := patchproto.Apply(in, p)
	fmt.Printf("s_1=%q s_2=%q s_3=%q\n", out.GetS_1(), out.GetS_2(), out.GetS_3())
	// Output: s_1="" s_2="" s_3="only"
}

// In navigates into a nested container before the operation applies.
func ExampleTargetScope_In() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("deep")),
	)

	in := &sample.Value{}
	in.SetM_1(&sample.Value{}) // a path never creates a container

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetM_1().GetS_1())
	// Output: deep
}

// Nest lets several entries share one path prefix.
func ExampleTargetScope_Nest() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("s_1")).Assign(patch.Str("a")),
			patch.Target(patch.Name("s_2")).Assign(patch.Str("b")),
		),
	)

	in := &sample.Value{}
	in.SetM_1(&sample.Value{})

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetM_1().GetS_1(), out.GetM_1().GetS_2())
	// Output: a b
}

// Skip tolerates a target that does not exist. The returned builder offers no
// Test or Exists: an assertion that can be skipped is one that can pass without
// being evaluated.
func ExampleTargetScope_Skip() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("no_such_field")).Skip().Remove(),
	)

	in := &sample.Value{}
	in.SetS_1("kept")

	out, err := patchproto.Apply(in, p)
	fmt.Println(out.GetS_1(), err)
	// Output: kept <nil>
}

// Name addresses a field by its declared name, Num by its number, and setting
// both pins them against each other -- a constraint, not a fallback. A patch
// that pins both refuses a message whose fields were renumbered.
func ExampleName() {
	var got []string
	for _, f := range []patch.Field{
		patch.Name("s_1"),          // by declared name
		patch.Num(109),             // by number
		patch.Name("s_1").Num(109), // both, pinned against each other
	} {
		p := patch.MustNew(messageType, patch.Target(f).Assign(patch.Str("ok")))
		out, _ := patchproto.Apply(&sample.Value{}, p)
		got = append(got, out.GetS_1())
	}
	fmt.Println(got)

	// A pin that does not hold is refused, and is never skippable.
	wrong := patch.MustNew(messageType,
		patch.Target(patch.Name("s_1").Num(209)).Skip().Remove())
	_, err := patchproto.Apply(&sample.Value{}, wrong)
	fmt.Println(patch.CodeOf(err))

	// Output:
	// [ok ok ok]
	// field identifiers disagree
}

// Index addresses a list element. A negative index counts from the end, and it
// means the same thing for every operation -- appending has its own selector.
func ExampleIndex() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Index(-1)).In(patch.Name("r_s_1")).Assign(patch.Str("Z")),
	)

	in := &sample.Value{}
	in.SetRS_1([]string{"a", "b", "c"})

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetRS_1())
	// Output: [a b Z]
}

// Append names the position one past the last element. It is legal only with
// insert, move, and copy -- the operations that can grow a list.
func ExampleAppend() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Append()).In(patch.Name("r_s_1")).Insert(patch.Str("c")),
	)

	in := &sample.Value{}
	in.SetRS_1([]string{"a", "b"})

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetRS_1())
	// Output: [a b c]
}

// Span selects a range of list elements. SpanFrom and SpanTo leave one side
// open, which is not the same as passing zero: SpanFrom(0) is everything while
// Span(0, 0) is nothing.
func ExampleSpan() {
	remove := func(sel patch.Selector) []string {
		p := patch.MustNew(messageType,
			patch.Target(sel).In(patch.Name("r_s_1")).Remove())
		in := &sample.Value{}
		in.SetRS_1([]string{"a", "b", "c", "d", "e"})
		out, _ := patchproto.Apply(in, p)
		return out.GetRS_1()
	}

	fmt.Println(remove(patch.Span(1, 4)))
	fmt.Println(remove(patch.SpanFrom(-2)))
	fmt.Println(remove(patch.SpanTo(-2)))
	fmt.Println(remove(patch.Span(0, 0)))
	fmt.Println(remove(patch.SpanAll()))

	// Output:
	// [a e]
	// [a b c]
	// [d e]
	// [a b c d e]
	// []
}

// Several targets in one entry resolve against the list as it stood BEFORE the
// entry ran, so the first insertion does not shift the second.
func ExampleTargetScope_Insert() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Index(0), patch.Index(2)).
			In(patch.Name("r_s_1")).
			Insert(patch.Str("Z")),
	)

	in := &sample.Value{}
	in.SetRS_1([]string{"a", "b", "c"})

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetRS_1())
	// Output: [Z a b Z c]
}

// MapStr addresses a map entry. The arm must match the map's declared key type;
// MapInt, MapUint, and MapBool cover the others.
func ExampleMapStr() {
	p := patch.MustNew(messageType,
		patch.Target(patch.MapStr("b")).In(patch.Name("m_s_s")).Assign(patch.Str("2")),
	)

	in := &sample.Value{}
	in.SetMSS(map[string]string{"a": "1"})

	out, _ := patchproto.Apply(in, p)
	fmt.Println(out.GetMSS()["a"], out.GetMSS()["b"])
	// Output: 1 2
}

// There is one value constructor per protobuf type class, and no conversion
// between them: a value whose arm does not match its target is an error.
func ExampleStr() {
	fits := patch.MustNew(messageType,
		patch.Target(patch.Name("s_1")).Assign(patch.Str("42")))
	out, err := patchproto.Apply(&sample.Value{}, fits)
	fmt.Printf("%q %v\n", out.GetS_1(), err)

	// The same value against an int32 field is refused rather than parsed.
	wrong := patch.MustNew(messageType,
		patch.Target(patch.Name("i32_1")).Assign(patch.Str("42")))
	_, err = patchproto.Apply(&sample.Value{}, wrong)
	fmt.Println(patch.CodeOf(err))

	// So is an integer of the wrong width.
	narrow := patch.MustNew(messageType,
		patch.Target(patch.Name("i32_1")).Assign(patch.Int64(42)))
	_, err = patchproto.Apply(&sample.Value{}, narrow)
	fmt.Println(patch.CodeOf(err))

	// Output:
	// "42" <nil>
	// illegal arm for target
	// illegal arm for target
}

// Msg builds a message value from its fields; List and Map build the container
// forms.
func ExampleMsg() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("m_1")).Assign(patch.Msg(
			patch.F(patch.Name("s_1"), patch.Str("inner")),
			patch.F(patch.Name("i32_1"), patch.Int32(7)),
		)),
		patch.Target(patch.Name("r_s_1")).Assign(patch.List(
			patch.Str("x"), patch.Str("y"),
		)),
		patch.Target(patch.Name("m_s_s")).Assign(patch.Map(
			patch.E(patch.MapStr("k"), patch.Str("v")),
		)),
	)

	out, _ := patchproto.Apply(&sample.Value{}, p)
	fmt.Println(out.GetM_1().GetS_1(), out.GetM_1().GetI32_1())
	fmt.Println(out.GetRS_1())
	fmt.Println(out.GetMSS())
	// Output:
	// inner 7
	// [x y]
	// map[k:v]
}

// Test guards a change. If the assertion does not hold, nothing in the document
// is applied -- not even the entries before it.
func ExampleTargetScope_Test() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_1")).Assign(patch.Str("changed")),
		patch.Target(patch.Name("s_2")).Test(patch.Str("expected")),
	)

	in := &sample.Value{}
	in.SetS_1("orig")
	in.SetS_2("actual")

	_, err := patchproto.Apply(in, p)
	fmt.Println(patch.CodeOf(err))
	fmt.Printf("%q\n", in.GetS_1()) // the first entry did not survive
	// Output:
	// test failed
	// "orig"
}

// Exists asserts presence rather than a value, which is how absence is checked.
func ExampleTargetScope_Exists() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_2")).Exists(false),
		patch.Target(patch.Name("s_1")).Exists(true),
	)

	in := &sample.Value{}
	in.SetS_1("here")

	_, err := patchproto.Apply(in, p)
	fmt.Println(err)
	// Output: <nil>
}

// Move relocates a value and clears the source; Copy leaves it. Here names a
// source in the same container, From one reached by a path.
func ExampleHere() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_2")).Move(patch.Here(patch.Name("s_1"))),
	)

	in := &sample.Value{}
	in.SetS_1("v")

	out, _ := patchproto.Apply(in, p)
	fmt.Printf("s_1=%q s_2=%q\n", out.GetS_1(), out.GetS_2())
	// Output: s_1="" s_2="v"
}

// From lets a move or copy cross containers: the source need not live where the
// targets do.
func ExampleFrom() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("s_2")).
			In(patch.Name("m_1")).
			Copy(patch.From(patch.Name("s_1"))),
	)

	in := &sample.Value{}
	in.SetS_1("v")
	in.SetM_1(&sample.Value{})

	out, _ := patchproto.Apply(in, p)
	fmt.Printf("s_1=%q m_1.s_2=%q\n", out.GetS_1(), out.GetM_1().GetS_2())
	// Output: s_1="v" m_1.s_2="v"
}

// CodeOf reports which rule a failure violated, and the error names where in
// the document the problem is.
func ExampleCodeOf() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("i32_1")).Assign(patch.Str("not a number")),
		),
	)

	in := &sample.Value{}
	in.SetM_1(&sample.Value{})

	_, err := patchproto.Apply(in, p)
	fmt.Println(patch.CodeOf(err))
	fmt.Println(err)
	// Output:
	// illegal arm for target
	// delta.entries[0].nest.delta.entries[0].assign.value: illegal arm for target: sample.Value.i32_1 takes i32, got s
}

// Validate checks everything decidable without a target, so a patch can be
// rejected at the door rather than at apply time.
func ExampleValidate() {
	p := patch.MustNew(messageType, patch.Target(patch.Name("s_1")).Remove())
	fmt.Println(patch.Validate(p))
	// Output: <nil>
}

// One constructor per protobuf type class. Each is legal against exactly the
// fields of that class and refused everywhere else.
func ExampleBool() {
	p := patch.MustNew(messageType,
		patch.Target(patch.Name("b_1")).Assign(patch.Bool(true)),
		patch.Target(patch.Name("i32_1")).Assign(patch.Int32(-1)),
		patch.Target(patch.Name("i64_1")).Assign(patch.Int64(-2)),
		patch.Target(patch.Name("u32_1")).Assign(patch.Uint32(3)),
		patch.Target(patch.Name("u64_1")).Assign(patch.Uint64(4)),
		patch.Target(patch.Name("f32_1")).Assign(patch.Float32(1.5)),
		patch.Target(patch.Name("f64_1")).Assign(patch.Float64(2.5)),
		patch.Target(patch.Name("s_1")).Assign(patch.Str("s")),
		patch.Target(patch.Name("bs_1")).Assign(patch.Bytes([]byte{1, 2})),
		patch.Target(patch.Name("enum_1")).Assign(patch.Enum(2)),
	)

	out, err := patchproto.Apply(&sample.Value{}, p)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.GetB_1(), out.GetI32_1(), out.GetI64_1(), out.GetU32_1(), out.GetU64_1())
	fmt.Println(out.GetF32_1(), out.GetF64_1(), out.GetS_1(), out.GetBs_1(), out.GetEnum_1())
	// Output:
	// true -1 -2 3 4
	// 1.5 2.5 s [1 2] LEVEL_HI
}

// A map key must match the map's declared key type. It is never coerced, so a
// numeric key does not address a string-keyed map.
func ExampleMapInt() {
	p := patch.MustNew(messageType,
		patch.Target(patch.MapStr("k")).In(patch.Name("m_s_s")).Assign(patch.Str("a")),
		patch.Target(patch.MapInt(7)).In(patch.Name("m_i32_s")).Assign(patch.Str("b")),
		patch.Target(patch.MapUint(8)).In(patch.Name("m_u32_s")).Assign(patch.Str("c")),
		patch.Target(patch.MapBool(true)).In(patch.Name("m_b_s")).Assign(patch.Str("d")),
	)

	out, err := patchproto.Apply(&sample.Value{}, p)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.GetMSS()["k"], out.GetMI32S()[7], out.GetMU32S()[8], out.GetMBS()[true])

	// A string key against an int-keyed map is refused, not parsed.
	wrong := patch.MustNew(messageType,
		patch.Target(patch.MapStr("7")).In(patch.Name("m_i32_s")).Assign(patch.Str("x")))
	_, err = patchproto.Apply(&sample.Value{}, wrong)
	fmt.Println(patch.CodeOf(err))

	// Output:
	// a b c d
	// illegal arm for target
}

// JSONName addresses a field by its ProtoJSON name, for patches authored
// against a JSON view of the message.
func ExampleJSONName() {
	p := patch.MustNew(messageType,
		patch.Target(patch.JSONName("s1")).Assign(patch.Str("v")),
	)

	out, err := patchproto.Apply(&sample.Value{}, p)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.GetS_1())
	// Output: v
}

// Oneof selects whichever member of a oneof is currently set, so a patch does
// not have to know which one that is — and does not stop covering a member
// added to the oneof later, the way enumerating the members would.
func ExampleOneof() {
	clear := patch.MustNew(messageType,
		patch.Target(patch.Oneof("source")).Remove(),
	)

	in := &sample.Value{}
	in.SetSrcI32(7)

	out, err := patchproto.Apply(in, clear)
	if err != nil {
		panic(err)
	}
	fmt.Println("after remove, anything set:", out.HasSrcI32() || out.HasSrcS())

	// Nothing set selects nothing, so the same patch is a no-op rather than a
	// failure — exactly as an empty Span is.
	again, err := patchproto.Apply(out, clear)
	fmt.Println("on an already-clear oneof:", err)
	_ = again

	// Under Test the oneof itself is read, which is what makes absence
	// assertable.
	_, err = patchproto.Apply(out, patch.MustNew(messageType,
		patch.Target(patch.Oneof("source")).Exists(false),
	))
	fmt.Println("exists=false holds:", err == nil)

	// Output:
	// after remove, anything set: false
	// on an already-clear oneof: <nil>
	// exists=false holds: true
}

// EveryEntry selects every entry of a map, which is what lets a stored patch
// change a map whose keys it does not know.
func ExampleEveryEntry() {
	p := patch.MustNew(messageType,
		patch.Target(patch.EveryEntry()).In(patch.Name("m_s_m")).Nest(
			patch.Target(patch.Name("s_2")).Assign(patch.Str("added")),
		),
	)

	in := &sample.Value{}
	one := &sample.Value{}
	one.SetS_1("one")
	two := &sample.Value{}
	two.SetS_1("two")
	in.SetMSM(map[string]*sample.Value{"a": one, "b": two})

	out, err := patchproto.Apply(in, p)
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"a", "b"} {
		v := out.GetMSM()[k]
		fmt.Printf("%s: %s %s\n", k, v.GetS_1(), v.GetS_2())
	}

	// Output:
	// a: one added
	// b: two added
}

// Limits bound how deep into a document an engine will recurse. Both places a
// patch nests are bounded, and both are overridable.
func ExampleLimits() {
	deep := patch.Target(patch.Name("s_1")).Assign(patch.Str("leaf"))
	for range patch.DefaultLimits.NestDepth + 1 {
		deep = patch.Target(patch.Name("m_1")).Nest(deep)
	}
	p := patch.MustNew(messageType, deep)

	_, err := patchproto.Apply(&sample.Value{}, p)
	fmt.Println(patch.CodeOf(err))

	_, err = patchproto.Apply(&sample.Value{}, p, patchproto.WithLimits(patch.Limits{
		NestDepth: 32,
	}))
	fmt.Println("with a raised bound:", patch.CodeOf(err) != patch.CodeTooDeep)

	// Output:
	// document is nested too deeply
	// with a raised bound: true
}
