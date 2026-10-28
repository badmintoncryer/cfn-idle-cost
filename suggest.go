package main

import (
	"fmt"
	"math"
	"strings"
)

// Suggestions are cheaper settings the template proves would cost less, priced
// from the same snapshot. Each one is attached to the line it would replace and
// shown with --suggest; the totals stay the current cost.

// suggest attaches a suggestion to the line added last, when it saves at least a cent.
func (e *est) suggest(saving float64, text string) {
	if saving < 0.005 || len(e.items) == 0 {
		return
	}
	it := &e.items[len(e.items)-1]
	if it.Suggestion == "" {
		it.Suggestion, it.Saving = text, saving
	}
}

// tryPrice is price without giving up on the resource when the key is missing.
func (e *est) tryPrice(key string) (float64, bool) {
	return e.prices.Get(e.st.Region, key)
}

// gravitonFamily maps x86 instance families to the Graviton family of the same
// generation (or the first Graviton one, for older x86 families).
var gravitonFamily = map[string]string{
	"t3": "t4g", "t3a": "t4g",
	"m5": "m6g", "m5a": "m6g", "m6i": "m6g", "m6a": "m6g", "m7i": "m7g", "m7a": "m7g", "m8i": "m8g", "m8a": "m8g",
	"c5": "c6g", "c5a": "c6g", "c6i": "c6g", "c6a": "c6g", "c7i": "c7g", "c7a": "c7g", "c8i": "c8g", "c8a": "c8g",
	"r5": "r6g", "r5a": "r6g", "r6i": "r6g", "r6a": "r6g", "r7i": "r7g", "r7a": "r7g", "r8i": "r8g", "r8a": "r8g",
	"m5d": "m6gd", "m6id": "m6gd", "c5d": "c6gd", "c6id": "c6gd", "r5d": "r6gd", "r6id": "r6gd",
}

// graviton returns the Graviton type of the same size as an x86 type, keeping the
// prefix (db., cache.) and suffix (.search) of RDS, ElastiCache and OpenSearch types.
func graviton(t string) (string, bool) {
	prefix := ""
	for _, p := range []string{"db.", "cache."} {
		if rest, ok := strings.CutPrefix(t, p); ok {
			prefix, t = p, rest
		}
	}
	family, rest, ok := strings.Cut(t, ".")
	if !ok {
		return "", false
	}
	g, ok := gravitonFamily[family]
	if !ok {
		return "", false
	}
	return prefix + g + "." + rest, true
}

// suggestGraviton offers the Graviton type of n instances whose hourly price key
// is keyOf(type), when it exists in the region and costs less.
func (e *est) suggestGraviton(t string, n float64, keyOf func(t string) string, caveat string) {
	g, ok := graviton(t)
	if !ok {
		return
	}
	cur, ok1 := e.tryPrice(keyOf(t))
	arm, ok2 := e.tryPrice(keyOf(g))
	if !ok1 || !ok2 {
		return
	}
	e.suggest(n*(cur-arm)*hoursPerMonth, g+" (Graviton)"+caveat)
}

const armCaveat = ", if the AMI and software run on arm64"

// suggestGP3 offers gp3 for n gp2 volumes, with the IOPS and throughput gp2
// gives the size as its baseline.
func (e *est) suggestGP3(size, n float64) {
	gp2, ok1 := e.tryPrice("ebs|EBS:VolumeUsage.gp2")
	gp3, ok2 := e.tryPrice("ebs|EBS:VolumeUsage.gp3")
	pIOPS, ok3 := e.tryPrice("ebs|EBS:VolumeP-IOPS.gp3")
	pTput, ok4 := e.tryPrice("ebs|EBS:VolumeP-Throughput.gp3")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	iops := math.Min(math.Max(3*size, 100), 16000)
	tput := 125.0 // gp2 gives up to 250 MiB/s from 334 GiB
	if size >= 334 {
		tput = 250
	}
	cost := size*gp3 + math.Max(0, iops-3000)*pIOPS + (tput-125)*pTput
	text := "gp3"
	if iops > 3000 || tput > 125 {
		text = fmt.Sprintf("gp3 with %.0f IOPS and %.0f MiB/s, gp2's baseline", iops, tput)
	}
	e.suggest(n*(size*gp2-cost), text)
}

// suggestOneNAT offers to drop the NAT Gateways of a VPC after its first one:
// the others' hours, and their Elastic IPs when they are in the template.
func (e *est) suggestOneNAT(p map[string]any) {
	if e.str(p, "ConnectivityType", "public") != "public" {
		return
	}
	vpc := func(nat Resource) string {
		subnet := e.st.Resources[refID(nat.Properties["SubnetId"])]
		if v := refID(subnet.Properties["VpcId"]); v != "" {
			return v
		}
		return "?" // the subnet is not in the template: group by stack
	}
	mine, n, first := vpc(e.r), 0, ""
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if r.Type != "AWS::EC2::NatGateway" || vpc(r) != mine || r.Properties["ConnectivityType"] != nil && r.Properties["ConnectivityType"] != "public" {
			continue
		}
		if first == "" {
			first = id
		}
		n++
	}
	if n < 2 || first == e.id {
		return
	}
	nat, ok := e.tryPrice("nat|NatGateway-Hours")
	if !ok {
		return
	}
	saving := nat * hoursPerMonth
	if eip := e.st.Resources[refID(p["AllocationId"])]; eip.Type == "AWS::EC2::EIP" {
		if ip, ok := e.tryPrice("vpc|PublicIPv4:InUseAddress"); ok {
			saving += ip * hoursPerMonth
		}
	}
	e.suggest(saving, fmt.Sprintf("one NAT Gateway for the VPC instead of %d (CDK natGateways: 1); egress then depends on one AZ", n))
}

func ec2Key(t string) string { return "ec2|" + t }

// suggestFargateARM offers ARM64 for x86 Linux tasks, attached to their vCPU line.
func (e *est) suggestFargateARM(vcpu, gb float64) {
	var cur, arm float64
	for _, k := range []struct {
		qty        float64
		x86, arm64 string
	}{
		{vcpu, "fargate|Fargate-vCPU-Hours:perCPU", "fargate|Fargate-ARM-vCPU-Hours:perCPU"},
		{gb, "fargate|Fargate-GB-Hours", "fargate|Fargate-ARM-GB-Hours"},
	} {
		x, ok1 := e.tryPrice(k.x86)
		a, ok2 := e.tryPrice(k.arm64)
		if !ok1 || !ok2 {
			return
		}
		cur, arm = cur+k.qty*x, arm+k.qty*a
	}
	e.suggest((cur-arm)*hoursPerMonth, "ARM64 vCPU and memory (Graviton, RuntimePlatform CpuArchitecture), if the image is built for arm64")
}
