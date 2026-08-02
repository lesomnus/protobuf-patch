package patchproto_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchproto"
)

// A proto3 `optional` field gets a SYNTHETIC oneof named _s. Addressing it
// would be a second way to spell what Key.field already says, and the format
// keeps one spelling per capability — so it is refused rather than allowed as
// a quiet alias.
//
// Editions replaced synthetic oneofs with features.field_presence, so the
// repo's own protos cannot produce one; the descriptor is built here instead.
func TestSyntheticOneofIsRefused(t *testing.T) {
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("synthetic.proto"),
		Package: proto.String("synth"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("M"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:           proto.String("s"),
				Number:         proto.Int32(1),
				Label:          descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:           descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Proto3Optional: proto.Bool(true),
				OneofIndex:     proto.Int32(0),
			}},
			OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_s")}},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	md := fd.Messages().ByName("M")
	if od := md.Oneofs().Get(0); !od.IsSynthetic() {
		t.Fatalf("setup: %s is not synthetic", od.FullName())
	}

	in := dynamicpb.NewMessage(md)
	_, err = patchproto.Apply(in, patch.MustNewUntyped(
		patch.Target(patch.Oneof("_s")).Remove(),
	))
	if got := patch.CodeOf(err); got != patch.CodeIllegalArm {
		t.Fatalf("code = %v, want %v (err: %v)", got, patch.CodeIllegalArm, err)
	}

	// The field itself stays addressable, which is the point.
	if _, err := patchproto.Apply(in, patch.MustNewUntyped(
		patch.Target(patch.Name("s")).Assign(patch.Str("v")),
	)); err != nil {
		t.Errorf("the field itself must still work: %v", err)
	}
}

// An empty name never reaches a descriptor: the builder refuses it, and so
// does validation of a document assembled by hand.
func TestOneofNameIsRequired(t *testing.T) {
	_, err := patch.New(mt, patch.Target(patch.Oneof("")).Remove())
	if got := patch.CodeOf(err); got != patch.CodeFieldNoIdentifier {
		t.Errorf("code = %v, want %v", got, patch.CodeFieldNoIdentifier)
	}
}
