package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/kinoforge/runnel-dashboard/internal/auth"
	"github.com/kinoforge/runnel-dashboard/internal/config"
	"github.com/kinoforge/runnel-dashboard/internal/web"
)

var version = "dev"

const usage = `runnel-dashboard serve [-config path]   serve the dashboard
runnel-dashboard hash                    read a password, print its bcrypt hash
runnel-dashboard version                 print the version`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "hash":
		err = hash()
	case "version":
		fmt.Println("runnel-dashboard", version)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "runnel-dashboard:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", envOr("RUNNEL_DASHBOARD_CONFIG", "/etc/runnel-dashboard/dashboard.yaml"), "config file")
	_ = fs.Parse(args)
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if !cfg.Secure() {
		log.Warn("secure_cookies is off: use this only on localhost over plain http")
	}
	h, err := web.New(cfg, log, nil)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout, ReadTimeout: cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout, IdleTimeout: cfg.HTTP.IdleTimeout, MaxHeaderBytes: 64 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "instances", len(cfg.Instances), "version", version)
		if cfg.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func hash() error {
	var pw string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Again: ")
		again, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if string(b) != string(again) {
			return errors.New("the passwords differ")
		}
		pw = string(b)
	} else {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	h, err := auth.Hash(pw)
	if err != nil {
		return err
	}
	fmt.Println(h)
	return nil
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
