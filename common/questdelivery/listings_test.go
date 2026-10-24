package questdelivery

import (
	"reflect"
	"testing"
)

func TestMissingListingQuestIDsIncludesClosedAndFutureSchedules(t *testing.T) {
	c, err := Parse([]byte(`{"version":"inventory","profiles":[{"id":"p","worlds":["Free"]}],"schedules":[{"id":"always"},{"id":"future","starts_at":"2030-01-01T00:00:00Z","ends_at":"2030-01-02T00:00:00Z"}],"quests":[{"quest_id":30,"profiles":["p"],"schedule":"future"},{"quest_id":20,"profiles":["p"],"schedule":"always"},{"quest_id":10,"disabled":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	input := []int{99, 99, 20}
	if got := c.MissingListingQuestIDs(input); !reflect.DeepEqual(got, []int{30}) {
		t.Fatalf("future publication lost its required source: %v", got)
	}
	if !reflect.DeepEqual(input, []int{99, 99, 20}) {
		t.Fatal("inventory was mutated")
	}
	missing := c.MissingListingQuestIDs(nil)
	if !reflect.DeepEqual(missing, []int{20, 30}) {
		t.Fatal("missing IDs are not sorted or include disabled IDs", missing)
	}
	missing[0] = 999
	if !reflect.DeepEqual(c.MissingListingQuestIDs(nil), []int{20, 30}) {
		t.Fatal("returned IDs alias the catalog")
	}
	if got := c.MissingListingQuestIDs([]int{30, 20}); len(got) != 0 {
		t.Fatal("complete inventory was refused", got)
	}
}
