package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// OfferBase serves the public, unauthenticated AWS Bulk Price List files.
const OfferBase = "https://pricing.us-east-1.amazonaws.com"

// Prices maps region -> key -> USD per unit. Keys are "<selector>|<detail>".
type Prices struct {
	AsOf    string                        `json:"asOf"`
	Regions map[string]map[string]float64 `json:"regions"`
	Source  string                        `json:"-"`
	wanted  map[string]map[string]bool    // set to probe which keys an estimate needs
}

//go:embed prices.json
var embeddedPrices []byte

func BundledPrices() (*Prices, error) {
	p := &Prices{}
	if err := json.Unmarshal(embeddedPrices, p); err != nil {
		return nil, fmt.Errorf("bundled prices: %w", err)
	}
	p.Source = "bundled snapshot " + p.AsOf
	return p, nil
}

// Get looks a key up in the region, falling back to global prices (Route 53).
func (p *Prices) Get(region, key string) (float64, bool) {
	if p.wanted != nil { // probe: record every key and keep estimating
		if p.wanted[region] == nil {
			p.wanted[region] = map[string]bool{}
		}
		p.wanted[region][key] = true
		return 1, true
	}
	if v, ok := p.Regions[region][key]; ok {
		return v, true
	}
	v, ok := p.Regions["global"][key]
	return v, ok
}

// A selector picks the products of one price family out of an offer and names
// their keys. The same selector drives the snapshot build and --live, so both
// paths produce identical keys.
type selector struct {
	name     string
	service  string
	global   bool              // one offer for all regions, stored under "global"
	families []string          // productFamily values; nil = whole service (small offers)
	filters  map[string]string // exact attribute matches, also sent to GetProducts
	key      func(a map[string]string, usage string) string
	// liveFilter narrows a --live query to one key's products (an instance
	// family has thousands); nil queries the whole family once.
	liveFilter func(detail string) map[string]string
}

func byInstanceType(detail string) map[string]string {
	return map[string]string{"instanceType": detail}
}

func in(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

var rdsEngines = []string{"2", "14", "16", "18", "21"} // mysql, postgres, aurora-mysql, mariadb, aurora-postgresql

var selectors = []selector{
	{name: "ec2", service: "AmazonEC2", families: []string{"Compute Instance"},
		filters: map[string]string{"operatingSystem": "Linux", "tenancy": "Shared", "preInstalledSw": "NA", "capacitystatus": "Used", "operation": "RunInstances"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "BoxUsage:") {
				return a["instanceType"]
			}
			return ""
		},
		liveFilter: byInstanceType},
	{name: "nat", service: "AmazonEC2", families: []string{"NAT Gateway"},
		key: func(a map[string]string, u string) string { return keep(u, "NatGateway-Hours") }},
	{name: "ebs", service: "AmazonEC2", families: []string{"Storage", "System Operation", "Provisioned Throughput"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "EBS:Volume") && !strings.HasPrefix(u, "EBS:VolumeIOUsage") {
				return u
			}
			return ""
		}},
	{name: "vpc", service: "AmazonVPC",
		key: func(a map[string]string, u string) string {
			return keep(u, "PublicIPv4:InUseAddress", "VpcEndpoint-Hours", "VpcEndpoint-GWLBE-Hours")
		}},
	{name: "elb", service: "AWSELB",
		key: func(a map[string]string, u string) string {
			if u == "LoadBalancerUsage" {
				return a["productFamily"]
			}
			return ""
		}},
	{name: "rds", service: "AmazonRDS", families: []string{"Database Instance", "Database Storage", "Provisioned IOPS", "Provisioned Throughput", "ServerlessV2"},
		key: func(a map[string]string, u string) string {
			if !in(a["engineCode"], rdsEngines...) {
				return ""
			}
			if a["productFamily"] == "Database Instance" {
				base, _, _ := strings.Cut(u, ":")
				if !in(base, "InstanceUsage", "Multi-AZUsage", "InstanceUsageIOOptimized") {
					return ""
				}
				// usagetype abbreviates xlarge to xl, so key on instanceType instead
				return base + ":" + a["instanceType"] + "|" + a["engineCode"]
			}
			return u + "|" + a["engineCode"]
		},
		liveFilter: func(d string) map[string]string {
			u, code, _ := strings.Cut(d, "|")
			f := map[string]string{"engineCode": code}
			switch _, t, inst := strings.Cut(u, ":db."); {
			case inst:
				f["productFamily"], f["instanceType"] = "Database Instance", "db."+t
			case strings.HasPrefix(u, "Aurora:ServerlessV2"):
				f["productFamily"] = "ServerlessV2"
			case strings.Contains(u, "Throughput"):
				f["productFamily"] = "Provisioned Throughput"
			case strings.Contains(u, "PIOPS") && !strings.Contains(u, "Storage"):
				f["productFamily"] = "Provisioned IOPS"
			default:
				f["productFamily"] = "Database Storage"
			}
			return f
		}},
	{name: "es", service: "AmazonES", families: []string{"Amazon OpenSearch Service Instance", "Amazon OpenSearch Service Volume"},
		key: func(a map[string]string, u string) string {
			switch {
			case strings.HasPrefix(u, "ESInstance:"):
				return a["instanceType"]
			case strings.HasPrefix(u, "ES:"):
				return u
			}
			return ""
		},
		liveFilter: func(d string) map[string]string {
			if strings.HasPrefix(d, "ES:") {
				return map[string]string{"productFamily": "Amazon OpenSearch Service Volume"}
			}
			return byInstanceType(d)
		}},
	{name: "elasticache", service: "AmazonElastiCache", families: []string{"Cache Instance", "ElastiCache Serverless"},
		key: func(a map[string]string, u string) string {
			switch {
			case strings.HasPrefix(u, "NodeUsage:"):
				return a["cacheEngine"] + "|" + a["instanceType"]
			case strings.HasPrefix(u, "CachedData:"):
				return u
			}
			return ""
		},
		liveFilter: func(d string) map[string]string {
			if strings.HasPrefix(d, "CachedData:") {
				return map[string]string{"productFamily": "ElastiCache Serverless"}
			}
			_, t, _ := strings.Cut(d, "|")
			return byInstanceType(t)
		}},
	{name: "ddb", service: "AmazonDynamoDB", families: []string{"Provisioned IOPS", "DDB-Operation-ReplicatedWrite"},
		key: func(a map[string]string, u string) string {
			return keep(u, "ReadCapacityUnit-Hrs", "WriteCapacityUnit-Hrs", "IA-ReadCapacityUnit-Hrs", "IA-WriteCapacityUnit-Hrs")
		}},
	{name: "kinesis", service: "AmazonKinesis", families: []string{"Kinesis Streams"},
		key: func(a map[string]string, u string) string { return keep(u, "Storage-ShardHour", "OnDemand-StreamHour") }},
	{name: "kms", service: "awskms", families: []string{"Encryption Key"},
		key: func(a map[string]string, u string) string { return keep(u, "KMS-Keys") }},
	{name: "secrets", service: "AWSSecretsManager", families: []string{"Secret"},
		key: func(a map[string]string, u string) string { return keep(u, "AWSSecretsManager-Secrets") }},
	{name: "fsx", service: "AmazonFSx", families: []string{"Storage", "Provisioned Throughput", "Provisioned IOPS"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Storage") || strings.HasPrefix(u, "ThroughputCapacity") || strings.HasPrefix(u, "ProvisionedSSDIOPS") {
				return a["fileSystemType"] + "|" + u // the same usagetype appears under several file system types
			}
			return ""
		}},
	{name: "efs", service: "AmazonEFS", families: []string{"Provisioned Throughput"},
		key: func(a map[string]string, u string) string { return keep(u, "ProvisionedTP-MiBpsHrs") }},
	{name: "fargate", service: "AmazonECS", families: []string{"Compute"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Fargate-vCPU-Hours:perCPU", "Fargate-GB-Hours", "Fargate-ARM-vCPU-Hours:perCPU", "Fargate-ARM-GB-Hours", "Fargate-EphemeralStorage-GB-Hours")
		}},
	{name: "r53", service: "AmazonRoute53", global: true, families: []string{"DNS Zone"},
		key: func(a map[string]string, u string) string { return keep(u, "HostedZone") }},
}

func keep(u string, want ...string) string {
	if in(u, want...) {
		return u
	}
	return ""
}

// regionCode matches commercial regions; GovCloud (us-gov-west-1), Local Zones
// (us-east-1-bos-1) and other partitions don't match.
var regionCode = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+$`)

var usagePrefix = regexp.MustCompile(`^(EU|[A-Z]{2,4}[0-9]+)-`)

// stripUsage removes the region prefix from a usagetype: "APN1-BoxUsage:t3.micro",
// "ap-northeast-1-KMS-Keys" -> "BoxUsage:t3.micro", "KMS-Keys". us-east-1 has none.
func stripUsage(u, region string) string {
	if region != "" && strings.HasPrefix(u, region+"-") {
		return u[len(region)+1:]
	}
	return usagePrefix.ReplaceAllString(u, "")
}

type offerProduct struct {
	SKU           string            `json:"sku"`
	ProductFamily string            `json:"productFamily"`
	Attributes    map[string]string `json:"attributes"`
}

type offerTerm struct {
	PriceDimensions map[string]struct {
		Unit         string            `json:"unit"`
		PricePerUnit map[string]string `json:"pricePerUnit"`
	} `json:"priceDimensions"`
}

// match returns the snapshot key of a product, or "".
func (s selector) match(p offerProduct) string {
	if s.families != nil && !in(p.ProductFamily, s.families...) {
		return ""
	}
	for k, v := range s.filters {
		if p.Attributes[k] != v {
			return ""
		}
	}
	a := map[string]string{"productFamily": p.ProductFamily}
	for k, v := range p.Attributes {
		a[k] = v
	}
	k := s.key(a, stripUsage(a["usagetype"], a["regionCode"]))
	if k == "" {
		return ""
	}
	return s.name + "|" + k
}

// price is the list price of an on-demand term. Tiered dimensions take the
// highest rate, which skips free-tier tiers (we ignore the free tier).
func price(terms map[string]offerTerm) (float64, bool) {
	best, found := 0.0, false
	for _, t := range terms {
		for _, d := range t.PriceDimensions {
			v, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64)
			if err != nil {
				continue
			}
			if d.Unit == "GiBps-mo" { // EBS gp3 throughput is listed per GiB/s; we use MiB/s
				v /= 1024
			}
			if !found || v > best {
				best, found = v, true
			}
		}
	}
	return best, found
}

// parseOffer streams one bulk offer file (EC2 is ~450 MB per region) and adds
// the selected prices to out.
func parseOffer(r io.Reader, sels []selector, out map[string]float64) (pub string, err error) {
	dec := json.NewDecoder(r)
	keys := map[string]string{} // sku -> key
	if _, err := dec.Token(); err != nil {
		return "", err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t {
		case "publicationDate":
			if err := dec.Decode(&pub); err != nil {
				return "", err
			}
		case "products":
			err = eachEntry(dec, func(sku string) error {
				var p offerProduct
				if err := dec.Decode(&p); err != nil {
					return err
				}
				for _, s := range sels {
					if k := s.match(p); k != "" {
						keys[sku] = k
						break
					}
				}
				return nil
			})
		case "terms":
			err = eachEntry(dec, func(termType string) error {
				if termType != "OnDemand" {
					var skip json.RawMessage
					return dec.Decode(&skip)
				}
				return eachEntry(dec, func(sku string) error {
					k, ok := keys[sku]
					if !ok {
						var skip json.RawMessage
						return dec.Decode(&skip)
					}
					var terms map[string]offerTerm
					if err := dec.Decode(&terms); err != nil {
						return err
					}
					if v, ok := price(terms); ok && v >= out[k] {
						out[k] = v
					}
					return nil
				})
			})
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return "", err
		}
	}
	return pub, nil
}

// eachEntry walks a JSON object, calling fn with each key while the decoder is
// positioned at its value.
func eachEntry(dec *json.Decoder, fn func(key string) error) error {
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if err := fn(t.(string)); err != nil {
			return err
		}
	}
	_, err := dec.Token()
	return err
}

// BuildPrices downloads the bulk offers of every service the selectors use, for
// every commercial region, and returns the snapshot.
func BuildPrices(regions []string, logf func(string, ...any)) (*Prices, error) {
	byService := map[string][]selector{}
	var services []string
	for _, s := range selectors {
		if byService[s.service] == nil {
			services = append(services, s.service)
		}
		byService[s.service] = append(byService[s.service], s)
	}
	type job struct{ service, region, url string }
	var jobs []job
	for _, svc := range services {
		if byService[svc][0].global {
			jobs = append(jobs, job{svc, "global", fmt.Sprintf("%s/offers/v1.0/aws/%s/current/index.json", OfferBase, svc)})
			continue
		}
		var idx struct {
			Regions map[string]struct {
				URL string `json:"currentVersionUrl"`
			} `json:"regions"`
		}
		if err := getJSON(fmt.Sprintf("%s/offers/v1.0/aws/%s/current/region_index.json", OfferBase, svc), &idx); err != nil {
			return nil, err
		}
		for r, v := range idx.Regions {
			if !regionCode.MatchString(r) || (len(regions) > 0 && !in(r, regions...)) {
				continue
			}
			jobs = append(jobs, job{svc, r, OfferBase + v.URL})
		}
	}
	p := &Prices{Regions: map[string]map[string]float64{}}
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := map[string]float64{}
			pub, err := fetchOffer(j.url, byService[j.service], out)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s %s: %w", j.service, j.region, err)
				}
				return
			}
			logf("%-18s %-16s %5d prices", j.service, j.region, len(out))
			if p.Regions[j.region] == nil {
				p.Regions[j.region] = map[string]float64{}
			}
			for k, v := range out {
				p.Regions[j.region][k] = v
			}
			if d := pub[:min(10, len(pub))]; d > p.AsOf {
				p.AsOf = d
			}
		}(j)
	}
	wg.Wait()
	return p, firstErr
}

func fetchOffer(url string, sels []selector, out map[string]float64) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return parseOffer(resp.Body, sels, out)
}

func getJSON(url string, v any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// WritePrices writes the snapshot with one price per line, sorted, so the
// weekly refresh produces a readable diff.
func WritePrices(path string, p *Prices) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "{\n  \"asOf\": %q,\n  \"regions\": {", p.AsOf)
	for i, r := range sortedIDs(p.Regions) {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "\n    %q: {", r)
		keys := sortedIDs(p.Regions[r])
		for j, k := range keys {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "\n      %q: %s", k, strconv.FormatFloat(p.Regions[r][k], 'f', -1, 64))
		}
		b.WriteString("\n    }")
	}
	b.WriteString("\n  }\n}\n")
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// selectorsFor returns the selectors whose keys appear in want.
func selectorsFor(want []string) []selector {
	names := map[string]bool{}
	for _, k := range want {
		n, _, _ := strings.Cut(k, "|")
		names[n] = true
	}
	var out []selector
	for _, s := range selectors {
		if names[s.name] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}
