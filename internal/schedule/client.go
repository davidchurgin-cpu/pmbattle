package schedule

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/davidchurgin-cpu/pmbattle/internal/domain"
)

type Client struct {
	URL  string
	HTTP *http.Client
}

func (c Client) Fetch(ctx context.Context) ([]domain.CanonicalEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch schedule: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch schedule: status %s", resp.Status)
	}
	return Parse(resp.Body)
}
