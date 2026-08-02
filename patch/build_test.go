package patch_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

const mt = "patch.sample.Value"

func TestNew(t *testing.T) {
	p, err := patch.New(mt, patch.Target(patch.Name("s_1")).Assign(patch.Str("hi")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.GetMessageType(); got != mt {
		t.Errorf("message_type = %q, want %q", got, mt)
	}
	if got := p.GetMinReaderRevision(); got != 0 {
		t.Errorf("min_reader_revision = %d, want 0", got)
	}
	if got := len(p.GetDelta().GetEntries()); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}
}

func TestNewRejectsEmptyMessageType(t *testing.T) {
	_, err := patch.New("", patch.Target(patch.Name("s_1")).Remove())
	if patch.CodeOf(err) != patch.CodeMessageTypeMismatch {
		t.Fatalf("CodeOf = %v, want CodeMessageTypeMismatch (err=%v)", patch.CodeOf(err), err)
	}
}

func TestScope(t *testing.T) {
	t.Run("targets", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("s_1"), patch.Num(209)).Remove())
		e := p.GetDelta().GetEntries()[0]
		if e.WhichScope() != patchpb.Entry_Targets_case {
			t.Fatalf("scope = %v, want targets", e.WhichScope())
		}
		if got := len(e.GetTargets().GetSelectors()); got != 2 {
			t.Errorf("selectors = %d, want 2", got)
		}
	})

	t.Run("container", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Container().Remove())
		e := p.GetDelta().GetEntries()[0]
		if e.WhichScope() != patchpb.Entry_Container_case {
			t.Fatalf("scope = %v, want container", e.WhichScope())
		}
	})

	t.Run("container is not an empty target list", func(t *testing.T) {
		// The whole point of the scope oneof: these two must be distinguishable
		// on the wire, because the schema gives "container" the most
		// destructive meaning available and a producer bug must not reach it.
		container := patch.MustNew(mt, patch.Container().Remove())
		targets := patch.MustNew(mt, patch.Target(patch.Name("s_1")).Remove())
		if proto.Equal(container, targets) {
			t.Fatal("container and targets scopes encode identically")
		}
	})
}

func TestPath(t *testing.T) {
	p := patch.MustNew(mt,
		patch.Target(patch.Name("s_1")).In(patch.Name("m_1"), patch.Name("m_2")).Assign(patch.Str("x")),
	)
	segs := p.GetDelta().GetEntries()[0].GetPath().GetSegments()
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
	if got := segs[0].GetField().GetName(); got != "m_1" {
		t.Errorf("segments[0] = %q, want m_1", got)
	}
}

func TestFieldIsAConstraint(t *testing.T) {
	// Setting more than one identifier must carry all of them, so that the
	// applier can reject a Patch authored against a different schema rather
	// than silently resolving by whichever one still works.
	p := patch.MustNew(mt, patch.Target(patch.Name("s_1").Num(109).JSONName("s1")).Remove())
	f := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetKey().GetField()

	if !f.HasName() || f.GetName() != "s_1" {
		t.Errorf("name = %q, has=%v; want s_1", f.GetName(), f.HasName())
	}
	if !f.HasNumber() || f.GetNumber() != 109 {
		t.Errorf("number = %d, has=%v; want 109", f.GetNumber(), f.HasNumber())
	}
	if !f.HasJsonName() || f.GetJsonName() != "s1" {
		t.Errorf("json_name = %q, has=%v; want s1", f.GetJsonName(), f.HasJsonName())
	}
}

func TestFieldRejectsNonIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		field patch.Field
	}{
		{"empty name", patch.Name("")},
		{"empty json_name", patch.JSONName("")},
		{"field number zero", patch.Num(0)},
		{"empty name added later", patch.Num(1).Name("")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patch.New(mt, patch.Target(tt.field).Remove())
			if patch.CodeOf(err) != patch.CodeFieldNoIdentifier {
				t.Fatalf("CodeOf = %v, want CodeFieldNoIdentifier (err=%v)", patch.CodeOf(err), err)
			}
		})
	}
}

func TestKeyKinds(t *testing.T) {
	tests := []struct {
		name string
		key  patch.Keyer
		want any
	}{
		{"field", patch.Name("s_1"), patchpb.Key_Field_case},
		{"index", patch.Index(-1), patchpb.Key_Index_case},
		{"map key", patch.MapStr("k"), patchpb.Key_MapKey_case},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patch.MustNew(mt, patch.Target(tt.key).Remove())
			k := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetKey()
			if got := any(k.WhichKind()); got != tt.want {
				t.Errorf("kind = %v, want %v", k.WhichKind(), tt.want)
			}
		})
	}
}

func TestMapKeyArms(t *testing.T) {
	tests := []struct {
		name string
		key  patch.MapKey
		want any
	}{
		{"string", patch.MapStr("k"), patchpb.MapKey_S_case},
		{"empty string is a legal key", patch.MapStr(""), patchpb.MapKey_S_case},
		{"signed", patch.MapInt(-1), patchpb.MapKey_I_case},
		{"unsigned", patch.MapUint(1), patchpb.MapKey_U_case},
		{"bool", patch.MapBool(true), patchpb.MapKey_B_case},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patch.MustNew(mt, patch.Target(tt.key).Remove())
			k := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetKey().GetMapKey()
			if got := any(k.WhichKind()); got != tt.want {
				t.Errorf("kind = %v, want %v", k.WhichKind(), tt.want)
			}
		})
	}
}

func TestSpanPresence(t *testing.T) {
	// An open bound and a zero bound must be distinguishable: the schema keys
	// "open to that side" on presence, not on the value 0, so SpanFrom(0) and
	// SpanTo(0) must not collapse into each other.
	tests := []struct {
		name             string
		sel              patch.Selector
		hasBegin, hasEnd bool
		begin, end       int64
	}{
		{"all", patch.SpanAll(), false, false, 0, 0},
		{"from", patch.SpanFrom(0), true, false, 0, 0},
		{"to", patch.SpanTo(0), false, true, 0, 0},
		{"both", patch.Span(-2, 3), true, true, -2, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patch.MustNew(mt, patch.Target(tt.sel).Remove())
			r := p.GetDelta().GetEntries()[0].GetTargets().GetSelectors()[0].GetRange()
			if r.HasBegin() != tt.hasBegin {
				t.Errorf("HasBegin = %v, want %v", r.HasBegin(), tt.hasBegin)
			}
			if r.HasEnd() != tt.hasEnd {
				t.Errorf("HasEnd = %v, want %v", r.HasEnd(), tt.hasEnd)
			}
			if tt.hasBegin && r.GetBegin() != tt.begin {
				t.Errorf("Begin = %d, want %d", r.GetBegin(), tt.begin)
			}
			if tt.hasEnd && r.GetEnd() != tt.end {
				t.Errorf("End = %d, want %d", r.GetEnd(), tt.end)
			}
		})
	}
}

func TestAppendOnlyWithGrowingOps(t *testing.T) {
	ok := []struct {
		name string
		op   patch.Op
	}{
		{"insert", patch.Target(patch.Append()).Insert(patch.Str("z"))},
		{"move", patch.Target(patch.Append()).Move(patch.Here(patch.Index(0)))},
		{"copy", patch.Target(patch.Append()).Copy(patch.Here(patch.Index(0)))},
	}
	for _, tt := range ok {
		t.Run(tt.name+" accepts append", func(t *testing.T) {
			if _, err := patch.New(mt, tt.op); err != nil {
				t.Fatalf("New: %v", err)
			}
		})
	}

	bad := []struct {
		name string
		op   patch.Op
	}{
		{"remove", patch.Target(patch.Append()).Remove()},
		{"assign", patch.Target(patch.Append()).Assign(patch.Str("z"))},
		{"test", patch.Target(patch.Append()).Test(patch.Str("z"))},
		{"exists", patch.Target(patch.Append()).Exists(true)},
		{"nest", patch.Target(patch.Append()).Nest(patch.Container().Remove())},
	}
	for _, tt := range bad {
		t.Run(tt.name+" rejects append", func(t *testing.T) {
			_, err := patch.New(mt, tt.op)
			if patch.CodeOf(err) != patch.CodeIllegalSelector {
				t.Fatalf("CodeOf = %v, want CodeIllegalSelector (err=%v)", patch.CodeOf(err), err)
			}
		})
	}
}

func TestValueKinds(t *testing.T) {
	tests := []struct {
		name  string
		value patch.Value
		want  any
	}{
		{"bool", patch.Bool(true), patchpb.Value_B_case},
		{"int32", patch.Int32(1), patchpb.Value_I32_case},
		{"int64", patch.Int64(1), patchpb.Value_I64_case},
		{"uint32", patch.Uint32(1), patchpb.Value_U32_case},
		{"uint64", patch.Uint64(1), patchpb.Value_U64_case},
		{"float32", patch.Float32(1), patchpb.Value_F32_case},
		{"float64", patch.Float64(1), patchpb.Value_F64_case},
		{"string", patch.Str("s"), patchpb.Value_S_case},
		{"bytes", patch.Bytes([]byte{1}), patchpb.Value_X_case},
		{"enum", patch.Enum(-3), patchpb.Value_E_case},
		{"message", patch.Msg(patch.F(patch.Name("s_1"), patch.Str("x"))), patchpb.Value_M_case},
		{"list", patch.List(patch.Str("a")), patchpb.Value_L_case},
		{"map", patch.Map(patch.E(patch.MapStr("k"), patch.Str("v"))), patchpb.Value_Map_case},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patch.MustNew(mt, patch.Target(patch.Name("f")).Assign(tt.value))
			v := p.GetDelta().GetEntries()[0].GetAssign().GetValue()
			if got := any(v.WhichKind()); got != tt.want {
				t.Errorf("kind = %v, want %v", v.WhichKind(), tt.want)
			}
		})
	}
}

func TestValueHasNoNull(t *testing.T) {
	// A zero Value carries no arm. It must be rejected rather than silently
	// meaning "clear", which is the overload the schema deleted.
	_, err := patch.New(mt, patch.Target(patch.Name("s_1")).Assign(patch.Value{}))
	if patch.CodeOf(err) != patch.CodeMissingOneof {
		t.Fatalf("CodeOf = %v, want CodeMissingOneof (err=%v)", patch.CodeOf(err), err)
	}
}

func TestKinds(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want any
	}{
		{"remove", patch.Target(patch.Name("f")).Remove(), patchpb.Entry_Remove_case},
		{"test", patch.Target(patch.Name("f")).Test(patch.Str("v")), patchpb.Entry_Test_case},
		{"exists", patch.Target(patch.Name("f")).Exists(false), patchpb.Entry_Test_case},
		{"insert", patch.Target(patch.Name("f")).Insert(patch.Str("v")), patchpb.Entry_Insert_case},
		{"assign", patch.Target(patch.Name("f")).Assign(patch.Str("v")), patchpb.Entry_Assign_case},
		{"move", patch.Target(patch.Name("f")).Move(patch.Here(patch.Name("g"))), patchpb.Entry_Move_case},
		{"copy", patch.Target(patch.Name("f")).Copy(patch.Here(patch.Name("g"))), patchpb.Entry_Copy_case},
		{"nest", patch.Target(patch.Name("f")).Nest(patch.Container().Remove()), patchpb.Entry_Nest_case},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := patch.MustNew(mt, tt.op)
			if got := any(p.GetDelta().GetEntries()[0].WhichKind()); got != tt.want {
				t.Errorf("kind = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTestArms(t *testing.T) {
	p := patch.MustNew(mt,
		patch.Target(patch.Name("f")).Test(patch.Str("v")),
		patch.Target(patch.Name("g")).Exists(false),
	)
	es := p.GetDelta().GetEntries()
	if got := es[0].GetTest().WhichWant(); got != patchpb.Test_Value_case {
		t.Errorf("Test want = %v, want value", got)
	}
	if got := es[1].GetTest().WhichWant(); got != patchpb.Test_Exists_case {
		t.Errorf("Exists want = %v, want exists", got)
	}
	if es[1].GetTest().GetExists() {
		t.Error("Exists(false) built exists=true")
	}
}

func TestSkipSetsOnMissing(t *testing.T) {
	strict := patch.MustNew(mt, patch.Target(patch.Name("f")).Remove())
	if got := strict.GetDelta().GetEntries()[0].GetOnMissing(); got != patchpb.OnMissing_ON_MISSING_UNSPECIFIED {
		t.Errorf("default on_missing = %v, want UNSPECIFIED (which is fail)", got)
	}

	lenient := patch.MustNew(mt, patch.Target(patch.Name("f")).Skip().Remove())
	if got := lenient.GetDelta().GetEntries()[0].GetOnMissing(); got != patchpb.OnMissing_ON_MISSING_SKIP {
		t.Errorf("Skip on_missing = %v, want SKIP", got)
	}
}

func TestLocation(t *testing.T) {
	t.Run("here", func(t *testing.T) {
		p := patch.MustNew(mt, patch.Target(patch.Name("f")).Move(patch.Here(patch.Name("g"))))
		loc := p.GetDelta().GetEntries()[0].GetMove().GetFrom()
		if loc.WhichOrigin() != patchpb.Location_SameContainer_case {
			t.Errorf("origin = %v, want same_container", loc.WhichOrigin())
		}
		if got := loc.GetKey().GetField().GetName(); got != "g" {
			t.Errorf("key = %q, want g", got)
		}
	})

	t.Run("from a path", func(t *testing.T) {
		p := patch.MustNew(mt,
			patch.Target(patch.Name("f")).Copy(patch.From(patch.Name("g"), patch.Name("m_1"))),
		)
		loc := p.GetDelta().GetEntries()[0].GetCopy().GetFrom()
		if loc.WhichOrigin() != patchpb.Location_Path_case {
			t.Fatalf("origin = %v, want path", loc.WhichOrigin())
		}
		segs := loc.GetPath().GetSegments()
		if len(segs) != 1 || segs[0].GetField().GetName() != "m_1" {
			t.Errorf("path = %v, want [m_1]", segs)
		}
	})
}

func TestNestCarriesInnerDelta(t *testing.T) {
	p := patch.MustNew(mt,
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("s_1")).Assign(patch.Str("deep")),
			patch.Target(patch.Name("s_2")).Remove(),
		),
	)
	inner := p.GetDelta().GetEntries()[0].GetNest().GetDelta().GetEntries()
	if len(inner) != 2 {
		t.Fatalf("inner entries = %d, want 2", len(inner))
	}
}

func TestErrorsPropagateThroughNesting(t *testing.T) {
	// A malformed key deep inside a nested value must surface at New, not be
	// silently dropped.
	_, err := patch.New(mt,
		patch.Target(patch.Name("m_1")).Nest(
			patch.Target(patch.Name("s_1")).Assign(patch.Msg(patch.F(patch.Name(""), patch.Str("x")))),
		),
	)
	if patch.CodeOf(err) != patch.CodeFieldNoIdentifier {
		t.Fatalf("CodeOf = %v, want CodeFieldNoIdentifier (err=%v)", patch.CodeOf(err), err)
	}
}

func TestRoundTripsThroughWire(t *testing.T) {
	p := patch.MustNew(mt,
		patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("hi")),
		patch.Container().In(patch.Name("r_s_1")).Assign(patch.List(patch.Str("a"), patch.Str("b"))),
		patch.Target(patch.Span(-2, -1)).Skip().Remove(),
	)
	b, err := proto.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got patchpb.Patch
	if err := proto.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !proto.Equal(p, &got) {
		t.Error("Patch did not survive a wire round trip")
	}
}
