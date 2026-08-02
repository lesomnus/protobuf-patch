# Examples

Patches written out in ProtoJSON. Every input, output, and error below was
produced by building the patch, applying it, and capturing what came back.

The target is `sample.Value` from
[`internal/proto/sample/`](../internal/proto/sample/) — a test fixture, which is
why it lives outside the published module:

```proto
string s_1 = 109;    string s_2 = 209;    string s_3 = 309;
int32  i32_1 = 105;  int64  i64_1 = 103;
Value  m_1 = 111;
repeated string r_s_1 = 1009;
map<string, string> m_s_s = 10909;
map<string, Value>  m_s_m = 10911;
Closed closed_1 = 119;   // a CLOSED enum declaring 0, 1, 2
oneof source { string src_s = 121; string src_s_too = 122; int32 src_i32 = 123; Value src_m = 124; }
```

> These show the documents. For assembling them in Go, see
> [builder.md](builder.md).

- [ProtoJSON is for reading](#protojson-is-for-reading)
- [Changing one field](#changing-one-field)
- [The basic operations](#the-basic-operations)
- [Addressing a oneof](#addressing-a-oneof)
- [Every entry of a map](#every-entry-of-a-map)
- [There is no type casting](#there-is-no-type-casting)
- [What equals means](#what-equals-means)
- [Errors](#errors)
- [message_type is optional](#message_type-is-optional)

---

## ProtoJSON is for reading

**Carry a patch in binary.** ProtoJSON drops unknown fields, which is exactly
what the forward-compatibility rule depends on seeing.

A `Value` carrying an arm from a newer revision — field 14, which the schema
reserves — renders like this:

```json
"assign": {
  "value": {}
}
```

The value is gone. The document that must be refused now looks like one with
nothing in it. Received in binary, the field is still there and validation
catches it:

```
delta.entries[0].assign.value: unknown field: a field from a newer revision,
or corrupt input; refusing rather than applying the part that is understood
```

The failure contract says so outright: *a Patch MUST NOT be carried over a
transport that discards them.* What follows is the human-readable rendering.

> 64-bit fields render as JSON strings. That is why `Range.begin` appears as
> `"1"` below.

## Changing one field

`targets` says where, `assign` says what.

```json
{
  "message_type": "sample.Value",
  "delta": {
    "entries": [
      {
        "targets": {
          "selectors": [
            { "key": { "field": { "name": "s_1" } } }
          ]
        },
        "assign": { "value": { "s": "new" } }
      }
    ]
  }
}
```

```
{"s_1":"old"}  →  {"s_1":"new"}
```

`"s"` is the `Value` arm for a string field. Each target kind takes exactly one
arm — see [there is no type casting](#there-is-no-type-casting).

## The basic operations

The wrapper is the same throughout, so only the entry and the result are shown.

| Goal | Entry | Result |
| ---- | ----- | ------ |
| remove | `"remove": {}` | `{"s_1":"x","s_2":"y"}` → `{"s_2":"y"}` |
| address **by number** | `"field": {"number": 109}` | `{"s_1":"old"}` → `{"s_1":"new"}` |
| pin name **against** number | `"field": {"name":"s_1","number":109}` | same, but refuses a message whose fields moved |
| several fields at once | three `selectors` | `{}` → `{"s_1":"z","s_2":"z","s_3":"z"}` |
| descend into a message | `"path": {"segments":[{"field":{"name":"m_1"}}]}` | `{"m_1":{}}` → `{"m_1":{"s_1":"deep"}}` |
| append to a list | `selectors: [{"append":{}}]` + `insert` | `["a","b"]` → `["a","b","c"]` |
| set a map entry | `"map_key": {"s":"b"}` + `assign` | `{"a":"1"}` → `{"a":"1","b":"2"}` |
| replace a whole container | `"container": {}` + `assign` | `{"s_1":"x","s_2":"y"}` → `{"s_3":"only"}` |
| move a value | `"move": {"from":{"same_container":{},"key":{...}}}` | `{"s_1":"v"}` → `{"s_2":"v"}` |

### One operation, several targets

```json
"targets": {
  "selectors": [
    { "key": { "field": { "name": "s_1" } } },
    { "key": { "field": { "name": "s_2" } } },
    { "key": { "field": { "name": "s_3" } } }
  ]
},
"assign": { "value": { "s": "z" } }
```

`targets` is an **unordered set**. Reordering the selectors cannot change the
result, and naming the same location twice is an error.

### A span of a list

```json
"path": { "segments": [ { "field": { "name": "r_s_1" } } ] },
"targets": {
  "selectors": [ { "range": { "begin": "1", "end": "4" } } ]
},
"remove": {}
```

```
["a","b","c","d","e"]  →  ["a","e"]
```

`[begin, end)`, negatives counting from the end, and **an omitted bound is
open**:

| Range | Means | Result |
| ----- | ----- | ------ |
| `{"begin":"1","end":"4"}` | `[1, 4)` | `["a","e"]` |
| `{"begin":"-2"}` | the last two | `["a","b","c"]` |
| `{}` | everything | `[]` |
| `{"begin":"0","end":"0"}` | **nothing** | unchanged |

That last row matters. `{"begin":"0","end":"0"}` and `{"begin":"0"}` are
different: the first selects nothing and the second selects everything.
Openness is decided by whether the field is **present**, not by its value.

### Indices resolve before the entry runs

```json
"targets": {
  "selectors": [
    { "key": { "index": "0" } },
    { "key": { "index": "2" } }
  ]
},
"insert": { "value": { "s": "Z" } }
```

```
["a","b","c"]  →  ["Z","a","b","Z","c"]
```

Both indices refer to the *original* list, so the first insertion does not shift
the second.

### Guarding a change

```json
"entries": [
  {
    "targets": { "selectors": [ { "key": { "field": { "name": "s_1" } } } ] },
    "test": { "value": { "s": "expected" } }
  },
  {
    "targets": { "selectors": [ { "key": { "field": { "name": "s_2" } } } ] },
    "assign": { "value": { "s": "written" } }
  }
]
```

```
{"s_1":"expected"}  →  {"s_1":"expected","s_2":"written"}
```

If the test does not hold, **nothing in the document is applied** — see
[errors](#errors).

### Sharing a prefix

```json
"targets": { "selectors": [ { "key": { "field": { "name": "m_1" } } } ] },
"nest": {
  "delta": {
    "entries": [
      { "targets": {"selectors":[{"key":{"field":{"name":"s_1"}}}]}, "assign": {"value":{"s":"a"}} },
      { "targets": {"selectors":[{"key":{"field":{"name":"s_2"}}}]}, "assign": {"value":{"s":"b"}} }
    ]
  }
}
```

```
{"m_1":{}}  →  {"m_1":{"s_1":"a","s_2":"b"}}
```

Where `path` puts a prefix on one entry, `nest` lets several entries share one.

---

## Addressing a oneof

`oneof_member` resolves to whichever member is currently set, so the document
does not have to know which one that is.

```json
"targets": {
  "selectors": [ { "oneofMember": { "name": "source" } } ]
},
"remove": {}
```

```
{"src_i32":7}  →  {}
```

The same patch against a message with `src_s` set clears that instead. And
against one with nothing set it is a **no-op**, not a failure — nothing set
selects nothing, which is the same defined empty answer an empty range gives.

Under `test` the oneof itself is read, which is what makes absence assertable:

```json
"test": { "exists": false }
```

```
{"s_1":"x"}  →  {"s_1":"x"}     holds; no member is set
```

Resolving through a oneof does not loosen anything. The arm still has to match
the member that is actually set:

```
delta.entries[0].assign.value: illegal arm for target:
  sample.Value.src_i32 takes i32, got s
```

> Prefer this to listing the members. An enumeration silently stops covering a
> member **added to the oneof later**; this does not.

## Every entry of a map

A map's keys are data, so a patch that has to name them can only be written by
something that already knows them. `every_entry` is what lets a stored document
change all of them — and under `nest`, edit *inside* all of them:

```json
"path": { "segments": [ { "field": { "name": "m_s_m" } } ] },
"targets": { "selectors": [ { "everyEntry": {} } ] },
"nest": {
  "delta": {
    "entries": [
      {
        "targets": {"selectors":[{"key":{"field":{"name":"s_2"}}}]},
        "assign": {"value":{"s":"added"}}
      }
    ]
  }
}
```

```
{"a":{"s_1":"one"},  "b":{"s_1":"two"}}
→ {"a":{"s_1":"one","s_2":"added"}, "b":{"s_1":"two","s_2":"added"}}
```

It is map-only. A range with both bounds unset already selects every element of
a list, and container scope already addresses a message:

```
delta.entries[0].targets.selectors[0].every_entry: illegal arm for target:
  every_entry addresses a map, and sample.Value is not one
```

An empty map selects nothing, so the entry is a no-op — and a `test` over one
asserts nothing, which is an error.

---

## There is no type casting

There is none, and that is deliberate. Each protobuf type class takes exactly
one `Value` arm:

| Target field | Arm |
| ------------ | --- |
| `bool` | `b` |
| `int32` `sint32` `sfixed32` | `i32` |
| `int64` `sint64` `sfixed64` | `i64` |
| `uint32` `fixed32` | `u32` |
| `uint64` `fixed64` | `u64` |
| `float` / `double` | `f32` / `f64` |
| `string` / `bytes` | `s` / `x` |
| `enum` | `e` |
| message / repeated / map | `m` / `l` / `map` |

Anything else is refused — not widened, not narrowed, not truncated.

```json
"targets": { "selectors": [ { "key": { "field": { "name": "i32_1" } } } ] },
"assign": { "value": { "s": "42" } }
```

```
delta.entries[0].assign.value: illegal arm for target:
  sample.Value.i32_1 takes i32, got s
```

**Integers of the same signedness are no different:**

```json
"assign": { "value": { "i64": "42" } }
```

```
delta.entries[0].assign.value: illegal arm for target:
  sample.Value.i32_1 takes i32, got i64
```

`move` and `copy` are the same, and go further: the **declared type** must match,
not merely the kind.

```
delta.entries[0].copy: type mismatch:
  source is string, target is int32; there is no conversion
```

### Why not

The previous implementation had a conversion lattice. It was asymmetric,
documented nowhere, and it truncated in silence — writing `1<<40` to an `int32`
field produced `0` with a nil error. For a format whose documents are stored and
replayed, that is data loss that survives review.

A self-describing value can also be validated against a field **without being
converted**, which is what lets an engine check arms even when it has no
protobuf descriptor at hand.

If a conversion is wanted, do it in the producer and write the matching arm. The
format does not guess.

---

## What `equals` means

`test` is the only comparison the format performs, so the rule is written down
rather than left to the implementation. Two clauses surprise people.

**A message compares exactly, not as a subset.**

```json
"targets": { "selectors": [ { "key": { "field": { "name": "m_1" } } } ] },
"test": { "value": { "m": { "fields": [
  { "key": {"name":"s_1"}, "value": {"s":"v"} }
] } } }
```

```
input   {"m_1":{"s_1":"v","s_2":"also here"}}
error   delta.entries[0].test: test failed: at sample.Value.m_1
```

A field absent from `fields` asserts the target does not have it set. For a
subset assertion, test the fields individually or `nest` a delta of tests.

**Unknown fields on the target take no part**, at any depth. The same patch
against a message carrying one holds:

```
input   {"m_1":{"s_1":"v", 2047:42}}     ← field 2047 is unknown
after   m_1:{s_1:"v"  2047:42}           ← held, and the unknown field survived
```

That has to be so: the format preserves unknown fields precisely because a
patch never discards data it did not name, and a `Value` has no way to mention
one. Counting them would make every `test` against such a message fail with no
value that could ever pass.

**NaN equals NaN**, and `-0.0` equals `+0.0`. A test asserts what a document
holds, not an arithmetic predicate — under IEEE inequality a patch could assign
a NaN and then be unable to assert the value it had just written.

---

## Errors

All real output. **Every failure leaves the target untouched** — the format is
atomic.

### A failed test undoes the entry before it

```json
"entries": [
  { "targets": {...s_1...},   "assign": { "value": { "s": "changed" } } },
  { "targets": {...i64_1...}, "test":   { "value": { "i64": "42" } } }
]
```

```
input   {"s_1":"orig"}
error   delta.entries[1].test: test failed: at sample.Value.i64_1
after   {"s_1":"orig"}     ← the first entry's write did not survive
```

### A field that is not there

```
delta.entries[0].targets.selectors[0]: target does not exist:
  nothing at that position in sample.Value; set on_missing to skip it deliberately
```

Tolerance is available, but it goes **in the document**:

```json
"on_missing": "ON_MISSING_SKIP",
"remove": {}
```

```
{"s_1":"x"}  →  {"s_1":"x"}     no error
```

It is the author's decision, not the reader's, which is why it is on the wire.

### When the name and the number disagree

```json
"field": { "name": "s_1", "number": 209 },
"on_missing": "ON_MISSING_SKIP"
```

```
delta.entries[0].targets.selectors[0].key.field: field identifiers disagree:
  field 209 of sample.Value is named "s_2", not "s_1"
```

**Refused even with `on_missing` set.** This is not "absent" — it means the
document was authored against a different schema, and it is the format's real
integrity check. It is what stops a stored patch from quietly succeeding after
a field was renumbered.

### The rest

| Situation | Error |
| --------- | ----- |
| `insert` onto something already set | `target already has a value: sample.Value.s_1 is already set` |
| a value a closed enum does not declare | `undeclared enum value: sample.Closed does not declare 9` |
| an arm from a newer revision | `unknown field: ... refusing rather than applying the part that is understood` |
| a number in an extension range | `extension field is not addressable: 100 is in an extension range of ...` |
| a document nested past the bound | `document is nested too deeply: nested deltas go deeper than 10` |

The extension one deserves a note. It is an **error**, not a missing target,
because an extension is data the message really holds — reporting it absent
would let `on_missing` silently skip an operation meant to change it, and let
`"exists": false` succeed about a value that is present.

The depth one is the format's resource bound. Both places a document recurses
are bounded and both fail closed:

```
delta.entries[0].nest.delta...nest.delta: document is nested too deeply:
  nested deltas go deeper than 10, which is as far as this reader follows
```

The last one is the shape of the whole contract: a document that is not
understood is **refused**, not applied in part.

---

## `message_type` is optional

Its presence is the assertion, and it is checked only when made.

```json
{
  "delta": { "entries": [ ... ] }
}
```

A patch with no `message_type` declares itself **type-agnostic** and applies to
any message.

Resource messages routinely share a prefix of common fields — a name, an etag,
labels — and a patch touching only those should apply to all of them. Requiring
a type would force either a copy of the document per resource or rewriting the
field before each apply, and a stored document that must be rewritten to be
applicable is not much of a stored document.

What is given up is the **coarsest** of the integrity checks. The fine one is
per-field and still bites:

```json
"field": { "name": "s_1", "number": 209 }
```

```
delta.entries[0].targets.selectors[0].key.field: field identifiers disagree:
  field 209 of sample.Value is named "s_2", not "s_1"
```

That holds whether or not the document names a type.

> An empty string is not absence. `"message_type": ""` is a declaration no
> message can satisfy, and is refused. Presence carries the meaning, not the
> value.

In Go:

```go
patch.New("example.v1.User", ...)   // pins the type
patch.NewUntyped(...)               // says it applies to anything
patch.New("", ...)                  // an error — more likely a mistake than a decision
```
