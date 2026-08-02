package patch_test

import (
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

func TestMemberName(t *testing.T) {
	ok := []struct {
		name  string
		field patch.Field
		want  string
	}{
		{"by name", patch.Name("s_1"), "s_1"},
		{"by json name", patch.JSONName("s1"), "s1"},
		{"both, agreeing", patch.Name("same").JSONName("same"), "same"},
	}
	for _, tt := range ok {
		t.Run(tt.name, func(t *testing.T) {
			got, err := patch.MemberName(fieldPB(t, tt.field), "")
			if err != nil {
				t.Fatalf("MemberName: %v", err)
			}
			if got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}

	bad := []struct {
		name  string
		field patch.Field
		want  patch.Code
	}{
		{
			// A number is a constraint a schema-less consumer cannot check, so
			// it must refuse rather than apply the rest of the Field.
			"a number cannot be checked",
			patch.Num(109),
			patch.CodeIllegalArm,
		},
		{"a number alongside a name is still unchecked", patch.Name("s_1").Num(109), patch.CodeIllegalArm},
		{"name and json_name disagreeing", patch.Name("s_1").JSONName("s1"), patch.CodeFieldConflict},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			_, err := patch.MemberName(fieldPB(t, tt.field), "")
			if got := patch.CodeOf(err); got != tt.want {
				t.Fatalf("CodeOf = %v, want %v (err = %v)", got, tt.want, err)
			}
		})
	}

	t.Run("no identifier at all", func(t *testing.T) {
		_, err := patch.MemberName(&patchpb.Field{}, "")
		if patch.CodeOf(err) != patch.CodeFieldNoIdentifier {
			t.Fatalf("CodeOf = %v, want CodeFieldNoIdentifier", patch.CodeOf(err))
		}
	})
}

func TestMemberKey(t *testing.T) {
	got, err := patch.MemberKey(keyPB(t, patch.MapStr("k")), "")
	if err != nil {
		t.Fatalf("MemberKey: %v", err)
	}
	if got != "k" {
		t.Errorf("= %q", got)
	}

	if got, err := patch.MemberKey(keyPB(t, patch.MapStr("")), ""); err != nil || got != "" {
		t.Errorf("the empty string is a legal key: %q, %v", got, err)
	}

	// A numeric key is refused rather than stringified. The previous
	// implementation stringified, which made Field("7") and FieldNum(7)
	// address the same entry.
	for _, k := range []patch.MapKey{patch.MapInt(7), patch.MapUint(7), patch.MapBool(true)} {
		_, err := patch.MemberKey(keyPB(t, k), "")
		if patch.CodeOf(err) != patch.CodeIllegalArm {
			t.Errorf("CodeOf = %v, want CodeIllegalArm", patch.CodeOf(err))
		}
	}
}
