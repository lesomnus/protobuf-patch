# Engines

One patch format, three targets. Each engine enforces as much of the format as
its target can express, refuses what it cannot check, and declares where it
disagrees with the others.

| Package | Target | Agrees with `patchproto` on |
| ------- | ------ | --------------------------- |
| [`patchproto`](../patchproto/) | `proto.Message` | 73 / 73 — it is the reference |
| [`patchstruct`](../patchstruct/) | a hand-written Go struct | 58 / 73 |
| [`patchjson`](../patchjson/) | schema-less JSON | 42 / 73 |

The numbers come from the shared corpus in [`conformance/`](../conformance/),
which every engine runs.

---

## Choosing one

**`patchproto`** if the target is a protobuf message. It is the reference
implementation: the whole format is checkable against a descriptor, so nothing
is given up.

```go
updated, err := patchproto.Apply(user, p)
```

Works on `dynamicpb` too, so a descriptor obtained at runtime is enough — no
generated Go type required.

**`patchstruct`** if the target is a plain Go type: a config struct, a DTO.

```go
cfg, err := patchstruct.Apply(cfg, p)
```

Not for protoc-generated structs. Under the opaque API their fields are
unexported and `reflect` cannot reach them, and they are already
`proto.Message`s that `patchproto` handles.

**`patchjson`** if the target is JSON with no schema behind it.

```go
out, err := patchjson.Apply(doc, p)
```

If the JSON *is* the rendering of a known message, prefer unmarshaling it and
using `patchproto` — you get the whole format back. Note that a protojson round
trip drops JSON members the message does not declare, which `patchjson` would
have preserved.

---

## What each can enforce

The format asks questions that only some targets can answer.

| Rule | `patchproto` | `patchstruct` | `patchjson` |
| ---- | :----------: | :-----------: | :---------: |
| `Value` arm matches the field kind | ✅ | ✅ | ❌ |
| `MapKey` arm matches the declared key type | ✅ | ✅ | ❌ |
| message vs map | ✅ | ✅ | ❌ |
| a declared field vs an undeclared one | ✅ | ✅ | ❌ |
| `Field.number` | ✅ | ❌ | ❌ |
| closed-enum values | ✅ | ❌ | ❌ |
| `move`/`copy` declared-type equality | ✅ | ✅ | ❌ |
| `oneof` occupancy under `insert` | ✅ | — | — |
| `oneof_member` — addressing a oneof at all | ✅ | ❌ | ❌ |
| `every_entry` — every entry of a map | ✅ | ✅ | ✅ |
| extension ranges refused rather than called absent | ✅ | — | — |
| NaN equals NaN | ✅ | ✅ | — |
| unknown fields excluded from equality | ✅ | — | — |
| depth bounded, and fails closed past it | ✅ | ✅ | ✅ |
| `message_type` | ✅ | with `ExpectType` | with `ExpectType` |
| everything else — addressing, operations, atomicity, fail-closed | ✅ | ✅ | ✅ |

A ❌ is always a **refusal**, never a guess. `patchjson` given a
`Field.number` does not fall back to a name; it reports `CodeIllegalArm`, and
does so even under `on_missing = SKIP`, because a constraint the author wrote
and the engine cannot check must not be quietly dropped.

Go's static types recover most of what JSON loses, which is why `patchstruct`
sits so much closer to the reference than `patchjson` does. A struct is not a
map, an `int32` is not an `int64`, `reflect.Type.Key()` gives a map's declared
key type, and a name absent from a struct type genuinely names nothing.

---

## Where they disagree, and why

The danger was never that the engines differ. It is that they differ
**silently** — which is exactly how the previous design of this format died,
with three backends applying the same document three different ways.

So each engine runs the shared corpus and every disagreement must be declared
with a cause. A new one cannot appear without someone writing it down, and a
declared one that starts agreeing fails the test too.

### `patchstruct` — 15 of 73

| cause | cases |
| ----- | ----- |
| a Go struct has no oneof to address | 11 |
| a Go struct has no field numbers, so a `Field` carrying one is refused before the case's own point is reached | 3 |
| a Go type name is not a protobuf name, so `message_type` has nothing to check against | 1 |

Go has no oneof, and that is the whole of the growth. A hand-written struct may
*model* one — an interface, a tag field, a set of pointers — but the format
cannot know which, and picking one would be guessing.

### `patchjson` — 31 of 73

| cause | cases |
| ----- | ----- |
| a oneof is declared by a schema, and a JSON document has none | 11 |
| an empty list or map exists in protobuf and is absent in JSON — protojson omits it | 9 |
| a field number cannot be checked without a schema, so it is refused first | 3 |
| "declared" is a property of a descriptor and has no JSON shadow | 2 |
| there are no declared types to compare for `move`/`copy` | 2 |
| JSON has no NaN; ProtoJSON writes it as the string `"NaN"`, which no float arm matches | 2 |
| an object stands in for both a message and a map, and behaves like the map | 1 |
| a JSON document carries no type name to check `message_type` against | 1 |

---

## Engine-specific rules

Each engine has to answer questions the format leaves to its target. These are
the answers.

### `patchstruct`

| | |
| --- | --- |
| `Field.name` | the Go field name |
| `Field.json_name` | the `json` tag, falling back to the field name |
| both set | resolved by one, verified against the other — `Field` stays a constraint |
| presence | a pointer field is absent when nil; a plain field when it holds its zero value, matching protobuf's implicit presence |
| embedded structs | **not flattened**; addressed by their type name like any other field |
| unexported fields | name no position at all |
| integer arms | a width class: `i32` lands only in `int8/16/32`, `i64` only in `int/int64`, with a range check inside the class so nothing truncates |
| refused | `Field.number`, `Value.e`, arrays (`[N]T` cannot change length), `interface`, `chan`, `func`, `**T` |
| nil map or slice | materialized when a path descends into it, matching the format's "a map or list field always exists as a container" |

### `patchjson`

| | |
| --- | --- |
| a JSON object | behaves like a **map**: with no schema every key is a valid position, so `assign` creates one and `insert` requires it empty |
| `field` and `map_key` | both address an object member; with no schema there is no difference to preserve |
| numbers | 64-bit values render as plain JSON numbers, not ProtoJSON's string form — this engine is for documents that never came from protobuf |
| untouched numbers | keep their exact text (`json.Number`), so `1e3` does not become `1000` |
| member order | not preserved; JSON objects are unordered ([RFC 8259 §4](https://www.rfc-editor.org/rfc/rfc8259#section-4)) and Go marshals a map in sorted key order |
| refused | `Field.number`, non-string `MapKey`, `NaN` and infinity (JSON has no representation for them) |
| not checked | `message_type` without `ExpectType`; `move`/`copy` declared types |

---

## Adding an engine

The shared rules live in [`patch`](../patch/) — validation, `Field` resolution,
range and index normalization, `Value` arm legality, the error taxonomy. An
engine supplies the target-specific half and nothing else. The split is by
whether a *target instance* is needed, not by target kind, which is why
`patch` holds everything decidable from a document plus a descriptor.

Then run the corpus:

```go
func TestConformance(t *testing.T) {
    conformance.Run(t, func(in *sample.Value, p *patchpb.Patch) (*sample.Value, error) {
        return myengine.Apply(in, p)
    })
}
```

The cases are textproto, not Go, precisely so a second implementation can run
them without importing the first one's tests.

A wire-format engine — applying a patch to serialized bytes without
unmarshaling — is the obvious next one, and would follow the same shape:
`patch` for the rules, its own application engine, the same corpus.
