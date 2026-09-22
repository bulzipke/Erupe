package channelserver

import (
	"fmt"
	"math"
)

// divaMainMapProgress is a calculation only: it does not grant rewards, persist
// area awards, schedule an update, or create the next map. A future repository
// must bind contributions to this map and apply each contribution only once.
type divaMainMapProgress struct {
	Map           DivaInterceptionMap
	NewAreas      uint32
	Discarded     uint64
	GoalCompleted bool
}

// advanceDivaMainMap consumes an already validated main-route contribution at
// the unfinished frontier. Area thresholds are supplied by the map catalog;
// this helper never invents a points-to-area conversion. Start is not an area.
// Official map rules discard surplus at the goal instead of advancing the next
// map. Branch points must not be passed here. No runtime handler calls this yet.
func advanceDivaMainMap(m DivaInterceptionMap, points uint64) (divaMainMapProgress, error) {
	if err := validateDivaInterceptionMap(m); err != nil {
		return divaMainMapProgress{}, err
	}
	// Clone every slice, including definitions and the previous map, so callers
	// cannot accidentally mutate a stored snapshot through this result.
	result := divaMainMapProgress{Map: m}
	result.Map.Treasures = append([]DivaInterceptionMapTreasure(nil), m.Treasures...)
	result.Map.Definitions = append([]DivaInterceptionMapDefinition(nil), m.Definitions...)
	for i := range result.Map.Definitions {
		result.Map.Definitions[i].Nodes = append([]DivaInterceptionMapNodeDefinition(nil), m.Definitions[i].Nodes...)
	}
	result.Map.States = append([]DivaInterceptionMapState(nil), m.States...)
	for i := range result.Map.States {
		result.Map.States[i].Nodes = append([]DivaInterceptionMapNodeState(nil), m.States[i].Nodes...)
	}
	current := &result.Map.States[0]
	var definition DivaInterceptionMapDefinition
	for _, d := range result.Map.Definitions {
		if d.ID == current.TemplateID {
			definition = d
			break
		}
	}
	goalIndex := divaInterceptionMapGoalIndex(*current, definition)
	for i := 1; i <= goalIndex && points > 0; i++ {
		node := &current.Nodes[i]
		needed := uint64(node.RequiredPoints - node.EarnedPoints)
		if needed == 0 {
			continue
		}
		used := min(points, needed)
		node.EarnedPoints += uint32(used)
		points -= used
		if node.EarnedPoints == node.RequiredPoints {
			result.NewAreas++
		}
	}
	if uint64(m.AcquiredAreas)+uint64(result.NewAreas) > math.MaxInt32 {
		return divaMainMapProgress{}, fmt.Errorf("diva map: acquired area total exceeds native signed range")
	}
	result.Map.AcquiredAreas += result.NewAreas
	result.Discarded = points
	goal := current.Nodes[goalIndex]
	result.GoalCompleted = goal.EarnedPoints == goal.RequiredPoints
	return result, nil
}
