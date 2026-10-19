package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// testPrices are fixed so the golden files don't move with the weekly snapshot.
var testPrices = &Prices{Source: "test prices", AsOf: "2026-10-16", Regions: map[string]map[string]float64{
	"ap-northeast-1": {
		"nat|NatGateway-Hours":              0.062,
		"vpc|PublicIPv4:InUseAddress":       0.005,
		"vpc|VpcEndpoint-Hours":             0.014,
		"ec2|t3.micro":                      0.0136,
		"ec2|t3.small":                      0.0272,
		"ebs|EBS:VolumeUsage.gp3":           0.096,
		"ebs|EBS:VolumeP-IOPS.gp3":          0.006,
		"ebs|EBS:VolumeP-Throughput.gp3":    0.048,
		"ddb|ReadCapacityUnit-Hrs":          0.0001484,
		"ddb|WriteCapacityUnit-Hrs":         0.000742,
		"kms|KMS-Keys":                      1,
		"secrets|AWSSecretsManager-Secrets": 0.4,
		"elb|Load Balancer-Application":     0.0243,
		"kinesis|Storage-ShardHour":         0.0195,
	},
	"eu-west-1": {
		"rdsproxy|RDS:ProxyUsage": 0.015, "rdsproxy|RDS:Proxy-ASv2-Usage": 0.015, "rds|InstanceUsage:db.r6g.large|16": 0.276,
		"rds|Aurora:ServerlessV2Usage|16": 0.13, "aoss|IndexingOCU": 0.24, "aoss|SearchOCU": 0.24,
		"memorydb|Redis|db.t4g.small": 0.048, "sagemaker|ml.m5.large": 0.128,
		"eks|AmazonEKS-Hours:perCluster": 0.1, "ec2|t3.medium": 0.0456, "nfw|Endpoint-Hour": 0.395,
		"waf|WebACLV2": 5, "waf|RuleV2": 1, "cw|CW:AlarmMonitorUsage": 0.1, "cw|CW:HighResAlarmMonitorUsage": 0.3,
		"lambda|Lambda-Provisioned-Concurrency-ARM": 0.0000033334, "tgw|TransitGatewayVPC": 0.05, "tgw|TransitGatewayVPN": 0.05,
		"vpc|VPN-Usage-Hours:ipsec.1": 0.05, "resolver|ResolverNetworkInterface": 0.125,
		"docdb|InstanceUsageIOOptimized:db.t4g.medium": 0.0975, "msk|Kafka.m5.large": 0.226, "msk|Kafka.Storage.GP2": 0.11,
		"mq|RabbitMQ|Multi-AZ|m5.large": 0.906, "transfer|ProtocolHours": 0.3, "codepipeline|activePipeline": 1,
	},
	"us-east-1": {"rds|Aurora:ServerlessV2IOOptimizedUsage|21": 0.16, "kms|KMS-Keys": 1, "ebs|EBS:VolumeUsage.gp3": 0.08, "ebs|EBS:VolumeP-IOPS.gp3": 0.005, "secrets|AWSSecretsManager-Secrets": 0.4,
		"ec2|t3.micro": 0.0104, "vpc|PublicIPv4:InUseAddress": 0.005, "fargate|Fargate-vCPU-Hours:perCPU": 0.04048, "fargate|Fargate-GB-Hours": 0.004445,
		"fsx|ONTAP|Storage.SAZ_2N:SSD": 0.125, "fsx|ONTAP|ThroughputCapacity.SAZ_2N": 0.72, "fsx|ONTAP|Storage.SAZ_2N2:SSD": 0.125,
		"fsx|ONTAP|ThroughputCapacity.SAZ_2N2": 1.6, "fsx|ONTAP|ProvisionedSSDIOPS.SAZ_2N2": 0.017,
		"ddb|ReadCapacityUnit-Hrs": 0.00013, "ddb|ReplWriteCapacityUnit-Hrs": 0.00065, "ddb|IA-ReadCapacityUnit-Hrs": 0.00016, "ddb|IA-WriteCapacityUnit-Hrs": 0.00081,
		"apigw|cache:0.5": 0.02, "apigw|cache:6.1": 0.2, "ds|Std-MicrosoftAD-DC-Usage": 0.06, "ds|MicrosoftAD-DC-Usage": 0.2, "ds|Small-SimpleAD-Usage": 0.05,
		"elb|Load Balancer-Application": 0.0225,
		"pca|PaidPrivateCA":             400, "pca|ShortLivedCertificatePrivateCA": 50, "dms|Multi-AZUsg:dms.t3.medium": 0.149,
		"notebook|ml.t3.medium": 0.05, "notebook|VolumeUsage.gp2": 0.14, "osis|IngestionOCU": 0.24, "cw|CW:Canary-runs": 0.0012,
		"kinesis|Storage-ShardHour": 0.015, "kinesis|EnhancedFanoutHour": 0.015, "msk|Kafka.mcu.general": 0.11,
		"msk|KafkaReplication-ReplicatorHours": 0.3, "ssm|PS-Advanced-Param-Tier1": 0.00007,
		"mcs|ReadCapacityUnit-Hrs": 0.00013, "mcs|WriteCapacityUnit-Hrs": 0.00065,
		"mwaa|SmallEnvironment": 0.49, "mwaa|SmallWorker": 0.055, "healthlake|FHIRDataStore": 0.27, "appsync|cache:1.55": 0.044,
		"vpc|VPCLattice-Service-Hourly": 0.025, "docdb|ElasticCPUUsage": 0.132,
		"influx|MultiAZUsage-Db.influx.medium": 0.24, "influx|MultiAZ-InfluxDBStorage-InfluxIOIncludedT1": 0.2,
		"rds|Multi-AZClusterUsage:db.m5d.large|14": 0.585, "rds|RDS:Multi-AZCluster-PIOPS-Storage|14": 0.375, "rds|RDS:Multi-AZCluster-PIOPS|14": 0.3,
		"fargate|Fargate-Windows-vCPU-Hours:perCPU": 0.046552, "fargate|Fargate-Windows-OS-Hours:perCPU": 0.046, "fargate|Fargate-Windows-GB-Hours": 0.00511175,
		"kinesis|Extended-ShardHour": 0.02, "vpc|VpcEndpoint-Hours": 0.01, "rds|InstanceUsage:db.t4g.micro|14": 0.016, "rds|RDS:GP3-Storage|14": 0.115,
		"kda|KPU-Hour-Java": 0.11, "kda|KPU-Hour-Interactive": 0.11, "kda|RunningApplicationStorage": 0.1, "kda|RunningApplicationStorage-Interactive": 0.1,
		"wan|CloudWAN-CoreNetworkEdge-Hours": 0.5,
		"kendra|KendraDeveloperEdition":      1.125, "kendra|Kendra-Enterprise-Edition": 1.4, "kendra|Kendra-Query-Capacity-Unit": 0.7, "kendra|Kendra-Storage-Capacity-Unit": 0.7,
		"vpc|VerifiedAccess-App-Hours": 0.27, "vpc|VerifiedAccess-non-HTTP-App-Hours": 0.2, "cw|CW:ContributorInsightRules": 0.5,
		"mlflow|Small": 0.642, "graph|16": 0.48, "gamelift|Linux|c5.large": 0.109, "gamelift|Windows|c5.large": 0.201,
		"es|r6g.large.search": 0.167, "es|ES:GP3-Storage": 0.122, "es|ES:GP3-Provisioned-ThroughPut": 0.064, "docdb|DocumentDB:ServerlessIOOptimizedUsage": 0.0905},
	"ap-southeast-1": {"rds|InstanceUsage:db.t3.small|10": 0.044, "rds|MirrorUsage:db.m5.large|12": 1.954, "rds|RDS:Mirror-GP3-Storage|12": 0.23,
		"rds|InstanceUsage:db.m5.large|20": 0.438, "rds|RDS:GP3-Storage|20": 0.115, "rds|RDS:GP3-Storage|10": 0.115,
		"ec2|m5.xlarge": 0.192, "emr|m5.xlarge": 0.048, "ebs|EBS:VolumeUsage.gp2": 0.1,
		"eks|AmazonEKS-Hours:perCluster": 0.1, "eks|AmazonEKS-Hours:extendedSupport": 0.5},
	"global": {"arc|Global-Cluster-Hours": 2.5, "arc|Global-ReadinessCheck-Hours": 0.045,
		"r53hc|Health-Check-AWS": 0.5, "r53hc|Health-Check-Non-AWS": 0.75, "r53hc|Health-Check-Option-AWS": 1, "r53hc|Health-Check-Option-Non-AWS": 2},
}}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name + ".golden"
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (go test -update to accept)\n--- got\n%s--- want\n%s", name, got, want)
	}
}

func report(t *testing.T, region string, inputs ...string) string {
	t.Helper()
	stacks, err := LoadInputs(inputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stacks {
		if s.Region == "" {
			s.Region = region
		}
	}
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, testPrices), testPrices).WriteText(&b)
	return b.String()
}

// Multiple stacks, a Stage, a nested stack (via assets.json), construct paths
// from assembly metadata, a Parameter default, and the unresolvable cases.
func TestCloudAssembly(t *testing.T) {
	golden(t, "cdk.out", report(t, "us-east-1", "testdata/cdk.out"))
}

// YAML short-form intrinsics must stay unresolved: `!Ref DbClass` is a
// Parameter, not the instance class "DbClass".
func TestYAMLTemplate(t *testing.T) {
	out := report(t, "us-east-1", "testdata/template.yaml")
	golden(t, "template.yaml", out)
	if !strings.Contains(out, "DBInstanceClass is Parameter DbClass") {
		t.Error("!Ref was not kept as a Ref")
	}
}

// The rules added in 0.0.3, and a type listed as "not counted yet".
func TestMoreTypes(t *testing.T) {
	golden(t, "more.yaml", report(t, "eu-west-1", "testdata/more.yaml"))
}

// Auto-assigned public IPv4, FSx for ONTAP, provisioned global tables, API
// Gateway caches and Directory Service.
func TestRules(t *testing.T) {
	golden(t, "rules.yaml", report(t, "us-east-1", "testdata/rules.yaml"))
}

// The second batch: private CAs, DMS, notebooks, ingestion pipelines, capacity
// reservations, canaries, stream consumers, MSK Connect and Replicator, SSM
// advanced parameters, Keyspaces, MWAA, HealthLake, ARC, VPC Lattice, DocumentDB
// elastic clusters, Timestream for InfluxDB, Multi-AZ DB clusters and Windows on Fargate.
func TestRules2(t *testing.T) {
	golden(t, "rules2.yaml", report(t, "us-east-1", "testdata/rules2.yaml"))
}

// The third batch: Oracle and SQL Server on RDS, and EMR clusters.
func TestRules3(t *testing.T) {
	golden(t, "rules3.yaml", report(t, "ap-southeast-1", "testdata/rules3.yaml"))
}

// The fourth batch: Managed Flink, Cloud WAN, EC2 Fleet, Spot Fleet and Route 53 health checks.
func TestRules4(t *testing.T) {
	golden(t, "rules4.yaml", report(t, "us-east-1", "testdata/rules4.yaml"))
}

// The fifth batch: Kendra, Verified Access, Contributor Insights, MLflow,
// Neptune Analytics, GameLift and Batch.
func TestRules5(t *testing.T) {
	golden(t, "rules5.yaml", report(t, "us-east-1", "testdata/rules5.yaml"))
}

// Fixes from the 2026-10-17 review (see the comments in the template).
func TestReview(t *testing.T) {
	golden(t, "review.yaml", report(t, "us-east-1", "testdata/review.yaml"))
}

// A nested stack whose AWS::CloudFormation::Stack is not created has no cost.
func TestNestedStackCondition(t *testing.T) {
	parse := func(name, src string) *Stack {
		s, err := ParseTemplate(name, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		s.Name, s.Region = "App", "us-east-1"
		return s
	}
	root := parse("App.template.json", `{"Parameters": {"Env": {"Type": "String", "Default": "dev"}},
		"Conditions": {"IsProd": {"Fn::Equals": [{"Ref": "Env"}, "prod"]}},
		"Resources": {"Archive": {"Type": "AWS::CloudFormation::Stack", "Condition": "IsProd",
			"Metadata": {"aws:asset:path": "AppArchive.nested.template.json"}}}}`)
	nested := parse("cdk.out/AppArchive.nested.template.json", `{"Resources": {"Key": {"Type": "AWS::KMS::Key"}}}`)
	linkParents([]*Stack{root, nested})
	if items := Estimate([]*Stack{root, nested}, testPrices); len(items) != 0 {
		t.Errorf("Env=dev: got %v", items)
	}
	root.Parameters["Env"] = Param{Value: "prod", Given: true}
	if items := Estimate([]*Stack{root, nested}, testPrices); len(items) != 1 || items[0].Monthly != 1 {
		t.Errorf("Env=prod: got %v", items)
	}
}

func TestDuplicateTemplateNames(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"a", "b"} {
		os.Mkdir(dir+"/"+d, 0o755)
		os.WriteFile(dir+"/"+d+"/template.yaml", []byte("Resources: {}\n"), 0o644)
	}
	if _, err := LoadInputs([]string{dir + "/a/template.yaml", dir + "/b/template.yaml"}); err == nil {
		t.Error("two templates named template were loaded as one stack")
	}
}

// --format json rounds the totals to cents; --max must still see $10.004.
func TestBudgetBeforeRounding(t *testing.T) {
	p, err := BundledPrices()
	if err != nil {
		t.Fatal(err)
	}
	gb, _ := p.Get("us-east-1", "ebs|EBS:VolumeUsage.gp3")
	path := t.TempDir() + "/vol.yaml"
	os.WriteFile(path, []byte(fmt.Sprintf("Resources: {Vol: {Type: AWS::EC2::Volume, Properties: {VolumeType: gp3, Size: %v}}}\n", 10.004/gb)), 0o644)
	for _, f := range []string{"text", "json"} {
		err := run([]string{path, "--region", "us-east-1", "--max", "10", "--format", f}, io.Discard)
		if _, ok := err.(overBudget); !ok {
			t.Errorf("--format %s: err = %v, want over budget", f, err)
		}
	}
}

// --parameter turns the Condition on: ProdKey is created and Spare is an m5.large.
func TestParameter(t *testing.T) {
	var b bytes.Buffer
	if err := run([]string{"testdata/cdk.out", "--region", "us-east-1", "--parameter", "App:Env=prod", "--parameter", "WorkerType=t3.nano"}, &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ProdKey", "m5.large", "t3.nano"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %s in\n%s", want, b.String())
		}
	}
	if err := run([]string{"testdata/cdk.out", "--region", "us-east-1", "--parameter", "Nope=1"}, io.Discard); err == nil {
		t.Error("an unknown parameter was accepted")
	}
}

func TestMissingRegion(t *testing.T) {
	err := run([]string{"testdata/template.yaml"}, io.Discard)
	if _, ok := err.(usageError); !ok {
		t.Fatalf("want usageError, got %v", err)
	}
}

func TestFlagsAfterInputs(t *testing.T) {
	if err := run([]string{"testdata/template.yaml", "--region", "us-east-1", "--format", "json"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestMarkdown(t *testing.T) {
	stacks, _ := LoadInputs([]string{"testdata/cdk.out"})
	for _, s := range stacks {
		if s.Region == "" {
			s.Region = "us-east-1"
		}
	}
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, testPrices), testPrices).WriteMarkdown(&b)
	golden(t, "cdk.out.md", b.String())
}

// A removed NAT Gateway, a resized instance, a resource that became
// unresolvable (Condition) and a new EIP.
func TestDiff(t *testing.T) {
	load := func(p string) Report {
		stacks, err := loadStacks([]string{p}, "ap-northeast-1")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stacks {
			s.Name = "app" // same stack in base and head
		}
		return NewReport(stacks, Estimate(stacks, testPrices), testPrices)
	}
	d := NewDiff(load("testdata/diff-base.yaml"), load("testdata/diff-head.yaml"))
	var text, md bytes.Buffer
	d.WriteText(&text)
	d.WriteMarkdown(&md)
	golden(t, "diff.txt", text.String())
	golden(t, "diff.md", md.String())
	head := load("testdata/diff-head.yaml") // $25/mo, down $32 from base without the Key
	for _, c := range []struct {
		max, maxIncrease float64
		fail             bool
	}{{-1, 0, true}, {-1, -1, false}, {100, -1, false}, {20, -1, true}} { // Key became "?": --max-increase can't pass
		if err := checkBudget(head, d, c.max, c.maxIncrease); (err != nil) != c.fail {
			t.Errorf("--max %v --max-increase %v: err = %v", c.max, c.maxIncrease, err)
		}
	}
}

func TestParseOffer(t *testing.T) {
	offer := `{"formatVersion":"v1.0","publicationDate":"2026-10-15T00:00:00Z",
	"products":{
	  "A":{"sku":"A","productFamily":"Compute Instance","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-BoxUsage:t3.micro","operation":"RunInstances","instanceType":"t3.micro","operatingSystem":"Linux","tenancy":"Shared","preInstalledSw":"NA","capacitystatus":"Used"}},
	  "B":{"sku":"B","productFamily":"Compute Instance","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-BoxUsage:t3.micro","operation":"RunInstances:0002","instanceType":"t3.micro","operatingSystem":"Windows","tenancy":"Shared","preInstalledSw":"NA","capacitystatus":"Used"}},
	  "C":{"sku":"C","productFamily":"Provisioned Throughput","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-EBS:VolumeP-Throughput.gp3"}},
	  "E":{"sku":"E","productFamily":"Compute Instance","attributes":{"regionCode":"ap-northeast-1-tpe-1","usagetype":"APN1-TPE1-BoxUsage:t3.micro","operation":"RunInstances","instanceType":"t3.micro","operatingSystem":"Linux","tenancy":"Shared","preInstalledSw":"NA","capacitystatus":"Used"}},
	  "D":{"sku":"D","productFamily":"Provisioned IOPS","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-ReadCapacityUnit-Hrs"}}
	},
	"terms":{"OnDemand":{
	  "A":{"A.1":{"priceDimensions":{"A.1.1":{"unit":"Hrs","pricePerUnit":{"USD":"0.0136"}}}}},
	  "B":{"B.1":{"priceDimensions":{"B.1.1":{"unit":"Hrs","pricePerUnit":{"USD":"0.0228"}}}}},
	  "E":{"E.1":{"priceDimensions":{"E.1.1":{"unit":"Hrs","pricePerUnit":{"USD":"0.02"}}}}},
	  "C":{"C.1":{"priceDimensions":{"C.1.1":{"unit":"GiBps-mo","pricePerUnit":{"USD":"49.152"}}}}},
	  "D":{"D.1":{"priceDimensions":{"D.1.1":{"unit":"ReadCapacityUnit-Hrs","pricePerUnit":{"USD":"0"}},"D.1.2":{"unit":"ReadCapacityUnit-Hrs","pricePerUnit":{"USD":"0.0001484"}}}}}
	},"Reserved":{"A":{}}}}`
	all := map[string]map[string]float64{}
	var ec2Sels []selector
	for _, s := range selectors {
		if s.service == "AmazonEC2" || s.service == "AmazonDynamoDB" {
			ec2Sels = append(ec2Sels, s)
		}
	}
	pub, err := parseOffer(strings.NewReader(offer), ec2Sels, "ap-northeast-1", all)
	out := all["ap-northeast-1"]
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"ec2|t3.micro":                   0.0136, // Linux only; the Local Zone SKU E is dropped
		"ebs|EBS:VolumeP-Throughput.gp3": 0.048,  // per MiB/s
		"ddb|ReadCapacityUnit-Hrs":       0.0001484,
	}
	if pub != "2026-10-15T00:00:00Z" || len(out) != len(want) || len(all) != 1 {
		t.Fatalf("pub=%q out=%v", pub, out)
	}
	for k, v := range want {
		if math.Abs(out[k]-v) > 1e-9 {
			t.Errorf("%s = %v, want %v", k, out[k], v)
		}
	}
}

func TestStripUsage(t *testing.T) {
	for in, want := range map[string]string{
		"APN1-BoxUsage:t3.micro":    "BoxUsage:t3.micro",
		"BoxUsage:t3.micro":         "BoxUsage:t3.micro",
		"EU-NatGateway-Hours":       "NatGateway-Hours",
		"ap-northeast-1-KMS-Keys":   "KMS-Keys",
		"AWSSecretsManager-Secrets": "AWSSecretsManager-Secrets",
		"EBS:VolumeUsage.gp3":       "EBS:VolumeUsage.gp3",
	} {
		if got := stripUsage(in, "ap-northeast-1"); got != want {
			t.Errorf("stripUsage(%q) = %q, want %q", in, got, want)
		}
	}
}

// The bundled snapshot must hold the fixed-price keys for the main regions; a
// selector that silently stops matching shows up here after a refresh.
func TestBundledPrices(t *testing.T) {
	p, err := BundledPrices()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"us-east-1", "ap-northeast-1"} {
		for _, k := range []string{
			"nat|NatGateway-Hours", "vpc|PublicIPv4:InUseAddress", "vpc|VpcEndpoint-Hours",
			"elb|Load Balancer-Application", "elb|Load Balancer-Network", "ec2|t3.micro", "ebs|EBS:VolumeUsage.gp3",
			"rds|InstanceUsage:db.t3.micro|2", "rds|Aurora:ServerlessV2Usage|16", "es|t3.small.search",
			"elasticache|Redis|cache.t3.micro", "ddb|ReadCapacityUnit-Hrs", "kinesis|Storage-ShardHour",
			"kms|KMS-Keys", "secrets|AWSSecretsManager-Secrets", "efs|ProvisionedTP-MiBpsHrs",
			"fargate|Fargate-vCPU-Hours:perCPU", "fsx|Windows|Storage", "r53|HostedZone",
			"rdsproxy|RDS:ProxyUsage", "rdsproxy|RDS:Proxy-ASv2-Usage", "aoss|IndexingOCU", "aoss|SearchOCU",
			"memorydb|Redis|db.t4g.small", "memorydb|Valkey|db.t4g.small", "sagemaker|ml.m5.large",
			"fsx|ONTAP|Storage.SAZ_2N:SSD", "ddb|ReplWriteCapacityUnit-Hrs", "apigw|cache:0.5", "apigw|cache:6.1",
			"ds|MicrosoftAD-DC-Usage", "ds|Small-SimpleAD-Usage",
		} {
			if v, ok := p.Get(r, k); !ok || v <= 0 {
				t.Errorf("%s: %s = %v, %v", r, k, v, ok)
			}
		}
	}
}
