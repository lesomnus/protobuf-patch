# protobuf-patch

A patch document format for Protocol Buffer messages, and a Go implementation
of it.

A patch describes a change to a message — set this field, remove that map key,
replace this element — as **itself a protobuf message**, so it serializes,
stores, and travels like any other. It is modeled on JSON Patch (RFC 6902) and
extends it where protobuf makes something expressible that JSON Pointer cannot:
addressing a field by number, selecting a span of list elements, applying one
operation to several targets, and nesting a sub-patch under a shared path
prefix.

```go
p, err := patch.New("example.v1.User",
    patch.Target(patch.Name("display_name")).Assign(patch.Str("Ada")),
    patch.Target(patch.Name("email")).Test(patch.Str("ada@example.com")),
    patch.Container().In(patch.Name("tags")).Insert(patch.List(patch.Str("admin"))),
)

updated, err := patchproto.Apply(user, p)
```

## What it does

**Applies patches, atomically.** `Apply` works on a copy and returns it only on
success, so a failure — a test that does not hold, a field that has moved, an
operation from a newer revision — leaves the input untouched. There is
deliberately no in-place variant, because one could not honor that.

**Fails closed.** Anything a reader does not understand, or cannot resolve,
aborts the whole document rather than applying the part it understood. The
single opt-out is recorded on the wire, so tolerance is always the author's
decision.

**Does not convert.** Each protobuf type class takes exactly one value form. A
mismatch is an error, never a widened, narrowed, or truncated write.

**Does not generate.** There is no `Diff`; a patch is built by hand.

## Targets

| Package | Target | |
| ------- | ------ | --- |
| [`patchproto`](patchproto/) | `proto.Message` | the reference implementation |
| [`patchstruct`](patchstruct/) | a hand-written Go struct | config types, DTOs |
| [`patchjson`](patchjson/) | schema-less JSON | |

```go
updated, err := patchproto.Apply(user, p)   // a message
cfg, err     := patchstruct.Apply(cfg, p)   // a Go struct
out, err     := patchjson.Apply(doc, p)     // a JSON document
```

Each engine enforces as much of the format as its target can express and
**refuses what it cannot check** rather than guessing. Go's static types recover
most of what JSON loses, so `patchstruct` sits much closer to the reference than
`patchjson` does — of the 42 conformance cases, `patchstruct` agrees on 38 and
`patchjson` on 29. Every remaining disagreement is declared with a cause and
tested, so a new one cannot appear silently. See [engines.md](docs/engines.md).

## Documentation

| | |
| --- | --- |
| [format.md](docs/format.md) | The format: addressing, values, operations, failure, evolution |
| [examples.md](docs/examples.md) | Patches in ProtoJSON, with their real inputs, outputs, and errors |
| [engines.md](docs/engines.md) | The three engines, what each can enforce, and where they differ |
| [decisions.md](docs/decisions.md) | Why the format is shaped this way |
| [history/](docs/history/) | The review and planning documents this came out of (Korean) |

The normative source is the comments in [`proto/patch/`](proto/patch/). The
`.proto` is the specification; everything above is derived from it.

## The schema

| File | Contents |
| ---- | -------- |
| [`patch.proto`](proto/patch/patch.proto) | `Patch`, `Delta`, `Entry`, the seven operations, the failure contract |
| [`path.proto`](proto/patch/path.proto) | Addressing: `Key`, `Field`, `MapKey`, `Path`, `Selector`, `Range`, `Location` |
| [`value.proto`](proto/patch/value.proto) | Literals: `Value`, `MessageValue`, `ListValue`, `MapValue` |

Generated Go bindings are in [`patchpb/`](patchpb/).

### Design principles

These five decide every case where the format had a choice.

1. **Fail closed.** Anything unrecognized or unresolvable aborts the document.
   The one opt-out, `Entry.on_missing`, is recorded on the wire.
2. **Values are self-describing.** One arm per protobuf type class and no
   conversion lattice — a value can be validated against a field without being
   converted, and a mismatch is an error rather than a truncated write.
3. **Absence is never meaning.** Container scope is an explicit `oneof` arm, not
   an empty target list. There is no null. Range bounds are read through
   presence.
4. **Single-valued and multi-valued addressing are different types.** `Key`
   names exactly one location and is the only thing a `Path` may contain;
   `Selector` names zero or more and appears only in `Entry.targets`.
5. **The `.proto` is the specification.** Every rule lives in its comments.

The reasoning behind each is in [decisions.md](docs/decisions.md).

## Versioning

The package is `patch`, with no version suffix. A future breaking revision will
be a new package — `patchv2`, in `proto/patchv2/` — rather than a suffix on this
one. Within `patch`, a change that alters the meaning of an existing construct
increments `Patch.min_reader_revision`, which readers must check.

Naming a message type is optional, and its presence is the assertion: a patch
that declares one is refused against anything else, and one built with
`patch.NewUntyped` applies to any message. That is for the operations addressing
fields several resource types share — a name, an etag, labels — where requiring
a type would mean a copy of the document per resource.

`buf lint`'s `PACKAGE_VERSION_SUFFIX` rule is excepted in `buf.yaml` for this
reason.

## Development

```bash
buf build && buf lint   # verify the schema
buf generate            # regenerate patchpb/, conformancepb/, internal/sample/
go test ./...
```

The conformance corpus lives in [`conformance/cases`](conformance/cases) as
textproto rather than Go, so that a second implementation can run the same cases
without importing the first one's tests.
