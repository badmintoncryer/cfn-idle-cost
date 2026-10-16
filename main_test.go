package main

import (
	"bytes"
	"flag"
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
	"us-east-1": {"rds|Aurora:ServerlessV2IOOptimizedUsage|21": 0.16, "kms|KMS-Keys": 1, "ebs|EBS:VolumeUsage.gp3": 0.08, "ebs|EBS:VolumeP-IOPS.gp3": 0.005, "secrets|AWSSecretsManager-Secrets": 0.4},
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

func TestMissingRegion(t *testing.T) {
	err := run([]string{"testdata/template.yaml"})
	if _, ok := err.(usageError); !ok {
		t.Fatalf("want usageError, got %v", err)
	}
}

func TestFlagsAfterInputs(t *testing.T) {
	if err := run([]string{"testdata/template.yaml", "--region", "us-east-1", "--json"}); err != nil {
		t.Fatal(err)
	}
}

func TestParseOffer(t *testing.T) {
	offer := `{"formatVersion":"v1.0","publicationDate":"2026-10-15T00:00:00Z",
	"products":{
	  "A":{"sku":"A","productFamily":"Compute Instance","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-BoxUsage:t3.micro","operation":"RunInstances","instanceType":"t3.micro","operatingSystem":"Linux","tenancy":"Shared","preInstalledSw":"NA","capacitystatus":"Used"}},
	  "B":{"sku":"B","productFamily":"Compute Instance","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-BoxUsage:t3.micro","operation":"RunInstances:0002","instanceType":"t3.micro","operatingSystem":"Windows","tenancy":"Shared","preInstalledSw":"NA","capacitystatus":"Used"}},
	  "C":{"sku":"C","productFamily":"Provisioned Throughput","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-EBS:VolumeP-Throughput.gp3"}},
	  "D":{"sku":"D","productFamily":"Provisioned IOPS","attributes":{"regionCode":"ap-northeast-1","usagetype":"APN1-ReadCapacityUnit-Hrs"}}
	},
	"terms":{"OnDemand":{
	  "A":{"A.1":{"priceDimensions":{"A.1.1":{"unit":"Hrs","pricePerUnit":{"USD":"0.0136"}}}}},
	  "B":{"B.1":{"priceDimensions":{"B.1.1":{"unit":"Hrs","pricePerUnit":{"USD":"0.0228"}}}}},
	  "C":{"C.1":{"priceDimensions":{"C.1.1":{"unit":"GiBps-mo","pricePerUnit":{"USD":"49.152"}}}}},
	  "D":{"D.1":{"priceDimensions":{"D.1.1":{"unit":"ReadCapacityUnit-Hrs","pricePerUnit":{"USD":"0"}},"D.1.2":{"unit":"ReadCapacityUnit-Hrs","pricePerUnit":{"USD":"0.0001484"}}}}}
	},"Reserved":{"A":{}}}}`
	out := map[string]float64{}
	var ec2Sels []selector
	for _, s := range selectors {
		if s.service == "AmazonEC2" || s.service == "AmazonDynamoDB" {
			ec2Sels = append(ec2Sels, s)
		}
	}
	pub, err := parseOffer(strings.NewReader(offer), ec2Sels, out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"ec2|t3.micro":                   0.0136, // Linux only
		"ebs|EBS:VolumeP-Throughput.gp3": 0.048,  // per MiB/s
		"ddb|ReadCapacityUnit-Hrs":       0.0001484,
	}
	if pub != "2026-10-15T00:00:00Z" || len(out) != len(want) {
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
		} {
			if v, ok := p.Get(r, k); !ok || v <= 0 {
				t.Errorf("%s: %s = %v, %v", r, k, v, ok)
			}
		}
	}
}
