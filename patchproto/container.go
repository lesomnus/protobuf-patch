package patchproto

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/lesomnus/protobuf-patch/patch"
	"github.com/lesomnus/protobuf-patch/patchpb"
)

// applyToContainer runs an operation whose scope is the container reached by
// the entry's path, rather than positions inside it.
//
// This is an explicit scope arm, not the absence of targets. The previous
// schema derived it from an empty target list, which made the most destructive
// operation available the natural result of a producer that computed nothing.
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

	// move and copy onto a container are rejected by Validate.
	return patch.Errf(patch.CodeIllegalScope, at, "not defined for a container")
}

// checkContainerArm requires the Value to be the container's own form: `m` for
// a message, `l` for a list, `map` for a map.
func checkContainerArm(c cont, v *patchpb.Value, at patch.At) error {
	want := patch.ShapeMessage
	name := "m"
	switch {
	case c.isList():
		want, name = patch.ShapeList, "l"
	case c.isMap():
		want, name = patch.ShapeMap, "map"
	}
	if got := patch.ShapeOf(v); got != want {
		return patch.Errf(patch.CodeIllegalArm, at, "%s takes %s", c.describe(), name)
	}
	return nil
}

func emptyContainer(c cont) {
	switch {
	case c.isMsg():
		clearMessage(c.msg)
	case c.isList():
		c.list.Truncate(0)
	default:
		clearMap(c.mp)
	}
}

func isEmptyContainer(c cont) bool {
	switch {
	case c.isMsg():
		empty := true
		c.msg.Range(func(protoreflect.FieldDescriptor, protoreflect.Value) bool {
			empty = false
			return false
		})
		return empty
	case c.isList():
		return c.list.Len() == 0
	default:
		return c.mp.Len() == 0
	}
}

// assignContainer replaces the container wholesale: clear, then apply.
func assignContainer(c cont, v *patchpb.Value, at patch.At) error {
	if err := checkContainerArm(c, v, at); err != nil {
		return err
	}
	switch {
	case c.isMsg():
		// Resolve everything before clearing, so that a value naming a field
		// this message does not declare fails with the target intact rather
		// than after it has been emptied.
		staged := c.msg.New()
		if err := fillMessage(staged, v.GetM(), at.Sub("m")); err != nil {
			return err
		}
		clearMessage(c.msg)
		staged.Range(func(fd protoreflect.FieldDescriptor, pv protoreflect.Value) bool {
			c.msg.Set(fd, pv)
			return true
		})
		return nil

	case c.isList():
		c.list.Truncate(0)
		return fillList(c.list, c.fd, v.GetL(), at.Sub("l"))

	default:
		clearMap(c.mp)
		return fillMap(c.mp, c.fd, v.GetMap(), at.Sub("map"))
	}
}

// insertContainer fills only what the container does not already have. A
// position that is already occupied is an error, exactly as it is for a
// targeted insert.
func insertContainer(c cont, v *patchpb.Value, at patch.At) error {
	if err := checkContainerArm(c, v, at); err != nil {
		return err
	}
	switch {
	case c.isMsg():
		for i, fv := range v.GetM().GetFields() {
			fat := at.Sub("m").Index("fields", i)
			fd, vacant, err := patch.ResolveField(c.msg.Descriptor(), fv.GetKey(), fat.Sub("key"))
			if err != nil {
				return err
			}
			if vacant {
				return patch.Errf(patch.CodeVacantTarget, fat.Sub("key"),
					"%s declares no such field", c.msg.Descriptor().FullName())
			}
			if occupied(c.msg, fd) {
				return patch.Errf(patch.CodeOccupied, fat.Sub("key"), "%s is already set", fd.FullName())
			}
			if err := setField(c.msg, fd, fv.GetValue(), fat.Sub("value")); err != nil {
				return err
			}
		}
		return nil

	case c.isList():
		return fillList(c.list, c.fd, v.GetL(), at.Sub("l"))

	default:
		for i, me := range v.GetMap().GetEntries() {
			eat := at.Sub("map").Index("entries", i)
			mk, err := patch.MapKeyFor(me.GetKey(), c.fd, eat.Sub("key"))
			if err != nil {
				return err
			}
			if c.mp.Has(mk) {
				return patch.Errf(patch.CodeOccupied, eat.Sub("key"),
					"%s already has that key", c.fd.FullName())
			}
			pv, err := singular(me.GetValue(), c.fd, patch.SiteMapValue, c.mp.NewValue, eat.Sub("value"))
			if err != nil {
				return err
			}
			c.mp.Set(mk, pv)
		}
		return nil
	}
}

func testContainer(c cont, t *patchpb.Test, at patch.At) error {
	if t.WhichWant() == patchpb.Test_Exists_case {
		// A container reached by a path always exists, so "exists" reports
		// whether it holds anything.
		if isEmptyContainer(c) == t.GetExists() {
			return patch.Errf(patch.CodeTestFailed, at,
				"%s is %s", c.describe(), emptiness(isEmptyContainer(c)))
		}
		return nil
	}

	v := t.GetValue()
	if err := checkContainerArm(c, v, at.Sub("value")); err != nil {
		return err
	}

	ok := false
	var err error
	switch {
	case c.isMsg():
		staged := c.msg.New()
		if err = fillMessage(staged, v.GetM(), at.Sub("value").Sub("m")); err == nil {
			ok = equalMessage(c.msg, staged)
		}
	case c.isList():
		ok, err = equalList(c.list, c.fd, v.GetL(), at.Sub("value"))
	default:
		ok, err = equalMap(c.mp, c.fd, v.GetMap(), at.Sub("value"))
	}
	if err != nil {
		return err
	}
	if !ok {
		return patch.Errf(patch.CodeTestFailed, at, "%s does not equal the value", c.describe())
	}
	return nil
}

func emptiness(empty bool) string {
	if empty {
		return "empty"
	}
	return "not empty"
}
