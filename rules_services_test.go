package main

import (
	"bytes"
	"testing"
)

var servicesPrices = &Prices{Source: "test prices", AsOf: "2026-10-20", Regions: map[string]map[string]float64{
	"us-east-1": {
		"codebuild|Linux:Reserved:x86-64.g1.small": 0.003, "appstream|stream.standard.medium": 0.1,
		"wsi|m5.large": 0.116, "ebs|EBS:VolumeUsage.gp3": 0.08, "ec2|t3.small": 0.0208, "ec2|c5.large": 0.085, "ec2|p5.48xlarge": 55.04,
		"ec2|i4i.metal": 10.982, "evs|EVSUsage": 0.92,
		"pcs|PCSController:Slurm:Small:RunningHours": 0.5825, "pcs|PCSAccountingUsage:Slurm:Small:RunningHours": 0.17,
		"pcs|PCSInstance:Slurm:StdNode:RunningHours": 0.08, "pcs|PCSInstance:Slurm:AdvNode:RunningHours": 0.64,
		"m2|m5.largeBluAgeRuntime": 0.31, "m2|m5.largeEnterpriseServer": 5.55,
		"medialive|INACTIVE_INPUT": 0.01, "medialive|INACTIVE_MULTIPLEX": 0.01, "ssm|IM-ResponsePlan-Months-Tier1": 7,
		"paycrypto|ActiveKeys": 1, "b2bi|PartnershipMonths": 8, "billingconductor|Proforma-Accounts": 8.25,
		"lightsail|LoadBalancerUsage": 0.024193548, "lightsail|DiskUsage": 0.1, "lightsail|UnusedStaticIP": 0.005,
		"lightsail|ContainerSvcUsage:Micro-0.25CPU-1GB": 0.01344086, "lightsail|Storage-BundleUsage:100GB": 3,
		"lightsail|CDN-BundleUsage:50GB": 2.5,
	},
}}

// CodeBuild reserved fleets, AppStream image builders, WorkSpaces Core, Cloud9,
// Deadline, PCS, EVS, Mainframe Modernization, MediaLive idle charges, Incident
// Manager, Payment Cryptography, B2B Data Interchange, Billing Conductor,
// CloudFront flat-rate plans, End User Messaging and Lightsail.
func TestServicesRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/services.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, servicesPrices), servicesPrices).WriteText(&b)
	golden(t, "services.yaml", b.String())
}
