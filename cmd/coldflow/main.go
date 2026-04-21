package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
	_ "time/tzdata"

	tea "charm.land/bubbletea/v2"
	"charm.land/log/v2"
	"coldflow/internal/client"
	"coldflow/internal/config"
	"coldflow/internal/engine"
	"coldflow/internal/server"
	"coldflow/internal/store"
	"coldflow/internal/tui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "coldflow:", err)
		os.Exit(1)
	}
}
func run() error {
	root, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "coldflow")
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "--data" {
			if i+1 >= len(args) {
				return errors.New("--data requires a directory")
			}
			dir = args[i+1]
			args = append(args[:i], args[i+2:]...)
			i--
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	cmd := "tui"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}
	if cmd == "version" {
		fmt.Println("Coldflow", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if cmd == "help" || cmd == "--help" || cmd == "-h" {
		fmt.Print(helpText)
		return nil
	}
	if cmd == "init" {
		flags := flag.NewFlagSet("init", flag.ContinueOnError)
		demo := flags.Bool("demo", false, "isolated simulated workflows")
		headless := flags.Bool("non-interactive", false, "write defaults without forms")
		if err = flags.Parse(args); err != nil {
			return err
		}
		return initialize(dir, *demo, *headless)
	}
	if cmd == "restore" {
		if len(args) != 1 {
			return errors.New("usage: coldflow --data EMPTY_DIRECTORY restore BACKUP.zip")
		}
		return restore(dir, args[0])
	}
	if cmd == "demo" {
		dir = filepath.Join(dir, "demo")
		if _, err = os.Stat(config.Path(dir)); os.IsNotExist(err) {
			if err = initialize(dir, true, true); err != nil {
				return err
			}
		}
	}
	c, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("read configuration: %w; run coldflow --data %q init", err, dir)
	}
	switch cmd {
	case "configure":
		return configure(c)
	case "auth":
		if len(args) != 1 || args[0] != "gmail" {
			return errors.New("usage: coldflow auth gmail")
		}
		return authGmail(c)
	case "backup":
		if len(args) != 1 {
			return errors.New("usage: coldflow backup BACKUP.zip")
		}
		return backup(c, args[0])
	case "doctor":
		return doctor(c)
	case "inbox-resync":
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		return cl.Action(server.Action{Kind: "inbox-resync"})
	case "serve", "demo":
		return serve(c, cmd == "demo")
	case "tui":
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(tui.New(cl, c.ReducedMotion), tea.WithFPS(20)).Run()
		return err
	case "status":
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		s, err := cl.Snapshot()
		if err != nil {
			return err
		}
		fmt.Printf("Contacts: %d | Drafts: %d | Jobs: %d | Paused: %t | Demo: %t\n", len(s.Contacts), len(s.Drafts), len(s.Jobs), s.Paused, s.Demo)
		return nil
	case "discord-register":
		return registerDiscord(c)
	default:
		return fmt.Errorf("unknown command %q; see coldflow help", cmd)
	}
}
func serve(c config.Config, withTUI bool) error {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(350 << 20)
	}
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(1)
	}
	s, err := store.Open(c.DataDir)
	if err != nil {
		return err
	}
	defer s.Close()
	e := engine.New(c, s)
	if c.Demo {
		if err = e.SeedDemo(); err != nil {
			return err
		}
	}
	// Avoid interleaving service logs with the TUI; retained history lives in SQLite.
	if withTUI {
		f, err := os.OpenFile(filepath.Join(c.DataDir, "demo.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		log.SetOutput(f)
	}
	srv, err := server.New(e)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Run(ctx) }()
	if c.WishListen != "" {
		cl, err := client.New(c)
		if err != nil {
			return err
		}
		go func() {
			if err := tui.ServeSSH(ctx, c, cl); err != nil {
				log.Error("SSH dashboard stopped", "error", err)
			}
		}()
	}
	if !withTUI {
		return <-result
	}
	// Wait for a real health check rather than assuming the listener is ready.
	cl := &http.Client{Timeout: time.Second}
	ready := false
	for i := 0; i < 30; i++ {
		select {
		case err := <-result:
			return err
		default:
		}
		host := strings.Replace(c.Listen, "0.0.0.0:", "127.0.0.1:", 1)
		resp, err := cl.Get("http://" + host + "/healthz")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		cancel()
		<-result
		return errors.New("demo service did not become ready")
	}
	local, err := client.New(c)
	if err != nil {
		cancel()
		<-result
		return err
	}
	_, err = tea.NewProgram(tui.New(local, c.ReducedMotion), tea.WithContext(ctx), tea.WithFPS(20)).Run()
	cancel()
	serviceErr := <-result
	if err != nil {
		return err
	}
	return serviceErr
}

const helpText = `Coldflow - persistent academic outreach agents

  coldflow init [--non-interactive] [--demo]  Create profile and preferences
  coldflow configure                        Configure integrations and AI
  coldflow auth gmail                       Connect Gmail using OAuth
  coldflow serve                            Run agents continuously
  coldflow tui                              Attach ASCII dashboard (default)
  coldflow demo                             Start isolated demo + dashboard
  coldflow status                           Print service counts
  coldflow doctor                           Check configuration and tools
  coldflow inbox-resync                     Recover an expired inbox cursor
  coldflow discord-register                Register /coldflow command
  coldflow backup ARCHIVE.zip               Back up database and credentials
  coldflow restore ARCHIVE.zip              Restore into an empty directory
  coldflow version

Use --data DIRECTORY with any command. Store credentials in environment variables.
See README.md and docs/SETUP.md for account setup and VPS installation.
`
