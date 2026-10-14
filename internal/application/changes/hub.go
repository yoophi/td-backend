package changes

import (
	"context"
	"sync"
	"time"

	"github.com/yoophi/td-backend/internal/domain"
)

type Notice struct {
	Token string
	Err   error
}
type Subscription struct {
	Events <-chan Notice
	Cancel func()
}
type HubOptions struct {
	PollEvery      time.Duration
	MaxSubscribers int
}
type group struct {
	ctx         context.Context
	cancel      context.CancelFunc
	last        string
	subscribers map[uint64]chan Notice
}
type Hub struct {
	Service *Service
	ctx     context.Context
	cancel  context.CancelFunc
	options HubOptions
	mu      sync.Mutex
	groups  map[string]*group
	closed  bool
	next    uint64
	count   int
	wg      sync.WaitGroup
}

func NewHub(ctx context.Context, service *Service, opts HubOptions) *Hub {
	if opts.PollEvery <= 0 {
		opts.PollEvery = 250 * time.Millisecond
	}
	if opts.MaxSubscribers <= 0 {
		opts.MaxSubscribers = 128
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Hub{Service: service, ctx: ctx, cancel: cancel, options: opts, groups: map[string]*group{}}
}
func (h *Hub) Subscribe(ctx context.Context, c domain.Caller) (Subscription, Snapshot, error) {
	initial, e := h.Service.Current(ctx, c)
	if e != nil {
		return Subscription{}, initial, e
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.ctx.Err() != nil || h.count >= h.options.MaxSubscribers {
		return Subscription{}, initial, domain.ErrUnavailable
	}
	g := h.groups[c.ProjectID]
	if g == nil {
		ctx, cancel := context.WithCancel(h.ctx)
		g = &group{ctx: ctx, cancel: cancel, last: initial.Token, subscribers: map[uint64]chan Notice{}}
		h.groups[c.ProjectID] = g
		h.wg.Add(1)
		go h.poll(c.ProjectID, g)
	}
	h.next++
	id := h.next
	ch := make(chan Notice, 1)
	g.subscribers[id] = ch
	h.count++
	var once sync.Once
	sub := Subscription{Events: ch, Cancel: func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, ok := g.subscribers[id]; !ok {
				return
			}
			delete(g.subscribers, id)
			h.count--
			select {
			case <-ch:
			default:
			}
			close(ch)
			if len(g.subscribers) == 0 {
				if h.groups[c.ProjectID] == g {
					delete(h.groups, c.ProjectID)
				}
				g.cancel()
			}
		})
	}}
	return sub, initial, nil
}
func (h *Hub) poll(project string, g *group) {
	defer h.wg.Done()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.groups[project] == g {
			delete(h.groups, project)
		}
		for id, ch := range g.subscribers {
			select {
			case <-ch:
			default:
			}
			close(ch)
			delete(g.subscribers, id)
			h.count--
		}
	}()
	ticker := time.NewTicker(h.options.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-g.ctx.Done():
			return
		case <-ticker.C:
			latest, e := h.Service.committed(g.ctx, project)
			h.mu.Lock()
			if e != nil || latest.Token != g.last {
				g.last = latest.Token
				for _, ch := range g.subscribers {
					// Capacity one replaces an obsolete invalidation for a slow consumer.
					select {
					case <-ch:
					default:
					}
					ch <- Notice{Token: latest.Token, Err: e}
				}
			}
			h.mu.Unlock()
			if e != nil {
				return
			}
		}
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	if !h.closed {
		h.closed = true
		h.cancel()
	}
	h.mu.Unlock()
	h.wg.Wait()
}
