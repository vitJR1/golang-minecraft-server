package store

import (
	"encoding/json"
	"testing"
)

func TestEnderChestRepoRoundTrip(t *testing.T) {
	s, ctx := testStore(t)
	const uuid = "22222222-2222-2222-2222-222222222222"

	got, err := s.EnderChests.LoadEnderChest(ctx, uuid)
	if err != nil || got != nil {
		t.Fatalf("empty load: %s / %v", got, err)
	}
	if err := s.EnderChests.SaveEnderChest(ctx, uuid, []byte(`[{"slot":0,"item":"minecraft:diamond","count":3}]`)); err != nil {
		t.Fatal(err)
	}
	if err := s.EnderChests.SaveEnderChest(ctx, uuid, []byte(`[{"slot":1,"item":"minecraft:stone","count":2}]`)); err != nil {
		t.Fatal(err)
	}
	got, err = s.EnderChests.LoadEnderChest(ctx, uuid)
	if err != nil {
		t.Fatal(err)
	}
	// JSONB normalises key order, so compare the decoded value.
	var rows []map[string]any
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatalf("decode %s: %v", got, err)
	}
	if len(rows) != 1 || rows[0]["item"] != "minecraft:stone" || rows[0]["slot"] != float64(1) || rows[0]["count"] != float64(2) {
		t.Fatalf("after upsert: %s", got)
	}
}
