// Package conformance holds the corpus every implementation of the patch
// format must satisfy, and a runner for it.
//
// The cases are textproto files, not Go code, so a second implementation can
// run them without importing the first one's tests. Applying a Patch to a live
// message and applying one to serialized bytes are necessarily separate
// engines; this corpus is what keeps them from drifting into reading the same
// document differently, which is what happened to the three backends of the
// previous design.
package conformance

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"

	"github.com/lesomnus/protobuf-patch/conformance/conformancepb"
	"github.com/lesomnus/protobuf-patch/internal/sample"
	patchlib "github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

//go:embed cases/*.textproto
var corpus embed.FS

// Applier is one implementation of the format under test. It must not modify
// in, whatever the outcome.
type Applier func(in *sample.Value, p *patchpb.Patch) (*sample.Value, error)

// Load reads every case in the corpus, in a stable order.
func Load() ([]*conformancepb.Case, error) {
	entries, err := corpus.ReadDir("cases")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var cases []*conformancepb.Case
	seen := map[string]string{}
	for _, name := range names {
		b, err := corpus.ReadFile(path.Join("cases", name))
		if err != nil {
			return nil, err
		}
		var suite conformancepb.Suite
		if err := prototext.Unmarshal(b, &suite); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, c := range suite.GetCases() {
			if c.GetName() == "" {
				return nil, fmt.Errorf("%s: a case has no name", name)
			}
			if prev, dup := seen[c.GetName()]; dup {
				return nil, fmt.Errorf("%s: case %q duplicates one in %s", name, c.GetName(), prev)
			}
			seen[c.GetName()] = name
			cases = append(cases, c)
		}
	}
	return cases, nil
}

// Run executes the whole corpus against apply.
func Run(t *testing.T, apply Applier) {
	t.Helper()

	cases, err := Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("the corpus is empty")
	}

	for _, c := range cases {
		t.Run(c.GetName(), func(t *testing.T) {
			if c.GetDoc() != "" {
				t.Log(c.GetDoc())
			}
			runCase(t, apply, c)
		})
	}
}

func runCase(t *testing.T, apply Applier, c *conformancepb.Case) {
	t.Helper()

	in := c.GetInput()
	if in == nil {
		in = &sample.Value{}
	}
	before := proto.Clone(in).(*sample.Value)

	got, err := apply(in, c.GetPatch())

	// The atomicity contract is not one case's subject: it holds for every
	// case, so it is checked on every case.
	if !proto.Equal(in, before) {
		t.Errorf("the implementation modified its input:\n got %v\nwant %v", in, before)
	}

	switch c.WhichWant() {
	case conformancepb.Case_Error_case:
		want, ok := patchlib.CodeByName(c.GetError())
		if !ok {
			t.Fatalf("the case expects %q, which is not a patch.Code name", c.GetError())
		}
		if err == nil {
			t.Fatalf("no error, want %v; result was %v", want, got)
		}
		if code := patchlib.CodeOf(err); code != want {
			t.Fatalf("CodeOf = %v, want %v (err = %v)", code, want, err)
		}

	case conformancepb.Case_Output_case:
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !proto.Equal(got, c.GetOutput()) {
			t.Errorf("\n got %v\nwant %v", got, c.GetOutput())
		}

	default:
		t.Fatalf("the case says nothing about what should happen")
	}
}
