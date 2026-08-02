package patchstruct

import (
	"reflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// applyToContainer runs an operation whose scope is the container the entry's
// path reached, rather than positions inside it.
func applyToContainer(c cont, e *patchpb.Entry, at patch.At) error {
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

func emptyContainer(c cont) {
	switch c.kind() {
	case reflect.Struct:
		for i := range c.v.NumField() {
			if c.v.Type().Field(i).IsExported() {
				clearValue(c.v.Field(i))
			}
		}
	case reflect.Map:
		c.v.Set(reflect.MakeMap(c.v.Type()))
	default:
		c.v.Set(reflect.MakeSlice(c.v.Type(), 0, 0))
	}
}

func isEmptyContainer(c cont) bool {
	switch c.kind() {
	case reflect.Struct:
		for i := range c.v.NumField() {
			if c.v.Type().Field(i).IsExported() && present(c.v.Field(i)) {
				return false
			}
		}
		return true
	default:
		return c.v.Len() == 0
	}
}

// containerValue converts a Value that must be the container's own form.
func containerValue(c cont, v *patchpb.Value, at patch.At) (reflect.Value, error) {
	want := shapeOf(c.v.Type())
	if got := patch.ShapeOf(v); got != want {
		return reflect.Value{}, patch.Errf(patch.CodeIllegalArm, at,
			"%s takes %s", c.describe(), shapeName(want))
	}
	return convert(v, c.v.Type(), at)
}

func assignContainer(c cont, v *patchpb.Value, at patch.At) error {
	// Convert before clearing, so a value this engine cannot interpret leaves
	// the container as it was rather than emptying it and then failing.
	out, err := containerValue(c, v, at)
	if err != nil {
		return err
	}
	if c.kind() == reflect.Struct {
		// Preserve whatever the Patch cannot address.
		for i := range c.v.NumField() {
			if c.v.Type().Field(i).IsExported() {
				c.v.Field(i).Set(out.Field(i))
			}
		}
		return nil
	}
	c.v.Set(out)
	return nil
}

func insertContainer(c cont, v *patchpb.Value, at patch.At) error {
	out, err := containerValue(c, v, at)
	if err != nil {
		return err
	}
	switch c.kind() {
	case reflect.Struct:
		for i := range c.v.NumField() {
			sf := c.v.Type().Field(i)
			if !sf.IsExported() || !present(out.Field(i)) {
				continue
			}
			if present(c.v.Field(i)) {
				return patch.Errf(patch.CodeOccupied, at, "%s is already set", sf.Name)
			}
			c.v.Field(i).Set(out.Field(i))
		}
		return nil

	case reflect.Map:
		iter := out.MapRange()
		for iter.Next() {
			if c.v.MapIndex(iter.Key()).IsValid() {
				return patch.Errf(patch.CodeOccupied, at, "the map already has that key")
			}
		}
		iter = out.MapRange()
		for iter.Next() {
			c.v.SetMapIndex(iter.Key(), iter.Value())
		}
		return nil

	default:
		c.v.Set(reflect.AppendSlice(c.v, out))
		return nil
	}
}

func testContainer(c cont, t *patchpb.Test, at patch.At) error {
	if t.WhichWant() == patchpb.Test_Exists_case {
		if isEmptyContainer(c) == t.GetExists() {
			return patch.Errf(patch.CodeTestFailed, at, "%s is %s",
				c.describe(), emptiness(isEmptyContainer(c)))
		}
		return nil
	}
	want, err := containerValue(c, t.GetValue(), at.Sub("value"))
	if err != nil {
		return err
	}
	if !equalDeep(c.v, want) {
		return patch.Errf(patch.CodeTestFailed, at, "%s does not equal the value", c.describe())
	}
	return nil
}

func shapeName(s patch.Shape) string {
	switch s {
	case patch.ShapeMessage:
		return "m"
	case patch.ShapeList:
		return "l"
	case patch.ShapeMap:
		return "map"
	}
	return "?"
}

func emptiness(empty bool) string {
	if empty {
		return "empty"
	}
	return "not empty"
}
