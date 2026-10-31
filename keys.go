package torn

import (
	"context"
	"errors"
	"sort"
	"time"
)

// RateWindow is Torn's rolling rate-limit window (PARITY_SPEC P3.1).
const RateWindow = 60 * time.Second

// KeyStatus is the health of one configured key (P2.3).
type KeyStatus string

// Key states.
const (
	KeyHealthy     KeyStatus = "healthy"
	KeyRateLimited KeyStatus = "rate_limited"
	KeyDisabled    KeyStatus = "disabled"
)

// KeyState is a redacted snapshot of one configured key (P2.5). It never
// contains the key itself.
type KeyState struct {
	// Key is "***" plus at most the last four key characters.
	Key string `json:"key"`
	// Index is the key's position in Config.APIKeys after de-duplication.
	Index int `json:"index"`
	// Group is the shared rate-limit budget the key belongs to (P3.2).
	Group  int       `json:"group"`
	Status KeyStatus `json:"status"`
	// Until is when a rate-limited or temporarily disabled key recovers; zero
	// when the state does not expire.
	Until time.Time `json:"until,omitempty"`
	// Reason is the Torn error that disabled the key, e.g. "incorrect_key(2)".
	Reason string `json:"reason,omitempty"`
	// Remaining is the request budget left in the group's sliding window.
	Remaining int `json:"remaining"`
	// Access is the key's known access level, or "" when unknown.
	Access AccessLevel `json:"access,omitempty"`
}

// window is a sliding log of send times (P3.1).
type window struct{ sends []time.Time }

func (w *window) prune(now time.Time) {
	i := 0
	for i < len(w.sends) && !w.sends[i].Add(RateWindow).After(now) {
		i++
	}
	if i > 0 {
		w.sends = append(w.sends[:0], w.sends[i:]...)
	}
}

// freeAt is the earliest time another send fits the budget.
func (w *window) freeAt(now time.Time, limit int) time.Time {
	w.prune(now)
	if len(w.sends) < limit {
		return now
	}
	return w.sends[len(w.sends)-limit].Add(RateWindow)
}

func (w *window) record(t time.Time) {
	i := sort.Search(len(w.sends), func(i int) bool { return w.sends[i].After(t) })
	w.sends = append(w.sends, time.Time{})
	copy(w.sends[i+1:], w.sends[i:])
	w.sends[i] = t
}

type keyEntry struct {
	key    string
	group  int
	status KeyStatus
	until  time.Time
	reason string
	access AccessLevel
	owner  int64
}

type routeEntry struct {
	name         string
	window       window
	blockedUntil time.Time
}

// keyPool holds key and route state shared by every goroutine using a Client
// (P3.5). All fields are guarded by Client.mu.
type keyPool struct {
	keys    []keyEntry
	groups  []window
	routes  []routeEntry
	nextKey int
}

func (p *keyPool) allKeys() []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p.keys))
	for i, k := range p.keys {
		out[i] = k.key
	}
	return out
}

func (p *keyPool) indexOf(key string) int {
	for i, k := range p.keys {
		if k.key == key {
			return i
		}
	}
	return -1
}

// refresh expires timed states.
func (k *keyEntry) refresh(now time.Time) {
	if k.status != KeyHealthy && !k.until.IsZero() && !k.until.After(now) {
		k.status, k.until, k.reason = KeyHealthy, time.Time{}, ""
	}
}

// KeyStates returns a redacted snapshot of every configured key (P2.5).
func (c *Client) KeyStates() []KeyState {
	now := c.cfg.Scheduler.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]KeyState, len(c.pool.keys))
	for i := range c.pool.keys {
		k := &c.pool.keys[i]
		k.refresh(now)
		g := &c.pool.groups[k.group]
		g.prune(now)
		remaining := c.cfg.KeyPerMinute - len(g.sends)
		if remaining < 0 {
			remaining = 0
		}
		out[i] = KeyState{Key: redactKey(k.key), Index: i, Group: k.group, Status: k.status, Until: k.until, Reason: k.reason, Remaining: remaining, Access: k.access}
	}
	return out
}

// lease is one reserved send: a key (or a request-pinned key) and a route.
type lease struct {
	key    int // pool index, or -1 for a pinned key outside the pool
	secret string
	route  int
}

type acquireRequest struct {
	route    string
	excluded map[int]bool
	required AccessLevel
	op       string
	pinned   string
	last     error
}

var errRouteUnavailable = errors.New("torn: request route unavailable")

// acquire reserves a concurrency slot, a key and a route, waiting for the
// sliding windows when needed (P3.1, P3.3). Rate-limited keys are skipped
// while another key has budget; it sleeps only when no key or route is ready.
func (c *Client) acquire(ctx context.Context, req acquireRequest) (lease, error) {
	for {
		if e := ctx.Err(); e != nil {
			return lease{}, e
		}
		select {
		case c.sem <- struct{}{}:
		case <-ctx.Done():
			return lease{}, ctx.Err()
		}
		now := c.cfg.Scheduler.Now()
		c.mu.Lock()
		l, wakeAt, err := c.pick(now, req)
		c.mu.Unlock()
		if err == nil && wakeAt.IsZero() {
			if c.cfg.Observer != nil {
				c.cfg.Observer(RouteUsage{c.pool.routes[l.route].name, l.key, now})
			}
			return l, nil
		}
		<-c.sem
		if err != nil {
			return lease{}, err
		}
		if e := c.cfg.Scheduler.Sleep(ctx, wakeAt.Sub(now)); e != nil {
			return lease{}, e
		}
	}
}

// pick chooses a ready key and route under c.mu. It returns a non-zero wakeAt
// when the caller must sleep.
func (c *Client) pick(now time.Time, req acquireRequest) (lease, time.Time, error) {
	p := &c.pool
	routeReady := time.Time{}
	ri := -1
	known := req.route == ""
	for i := range p.routes {
		r := &p.routes[i]
		if req.route != "" && r.name != req.route {
			continue
		}
		known = true
		at := r.window.freeAt(now, c.cfg.RoutePerMinute)
		if r.blockedUntil.After(at) {
			at = r.blockedUntil
		}
		if !at.After(now) {
			ri = i
			break
		}
		if routeReady.IsZero() || at.Before(routeReady) {
			routeReady = at
		}
	}
	if !known {
		return lease{}, time.Time{}, validationf("unknown request route %q", req.route)
	}
	if req.pinned != "" {
		if ri < 0 {
			return lease{}, routeReady, nil
		}
		idx := p.indexOf(req.pinned)
		if idx >= 0 {
			p.groups[p.keys[idx].group].record(now)
		}
		p.routes[ri].window.record(now)
		return lease{key: idx, secret: req.pinned, route: ri}, time.Time{}, nil
	}
	keyReady := time.Time{}
	ki := -1
	candidates := 0
	var have []AccessLevel
	denied := 0
	for n := range p.keys {
		i := (p.nextKey + n) % len(p.keys)
		k := &p.keys[i]
		k.refresh(now)
		if k.status == KeyDisabled || req.excluded[i] {
			continue
		}
		if c.cfg.CheckAccess && req.required != "" && k.access != "" && !k.access.Satisfies(req.required) {
			denied++
			have = append(have, k.access)
			continue
		}
		candidates++
		at := p.groups[k.group].freeAt(now, c.cfg.KeyPerMinute)
		if k.status == KeyRateLimited && k.until.After(at) {
			at = k.until
		}
		if !at.After(now) {
			ki = i
			break
		}
		if keyReady.IsZero() || at.Before(keyReady) {
			keyReady = at
		}
	}
	if candidates == 0 {
		if denied > 0 && req.last == nil {
			return lease{}, time.Time{}, &AccessError{Operation: req.op, Required: req.required, Have: have}
		}
		return lease{}, time.Time{}, &NoUsableKeysError{Last: req.last}
	}
	if ki >= 0 && ri >= 0 {
		k := &p.keys[ki]
		p.groups[k.group].record(now)
		p.routes[ri].window.record(now)
		p.nextKey = (ki + 1) % len(p.keys)
		return lease{key: ki, secret: k.key, route: ri}, time.Time{}, nil
	}
	wake := keyReady
	if ki >= 0 {
		wake = routeReady
	} else if ri < 0 && routeReady.After(wake) {
		wake = routeReady
	}
	return lease{}, wake, nil
}

// markKey applies the key policy of a Torn error (P2.4, P3.3).
func (c *Client) markKey(l lease, code ErrorCode) {
	if l.key < 0 {
		return
	}
	now := c.cfg.Scheduler.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	k := &c.pool.keys[l.key]
	switch {
	case code.Action() == ActionWaitRateWindow:
		g := &c.pool.groups[k.group]
		g.prune(now)
		until := now.Add(RateWindow)
		if len(g.sends) > 0 {
			until = g.sends[0].Add(RateWindow)
		}
		for i := range c.pool.keys {
			other := &c.pool.keys[i]
			if other.group == k.group && other.status != KeyDisabled {
				other.status, other.until, other.reason = KeyRateLimited, until, code.String()
			}
		}
	case code.DisablesKey() == "client_lifetime":
		k.status, k.until, k.reason = KeyDisabled, time.Time{}, code.String()
	case code.DisablesKey() == "until_utc_midnight":
		u := now.UTC()
		k.status, k.until, k.reason = KeyDisabled, time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC), code.String()
	}
}

// blockRoute marks a route blocked after Torn code 8 (P3.4) and reports
// whether another route is ready for an immediate retry.
func (c *Client) blockRoute(l lease, pinnedRoute string) bool {
	now := c.cfg.Scheduler.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pool.routes[l.route].blockedUntil = now.Add(RateWindow)
	if pinnedRoute != "" {
		return false
	}
	for i := range c.pool.routes {
		r := &c.pool.routes[i]
		if i != l.route && !r.blockedUntil.After(now) {
			return true
		}
	}
	return false
}

// recordUsage adds a send to a key's window; tests use it to preload budgets.
func (c *Client) recordUsage(key int, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pool.groups[c.pool.keys[key].group].record(at)
}
