# The patch format

A reference for `patch.Patch`. The normative source is the comments in
[`proto/patch/`](../proto/patch/); this page is the readable version of them.

- [A patch is a message](#a-patch-is-a-message)
- [Anatomy of an entry](#anatomy-of-an-entry)
- [Addressing](#addressing)
- [Values](#values)
- [Operations](#operations)
- [Failure](#failure)
- [Evolution](#evolution)

---

## A patch is a message

```proto
message Patch {
  string message_type = 1;         // optional; see Evolution
  uint32 min_reader_revision = 2;
  Delta  delta = 3;                // required
}

message Delta { repeated Entry entries = 1; }
```

A `Patch` is an ordinary protobuf message, so it serializes, stores, and travels
like any other. Entries apply in order, and each observes the effects of those
before it.

A `Delta` on its own is a **fragment**: its addressing is relative to whichever
container it is applied to, so it cannot be interpreted alone. Only a `Patch`
names its target and its requirements. Store and transmit patches, not deltas.

> **Carry a patch in binary.** ProtoJSON drops unknown fields, which is exactly
> what the forward-compatibility rule depends on seeing — a value from a newer
> revision renders as `{}` and the document that should be refused looks like
> one with nothing in it. ProtoJSON is for reading.

## Anatomy of an entry

```
path    where to go first        optional; empty means this delta's container
scope   what to act on there     required: specific targets, or the container itself
kind    the operation            required: exactly one of seven
```

```proto
message Entry {
  Path path = 1;
  oneof scope {
    Targets   targets   = 2;
    Container container = 3;
  }
  OnMissing on_missing = 4;
  oneof kind {
    Remove remove = 5;  Test test   = 6;  Insert insert = 7;  Assign assign = 8;
    Move   move   = 9;  Copy copy   = 10; Nest   nest   = 11;
  }
}
```

Container scope is an explicit arm, not an empty target list. An entry with no
scope is an error, and `Targets` with no selectors is an error too.

## Addressing

### Key — exactly one location

```proto
message Key {
  oneof kind {
    Field  field   = 1;   // a message field
    sint64 index   = 2;   // a list element
    MapKey map_key = 3;   // a map entry
  }
}
```

The arm must match the container: `field` against a message, `index` against a
list, `map_key` against a map. Anything else is an error.

A negative index counts from the end — the effective index is `len + i` — and
that holds for **every** operation. There is no per-operation off-by-one.

### Field — a constraint, not a fallback

```proto
message Field {
  string name      = 1;   // the declared name
  string json_name = 2;   // the ProtoJSON name
  uint32 number    = 3;   // the field number, >= 1
}
```

Resolution goes **number, then name, then json_name** — the number first because
it is protobuf's stable identity while names may be edited without a wire break.
Every *other* identifier that is set must then agree with what resolved.

Disagreement is an error, never a miss, and never skippable. Pinning a name
against a number is how a stored patch refuses to apply to a message whose
fields were renumbered underneath it.

### MapKey — matched against the declared key type

| declared key type | arm |
| ----------------- | --- |
| `string` | `s` |
| `int32`, `sint32`, `sfixed32`, `int64`, `sint64`, `sfixed64` | `i` |
| `uint32`, `fixed32`, `uint64`, `fixed64` | `u` |
| `bool` | `b` |

The arm is never coerced: a numeric key does not stringify onto a string-keyed
map. The value must also lie within the declared type's range — a key outside it
is an error, not a truncation.

The empty string is a legal `s` key, and unambiguous because the oneof carries
its own presence.

### Path — navigate in

```proto
message Path { repeated Key segments = 1; }
```

A path is always relative to the container the enclosing delta is applied to;
for the outermost delta that is the root message. It only descends, so a nested
delta cannot escape the container `nest` handed it.

Every container along the way must already exist — a patch never auto-creates
one, and `on_missing` never applies to a path. "Exist" means presence, not
contents: a singular message field must be set, while a repeated or map field
has no presence in protobuf and therefore always exists as a container, empty or
not.

### Selector — zero or more locations

```proto
message Selector {
  oneof kind {
    Key    key    = 1;   // exactly one; legal everywhere
    Range  range  = 2;   // a span of list elements
    Append append = 3;   // one past the last element
  }
}
```

`Path` takes only a `Key`, because a path must reach exactly one container.
Multi-valued segments live in `Selector`, which appears only in `targets`.

`Append` is the RFC 6901 `-` token. It is legal only with `insert`, `move`, and
`copy` — the operations that can grow a list.

### Range

```proto
message Range { sint64 begin = 1; sint64 end = 2; }
```

Selects `[begin, end)`. Normalization, in order:

1. `begin` unset → 0; `end` unset → length
2. a negative bound `v` → `length + v`
3. both clamp to `[0, length]`

After that, `begin >= end` selects **nothing** — a defined answer, not a
failure. A range never wraps and never selects a union of two spans; express
that as two selectors.

Bounds are read through presence, not through the value. `[0, 0)` selects
nothing while `[0, _)` selects everything, and those are different wire states.

For a list of five:

```
xxxxx  [_, _)   all            -xxx-  [1, 4)   the middle three
---xx  [-2, _)  the last two   -----  [3, 2)   empty
xxx--  [_, -2)  all but two    -----  [9, _)   clamped to empty
```

### Targets

`targets` is an **unordered set**. Every selector resolves against the container
as it stood *before the entry began*, so no permutation changes the result and
an earlier write cannot shift a later target. Two selectors resolving to the
same location is an error.

On `[a, b, c]`:

```
remove,   targets [0, 2]           => [b]
insert Z, targets [0, 2]           => [Z, a, b, Z, c]
insert Z, targets [append]         => [a, b, c, Z]
assign Z, targets [-1]             => [a, b, Z]
```

### Location — the source of a move or copy

```proto
message Location {
  oneof origin { Path path = 1; SameContainer same_container = 3; }
  Key key = 2;
}
```

Because a `Location` carries its own path, the source need not live where the
targets do — a cross-container relocation is expressible. `origin` is required:
"the entry's own container" is a choice, not an absence.

## Values

A `Value` is a literal carried by `test`, `insert`, and `assign`. It is
**self-describing**: each arm corresponds to exactly one protobuf type class.

| target field kind | arm |
| ----------------- | --- |
| `bool` | `b` |
| `int32`, `sint32`, `sfixed32` | `i32` |
| `int64`, `sint64`, `sfixed64` | `i64` |
| `uint32`, `fixed32` | `u32` |
| `uint64`, `fixed64` | `u64` |
| `float` / `double` | `f32` / `f64` |
| `string` / `bytes` | `s` / `x` |
| `enum` | `e` |
| message | `m` |
| repeated | `l` |
| map | `map` |

**There is no conversion.** A value whose arm does not match is an error — never
widened, narrowed, or truncated. `i64` does not land in an `int32` field any
more than `s` does. If a conversion is wanted, the producer does it and writes
the matching arm.

The same field takes different arms depending on where the value is headed: a
`repeated string` takes `l` as a whole and `s` per element.

Against a **closed** enum, `e` must carry a number the enum declares.

There is deliberately **no null**. To clear a field, use `remove`; to assert
absence, use `test.exists = false`; a field absent from a `MessageValue` is
simply not set. An unset `Value.kind` is an error, not an implicit clear.

## Operations

Each kind is defined for all four scopes.

### remove — delete the target

| scope | effect |
| ----- | ------ |
| message field | clear it; a field without presence returns to its default |
| list index | remove the element; the list shrinks |
| map key | delete the entry; a key with no entry is a missing target |
| container | message: clear every declared field. list/map: empty it |

### test — assert, or abort

Never mutates. `on_missing` may not be set on a test, and a test whose
selectors resolve to zero locations is an error, so a test can never pass
vacuously.

| want | meaning |
| ---- | ------- |
| `value` | the target exists and equals it; kind and declared type must match |
| `exists: true/false` | the target is present / absent |

For a container, `exists: true` means non-empty.

### insert — create without overwriting

An existing value is an error; that is what separates insert from assign.

| scope | effect |
| ----- | ------ |
| message field | set it only if absent (a set sibling in the same oneof counts as set) |
| list index | insert **before** the index; use `append` to add at the end |
| map key | add only if the key is absent |
| container | message: fill only absent fields. list: append all elements. map: add absent keys |

### assign — set, creating or overwriting

| scope | effect |
| ----- | ------ |
| message field | set it |
| list index | overwrite in place; assign never grows a list |
| map key | set it, creating the entry if absent |
| container | replace wholesale: clear, then apply |

### move / copy — relocate

The source is resolved and read **before any target is written**, so the result
does not depend on target order. A target is written with assign semantics. For
`move`, the source is cleared once, after every target has been written.

Source and targets must share both kind and **declared type** — the same message
or enum name, the same element or key/value types. There is no conversion and no
reinterpretation of an enum number across enum types.

A target that resolves to the source location is a no-op for that target and the
source is not cleared ([RFC 6902 §4.4](https://www.rfc-editor.org/rfc/rfc6902#section-4.4)).

Container scope is an error for both.

### nest — apply an inner delta

The target must be a container. The inner delta's keys are interpreted relative
to it: fields for a message, indices for a list, keys for a map.

Where `path` puts a prefix on one entry, `nest` lets several entries share one.

## Failure

### Fail closed

Every one of these aborts the whole patch. None may be skipped, defaulted, or
treated as a no-op:

- `message_type` is set and does not name the target
- `min_reader_revision` exceeds what the reader implements
- an unrecognized `oneof` arm, anywhere
- **any unknown field number, at any depth**
- an unset required oneof or field; empty `Targets.selectors` or `Delta.entries`
- two selectors resolving to the same location
- a `Field` with no identifier, or whose identifiers disagree
- an arm not legal for the container or field it lands on
- a `MapKey` outside the declared key type's range
- a `Value.e` a closed enum does not declare
- a `path` that does not reach an existing container
- a `test` that does not hold, or one that asserts nothing
- a `move`/`copy` whose source does not resolve, or whose types differ

The unknown-field rule carries more weight than it looks like. Protobuf reports
an unrecognized oneof arm as "not set", so the unknown-field set is the only
thing separating "the producer omitted it" from "the producer used an arm from a
newer revision". A reader that skips the scan will silently apply a subset of a
document it does not understand.

### Atomicity

A patch applies entirely or not at all. Pre-validating the whole document is
*not* sufficient — an entry's legality can depend on state an earlier entry
produced — so an implementation applies to a private copy and publishes it only
on success.

An applier that mutates a caller-owned value in place cannot honor this, which
is why none of the engines offers one.

### Missing targets

A key can fail to reach content in two different ways, and the difference
decides what an operation may do:

| | meaning | treated as |
| --- | ------- | ---------- |
| **no slot** | the address names no position: an undeclared field, an index outside `[0, len)` | missing for every operation |
| **empty slot** | the position exists but holds nothing: a map key with no entry | missing only for `remove` and `nest`, which need content; the writing kinds fill it |

A declared message field is neither. It always exists as a position; whether it
holds a value is **presence**, which is what `test.exists` reports.

A missing target is not itself a failure. What happens depends on where the key
appears:

- in `targets` under `test` — it is **read**; this is what `exists` reports
- in `targets` under anything else — `on_missing` decides, default fail
- in a `path` or a `Location` — an unconditional error

```proto
enum OnMissing {
  ON_MISSING_UNSPECIFIED = 0;   // fail — the only encoding of it
  ON_MISSING_SKIP = 1;
}
```

Tolerance is always a recorded decision on the wire, never a reader's default.
It covers absence and nothing else: a kind mismatch, a field conflict, an
unrecognized arm, and a failed test are unaffected.

### Errors carry a position

Every failure names where in the document it is:

```
delta.entries[0].nest.delta.entries[0].assign.value: illegal arm for target:
  example.v1.User.age takes i32, got s
```

With this many conditions, "apply failed" is not a debuggable answer.

## Evolution

### message_type

Optional, and **its presence is the assertion**. When set, an applier refuses
the patch against anything else. When unset, the patch declares itself
type-agnostic and no such check happens.

Leaving it unset is a real case. Resource messages routinely share a prefix of
common fields — a name, an etag, labels — and a patch that only addresses those
applies to all of them. Requiring a type would force either a copy of the
document per resource or rewriting the field before each apply, and rewriting a
stored document to make it applicable defeats storing it.

An empty string is not the same as absence: `""` is a declaration no message can
satisfy, and is refused.

What is given up is the coarsest of the integrity checks. The fine one is
per-field and survives — a `Field` pinning a name against a number still refuses
a message whose fields moved, whatever the document says about its type.

### min_reader_revision

The lowest revision of this schema a reader must implement. 0 means the format
as first published.

Unrecognized arms and unknown fields are already hard errors, so this exists for
the other kind of change: a revision that alters the *meaning* of a construct
already defined. Such a revision increments the number, and every producer
relying on the new meaning must set it.

A monotone integer is used rather than a feature-string list so that there is no
registry to maintain and no way to spell a capability two ways. The cost is that
a revision locks out older readers for every document, not only those using the
changed construct.

### Versioning

The package is `patch`, with no version suffix. A future breaking revision will
be a new package — `patchv2` — rather than a suffix on this one. Within `patch`,
compatible evolution is governed by `min_reader_revision`.

### Presence is load-bearing

`Field.name` / `.number` and `Range.begin` / `.end` mean nothing without the
unset-versus-zero distinction, so `features.field_presence = EXPLICIT` is pinned
in the schema rather than left to the edition default. `features.enum_type =
OPEN` is pinned for the same reason: an `OnMissing` value a reader does not
recognize has to be visible to it, not silently defaulted.
