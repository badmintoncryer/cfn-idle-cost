package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A download cut off mid-offer is tried again from the start.
func TestFetchOfferRetry(t *testing.T) {
	offer := `{"publicationDate":"2026-11-20T00:00:00Z","products":{"S1":{"sku":"S1","productFamily":"NAT Gateway",` +
		`"attributes":{"usagetype":"USE1-NatGateway-Hours","regionCode":"us-east-1"}}},` +
		`"terms":{"OnDemand":{"S1":{"S1.T":{"priceDimensions":{"S1.T.D":{"unit":"Hrs","pricePerUnit":{"USD":"0.045"}}}}}}}}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(offer[:len(offer)/2])) // cut off
			return
		}
		w.Write([]byte(offer))
	}))
	defer srv.Close()
	saved := retryDelay
	retryDelay = 0
	defer func() { retryDelay = saved }()
	var nat []selector
	for _, s := range selectors {
		if s.name == "nat" {
			nat = append(nat, s)
		}
	}
	out, pub, err := fetchOfferRetry(srv.URL, nat, "us-east-1")
	if err != nil || calls != 2 || out["us-east-1"]["nat|NatGateway-Hours"] != 0.045 || !strings.HasPrefix(pub, "2026-11-20") {
		t.Fatalf("calls %d, out %v, pub %q, err %v", calls, out, pub, err)
	}
}
