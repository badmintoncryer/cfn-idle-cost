package main

import (
	"fmt"
	"math"
	"net/netip"
	"strings"
)

// Connectivity, DNS, firewall, monitoring and email types: VPN concentrators,
// route servers, IPAM, Direct Connect, Cloud WAN, VPC Lattice, Route 53, WAF
// Classic, Firewall Manager and SES.
func init() {
	rules["AWS::EC2::RouteServerEndpoint"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "vpc|RouteServer-EndpointHours", "route server endpoint")
	}
	rules["AWS::EC2::VPNConcentrator"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "vpc|VPN-concentrator-Usage-Hours:ipsec.1", "VPN concentrator (site connections are counted on each VPNConnection)")
		if p["TransitGatewayId"] != nil {
			e.hourly(1, "", "tgw|TransitGatewayConcentratorVPN", "Transit Gateway attachment")
		}
	}
	rules["AWS::EC2::TrafficMirrorSession"] = func(e *est, p map[string]any) {
		eni := fmt.Sprint(p["NetworkInterfaceId"])
		if e.first(func(q map[string]any) bool { return fmt.Sprint(q["NetworkInterfaceId"]) == eni }) {
			e.hourly(1, "", "vpc|ENI-Mirror", "mirrored network interface (one fee per ENI)")
		}
	}
	rules["AWS::EC2::VPCEncryptionControl"] = func(e *est, p map[string]any) {
		// ponytail: assumes the VPC has an ENI; an empty VPC is not billed.
		e.hourly(1, "", "vpc|VPCEncryptionControls-hours", "VPC Encryption Controls")
	}
	rules["AWS::EC2::IPAM"] = func(e *est, p map[string]any) {
		switch e.str(p, "Tier", "") {
		case "free":
		case "advanced":
			panic(unresolved("the Advanced Tier bills per active IP address in the operating Regions"))
		default:
			panic(unresolved("Tier is not set (the Advanced Tier bills per active IP address)"))
		}
	}
	rules["AWS::EC2::IPAMPoolCidr"] = ipamPoolCidr
	rules["AWS::EC2::CapacityReservationFleet"] = capacityReservationFleet

	rules["AWS::DirectConnect::Connection"] = func(e *est, p map[string]any) {
		bw := strings.TrimSuffix(e.mustStr(p, "Bandwidth"), "bps") // 10Gbps -> 10G
		e.hourly(1, "", "dx|PortUsage:"+bw, bw+"bps dedicated port (billed from activation or 90 days after the LOA)")
	}
	siteLink := func(e *est, p map[string]any) {
		if e.flag(p, "EnableSiteLink") {
			e.hourly(1, "", "dx|SiteLinkVifHour", "SiteLink")
		}
	}
	rules["AWS::DirectConnect::PrivateVirtualInterface"] = siteLink
	rules["AWS::DirectConnect::TransitVirtualInterface"] = siteLink
	rules["AWS::DirectConnect::DirectConnectGatewayAssociation"] = func(e *est, p map[string]any) {
		if r, ok := e.st.Resources[refID(p["AssociatedGatewayId"])]; ok {
			if r.Type != "AWS::EC2::TransitGateway" {
				return
			}
		} else if !strings.HasPrefix(e.mustStr(p, "AssociatedGatewayId"), "tgw-") {
			return // a virtual private gateway: free
		}
		e.hourly(1, "", "tgw|TransitGatewayDirectConnect", "Transit Gateway Direct Connect attachment")
	}
	rules["AWS::Interconnect::Connection"] = func(e *est, p map[string]any) {
		bw := strings.TrimSuffix(e.mustStr(p, "Bandwidth"), "bps") // 1Gbps -> 1G
		// ponytail: AWS assigns the tier by Region pair (GetAtt BillingTier); Tier 1 is the floor.
		e.hourly(1, "", "interconnect|"+bw+"-Tier1", bw+"bps managed connection (Tier 1, the lowest)")
	}

	// Every Cloud WAN attachment type has one price per Region; the Price List
	// has no Direct Connect row in most Regions, so it uses the VPC one.
	for t, op := range map[string]string{
		"AWS::NetworkManager::VpcAttachment": "CloudWANVPC", "AWS::NetworkManager::SiteToSiteVpnAttachment": "CloudWANS2SVPN",
		"AWS::NetworkManager::ConnectAttachment": "CloudWANConnect", "AWS::NetworkManager::DirectConnectGatewayAttachment": "CloudWANVPC",
	} {
		rules[t] = func(e *est, p map[string]any) {
			// ponytail: priced in the stack's Region, not the edge location's.
			e.hourly(1, "", "wan|"+op, "Cloud WAN attachment")
		}
	}
	rules["AWS::NetworkManager::TransitGatewayPeering"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "wan|CloudWANTGWPeering", "Cloud WAN peering")
		e.hourly(1, "", "tgw|TransitGatewayPeering", "Transit Gateway peering attachment")
	}

	rules["AWS::VpcLattice::ServiceNetworkResourceAssociation"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "vpc|VpcLattice-Service-Network-Resource-Hours", "resource in a service network")
	}
	interfaceEndpoint := func(subnets, why string) func(e *est, p map[string]any) {
		return func(e *est, p map[string]any) {
			n := e.length(p, subnets)
			if n < 0 {
				panic(unresolved(subnets + " is not set"))
			}
			e.hourly(float64(n), "AZ", "vpc|VpcEndpoint-Hours", why)
		}
	}
	rules["AWS::NeptuneGraph::PrivateGraphEndpoint"] = interfaceEndpoint("SubnetIds", "interface endpoint")
	rules["AWS::Deadline::LicenseEndpoint"] = interfaceEndpoint("SubnetIds", "license endpoint (interface endpoint rate)")
	rules["AWS::MSK::VpcConnection"] = interfaceEndpoint("ClientSubnets", "MSK-managed interface endpoint")
	rules["AWS::OpenSearchServerless::VpcEndpoint"] = func(e *est, p map[string]any) {
		interfaceEndpoint("SubnetIds", "interface endpoint")(e, p)
		// ponytail: the service-managed endpoint may add a second hosted zone.
		e.monthly(1, "", "r53|HostedZone", "private hosted zone")
	}

	rules["AWS::Route53Profiles::ProfileAssociation"] = func(e *est, p map[string]any) {
		// ponytail: beyond 100 associations each one adds $0.0014/h; not counted.
		if e.first(nil) {
			e.hourly(1, "", "resolver|DNS-ProfileBasePackage", "Profiles fee, once per account and Region (up to 100 VPC associations)")
		}
	}
	rules["AWS::Route53GlobalResolver::GlobalResolver"] = globalResolver
	namespace := func(e *est, p map[string]any) {
		e.monthly(1, "", "r53|HostedZone", "hosted zone of the namespace")
	}
	rules["AWS::ServiceDiscovery::PrivateDnsNamespace"] = namespace
	rules["AWS::ServiceDiscovery::PublicDnsNamespace"] = namespace

	// WAF Classic bills $1 per rule added to a web ACL, so the rules are counted there.
	rules["AWS::WAF::WebACL"] = wafClassicACL
	rules["AWS::WAFRegional::WebACL"] = wafClassicACL
	for _, t := range []string{"AWS::WAF::Rule", "AWS::WAFRegional::Rule", "AWS::WAFRegional::RateBasedRule"} {
		rules[t] = func(e *est, p map[string]any) {}
	}
	rules["AWS::FMS::Policy"] = func(e *est, p map[string]any) {
		if e.str(asMap(p["SecurityServicePolicyData"]), "Type", "") == "SHIELD_ADVANCED" {
			return // included with the Shield Advanced subscription
		}
		e.monthly(1, "", "fms|Policy", "Firewall Manager policy (resources it deploys are not counted)")
	}
	rules["AWS::NetworkSecurityManager::Policy"] = func(e *est, p map[string]any) {
		if e.first(nil) {
			e.hourly(1, "", "nsm|Usage-Trigger-Policy", "Network Security Manager fee, once per account and Region")
		}
	}

	rules["AWS::SES::MailManagerIngressPoint"] = func(e *est, p map[string]any) {
		if t := e.mustStr(p, "Type"); t != "AUTH" { // one authenticated endpoint is free
			e.monthly(1, "", "ses|IngressPoint-Subscription", t+" ingress endpoint (billed while closed too)")
		}
	}
	rules["AWS::SES::DedicatedIpPool"] = func(e *est, p map[string]any) {
		managed := func(q map[string]any) bool { return e.str(q, "ScalingMode", "STANDARD") == "MANAGED" }
		if managed(p) && e.first(managed) {
			e.monthly(1, "", "ses|DIP-Managed-Acc-Sub-Hours", "Dedicated IPs (managed), once per account")
		}
	}
	rules["AWS::SES::Tenant"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "ses|Tenant-Count", "tenant")
	}

	rules["AWS::InternetMonitor::Monitor"] = func(e *est, p map[string]any) {
		if e.str(p, "Status", "ACTIVE") == "INACTIVE" {
			return
		}
		e.hourly(float64(max(0, e.length(p, "Resources"))), "resources", "im|CW:InternetMonitor-MonitoredResource", "monitored resources")
	}
	rules["AWS::ApplicationInsights::Application"] = func(e *est, p map[string]any) {
		if e.flag(p, "AutoConfigurationEnabled") || e.length(p, "ComponentMonitoringSettings") > 0 || e.length(p, "CustomComponents") > 0 {
			panic(unresolved("the alarms and metrics it creates depend on the resources it finds"))
		}
	}
	rules["AWS::ARCRegionSwitch::Plan"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "arc|Global-AmazonARCRegionSwitchPlan", "Region switch plan")
	}

	rules["AWS::RefactorSpaces::Environment"] = refactorEnvironment
	rules["AWS::RefactorSpaces::Application"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "elb|Load Balancer-Network", "proxy Network Load Balancer")
	}
	rules["AWS::RefactorSpaces::Service"] = func(e *est, p map[string]any) {} // its VPC is counted on the environment
}

// first reports whether this resource is the first of its type in the stack,
// by logical ID, whose properties match (nil matches any). Fees billed once per
// account or per shared thing are put on the first.
func (e *est) first(match func(p map[string]any) bool) bool {
	for _, id := range sortedIDs(e.st.Resources) {
		if id == e.id {
			return true
		}
		if r := e.st.Resources[id]; r.Type == e.r.Type && (match == nil || e.maybe(func() bool { return match(e.props(r)) })) {
			return false
		}
	}
	return true
}

// ipamPoolCidr bills the IPv4 addresses of an Amazon-provided contiguous public
// block; BYOIP and private pools are free.
func ipamPoolCidr(e *est, p map[string]any) {
	_, pool := e.ref(p, "IpamPoolId")
	if e.str(pool.Properties, "PublicIpSource", "byoip") != "amazon" || e.str(pool.Properties, "AddressFamily", "") != "ipv4" {
		return
	}
	bits := e.num(p, "NetmaskLength", 0)
	if c := e.str(p, "Cidr", ""); c != "" {
		pf, err := netip.ParsePrefix(c)
		if err != nil {
			panic(unresolved(fmt.Sprintf("Cidr is %q", c)))
		}
		bits = float64(pf.Bits())
	}
	if bits == 0 {
		panic(unresolved("NetmaskLength is not set"))
	}
	e.hourly(math.Pow(2, 32-bits), "IPv4", "vpc|PublicIPv4:ContiguousBlock", fmt.Sprintf("Amazon-provided contiguous IPv4 block (/%g)", bits))
}

// capacityReservationFleet bills the capacity of the highest-priority instance
// type (lowest Priority), which a prioritized fleet fills first.
// ponytail: assumes that type has room for the whole target; spill-over to the next type is not modelled.
func capacityReservationFleet(e *est, p map[string]any) {
	specs, _ := p["InstanceTypeSpecifications"].([]any)
	var best map[string]any
	for _, s := range specs {
		s := asMap(s)
		if best == nil || e.num(s, "Priority", 0) < e.num(best, "Priority", 0) {
			best = s
		}
	}
	if best == nil {
		panic(unresolved("InstanceTypeSpecifications is not set"))
	}
	if pl := e.mustStr(best, "InstancePlatform"); pl != "Linux/UNIX" {
		panic(unresolved("InstancePlatform " + pl + " is not supported yet"))
	}
	t := e.mustStr(best, "InstanceType")
	n := math.Ceil(e.mustNum(p, "TotalTargetCapacity") / e.num(best, "Weight", 1))
	e.hourly(n, "", "ec2|"+t, t+" reserved capacity (Linux)")
}

// globalResolver bills the two bundled Regions and each additional one, at the
// DNS filtering rate when a firewall rule in the template sits on one of its DNS views.
// ponytail: firewall rules in other stacks are missed; AWS doesn't say what triggers the filtering rate.
func globalResolver(e *est, p map[string]any) {
	n := e.length(p, "Regions")
	if n < 0 {
		panic(unresolved("Regions is not set"))
	}
	rate := "WithoutFiltering"
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if r.Type != "AWS::Route53GlobalResolver::FirewallRule" {
			continue
		}
		if v, ok := e.st.Resources[refID(e.props(r)["DnsViewId"])]; ok && refID(e.props(v)["GlobalResolverId"]) == e.id {
			rate = "WithFiltering"
			break
		}
	}
	e.hourly(2, "Regions", "gr|GlobalResolver-BaseRegions"+rate, "first two Regions")
	e.hourly(float64(n-2), "Regions", "gr|GlobalResolver-AdditionalRegions"+rate, "additional Regions")
}

// wafClassicACL bills a WAF Classic web ACL and the rules added to it.
func wafClassicACL(e *est, p map[string]any) {
	e.monthly(1, "", "waf|WebACL", "web ACL (WAF Classic)")
	e.monthly(float64(max(0, e.length(p, "Rules"))), "rules", "waf|Rule", "rules (WAF Classic)")
}

// refactorEnvironment bills the environment and, with a transit gateway
// network fabric, an attachment per VPC of its applications and services in
// this template.
func refactorEnvironment(e *est, p map[string]any) {
	e.hourly(1, "", "refactor|EnvironmentHours", "Refactor Spaces environment")
	switch f := e.str(p, "NetworkFabricType", ""); f {
	case "NONE":
		return
	case "":
		panic(unresolved("NetworkFabricType is not set"))
	}
	vpcs := map[string]bool{}
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if !in(r.Type, "AWS::RefactorSpaces::Application", "AWS::RefactorSpaces::Service") {
			continue
		}
		if rp := e.props(r); refID(rp["EnvironmentIdentifier"]) == e.id && rp["VpcId"] != nil {
			vpcs[fmt.Sprint(rp["VpcId"])] = true
		}
	}
	e.hourly(float64(len(vpcs)), "VPCs", "tgw|TransitGatewayVPC", "Transit Gateway attachment per VPC in this template")
}
