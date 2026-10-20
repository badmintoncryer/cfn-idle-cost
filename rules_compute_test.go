package main

import (
	"bytes"
	"testing"
)

var computePrices = &Prices{Source: "test prices", AsOf: "2026-10-20", Regions: map[string]map[string]float64{
	"us-east-1": {
		"rds|Aurora:IO-OptimizedLimitlessACUUsage-Limitless|21": 0.16, "rds|InstanceUsage:db.r6g.large|21": 0.26,
		"rds|InstanceUsage:db.t4g.micro|14": 0.016, "rds|InstanceUsage:db.t4g.small|14": 0.032, "rds|RDS:GP3-Storage|14": 0.115,
		"ec2|t3.micro": 0.0104, "ebs|EBS:VolumeUsage.gp3": 0.08, "ec2|c5.large": 0.085,
		"gamelift|Linux|c5.large": 0.109, "gamelift|LinuxFleetIQ|c5.large": 0.017,
		"fargate|Fargate-vCPU-Hours:perCPU": 0.04048, "fargate|Fargate-GB-Hours": 0.004445, "vpc|PublicIPv4:InUseAddress": 0.005,
		"elb|Load Balancer-Application": 0.0225, "eks|AmazonEKSCapabilities-ArgoCD-Hours:perCapability": 0.03,
		"eks|AmazonEKSCapabilities-ACK-Hours:perCapability": 0.005, "transfer|WebAppHours": 0.5, "ssm|AppConfig-ExperimentHours": 0.9, "cw|CW:AlarmMonitorUsage": 0.1,
		"lambda|Lambda-SnapStart-Cached-GB-S": 0.0000015046,
	},
	"global": {"budgets|ActionEnabledBudgetsUsage": 0.1},
}}

// Limitless shard groups, Blue/Green deployments, warm pools, GameLift container
// fleets and FleetIQ, ECS Express Mode, EKS capabilities, File Cache, Transfer
// web apps, AppConfig experiments, Glue schedules, budget actions, log alarms
// and the SnapStart cache.
func TestComputeRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/compute.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, computePrices), computePrices).WriteText(&b)
	golden(t, "compute.yaml", b.String())
}
