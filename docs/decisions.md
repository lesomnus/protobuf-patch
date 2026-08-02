# Design decisions

Why the format is shaped the way it is. Each entry states the rule, then what it
costs and what it buys — the parts worth knowing before changing anything.

Most of these were settled by taking the previous design of this format apart.
That review, and the defect list it produced, are in [history/](history/); the
conclusions are here.

- [Principles](#principles)
- [Values](#values)
- [Addressing](#addressing)
- [Operations](#operations)
- [Failure](#failure)
- [Evolution](#evolution)
- [Scope](#scope)
- [Structure](#structure)

---

## Principles

### The `.proto` is the specification

Every rule lives in the schema comments. Documentation is derived from them, not
the other way round.

The previous schema contained four normative sentences and three of them were
false — a conjunction rule no consumer implemented, four range examples no single
rule could reproduce, and a root rule one of three backends honored. Everything
else lived in a Go README, so a foreign-language implementer had almost nothing
to read and was misled where it spoke.

### Fail closed

Anything a reader does not understand, or cannot resolve, aborts the whole
document. The single opt-out — `Entry.on_missing` — is recorded on the wire, so
tolerance is always the author's decision and never a reader's default.

The previous implementation returned `nil` on every path it did not understand:
an unresolvable target, an unrecognized segment kind, an unresolvable move
source, an unknown `Value` kind. Combined with in-place mutation, a document
that half-applied reported success — and "returned nil, changed nothing" is the
one outcome a caller can neither detect nor recover from.

### Absence is never meaning

Container scope is an explicit `oneof` arm. There is no null. `Range` bounds are
read through presence.

The previous schema derived "operate on the whole container" from an empty
target list. `repeated` has no presence, so "I computed zero targets" — the
natural result of any producer bug — was byte-identical to "I deliberately
addressed the container", and the schema gave that state the most destructive
meaning available.

The same mistake appeared twice more: `end == 0` meant "open to the end", so an
explicit `[0, 0)` selected the whole list; and an unset `Value.kind` meant
"clear", so a value carrying an arm from a future revision would delete the
container.

---

## Values

### One arm per type class, and no conversion

A `Value` whose arm does not match its target is an error — never widened,
narrowed, or truncated.

The previous implementation had a conversion lattice. It was asymmetric
(`int → bool` yes, `bool → int` no; `int → float` yes, `float → int` no),
documented nowhere, and it truncated in silence: `ValI(1<<40)` written to an
`int32` field produced `0` with a nil error. For a format whose documents are
stored and replayed, silent numeric truncation is data loss that survives
review.

**Cost.** A producer that has an `int64` and wants an `int32` field must narrow
it itself. That is the point: the narrowing happens where someone can see it.

**Also bought.** A `Value` can be validated against a field without being
converted, which is what lets `patchstruct` check arms that `patchjson` cannot.

### No null

Clearing is `remove`. Asserting absence is `test.exists = false`. A field absent
from a `MessageValue` is simply not set.

The previous schema's `NullValue` carried three meanings at once — clear a
field, assert absence, assert an empty container — and was redundant with the
oneof's own unset state, which was handled inconsistently: accepted as
equivalent in one code path and rejected as an error in another, two bytes
apart.

### Not canonical

Two patches that mean the same thing may encode differently.
`MessageValue.fields`, `MapValue.entries`, and `Targets.selectors` are
order-insensitive collections stored in ordered `repeated` fields, so byte
equality is strictly stronger than logical equality.

An earlier draft claimed canonicality. It was false, and earning it would have
meant imposing sort orders on producers for a property nobody had asked for. The
claim was scoped down instead — do not use proto equality on a `Patch` for
deduplication or idempotency.

---

## Addressing

### `Field` is a constraint, not a fallback

Every identifier that is set must agree. Disagreement is an error, never a miss,
and never skippable by `on_missing`.

This is the format's real integrity check. Pinning a name against a number is
how a stored patch refuses to apply to a message whose fields were renumbered
underneath it. Reporting that as "missing" would let `on_missing` skip it, which
would defeat the whole point.

Resolution goes **number, then name, then json_name**, because the number is
protobuf's stable identity while names may be edited without a wire break.

The previous schema documented the conjunction and implemented it three
different ways: a first-match disjunction that never failed, a name-only lookup
that ignored `json_name` entirely, and a `Match` that ANDed the conjuncts but was
structurally unsatisfiable.

### `Key` and `Selector` are different types

`Key` names exactly one location and is the only thing a `Path` may contain.
`Selector` names zero or more and appears only in `targets`.

A path must reach exactly one container, so a multi-valued segment cannot appear
in one. Expressing that in the type system means the rule needs no prose and
cannot be violated.

### One integer per namespace

A field number, a list index, and a map key are separate arms. The previous
schema used one `sint64` for all of them plus a stringify fallback for string
map keys — four namespaces with incompatible legal domains in one field. Two
resolvers in the same package read `FieldSegment{number: 3}` as key `""` and key
`"3"` respectively.

### `append` is its own selector

"One past the end" has an encoding rather than being a magic `-1`.

In the previous design a negative index meant "the last element" for four
operations and "one past the last" for `insert`, an off-by-one a user touched on
every entry and could not check against the schema. Giving append its own arm
lets `-1` mean the same thing everywhere.

### One rule for `Range`, read through presence

`begin` unset is 0, `end` unset is the length, negatives count from the end,
both clamp, and `begin >= end` selects nothing.

The previous schema gave four worked examples that no single rule reproduces —
two required a wrap-around union, and one contradicted its own diagram. Its
implementation branched on `end <= 0`, so an explicit `[0, 0)` selected the whole
list. Presence is what separates "open" from "zero", and the schema now keys on
it.

### `targets` is an unordered set, resolved pre-entry

Every selector resolves against the container as it stood before the entry
began; two selectors reaching the same location is an error.

The previous schema said nothing about ordering, duplicates, or which state
indices resolved against, and the kinds interpreted the same list three ways —
`remove` deduplicated, `insert` multiplied, and a message `move` was
order-sensitive enough that `[209, 109]` and `[109, 209]` left different final
states.

---

## Operations

### Missing targets: no slot, empty slot, presence

Three states, not one:

- **no slot** — the address names no position (an undeclared field, an index
  outside the list). Missing for every operation.
- **empty slot** — the position exists but holds nothing (a map key with no
  entry). Missing only for `remove` and `nest`; the writing kinds fill it.
- **presence** — whether a declared field holds a value. Never missing.

This one was found by the implementation. The first draft collapsed all three,
and the operation table then contradicted the vacancy rule: `assign` on a map
key said "creating it if absent" while the vacancy rule made an absent key a
missing target that fails by default. Both cannot hold — together they make a
map entry impossible to create.

The same collapse made `test.exists = false` unsatisfiable for list indices and
map keys, since an absent target aborted before the assertion could read it.

### `move` and `copy` read before they write

The source is resolved and read before any target is written, and cleared once
after all of them, so the result does not depend on target order.

Degenerate cases are defined rather than emergent. A target that resolves to the
source is a no-op ([RFC 6902 §4.4](https://www.rfc-editor.org/rfc/rfc6902#section-4.4));
an unresolvable source is an error. The previous implementation deleted the field
in the first case and cleared the *destination* in the second — both silently,
so a patch meant to relocate an optional field became one that deleted the
destination whenever the field happened to be unset.

Source and target must share a **declared type**, not just a kind. Two enums are
both kind `e`, and moving a number between unrelated enums would silently change
what it means.

### `remove` is a message, not a bool

`Remove {}` rather than `bool remove`.

The oneof case already carries presence, so a bool payload is redundant — and
`remove: false` was a well-formed operation whose effect was a defined no-op
reporting success. Being the only scalar arm, it was also the one kind that
could never grow a modifier without burning a new kind number.

---

## Failure

### Atomicity, and therefore no in-place API

A patch applies entirely or not at all. Pre-validating the document is not
enough — an entry's legality can depend on state an earlier entry produced — so
an implementation applies to a private copy and publishes on success.

An applier that mutates a caller-owned value cannot honor that, so none is
offered. The previous implementation's primary entry point did exactly that: a
guard firing on entry 3 left entries 1 and 2 applied, which is the most damaging
possible reading for anyone carrying over RFC 6902's mental model.

### Errors carry a position

Every failure names where in the document it is:
`delta.entries[0].nest.delta.entries[0].assign.value`.

With this many failure conditions, "apply failed" is not a debuggable answer.
The error taxonomy is API, not diagnostics — which is also why it lives in the
exported `patch` package rather than an internal one.

### `test` cannot pass vacuously

`on_missing` may not be set on a test, an unresolvable target is read rather than
skipped, and a test whose selectors reach zero locations is an error.

A `test` exists only to make conditional application safe. The previous
implementation returned nil for a test against a nonexistent field, so a guard
whose address had drifted reported success.

### Unknown fields are refused, and preserved

Two different rules that are easy to confuse.

**In the patch**: any unknown field number, at any depth, aborts the document.
Protobuf reports an unrecognized oneof arm as "not set", so the unknown-field set
is the only thing separating "the producer omitted it" from "the producer used an
arm from a newer revision". Without the scan, the first new arm makes deployed
readers apply a subset of a stored document and report success.

**On the target**: fields present on the wire but not declared by the descriptor
are preserved. A patch never discards data it did not name.

---

## Evolution

### `message_type` is optional; its presence is the assertion

Set, and an applier refuses the patch against anything else. Unset, and the
patch is type-agnostic.

Resource messages routinely share a prefix of common fields — a name, an etag,
labels — and a patch addressing only those applies to all of them. Requiring a
type would force either a copy of the document per resource or rewriting the
field before each apply, and rewriting a stored document to make it applicable
defeats storing it.

This does not weaken fail-closed. `on_missing` decides what happens when
something *fails*; `message_type` is an assertion the author may or may not make,
and declining to check one nobody made is not doing less than the document asked.

**Cost.** The coarsest integrity check becomes opt-in. The fine one is per-field
and survives untouched.

An empty string is not absence: `""` is a declaration no message can satisfy.

### A revision number, not feature strings

`min_reader_revision` is a monotone integer.

A feature-string list is finer-grained but needs a registry, a naming
convention, and producer discipline the schema cannot enforce — and if the
schema does not say which strings exist, two conforming readers disagree about
the same document. An integer needs none of that.

**Cost.** A revision locks out older readers for every document, not only those
using the changed construct. For a young format that trade is worth taking.

### Versioning by package name

The package is `patch`, with no version suffix; a breaking revision becomes
`patchv2`. `buf lint`'s `PACKAGE_VERSION_SUFFIX` is excepted in `buf.yaml` with
that reason recorded.

### Presence is pinned, not inherited

`features.field_presence = EXPLICIT` and `features.enum_type = OPEN` are stated
in the schema rather than left to the edition default.

`Field.name`/`.number` and `Range.begin`/`.end` mean nothing without
unset-versus-zero, and an `OnMissing` value a reader does not recognize has to
be visible to it. Both dependencies are load-bearing and neither was obvious, so
they are declared where a future edit will see them.

---

## Scope

### No `Diff`

The library validates and applies patches; it does not generate them from a pair
of messages. A patch is built by hand.

### No RFC 6902 converter

It existed briefly and was removed. It came from the previous repository and
entered the plan by inheritance — it survived scoping because `Diff` and the
struct backend were cut by name while this was not mentioned. What was actually
wanted was an engine that applies a patch to a JSON document, which is
`patchjson`, and that has nothing to do with the JSON Patch standard.

The schema still cites RFC 6901 and RFC 6902 where it borrowed a concept.

### Three engines, one set of rules

`patchproto`, `patchstruct`, `patchjson` — see [engines.md](engines.md).

The previous design also had three backends, and they drifted until the same
bytes were destructive under one, inert under another, and wrote to the wrong
container under a third. The root cause was that each resolved addresses itself.
Here the shared rules have exactly one implementation, in `patch`, and a
conformance corpus binds the engines to a common answer.

---

## Structure

### `patch` is exported, not internal

The plan put the shared rules in `internal/spec`. That could not work: the error
taxonomy is API, and hiding it under `internal/` would leave callers unable to
inspect a `Code`.

The split is instead by whether a *target instance* is needed. `patch` takes a
document and a descriptor; the engines take an instance. The shared rules still
have one implementation and the types are exported.

### The corpus is data

Conformance cases are textproto, not Go, so a second implementation can run them
without importing the first one's tests.

Every case also asserts the input is unchanged, so atomicity is a global
invariant of the suite rather than one case's subject.

### Divergence must be declared

Each engine runs the whole corpus, and any case it does not agree on must be
listed with a cause. A declared divergence that starts agreeing fails too.

The danger was never that the engines differ — they must, since JSON has no
field numbers and Go has no oneofs. It is that they differ silently. This has
already earned its keep twice: it caught `patchstruct` refusing to descend into
a nil map when the format says a map field always exists as a container, and it
caught two undeclared disagreements the moment `message_type` became optional.

### The builder makes invalid patches unrepresentable

Empty `Targets`, an entry with no scope or kind, a `test` that tolerates a
missing target, a `move` onto a container — none of these compile.

What types cannot catch accumulates and surfaces at `New`. The negative test
cases are assembled from `patchpb` directly, which is the point: if the builder
could express them, the builder would be wrong.
