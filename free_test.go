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

// costsIdle are types an audit of every CloudFormation type found to cost money idle
// (AWS::WAFv2::RuleGroup is left out: its rules are billed through the web ACL rule).
var costsIdle = []string{
	"AWS::APS::Scraper", "AWS::ARCRegionSwitch::Plan", "AWS::AccessAnalyzer::Analyzer",
	"AWS::AgentRegistry::RegistryRecord", "AWS::AppFabric::IngestionDestination", "AWS::AppFlow::Flow",
	"AWS::AppStream::AppBlockBuilder", "AWS::AppStream::ImageBuilder", "AWS::ApplicationInsights::Application",
	"AWS::ApplicationSignals::ServiceLevelObjective", "AWS::Athena::CapacityReservation", "AWS::AuditManager::Assessment",
	"AWS::B2BI::Partnership", "AWS::BedrockAgentCore::GatewayTarget", "AWS::BillingConductor::BillingGroup",
	"AWS::Cloud9::EnvironmentEC2", "AWS::CodeBuild::Fleet", "AWS::CodeGuruReviewer::RepositoryAssociation",
	"AWS::Comprehend::DocumentClassifier", "AWS::Comprehend::DocumentClassifierEndpoint",
	"AWS::Comprehend::EntityRecognizer", "AWS::Comprehend::EntityRecognizerEndpoint", "AWS::DMS::DataMigration",
	"AWS::DMS::ReplicationConfig", "AWS::DRS::RecoveryPlanExecution", "AWS::DataBrew::Schedule",
	"AWS::DataPipeline::Pipeline", "AWS::DataSync::Task", "AWS::Deadline::Fleet", "AWS::Deadline::LicenseEndpoint",
	"AWS::DevOpsGuru::ResourceCollection", "AWS::DirectConnect::Connection",
	"AWS::DirectConnect::DirectConnectGatewayAssociation", "AWS::DirectConnect::PrivateVirtualInterface",
	"AWS::DirectConnect::TransitVirtualInterface", "AWS::DocDBElastic::ClusterSnapshot",
	"AWS::EC2::ApplicationStatusCheck", "AWS::EC2::CapacityReservationFleet", "AWS::EC2::HostReservation",
	"AWS::EC2::IPAM", "AWS::EC2::IPAMPoolCidr", "AWS::EC2::NetworkPerformanceMetricSubscription",
	"AWS::EC2::ReservedInstances", "AWS::EC2::RouteServerEndpoint", "AWS::EC2::SpotInstancesRequest",
	"AWS::EC2::TrafficMirrorSession", "AWS::EC2::VPCEncryptionControl", "AWS::EC2::VPNConcentrator",
	"AWS::EMR::InstanceFleetConfig", "AWS::EMRContainers::Endpoint", "AWS::EMRServerless::Application",
	"AWS::EVS::Environment", "AWS::FMS::Policy", "AWS::FinSpace::Environment", "AWS::FraudDetector::Detector",
	"AWS::Glue::DevEndpoint", "AWS::Glue::Session", "AWS::Grafana::Workspace", "AWS::HealthAgent::Subscription",
	"AWS::HealthImaging::Datastore", "AWS::ImageBuilder::Image", "AWS::ImageBuilder::ImagePipeline",
	"AWS::InspectorV2::CisScanConfiguration", "AWS::InspectorV2::CodeSecurityScanConfiguration",
	"AWS::InspectorV2::Connector", "AWS::Interconnect::Connection", "AWS::InternetMonitor::Monitor",
	"AWS::IoTSiteWise::Gateway", "AWS::IoTTwinMaker::Entity", "AWS::IoTTwinMaker::SyncJob", "AWS::Kendra::DataSource",
	"AWS::KendraRanking::ExecutionPlan", "AWS::Lambda::MicrovmImage", "AWS::LaunchWizard::Deployment",
	"AWS::Lightsail::Bucket", "AWS::Lightsail::Container", "AWS::Lightsail::DatabaseSnapshot", "AWS::Lightsail::Disk",
	"AWS::Lightsail::DiskSnapshot", "AWS::Lightsail::Distribution", "AWS::Lightsail::InstanceSnapshot",
	"AWS::Lightsail::LoadBalancer", "AWS::Lightsail::StaticIp", "AWS::LookoutEquipment::InferenceScheduler",
	"AWS::M2::Environment", "AWS::MSK::VpcConnection", "AWS::Macie::Member", "AWS::Macie::Session",
	"AWS::ManagedBlockchain::Member", "AWS::ManagedBlockchain::Node", "AWS::MediaConnect::Flow",
	"AWS::MediaConnect::FlowOutput", "AWS::MediaConnect::RouterInput", "AWS::MediaConnect::RouterOutput",
	"AWS::MediaLive::Input", "AWS::MediaLive::Multiplex", "AWS::MediaTailor::Channel",
	"AWS::NeptuneGraph::PrivateGraphEndpoint", "AWS::NetworkFlowMonitor::Monitor", "AWS::NetworkFlowMonitor::Scope",
	"AWS::NetworkManager::ConnectAttachment", "AWS::NetworkManager::DirectConnectGatewayAttachment",
	"AWS::NetworkManager::SiteToSiteVpnAttachment", "AWS::NetworkManager::TransitGatewayPeering",
	"AWS::NetworkManager::VpcAttachment", "AWS::NetworkMonitor::Monitor", "AWS::NetworkSecurityManager::Deployment",
	"AWS::NetworkSecurityManager::Policy", "AWS::ODB::CloudAutonomousVmCluster", "AWS::ODB::CloudExadataInfrastructure",
	"AWS::ODB::CloudVmCluster", "AWS::Omics::ReadSet", "AWS::OpenSearchServerless::CollectionGroup",
	"AWS::OpenSearchServerless::VpcEndpoint", "AWS::PCS::Cluster", "AWS::PCS::ComputeNodeGroup",
	"AWS::PaymentCryptography::Key", "AWS::Personalize::Campaign", "AWS::PricingPlanManager::Subscription",
	"AWS::QBusiness::Index", "AWS::RedshiftServerless::Snapshot", "AWS::RefactorSpaces::Application",
	"AWS::RefactorSpaces::Environment", "AWS::RefactorSpaces::Service", "AWS::ResilienceHub::App",
	"AWS::ResilienceHub::AppAssessment", "AWS::ResilienceHubV2::Service", "AWS::Route53GlobalResolver::GlobalResolver",
	"AWS::Route53Profiles::ProfileAssociation", "AWS::SES::DedicatedIpPool", "AWS::SES::MailManagerIngressPoint",
	"AWS::SES::Tenant", "AWS::SMSVOICE::PhoneNumber", "AWS::SMSVOICE::RcsAgent", "AWS::SMSVOICE::Registration",
	"AWS::SSMIncidents::ResponsePlan", "AWS::SSMQuickSetup::ConfigurationManager", "AWS::SageMaker::App",
	"AWS::SageMaker::Cluster", "AWS::SageMaker::FeatureGroup", "AWS::SageMaker::InferenceExperiment",
	"AWS::SageMaker::PartnerApp", "AWS::SageMaker::Space", "AWS::SavingsPlans::SavingsPlan",
	"AWS::ServiceCatalog::CloudFormationProvisionedProduct", "AWS::ServiceDiscovery::PrivateDnsNamespace",
	"AWS::ServiceDiscovery::PublicDnsNamespace", "AWS::StorageGateway::Gateway", "AWS::Timestream::InfluxDBBackup",
	"AWS::Timestream::InfluxDBCluster", "AWS::Timestream::ScheduledQuery",
	"AWS::VpcLattice::ServiceNetworkResourceAssociation", "AWS::WAF::Rule", "AWS::WAF::WebACL",
	"AWS::WAFRegional::RateBasedRule", "AWS::WAFRegional::Rule", "AWS::WAFRegional::WebACL",
	"AWS::WorkSpaces::WorkspacesPool", "AWS::WorkspacesInstances::Volume", "AWS::WorkspacesInstances::WorkspaceInstance",
}

func TestFreeIsExplicit(t *testing.T) {
	for _, f := range free {
		if strings.HasSuffix(f, "::") && f != "Custom::" {
			t.Errorf("free has prefix %s; list its free types one by one", f)
		}
	}
	for _, p := range append(paid, costsIdle...) {
		if freeType(p) {
			t.Errorf("%s costs money idle but is in free", p)
		}
	}
}
