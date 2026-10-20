package main

import (
	"bytes"
	"testing"
)

// networkPrices are the us-east-1 list prices of the network rules, fixed so the
// golden file doesn't move with the snapshot.
var networkPrices = &Prices{Source: "test prices", AsOf: "2026-10-20", Regions: map[string]map[string]float64{
	"us-east-1": {
		"vpc|VPCEncryptionControls-hours": 0.15, "vpc|VpcEndpoint-Hours": 0.01, "nfw|Endpoint-Hour": 0.395,
		"nfw|Secondary-Endpoint-Hour": 0.158, "nfw|Advanced-Inspection-Endpoint-Hour": 0.38,
		"resolver|AdvancedDNS-ThreatProtectionsVPCAssociationHours": 0.16, "resolver|DNS-FirewallDomainName": 0.0005,
		"cloudmap|Cloud-Map-Resources": 0.1, "firehose|Firehose-VpcDelivery-Hours": 0.01, "cw|CW:MetricMonitorUsage": 0.3,
		"waf|WebACLV2": 5, "waf|RuleV2": 1, "amplify|AmplifyWAF": 15, "tgw|TransitGatewayVPC": 0.05,
	},
	"global": {"r53tf|Traffic-Flow-Policy-Records": 50, "cf|SSL-Cert-Custom": 600, "cf|Global-AnycastStaticIP-IPv4": 0.005},
}}

// VPC encryption controls, Network Firewall secondary endpoints and TLS
// inspection, proxy endpoints, cross-Region PrivateLink, DNS Firewall, Traffic
// Flow, Cloud Map, Firehose VPC delivery, CloudFront, WAF rule groups and
// Amplify, and exportable certificates.
func TestNetworkRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/network.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, networkPrices), networkPrices).WriteText(&b)
	golden(t, "network.yaml", b.String())
}
