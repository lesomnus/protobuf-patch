# protobuf-patch

A patch document format for Protocol Buffer messages, and a Go implementation
of it.

A `Patch` describes a change to a message — set this field, remove that map
key, replace this element — as **itself a protobuf message**, so it can be
serialized, stored, and transmitted like any other. It is modeled on JSON Patch
(RFC 6902) and extends it where protobuf makes something expressible that JSON
Pointer cannot: addressing a field by number, selecting a span of list
elements, applying one operation to several targets, and nesting a sub-patch
under a shared path prefix.

The library validates and applies patches; it does not generate them from a
pair of messages. There is no `Diff`. A `Patch` is built by hand, or converted
from an RFC 6902 JSON Patch document.

```go
p, err := patch.New("example.v1.User",
    patch.Target(patch.Name("display_name")).Assign(patch.Str("Ada")),
    patch.Target(patch.Name("email")).Test(patch.Str("ada@example.com")),
    patch.Container().In(patch.Name("tags")).Insert(patch.List(patch.Str("admin"))),
)

updated, err := patchproto.Apply(user, p)
```

`Apply` is atomic: it works on a copy and returns it only on success, so a
failure — a test that does not hold, a field that has moved, an operation from
a newer revision of the schema — leaves the input untouched. There is
deliberately no in-place variant, because one could not honor that.

Patches apply to a `proto.Message`. Applying one directly to serialized
wire-format bytes, without unmarshaling, is planned as a separate
implementation sharing the same schema rules and the same conformance corpus.
To patch a JSON document, unmarshal it and apply:

```go
m := &pb.User{}
protojson.Unmarshal(doc, m)
m, err := patchproto.Apply(m, p)
out, err := protojson.Marshal(m)
```

## Packages

| Package | What it does |
| ------- | ------------ |
| [`patch`](patch/) | The error taxonomy, the builders, validation, and every rule decidable from a document and a descriptor |
| [`patchproto`](patchproto/) | Applies a patch to a `proto.Message` |
| [`jsonpatch`](jsonpatch/) | Parses RFC 6902 documents and converts them to patches |
| [`conformance`](conformance/) | The corpus every implementation must satisfy, and its runner |
| [`patchpb`](patchpb/) | Generated bindings |

The split between `patch` and `patchproto` is whether a message *instance* is
needed. Field resolution, range normalization, and value legality need only a
descriptor, so they live in `patch` and will be shared with the wire-format
implementation rather than reimplemented — the previous design's three backends
each resolved these themselves and drifted into reading the same document three
different ways.

## The schema

Three files, all normative — the comments in them are the specification, not
the documentation of one:

| File | Contents |
| ---- | -------- |
| [`proto/patch/patch.proto`](proto/patch/patch.proto) | `Patch`, `Delta`, `Entry`, the seven operations, and the failure contract |
| [`proto/patch/path.proto`](proto/patch/path.proto) | Addressing: `Key`, `Field`, `MapKey`, `Path`, `Selector`, `Range`, `Location` |
| [`proto/patch/value.proto`](proto/patch/value.proto) | Literals: `Value`, `MessageValue`, `ListValue`, `MapValue` |

Generated Go bindings live in [`patchpb/`](patchpb/).

### Design principles

These five decide every case where the schema had a choice.

1. **Fail closed.** Anything a reader does not understand, or cannot resolve,
   is an error that aborts the whole document. The single opt-out
   (`Entry.on_missing`) is recorded on the wire, so tolerance is always the
   author's decision and never a reader's default.
2. **Values are self-describing.** One `Value` arm per protobuf type class, and
   no conversion lattice — a value can be validated against a target field
   without being converted, and a mismatch is an error rather than a truncated
   write.
3. **Absence is never meaning.** Container scope is an explicit `oneof` arm,
   not an empty target list. There is no null.
4. **Single-valued and multi-valued addressing are different types.** `Key`
   names exactly one location and is the only thing a `Path` may contain;
   `Selector` names zero or more and appears only in `Entry.targets`.
5. **The `.proto` is the specification.** Every rule is in the comments. This
   README is derived documentation.

## Versioning

The package is `patch`, with no version suffix. A future breaking revision will
be a new package — `patchv2`, in `proto/patchv2/` — rather than a suffix on
this one. Within `patch`, a change that alters the meaning of an existing
construct increments `Patch.min_reader_revision`, which readers must check.

`buf lint`'s `PACKAGE_VERSION_SUFFIX` rule is excepted in `buf.yaml` for this
reason.

## Documents

| Document | What it is |
| -------- | ---------- |
| [progress.md](docs/progress.md) | What is built, and the decisions taken while building it |
| [implementation-plan.md](docs/implementation-plan.md) | The plan it was built to, in dependency order |
| [patch-schema-redesign.md](docs/patch-schema-redesign.md) | What the current schema decided, and why |
| [patch-spec-defects.md](docs/patch-spec-defects.md) | The 21 definition defects in the previous schema that this one resolves |
| [patch-schema-review.md](docs/patch-schema-review.md) | The full review the defect list was distilled from |

## Development

```bash
buf build && buf lint   # verify the schema
buf generate            # regenerate patchpb/, conformancepb/, internal/sample/
go test ./...
```

The conformance corpus lives in [`conformance/cases`](conformance/cases) as
textproto, not Go, so that a second implementation can run the same cases
without importing the first one's tests.

The previous implementation, against the previous schema, is preserved at
commit `2a96ef1` of `github.com/lesomnus/protobuf-diff`.
