package observer

import (
	"context"
	"net/http"
	"time"
)

type ProbeResult struct {
	SentAt     time.Time
	Latency    time.Duration
	StatusCode int
	Err        error
}

type HTTPProbe struct {
	URL      string
	Interval time.Duration
	Timeout  time.Duration
	client   *http.Client
}

func NewHTTPProbe(url string, interval, timeout time.Duration) *HTTPProbe {
	tr := &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
	}
	return &HTTPProbe{
		URL:      url,
		Interval: interval,
		Timeout:  timeout,
		client:   &http.Client{Transport: tr, Timeout: timeout},
	}
}

// Start begins probing in a goroutine and emits results on the returned channel.
// The channel is closed when ctx is cancelled.
func (p *HTTPProbe) Start(ctx context.Context) <-chan ProbeResult {
	out := make(chan ProbeResult, 256)
	go func() {
		defer close(out)
		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r := p.probeOnce(ctx)
				select {
				case out <- r:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func (p *HTTPProbe) probeOnce(ctx context.Context) ProbeResult {
	sentAt := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return ProbeResult{SentAt: sentAt, Err: err}
	}
	resp, err := p.client.Do(req)
	latency := time.Since(sentAt)
	if err != nil {
		return ProbeResult{SentAt: sentAt, Latency: latency, Err: err}
	}
	defer resp.Body.Close()
	// drain body (small) so connection can be reused
	var buf [512]byte
	for {
		_, rerr := resp.Body.Read(buf[:])
		if rerr != nil {
			break
		}
	}
	return ProbeResult{
		SentAt:     sentAt,
		Latency:    time.Since(sentAt),
		StatusCode: resp.StatusCode,
	}
}
