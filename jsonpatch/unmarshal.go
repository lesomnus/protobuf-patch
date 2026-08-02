package jsonpatch

import (
	"encoding/json"
	"fmt"
)

func Unmarshal(data []byte) (Doc, error) {
	doc := Doc{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (d Doc) Validate() error {
	for _, op := range d {
		switch op.Op {
		case "move", "copy":
			if op.From == "" {
				return fmt.Errorf("op %q: missing 'from' field", op.Op)
			}

		case "add", "replace", "test":
			if op.Value == nil {
				return fmt.Errorf("op %q: missing 'value' field", op.Op)
			}

		case "remove":

		default:
			return fmt.Errorf("invalid op: %q", op.Op)
		}
	}

	return nil
}
