package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	"coldflow/internal/client"
	"coldflow/internal/config"
)

func ServeSSH(ctx context.Context, c config.Config, cl *client.Client) error {
	if c.WishListen == "" {
		return nil
	}
	if c.AuthorizedKeys == "" {
		return errors.New("Wish requires authorized_keys")
	}
	if _, err := os.Stat(c.AuthorizedKeys); err != nil {
		return err
	}
	var sessions sync.WaitGroup
	slots := make(chan struct{}, 2)
	middleware := func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			default:
				wish.Fatalln(s, "Two dashboard sessions are already open.")
				return
			}
			sessions.Add(1)
			defer sessions.Done()
			pty, windows, ok := s.Pty()
			if !ok {
				wish.Fatalln(s, "Request a terminal using ssh -t.")
				return
			}
			program := tea.NewProgram(New(cl, c.ReducedMotion), tea.WithContext(s.Context()), tea.WithInput(s), tea.WithOutput(s), tea.WithEnvironment(s.Environ()), tea.WithWindowSize(pty.Window.Width, pty.Window.Height), tea.WithFPS(20), tea.WithoutSignalHandler())
			done := make(chan struct{})
			go func() {
				for {
					select {
					case win, open := <-windows:
						if !open {
							return
						}
						program.Send(tea.WindowSizeMsg{Width: win.Width, Height: win.Height})
					case <-done:
						return
					case <-s.Context().Done():
						return
					}
				}
			}()
			_, err := program.Run()
			close(done)
			if err != nil {
				wish.Errorln(s, "Dashboard session ended.")
			}
			next(s)
		}
	}
	srv, err := wish.NewServer(wish.WithAddress(c.WishListen), wish.WithHostKeyPath(filepath.Join(c.DataDir, "ssh_host_ed25519")), wish.WithAuthorizedKeys(c.AuthorizedKeys), wish.WithPasswordAuth(func(ssh.Context, string) bool { return false }), wish.WithIdleTimeout(30*time.Minute), wish.WithMiddleware(middleware))
	if err != nil {
		return err
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			child, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(child)
		case <-stopped:
		}
	}()
	err = srv.ListenAndServe()
	close(stopped)
	if ctx.Err() != nil {
		return nil
	}
	return err
}
