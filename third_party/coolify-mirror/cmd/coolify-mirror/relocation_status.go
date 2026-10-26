package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/mtalavi/coolify-mirror/internal/engine"
	"os"
	"time"
)

func cmdRelocationStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("relocation-status", flag.ContinueOnError)
	file := fs.String("receipt", "", "")
	phase := fs.String("phase", "", "")
	drainFile := fs.String("drain-record", "", "")
	intentPath := fs.String("intent", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("receipt is required")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	if *phase == "aborted" {
		unlock, err := engine.Lock()
		if err != nil {
			return err
		}
		defer unlock()
		var a, b engine.RelocationIntent
		if json.Unmarshal(raw, &a) != nil {
			return fmt.Errorf("invalid abort receipt")
		}
		stored, err := os.ReadFile(*intentPath)
		if err != nil {
			return err
		}
		if json.Unmarshal(stored, &b) != nil || a != b || a.State != "aborted" || !a.NoDestinationEffects {
			return fmt.Errorf("destination abort no longer matches its permanent journal")
		}
		return writeJSON("", map[string]any{"verified": true, "observed_at": time.Now().UTC()})
	}
	if *phase == "resumed" {
		var v struct {
			TransferID string `json:"transfer_id"`
			PlanJSON   string `json:"plan_json"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return fmt.Errorf("invalid resumed source receipt")
		}
		if _, err := engine.VerifyRelocationAborted(*drainFile+".resumed", v.TransferID, []byte(v.PlanJSON)); err != nil {
			return err
		}
	}
	in, err := detect(ctx)
	if err != nil {
		return err
	}
	var drain *engine.DrainRecord
	if *phase == "sent" || *phase == "prepared" || *phase == "resumed" {
		drain, err = readDrainRecord(*drainFile)
		if err != nil && !(*phase == "resumed" && errors.Is(err, os.ErrNotExist)) {
			return err
		}
	}
	if err := engine.ObserveRelocation(ctx, in, raw, *phase, drain); err != nil {
		return err
	}
	return writeJSON("", map[string]any{"verified": true, "observed_at": time.Now().UTC()})
}
