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
	// reserved also records the 1-year, no-upfront standard Reserved Instance
	// rate of each key, under key + riSuffix (--pricing reserved-1y).
	reserved bool
}

// riSuffix marks the snapshot key of a Reserved Instance rate.
const riSuffix = "|ri1y"

// The services whose instances have Reserved Instances (or reserved nodes).
func init() {
	for i := range selectors {
		if in(selectors[i].name, "ec2", "rds", "es", "elasticache", "redshift", "memorydb") {
			selectors[i].reserved = true
		}
	}
}

func byInstanceType(detail string) map[string]string {
	return map[string]string{"instanceType": detail}
}

// byUsagetype narrows a --live query of a global offer, whose usagetypes carry
// no region prefix.
func byUsagetype(detail string) map[string]string {
	return map[string]string{"usagetype": detail}
}

func in(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

var rdsEngines = []string{"2", "14", "16", "18", "21", // mysql, postgres, aurora-mysql, mariadb, aurora-postgresql
	"5", "19", "20", // oracle-ee BYOL, oracle-se2 BYOL, oracle-se2 license included
	"10", "11", "12", "15", "51", "52", "53"} // sqlserver ex, web, se, ee (license included); dev-ee, se, ee (bring your own media)

var selectors = []selector{
	{name: "ec2", service: "AmazonEC2", families: []string{"Compute Instance", "Compute Instance (bare metal)"},
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
			return keep(u, "PublicIPv4:InUseAddress", "VpcEndpoint-Hours", "VpcEndpoint-GWLBE-Hours", "VPN-Usage-Hours:ipsec.1", "ClientVPN-EndpointHours", "VPCLattice-Service-Hourly",
				"VerifiedAccess-App-Hours", "VerifiedAccess-non-HTTP-App-Hours", "VPCEncryptionControls-hours",
				"ENI-Mirror", "RouteServer-EndpointHours", "VPN-concentrator-Usage-Hours:ipsec.1", "VPN-concentrator-site-Usage-Hours:ipsec.1",
				"PublicIPv4:ContiguousBlock", "VpcEndpoint-Resource-Hours", "VpcLattice-Service-Network-Resource-Hours")
		}},
	{name: "tgw", service: "AmazonVPC",
		key: func(a map[string]string, u string) string {
			if u == "TransitGateway-Hours" {
				return a["operation"] // TransitGatewayVPC, TransitGatewayPeering, ...
			}
			return ""
		}},
	{name: "elb", service: "AWSELB",
		key: func(a map[string]string, u string) string {
			if u == "LoadBalancerUsage" {
				return a["productFamily"]
			}
			return ""
		}},
	{name: "rds", service: "AmazonRDS", families: []string{"Database Instance", "Database Storage", "Provisioned IOPS", "Provisioned Throughput", "ServerlessV2", "Limitless"},
		key: func(a map[string]string, u string) string {
			if !in(a["engineCode"], rdsEngines...) {
				return ""
			}
			if a["productFamily"] == "Database Instance" {
				base, _, _ := strings.Cut(u, ":")
				if !in(base, "InstanceUsage", "Multi-AZUsage", "InstanceUsageIOOptimized", "Multi-AZClusterUsage", "MirrorUsage") {
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
			case strings.Contains(u, "Limitless"):
				f["productFamily"] = "Limitless"
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
			return keep(u, "ReadCapacityUnit-Hrs", "WriteCapacityUnit-Hrs", "IA-ReadCapacityUnit-Hrs", "IA-WriteCapacityUnit-Hrs",
				"ReplWriteCapacityUnit-Hrs", "IA-ReplWriteCapacityUnit-Hrs")
		}},
	{name: "kinesis", service: "AmazonKinesis", families: []string{"Kinesis Streams"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Storage-ShardHour", "Extended-ShardHour", "OnDemand-StreamHour", "EnhancedFanoutHour")
		}},
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
			return keep(u, "Fargate-vCPU-Hours:perCPU", "Fargate-GB-Hours", "Fargate-ARM-vCPU-Hours:perCPU", "Fargate-ARM-GB-Hours", "Fargate-EphemeralStorage-GB-Hours",
				"Fargate-Windows-vCPU-Hours:perCPU", "Fargate-Windows-OS-Hours:perCPU", "Fargate-Windows-GB-Hours")
		}},
	{name: "r53", service: "AmazonRoute53", global: true, families: []string{"DNS Zone"},
		key: func(a map[string]string, u string) string { return keep(u, "HostedZone") }},
	// Resolver endpoints are priced per region inside the global Route 53 offer.
	{name: "resolver", service: "AmazonRoute53", global: true, families: []string{"DNS Query", "DNS Domain Names"},
		key: func(a map[string]string, u string) string {
			return keep(u, "ResolverNetworkInterface", "AdvancedDNS-ThreatProtectionsVPCAssociationHours", "DNS-FirewallDomainName", "DNS-ProfileBasePackage")
		}},
	// Traffic Flow records and the CloudFront fees have no productFamily.
	{name: "r53tf", service: "AmazonRoute53", global: true,
		key:        func(a map[string]string, u string) string { return keep(u, "Traffic-Flow-Policy-Records") },
		liveFilter: byUsagetype},
	{name: "gr", service: "AmazonRoute53", global: true,
		key: func(a map[string]string, u string) string {
			return keep(u, "GlobalResolver-BaseRegionsWithFiltering", "GlobalResolver-BaseRegionsWithoutFiltering",
				"GlobalResolver-AdditionalRegionsWithFiltering", "GlobalResolver-AdditionalRegionsWithoutFiltering")
		},
		liveFilter: byUsagetype},
	{name: "cf", service: "AmazonCloudFront", global: true,
		key: func(a map[string]string, u string) string {
			return keep(u, "SSL-Cert-Custom", "Global-AnycastStaticIP-IPv4")
		},
		liveFilter: byUsagetype},
	{name: "cloudmap", service: "AWSCloudMap", families: []string{"AWS Cloud Map"},
		key: func(a map[string]string, u string) string { return keep(u, "Cloud-Map-Resources") }},
	{name: "firehose", service: "AmazonKinesisFirehose", families: []string{"Kinesis Firehose"},
		key: func(a map[string]string, u string) string { return keep(u, "Firehose-VpcDelivery-Hours") }},
	{name: "amplify", service: "AWSAmplify", families: []string{"AWS Amplify"},
		key: func(a map[string]string, u string) string { return keep(u, "AmplifyWAF") }},
	{name: "ga", service: "AWSGlobalAccelerator", global: true, families: []string{"AWS Global Accelerator"},
		key: func(a map[string]string, u string) string { return keep(u, "Global-Accelerator-fixed-fee") }},
	{name: "eks", service: "AmazonEKS", families: []string{"Compute"},
		key: func(a map[string]string, u string) string {
			return keep(u, "AmazonEKS-Hours:perCluster", "AmazonEKS-Hours:extendedSupport", "AmazonEKSCapabilities-ArgoCD-Hours:perCapability",
				"AmazonEKSCapabilities-ACK-Hours:perCapability", "AmazonEKSCapabilities-KRO-Hours:perCapability")
		}},
	{name: "nfw", service: "AWSNetworkFirewall", families: []string{"AWS Firewall"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Endpoint-Hour", "Secondary-Endpoint-Hour", "Advanced-Inspection-Endpoint-Hour")
		}},
	{name: "waf", service: "awswaf", families: []string{"Web Application Firewall"},
		key: func(a map[string]string, u string) string { return keep(u, "WebACLV2", "RuleV2", "WebACL", "Rule") }},
	{name: "cw", service: "AmazonCloudWatch", families: []string{"Alarm", "Canaries", "Contributor Insights", "Metric"},
		key: func(a map[string]string, u string) string {
			return keep(u, "CW:AlarmMonitorUsage", "CW:HighResAlarmMonitorUsage", "CW:CompositeAlarmMonitorUsage", "CW:Canary-runs", "CW:ContributorInsightRules", "CW:MetricMonitorUsage")
		}},
	{name: "lambda", service: "AWSLambda", families: []string{"Serverless"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Lambda-Provisioned-Concurrency", "Lambda-Provisioned-Concurrency-ARM", "Lambda-SnapStart-Cached-GB-S")
		}},
	{name: "lmi", service: "AWSLambda", families: []string{"Serverless"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Lambda-Managed-Instances-") && strings.HasSuffix(u, "-Management-Hours") {
				return a["lambdaManagedInstanceType"]
			}
			return ""
		},
		liveFilter: func(detail string) map[string]string { return map[string]string{"lambdaManagedInstanceType": detail} }},
	{name: "docdb", service: "AmazonDocDB", families: []string{"Database Instance", "Serverless", "Database Utilization"},
		key: func(a map[string]string, u string) string {
			return dbInstanceKey(a, u, "DocumentDB:ServerlessUsage", "DocumentDB:ServerlessIOOptimizedUsage", "ElasticCPUUsage")
		}},
	{name: "neptune", service: "AmazonNeptune", families: []string{"Database Instance", "Serverless"},
		key: func(a map[string]string, u string) string {
			return dbInstanceKey(a, u, "Neptune:ServerlessUsage", "Neptune:ServerlessIOOptimizedUsage")
		}},
	{name: "rdsproxy", service: "AmazonRDS", families: []string{"RDSProxy"},
		key: func(a map[string]string, u string) string { return keep(u, "RDS:ProxyUsage", "RDS:Proxy-ASv2-Usage") }},
	{name: "aoss", service: "AmazonES", families: []string{"Amazon OpenSearch Service Serverless"},
		key: func(a map[string]string, u string) string { return keep(u, "IndexingOCU", "SearchOCU") }},
	{name: "memorydb", service: "AmazonMemoryDB", families: []string{"Amazon MemoryDB"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "NodeUsage:") {
				return a["engine"] + "|" + a["instanceType"]
			}
			return ""
		}},
	{name: "sagemaker", service: "AmazonSageMaker", families: []string{"ML Instance"}, filters: map[string]string{"component": "Hosting"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Host:") {
				return a["instanceName"]
			}
			return ""
		},
		liveFilter: func(d string) map[string]string { return map[string]string{"instanceName": d} }},
	{name: "msk", service: "AmazonMSK", families: []string{"Managed Streaming for Apache Kafka (MSK)"},
		key: func(a map[string]string, u string) string {
			if (strings.HasPrefix(u, "Kafka.") || strings.HasPrefix(u, "Express.")) && !strings.Contains(u, "PrivateConnectivity") || in(u, "KafkaServerless-ClusterHours", "KafkaReplication-ReplicatorHours") {
				return u
			}
			return ""
		}},
	{name: "redshift", service: "AmazonRedshift", families: []string{"Compute Instance"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Node:") {
				return strings.TrimPrefix(u, "Node:")
			}
			return ""
		}},
	{name: "dax", service: "AmazonDAX", families: []string{"DAX"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "NodeUsage:") {
				return strings.TrimPrefix(u, "NodeUsage:")
			}
			return ""
		}},
	{name: "mq", service: "AmazonMQ", families: []string{"Broker Instances"},
		key: func(a map[string]string, u string) string {
			return a["brokerEngine"] + "|" + a["deploymentOption"] + "|" + a["instanceType"]
		}},
	{name: "transfer", service: "AWSTransfer", families: []string{"AWS Transfer Family"},
		key: func(a map[string]string, u string) string { return keep(u, "ProtocolHours", "WebAppHours") }},
	{name: "codepipeline", service: "AWSCodePipeline", families: []string{"Active Pipeline"},
		key: func(a map[string]string, u string) string { return keep(u, "activePipeline") }},
	{name: "apprunner", service: "AWSAppRunner", families: []string{"Compute"},
		key: func(a map[string]string, u string) string { return keep(u, "AppRunner-Provisioned-GB-hours") }},
	// cacheMemorySizeGb is the CacheClusterSize value (1.6), not the usagetype's (1.55GB).
	{name: "apigw", service: "AmazonApiGateway", families: []string{"Amazon API Gateway Cache"},
		key: func(a map[string]string, u string) string { return "cache:" + a["cacheMemorySizeGb"] }},
	{name: "ds", service: "AWSDirectoryService", families: []string{"AWS Directory Service"},
		key: func(a map[string]string, u string) string {
			return keep(u, "MicrosoftAD-DC-Usage", "Std-MicrosoftAD-DC-Usage", "Small-SimpleAD-Usage", "Large-SimpleAD-Usage")
		}},
	{name: "pca", service: "AWSCertificateManager", families: []string{"AWS Certificate Manager"},
		key: func(a map[string]string, u string) string {
			return keep(u, "PaidPrivateCA", "ShortLivedCertificatePrivateCA")
		}},
	{name: "dms", service: "AWSDatabaseMigrationSvc", families: []string{"Replication Server"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "InstanceUsg:dms.") || strings.HasPrefix(u, "Multi-AZUsg:dms.") {
				return u
			}
			return ""
		}},
	{name: "notebook", service: "AmazonSageMaker", families: []string{"ML Instance", "Storage"},
		key: func(a map[string]string, u string) string {
			if v, ok := strings.CutPrefix(u, "Notebk:"); ok && (a["component"] == "Notebook" || v == "VolumeUsage.gp2") {
				return v
			}
			return ""
		},
		liveFilter: func(d string) map[string]string {
			if strings.HasPrefix(d, "ml.") {
				return map[string]string{"productFamily": "ML Instance", "component": "Notebook", "instanceName": d}
			}
			return map[string]string{"productFamily": "Storage"}
		}},
	{name: "osis", service: "AmazonES", families: []string{"Amazon OpenSearch Service Ingestion"},
		key: func(a map[string]string, u string) string { return keep(u, "IngestionOCU") }},
	{name: "ssm", service: "AWSSystemsManager", families: []string{"AWS Systems Manager"},
		key: func(a map[string]string, u string) string {
			return keep(u, "PS-Advanced-Param-Tier1", "AppConfig-ExperimentHours", "IM-ResponsePlan-Months-Tier1")
		}},
	{name: "mcs", service: "AmazonMCS", families: []string{"Provisioned IOPS"},
		key: func(a map[string]string, u string) string {
			return keep(u, "ReadCapacityUnit-Hrs", "WriteCapacityUnit-Hrs")
		}},
	// MWAA products have no productFamily.
	{name: "mwaa", service: "AmazonMWAA",
		key: func(a map[string]string, u string) string {
			v, ok := strings.CutPrefix(u, "Airflow-")
			if ok && (strings.HasSuffix(v, "Environment") || strings.HasSuffix(v, "Worker") || strings.HasSuffix(v, "Scheduler") || strings.HasSuffix(v, "WebServer")) {
				return v
			}
			return ""
		}},
	{name: "appsync", service: "AWSAppSync", families: []string{"API Calls"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "AppSyncCacheUsage:") {
				return "cache:" + a["cachememorysize"]
			}
			return ""
		}},
	{name: "emr", service: "ElasticMapReduce", families: []string{"Elastic Map Reduce Instance"}, filters: map[string]string{"softwareType": "EMR"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "BoxUsage:") {
				return a["instanceType"]
			}
			return ""
		},
		liveFilter: byInstanceType},
	{name: "healthlake", service: "AmazonHealthLake", families: []string{"Machine Learning"},
		key: func(a map[string]string, u string) string { return keep(u, "FHIRDataStore") }},
	// Application Recovery Controller has one offer, for the "aws-other" location.
	{name: "arc", service: "AWSR53AppRecoveryController", global: true, families: []string{"Application Recovery Controller"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Global-Cluster-Hours", "Global-ReadinessCheck-Hours", "Global-AmazonARCRegionSwitchPlan")
		}},
	{name: "kda", service: "AmazonKinesisAnalytics", families: []string{"Kinesis Analytics"},
		key: func(a map[string]string, u string) string {
			return keep(u, "KPU-Hour-Java", "KPU-Hour-Interactive", "RunningApplicationStorage", "RunningApplicationStorage-Interactive")
		}},
	// Core network edges are priced per region inside one global offer.
	{name: "wan", service: "AWSCloudWAN", global: true,
		key: func(a map[string]string, u string) string {
			if in(u, "CloudWAN-Attachment-Hours", "CloudWAN-Peering-Hours") {
				return a["operation"] // CloudWANVPC, CloudWANConnect, CloudWANTGWPeering, ...
			}
			return keep(u, "CloudWAN-CoreNetworkEdge-Hours")
		}},
	{name: "r53hc", service: "AmazonRoute53", global: true, families: []string{"DNS Health Check"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Health-Check-AWS", "Health-Check-Non-AWS", "Health-Check-Option-AWS", "Health-Check-Option-Non-AWS")
		}},
	// KendraEnterpriseEdition ($7/h) is the retired price; Kendra-Enterprise-Edition is the current one.
	{name: "kendra", service: "AmazonKendra",
		key: func(a map[string]string, u string) string {
			return keep(u, "KendraDeveloperEdition", "Kendra-Enterprise-Edition", "Kendra-Query-Capacity-Unit", "Kendra-Storage-Capacity-Unit",
				"Kendra-GenAI-Ent-Base-Capacity", "Kendra-GenAI-Ent-Addl-QueryCap", "Kendra-GenAI-Ent-Addl-StorageCap",
				"KendraIntelligentRanking-BaseCapacity", "KendraIntelligentRanking-CapacityUnits", "Kendra-Connector-MonthlyFee")
		}},
	{name: "mlflow", service: "AmazonSageMaker", families: []string{"Amazon SageMaker"}, filters: map[string]string{"operation": "RunTrackingServer"},
		key: func(a map[string]string, u string) string {
			if v, ok := strings.CutPrefix(u, "MLflow:TrackingServerCompute-"); ok {
				return v
			}
			return ""
		}},
	{name: "graph", service: "AmazonNeptune", families: []string{"Neptune Memory Optimized Graph"},
		key: func(a map[string]string, u string) string {
			if v, ok := strings.CutPrefix(u, "MemoryOptimizedGraphUsage:"); ok {
				return strings.TrimSuffix(v, "-m-NCU")
			}
			return ""
		}},
	{name: "gamelift", service: "AmazonGameLift", families: []string{"GameLift EC2 Instance"},
		key: func(a map[string]string, u string) string {
			if os, ok := strings.CutPrefix(a["operation"], "Consume"); ok && strings.HasPrefix(u, "BoxUsage:") {
				if os, ok := strings.CutSuffix(os, "GameLiftInstance"); ok {
					return os + "|" + a["instanceType"]
				}
				if os, ok := strings.CutSuffix(os, "FleetIQOnDemand"); ok { // the FleetIQ fee, on top of EC2
					return os + "FleetIQ|" + a["instanceType"]
				}
			}
			return ""
		},
		liveFilter: func(d string) map[string]string {
			_, t, _ := strings.Cut(d, "|")
			return map[string]string{"instanceType": t}
		}},
	// Reserved fleets of the predefined g1 (and Mac m2) sizes, priced per minute.
	{name: "codebuild", service: "CodeBuild", families: []string{"Compute"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Build-Min:") && strings.Contains(u, ":Reserved:") && (strings.Contains(u, ".g1.") || strings.Contains(u, "arm.m2.")) {
				return strings.TrimPrefix(u, "Build-Min:")
			}
			return ""
		}},
	// Windows image builders (the plain "-ib" usagetype), keyed by instance type.
	{name: "appstream", service: "AmazonAppStream", families: []string{"Streaming Instance"},
		key: func(a map[string]string, u string) string {
			if a["instanceType"] != "" && u == a["instanceType"]+"-ib" {
				return a["instanceType"]
			}
			return ""
		},
		liveFilter: byInstanceType},
	// WorkSpaces Core managed instances, hourly, Linux on shared tenancy (EC2 included).
	{name: "wsi", service: "AmazonWorkSpacesInstances", families: []string{"WorkSpaces Core"},
		filters: map[string]string{"operation": "RunInstances", "billingoption": "Hourly", "tenancy": "Shared"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "BoxUsage:") {
				return a["instanceType"]
			}
			return ""
		},
		liveFilter: byInstanceType},
	{name: "pcs", service: "AWSPCS",
		key: func(a map[string]string, u string) string {
			if strings.HasSuffix(u, ":RunningHours") {
				return u
			}
			return ""
		}},
	{name: "evs", service: "AmazonEVS",
		key: func(a map[string]string, u string) string { return keep(u, "EVSUsage") }},
	{name: "m2", service: "AWSM2",
		key: func(a map[string]string, u string) string {
			if strings.HasSuffix(u, "BluAgeRuntime") || strings.HasSuffix(u, "EnterpriseServer") {
				return u
			}
			return ""
		}},
	// Idle charges; usagetypes carry two prefixes (EML2-USE1-IN-IDLE), so key by operation.
	{name: "medialive", service: "AWSElementalMediaLive",
		key: func(a map[string]string, u string) string {
			return keep(a["operation"], "INACTIVE_INPUT", "INACTIVE_MULTIPLEX")
		}},
	{name: "paycrypto", service: "PaymentCryptography", families: []string{"Encryption Key"},
		key: func(a map[string]string, u string) string { return keep(u, "ActiveKeys") }},
	{name: "b2bi", service: "AWSB2Bi",
		key: func(a map[string]string, u string) string { return keep(u, "PartnershipMonths") }},
	{name: "billingconductor", service: "AWSBillingConductor",
		key: func(a map[string]string, u string) string { return keep(u, "Proforma-Accounts") }},
	{name: "lightsail", service: "AmazonLightsail",
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "ContainerSvcUsage:") && !strings.HasSuffix(u, "-Free") {
				return u
			}
			return keep(u, "LoadBalancerUsage", "DiskUsage", "UnusedStaticIP", "Storage-BundleUsage:5GB", "Storage-BundleUsage:100GB",
				"Storage-BundleUsage:250GB", "CDN-BundleUsage:50GB", "CDN-BundleUsage:200GB", "CDN-BundleUsage:500GB")
		}},
	// Budgets has one offer, for the "aws-other" location.
	{name: "budgets", service: "AWSBudgets", global: true, families: []string{"AWS Budgets"},
		key: func(a map[string]string, u string) string { return keep(u, "ActionEnabledBudgetsUsage") }},
	{name: "fms", service: "AWSFMS", families: []string{"Firewall Manager Policy"},
		key: func(a map[string]string, u string) string { return keep(u, "Policy") }},
	{name: "nsm", service: "AWSNSM",
		key: func(a map[string]string, u string) string { return keep(u, "Usage-Trigger-Policy") }},
	// The Pro- and Enterprise- plan variants are separate usagetypes.
	{name: "ses", service: "AmazonSES",
		key: func(a map[string]string, u string) string {
			return keep(u, "IngressPoint-Subscription", "DIP-Managed-Acc-Sub-Hours", "Tenant-Count")
		}},
	{name: "refactor", service: "AWSMigrationHubRefactorSpaces", families: []string{"AWS Migration Hub"},
		key: func(a map[string]string, u string) string { return keep(u, "EnvironmentHours") }},
	// Ports carry their location code (TLXA1-PortUsage:1G); the locations of a
	// region share one price.
	{name: "dx", service: "AWSDirectConnect", families: []string{"Direct Connect"},
		key: func(a map[string]string, u string) string {
			_, u, _ = strings.Cut(u, "-")
			if strings.HasPrefix(u, "PortUsage:") || u == "SiteLinkVifHour" {
				return u
			}
			return ""
		}},
	// Usagetypes carry the partner (GCP-MgdConUsage:1G-Tier1); partners share one price.
	{name: "interconnect", service: "AWSInterconnect",
		key: func(a map[string]string, u string) string {
			if _, u, ok := strings.Cut(u, "-MgdConUsage:"); ok {
				return u
			}
			return ""
		}},
	// Internet Monitor has no productFamily.
	{name: "im", service: "AmazonCloudWatch",
		key: func(a map[string]string, u string) string { return keep(u, "CW:InternetMonitor-MonitoredResource") }},
	{name: "influx", service: "AmazonTimestream", families: []string{"Database Instance", "Influx Optimized Storage"},
		key: func(a map[string]string, u string) string {
			if strings.Contains(u, "Db.influx") || strings.Contains(u, "InfluxDBStorage") {
				return u
			}
			return ""
		}},
	{name: "grafana", service: "AmazonGrafana", families: []string{"User Fees"},
		key: func(a map[string]string, u string) string { return keep(u, "Grafana:EditorUser") }},
	// Global-Inference-Hours-L4E is the free tier; keep only the regional row.
	{name: "l4e", service: "AmazonLookoutEquipment", families: []string{"Machine Learning"},
		key: func(a map[string]string, u string) string { return keep(u, "Inference-Hours-L4E") }},
	{name: "blockchain", service: "AmazonManagedBlockchain", families: []string{"Blockchain Member", "Blockchain Instance"},
		key: func(a map[string]string, u string) string {
			if u == "Member" {
				return u + "|" + a["operation"] // Fabric:Standard, Fabric:Starter
			}
			if strings.HasPrefix(u, "Node:") {
				return u
			}
			return ""
		}},
	{name: "datapipeline", service: "datapipeline", families: []string{"Inactive Pipeline"},
		key: func(a map[string]string, u string) string { return keep(u, "InactivePipelines") }},
	{name: "healthimaging", service: "AmazonMedicalImaging", families: []string{"Storage"},
		key: func(a map[string]string, u string) string { return keep(u, "DataStore-Hours") }},
	{name: "comprehend", service: "comprehend", families: []string{"Custom Classification", "Custom Entities"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Endpoint-Time", "DocClassifier-Storage", "EntityRecognizer-Storage")
		}},
	{name: "athena", service: "AmazonAthena", families: []string{"Athena Queries"},
		key: func(a map[string]string, u string) string { return keep(u, "ReservedCapacityInDPUHours") }},
	{name: "qbusiness", service: "AmazonQ", families: []string{"Amazon Q"},
		key: func(a map[string]string, u string) string {
			return keep(u, "Q-Business-Starter-Index", "Q-Business-Enterprise-Index")
		}},
	{name: "amp", service: "AmazonPrometheus", families: []string{"Metric"},
		key: func(a map[string]string, u string) string { return keep(u, "AMP:CollectorHours") }},
	{name: "glue", service: "AWSGlue", families: []string{"AWS Glue"},
		key: func(a map[string]string, u string) string { return keep(u, "DEVED-DPU-Hour") }},
	{name: "resiliencehub", service: "AWSResilienceHub", families: []string{"AWS Resilience Hub"},
		key: func(a map[string]string, u string) string { return keep(u, "PaidTierApplication") }},
	{name: "appsignals", service: "AmazonCloudWatch", families: []string{"Application Signals"},
		key: func(a map[string]string, u string) string { return keep(u, "Application-Signals") }},
	{name: "agentregistry", service: "awsagentregistry", families: []string{"Amazon Bedrock"},
		key: func(a map[string]string, u string) string { return keep(u, "Registry:Consumption-based:Records") }},
	{name: "agentcore", service: "AmazonBedrockAgentCore", families: []string{"Amazon Bedrock"},
		key: func(a map[string]string, u string) string { return keep(u, "Gateway:Consumption-based:Tool-Indexing") }},
	{name: "finspace", service: "AmazonFinSpace", families: []string{"FinSpace"},
		key: func(a map[string]string, u string) string { return keep(u, "StandardUser") }},
	{name: "accessanalyzer", service: "AWSIAMAccessAnalyzer", families: []string{"Findings"},
		key: func(a map[string]string, u string) string { return keep(u, "AccessFinding-Monitored-AWS-Resources") }},
	{name: "emrserverless", service: "ElasticMapReduce", families: []string{"EMR Serverless"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "EMR-SERVERLESS-") {
				return strings.TrimPrefix(u, "EMR-SERVERLESS-") // vCPUHours, ARM-vCPUHours, MemoryGBHours, ...
			}
			return ""
		}},
	// SageMaker Studio apps (Studio:KernelGateway-ml.t3.medium, RStudio:RSession-…,
	// Canvas:Session-Hrs) and the EBS volume of a space (Studio:VolumeUsage.gp3).
	{name: "studio", service: "AmazonSageMaker", families: []string{"ML Instance", "Storage", "AutoML Jobs"},
		key: func(a map[string]string, u string) string {
			if strings.HasPrefix(u, "Studio:KernelGateway-") || strings.HasPrefix(u, "RStudio:") || in(u, "Canvas:Session-Hrs", "Studio:VolumeUsage.gp3") {
				return u
			}
			return ""
		},
		liveFilter: sageMakerUsage},
	// HyperPod instances (Cluster:ml.m5.large; the -Reserved rows are training
	// plans) and their extra EBS volumes (Cluster:VolumeUsage.gp2).
	{name: "hyperpod", service: "AmazonSageMaker", families: []string{"ML Instance", "Storage"},
		key: func(a map[string]string, u string) string {
			if v, ok := strings.CutPrefix(u, "Cluster:"); ok && !strings.HasSuffix(v, "-Reserved") {
				return v
			}
			return ""
		},
		liveFilter: func(d string) map[string]string { return sageMakerUsage("Cluster:" + d) }},
	{name: "featurestore", service: "AmazonSageMaker", families: []string{"FeatureStore Storage", "FeatureStore PayPerRequestThroughput"},
		key: func(a map[string]string, u string) string {
			return keep(u, "FeatureStore:InMemoryStorage", "FeatureStore:ReadCapacityUnit-Hours", "FeatureStore:WriteCapacityUnit-Hours")
		}},
}

// sageMakerUsage narrows a --live query of a SageMaker usagetype to the
// products of its instance type (the instanceType attribute carries a suffix,
// ml.m5.large-Cluster, so instanceName is used), or to its family.
func sageMakerUsage(u string) map[string]string {
	if i := strings.Index(u, "ml."); i >= 0 {
		return map[string]string{"productFamily": "ML Instance", "instanceName": u[i:]}
	}
	if strings.HasPrefix(u, "Canvas:") {
		return map[string]string{"productFamily": "AutoML Jobs"}
	}
	return map[string]string{"productFamily": "Storage"}
}

// dbInstanceKey keys RDS-like instance prices by instanceType (usagetype
// abbreviates xlarge to xl), and keeps the listed serverless usagetypes.
func dbInstanceKey(a map[string]string, u string, serverless ...string) string {
	base, _, _ := strings.Cut(u, ":")
	if in(base, "InstanceUsage", "InstanceUsageIOOptimized") && a["instanceType"] != "" {
		return base + ":" + a["instanceType"]
	}
	return keep(u, serverless...)
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
	TermAttributes  map[string]string         `json:"termAttributes"`
	PriceDimensions map[string]priceDimension `json:"priceDimensions"`
}

type priceDimension struct {
	Unit         string            `json:"unit"`
	PricePerUnit map[string]string `json:"pricePerUnit"`
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

// productRegion is where a product's price belongs: its regionCode, or the
// offer's region when it has none (global offers). Products of Local Zones and
// other non-region locations are dropped (ok=false) so they never overwrite a
// region's price under the same key.
func productRegion(p offerProduct, offerRegion string) (string, bool) {
	r := p.Attributes["regionCode"]
	if r == "" {
		return offerRegion, true
	}
	return r, regionCode.MatchString(r)
}

// record keeps the highest price seen for a key (several SKUs can share one).
func record(out map[string]map[string]float64, region, key string, terms map[string]offerTerm) {
	v, ok := price(terms)
	if !ok {
		return
	}
	if out[region] == nil {
		out[region] = map[string]float64{}
	}
	if old, seen := out[region][key]; !seen || v > old {
		out[region][key] = v
	}
}

// recordReserved keeps the hourly rate of the 1-year, no-upfront standard
// Reserved Instance term under key + riSuffix.
func recordReserved(out map[string]map[string]float64, region, key string, terms map[string]offerTerm) {
	for _, t := range terms {
		a := t.TermAttributes
		if a["LeaseContractLength"] != "1yr" || a["PurchaseOption"] != "No Upfront" || !in(a["OfferingClass"], "", "standard") {
			continue
		}
		hourly := offerTerm{PriceDimensions: map[string]priceDimension{}} // not the upfront Quantity, if any
		for id, d := range t.PriceDimensions {
			if d.Unit == "Hrs" {
				hourly.PriceDimensions[id] = d
			}
		}
		record(out, region, key+riSuffix, map[string]offerTerm{"": hourly})
	}
}

// parseOffer streams one bulk offer file (EC2 is ~450 MB per region) and adds
// the selected prices to out (region -> key -> price).
func parseOffer(r io.Reader, sels []selector, offerRegion string, out map[string]map[string]float64) (pub string, err error) {
	dec := json.NewDecoder(r)
	keys := map[string][2]string{} // sku -> region, key
	reserved := map[string]bool{}  // skus whose Reserved terms are kept
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
				reg, ok := productRegion(p, offerRegion)
				if !ok {
					return nil
				}
				for _, s := range sels {
					if k := s.match(p); k != "" {
						keys[sku] = [2]string{reg, k}
						reserved[sku] = s.reserved
						break
					}
				}
				return nil
			})
		case "terms":
			err = eachEntry(dec, func(termType string) error {
				if termType != "OnDemand" && termType != "Reserved" {
					var skip json.RawMessage
					return dec.Decode(&skip)
				}
				return eachEntry(dec, func(sku string) error {
					rk, ok := keys[sku]
					if !ok || termType == "Reserved" && !reserved[sku] {
						var skip json.RawMessage
						return dec.Decode(&skip)
					}
					var terms map[string]offerTerm
					if err := dec.Decode(&terms); err != nil {
						return err
					}
					if termType == "Reserved" {
						recordReserved(out, rk[0], rk[1], terms)
					} else {
						record(out, rk[0], rk[1], terms)
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

// BuildPrices downloads the bulk offers of every service the selectors use (or
// only of services), for every commercial region (or only regions), and returns
// the snapshot.
func BuildPrices(regions, only []string, logf func(string, ...any)) (*Prices, error) {
	byService := map[string][]selector{}
	var services []string
	for _, s := range selectors {
		if len(only) > 0 && !in(s.service, only...) {
			continue
		}
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
			out := map[string]map[string]float64{}
			pub, err := fetchOffer(j.url, byService[j.service], j.region, out)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s %s: %w", j.service, j.region, err)
				}
				return
			}
			n := 0
			for r, prices := range out {
				if r != "global" && len(regions) > 0 && !in(r, regions...) {
					continue
				}
				if p.Regions[r] == nil {
					p.Regions[r] = map[string]float64{}
				}
				for k, v := range prices {
					if old, seen := p.Regions[r][k]; !seen || v > old {
						p.Regions[r][k] = v
					}
					n++
				}
			}
			logf("%-18s %-16s %5d prices", j.service, j.region, n)
			if d := pub[:min(10, len(pub))]; d > p.AsOf {
				p.AsOf = d
			}
		}(j)
	}
	wg.Wait()
	return p, firstErr
}

func fetchOffer(url string, sels []selector, offerRegion string, out map[string]map[string]float64) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return parseOffer(resp.Body, sels, offerRegion, out)
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
