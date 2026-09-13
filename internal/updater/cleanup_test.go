package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestClearPreviousExecutablesOnlyCompletedMatchingJobs(t *testing.T) {
	for _, scenario := range []string{"completed", "pending", "other-app"} {
		t.Run(scenario, func(t *testing.T) {
			j, dir := makeJob(t)
			raw, _ := json.Marshal(j)
			if err := os.WriteFile(filepath.Join(dir, "job.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(dir, "previous.exe")
			if err := os.WriteFile(backup, []byte("backup"), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario != "pending" {
				raw, _ = json.Marshal(result{Target: j.Target, Success: true})
				if err := os.WriteFile(filepath.Join(dir, "result.json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			target := j.Target
			if scenario == "other-app" {
				target += "-other"
			}
			if err := clearPreviousExecutables(target, []string{filepath.Dir(j.Target)}); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(backup)
			if scenario == "completed" && !os.IsNotExist(err) {
				t.Fatal("backup retained", err)
			}
			if scenario != "completed" && err != nil {
				t.Fatal("unrelated or pending backup removed", err)
			}
			if _, err := os.Stat(j.Target); err != nil {
				t.Fatal("application removed", err)
			}
		})
	}
}
