package patchproto_test

import (
	"testing"

	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// The engine must reach the bound through Apply, not only through a Validate
// the caller remembered to make.
func TestApplyBoundsDepth(t *testing.T) {
	deep := func(n int) patch.Op {
		op := patch.Target(patch.Name("s_1")).Assign(patch.Str("leaf"))
		for range n {
			op = patch.Target(patch.Name("m_1")).Nest(op)
		}
		return op
	}

	in := &sample.Value{}
	p := patch.MustNew(mt, deep(patch.DefaultLimits.NestDepth+1))

	if got := patch.CodeOf(mustFail(t, in, p)); got != patch.CodeTooDeep {
		t.Errorf("code = %v, want %v", got, patch.CodeTooDeep)
	}

	if _, err := patchproto.Apply(in, p, patchproto.WithLimits(patch.Limits{
		NestDepth: patch.DefaultLimits.NestDepth + 1,
	})); patch.CodeOf(err) == patch.CodeTooDeep {
		t.Errorf("WithLimits did not raise the bound: %v", err)
	}
}
