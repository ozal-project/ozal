package certs

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// VerifyToken asks Cloudflare whether the token is active. It says nothing
// about the token's permissions; the zone setup reports any it lacks.
func (c *CloudflareClient) VerifyToken(ctx context.Context) error {
	var result struct {
		Status string `json:"status"`
	}
	if err := c.call(ctx, "GET", "/user/tokens/verify", nil, &result); err != nil {
		return err
	}
	if result.Status != "active" {
		return fmt.Errorf("cloudflare: the token is %q, not active", result.Status)
	}
	return nil
}

// maxZonePages bounds the zone listing: 20 pages of 50 zones.
const maxZonePages = 20

// ZoneNames lists the zones the token can read, sorted. A token that reads
// none is an error: without Zone · Read nothing else can work.
func (c *CloudflareClient) ZoneNames(ctx context.Context) ([]string, error) {
	var names []string
	for page := 1; page <= maxZonePages; page++ {
		var zones []struct {
			Name string `json:"name"`
		}
		query := url.Values{"per_page": {"50"}, "page": {fmt.Sprint(page)}}
		if err := c.call(ctx, "GET", "/zones?"+query.Encode(), nil, &zones); err != nil {
			return nil, err
		}
		for _, z := range zones {
			if name := strings.ToLower(strings.TrimSuffix(z.Name, ".")); name != "" {
				names = append(names, name)
			}
		}
		if len(zones) < 50 {
			break
		}
	}
	if len(names) == 0 {
		return nil, errors.New("cloudflare: the token sees no zone; it needs Zone · Read on the zones Ozal serves")
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}
