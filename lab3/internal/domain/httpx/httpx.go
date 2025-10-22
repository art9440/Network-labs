package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"time"

	"github.com/art9440/Network-labs/lab3/internal/shared/result"
)

const userAgent = "NetworkLabs-Geosearch/1.0 (+https://github.com/art9440/Network-labs; contact: art9440@gmail.com)"

func HttpWork[Raw any, Out any](
	ctx context.Context,
	url string,
	conv func(Raw) Out, ch chan result.Result[Out],
) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", userAgent) // <- ОБЯЗАТЕЛЬНО для Wikipedia
	req.Header.Set("Api-User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		ch <- result.Result[Out]{Err: fmt.Errorf("http: %w", err)}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		ch <- result.Result[Out]{Err: fmt.Errorf("http %d: %s", resp.StatusCode, b)}
		return
	}

	var raw Raw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		ch <- result.Result[Out]{Err: fmt.Errorf("decode: %w", err)}
		return
	}

	out := conv(raw)

	ch <- result.Result[Out]{Val: out}

}
