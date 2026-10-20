package main

import (
	"strings"
	"testing"
)

// paid are types that cost money idle; a whole-service prefix in free once hid them.
var paid = []string{
	"AWS::RDS::DBShardGroup", "AWS::EKS::EksAnywhereSubscription", "AWS::FSx::FileCache", "AWS::RDS::Deployment",
	"AWS::AppConfig::ExperimentRun", "AWS::CloudFront::Distribution", "AWS::Route53Resolver::OutpostResolver",
	"AWS::Transfer::WebApp", "AWS::AutoScaling::WarmPool", "AWS::GameLift::ContainerFleet", "AWS::GameLift::GameServerGroup",
	"AWS::Route53Resolver::FirewallRuleGroupAssociation", "AWS::NetworkFirewall::VpcEndpointAssociation", "AWS::EC2::VPC",
	"AWS::EC2::TransitGateway", "AWS::Route53::TrafficPolicyInstance", "AWS::CloudFront::AnycastIpList",
	"AWS::ECS::ExpressGatewayService", "AWS::RDS::DBProxyEndpoint", "AWS::EC2::VPCEndpointService", "AWS::EKS::Capability",
	"AWS::Glue::Trigger", "AWS::Glue::Crawler", "AWS::WAFv2::WebACLAssociation", "AWS::CloudFront::DistributionTenant",
	"AWS::KinesisFirehose::DeliveryStream", "AWS::Bedrock::ImportedModel", "AWS::Budgets::BudgetsAction",
	"AWS::CloudFront::MonitoringSubscription", "AWS::CloudWatch::LogAlarm", "AWS::ServiceDiscovery::Instance",
	"AWS::Route53Resolver::FirewallDomainList", "AWS::CertificateManager::Certificate", "AWS::CloudWatch::OTelEnrichment",
	"AWS::GuardDuty::Detector", "AWS::SecurityHub::HubV2", "AWS::Config::ConfigurationRecorder", "AWS::Logs::Integration",
	"AWS::RDS::ReservedDBInstance", "AWS::Lambda::Version",
}

func TestFreeIsExplicit(t *testing.T) {
	for _, f := range free {
		if strings.HasSuffix(f, "::") && f != "Custom::" {
			t.Errorf("free has prefix %s; list its free types one by one", f)
		}
	}
	for _, p := range paid {
		if freeType(p) {
			t.Errorf("%s costs money idle but is in free", p)
		}
	}
}
