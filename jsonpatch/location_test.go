package jsonpatch_test

import (
	"slices"
	"testing"

	"github.com/lesomnus/protobuf-patch/jsonpatch"
)

func TestLocation(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{"", nil},
		{"foo", []string{"foo"}},
		{"foo/bar", []string{"foo", "bar"}},
		{"/", []string{""}},
		{"/foo", []string{"foo"}},
		{"/foo/bar/0/baz", []string{"foo", "bar", "0", "baz"}},
		{"/~0~1~2/q~1u~x", []string{"~/~2", "q/u~x"}},
		{"/~01", []string{"~1"}}, // ~0 then 1, not ~0 → ~ then ~1 → /
		{"/foo/bar/-/baz", []string{"foo", "bar", "-", "baz"}},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := slices.Collect(jsonpatch.Location(c.input).Seq())
			if !slices.Equal(got, c.expected) {
				t.Errorf("got %v, want %v", got, c.expected)
			}
		})
	}
}
