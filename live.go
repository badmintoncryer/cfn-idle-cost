package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// LivePrices fetches, from the Price List Query API, the prices the stacks
// need. It finds them by estimating once with a probe that records every key.
func LivePrices(ctx context.Context, stacks []*Stack) (*Prices, error) {
	probe := &Prices{wanted: map[string]map[string]bool{}}
	Estimate(stacks, probe)
	// The Query API is served from us-east-1 (and a few others); us-east-1 always works.
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		return nil, err
	}
	client := pricing.NewFromConfig(cfg)
	p := &Prices{Regions: map[string]map[string]float64{}, AsOf: time.Now().UTC().Format("2006-01-02")}
	p.Source = "live Price List Query API " + p.AsOf
	for _, region := range sortedIDs(probe.wanted) {
		for _, s := range selectorsFor(sortedIDs(probe.wanted[region])) {
			offerRegion := region
			if s.global {
				offerRegion = "global"
			}
			for _, q := range liveQueries(s, region, sortedIDs(probe.wanted[region])) {
				if err := fetchLive(ctx, client, s, q, offerRegion, p.Regions); err != nil {
					return nil, fmt.Errorf("--live: %s in %s: %w", s.service, region, err)
				}
			}
		}
	}
	return p, nil
}

// liveQueries returns the GetProducts filter sets for a selector: one per
// needed key when the selector can narrow by key, else one per family.
func liveQueries(s selector, region string, keys []string) []map[string]string {
	base := map[string]string{}
	if !s.global {
		base["regionCode"] = region
	}
	for k, v := range s.filters {
		base[k] = v
	}
	var narrow []map[string]string
	if s.liveFilter != nil {
		for _, k := range keys {
			if name, detail, _ := strings.Cut(k, "|"); name == s.name {
				narrow = append(narrow, s.liveFilter(detail))
			}
		}
	}
	if narrow == nil {
		narrow = []map[string]string{{}}
	}
	var out []map[string]string
	seen := map[string]bool{}
	for _, n := range narrow {
		fams := s.families
		if f, ok := n["productFamily"]; ok || fams == nil {
			fams = []string{f}
		}
		for _, fam := range fams {
			q := map[string]string{}
			for k, v := range base {
				q[k] = v
			}
			for k, v := range n {
				q[k] = v
			}
			if fam != "" {
				q["productFamily"] = fam
			} else {
				delete(q, "productFamily")
			}
			id := fmt.Sprint(q)
			if !seen[id] {
				seen[id] = true
				out = append(out, q)
			}
		}
	}
	return out
}

func fetchLive(ctx context.Context, client *pricing.Client, s selector, q map[string]string, offerRegion string, out map[string]map[string]float64) error {
	var filters []types.Filter
	for _, k := range sortedIDs(q) {
		filters = append(filters, types.Filter{Field: aws.String(k), Type: types.FilterTypeTermMatch, Value: aws.String(q[k])})
	}
	pg := pricing.NewGetProductsPaginator(client, &pricing.GetProductsInput{ServiceCode: aws.String(s.service), Filters: filters})
	for pg.HasMorePages() {
		page, err := pg.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, raw := range page.PriceList {
			var item struct {
				Product offerProduct `json:"product"`
				Terms   struct {
					OnDemand map[string]offerTerm `json:"OnDemand"`
				} `json:"terms"`
			}
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				return err
			}
			reg, ok := productRegion(item.Product, offerRegion)
			if k := s.match(item.Product); ok && k != "" {
				record(out, reg, k, item.Terms.OnDemand)
			}
		}
	}
	return nil
}
