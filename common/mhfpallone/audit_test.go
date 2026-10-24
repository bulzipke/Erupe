package mhfpallone

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAuditExactQuestIDsAndSupportedVariants(t *testing.T) {
	binPath := t.TempDir()
	quests := filepath.Join(binPath, "quests")
	if err := os.Mkdir(quests, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"27438d0.bin", "27438n2.json", "27479d0.json", "27348d0.bin.backup", "27365n3.bin"} {
		if err := os.WriteFile(filepath.Join(quests, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(quests, "27365d0.bin"), 0700); err != nil {
		t.Fatal(err)
	}
	routes := []Route{{Index: 0, StageQuestIDs: []uint16{27438, 27479, 27348, 27365}},
		{Index: 1, StageQuestIDs: []uint16{27438}}}
	got, err := AuditAssets(routes, binPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.RouteCount != 2 || got.QuestCount != 4 || !reflect.DeepEqual(got.MissingQuestIDs, []uint16{27348, 27365}) {
		t.Fatalf("incorrect audit totals: %+v", got)
	}
	want := []QuestFiles{{QuestID: 27348, Files: []string{}}, {QuestID: 27365, Files: []string{}},
		{QuestID: 27438, Files: []string{"27438d0.bin", "27438n2.json"}},
		{QuestID: 27479, Files: []string{"27479d0.json"}}}
	if !reflect.DeepEqual(got.Quests, want) {
		t.Fatalf("quest files = %+v, want %+v", got.Quests, want)
	}
	if !reflect.DeepEqual(got.Routes, routes) {
		t.Fatal("changed route stage order")
	}
}

func TestAuditReportsMissingQuestDirectory(t *testing.T) {
	if _, err := AuditAssets(nil, t.TempDir()); err == nil {
		t.Fatal("missing quest directory was treated as an empty installation")
	}
}
