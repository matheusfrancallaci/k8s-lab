package tutor

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var sharedLLMHTTPClient = &http.Client{Transport: &http.Transport{
	Proxy:        http.ProxyFromEnvironment,
	DialContext:  (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	MaxIdleConns: 32, MaxIdleConnsPerHost: 16, IdleConnTimeout: 90 * time.Second,
	TLSHandshakeTimeout: 5 * time.Second, ExpectContinueTimeout: time.Second,
}}

var llmSlots struct {
	sync.Mutex
	capacity int
	ch       chan struct{}
}

func llmConcurrency() int {
	n, _ := strconv.Atoi(envOr("OLLAMA_MAX_CONCURRENCY", "1"))
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	return n
}

func acquireLLMSlot(ctx context.Context) (func(), error) {
	capacity := llmConcurrency()
	llmSlots.Lock()
	if llmSlots.ch == nil || llmSlots.capacity != capacity {
		llmSlots.capacity = capacity
		llmSlots.ch = make(chan struct{}, capacity)
	}
	ch := llmSlots.ch
	llmSlots.Unlock()
	started := time.Now()
	select {
	case ch <- struct{}{}:
		recordTutorLatency("llm.queue", time.Since(started), 0, false)
		return func() { <-ch }, nil
	case <-ctx.Done():
		recordTutorLatency("llm.queue", time.Since(started), 0, true)
		return nil, ctx.Err()
	}
}

func llmRequest(ctx context.Context, method, url, contentType string, body *bytes.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return sharedLLMHTTPClient.Do(req)
}
