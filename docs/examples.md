# Examples

Patches written out in ProtoJSON. Every input, output, and error below was
produced by building the patch, applying it, and capturing what came back.

The target is `sample.Value` from [`proto/sample/`](../proto/sample/):

```proto
string s_1 = 109;    string s_2 = 209;    string s_3 = 309;
int32  i32_1 = 105;  int64  i64_1 = 103;
Value  m_1 = 111;
repeated string r_s_1 = 1009;
map<string, string> m_s_s = 10909;
Closed closed_1 = 119;   // a CLOSED enum declaring 0, 1, 2
```

> These show the documents. For assembling them in Go, see
> [builder.md](builder.md).

- [ProtoJSON is for reading](#protojson-is-for-reading)
- [Changing one field](#changing-one-field)
- [The basic operations](#the-basic-operations)
- [There is no type casting](#there-is-no-type-casting)
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
