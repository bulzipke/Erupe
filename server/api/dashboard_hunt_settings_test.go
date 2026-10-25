package api

import (
	"encoding/json"
	"testing"
)

func TestMonsterTimeRankEntryJSONClientSettings(t *testing.T) {
	entry := MonsterTimeRankEntry{Frames: 8051}
	// Exercise the real JSONB Scanner, including legacy SQL NULL.
	if err := entry.ClientSettings.Scan(nil); err != nil {
		t.Fatal(err)
	}
	if err := entry.ClientSettings.Scan([]byte(`{"version":1,"hpView":true,"elapsedMode":2,"fov":60,"fovUsed":true}`)); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Frames   int `json:"frames"`
		Settings struct {
			Mode int `json:"elapsedMode"`
			FOV  int `json:"fov"`
		} `json:"clientSettings"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.Frames != 8051 || response.Settings.Mode != 2 || response.Settings.FOV != 60 {
		t.Fatalf("response %s", data)
	}
}
