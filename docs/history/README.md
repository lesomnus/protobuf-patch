# History

Working documents, kept for the reasoning in them. They are **not** current
documentation and are written in Korean.

The conclusions that outlived them are in [decisions.md](../decisions.md); the
rules themselves are in [`proto/patch/`](../../proto/patch/) and
[format.md](../format.md).

| | |
| --- | --- |
| [schema-review.md](schema-review.md) | A review of the previous design of this format, which found 52 defects across six independent lenses. The evidence base for most of what follows. |
| [spec-defects.md](spec-defects.md) | The 21 of those that were defects in the *definition* rather than in some implementation of it, with the standard used to separate the two. |
| [redesign.md](redesign.md) | What the current schema decided against that list, and the choices made where more than one answer would have worked. |
| [plan.md](plan.md) | The implementation plan the current code was built to. |
| [build-log.md](build-log.md) | What was built, in order, and the decisions that only became visible while building it. |

The code these reviewed is preserved at commit `2a96ef1` of
`github.com/lesomnus/protobuf-diff`. Nothing in this directory describes the
current implementation.
