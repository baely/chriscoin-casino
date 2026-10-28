package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
	"chriscoin-casino/internal/processor"
	"chriscoin-casino/internal/site"
)

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: casino validate|state|process|site [--config casino.json] [--ledger ledger/events.jsonl] [--base-ledger PATH] [--out _site] [--commit SHA]")
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	configPath := flags.String("config", "casino.json", "configuration file")
	ledgerPath := flags.String("ledger", "ledger/events.jsonl", "local ledger")
	basePath := flags.String("base-ledger", "", "previous ledger; require append-only prefix")
	outDir := flags.String("out", "_site", "static site output directory")
	commit := flags.String("commit", "", "ledger commit shown on the static site")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	switch os.Args[1] {
	case "validate", "state":
		data, err := os.ReadFile(*ledgerPath)
		if err != nil {
			return err
		}
		events, err := ledger.Parse(data)
		if err != nil {
			return err
		}
		s, err := ledger.Replay(events)
		if err != nil {
			return err
		}
		if *basePath != "" {
			old, err := os.ReadFile(*basePath)
			if err != nil {
				return err
			}
			before, err := ledger.Parse(old)
			if err != nil {
				return err
			}
			if len(before) > len(events) || ledger.Hash(ledger.Serialize(before)) != ledger.Hash(ledger.Serialize(events[:len(before)])) {
				return fmt.Errorf("ledger history must be append-only")
			}
		}
		if os.Args[1] == "state" {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		}
		fmt.Printf("Validated %d events. Player liabilities: %d CC. House reserve after exposure: %d CC.\n", len(events), s.Liabilities(), s.FreeHouse())
		return nil
	case "site":
		data, err := os.ReadFile(*ledgerPath)
		if err != nil {
			return err
		}
		if err := site.Build(data, *outDir, *commit); err != nil {
			return err
		}
		fmt.Printf("Wrote %s/index.html\n", *outDir)
		return nil
	case "process":
		if *ledgerPath != "ledger/events.jsonl" || *basePath != "" {
			return fmt.Errorf("process always operates on canonical ledger/events.jsonl through GitHub")
		}
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			return err
		}
		var cfg ledger.Config
		if err := ledger.Decode(raw, &cfg); err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		repo, token := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_TOKEN")
		if repo == "" || token == "" {
			return fmt.Errorf("process requires GITHUB_REPOSITORY and GITHUB_TOKEN")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		p := processor.Processor{Repository: repo, Config: cfg, Store: gh.New(repo, token), Upstream: gh.New(cfg.UpstreamRepository, token)}
		if treasuryToken := os.Getenv("CHRISCOIN_TOKEN"); treasuryToken != "" {
			p.Payer = gh.New(cfg.UpstreamRepository, treasuryToken)
		}
		return p.Run(ctx)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
