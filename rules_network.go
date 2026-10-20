package main

import (
	"fmt"
	"strings"
)

// Network and edge types that cost money idle only in some configurations.
func init() {
	rules["AWS::EC2::VPC"] = func(e *est, p map[string]any) {
		// ponytail: assumes the VPC has an ENI; an empty VPC is not billed.
		if m := e.str(asMap(p["VpcEncryptionControl"]), "Mode", ""); m != "" {
			e.hourly(1, "", "vpc|VPCEncryptionControls-hours", "VPC Encryption Controls ("+m+" mode)")
		}
	}
	rules["AWS::EC2::TransitGateway"] = transitGatewayEncryption
	rules["AWS::NetworkFirewall::VpcEndpointAssociation"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "nfw|Secondary-Endpoint-Hour", "secondary firewall endpoint")
	}
	rules["AWS::RDS::DBProxyEndpoint"] = func(e *est, p map[string]any) {
		n := e.length(p, "VpcSubnetIds")
		if n < 0 {
			panic(unresolved("VpcSubnetIds is not set"))
		}
		e.hourly(float64(n), "AZ", "vpc|VpcEndpoint-Hours", "proxy endpoint (interface endpoint per subnet)")
	}
	rules["AWS::EC2::VPCEndpointService"] = func(e *est, p map[string]any) {
		e.length(p, "SupportedRegions") // panics unless absent or a list
		l, _ := p["SupportedRegions"].([]any)
		for _, r := range l {
			if e.str(map[string]any{"SupportedRegions": r}, "SupportedRegions", "") != e.st.Region {
				panic(unresolved("cross-Region PrivateLink bills per active remote Region"))
			}
		}
	}
	rules["AWS::Route53Resolver::FirewallRuleGroupAssociation"] = func(e *est, p map[string]any) {
		_, g := e.ref(p, "FirewallRuleGroupId")
		l, _ := g.Properties["FirewallRules"].([]any)
		for _, r := range l {
			if _, ok := asMap(r)["DnsThreatProtection"]; ok {
				e.hourly(1, "", "resolver|AdvancedDNS-ThreatProtectionsVPCAssociationHours", "DNS Firewall Advanced rule group association")
				return
			}
		}
	}
	rules["AWS::Route53Resolver::FirewallDomainList"] = func(e *est, p map[string]any) {
		if _, ok := p["DomainFileUrl"]; ok {
			panic(unresolved("the domains come from DomainFileUrl"))
		}
		e.monthly(float64(max(0, e.length(p, "Domains"))), "domains", "resolver|DNS-FirewallDomainName", "domains in a custom list")
	}
	rules["AWS::Route53::TrafficPolicyInstance"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "r53tf|Traffic-Flow-Policy-Records", "Traffic Flow policy record")
	}
	rules["AWS::ServiceDiscovery::Instance"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "cloudmap|Cloud-Map-Resources", "Cloud Map registered instance")
	}
	rules["AWS::KinesisFirehose::DeliveryStream"] = func(e *est, p map[string]any) {
		for _, d := range []string{"AmazonopensearchserviceDestinationConfiguration", "AmazonOpenSearchServerlessDestinationConfiguration", "ElasticsearchDestinationConfiguration"} {
			vpc, ok := asMap(p[d])["VpcConfiguration"]
			if !ok {
				continue
			}
			n := e.length(asMap(vpc), "SubnetIds")
			if n < 0 {
				panic(unresolved("SubnetIds is not set"))
			}
			// ponytail: one AZ per subnet; two subnets in one AZ are billed once.
			e.hourly(float64(n), "AZ", "firehose|Firehose-VpcDelivery-Hours", "VPC delivery per AZ")
		}
	}
	rules["AWS::CloudFront::Distribution"] = func(e *est, p map[string]any) {
		vc := asMap(asMap(p["DistributionConfig"])["ViewerCertificate"])
		if e.str(vc, "SslSupportMethod", "sni-only") == "vip" {
			// ponytail: billed per certificate; two distributions sharing one are counted twice.
			e.monthly(1, "", "cf|SSL-Cert-Custom", "dedicated IP custom SSL certificate")
		}
	}
	rules["AWS::CloudFront::MonitoringSubscription"] = func(e *est, p map[string]any) {
		c := asMap(asMap(p["MonitoringSubscription"])["RealtimeMetricsSubscriptionConfig"])
		if e.str(c, "RealtimeMetricsSubscriptionStatus", "Disabled") == "Enabled" {
			e.monthly(8, "metrics", "cw|CW:MetricMonitorUsage", "additional CloudFront metrics")
		}
	}
	rules["AWS::CloudFront::AnycastIpList"] = func(e *est, p map[string]any) {
		if e.length(p, "IpamCidrConfigs") > 0 {
			return // BYOIP: no CloudFront charge
		}
		// The pricing page bills AWS-provided IPv4 per IP; the Price List also has
		// StaticIP-IPv4 at $15,000 per list-month, which the page does not mention.
		e.hourly(e.mustNum(p, "IpCount"), "IPv4", "cf|Global-AnycastStaticIP-IPv4", "Anycast static IPv4 (IPv6 is free)")
	}
	rules["AWS::WAFv2::WebACLAssociation"] = func(e *est, p map[string]any) {
		arn := p["ResourceArn"]
		r, ok := e.st.Resources[refID(arn)]
		if ok && r.Type == "AWS::Amplify::App" || !ok && strings.Contains(fmt.Sprint(arn), ":amplify:") {
			e.monthly(1, "", "amplify|AmplifyWAF", "Amplify Hosting firewall")
		}
	}
	rules["AWS::CertificateManager::Certificate"] = func(e *est, p map[string]any) {
		if e.str(p, "CertificateExport", "DISABLED") == "ENABLED" {
			panic(unresolved("exportable certificates bill $7 per name / $79 per wildcard at issuance and each renewal"))
		}
	}
}

// transitGatewayEncryption bills VPC Encryption Controls for every VPC attached
// to a transit gateway with encryption support, in this template, unless the
// VPC already pays for its own encryption controls.
// ponytail: attachments in other stacks (a CDK network stack) are missed; scan e.all with pointsHere if that matters.
func transitGatewayEncryption(e *est, p map[string]any) {
	if e.str(p, "EncryptionSupport", "disable") != "enable" {
		return
	}
	vpcs := map[string]bool{}
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if !in(r.Type, "AWS::EC2::TransitGatewayVpcAttachment", "AWS::EC2::TransitGatewayAttachment") {
			continue
		}
		if r.Condition != "" {
			if on, ok := e.tryCondition(r.Condition); ok && !on {
				continue
			}
		}
		ap := e.props(r)
		if refID(ap["TransitGatewayId"]) != e.id {
			continue
		}
		if v, ok := e.st.Resources[refID(ap["VpcId"])]; ok && v.Type == "AWS::EC2::VPC" &&
			e.maybe(func() bool { return e.str(asMap(e.props(v)["VpcEncryptionControl"]), "Mode", "") != "" }) {
			continue // counted on the VPC
		}
		vpcs[fmt.Sprint(ap["VpcId"])] = true
	}
	e.hourly(float64(len(vpcs)), "VPCs", "vpc|VPCEncryptionControls-hours", "encryption support, per attached VPC in this template")
}

// networkFirewall bills an endpoint per AZ, plus Advanced Inspection when its
// policy (in this template) uses TLS inspection.
func networkFirewall(e *est, p map[string]any) {
	n := e.length(p, "SubnetMappings")
	if n < 0 {
		n = e.length(p, "AvailabilityZoneMappings")
	}
	if n < 0 {
		panic(unresolved("SubnetMappings is not set"))
	}
	e.hourly(float64(n), "endpoints", "nfw|Endpoint-Hour", "firewall endpoint per AZ")
	if e.maybe(func() bool {
		_, pol := e.ref(p, "FirewallPolicyArn")
		_, tls := asMap(pol.Properties["FirewallPolicy"])["TLSInspectionConfigurationArn"]
		return tls
	}) {
		e.hourly(float64(n), "endpoints", "nfw|Advanced-Inspection-Endpoint-Hour", "Advanced Inspection (TLS inspection) per AZ")
	}
}

// webACL bills the web ACL, its rules, and the rules of the rule groups (in
// this template) it references.
func webACL(e *est, p map[string]any) {
	e.monthly(1, "", "waf|WebACLV2", "web ACL")
	e.monthly(float64(max(0, e.length(p, "Rules"))), "rules", "waf|RuleV2", "rules (managed rule group fees not counted)")
	l, _ := p["Rules"].([]any)
	for _, r := range l {
		arn := asMap(asMap(asMap(r)["Statement"])["RuleGroupReferenceStatement"])["Arn"]
		g, ok := e.st.Resources[refID(arn)]
		if !ok || g.Type != "AWS::WAFv2::RuleGroup" {
			continue
		}
		if gr, _ := e.props(g)["Rules"].([]any); len(gr) > 0 {
			e.monthly(float64(len(gr)), "rules", "waf|RuleV2", "rules in rule group "+refID(arn))
		}
	}
}
