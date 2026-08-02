package patch

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patchpb"
)

// ReadOnly is a set of fields a Patch is not allowed to modify.
//
// It is the caller's policy, not the format's: nothing in the schema marks a
// field unwritable, and a Patch rejected here is a perfectly valid document
// that would have applied. Compile one per message type at startup and check
// every incoming document with it before Apply.
//
//	ro, err := patch.NewReadOnly(md, "id", "created_at", "status.observed_at")
//	...
//	if err := ro.Check(p); err != nil {
//	    return err // CodeReadOnly, positioned at the entry to blame
//	}
//	out, err := patchproto.Apply(m, p)
//
// Checking is a refusal, not a filter. It never edits the document to drop the
// offending entry: the producer asked for something it was not allowed to ask
// for, and applying the rest would leave both sides believing different things
// about what happened.
//
// The check is an over-approximation in the safe direction — see Writes for
// exactly which three cases are widened, and why a `move` counts as a write to
// its source as well as its targets.
type ReadOnly struct {
	md     protoreflect.MessageDescriptor
	fields []FieldPath
}

// NewReadOnly compiles a read-only policy for messages of type md.
//
// Each path is dot-separated field names from the root — "id",
// "status.observed_at" — resolved against md at construction so that a typo in
// a policy is a startup failure rather than a check that silently permits
// everything. A path through a repeated or map field names the field in every
// element or entry: "items.created_at" is that field of every item.
//
// Naming a message field freezes the whole subtree under it, and naming a leaf
// also refuses any write that would replace an ancestor holding it. Both
// follow from Write.Affects, which is what the check uses.
//
// The empty string names the root, which freezes the entire message. That is
// legal and occasionally what you want; it is also what an accidentally empty
// config entry produces, so it is worth guarding upstream.
func NewReadOnly(md protoreflect.MessageDescriptor, paths ...string) (*ReadOnly, error) {
	if md == nil {
		return nil, Errf(CodeMissingField, "", "nil descriptor")
	}

	r := &ReadOnly{md: md, fields: make([]FieldPath, 0, len(paths))}
	for _, s := range paths {
		p, err := ParseFieldPath(md, s)
		if err != nil {
			return nil, err
		}
		r.fields = append(r.fields, p)
	}
	return r, nil
}

// MustNewReadOnly is NewReadOnly, panicking on error. Intended for a policy
// written as a literal, where a bad path is a bug in the program rather than
// in its input.
func MustNewReadOnly(md protoreflect.MessageDescriptor, paths ...string) *ReadOnly {
	r, err := NewReadOnly(md, paths...)
	if err != nil {
		panic(err)
	}
	return r
}

// Descriptor returns the message type the policy was compiled against.
func (r *ReadOnly) Descriptor() protoreflect.MessageDescriptor { return r.md }

// Fields returns the paths the policy protects.
func (r *ReadOnly) Fields() []FieldPath { return r.fields }

// Check reports the first write in p that lands on a protected field, as an
// Error with CodeReadOnly positioned at the entry responsible.
//
// A document that fails Validate, or that is addressed to another message
// type, fails here with that error instead — an unreadable document has not
// been shown to respect the policy, so it must not pass a check whose whole
// job is to say that it does.
func (r *ReadOnly) Check(p *patchpb.Patch) error {
	return r.CheckWith(p, DefaultLimits)
}

// CheckWith is Check under caller-chosen Limits.
func (r *ReadOnly) CheckWith(p *patchpb.Patch, lim Limits) error {
	ws, err := WritesWith(r.md, p, lim)
	if err != nil {
		return err
	}
	for _, w := range ws {
		for _, f := range r.fields {
			if w.Affects(f) {
				return Errf(CodeReadOnly, w.At, "%s", explain(w, f))
			}
		}
	}
	return nil
}

// explain says which way the write and the protected field overlap, because
// "id is read-only" is unhelpful when the entry the author wrote names
// something else entirely.
func explain(w Write, ro FieldPath) string {
	name := func(p FieldPath) string {
		if len(p) == 0 {
			return "the message itself"
		}
		return p.String()
	}
	verb := "writes"
	if w.Materializes {
		verb = "creates"
	}
	switch {
	case len(w.Path) == len(ro):
		return name(ro) + " is read-only"
	case len(ro) < len(w.Path):
		return name(ro) + " is read-only, and this " + verb + " " + name(w.Path) + " inside it"
	default:
		// Unreachable for a materializing write, which never reaches past the
		// container it creates.
		return name(ro) + " is read-only, and this replaces " + name(w.Path) + ", which holds it"
	}
}
