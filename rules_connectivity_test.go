package main

import (
	"bytes"
	"testing"
)

// connectivityPrices are the us-east-1 list prices of the connectivity rules,
// fixed so the golden file doesn't move with the snapshot.
var connectivityPrices = &Prices{Source: "test prices", AsOf: "2026-10-20", Regions: map[string]map[string]float64{
	"us-east-1": {
		"vpc|RouteServer-EndpointHours": 0.75, "vpc|VPN-concentrator-Usage-Hours:ipsec.1": 1.95, "vpc|VPN-concentrator-site-Usage-Hours:ipsec.1": 0.01,
		"tgw|TransitGatewayConcentratorVPN": 0.05, "vpc|ENI-Mirror": 0.015, "vpc|VPCEncryptionControls-hours": 0.15,
		"vpc|PublicIPv4:ContiguousBlock": 0.008, "ec2|m5.large": 0.096, "dx|PortUsage:10G": 2.25, "dx|SiteLinkVifHour": 0.5,
		"tgw|TransitGatewayDirectConnect": 0.05, "interconnect|1G-Tier1": 1.37, "wan|CloudWANVPC": 0.065,
		"wan|CloudWANTGWPeering": 0.065, "tgw|TransitGatewayPeering": 0.05, "vpc|VpcLattice-Service-Network-Resource-Hours": 0.1,
		"vpc|VpcEndpoint-Resource-Hours": 0.02, "vpc|VpcEndpoint-Hours": 0.01, "resolver|DNS-ProfileBasePackage": 0.75,
		"waf|WebACL": 5, "waf|Rule": 1, "fms|Policy": 100, "nsm|Usage-Trigger-Policy": 0.35, "ses|IngressPoint-Subscription": 50,
		"ses|DIP-Managed-Acc-Sub-Hours": 15, "ses|Tenant-Count": 0.005, "im|CW:InternetMonitor-MonitoredResource": 0.01,
		"refactor|EnvironmentHours": 0.028, "tgw|TransitGatewayVPC": 0.05, "elb|Load Balancer-Network": 0.0225,
	},
	"global": {
		"r53|HostedZone": 0.5, "gr|GlobalResolver-BaseRegionsWithFiltering": 2.5, "gr|GlobalResolver-AdditionalRegionsWithFiltering": 1.5,
		"arc|Global-AmazonARCRegionSwitchPlan": 70,
	},
}}

// VPN concentrators, route servers, traffic mirroring, IPAM, capacity
// reservation fleets, Direct Connect, Interconnect, Cloud WAN, VPC Lattice,
// endpoints, Route 53 Profiles and Global Resolver, Cloud Map namespaces, WAF
// Classic, Firewall Manager, Network Security Manager, SES, Internet Monitor,
// Application Insights, Region switch and Refactor Spaces.
func TestConnectivityRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/connectivity.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, connectivityPrices), connectivityPrices).WriteText(&b)
	golden(t, "connectivity.yaml", b.String())
}
