package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"github.com/mtalavi/coolify-mirror/internal/engine"
	"os"
)

func cmdRelocationAbort(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("relocation-abort", flag.ContinueOnError)
	receipt := fs.String("receipt", "", "")
	planPath := fs.String("plan", "", "")
	transfer := fs.String("transfer-id", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	plan, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	unlock, err := engine.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	v, err := engine.RecordRelocationIntent(engine.RelocationIntentPath(*receipt), *transfer, plan, "aborted")
	if err != nil {
		return err
	}
	return writeJSON("", v)
}

func cmdRelocationResume(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("relocation-resume", flag.ContinueOnError)
	record := fs.String("record", "", "")
	planPath := fs.String("plan", "", "")
	transfer := fs.String("transfer-id", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	plan, err := os.ReadFile(*planPath)
	if err != nil {
		return err
	}
	unlock, err := engine.Lock()
	if err != nil {
		return err
	}
	_, err = engine.RecordRelocationIntent(*record+".resumed", *transfer, plan, "aborted")
	unlock()
	if err != nil {
		return err
	}
	rec, err := readDrainRecord(*record)
	if errors.Is(err, os.ErrNotExist) {
		return writeJSON("", map[string]any{"source_resumed": true})
	}
	if err != nil {
		return err
	}
	if rec.TransferID != *transfer || rec.PlanSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(plan)) {
		return errors.New("source drain differs from approved resume")
	}
	in, err := detect(ctx)
	if err != nil {
		return err
	}
	pr := engine.NewProgress("Resume source")
	stop := printProgress(pr)
	results, err := engine.Undrain(ctx, in, rec, pr)
	stop()
	if err != nil {
		return err
	}
	for _, r := range results {
		if !r.OK || r.Stopped || r.Resource.Hold != "" {
			return errors.New("source resume remains unverified")
		}
	}
	return writeJSON("", map[string]any{"source_resumed": true})
}
