package main

import (
	"bytes"
	"flag"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// testPrices are fixed so the golden files don't move with the weekly snapshot.
var testPrices = &Prices{Source: "test prices", Regions: map[string]map[string]float64{
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
		"elb|Load Balancer-Application": 0.0225},
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
	head := load("testdata/diff-head.yaml") // $24/mo, down $33 from base
	for _, c := range []struct {
		max, maxIncrease float64
		fail             bool
	}{{-1, 0, false}, {100, -1, false}, {20, -1, true}} {
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
