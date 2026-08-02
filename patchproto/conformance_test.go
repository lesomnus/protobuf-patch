package patchproto_test

import (
	"testing"

	"github.com/lesomnus/protobuf-patch/conformance"
	"github.com/lesomnus/protobuf-patch/internal/sample"
	"github.com/lesomnus/protobuf-patch/patchpb"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// TestConformance runs the shared corpus against this implementation. When
// patchwire arrives it runs the same file with its own Applier, and any
// divergence between the two shows up here rather than in production.
func TestConformance(t *testing.T) {
	conformance.Run(t, func(in *sample.Value, p *patchpb.Patch) (*sample.Value, error) {
		return patchproto.Apply(in, p)
	})
}
