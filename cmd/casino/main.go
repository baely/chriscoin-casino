package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	gh "chriscoin-casino/internal/github"
	"chriscoin-casino/internal/ledger"
	"chriscoin-casino/internal/live"
	"chriscoin-casino/internal/processor"
	"chriscoin-casino/internal/site"
	"chriscoin-casino/internal/web"
)

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: casino serve|validate|state|process|site [--config casino.json] [--ledger ledger/events.jsonl] [--base-ledger PATH] [--out _site] [--commit SHA]")
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
	case "serve":
		return serve()
	case "healthcheck":
		// For the container healthcheck: the image has no shell or curl.
		res, err := http.Get("http://127.0.0.1" + or(env("CASINO_ADDR"), ":8080") + "/healthz")
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("healthz: %s", res.Status)
		}
		return nil
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
		fmt.Printf("Validated %d events. Player liabilities: %d CC. House balance: %d CC. Payable to withdrawals: %d CC.\n", len(events), s.Liabilities(), s.HouseBalance(), s.Payable())
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
		p.Closed = os.Getenv("CASINO_CLOSED") == "true"
		if treasuryToken := os.Getenv("CHRISCOIN_TOKEN"); treasuryToken != "" {
			p.Payer = gh.New(cfg.UpstreamRepository, treasuryToken)
		}
		return p.Run(ctx)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

// env returns NAME, or the trimmed contents of the file named by NAME_FILE.
func env(name string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	if p := strings.TrimSpace(os.Getenv(name + "_FILE")); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// serve runs the live casino. Bailey Bank holds every balance and signs
// players in; see internal/web.
func serve() error {
	prod := env("CASINO_ENV") == "production"
	addr := or(env("CASINO_ADDR"), map[bool]string{true: ":8080", false: "127.0.0.1:8081"}[prod])
	bank := &web.Bank{URL: env("BANK_URL"), Token: env("BANK_APP_TOKEN")}
	public := strings.TrimRight(env("BANK_PUBLIC_URL"), "/")
	if bank.URL == "" || bank.Token == "" || public == "" {
		return fmt.Errorf("configuration: BANK_URL, BANK_PUBLIC_URL and BANK_APP_TOKEN are required")
	}
	base := env("CASINO_BASE_URL")
	if prod && (!strings.HasPrefix(base, "https://") || !strings.HasPrefix(public, "https://")) {
		return fmt.Errorf("configuration: production requires https CASINO_BASE_URL and BANK_PUBLIC_URL")
	}
	var log *slog.Logger
	if prod {
		log = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	} else {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("listening", "addr", addr, "bank", bank.URL, "sign-in", public+"/auth/app")
	// Publish settled bets to the repository's public live ledger.
	if token := env("CASINO_LEDGER_TOKEN"); token != "" {
		repo := or(env("CASINO_LEDGER_REPO"), "baely/chriscoin-casino")
		pub := &live.Publisher{Bank: bank, Repo: gh.New(repo, token), Interval: time.Minute, Log: log}
		go pub.Run(ctx)
		log.Info("publishing the live ledger", "repo", repo, "path", live.Path)
	} else {
		log.Warn("CASINO_LEDGER_TOKEN is not set: the live ledger is not published")
	}
	srv := web.New(web.Config{BankURL: public, Secure: strings.HasPrefix(base, "https://"), Bank: bank, Log: log})
	return srv.Serve(ctx, addr, 10*time.Second)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
