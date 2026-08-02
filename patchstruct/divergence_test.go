package patchstruct_test

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/conformance"
	"github.com/lesomnus/protobuf-patch/conformance/conformancepb"
	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
	"github.com/lesomnus/protobuf-patch/patchstruct"
)

// Mirror is the Go shape of the corpus fixture, named the way the corpus
// addresses it. Field.name is the Go field name here, so the fields carry the
// proto spellings.
type Mirror struct {
	S_1 string `json:"s_1"`
	S_2 string `json:"s_2"`
	S_3 string `json:"s_3"`

	I32_1 int32 `json:"i32_1"`
	I64_1 int64 `json:"i64_1"`

	R_S_1 []string `json:"r_s_1"`

	M_S_S map[string]string `json:"m_s_s"`
	M_1   *Mirror           `json:"m_1"`
}

// retarget rewrites every Field.name in a Patch to Field.json_name.
//
// The corpus addresses protobuf fields — s_1, r_s_1 — and a Go field name must
// start with an upper-case letter to be exported, so no Go struct can carry
// those spellings. That is a NAMING difference, not a semantic one, so it is
// translated away here rather than declared as a divergence. Everything the
// corpus actually pins is left untouched.
func retarget(p *patchpb.Patch) *patchpb.Patch {
	out := proto.Clone(p).(*patchpb.Patch)
	rewriteFields(out.ProtoReflect())
	return out
}

func rewriteFields(m protoreflect.Message) {
	if m.Descriptor().FullName() == "patch.Field" {
		f := m.Interface().(*patchpb.Field)
		if f.HasName() && !f.HasJsonName() {
			f.SetJsonName(f.GetName())
			f.ClearName()
		}
		return
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
		case fd.IsList():
			if fd.Kind() == protoreflect.MessageKind {
				l := v.List()
				for i := range l.Len() {
					rewriteFields(l.Get(i).Message())
				}
			}
		case fd.Kind() == protoreflect.MessageKind:
			rewriteFields(v.Message())
		}
		return true
	})
}

// A Patch does not mean the same thing to patchstruct as to patchproto, and
// the danger is that they differ silently. So the shared corpus runs through
// this engine too and every disagreement must be declared with a cause.
//
// Compare this list with patchjson's: it is shorter, because Go's static types
// answer questions JSON cannot. A struct field genuinely exists or does not, a
// map's key type is declared, and a slice is not a map — so the whole
// causeDeclared and causeEmptyContainer families that patchjson has to live
// with are absent here.
const (
	// A Go struct has no field numbers, so a Field carrying one is refused
	// before the case's own point is reached.
	causeNoNumbers = "a field number cannot be checked against a Go struct, so it is refused first"

	// The corpus fixture is a protobuf message with fields this mirror does
	// not carry. Not a semantic divergence — the target simply differs.
	causeNotMirrored = "the case addresses a field the Go mirror does not declare"

	// A Go type name is not a protobuf message name, so there is nothing to
	// compare Patch.message_type against unless the caller supplies one with
	// ExpectType. The corpus runner does not.
	causeNoTypeName = "a Go type name is not a protobuf name, so message_type has nothing to check against"
)

var knownDivergences = map[string]string{
	"duplicate_target_is_an_error":    causeNoNumbers,
	"field_conflict_is_never_skipped": causeNoNumbers,

	"an_empty_message_type_is_not_the_same_as_none": causeNoTypeName,
	"an_untyped_patch_still_checks_the_field":       causeNoNumbers,
}

// TestDivergenceFromPatchproto runs the shared corpus against the Go mirror.
//
// Cases the mirror cannot represent are skipped rather than declared: the
// point is to catch semantic disagreement, not to mirror every field of a
// protobuf fixture in Go.
func TestDivergenceFromPatchproto(t *testing.T) {
	cases, err := conformance.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	agreed, skipped := 0, 0
	for _, c := range cases {
		t.Run(c.GetName(), func(t *testing.T) {
			in, ok := toMirror(c.GetInput())
			if !ok {
				skipped++
				t.Skip(causeNotMirrored)
			}

			got, applyErr := patchstruct.Apply(in, retarget(c.GetPatch()))
			same, why := agrees(c, got, applyErr)

			reason, declared := knownDivergences[c.GetName()]
			switch {
			case same && !declared:
				agreed++
			case same && declared:
				t.Errorf("listed as a divergence but the engines agree; remove it\n  listed: %s", reason)
			case !same && declared:
				t.Logf("declared divergence: %s", reason)
			default:
				t.Errorf("undeclared divergence: %s\nadd it with a cause, or fix the engine", why)
			}
		})
	}

	if agreed == 0 {
		t.Error("no case agreed; the corpus is not exercising the shared semantics")
	}
	t.Logf("%d agree, %d not representable in the Go mirror", agreed, skipped)
}

func agrees(c *conformancepb.Case, got Mirror, gotErr error) (bool, string) {
	if c.WhichWant() == conformancepb.Case_Error_case {
		want, _ := patch.CodeByName(c.GetError())
		if gotErr == nil {
			return false, "the corpus expects " + want.String() + ", the struct engine succeeded"
		}
		if code := patch.CodeOf(gotErr); code != want {
			return false, "the corpus expects " + want.String() + ", the struct engine reported " + code.String()
		}
		return true, ""
	}
	if gotErr != nil {
		return false, "the corpus expects success, the struct engine reported " + patch.CodeOf(gotErr).String()
	}
	want, ok := toMirror(c.GetOutput())
	if !ok {
		return false, "the expected output is not representable in the mirror"
	}
	if !reflect.DeepEqual(normalize(got), normalize(want)) {
		return false, "outputs differ"
	}
	return true, ""
}

// normalize erases the difference between a nil and an empty collection, which
// protobuf does not have and Go does.
func normalize(m Mirror) Mirror {
	if len(m.R_S_1) == 0 {
		m.R_S_1 = nil
	}
	if len(m.M_S_S) == 0 {
		m.M_S_S = nil
	}
	if m.M_1 != nil {
		inner := normalize(*m.M_1)
		m.M_1 = &inner
	}
	return m
}

// toMirror converts a corpus fixture into the Go shape, reporting false when
// it uses something the mirror does not carry.
func toMirror(v any) (Mirror, bool) {
	type valueLike interface {
		GetS_1() string
		GetS_2() string
		GetS_3() string
		GetI32_1() int32
		GetI64_1() int64
		GetRS_1() []string
		GetMSS() map[string]string
		GetM_1() *sample.Value
	}
	if v == nil {
		return Mirror{}, true
	}
	sv, ok := v.(valueLike)
	if !ok {
		return Mirror{}, false
	}

	if inner := sv.GetM_1(); inner != nil {
		nested, ok := toMirror(inner)
		if !ok {
			return Mirror{}, false
		}
		defer func() {}()
		m := mirrorOf(sv)
		m.M_1 = &nested
		return m, true
	}

	m := Mirror{
		S_1:   sv.GetS_1(),
		S_2:   sv.GetS_2(),
		S_3:   sv.GetS_3(),
		I32_1: sv.GetI32_1(),
		I64_1: sv.GetI64_1(),
		R_S_1: sv.GetRS_1(),
		M_S_S: sv.GetMSS(),
	}
	return m, true
}

func mirrorOf(sv interface {
	GetS_1() string
	GetS_2() string
	GetS_3() string
	GetI32_1() int32
	GetI64_1() int64
	GetRS_1() []string
	GetMSS() map[string]string
}) Mirror {
	return Mirror{
		S_1:   sv.GetS_1(),
		S_2:   sv.GetS_2(),
		S_3:   sv.GetS_3(),
		I32_1: sv.GetI32_1(),
		I64_1: sv.GetI64_1(),
		R_S_1: sv.GetRS_1(),
		M_S_S: sv.GetMSS(),
	}
}
