package x

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
)

func NoError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	t.Fatalf("unexpected error: %v", err)
}

func Error(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		return
	}
	t.Fatal("expected error, got nil")
}

func Eq[T any](t *testing.T, expected, actual T) {
	t.Helper()
	if reflect.DeepEqual(expected, actual) {
		return
	}
	t.Fatalf("unexpected value: got %v, want %v", actual, expected)
}

func NotNil(t *testing.T, value any) {
	t.Helper()
	if value != nil {
		return
	}
	t.Fatal("expected non-nil value, got nil")
}

func Same(t *testing.T, expected, actual any) {
	t.Helper()
	if expected == actual {
		return
	}
	t.Fatalf("unexpected value: got %v, want %v", actual, expected)
}

func TypeEq(t *testing.T, expected, actual any) {
	t.Helper()
	if reflect.TypeOf(expected) == reflect.TypeOf(actual) {
		return
	}
	t.Fatalf("unexpected type: got %T, want %T", actual, expected)
}

func TypeImpl[T any](t *testing.T, expected *T, actual any) T {
	t.Helper()
	if v, ok := actual.(T); ok {
		return v
	}

	t.Fatalf("unexpected type: got %T, want implements %T", actual, expected)

	var z T
	return z
}

func Len[T any](t *testing.T, value []T, size int) {
	t.Helper()
	if len(value) == size {
		return
	}
	t.Fatalf("unexpected length: got %d, want %d", len(value), size)
}

func PbEq(t *testing.T, expected, actual proto.Message) {
	t.Helper()
	if proto.Equal(expected, actual) {
		return
	}
	t.Fatalf("unexpected protobuf message: got %v, want %v", actual, expected)
}
