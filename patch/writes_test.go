package patch_test

import (
	"strings"
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
)

// writesOf builds an untyped Patch from ops and returns the field paths it may
// modify, in document order. A write that only MATERIALIZES its path — see
// Write.Materializes — is marked with a leading "+", so that the two kinds
// cannot be confused for each other in a table.
func writesOf(t *testing.T, ops ...patch.Op) []string {
	t.Helper()
	ws, err := patch.Writes(sampleDesc, patch.MustNewUntyped(ops[0], ops[1:]...))
	if err != nil {
		t.Fatalf("Writes: %v", err)
	}
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Path.String()
		if w.Materializes {
			out[i] = "+" + out[i]
		}
	}
	return out
}

func writesErr(t *testing.T, ops ...patch.Op) error {
	t.Helper()
	_, err := patch.Writes(sampleDesc, patch.MustNewUntyped(ops[0], ops[1:]...))
	if err == nil {
		t.Fatal("Writes: no error, want one")
	}
	return err
}

func eqStrs(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("= %q, want %q", got, want)
	}
}

func TestWritesByOperation(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		{"assign", patch.Target(patch.Name("s_1")).Assign(patch.Str("v")), []string{"s_1"}},
		{"remove", patch.Target(patch.Name("s_1")).Remove(), []string{"s_1"}},
		{"insert", patch.Target(patch.Name("s_1")).Insert(patch.Str("v")), []string{"s_1"}},

		// A test reads. It is the only operation that contributes nothing, and
		// the reason a read-only check does not have to refuse assertions
		// about protected fields.
		{"test", patch.Target(patch.Name("s_1")).Test(patch.Str("v")), nil},
		{"exists", patch.Target(patch.Name("s_1")).Exists(true), nil},

		// copy reads its source and writes its targets.
		{
			"copy",
			patch.Target(patch.Name("s_2")).Copy(patch.Here(patch.Name("s_1"))),
			[]string{"s_2"},
		},
		// move EMPTIES its source, so the source is a write too. This is the
		// case a read-only policy is most likely to be walked past.
		{
			"move",
			patch.Target(patch.Name("s_2")).Move(patch.Here(patch.Name("s_1"))),
			[]string{"s_1", "s_2"},
		},

		// Several selectors in one entry, in document order.
		{
			"two targets",
			patch.Target(patch.Name("s_1"), patch.Name("s_2")).Remove(),
			[]string{"s_1", "s_2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

func TestWritesPath(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		{
			"through a message",
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("v")),
			[]string{"m_1.s_1"},
		},
		{
			"through two messages",
			patch.Target(patch.Name("s_1")).In(patch.Name("m_1"), patch.Name("m_1")).Assign(patch.Str("v")),
			[]string{"m_1.m_1.s_1"},
		},
		// A list index and a map key are elided: the policy question is about
		// the field, and every element of it answers the same way.
		{
			"through a list element",
			patch.Target(patch.Name("s_1")).In(patch.Name("r_m_1"), patch.Index(0)).Assign(patch.Str("v")),
			[]string{"r_m_1.s_1"},
		},
		{
			"through a map value",
			patch.Target(patch.Name("s_1")).In(patch.Name("m_s_m"), patch.MapStr("k")).Assign(patch.Str("v")),
			[]string{"m_s_m.s_1"},
		},
		// Creation is a write of its own: the containers along the path come
		// into existence even if every target is then skipped.
		{
			"on_absent_path creates",
			patch.Target(patch.Name("s_1")).InOrCreate(patch.Name("m_1"), patch.Name("m_1")).Assign(patch.Str("v")),
			[]string{"+m_1.m_1", "m_1.m_1.s_1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

// TestWritesInsideACollection covers the case where the container IS the field:
// an index, a range, an append, a map key, and every_entry all land on the
// list or map itself, because that is the granularity a policy is stated at.
func TestWritesInsideACollection(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		{
			"index",
			patch.Target(patch.Index(0)).In(patch.Name("r_s_1")).Assign(patch.Str("v")),
			[]string{"r_s_1"},
		},
		{
			"range",
			patch.Target(patch.Span(0, 2)).In(patch.Name("r_s_1")).Remove(),
			[]string{"r_s_1"},
		},
		{
			"append",
			patch.Target(patch.Append()).In(patch.Name("r_s_1")).Insert(patch.Str("v")),
			[]string{"r_s_1"},
		},
		{
			"map key",
			patch.Target(patch.MapStr("k")).In(patch.Name("m_s_s")).Assign(patch.Str("v")),
			[]string{"m_s_s"},
		},
		{
			"every entry",
			patch.Target(patch.EveryEntry()).In(patch.Name("m_s_s")).Assign(patch.Str("v")),
			[]string{"m_s_s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

// TestWritesOneofNamesEveryMember is the over-approximation that matters most.
// Which member a oneof selector resolves to is a property of the instance, so
// a descriptor-level analysis can only name them all — and naming them all is
// the direction that cannot let a protected member through.
func TestWritesOneofNamesEveryMember(t *testing.T) {
	got := writesOf(t, patch.Target(patch.Oneof("source")).Remove())
	eqStrs(t, got, []string{"src_s", "src_s_too", "src_i32", "src_m"})
}

func TestWritesContainerScope(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		// The root container is the empty path, which overlaps every field —
		// correctly, since this empties the whole message.
		{"remove the root", patch.Container().Remove(), []string{""}},
		{
			"assign a nested container",
			patch.Container().In(patch.Name("m_1")).Assign(patch.Msg()),
			[]string{"m_1"},
		},
		{
			"nest into a container",
			patch.Container().In(patch.Name("m_1")).Nest(
				patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
			),
			[]string{"m_1.s_1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

func TestWritesNest(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		{
			"into a message field",
			patch.Target(patch.Name("m_1")).Nest(
				patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
			),
			[]string{"+m_1", "m_1.s_1"},
		},
		{
			"into a list, then its elements",
			patch.Target(patch.Name("r_m_1")).Nest(
				patch.Target(patch.Span(0, 2)).Nest(
					patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
				),
			),
			[]string{"r_m_1.s_1"},
		},
		{
			"into a map, then its values",
			patch.Target(patch.Name("m_s_m")).Nest(
				patch.Target(patch.EveryEntry()).Nest(
					patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
				),
			),
			[]string{"m_s_m.s_1"},
		},
		// A nest applied to several targets reports the delta once per target,
		// so a policy sees each of them.
		{
			"two targets, one delta",
			patch.Target(patch.Name("m_1"), patch.Name("m_2")).Nest(
				patch.Target(patch.Name("s_1")).Assign(patch.Str("v")),
			),
			[]string{"+m_1", "m_1.s_1", "+m_2", "m_2.s_1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

// TestWritesLocationOrigin pins that a Location path starts at the delta's base
// container and not at the entry's own path — the one place where getting the
// origin wrong would report a write against the wrong subtree.
func TestWritesLocationOrigin(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		want []string
	}{
		{
			"same container",
			patch.Target(patch.Name("s_2")).In(patch.Name("m_1")).Move(patch.Here(patch.Name("s_1"))),
			[]string{"m_1.s_1", "m_1.s_2"},
		},
		{
			"from a path, which is rooted at the delta's base",
			patch.Target(patch.Name("s_2")).In(patch.Name("m_1")).
				Move(patch.From(patch.Name("s_1"), patch.Name("m_2"))),
			[]string{"m_2.s_1", "m_1.s_2"},
		},
		{
			"inside a nest, the base is the nest target",
			patch.Target(patch.Name("m_1")).Nest(
				patch.Target(patch.Name("s_2")).Move(patch.From(patch.Name("s_1"), patch.Name("m_2"))),
			),
			[]string{"+m_1", "m_1.m_2.s_1", "m_1.s_2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eqStrs(t, writesOf(t, tt.op), tt.want)
		})
	}
}

// TestWritesVacantTargetIsNoWrite: no operation can create a field the
// descriptor does not declare, so there is nothing to report. Apply refuses the
// entry unless on_missing says to skip it; either way nothing changes.
func TestWritesVacantTargetIsNoWrite(t *testing.T) {
	eqStrs(t, writesOf(t, patch.Target(patch.Name("nope")).Assign(patch.Str("v"))), nil)
	eqStrs(t, writesOf(t, patch.Target(patch.Oneof("nope")).Remove()), nil)
}

func TestWritesRefuses(t *testing.T) {
	tests := []struct {
		name string
		op   patch.Op
		code patch.Code
	}{
		// A path that cannot resolve is the same error Apply would raise, and
		// surfacing it here is the point of checking before applying.
		{
			"path into an undeclared field",
			patch.Target(patch.Name("s_1")).In(patch.Name("nope")).Assign(patch.Str("v")),
			patch.CodePathNotReached,
		},
		{
			"path into a scalar",
			patch.Target(patch.Name("s_1")).In(patch.Name("i32_1")).Assign(patch.Str("v")),
			patch.CodePathNotReached,
		},
		{
			"index into a message",
			patch.Target(patch.Index(0)).Assign(patch.Str("v")),
			patch.CodeIllegalArm,
		},
		{
			"range against a message",
			patch.Target(patch.Span(0, 1)).Remove(),
			patch.CodeIllegalArm,
		},
		{
			"every_entry against a list",
			patch.Target(patch.EveryEntry()).In(patch.Name("r_s_1")).Remove(),
			patch.CodeIllegalArm,
		},
		{
			"oneof against a list",
			patch.Target(patch.Oneof("source")).In(patch.Name("r_s_1")).Remove(),
			patch.CodeIllegalArm,
		},
		{
			// The integrity check survives into the analysis: a stored policy
			// must not silently pass a document authored against a schema whose
			// fields have since moved.
			"a field pinned to the wrong number",
			patch.Target(patch.Name("s_1").Num(209)).Assign(patch.Str("v")),
			patch.CodeFieldConflict,
		},
		{
			"nest into a scalar",
			patch.Target(patch.Name("s_1")).Nest(patch.Target(patch.Name("s_1")).Remove()),
			patch.CodeNotAContainer,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := patch.CodeOf(writesErr(t, tt.op)); got != tt.code {
				t.Errorf("= %v, want %v", got, tt.code)
			}
		})
	}
}

// TestWritesValidatesFirst is what makes the analysis safe to authorize
// against: a document carrying a field this build does not know is refused,
// rather than analyzed as though the part understood were the whole.
func TestWritesValidatesFirst(t *testing.T) {
	p := patch.MustNewUntyped(patch.Target(patch.Name("s_1")).Assign(patch.Str("v")))
	setUnknown(p.GetDelta().GetEntries()[0], 9999)

	if _, err := patch.Writes(sampleDesc, p); patch.CodeOf(err) != patch.CodeUnknownField {
		t.Fatalf("= %v, want unknown field", err)
	}
}

func TestWritesMessageTypeMismatch(t *testing.T) {
	p := patch.MustNew("other.Message", patch.Target(patch.Name("s_1")).Assign(patch.Str("v")))
	if _, err := patch.Writes(sampleDesc, p); patch.CodeOf(err) != patch.CodeMessageTypeMismatch {
		t.Fatalf("= %v, want message type mismatch", err)
	}
}

func TestWritesAt(t *testing.T) {
	p := patch.MustNewUntyped(
		patch.Target(patch.Name("s_1")).Test(patch.Str("x")),
		patch.Target(patch.Name("s_1"), patch.Name("s_2")).Remove(),
	)
	ws, err := patch.Writes(sampleDesc, p)
	if err != nil {
		t.Fatalf("Writes: %v", err)
	}
	want := []patch.At{
		"delta.entries[1].targets.selectors[0]",
		"delta.entries[1].targets.selectors[1]",
	}
	if len(ws) != len(want) {
		t.Fatalf("got %d writes, want %d", len(ws), len(want))
	}
	for i, w := range ws {
		if w.At != want[i] {
			t.Errorf("[%d].At = %q, want %q", i, w.At, want[i])
		}
	}
}

func TestFieldPathOverlaps(t *testing.T) {
	fp := func(s string) patch.FieldPath {
		p, err := patch.ParseFieldPath(sampleDesc, s)
		if err != nil {
			t.Fatalf("ParseFieldPath(%q): %v", s, err)
		}
		return p
	}
	tests := []struct {
		a, b string
		want bool
	}{
		{"s_1", "s_1", true},
		{"s_1", "s_2", false},
		{"m_1", "m_1.s_1", true}, // assigning m_1 replaces m_1.s_1
		{"m_1.s_1", "m_1", true}, // writing m_1.s_1 changes m_1
		{"m_1.s_1", "m_2.s_1", false},
		{"m_1.s_1", "m_1.s_2", false},
		{"", "m_1.s_1", true}, // the root holds everything
	}
	for _, tt := range tests {
		if got := fp(tt.a).Overlaps(fp(tt.b)); got != tt.want {
			t.Errorf("%q.Overlaps(%q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestParseFieldPath(t *testing.T) {
	tests := []struct {
		path string
		want string // the error code name, or "" if it should resolve
	}{
		{"", ""},
		{"s_1", ""},
		{"m_1.m_1.s_1", ""},
		{"r_m_1.s_1", ""}, // through a repeated message
		{"m_s_m.s_1", ""}, // through a map value
		{"nope", "target does not exist"},
		{"m_1.nope", "target does not exist"},
		{"s_1.s_1", "target is not a container"},   // through a scalar
		{"r_s_1.s_1", "target is not a container"}, // through repeated scalars
		{"m_s_s.s_1", "target is not a container"}, // through scalar map values
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, err := patch.ParseFieldPath(sampleDesc, tt.path)
			got := ""
			if err != nil {
				got = patch.CodeOf(err).String()
			}
			if got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

// TestWritesNestMaterializesEvenWhenItOnlyReads is the fact that makes
// Write.Materializes necessary rather than decorative. Descending into an
// unset singular message field POPULATES it, so a nest changes its target even
// when the delta inside is a bare assertion. Verified against patchproto: the
// same document leaves m_1 present and empty.
func TestWritesNestMaterializesEvenWhenItOnlyReads(t *testing.T) {
	got := writesOf(t, patch.Target(patch.Name("m_1")).Nest(
		patch.Target(patch.Name("s_1")).Exists(false),
	))
	eqStrs(t, got, []string{"+m_1"})

	// A list or a map materializes into an empty collection, which no reader
	// can tell from an unset one, so those are not reported.
	eqStrs(t, writesOf(t, patch.Target(patch.Name("r_m_1")).Nest(
		patch.Target(patch.Index(0)).Exists(true),
	)), nil)
}

// TestWriteAffects separates the two kinds of write. Replacing a container
// drops what was under it; creating one cannot, because a container that did
// not exist held nothing to drop.
func TestWriteAffects(t *testing.T) {
	fp := func(s string) patch.FieldPath {
		p, err := patch.ParseFieldPath(sampleDesc, s)
		if err != nil {
			t.Fatalf("ParseFieldPath(%q): %v", s, err)
		}
		return p
	}
	tests := []struct {
		wrote        string
		materializes bool
		field        string
		want         bool
	}{
		{"m_1", false, "m_1.s_1", true}, // assigning m_1 drops m_1.s_1
		{"m_1", true, "m_1.s_1", false}, // creating m_1 leaves m_1.s_1 as absent as it was
		{"m_1", true, "m_1", true},      // but m_1 itself did change
		{"m_1", true, "", true},         // and so did the message that holds it
		{"m_1.s_1", true, "m_1", true},  // creating deeper still changes the ancestor
		{"m_1", true, "m_2", false},     // a sibling is untouched either way
	}
	for _, tt := range tests {
		w := patch.Write{Path: fp(tt.wrote), Materializes: tt.materializes}
		if got := w.Affects(fp(tt.field)); got != tt.want {
			t.Errorf("Write{%q, materializes:%v}.Affects(%q) = %v, want %v",
				tt.wrote, tt.materializes, tt.field, got, tt.want)
		}
	}
}

// TestWritesDoesNotDescendIntoAValueLiteral: an assign carrying a nested
// message replaces the target wholesale, so the target alone is the write —
// and prefix matching already covers every field the literal names, plus the
// ones it does not, which the assign drops.
func TestWritesDoesNotDescendIntoAValueLiteral(t *testing.T) {
	got := writesOf(t, patch.Target(patch.Name("m_1")).Assign(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("v"))),
	))
	eqStrs(t, got, []string{"m_1"})

	// The field the literal never mentions is protected all the same, because
	// the assign clears it.
	ro := patch.MustNewReadOnly(sampleDesc, "m_1.s_2")
	err := ro.Check(patch.MustNewUntyped(patch.Target(patch.Name("m_1")).Assign(
		patch.Msg(patch.F(patch.Name("s_1"), patch.Str("v"))),
	)))
	if patch.CodeOf(err) != patch.CodeReadOnly {
		t.Fatalf("= %v, want read-only", err)
	}
}
