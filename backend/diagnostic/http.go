package diagnostic

import (
	"context"
	"io"
	"net/http"
	"time"
)

type HTTPChecker struct {
	Client *http.Client
}

func (HTTPChecker) Kind() Kind { return KindHTTP }

func (c HTTPChecker) Check(ctx context.Context, target Target) Result {
	started := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.Address, nil)
	if err != nil {
		result := baseResult(KindHTTP, target.Address, started, err)
		result.ErrorCode = "invalid_url"
		return result
	}
	req.Header.Set("User-Agent", "CheckNetwork/1.0")
	client := c.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		}}
	}
	resp, err := client.Do(req)
	result := baseResult(KindHTTP, target.Address, started, err)
	if err != nil {
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 32*1024))
	expected := target.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	result.Details = map[string]any{
		"status_code":     resp.StatusCode,
		"expected_status": expected,
		"protocol":        resp.Proto,
		"content_type":    resp.Header.Get("Content-Type"),
	}
	if resp.StatusCode != expected {
		result.Status = StatusUnreachable
		result.ErrorCode = "unexpected_status"
		result.Message = "service returned an unexpected HTTP status"
	}
	return result
}
