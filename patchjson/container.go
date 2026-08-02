package patchjson

import (
	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// applyToContainer runs an operation whose scope is the container the entry's
// path reached, rather than positions inside it.
func applyToContainer(c *cont, e *patchpb.Entry, at patch.At) error {
	switch e.WhichKind() {
	case patchpb.Entry_Remove_case:
		emptyContainer(c)
		return nil

	case patchpb.Entry_Test_case:
		return testContainer(c, e.GetTest(), at.Sub("test"))

	case patchpb.Entry_Insert_case:
		return insertContainer(c, e.GetInsert().GetValue(), at.Sub("insert").Sub("value"))

	case patchpb.Entry_Assign_case:
		return assignContainer(c, e.GetAssign().GetValue(), at.Sub("assign").Sub("value"))

	case patchpb.Entry_Nest_case:
		return applyDelta(c, e.GetNest().GetDelta(), at.Sub("nest").Sub("delta"))
	}
	return patch.Errf(patch.CodeIllegalScope, at, "not defined for a container")
}

// containerValue renders a Value that must be the container's own form.
//
// An object accepts both `m` and `map`: with no schema a message and a map are
// the same thing, and refusing one would only make the author guess which
// spelling this engine wanted.
func containerValue(c *cont, v *patchpb.Value, at patch.At) (any, error) {
	shape := patch.ShapeOf(v)
	if c.isArr {
		if shape != patch.ShapeList {
			return nil, patch.Errf(patch.CodeIllegalArm, at, "an array takes l")
		}
	} else if shape != patch.ShapeMessage && shape != patch.ShapeMap {
		return nil, patch.Errf(patch.CodeIllegalArm, at, "an object takes m or map")
	}
	return render(v, at)
}

func emptyContainer(c *cont) {
	if c.isArr {
		c.setArr([]any{})
		return
	}
	for k := range c.obj {
		delete(c.obj, k)
	}
}

func isEmptyContainer(c *cont) bool {
	if c.isArr {
		return len(c.arr) == 0
	}
	return len(c.obj) == 0
}

// assignContainer replaces the container wholesale.
func assignContainer(c *cont, v *patchpb.Value, at patch.At) error {
	// Render before clearing, so a value this engine cannot interpret leaves
	// the container intact rather than emptying it and then failing.
	rendered, err := containerValue(c, v, at)
	if err != nil {
		return err
	}
	if c.isArr {
		c.setArr(rendered.([]any))
		return nil
	}
	for k := range c.obj {
		delete(c.obj, k)
	}
	for k, e := range rendered.(map[string]any) {
		c.obj[k] = e
	}
	return nil
}

// insertContainer fills only what the container does not already have.
func insertContainer(c *cont, v *patchpb.Value, at patch.At) error {
	rendered, err := containerValue(c, v, at)
	if err != nil {
		return err
	}
	if c.isArr {
		c.setArr(append(c.arr, rendered.([]any)...))
		return nil
	}
	obj := rendered.(map[string]any)
	for k := range obj {
		if _, present := c.obj[k]; present {
			return patch.Errf(patch.CodeOccupied, at, "%q is already there", k)
		}
	}
	for k, e := range obj {
		c.obj[k] = e
	}
	return nil
}

func testContainer(c *cont, t *patchpb.Test, at patch.At) error {
	if t.WhichWant() == patchpb.Test_Exists_case {
		// The container itself always exists — a path that does not reach one
		// is already an error — so `exists` reports whether it holds anything.
		if isEmptyContainer(c) == t.GetExists() {
			return patch.Errf(patch.CodeTestFailed, at, "the %s is %s",
				c.describe(), emptiness(isEmptyContainer(c)))
		}
		return nil
	}

	want, err := containerValue(c, t.GetValue(), at.Sub("value"))
	if err != nil {
		return err
	}
	var got any = c.obj
	if c.isArr {
		got = c.arr
	}
	if !sameJSON(got, want) {
		return patch.Errf(patch.CodeTestFailed, at, "the %s does not equal the value", c.describe())
	}
	return nil
}

func emptiness(empty bool) string {
	if empty {
		return "empty"
	}
	return "not empty"
}
