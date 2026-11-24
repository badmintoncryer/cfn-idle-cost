package main

import (
	"bytes"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var osPrices = &Prices{Source: "test prices", AsOf: "2026-11-24", Regions: map[string]map[string]float64{
	"us-east-1": {"ec2|m5.large": 0.096, "ec2win|m5.large": 0.188, "ebs|EBS:VolumeUsage.gp3": 0.08},
}}

const osTemplate = `Parameters:
  WinAmi:
    Type: AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>
    Default: /aws/service/ami-windows-latest/Windows_Server-2022-English-Full-Base
  SqlAmi:
    Type: AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>
    Default: /aws/service/ami-windows-latest/Windows_Server-2022-English-Full-SQL_2022_Standard
  LinuxAmi:
    Type: AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>
    Default: /aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64
Resources:
  Win: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: {Ref: WinAmi}}}
  Sql: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: {Ref: SqlAmi}}}
  Al: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: {Ref: LinuxAmi}}}
  Resolved: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: "{{resolve:ssm:/aws/service/ami-windows-latest/Windows_Server-2019-English-Full-Base}}"}}
  Literal: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: ami-0abc}}
  Literal2: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: ami-0abc}}
  Tagged:
    Type: AWS::EC2::Instance
    Metadata: {cfn-idle-cost: {os: windows}}
    Properties: {InstanceType: m5.large, ImageId: ami-0def}
`

func osStacks(t *testing.T) []*Stack {
	t.Helper()
	path := t.TempDir() + "/os.yaml"
	if err := os.WriteFile(path, []byte(osTemplate), 0o644); err != nil {
		t.Fatal(err)
	}
	stacks, err := LoadInputs([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	return stacks
}

// The OS comes from the ImageId's SSM parameter path or the Metadata; else
// Linux is assumed, marked, with what Windows would cost and how to set it.
func TestImageOS(t *testing.T) {
	got := map[string]Item{}
	for _, it := range Estimate(osStacks(t), osPrices) {
		if strings.HasPrefix(it.Reason, "m5.large") {
			got[it.LogicalID] = it
		}
	}
	win, lin := 0.188*730, 0.096*730
	for id, want := range map[string]struct {
		monthly float64
		reason  string
		assumed bool
	}{
		"Win":      {win, "m5.large on-demand (Windows, license included)", false},
		"Resolved": {win, "m5.large on-demand (Windows, license included)", false},
		"Tagged":   {win, "m5.large on-demand (Windows, license included)", false},
		"Sql":      {win, "m5.large on-demand (Windows, license included)", true},
		"Al":       {lin, "m5.large on-demand (Linux)", false},
		"Literal":  {lin, "m5.large on-demand (Linux assumed)", true},
	} {
		it := got[id]
		if math.Abs(it.Monthly-want.monthly) > 1e-9 || it.Reason != want.reason || (it.Assumption != nil) != want.assumed {
			t.Errorf("%s = %g %q assumed=%v, want %g %q assumed=%v", id, it.Monthly, it.Reason, it.Assumption != nil, want.monthly, want.reason, want.assumed)
		}
	}
	a := got["Literal"].Assumption
	if a == nil || a.IfWrongCase != "Windows" || math.Abs(a.IfWrong-(win-lin)) > 1e-9 || !strings.Contains(a.Override, "--os ami-0abc=windows") {
		t.Errorf("Literal assumption = %+v", a)
	}

	var b bytes.Buffer
	rep := NewReport(osStacks(t), Estimate(osStacks(t), osPrices), osPrices)
	rep.WriteText(&b)
	for _, want := range []string{
		"· $289 of it on assumptions\n", // Sql $137 + Literal $70 + Literal2 $70 + root volumes $12
		"≈ m5.large on-demand (Linux assumed)",
		"≈ assumptions: 4 ",
		"os  Resolved, Sql, Tagged, Win  30 GB gp3 root volume per instance, the Windows Server default",
		"os  Literal, Literal2  Linux assumed: ImageId ami-0abc doesn't say the OS. +$134/mo if Windows. Set it: --os ami-0abc=windows",
		"os  Sql  SQL Server license not counted",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %q in\n%s", want, b.String())
		}
	}
}

// --os sets the OS by AMI or by resource; --assume none leaves assumed lines out.
func TestOSFlags(t *testing.T) {
	defer func() { excludeAssumed = false; clear(osOverrides) }()
	path := t.TempDir() + "/os.yaml"
	os.WriteFile(path, []byte(osTemplate), 0o644)
	var b bytes.Buffer
	if err := run([]string{path, "--region", "us-east-1", "--format", "json", "--os", "ami-0abc=windows"}, &b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Linux assumed") {
		t.Errorf("--os ami-0abc=windows left Linux assumed:\n%s", b.String())
	}
	b.Reset()
	if err := run([]string{path, "--region", "us-east-1", "--os", "Literal=windows", "--assume", "none"}, &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Literal2  --assume none: Linux assumed", "Sql  --assume none: SQL Server license not counted"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %q in\n%s", want, b.String())
		}
	}
	for _, bad := range [][]string{{"--os", "ami-1=mac"}, {"--os", "=windows"}, {"--assume", "all"}} {
		if err := run(append([]string{path, "--region", "us-east-1"}, bad...), io.Discard); err == nil {
			t.Errorf("%v: want an error", bad)
		}
	}
}

// Spot is priced at the Spot Instance Advisor's saving, with the on-demand
// price as how much more it could cost.
func TestSpot(t *testing.T) {
	path := t.TempDir() + "/spot.yaml"
	os.WriteFile(path, []byte(`Resources:
  Box: {Type: AWS::EC2::Instance, Properties: {InstanceType: m5.large, ImageId: "{{resolve:ssm:/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64}}", InstanceMarketOptions: {MarketType: spot}}}
  Nodes: {Type: AWS::EKS::Nodegroup, Properties: {CapacityType: SPOT, InstanceTypes: [m5.large], ScalingConfig: {DesiredSize: 2}}}
`), 0o644)
	stacks, err := LoadInputs([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	prices := &Prices{Regions: map[string]map[string]float64{"us-east-1": {
		"ec2|m5.large": 0.1, "spot|Linux:m5.large": 0.6, "ebs|EBS:VolumeUsage.gp3": 0.08}}}
	got := map[string]Item{}
	for _, it := range Estimate(stacks, prices) {
		if strings.Contains(it.Reason, "Spot") {
			got[it.LogicalID] = it
		}
	}
	for id, n := range map[string]float64{"Box": 1, "Nodes": 2} {
		it := got[id]
		if math.Abs(it.Monthly-n*0.04*730) > 1e-9 || it.Assumption == nil || math.Abs(it.Assumption.IfWrong-n*0.06*730) > 1e-9 {
			t.Errorf("%s = %g %q %+v", id, it.Monthly, it.Reason, it.Assumption)
		}
	}
}

func TestAddSpotSavings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"spot_advisor": {"us-east-1": {"Linux": {"m5.large": {"s": 62, "r": 1}}, "Windows": {"m5.large": {"s": 40, "r": 1}}},
			"eu-west-1": {"Linux": {"m5.large": {"s": 70, "r": 1}}}}}`)
	}))
	defer srv.Close()
	defer func(u string) { SpotAdvisorURL = u }(SpotAdvisorURL)
	SpotAdvisorURL = srv.URL
	p := &Prices{Regions: map[string]map[string]float64{}}
	n, err := addSpotSavings(p, []string{"us-east-1"})
	if err != nil || n != 2 || p.Regions["us-east-1"]["spot|Linux:m5.large"] != 0.62 || p.Regions["us-east-1"]["spot|Windows:m5.large"] != 0.4 || p.Regions["eu-west-1"] != nil {
		t.Errorf("n=%d err=%v prices=%v", n, err, p.Regions)
	}
}
