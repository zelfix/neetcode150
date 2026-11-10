package tutor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"neetcode150/internal/studydb"
)

// DefaultDeepSeekModel is the model used unless one is named.
const DefaultDeepSeekModel = "deepseek-pro"

// defaultDeepSeekBase is the API root. Overridden in tests by a stand-in
// server, which is what keeps them from costing money.
const defaultDeepSeekBase = "https://api.deepseek.com"

// deepseekPrices is the price per million tokens.
//
// Three input prices rather than one, because a cached prefix costs about a
// thirtieth of a fresh one — which is the whole reason the prompt is split the
// way it is. The published rates halve outside peak hours, so the cost of a
// turn depends on when it happened.
type deepseekPrices struct {
	CacheHit  float64 `json:"cache_hit"`
	CacheMiss float64 `json:"cache_miss"`
	Output    float64 `json:"output"`
}

// DeepSeekRates is the price list per model. It is JSON so an operator can
// replace it with `nc serve --tutor-rates <file>` when the vendor changes the
// numbers, which happens more often than this code is rebuilt.
type DeepSeekRates map[string]struct {
	Peak    deepseekPrices `json:"peak"`
	OffPeak deepseekPrices `json:"off_peak"`
}

// defaultDeepSeekRates are the published prices as of 2026-11-09. A model
// not listed here is priced as the dearest one, see pricesFor.
var defaultDeepSeekRates = DeepSeekRates{
	"deepseek-pro": {
		Peak:    deepseekPrices{CacheHit: 0.044, CacheMiss: 1.32, Output: 3.96},
		OffPeak: deepseekPrices{CacheHit: 0.022, CacheMiss: 0.66, Output: 1.98},
	},
	"deepseek-flash": {
		Peak:    deepseekPrices{CacheHit: 0.006, CacheMiss: 0.30, Output: 1.20},
		OffPeak: deepseekPrices{CacheHit: 0.003, CacheMiss: 0.15, Output: 0.60},
	},
}

// LoadDeepSeekRates reads a price list in the shape of defaultDeepSeekRates:
//
//	{"deepseek-pro": {"peak": {"cache_hit": 0.044, "cache_miss": 1.32, "output": 3.96},
//	                  "off_peak": {"cache_hit": 0.022, "cache_miss": 0.66, "output": 1.98}}}
//
// Every number has to be present and non-negative; a zero would make a turn
// free and quietly defeat the allowance.
func LoadDeepSeekRates(path string) (DeepSeekRates, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r DeepSeekRates
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(r) == 0 {
		return nil, fmt.Errorf("%s: no models listed", path)
	}
	for model, m := range r {
		for _, p := range []deepseekPrices{m.Peak, m.OffPeak} {
			if p.CacheHit <= 0 || p.CacheMiss <= 0 || p.Output <= 0 {
				return nil, fmt.Errorf("%s: %s needs positive cache_hit, cache_miss and output prices", path, model)
			}
		}
	}
	return r, nil
}

// dearest is the model whose peak output price is highest, which is what an
// unknown model is charged as.
func (r DeepSeekRates) dearest() string {
	best, bestPrice := "", -1.0
	for model, m := range r {
		if m.Peak.Output > bestPrice || (m.Peak.Output == bestPrice && model < best) {
			best, bestPrice = model, m.Peak.Output
		}
	}
	return best
}

// peakHours are 01:00–04:00 and 06:00–10:00 UTC, Monday to Friday. Outside
// them everything is half price.
func isPeak(t time.Time) bool {
	u := t.UTC()
	if u.Weekday() == time.Saturday || u.Weekday() == time.Sunday {
		return false
	}
	h := u.Hour()
	return (h >= 1 && h < 4) || (h >= 6 && h < 10)
}

// pricesFor falls back to the dearest model so an unknown name over-estimates
// rather than under-charges somebody's allowance.
func pricesFor(rates DeepSeekRates, model string, at time.Time) deepseekPrices {
	if rates == nil {
		rates = defaultDeepSeekRates
	}
	r, ok := rates[model]
	if !ok {
		r = rates[rates.dearest()]
	}
	if isPeak(at) {
		return r.Peak
	}
	return r.OffPeak
}

// deepseekUsage is what the API reports for one request. Unlike the Claude
// CLI's running total, this is the cost of this request alone, so it converts
// straight into the increment the budget wants.
type deepseekUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptCacheHit      int `json:"prompt_cache_hit_tokens"`
	PromptCacheMiss     int `json:"prompt_cache_miss_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// cost converts a usage report into dollars.
func (u deepseekUsage) cost(rates DeepSeekRates, model string, at time.Time) float64 {
	p := pricesFor(rates, model, at)
	hit, miss := u.PromptCacheHit, u.PromptCacheMiss
	if hit == 0 && miss == 0 {
		// Older or OpenAI-shaped responses report the split differently, and
		// some report no split at all. Treating the unknown part as a miss
		// over-estimates, which is the safe direction for a ceiling.
		hit = u.PromptTokensDetails.CachedTokens
		miss = u.PromptTokens - hit
		if miss < 0 {
			miss = 0
		}
	}
	const perMillion = 1_000_000.0
	return float64(hit)*p.CacheHit/perMillion +
		float64(miss)*p.CacheMiss/perMillion +
		float64(u.CompletionTokens)*p.Output/perMillion
}

// deepseekClient drives one conversation over HTTP.
//
// No tools and no file access: everything it knows arrives in the messages the
// server builds. That is what lets the panel be offered to guests at all — a
// CLI provider rooted at the repository could read every person's work.
type deepseekClient struct {
	session *Session
	http    *http.Client
	key     string
	model   string
	base    string
	system  string
	rates   DeepSeekRates

	// retryWait is how long to wait before attempt n (1-based) after a
	// transient failure; tests shorten it.
	retryWait func(attempt int, retryAfter time.Duration) time.Duration

	// context is what the turn appends to each message: the person's files as
	// they stand. Rebuilt per turn by the manager.
	turnContext func() string

	mu      sync.Mutex
	history []deepseekMessage
	cancel  context.CancelFunc
}

type deepseekMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// startDeepSeek prepares the conversation. Nothing is sent until a turn is.
func (m *Manager) startDeepSeek(scope *studydb.Scope, s *Session) error {
	c := &deepseekClient{
		session: s,
		http:    &http.Client{Timeout: 10 * time.Minute},
		key:     m.deepseekKey,
		model:   m.model,
		base:    m.deepseekBase,
		rates:   m.deepseekRates,
		system:  m.systemPrompt(scope, s.Problem),
		retryWait: func(attempt int, retryAfter time.Duration) time.Duration {
			if retryAfter > 0 {
				return min(retryAfter, 10*time.Second)
			}
			return time.Duration(attempt) * 500 * time.Millisecond
		},
		turnContext: func() string {
			return m.turnContext(scope, s.Problem)
		},
	}
	if c.model == "" {
		c.model = DefaultDeepSeekModel
	}
	if c.base == "" {
		c.base = defaultDeepSeekBase
	}
	// A restored conversation keeps going rather than starting over, so the
	// person is not re-asked what they already answered.
	c.history = s.replayMessages()

	s.mu.Lock()
	s.deep = c
	s.running = true
	s.model = c.model
	s.tools = nil
	s.publishLocked(Event{Kind: KindReady, Model: c.model, Mode: string(ModeTutor), Tools: nil})
	s.mu.Unlock()
	return nil
}

// replayMessages rebuilds the conversation from the stored transcript.
func (s *Session) replayMessages() []deepseekMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []deepseekMessage
	for _, e := range s.events {
		switch e.Kind {
		case KindUser, KindInternalUser:
			if strings.TrimSpace(e.Text) != "" {
				out = append(out, deepseekMessage{Role: "user", Content: e.Text})
			}
		case KindText, KindInternalText:
			if strings.TrimSpace(e.Text) != "" {
				out = append(out, deepseekMessage{Role: "assistant", Content: e.Text})
			}
		}
	}
	return out
}

func (c *deepseekClient) sendAs(text, display string) error {
	c.session.mu.Lock()
	if !c.session.running {
		c.session.mu.Unlock()
		return fmt.Errorf("the tutor session for %s is not running", c.session.Slug)
	}
	c.session.streamed.Reset()
	c.session.publishLocked(Event{Kind: KindUser, Text: display})
	c.session.mu.Unlock()

	go c.turn(text)
	return nil
}

// turn sends one request and streams the reply back as events.
func (c *deepseekClient) turn(text string) {
	// The files as they stand ride with the message rather than the system
	// prompt, so a changed solution does not invalidate the cached prefix.
	prompt := text
	if extra := c.turnContext(); extra != "" {
		prompt += extra
	}

	c.mu.Lock()
	c.history = append(c.history, deepseekMessage{Role: "user", Content: prompt})
	msgs := append([]deepseekMessage{{Role: "system", Content: c.system}}, c.history...)
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.mu.Unlock()
	defer cancel()

	reply, usage, err := c.stream(ctx, msgs)
	if err != nil {
		if ctx.Err() != nil {
			c.session.publish(Event{Kind: KindError, Text: "the turn was interrupted"})
			return
		}
		c.session.publish(Event{Kind: KindError, Text: err.Error()})
		return
	}

	c.mu.Lock()
	c.history = append(c.history, deepseekMessage{Role: "assistant", Content: reply})
	c.mu.Unlock()

	cost := usage.cost(c.rates, c.model, time.Now())
	c.session.mu.Lock()
	c.session.cost += cost
	total := c.session.cost
	c.session.streamed.Reset()
	c.session.mu.Unlock()
	if cost > 0 {
		// Already an increment, unlike the CLI's running total.
		c.session.recordSpend(cost)
	}
	c.session.publish(Event{Kind: KindDone, Cost: total})
}

type deepseekRequest struct {
	Model         string            `json:"model"`
	Messages      []deepseekMessage `json:"messages"`
	Stream        bool              `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

type deepseekChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *deepseekUsage `json:"usage"`
}

// deepseekAttempts is how many times a turn is tried before the failure is
// shown. Only the request is retried: once text has started streaming, a cut
// stream is reported, since part of the reply is already on the screen.
const deepseekAttempts = 3

// stream posts the request and publishes text as it arrives.
func (c *deepseekClient) stream(ctx context.Context, msgs []deepseekMessage) (string, deepseekUsage, error) {
	var usage deepseekUsage
	body := deepseekRequest{Model: c.model, Messages: msgs, Stream: true}
	body.StreamOptions.IncludeUsage = true
	raw, err := json.Marshal(body)
	if err != nil {
		return "", usage, err
	}

	var resp *http.Response
	for attempt := 1; ; attempt++ {
		var retryAfter time.Duration
		resp, retryAfter, err = c.post(ctx, raw)
		if err == nil || attempt == deepseekAttempts || ctx.Err() != nil {
			break
		}
		var transient *transientError
		if !errors.As(err, &transient) {
			break
		}
		select {
		case <-time.After(c.retryWait(attempt, retryAfter)):
		case <-ctx.Done():
			return "", usage, ctx.Err()
		}
	}
	if err != nil {
		return "", usage, err
	}
	defer resp.Body.Close()

	var reply strings.Builder
	sc := bufio.NewScanner(resp.Body)
	// A single reply can exceed the scanner's default line budget, and a
	// truncated SSE line is a JSON parse error rather than a short answer.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk deepseekChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // a keep-alive or a shape we do not model
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			reply.WriteString(ch.Delta.Content)
			c.session.mu.Lock()
			c.session.streamed.WriteString(ch.Delta.Content)
			c.session.publishLocked(Event{Kind: KindText, Text: ch.Delta.Content})
			c.session.mu.Unlock()
		}
	}
	if err := sc.Err(); err != nil {
		return reply.String(), usage, fmt.Errorf("the reply was cut off: %w", err)
	}
	if strings.TrimSpace(reply.String()) == "" {
		return "", usage, fmt.Errorf("the tutor returned an empty reply")
	}
	return reply.String(), usage, nil
}

// transientError marks a failure worth another try: the service was busy or
// unreachable, rather than refusing the request.
type transientError struct{ err error }

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// post sends one request and returns the response only when it can be read as
// a stream. retryAfter is what the service asked for, when it said.
func (c *deepseekClient) post(ctx context.Context, raw []byte) (*http.Response, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.base, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, &transientError{fmt.Errorf("the tutor could not be reached: %w", err)}
	}
	if resp.StatusCode == http.StatusOK {
		return resp, 0, nil
	}
	snippet, _ := bufio.NewReader(resp.Body).Peek(400)
	resp.Body.Close()
	err = fmt.Errorf("the tutor service answered %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		var wait time.Duration
		if secs, convErr := strconv.Atoi(resp.Header.Get("Retry-After")); convErr == nil && secs > 0 {
			wait = time.Duration(secs) * time.Second
		}
		return nil, wait, &transientError{err}
	}
	return nil, 0, err
}

// interrupt stops the turn in progress.
func (c *deepseekClient) interrupt() error {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel == nil {
		return fmt.Errorf("nothing to interrupt")
	}
	cancel()
	return nil
}
