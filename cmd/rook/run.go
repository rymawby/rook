package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/loopengine"
	"github.com/rymawby/rook/internal/tui"
)

func runRun(args []string, configPath string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	headless := fs.Bool("headless", false, "print iteration/task events as log lines instead of opening the TUI")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if configPath == "" {
		configPath = "rook.json"
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("%s not found; run `rook init` first", configPath)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	engine, err := loopengine.New(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		hardStops := 0
		for range sigCh {
			hardStops++
			if hardStops == 1 {
				fmt.Fprintln(os.Stderr, "\nrook: finishing current iteration, then stopping (press again to stop immediately)")
				engine.RequestStop()
			} else {
				fmt.Fprintln(os.Stderr, "\nrook: stopping immediately")
				cancel()
				return
			}
		}
	}()
	defer signal.Stop(sigCh)

	if *headless {
		return runHeadless(ctx, engine)
	}
	return tui.Run(ctx, engine, cfg)
}

func runHeadless(ctx context.Context, engine *loopengine.Engine) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range engine.Events() {
			printEvent(ev)
		}
	}()

	reason, err := engine.Run(ctx)
	<-done
	fmt.Printf("rook: loop stopped (%s)\n", reason)
	return err
}

func printEvent(ev loopengine.Event) {
	switch ev.Type {
	case loopengine.EventIterationStart:
		fmt.Printf("[iter %d] starting\n", ev.IterationN)
	case loopengine.EventAssessDone:
		pct := 0.0
		if ev.Assessment != nil {
			pct = ev.Assessment.CompletionEstimate * 100
		}
		fmt.Printf("[iter %d] assessed: %.0f%% complete\n", ev.IterationN, pct)
	case loopengine.EventDispatchStart:
		fmt.Printf("[iter %d] dispatching tasks\n", ev.IterationN)
	case loopengine.EventTaskUpdate:
		if ev.Task != nil {
			fmt.Printf("[iter %d] task %s: %s\n", ev.IterationN, ev.Task.ID, ev.Task.Status)
		}
	case loopengine.EventAcceptanceDone:
		fmt.Printf("[iter %d] acceptance criteria checked\n", ev.IterationN)
	case loopengine.EventIterationDone:
		if ev.Verdict != nil {
			fmt.Printf("[iter %d] done: satisfied=%v acceptancePassing=%v\n", ev.IterationN, ev.Verdict.Satisfied, ev.Verdict.AcceptancePassing)
		}
	case loopengine.EventStopped:
		fmt.Printf("stopped: %s\n", ev.StopReason)
	case loopengine.EventError:
		fmt.Printf("[iter %d] error: %v\n", ev.IterationN, ev.Err)
	}
}
