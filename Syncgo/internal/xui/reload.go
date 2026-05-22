package xui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Reloader debounces XUI panel reloads triggered by inserts.
// Without this, importing 100 movies would shell out 100 reloads.
type Reloader struct {
	cfg     ReloadConfig
	mu      sync.Mutex
	timer   *time.Timer
	pending bool
}

type ReloadConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Command  string // default: "sudo service xuione reload"
	Debounce time.Duration
}

func NewReloader(cfg ReloadConfig) *Reloader {
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.Command == "" {
		cfg.Command = "sudo service xuione reload"
	}
	if cfg.Debounce == 0 {
		cfg.Debounce = 30 * time.Second
	}
	return &Reloader{cfg: cfg}
}

// Trigger schedules a reload after the debounce window.
// Multiple Trigger() calls within the window collapse to one reload.
func (r *Reloader) Trigger() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = true
	if r.timer == nil {
		r.timer = time.AfterFunc(r.cfg.Debounce, r.fire)
	} else {
		r.timer.Reset(r.cfg.Debounce)
	}
}

func (r *Reloader) fire() {
	r.mu.Lock()
	if !r.pending {
		r.mu.Unlock()
		return
	}
	r.pending = false
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = r.runOnce(ctx)
}

func (r *Reloader) runOnce(ctx context.Context) error {
	cfg := &ssh.ClientConfig{
		User: r.cfg.User,
		Auth: []ssh.AuthMethod{ssh.Password(r.cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", r.cfg.Host, r.cfg.Port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(r.cfg.Command)
		done <- result{out, err}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-done:
		return res.err
	}
}
