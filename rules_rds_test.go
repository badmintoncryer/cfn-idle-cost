package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// rdsPrices are us-east-1 list prices, fixed so the golden file doesn't move
// with the snapshot. AsOf decides which RDS Extended Support rate applies.
var rdsPrices = &Prices{Source: "test prices", AsOf: "2026-11-16", Regions: map[string]map[string]float64{
	"us-east-1": {
		"rds|InstanceUsage:db.r6g.large|2": 0.215, "rds|Multi-AZUsage:db.r6g.large|2": 0.43, "rds|RDS:Multi-AZ-GP3-Storage|2": 0.23,
		"rds|InstanceUsage:db.t3.medium|2": 0.068, "rds|RDS:GP3-Storage|2": 0.115, "rds|InstanceUsage:db.r6g.large|14": 0.225,
		"rds|InstanceUsage:db.m7g.xlarge|14": 0.337, "rds|RDS:GP3-Storage|14": 0.115, "rds|InstanceUsage:db.r6g.large|21": 0.26,
		"rds|Aurora:ServerlessV2Usage|21": 0.12, "rds|InstanceUsage:db.r5.large|16": 0.29, "rds|Multi-AZClusterUsage:db.m6gd.large|14": 0.522,
		"rds|RDS:Multi-AZCluster-PIOPS-Storage|14": 0.375, "rds|RDS:Multi-AZCluster-PIOPS|14": 0.3, "rds|InstanceUsage:db.m5.large|20": 0.438,
		"rds|RDS:GP3-Storage|20": 0.115, "rds|RDS:PIOPS-Storage-IO2|20": 0.125, "rds|RDS:IO2-PIOPS|20": 0.1,
		"rdses|ExtendedSupport:Yr1-Yr2:MySQL8.0": 0.1, "rdses|ExtendedSupport:Yr3:MySQL5.7": 0.2,
		"rdses|ExtendedSupport:Yr1-Yr2:PostgreSQL13": 0.1, "rdses|ExtendedSupport:Yr1-Yr2:AuroraPostgreSQL13": 0.1,
		"rdses|ExtendedSupport:Yr1-Yr2:ASv2:AuroraPostgreSQL13": 0.085, "rdses|ExtendedSupport:Yr1-Yr2:AuroraMySQL2": 0.1,
		"cw|CW:DatabaseInsights-vCPU-Hours": 0.0125, "cw|CW:DatabaseInsights-ACU-Hours": 0.003125,
	},
}}

func TestRDSRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/rds.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, rdsPrices), rdsPrices).WriteText(&b)
	golden(t, "rds.yaml", b.String())
}

// The Extended Support rate follows the date of the prices: none before the
// year 1 date, the year 3 rate from its date, and no estimate past the end.
func TestRDSExtendedSupportDates(t *testing.T) {
	path := t.TempDir() + "/t.yaml"
	tpl := `Resources: { Db: { Type: AWS::RDS::DBInstance, Properties: { Engine: mysql, EngineVersion: "8.0.40", DBInstanceClass: db.r6g.large, AllocatedStorage: 20 } } }`
	if err := os.WriteFile(path, []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	stacks, err := LoadInputs([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	for day, want := range map[string]string{
		"2026-07-31": "",
		"2026-08-01": "year 1-2 rate since 2026-08-01",
		"2028-08-01": "year 3 rate since 2028-08-01",
		"2029-08-01": "reached the end of RDS Extended Support on 2029-07-31",
	} {
		p := &Prices{AsOf: day, Regions: map[string]map[string]float64{"us-east-1": {
			"rds|InstanceUsage:db.r6g.large|2": 0.215, "rds|RDS:GP3-Storage|2": 0.115,
			"rdses|ExtendedSupport:Yr1-Yr2:MySQL8.0": 0.1, "rdses|ExtendedSupport:Yr3:MySQL8.0": 0.2,
		}}}
		got := ""
		for _, it := range Estimate(stacks, p) {
			if strings.Contains(it.Reason, "Extended Support") {
				got = it.Reason
			}
		}
		if want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want %q", day, got, want)
		}
	}
	for _, c := range [][3]string{
		{"mysql", "8.0.40", "MySQL8.0"}, {"postgres", "13.15", "PostgreSQL13"}, {"postgres", "16", "PostgreSQL16"},
		{"aurora-mysql", "5.7.mysql_aurora.2.11.2", "AuroraMySQL2"}, {"aurora-mysql", "8.0.mysql_aurora.3.05.2", "AuroraMySQL3"},
		{"aurora-postgresql", "13.15", "AuroraPostgreSQL13"}, {"mariadb", "10.6.14", ""}, {"mysql", "8", ""},
	} {
		if got := rdsMajorVersion(c[0], c[1]); got != c[2] {
			t.Errorf("rdsMajorVersion(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
