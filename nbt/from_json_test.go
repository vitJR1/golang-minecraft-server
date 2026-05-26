package nbt

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestFromJSONBasic(t *testing.T) {
	json := []byte(`{"a": "hi", "b": true, "c": 0}`)
	root, err := FromJSONBytes(json, TypeHints{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := root["a"].(String); !ok {
		t.Errorf("a: want String, got %T", root["a"])
	}
	if root["b"] != Byte(1) {
		t.Errorf("b: want Byte(1), got %v", root["b"])
	}
	if root["c"] != Int(0) {
		t.Errorf("c: want Int(0), got %v (%T)", root["c"], root["c"])
	}
}

func TestFromJSONHints(t *testing.T) {
	json := []byte(`{
		"has_skylight": 1,
		"coordinate_scale": 8,
		"ambient_light": 0,
		"fixed_time": 18000,
		"id": 0
	}`)
	hints := TypeHints{
		ByteKeys:   map[string]bool{"has_skylight": true},
		DoubleKeys: map[string]bool{"coordinate_scale": true},
		FloatKeys:  map[string]bool{"ambient_light": true},
		LongKeys:   map[string]bool{"fixed_time": true},
	}
	root, err := FromJSONBytes(json, hints)
	if err != nil {
		t.Fatal(err)
	}
	if root["has_skylight"] != Byte(1) {
		t.Errorf("has_skylight: want Byte(1), got %v (%T)", root["has_skylight"], root["has_skylight"])
	}
	if root["coordinate_scale"] != Double(8) {
		t.Errorf("coordinate_scale: want Double(8), got %v (%T)", root["coordinate_scale"], root["coordinate_scale"])
	}
	if root["ambient_light"] != Float(0) {
		t.Errorf("ambient_light: want Float(0), got %v (%T)", root["ambient_light"], root["ambient_light"])
	}
	if root["fixed_time"] != Long(18000) {
		t.Errorf("fixed_time: want Long(18000), got %v (%T)", root["fixed_time"], root["fixed_time"])
	}
	if root["id"] != Int(0) {
		t.Errorf("id: want Int(0) (no hint), got %v (%T)", root["id"], root["id"])
	}
}

func TestFromJSONNestedAndList(t *testing.T) {
	json := []byte(`{"value":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`)
	root, err := FromJSONBytes(json, TypeHints{})
	if err != nil {
		t.Fatal(err)
	}
	got := Marshal(root)
	// Just sanity-check it Marshal'd without panics and has the expected names.
	if !bytes.Contains(got, []byte("name")) || !bytes.Contains(got, []byte("value")) {
		t.Fatalf("expected names in NBT: %s", hex.EncodeToString(got))
	}
}

func TestFromJSONMixedListErrors(t *testing.T) {
	json := []byte(`{"bad":[1,"two"]}`)
	_, err := FromJSONBytes(json, TypeHints{})
	if err == nil {
		t.Fatal("expected error for mixed-type list")
	}
}
