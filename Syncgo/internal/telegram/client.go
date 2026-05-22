package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type Client struct {
	Index    int
	Token    string
	TG       *telegram.Client
	API      *tg.Client
	Self     *tg.User
	Workload atomic.Int64
	DC       int          // Telegram DC this client is connected to (1-5)
	sem      chan struct{} // limits concurrent streams
	waiter   *floodwait.Waiter
}

// Acquire tries to reserve a stream slot. Returns false if at capacity.
func (c *Client) Acquire() bool {
	select {
	case c.sem <- struct{}{}:
		c.Workload.Add(1)
		return true
	default:
		return false
	}
}

// Release frees a stream slot.
func (c *Client) Release() {
	<-c.sem
	c.Workload.Add(-1)
}

type Pool struct {
	Main    *Client
	Workers []*Client
	mu      sync.RWMutex
	all     []*Client
	opts    Options // kept for AddClient
}

type Options struct {
	APIID             int
	APIHash           string
	BotToken          string
	ExtraTokens       []string
	SessionDir        string
	MaxStreamsPerToken int
}

func NewClient(index int, token string, opts Options) (*Client, error) {
	storage := &session.FileStorage{
		Path: filepath.Join(opts.SessionDir, fmt.Sprintf("bot_%d.json", index)),
	}

	waiter := floodwait.NewWaiter().WithMaxRetries(5)

	tgOpts := telegram.Options{
		SessionStorage: storage,
		Middlewares: []telegram.Middleware{
			waiter,
		},
	}

	maxStreams := opts.MaxStreamsPerToken
	if maxStreams <= 0 {
		maxStreams = 8
	}

	c := &Client{
		Index:  index,
		Token:  token,
		waiter: waiter,
		sem:    make(chan struct{}, maxStreams),
	}
	c.TG = telegram.NewClient(opts.APIID, opts.APIHash, tgOpts)
	return c, nil
}

func (c *Client) Run(ctx context.Context, ready func(context.Context, *Client) error) error {
	return c.waiter.Run(ctx, func(ctx context.Context) error {
		return c.runClient(ctx, ready)
	})
}

func (c *Client) runClient(ctx context.Context, ready func(context.Context, *Client) error) error {
	return c.TG.Run(ctx, func(ctx context.Context) error {
		status, err := c.TG.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status: %w", err)
		}
		if !status.Authorized {
			if _, err := c.TG.Auth().Bot(ctx, c.Token); err != nil {
				return fmt.Errorf("auth bot: %w", err)
			}
		}
		c.API = c.TG.API()
		me, err := c.TG.Self(ctx)
		if err != nil {
			return fmt.Errorf("self: %w", err)
		}
		c.Self = me

		// Detecta o DC ao qual este cliente está conectado (timeout de 5s).
		dcCtx, dcCancel := context.WithTimeout(ctx, 5*time.Second)
		if dc, err := c.API.HelpGetNearestDC(dcCtx); err == nil {
			c.DC = dc.ThisDC
		} else {
			// Falha não é crítica; roteamento por DC fica desabilitado para este cliente.
		}
		dcCancel()

		if ready != nil {
			if err := ready(ctx, c); err != nil {
				return err
			}
		}
		<-ctx.Done()
		return ctx.Err()
	})
}

func NewPool(ctx context.Context, opts Options) (*Pool, error) {
	pool := &Pool{opts: opts}
	main, err := NewClient(0, opts.BotToken, opts)
	if err != nil {
		return nil, err
	}
	pool.Main = main
	pool.all = append(pool.all, main)

	for i, t := range opts.ExtraTokens {
		w, err := NewClient(i+1, t, opts)
		if err != nil {
			return nil, fmt.Errorf("worker %d: %w", i+1, err)
		}
		pool.Workers = append(pool.Workers, w)
		pool.all = append(pool.all, w)
	}

	return pool, nil
}

// AddClient creates a new Client for the given token and starts it in the background.
// The client is immediately appended to the pool and becomes eligible for stream routing
// once its Run callback fires. Safe to call concurrently.
func (p *Pool) AddClient(ctx context.Context, token string, logger *slog.Logger) (*Client, error) {
	p.mu.Lock()
	index := len(p.all)
	p.mu.Unlock()

	c, err := NewClient(index, token, p.opts)
	if err != nil {
		return nil, fmt.Errorf("new client for token index %d: %w", index, err)
	}

	p.mu.Lock()
	p.Workers = append(p.Workers, c)
	p.all = append(p.all, c)
	p.mu.Unlock()

	go func() {
		if err := c.Run(ctx, func(ctx context.Context, cl *Client) error {
			if logger != nil {
				logger.Info("dynamic token client ready",
					"index", cl.Index, "bot", cl.Self.Username, "dc", cl.DC)
			}
			return nil
		}); err != nil && ctx.Err() == nil && logger != nil {
			logger.Error("dynamic token client exited", "index", c.Index, "err", err)
		}
	}()

	return c, nil
}

func (p *Pool) All() []*Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Client, len(p.all))
	copy(out, p.all)
	return out
}

// Pick retorna o cliente com menor carga (sem considerar semáforo).
func (p *Pool) Pick() *Client {
	all := p.All()
	var best *Client
	var bestLoad int64
	for _, c := range all {
		if c.API == nil {
			continue
		}
		l := c.Workload.Load()
		if best == nil || l < bestLoad {
			best = c
			bestLoad = l
		}
	}
	return best
}

// PickForStream seleciona o melhor cliente para streaming:
// 1. Prefere clientes no mesmo DC do arquivo
// 2. Prefere clientes com slot de semáforo disponível
// 3. Se nenhum tiver slot, pega o menos carregado (graceful degradation)
func (p *Pool) PickForStream(fileDC int) *Client {
	all := p.All()
	var bestDC, bestAny *Client
	var bestDCLoad, bestAnyLoad int64 = -1, -1

	for _, c := range all {
		if c.API == nil {
			continue
		}
		load := c.Workload.Load()
		hasCap := int64(len(c.sem)) < int64(cap(c.sem))

		if hasCap && (fileDC == 0 || c.DC == fileDC) {
			if bestDC == nil || load < bestDCLoad {
				bestDC = c
				bestDCLoad = load
			}
		}
		if hasCap {
			if bestAny == nil || load < bestAnyLoad {
				bestAny = c
				bestAnyLoad = load
			}
		}
	}

	if bestDC != nil {
		return bestDC
	}
	if bestAny != nil {
		return bestAny
	}
	return p.Pick()
}

func (p *Pool) ReadyCount() int {
	all := p.All()
	n := 0
	for _, c := range all {
		if c.API != nil {
			n++
		}
	}
	return n
}

func (c *Client) IsBot() bool { return c.Self != nil && c.Self.Bot }
