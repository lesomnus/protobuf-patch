package jsonpatch_test

import (
	"encoding/json"
	"testing"

	"github.com/lesomnus/protobuf-patch/jsonpatch"
)

func TestUnmarshal(t *testing.T) {
	t.Run("parses all op types", func(t *testing.T) {
		data := []byte(`[
			{"op":"add",     "path":"/a",   "value":1},
			{"op":"remove",  "path":"/b"},
			{"op":"replace", "path":"/c",   "value":"x"},
			{"op":"move",    "path":"/d",   "from":"/e"},
			{"op":"copy",    "path":"/f",   "from":"/g"},
			{"op":"test",    "path":"/h",   "value":true}
		]`)
		doc, err := jsonpatch.Unmarshal(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(doc) != 6 {
			t.Fatalf("got %d ops, want 6", len(doc))
		}
	})

	t.Run("preserves value as raw JSON", func(t *testing.T) {
		data := []byte(`[{"op":"add","path":"/x","value":{"nested":true}}]`)
		doc, err := jsonpatch.Unmarshal(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := json.RawMessage(`{"nested":true}`)
		if string(doc[0].Value) != string(want) {
			t.Errorf("got %s, want %s", doc[0].Value, want)
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := jsonpatch.Unmarshal([]byte(`not json`)); err == nil {
			t.Error("expected error, got nil")
		}
	})
	t.Run("not an array", func(t *testing.T) {
		if _, err := jsonpatch.Unmarshal([]byte(`{"op":"add"}`)); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

func TestDocValidate(t *testing.T) {
	mustParse := func(t *testing.T, s string) jsonpatch.Doc {
		t.Helper()
		doc, err := jsonpatch.Unmarshal([]byte(s))
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		return doc
	}

	t.Run("valid ops pass", func(t *testing.T) {
		doc := mustParse(t, `[
			{"op":"add",     "path":"/a","value":1},
			{"op":"remove",  "path":"/b"},
			{"op":"replace", "path":"/c","value":2},
			{"op":"move",    "path":"/d","from":"/e"},
			{"op":"copy",    "path":"/f","from":"/g"},
			{"op":"test",    "path":"/h","value":3}
		]`)
		if err := doc.Validate(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("invalid op", func(t *testing.T) {
		doc := mustParse(t, `[{"op":"unknown","path":"/a"}]`)
		if err := doc.Validate(); err == nil {
			t.Error("expected error, got nil")
		}
	})

	for _, op := range []string{"move", "copy"} {
		t.Run(op+" missing from", func(t *testing.T) {
			doc := mustParse(t, `[{"op":"`+op+`","path":"/a"}]`)
			if err := doc.Validate(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}

	for _, op := range []string{"add", "replace", "test"} {
		t.Run(op+" missing value", func(t *testing.T) {
			doc := mustParse(t, `[{"op":"`+op+`","path":"/a"}]`)
			if err := doc.Validate(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}
