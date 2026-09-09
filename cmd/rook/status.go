package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/loopengine"
	"github.com/rymawby/rook/internal/store"
)

func runStatus(args []string, configPath string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if configPath == "" {
		configPath = "rook.json"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	st := store.New(cfg.AbsTargetDir())

	n, ok, err := st.LatestIteration()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("no iterations recorded yet")
		return nil
	}

	completed := store.Exists(st.VerdictPath(n))
	fmt.Printf("iteration: %d (%s)\n", n, statusWord(completed))

	var plan loopengine.PlanOutput
	if store.Exists(st.TasksPath(n)) {
		if err := store.LoadJSON(st.TasksPath(n), &plan); err == nil {
			fmt.Printf("completion estimate: %.0f%%\n", plan.Assessment.CompletionEstimate*100)
			fmt.Printf("tasks planned: %d\n", len(plan.Tasks))
		}
	}

	results, err := st.LoadResults(n)
	if err == nil && len(results) > 0 {
		counts := map[string]int{}
		for _, raw := range results {
			var r struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(raw, &r); err == nil {
				counts[r.Status]++
			}
		}
		fmt.Printf("task results: done=%d failed=%d\n", counts["done"], counts["failed"])
	}

	if completed {
		var v loopengine.Verdict
		if err := store.LoadJSON(st.VerdictPath(n), &v); err == nil {
			fmt.Printf("verdict: satisfied=%v acceptancePassing=%v\n", v.Satisfied, v.AcceptancePassing)
			fmt.Printf("summary: %s\n", v.Summary)
		}
	} else {
		fmt.Println("this iteration has not finished (crashed or still running); `rook run` will resume it")
	}
	return nil
}

func statusWord(completed bool) string {
	if completed {
		return "completed"
	}
	return "incomplete"
}
