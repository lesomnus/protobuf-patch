package jsonpatch

import "encoding/json"

type Doc []Op

type Op struct {
	Op    string
	Path  string
	From  string
	Value json.RawMessage
}
