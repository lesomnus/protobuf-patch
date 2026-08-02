package patch_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lesomnus/protobuf-patch/patch"
)

// everyCode is every Code the package defines. Adding a Code without adding it
// here fails TestCodeHasName, which is the point: the schema's FAILURE
// CONTRACT enumerates its clauses, and this is the list that has to keep up.
var everyCode = []patch.Code{
	patch.CodeUnknownField,
	patch.CodeMissingOneof,
	patch.CodeMissingField,
	patch.CodeEmptyCollection,
	patch.CodeUnrecognizedEnum,
	patch.CodeFieldNoIdentifier,
	patch.CodeIllegalSelector,
	patch.CodeIllegalScope,
	patch.CodeTestNotStrict,
	patch.CodeMessageTypeMismatch,
	patch.CodeReaderRevisionTooOld,
	patch.CodeIllegalArm,
	patch.CodeFieldConflict,
	patch.CodeMapKeyOutOfRange,
	patch.CodeUndeclaredEnumValue,
	patch.CodeTypeMismatch,
	patch.CodePathNotReached,
	patch.CodeVacantTarget,
	patch.CodeDuplicateTarget,
	patch.CodeIndexOutOfRange,
	patch.CodeOccupied,
	patch.CodeNotAContainer,
	patch.CodeSourceUnresolved,
	patch.CodeTestFailed,
	patch.CodeTestVacuous,
}

func TestCodeHasName(t *testing.T) {
	seen := map[string]patch.Code{}
	for _, c := range everyCode {
		name := c.String()
		if name == fmt.Sprintf("code(%d)", int(c)) {
			t.Errorf("Code(%d) has no name", int(c))
		}
		if prev, ok := seen[name]; ok {
			t.Errorf("Code(%d) and Code(%d) share the name %q", int(prev), int(c), name)
		}
		seen[name] = c
	}
}

func TestCodeOK(t *testing.T) {
	if patch.CodeOK != 0 {
		t.Fatalf("CodeOK = %d, want 0", int(patch.CodeOK))
	}
	if got := patch.CodeOf(nil); got != patch.CodeOK {
		t.Errorf("CodeOf(nil) = %v, want CodeOK", got)
	}
	if got := patch.CodeOf(errors.New("unrelated")); got != patch.CodeOK {
		t.Errorf("CodeOf(unrelated) = %v, want CodeOK", got)
	}
}

func TestCodeOfWrapped(t *testing.T) {
	base := patch.Err(patch.CodeVacantTarget, "delta.entries[0]")
	wrapped := fmt.Errorf("applying: %w", base)

	if got := patch.CodeOf(wrapped); got != patch.CodeVacantTarget {
		t.Errorf("CodeOf(wrapped) = %v, want CodeVacantTarget", got)
	}
	if !errors.Is(wrapped, &patch.Error{Code: patch.CodeVacantTarget}) {
		t.Error("errors.Is did not match on Code")
	}
	if errors.Is(wrapped, &patch.Error{Code: patch.CodeTestFailed}) {
		t.Error("errors.Is matched a different Code")
	}
}

func TestAt(t *testing.T) {
	tests := []struct {
		name string
		at   patch.At
		want string
	}{
		{"root field", patch.At("").Sub("delta"), "delta"},
		{"nested", patch.At("").Sub("delta").Index("entries", 2), "delta.entries[2]"},
		{
			"deep",
			patch.At("").Sub("delta").Index("entries", 2).Sub("targets").Index("selectors", 0),
			"delta.entries[2].targets.selectors[0]",
		},
		{"index at root", patch.At("").Index("entries", 0), "entries[0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tt.at); got != tt.want {
				t.Errorf("At = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		err  *patch.Error
		want string
	}{
		{
			"code only",
			patch.Err(patch.CodeTestFailed, ""),
			"test failed",
		},
		{
			"with position",
			patch.Err(patch.CodeMissingOneof, "delta.entries[0].kind"),
			"delta.entries[0].kind: missing oneof",
		},
		{
			"with detail",
			patch.Errf(patch.CodeIllegalArm, "delta.entries[0]", "index against a message"),
			"delta.entries[0]: illegal arm for target: index against a message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
