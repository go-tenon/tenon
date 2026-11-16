package vector

import "testing"

func TestCheckModelsWhileReindexing(t *testing.T) {
	t.Parallel()
	old, current := modelID{"old", 8}, modelID{"new", 4}
	configured := map[modelID]Model{old: {}, current: {}}
	if err := checkModels("chunks", current, configured, []modelID{old, current}, true); err != nil {
		t.Errorf("reindexing from the previous model: %v", err)
	}
	if err := checkModels("chunks", current, configured, []modelID{old}, true); err != nil {
		t.Errorf("reindex registered, new collection not created yet: %v", err)
	}
}
