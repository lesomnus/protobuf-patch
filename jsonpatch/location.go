package jsonpatch

import (
	"iter"
	"strings"
)

type Location string

// /foo/bar/0/baz/~0~1~2/q~1u~x
// -> ["foo", "bar", "0", "baz", "~/~2", "q/u~x"]
func (l Location) Seq() iter.Seq[string] {
	return func(yield func(string) bool) {
		s := string(l)
		if len(s) == 0 {
			return
		}
		if s[0] == '/' {
			s = s[1:]
		}
		for {
			seg, rest, more := strings.Cut(s, "/")
			seg = strings.ReplaceAll(seg, "~1", "/")
			seg = strings.ReplaceAll(seg, "~0", "~")
			if !yield(seg) {
				return
			}
			if !more {
				return
			}
			s = rest
		}
	}
}
