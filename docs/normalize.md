# Normalizing a patch for a whole-delta backend

The format says entries apply in order and each observes the effects of those
before it. A backend that applies the entries one at a time gets that for free.
One that compiles the whole document into a **single statement** does not, and
list indices are where it breaks.

`patch.Normalize` rewrites a document into an order such a backend can honor,
and — the part this page exists for — states exactly what it will not rewrite,
so that a backend author knows what is still theirs to refuse.

```go
q, err := patch.Normalize(p)   // structurally invalid input is the only error
```

- [The problem](#the-problem)
- [What it guarantees](#what-it-guarantees)
- [The arithmetic](#the-arithmetic)
- [What it leaves alone](#what-it-leaves-alone)
- [Reordering across a test](#reordering-across-a-test)
- [The normal form, and where it cannot be reached](#the-normal-form-and-where-it-cannot-be-reached)
- [What this is not](#what-this-is-not)

---

## The problem

A single statement nests its edits correctly — the second edit really does see
the first one's output. The **existence guards** do not. A guard lands in the
`WHERE` clause and is evaluated against the row as it stood *before* the
statement, so an index an earlier entry shifted is asked about at the wrong
position.

On `[10 20 30]`:

```
remove(0); assign(1) = 99
```

`assign(1)` means the element that was at 2. The backend asks "does index 1
exist" of the untouched row, which is a question about the element that was at 1.
Both answers are yes here, so nothing is caught — the guard is checking the wrong
element, and on `[10 20]` it would pass while the document should fail.

The fix is to say the same thing in the coordinates the guards are evaluated in:

```
assign(2) = 99; remove(0)
```

Now every index the document names is an index into the list as it stood before
the document began, which is the row the guards see.

## What it guarantees

Against any message:

| | |
| --- | --- |
| p applies | the returned document applies, to the **same value** |
| p is refused | the returned document is refused |

A failure is never swallowed and a success is never invented. That direction is
the one that matters: a transform preserving the successes and quietly dropping
the failures would be worse than no transform at all.

Verified against `patchproto` over generated documents — random sequences of
list operations over seeded starting lists, several hundred per shape, with the
seed logged so a failure is reproducible.

### The one thing it does not guarantee

**Which rule** a refusal names, when the message defeats *more than one* entry of
the document. Both orders refuse and neither writes anything, but the entry
reached first is the one blamed.

```
remove(9); assign i32_1 = "no"       → target does not exist
assign i32_1 = "no"; remove(9)       → illegal arm
```

The shape that reads as a contradiction is worth seeing, because both entries
fail for the very same reason:

```
on a list of one

remove(r_m_1[3]);  assign r_m_1[5].s_1     → target does not exist
assign r_m_1[5].s_1;  remove(r_m_1[3])     → path does not reach a container
```

Neither index is in the list. They are refused under different rules because the
format draws that line deliberately: an index in a `path` is never tolerated,
while an index in `targets` is what `on_missing` governs. Reordering decides
which one is reached, and so which is reported.

A document with a **single** defeated entry is refused for the same reason
whatever order it is in. Several of the barriers below exist to keep that true.

## The arithmetic

Length changes move later; the entries that jump over them are rewritten into the
earlier coordinates.

A **remove** at `j` shifts every later reference above `j` down by one, so an
entry moved in front of it addresses one higher:

```
remove(0); assign(1)      →  assign(2); remove(0)
remove(0); remove(1)      →  remove(2); remove(0)
```

An **insert** shifts the other way, and has positions that cannot be rewritten at
all: the element it created has no index in the list before it ran.

```
insert(1) = 7; assign(1)  →  left alone — index 1 is the element the insert made
```

Two length changes that end up next to each other are ordered **from the far end
of the list inward**, so that neither disturbs the other's indices. Two removes
stack; an insert may sit at the position another insert names, because an insert
names the *gap* in front of an element rather than the element — but not at a
position a remove took, because the gap in front of an element that is gone is
the position one past the end whenever that element was the last one, and whether
it was is a fact about the row.

Failures are preserved by the same map, in both directions. An index that is out
of range after the length change rewrites to one that is out of range before it:

```
on [10 20 30]

remove(0); assign(2) = 99  →  assign(3) = 99; remove(0)
     target does not exist          target does not exist
```

## What it leaves alone

This is the list a backend author has to read. Each of these stays exactly where
the author wrote it, and the reason is always the same in the end: the answer
depends on something that is not in the document.

| Left alone | Why |
| --- | --- |
| a **negative index**, anywhere | it counts from the end — the effective index is `len + i`, so it names a different element under every length |
| an **append** before an index reference | the appended element lands at the old length. The one index it moves is the one that named nothing before and names the new element after, and which index that is, is the length |
| a **range** | both bounds normalize against the length, and then clamp to it |
| a length change carrying **`on_missing = SKIP`** | whether the list shifted *at all* depends on whether the target was there, which is a fact about the row |
| an index naming an element an **insert created** | the earlier coordinates have no name for it |
| **container scope** | it empties, fills, or replaces a whole list, by an amount that depends on what is in it |
| **`move` / `copy`** | the source is read as the entries before it left it, and `move` clears it — for a list element that is a length change bundled with a write somewhere else |
| **`every_entry`** | it resolves against the keys the map holds, which are data |
| **`oneof_member`** | it resolves to whichever member is set, which the document does not say |
| **`on_absent_path = CREATE`** | the entry may materialize a container a later entry needs. Run first it creates an empty list and the list operation is refused for the position; run second the list operation is refused for the path. Both refuse — but naming a different rule for a document with one thing wrong with it is a worse answer than not reordering |
| a **`test`** | see below |
| two length changes whose positions **interleave** | no order of them leaves the others' indices untouched |
| **indices mixed with a field or map key** in one entry's targets | the entry is partly a position and partly a name, and only the positions would shift — so it is treated as one indivisible thing and left where it is |
| two length changes on **different lists** | each is only ever exchanged with entries that name a position, so two of them keep their order relative to each other. `[remove A[0], remove B[0], assign A[1]]` still reaches the normal form, by moving the assign rather than the removes |
| two entries reaching into **different elements of the same list of messages** | two indices are never called distinct, because whether `[0]` and `[1]` are the same element is a fact about the list |
| **`on_absent_path = CREATE`** with an empty path | it creates nothing, but the barrier does not look at the path — the conservative answer costs a reordering, the other costs a wrong one |
| a field named **two ways** | a `name` in one entry against a `number` in another may well be the same field, and normalization has no descriptor to ask |
| anything this build does not recognize | a construct added to the schema without teaching the pass about it stops the reordering rather than being reordered wrongly |

**`nest` is crossed.** It is the one compound construct that is, and it is safe
because it only descends: the inner delta reaches nothing but the container the
outer entry hands it, and an index naming that container is rewritten like any
other index, which keeps it naming the same element. A `nest` whose target *is*
the list, or a container holding it, is refused by the same rule that refuses any
other write to it — including the fact that a `nest` materializes its target.

Ordinary writes to **map keys** and to **fields** are crossed freely. They change
no list's length, so no index means anything different on the other side of them.

## Reordering across a test

**Nothing is ever moved across a `test`, in either direction, whatever it
addresses.**

An entry observes the effects of those before it, so a `test` is the one place a
document reads. Moving a write past a test on the same path plainly changes what
the test sees — asserting that a list is non-empty *before* rather than *after*
the remove that empties it is a different assertion.

A test on a provably unrelated path could be crossed without changing whether it
holds. It is still not crossed. A document that fails must fail the same way, and
a test is what a failing document is most likely to fail on, so moving one
changes which failure gets reported — for a document whose only defect is that
test.

## The normal form, and where it cannot be reached

The form aimed at: **every index the document names is an index into the list as
it stood before the document began.**

It is deliberately not the narrower "no index appears after any entry that
changed the length". Two removes cannot satisfy that reading at all — one of them
has to come second — and they do not need to. Ordered from the far end inward,
neither disturbs the index the other names, and both are still start
coordinates. What matters is the coordinates, not the adjacency.

The form is always reachable for **single-target entries on a list that only
shrinks**, and that is the only shape for which it is claimed. Two cases show why
nothing wider is:

```
insert(2) = X;  assign(2)          index 2 is the element the insert created
remove{0,4};    remove(1)          {0,4} straddles 1 — no order separates them
```

For the second: put the single removal first and it is `remove(2)`, but then
`{0,4}` is running against a list that has lost an element below 4, so 4 no
longer names what it named. Put it second and 1 is a coordinate the first removal
produced. There is no third order.

A document that cannot be brought all the way comes back **partly normalized, or
not at all** — never mangled into an order that merely looks normalized. Every
individual exchange preserves meaning on its own, so stopping early is safe.

## What this is not

It is **not a validator**. It is total over well-formed documents: it never
refuses one for being un-normalizable, because deciding whether the remainder is
acceptable belongs to the backend. The error it returns is `Validate`'s, for a
document that is not well-formed at all — including the unknown-field scan, so
that a document carrying an arm from a newer revision is refused rather than
reordered as though the part understood were the whole.

It is **not a promise that the result is applicable in one statement**. It brings
what it can prove into start coordinates and leaves the rest. A backend still has
to check what it got, and the table above is the list of what it will still see.

It does **not** merge, split, or drop entries. The returned document has exactly
the entries `p` had, in a different order, with some indices rewritten — which is
why blame can move between them but never vanish.

`p` is not modified; the result is a fresh document.
