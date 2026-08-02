# Checking what a patch would change

A patch document arrives from somewhere you do not control, and some of the
target's fields are not the sender's to write — an id, a creation timestamp,
a status the server observes. You want to refuse the document, not sanitize it.

Two pieces, in package `patch`:

| | |
| --- | --- |
| `Writes(md, p)` | every field `p` may modify, from the descriptor alone |
| `ReadOnly` | a compiled set of protected fields, and a `Check` against it |

Neither needs the target instance, so the check runs before `Apply` and costs
nothing but a walk of the document.

## Using it

```go
md := (&pb.User{}).ProtoReflect().Descriptor()
ro := patch.MustNewReadOnly(md, "id", "created_at", "status.observed_at")
```

Paths are dot-separated field names from the root. They are resolved at
construction, so a typo is a startup failure rather than a check that silently
protects nothing. A path through a repeated or map field names that field in
every element or entry: `"items.created_at"` is that field of every item.

```go
if err := ro.Check(p); err != nil {
    return err     // *patch.Error, CodeReadOnly, At the entry to blame
}
out, err := patchproto.Apply(user, p)
```

The error carries the document position the same way every other error in this
package does:

```
delta.entries[2].targets.selectors[0]: field is read-only: created_at is read-only
```

`Check` is a **refusal, not a filter**. It never edits the document to drop the
offending entry: the producer asked for something it was not allowed to ask
for, and applying the rest would leave the two sides believing different things
about what happened.

A document that fails `Validate`, or that is addressed to another message type,
fails `Check` with that error instead. An unreadable document has not been shown
to respect the policy, so it must not pass a check whose whole job is to say
that it does.

## What counts as a write

| Operation | Reported |
| --- | --- |
| `assign`, `insert`, `remove` | its targets |
| `copy` | its targets — the source is only read |
| `move` | its targets **and its source**, which it clears |
| `nest` | whatever the inner delta writes, **plus the target itself** |
| `test` | nothing |

Two of those rows are the ones a hand-rolled check gets wrong.

**`move` empties its source.** A policy that only looked at targets would let
`move` carry a protected field away.

**`nest` populates its target.** Descending into an unset singular message
field materializes it, so `nest` changes `m_1` even when the delta inside is a
bare assertion. Verified against the reference engine, not assumed.

`test` is the one operation that contributes nothing, which is what lets a
client assert about a field it may not write — an etag check on a server-owned
version field still works under a policy that freezes it.

## Two ways a write reaches a field

`Write.Affects` decides whether a write can change a given field, and the rule
depends on which kind of write it is.

An ordinary write at `W` affects `R` when **either path is a prefix of the
other**:

- Writing `spec.created_at` changes `spec`, because a message *is* its fields.
  Freezing `spec` is not honored by a patch that only reaches inside it.
- Assigning `spec` replaces `spec.created_at` along with everything else it
  held, whether or not the document names it.

A **materializing** write — path creation under `on_absent_path`, and the
target of a `nest` — only brings its path into existence as an empty container.
It affects `R` only when `R` names it or an ancestor of it, never a field
beneath it. Creating `spec` cannot drop `spec.created_at`, because a container
that did not exist held nothing to drop; treating it as a replacement would
make `nest` and `InOrCreate` unusable under any policy on a nested field.

```go
patch.Write{Path: fp("m_1")}.Affects(fp("m_1.s_1"))                      // true
patch.Write{Path: fp("m_1"), Materializes: true}.Affects(fp("m_1.s_1"))  // false
patch.Write{Path: fp("m_1"), Materializes: true}.Affects(fp("m_1"))      // true
```

## Granularity: fields, not positions

`FieldPath` elides list indices and map keys. `r_m_1[0].s_1` and `r_m_1[7].s_1`
are both `r_m_1.s_1`.

That is the granularity a policy is stated at — "this field is read-only", not
"element 3 of it is" — and a patch that rewrites one element of a protected
list has changed the protected field as surely as one that replaces the whole
list.

The cost is that a policy cannot name one key of a map: freezing `labels`
freezes every entry of it. Anything finer would have to answer for a
`Selector.range` or `every_entry` whose matched keys are a property of the
instance, which a descriptor-level analysis does not have.

## Widened, never narrowed

`Writes` over-approximates on purpose. A refusal that is occasionally too
strict is a policy decision; one that is occasionally too lax is a hole.

- `Selector.oneof_member` resolves to whichever member is **set**, which no
  descriptor can know. Every member of the oneof is reported — so protecting
  one member refuses the selector that could have hit it.
- `Selector.range`, `append`, and `every_entry` may match nothing at all
  against a given instance. The field is reported anyway.
- `on_missing = SKIP` may drop a target. The target is reported anyway.

It is not an under-approximation anywhere, and two things keep it that way.
`Writes` runs `Validate` first, so a document carrying a field from a **newer
revision** — an operation or selector this build cannot interpret — is refused
rather than analyzed as though the part understood were the whole. And every
switch over an arm is exhaustive with an erroring default, so a new arm added
without teaching `Writes` about it cannot compile down to a silent "modifies
nothing".

An `assign` carrying a nested message literal is reported as a write to its
target and not descended into. Prefix matching already covers every field the
literal names — and, correctly, the ones it does not, which the assign drops.

## Using `Writes` directly

`ReadOnly` is a thin layer over it. Use the primitive for anything it does not
express — routing an approval, logging a change summary, a policy keyed on
something other than a field path:

```go
ws, err := patch.Writes(md, p)
for _, w := range ws {
    fmt.Println(w.Path, w.At, w.Materializes)
}
```

The same field may be reported more than once, once per position in the
document that writes it. Order follows the document.

## What this is not

It is **caller policy, not part of the format**. Nothing in the schema marks a
field unwritable, and a document `ReadOnly` rejects is a perfectly valid patch
that would have applied. `CodeReadOnly` sits in the error taxonomy for
convenience — so a handler that already switches on `CodeOf` does not need a
second branch — and is marked there as not being a rule of the format.

It is also not a permission model. It answers one question, "may this document
touch this field", and holds no notion of who is asking. Per-caller policies
are several `ReadOnly` values, chosen before `Check`.
