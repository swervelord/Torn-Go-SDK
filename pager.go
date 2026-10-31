package torn

import (
	"context"
	"encoding/json"
	"iter"
	"net/url"
	"strings"
)

// PageOptions controls a Pager (PARITY_SPEC P7.1).
type PageOptions struct {
	// MaxPages stops after this many pages; 0 means no limit.
	MaxPages int
	// MaxItems stops item iteration (Items, RawItems) after this many items; 0 means no limit.
	MaxItems int
	// Request applies to every page request.
	Request RequestOptions
}

// Page is one fetched page.
type Page[T any] struct {
	// Value is the decoded page.
	Value T
	// Raw is the exact response body.
	Raw json.RawMessage
	// Number counts pages fetched by the pager, starting at 1.
	Number int
	// Next and Prev are the validated _metadata.links ("" when absent). They
	// never contain a key.
	Next, Prev string
	Report     ExecutionReport
}

// Pager follows _metadata.links page by page (P7.1). Every request goes
// through the client's executor (rate limits, retries, key failover, hooks)
// and authenticates with the Authorization header; keys in link URLs are
// dropped and links must point at the configured API host. A Pager is not
// safe for concurrent use.
type Pager[T any] struct {
	c       *Client
	op      Operation
	params  any
	schema  string
	opts    PageOptions
	started bool
	next    string
	prev    string
	pages   int
}

func newOperationPager[T any](c *Client, op Operation, params any, schema string, opts []PageOptions) *Pager[T] {
	p := &Pager[T]{c: c, op: op, params: params, schema: schema}
	if len(opts) > 0 {
		p.opts = opts[0]
	}
	return p
}

// Paginate returns a Pager for any operation whose responses carry
// _metadata.links; T is the page model (json.RawMessage for raw pages).
func Paginate[T any](c *Client, op Operation, params any, opts PageOptions) *Pager[T] {
	return &Pager[T]{c: c, op: op, params: params, opts: opts}
}

// Pages returns a raw Pager for a v2 path such as "/user/attacks" (P7.1, P8.2).
func (c *Client) Pages(path string, params map[string]any, opts PageOptions) *Pager[json.RawMessage] {
	return Paginate[json.RawMessage](c, Operation{ID: "raw " + path, Path: path, Method: "GET"}, params, opts)
}

// Pages returns the number of pages fetched so far.
func (p *Pager[T]) Pages() int { return p.pages }

// Next fetches the next page. ok is false once the last page was returned
// (no next link) or MaxPages is reached.
func (p *Pager[T]) Next(ctx context.Context) (page Page[T], ok bool, err error) {
	if !p.started {
		return p.fetch(ctx, "")
	}
	if p.next == "" {
		return page, false, nil
	}
	return p.fetch(ctx, p.next)
}

// Prev fetches the page before the most recently fetched one by following
// its prev link. ok is false when there is no prev link.
func (p *Pager[T]) Prev(ctx context.Context) (page Page[T], ok bool, err error) {
	if !p.started || p.prev == "" {
		return page, false, nil
	}
	return p.fetch(ctx, p.prev)
}

// All iterates pages forward until the last page, MaxPages, an error, or the
// loop breaks. The error, if any, is yielded once as the final element.
func (p *Pager[T]) All(ctx context.Context) iter.Seq2[Page[T], error] {
	return func(yield func(Page[T], error) bool) {
		for {
			page, ok, err := p.Next(ctx)
			if err != nil {
				yield(page, err)
				return
			}
			if !ok || !yield(page, nil) {
				return
			}
		}
	}
}

func (p *Pager[T]) fetch(ctx context.Context, link string) (page Page[T], ok bool, err error) {
	if p.c == nil {
		return page, false, validationf("pager has no client")
	}
	if p.opts.MaxPages > 0 && p.pages >= p.opts.MaxPages {
		return page, false, nil
	}
	op, params := p.op, p.params
	if link != "" {
		path, query, err := p.c.linkTarget(link)
		if err != nil {
			return page, false, err
		}
		op.Path, params = path, query
	}
	meta := requestMeta{}
	if m, ok := operationMetadata[p.op.ID]; ok {
		meta.access = m.AccessLevel
	}
	raw, report, err := p.c.doOperation(ctx, op, params, p.opts.Request, "v2", nil, false, meta)
	if err != nil {
		return page, false, err
	}
	var links struct {
		Metadata struct {
			Links struct {
				Next, Prev *string
			} `json:"links"`
		} `json:"_metadata"`
	}
	_ = json.Unmarshal(raw, &links)
	next, prev := "", ""
	if l := links.Metadata.Links.Next; l != nil && *l != "" {
		if _, _, err := p.c.linkTarget(*l); err != nil {
			return page, false, err
		}
		next = stripKey(*l)
	}
	if l := links.Metadata.Links.Prev; l != nil && *l != "" {
		if _, _, err := p.c.linkTarget(*l); err != nil {
			return page, false, err
		}
		prev = stripKey(*l)
	}
	if err = p.c.decodeResponse(p.op.ID, p.schema, raw, &page.Value, p.opts.Request); err != nil {
		return page, false, err
	}
	p.started = true
	p.pages++
	p.next, p.prev = next, prev
	page.Raw, page.Number, page.Next, page.Prev, page.Report = raw, p.pages, next, prev, report
	return page, true, nil
}

// linkTarget validates a pagination link against the configured v2 base URL
// and returns the path relative to it and the query without any key.
func (c *Client) linkTarget(link string) (string, map[string]any, error) {
	u, err := url.Parse(link)
	base, berr := url.Parse(c.cfg.BaseURL)
	if err != nil || berr != nil || !u.IsAbs() || u.User != nil || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return "", nil, validationf("pagination link does not point at the configured API host")
	}
	prefix := strings.TrimRight(base.Path, "/")
	if !strings.HasPrefix(u.Path, prefix+"/") {
		return "", nil, validationf("pagination link is outside the API base path")
	}
	query := map[string]any{}
	for k, v := range u.Query() {
		if k != "key" {
			query[k] = strings.Join(v, ",")
		}
	}
	return strings.TrimPrefix(u.Path, prefix), query, nil
}

// stripKey removes a key query parameter from a link before exposing it.
func stripKey(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	q := u.Query()
	if !q.Has("key") {
		return link
	}
	q.Del("key")
	u.RawQuery = q.Encode()
	return u.String()
}

// Items iterates the items of every page, using extract to select each page's
// list (P7.1). It honors PageOptions.MaxItems; an error ends the iteration
// after being yielded once.
func Items[T, I any](ctx context.Context, p *Pager[T], extract func(T) []I) iter.Seq2[I, error] {
	return func(yield func(I, error) bool) {
		n := 0
		for page, err := range p.All(ctx) {
			if err != nil {
				var zero I
				yield(zero, err)
				return
			}
			for _, item := range extract(page.Value) {
				if p.opts.MaxItems > 0 && n >= p.opts.MaxItems {
					return
				}
				n++
				if !yield(item, nil) {
					return
				}
			}
		}
	}
}

// RawItems iterates the elements of each raw page's single top-level array
// (for example "attacks"), ignoring _metadata.
func RawItems(ctx context.Context, p *Pager[json.RawMessage]) iter.Seq2[json.RawMessage, error] {
	return Items(ctx, p, func(page json.RawMessage) []json.RawMessage {
		var fields map[string]json.RawMessage
		if json.Unmarshal(page, &fields) != nil {
			return nil
		}
		for k, v := range fields {
			var items []json.RawMessage
			if k != "_metadata" && json.Unmarshal(v, &items) == nil {
				return items
			}
		}
		return nil
	})
}
