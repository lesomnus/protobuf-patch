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

> **Status: the schema is defined; the implementation is being rewritten
> against it.**
>
> `proto/patch/*.proto` is complete and normative. The Go packages that applied
> the previous schema have been removed rather than adapted — the addressing
> and value models changed enough that adapting them would have carried the old
> ambiguities forward. See [docs/implementation-plan.md](docs/implementation-plan.md).

The library validates and applies patches; it does not generate them from a
pair of messages. There is no `Diff`. A `Patch` is built by hand through the
builder package, or converted from an RFC 6902 JSON Patch document.

Patches apply to a `proto.Message`. Applying one directly to serialized
wire-format bytes, without unmarshaling, is planned as a separate
implementation sharing the same schema rules. To patch a JSON document, unmarshal
it and apply:

```go
m := &pb.User{}
protojson.Unmarshal(doc, m)
m, err := patchproto.Apply(m, p)
out, err := protojson.Marshal(m)
```

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
| [implementation-plan.md](docs/implementation-plan.md) | How the schema gets implemented, in dependency order |
| [patch-schema-redesign.md](docs/patch-schema-redesign.md) | What the current schema decided, and why |
| [patch-spec-defects.md](docs/patch-spec-defects.md) | The 21 definition defects in the previous schema that this one resolves |
| [patch-schema-review.md](docs/patch-schema-review.md) | The full review the defect list was distilled from |

## Development

```bash
buf build && buf lint   # verify the schema
buf generate            # regenerate patchpb/ and internal/sample/
go test ./...
```

The previous implementation, against the previous schema, is preserved at
commit `2a96ef1` of `github.com/lesomnus/protobuf-diff`.
