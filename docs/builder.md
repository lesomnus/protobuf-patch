# Building patches in Go

There is no `Diff`, so a patch is assembled by hand. The [`patch`](../patch/)
package is the builder for that.

Every snippet below is taken from a runnable example in
[`patch/example_test.go`](../patch/example_test.go) — they compile and their
output is checked, so nothing here can drift from the code.

- [Shape](#shape)
- [Scope](#scope)
- [Addressing](#addressing)
- [Values](#values)
- [Operations](#operations)
- [What will not compile](#what-will-not-compile)
- [Errors](#errors)

---

## Shape

```go
p, err := patch.New("example.v1.User",
    patch.Target(patch.Name("display_name")).Assign(patch.Str("Ada")),
    patch.Target(patch.Name("email")).Test(patch.Str("ada@example.com")),
)
```

An entry reads left to right as **where → what → do**:

```
patch.Target(...)   .In(...)   .Skip()   .Assign(v)
└─ scope            └─ path    └─ policy └─ operation, and the entry is done
```

The operation is the terminator: it returns an `Op`, which is what `New` takes.

| | |
| --- | --- |
| `New(messageType, first, rest...)` | pins the type; an applier refuses the patch against anything else |
| `NewUntyped(first, rest...)` | names no type, so it applies to any message |
| `MustNew` / `MustNewUntyped` | the same, panicking — for tests and constants |

At least one operation is required, which is why the first is a separate
parameter. A patch that does nothing is expressible as a `test` that holds,
which says so out loud.

## Scope

**`Target(...)`** — specific locations inside the container.

```go
patch.Target(patch.Name("s_1"), patch.Name("s_2"), patch.Name("s_3")).
    Assign(patch.Str("z"))
// {} → {s_1:"z" s_2:"z" s_3:"z"}
```

**`Container()`** — the container itself, as a whole.

```go
patch.Container().Assign(patch.Msg(
    patch.F(patch.Name("s_3"), patch.Str("only")),
))
// {s_1:"gone" s_2:"also gone"} → {s_3:"only"}
```

**`.In(...)`** navigates in first. Every container along the way must already
exist — a patch never creates one.

```go
patch.Target(patch.Name("s_1")).In(patch.Name("m_1")).Assign(patch.Str("deep"))
// {m_1:{}} → {m_1:{s_1:"deep"}}
```

**`.Skip()`** tolerates a target that does not exist.

```go
patch.Target(patch.Name("no_such_field")).Skip().Remove()
// {s_1:"kept"} → {s_1:"kept"}, no error
```

## Addressing

### Fields

| | |
| --- | --- |
| `Name("s_1")` | by declared name |
| `Num(109)` | by field number |
| `JSONName("s1")` | by ProtoJSON name |

They chain, and chaining **pins them against each other**:

```go
patch.Name("s_1").Num(109)      // both must describe the same field
```

That is the format's real integrity check — a stored patch refuses a message
whose fields were renumbered underneath it. A pin that does not hold is an
error, and `Skip()` does not reach it:

```go
patch.Target(patch.Name("s_1").Num(209)).Skip().Remove()
// → CodeFieldConflict
```

### List elements

```go
patch.Index(0)      // the first
patch.Index(-1)     // the last; negatives count from the end
patch.Append()      // one past the last
```

`-1` means the same thing for every operation. Appending has its own selector
rather than being a magic index, so there is no per-operation off-by-one.

`Append()` is legal only with `Insert`, `Move`, and `Copy` — the operations that
can grow a list.

### Spans

```go
patch.Span(1, 4)      // [1, 4)      [a b c d e] → [a e]
patch.SpanFrom(-2)    // the last two           → [a b c]
patch.SpanTo(-2)      // all but the last two   → [d e]
patch.Span(0, 0)      // nothing                → unchanged
patch.SpanAll()       // everything             → []
```

`SpanFrom(0)` and `Span(0, 0)` are different: openness is decided by whether the
bound is **set**, not by its value. Separate constructors make that impossible
to get wrong.

### Map keys

The arm must match the map's declared key type; it is never coerced.

```go
patch.MapStr("k")     // map<string, V>
patch.MapInt(7)       // map<int32|int64|sint*|sfixed*, V>
patch.MapUint(8)      // map<uint32|uint64|fixed*, V>
patch.MapBool(true)   // map<bool, V>
```

```go
patch.Target(patch.MapStr("7")).In(patch.Name("m_i32_s")).Assign(patch.Str("x"))
// → CodeIllegalArm; a string key does not address an int-keyed map
```

## Values

One constructor per protobuf type class, and **no conversion between them**.

| Constructor | Target field |
| ----------- | ------------ |
| `Bool(v)` | `bool` |
| `Int32(v)` / `Int64(v)` | `int32 sint32 sfixed32` / `int64 sint64 sfixed64` |
| `Uint32(v)` / `Uint64(v)` | `uint32 fixed32` / `uint64 fixed64` |
| `Float32(v)` / `Float64(v)` | `float` / `double` |
| `Str(v)` / `Bytes(v)` | `string` / `bytes` |
| `Enum(number)` | `enum` |
| `Msg(fields...)` | a message |
| `List(values...)` | a repeated field |
| `Map(entries...)` | a map field |

```go
patch.Target(patch.Name("i32_1")).Assign(patch.Str("42"))    // → CodeIllegalArm
patch.Target(patch.Name("i32_1")).Assign(patch.Int64(42))    // → CodeIllegalArm
```

The second one is the point: **integers of the same signedness are still
different arms**. If a conversion is wanted, do it here in the producer, where
someone can see it.

Container forms use `F` for a message field and `E` for a map entry:

```go
patch.Msg(
    patch.F(patch.Name("s_1"), patch.Str("inner")),
    patch.F(patch.Name("i32_1"), patch.Int32(7)),
)
patch.List(patch.Str("x"), patch.Str("y"))
patch.Map(patch.E(patch.MapStr("k"), patch.Str("v")))
```

There is no null. To clear, use `Remove()`; to assert absence, `Exists(false)`.

## Operations

| | |
| --- | --- |
| `.Remove()` | delete the target |
| `.Assign(v)` | set it, creating or overwriting |
| `.Insert(v)` | create without overwriting; an existing value is an error |
| `.Test(v)` | assert a value |
| `.Exists(b)` | assert presence or absence |
| `.Move(from)` / `.Copy(from)` | relocate, clearing the source or not |
| `.Nest(first, rest...)` | apply a sub-patch to the target |

### Several targets resolve before the entry runs

```go
patch.Target(patch.Index(0), patch.Index(2)).In(patch.Name("r_s_1")).Insert(patch.Str("Z"))
// [a b c] → [Z a b Z c]
```

Both indices refer to the *original* list, so the first insertion does not shift
the second. `targets` is an unordered set: reordering the selectors cannot change
the result, and naming the same location twice is an error.

### Guarding

```go
patch.MustNew(messageType,
    patch.Target(patch.Name("s_1")).Assign(patch.Str("changed")),
    patch.Target(patch.Name("s_2")).Test(patch.Str("expected")),
)
```

If the test does not hold, **the whole document is abandoned** — the first
entry's write does not survive either. `Apply` works on a copy, so the input is
untouched.

### Relocating

```go
patch.Here(patch.Name("s_1"))              // the same container as the targets
patch.From(patch.Name("s_1"))              // this delta's container
patch.From(patch.Name("s_1"), patch.Name("m_1"))   // under a path
```

Because a source carries its own path, a move or copy can cross containers:

```go
patch.Target(patch.Name("s_2")).In(patch.Name("m_1")).Copy(patch.From(patch.Name("s_1")))
// {s_1:"v" m_1:{}} → {s_1:"v" m_1:{s_2:"v"}}
```

### Sharing a prefix

```go
patch.Target(patch.Name("m_1")).Nest(
    patch.Target(patch.Name("s_1")).Assign(patch.Str("a")),
    patch.Target(patch.Name("s_2")).Assign(patch.Str("b")),
)
```

Where `.In(...)` puts a prefix on one entry, `Nest` lets several share one.

## What will not compile

The builder makes the structurally invalid states unrepresentable rather than
merely diagnosed. None of these is a runtime error, because none of them is
expressible:

| | How |
| --- | --- |
| an empty target list | `Target` takes its first selector as a separate parameter |
| an empty patch or `Nest` | `New` and `Nest` do the same for operations |
| an entry with no scope | `Target` and `Container` are the only ways to start one |
| an entry with no operation | only a terminator returns an `Op` |
| a `test` that tolerates a missing target | `Skip()` returns a builder with no `Test` or `Exists` |
| a `move` or `copy` onto a container | `Container()` returns a builder with no `Move` or `Copy` |

An empty target list matters most: on the wire it would be indistinguishable
from addressing the whole container, and the format gives that the most
destructive meaning available. A producer that computed zero targets has a bug,
and it must not be handed `remove everything`.

What types cannot catch — an `Append()` given to an operation that cannot grow a
list, an empty field name, field number zero, a value with no arm — accumulates
and surfaces at `New`.

## Errors

`CodeOf` reports which rule was violated, and the error names where in the
document the problem is.

```go
_, err := patchproto.Apply(in, p)

fmt.Println(patch.CodeOf(err))
// illegal arm for target

fmt.Println(err)
// delta.entries[0].nest.delta.entries[0].assign.value: illegal arm for target:
//   sample.Value.i32_1 takes i32, got s
```

`errors.Is` works too:

```go
if errors.Is(err, &patch.Error{Code: patch.CodeTestFailed}) { ... }
```

`Validate` checks everything decidable without a target, so a patch received
over the wire can be rejected at the door rather than at apply time:

```go
if err := patch.Validate(p); err != nil { ... }
```

Appliers call it themselves, so this is for validating early — at an API
boundary, or before storing a patch you did not build.
