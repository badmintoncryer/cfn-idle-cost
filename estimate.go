package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const hoursPerMonth = 730

// Item is one billed line of a resource, or (Unknown != "") a resource whose
// idle cost could not be estimated.
type Item struct {
	Stack     string  `json:"stack"`
	Region    string  `json:"region"`
	Path      string  `json:"path"`
	LogicalID string  `json:"logicalId"`
	Type      string  `json:"type"`
	Quantity  string  `json:"quantity,omitempty"`
	Monthly   float64 `json:"monthly"`
	Reason    string  `json:"reason"`
	Unknown   bool    `json:"-"`
	// NotCounted marks a resource of a type that costs money idle but has no rule yet.
	NotCounted bool `json:"-"`
	// NoRule marks a resource of a type with no rule that is not known to be free.
	NoRule bool `json:"-"`
}

// unresolved aborts the estimate of one resource; the message says why.
type unresolved string

type est struct {
	st     *Stack
	all    []*Stack // every input stack, for references across stacks
	prices *Prices
	r      Resource
	id     string
	items  []Item
	notes  []string // parameters whose defaults were used
}

// Estimate returns the billed items of every stack, plus the resources it could
// not price (Unknown).
func Estimate(stacks []*Stack, prices *Prices) []Item {
	var out []Item
	for _, st := range stacks {
		for _, id := range sortedIDs(st.Resources) {
			out = append(out, estimateResource(stacks, st, prices, id)...)
		}
	}
	return out
}

func estimateResource(all []*Stack, st *Stack, prices *Prices, id string) (items []Item) {
	e := &est{st: st, all: all, prices: prices, r: st.Resources[id], id: id}
	base := Item{Stack: st.Name, Region: st.Region, Path: st.DisplayPath(id), LogicalID: id, Type: e.r.Type}
	defer func() {
		if v := recover(); v != nil {
			msg, ok := v.(unresolved)
			if !ok {
				panic(v)
			}
			u := base
			u.Unknown, u.Reason = true, string(msg)
			items = []Item{u}
		}
	}()
	rule := rules[e.r.Type]
	if on, ok, c := stackCondition(st); ok && !on {
		return nil // its nested stack is not created
	} else if !ok && (rule != nil || in(e.r.Type, notCounted...)) {
		panic(unresolved("its nested stack depends on Condition " + c))
	}
	if c := e.r.Condition; c != "" {
		on, ok := e.tryCondition(c)
		if ok && !on {
			return nil // not created
		}
		if !ok && rule != nil {
			panic(unresolved("depends on Condition " + c))
		}
	}
	if rule == nil {
		nc := base
		switch {
		case in(e.r.Type, notCounted...):
			nc.NotCounted, nc.Reason = true, "not counted yet"
		case !freeType(e.r.Type):
			nc.NoRule, nc.Reason = true, "no rule; assumed $0 idle"
		default:
			return nil
		}
		return []Item{nc}
	}
	rule(e, e.props(e.r))
	for _, it := range e.items {
		b := base
		b.Quantity, b.Monthly, b.Reason = it.Quantity, it.Monthly, it.Reason
		if len(e.notes) > 0 {
			b.Reason += " (default of " + strings.Join(e.notes, ", ") + ")"
		}
		items = append(items, b)
	}
	return items
}

// stackCondition evaluates the Conditions of the AWS::CloudFormation::Stack
// resources that create a nested stack, up to its root.
func stackCondition(st *Stack) (on, ok bool, name string) {
	for s := st; s.parent != nil; s = s.parent {
		c := s.parent.Resources[s.parentID].Condition
		if c == "" {
			continue
		}
		e := &est{st: s.parent}
		if on, ok := e.tryCondition(c); !ok || !on {
			return on, ok, c
		}
	}
	return true, true, ""
}

// ---- value resolution

// resolve returns a literal value. Ref to AWS::Region and to Parameters with a
// Default resolve; anything else (other Refs, Fn::*) does not.
func (e *est) resolve(name string, v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	if ref, ok := m["Ref"].(string); ok && len(m) == 1 {
		if ref == "AWS::Region" {
			return e.st.Region
		}
		if p, ok := e.st.Parameters[ref]; ok {
			if p.Given {
				return p.Value
			}
			if p.Default == nil || strings.HasPrefix(p.Type, "AWS::SSM::") {
				panic(unresolved(fmt.Sprintf("%s is Parameter %s", name, ref)))
			}
			if !in(ref, e.notes...) {
				e.notes = append(e.notes, ref)
			}
			return p.Default
		}
		panic(unresolved(fmt.Sprintf("%s refers to %s", name, ref)))
	}
	for k := range m {
		panic(unresolved(fmt.Sprintf("%s uses %s", name, k)))
	}
	panic(unresolved(name + " is empty"))
}

// ---- conditions

// noValue stands for Ref AWS::NoValue while props rewrites a tree.
var noValue = &struct{}{}

// props returns the properties of r with every Fn::If whose condition resolves
// replaced by the chosen branch, and AWS::NoValue removed. An Fn::If that does
// not resolve is kept, so reading through it is still unresolved.
func (e *est) props(r Resource) map[string]any {
	m, _ := e.pick(r.Properties).(map[string]any)
	return m
}

func (e *est) pick(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 1 {
			if x["Ref"] == "AWS::NoValue" {
				return noValue
			}
			if l, ok := x["Fn::If"].([]any); ok && len(l) == 3 {
				name, _ := l[0].(string)
				if on, ok := e.tryCondition(name); ok {
					if on {
						return e.pick(l[1])
					}
					return e.pick(l[2])
				}
				return x
			}
		}
		out := make(map[string]any, len(x))
		for k, c := range x {
			if c = e.pick(c); c != noValue {
				out[k] = c
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, c := range x {
			if c = e.pick(c); c != noValue {
				out = append(out, c)
			}
		}
		return out
	}
	return v
}

func (e *est) tryCondition(name string) (on, ok bool) {
	defer func() {
		if v := recover(); v != nil {
			if _, isU := v.(unresolved); !isU {
				panic(v)
			}
			ok = false
		}
	}()
	return e.condition(name, map[string]any{"Condition": name}), true
}

// condition evaluates a Conditions expression; it panics when a value does not
// resolve (see resolve).
func (e *est) condition(name string, v any) bool {
	m, _ := v.(map[string]any)
	if len(m) != 1 {
		panic(unresolved("Condition " + name + " is not a condition"))
	}
	for fn, arg := range m {
		l, _ := arg.([]any)
		switch fn {
		case "Condition":
			c, _ := arg.(string)
			def, ok := e.st.Conditions[c]
			if !ok {
				panic(unresolved("Condition " + c + " is not defined"))
			}
			return e.condition(c, def)
		case "Fn::Equals":
			if len(l) != 2 {
				break
			}
			return fmt.Sprint(e.scalar(name, l[0])) == fmt.Sprint(e.scalar(name, l[1]))
		case "Fn::Not":
			if len(l) != 1 {
				break
			}
			return !e.condition(name, l[0])
		case "Fn::And", "Fn::Or":
			and := fn == "Fn::And"
			for _, c := range l {
				if e.condition(name, c) != and {
					return !and
				}
			}
			return and
		}
		panic(unresolved("Condition " + name + " uses " + fn))
	}
	panic("unreachable")
}

// scalar resolves a condition operand, which may itself be an Fn::If.
func (e *est) scalar(name string, v any) any {
	v = e.pick(v)
	if v == noValue {
		return ""
	}
	return e.resolve("Condition "+name, v)
}

// str returns m[key], def when absent; panics when present but unresolvable.
func (e *est) str(m map[string]any, key, def string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch x := e.resolve(key, v).(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	panic(unresolved(key + " is not a scalar"))
}

func (e *est) mustStr(m map[string]any, key string) string {
	s := e.str(m, key, "")
	if s == "" {
		panic(unresolved(key + " is not set"))
	}
	return s
}

func (e *est) num(m map[string]any, key string, def float64) float64 {
	s := e.str(m, key, "")
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		panic(unresolved(fmt.Sprintf("%s is %q", key, s)))
	}
	return f
}

func (e *est) mustNum(m map[string]any, key string) float64 {
	if _, ok := m[key]; !ok {
		panic(unresolved(key + " is not set"))
	}
	return e.num(m, key, 0)
}

func (e *est) flag(m map[string]any, key string) bool {
	return strings.EqualFold(e.str(m, key, "false"), "true")
}

// length counts a list without resolving its items (a list of subnet Refs is
// still two subnets). -1 when absent.
func (e *est) length(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return -1
	}
	l, ok := v.([]any)
	if !ok {
		e.resolve(key, v) // panics with the reason
		panic(unresolved(key + " is not a list"))
	}
	for _, x := range l { // props already chose the Fn::Ifs that resolve
		if _, ok := asMap(x)["Fn::If"]; ok {
			panic(unresolved(key + " has an item that uses Fn::If"))
		}
	}
	return len(l)
}

// ref returns the same-template resource a property points to with Ref (or
// Fn::GetAtt), or panics.
func (e *est) ref(m map[string]any, key string) (string, Resource) {
	v := m[key]
	var id string
	if r, ok := v.(map[string]any); ok {
		if s, ok := r["Ref"].(string); ok {
			id = s
		} else if l, ok := r["Fn::GetAtt"].([]any); ok && len(l) > 0 {
			id, _ = l[0].(string)
		}
	}
	if r, ok := e.st.Resources[id]; ok {
		r.Properties = e.props(r)
		return id, r
	}
	panic(unresolved(key + " does not point to a resource in this template"))
}

func (e *est) price(key string) float64 {
	v, ok := e.prices.Get(e.st.Region, key)
	if !ok {
		_, detail, _ := strings.Cut(key, "|")
		panic(unresolved(fmt.Sprintf("no price for %s in %s", detail, e.st.Region)))
	}
	return v
}

// ---- items

func (e *est) hourly(qty float64, unit, key, reason string) {
	if qty > 0 {
		e.add(qty, unit, qty*e.price(key)*hoursPerMonth, reason)
	}
}

func (e *est) monthly(qty float64, unit, key, reason string) {
	if qty > 0 {
		e.add(qty, unit, qty*e.price(key), reason)
	}
}

func (e *est) add(qty float64, unit string, monthly float64, reason string) {
	if qty <= 0 {
		return
	}
	q := "×" + strconv.FormatFloat(qty, 'f', -1, 64)
	if unit != "" {
		q += " " + unit
	}
	e.items = append(e.items, Item{Quantity: q, Monthly: monthly, Reason: reason})
}

// ---- rules

var rules map[string]func(e *est, p map[string]any)

func init() {
	rules = map[string]func(e *est, p map[string]any){
		"AWS::EC2::NatGateway": func(e *est, p map[string]any) {
			e.hourly(1, "", "nat|NatGateway-Hours", "NAT Gateway")
		},
		"AWS::EC2::EIP": func(e *est, p map[string]any) {
			e.hourly(1, "", "vpc|PublicIPv4:InUseAddress", "public IPv4 address")
		},
		"AWS::EC2::VPCEndpoint": func(e *est, p map[string]any) {
			switch t := e.str(p, "VpcEndpointType", "Gateway"); t {
			case "Gateway":
			case "Interface":
				n := e.length(p, "SubnetIds")
				if n < 0 {
					panic(unresolved("SubnetIds is not set"))
				}
				e.hourly(float64(n), "AZ", "vpc|VpcEndpoint-Hours", "interface endpoint")
			case "GatewayLoadBalancer":
				e.hourly(1, "", "vpc|VpcEndpoint-GWLBE-Hours", "Gateway Load Balancer endpoint")
			default:
				panic(unresolved("VpcEndpointType " + t + " is not supported yet"))
			}
		},
		"AWS::ElasticLoadBalancingV2::LoadBalancer": func(e *est, p map[string]any) {
			family := map[string]string{"application": "Application", "network": "Network", "gateway": "Gateway"}
			t := e.str(p, "Type", "application")
			f, ok := family[t]
			if !ok {
				panic(unresolved("Type " + t + " is not supported"))
			}
			e.hourly(1, "", "elb|Load Balancer-"+f, f+" Load Balancer")
			if t == "gateway" || e.str(p, "Scheme", "internet-facing") != "internet-facing" || e.str(p, "IpAddressType", "ipv4") == "dualstack-without-public-ipv4" {
				return
			}
			n := e.length(p, "Subnets")
			if n < 0 {
				n = 0
				ml, _ := p["SubnetMappings"].([]any)
				if ml == nil {
					e.length(p, "SubnetMappings") // panics when it is an intrinsic
					panic(unresolved("Subnets is not set"))
				}
				for _, sm := range ml {
					if _, eip := asMap(sm)["AllocationId"]; !eip { // an EIP is counted on its own
						n++
					}
				}
			}
			e.hourly(float64(n), "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 per AZ (internet-facing)")
		},
		"AWS::ElasticLoadBalancing::LoadBalancer": func(e *est, p map[string]any) {
			e.hourly(1, "", "elb|Load Balancer", "Classic Load Balancer")
		},
		"AWS::EC2::Instance": func(e *est, p map[string]any) {
			t := e.str(p, "InstanceType", "")
			bdm := p["BlockDeviceMappings"]
			nics := p["NetworkInterfaces"]
			if lt, ok := p["LaunchTemplate"]; ok {
				data := e.launchTemplate(asMap(lt))
				if t == "" {
					t = e.str(data, "InstanceType", "")
				}
				if bdm == nil {
					bdm = data["BlockDeviceMappings"]
				}
				if nics == nil {
					nics = data["NetworkInterfaces"]
				}
			}
			if t == "" {
				panic(unresolved("InstanceType is not set"))
			}
			if e.str(p, "Tenancy", "default") != "default" {
				panic(unresolved("dedicated tenancy is not supported yet"))
			}
			e.notLocalZone(p)
			e.hourly(1, "", "ec2|"+t, t+" on-demand (Linux)")
			e.blockDevices(bdm, 1)
			nic := firstMap(nics)
			subnet := p["SubnetId"]
			if subnet == nil {
				subnet = nic["SubnetId"]
			}
			if e.autoPublicIP(nic["AssociatePublicIpAddress"], []any{subnet}) && !e.hasEIP() {
				e.hourly(1, "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 assigned at launch")
			}
		},
		"AWS::AutoScaling::AutoScalingGroup": func(e *est, p map[string]any) {
			if _, ok := p["MixedInstancesPolicy"]; ok {
				panic(unresolved("MixedInstancesPolicy is not supported yet"))
			}
			n, why := e.num(p, "DesiredCapacity", -1), "DesiredCapacity"
			if n < 0 {
				n, why = e.mustNum(p, "MinSize"), "MinSize"
			}
			var t string
			var bdm, assoc any
			if lt, ok := p["LaunchTemplate"]; ok {
				data := e.launchTemplate(asMap(lt))
				t, bdm = e.mustStr(data, "InstanceType"), data["BlockDeviceMappings"]
				assoc = firstMap(data["NetworkInterfaces"])["AssociatePublicIpAddress"]
			} else if _, ok := p["LaunchConfigurationName"]; ok {
				_, lc := e.ref(p, "LaunchConfigurationName")
				t, bdm = e.mustStr(lc.Properties, "InstanceType"), lc.Properties["BlockDeviceMappings"]
				assoc = lc.Properties["AssociatePublicIpAddress"]
			} else {
				panic(unresolved("no LaunchTemplate or LaunchConfigurationName"))
			}
			e.hourly(n, "", "ec2|"+t, fmt.Sprintf("%s on-demand (Linux), %s", t, why))
			e.blockDevices(bdm, n)
			subnets, _ := p["VPCZoneIdentifier"].([]any)
			if e.autoPublicIP(assoc, subnets) {
				e.hourly(n, "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 assigned at launch, "+why)
			}
		},
		"AWS::EC2::Volume": func(e *est, p map[string]any) {
			e.ebs(e.str(p, "VolumeType", "gp2"), e.mustNum(p, "Size"), e.num(p, "Iops", 0), e.num(p, "Throughput", 0), 1)
		},
		"AWS::RDS::DBInstance": rdsInstance,
		"AWS::RDS::DBCluster":  rdsCluster,
		"AWS::OpenSearchService::Domain": func(e *est, p map[string]any) {
			e.searchDomain(asMap(p["ClusterConfig"]), "ClusterConfig", p)
		},
		"AWS::Elasticsearch::Domain": func(e *est, p map[string]any) {
			e.searchDomain(asMap(p["ElasticsearchClusterConfig"]), "ElasticsearchClusterConfig", p)
		},
		"AWS::ElastiCache::CacheCluster": func(e *est, p map[string]any) {
			e.cacheNodes(e.mustStr(p, "Engine"), e.mustStr(p, "CacheNodeType"), e.mustNum(p, "NumCacheNodes"))
		},
		"AWS::ElastiCache::ReplicationGroup": func(e *est, p map[string]any) {
			var n float64
			switch {
			case p["NumCacheClusters"] != nil:
				n = e.num(p, "NumCacheClusters", 0)
			case p["NumNodeGroups"] != nil || p["ReplicasPerNodeGroup"] != nil:
				n = e.num(p, "NumNodeGroups", 1) * (e.num(p, "ReplicasPerNodeGroup", 0) + 1)
			case p["NodeGroupConfiguration"] != nil:
				l, ok := p["NodeGroupConfiguration"].([]any)
				if !ok {
					panic(unresolved("NodeGroupConfiguration is not a list"))
				}
				for _, g := range l {
					n += e.num(asMap(g), "ReplicaCount", 0) + 1
				}
			default:
				panic(unresolved("node count is not set"))
			}
			e.cacheNodes(e.str(p, "Engine", "redis"), e.mustStr(p, "CacheNodeType"), n)
		},
		"AWS::ElastiCache::ServerlessCache": func(e *est, p map[string]any) {
			engine := cacheEngine(e.mustStr(p, "Engine"))
			min := 1.0
			if engine == "Valkey" {
				min = 0.1
			}
			ds := asMap(asMap(p["CacheUsageLimits"])["DataStorage"])
			if v := e.num(ds, "Minimum", 0); v > min {
				min = v
			}
			e.hourly(min, "GB", "elasticache|CachedData:"+engine, "Serverless minimum data storage")
		},
		"AWS::DynamoDB::Table": func(e *est, p map[string]any) {
			if e.str(p, "BillingMode", "PROVISIONED") != "PROVISIONED" {
				return
			}
			ia := ""
			if e.str(p, "TableClass", "STANDARD") == "STANDARD_INFREQUENT_ACCESS" {
				ia = "IA-"
			}
			pt := asMap(p["ProvisionedThroughput"])
			r, w := e.mustNum(pt, "ReadCapacityUnits"), e.mustNum(pt, "WriteCapacityUnits")
			if l, ok := p["GlobalSecondaryIndexes"].([]any); ok {
				for _, g := range l {
					gpt := asMap(asMap(g)["ProvisionedThroughput"])
					r += e.num(gpt, "ReadCapacityUnits", 0)
					w += e.num(gpt, "WriteCapacityUnits", 0)
				}
			}
			e.hourly(r, "RCU", "ddb|"+ia+"ReadCapacityUnit-Hrs", "provisioned read capacity")
			e.hourly(w, "WCU", "ddb|"+ia+"WriteCapacityUnit-Hrs", "provisioned write capacity")
		},
		"AWS::DynamoDB::GlobalTable": globalTable,
		"AWS::Kinesis::Stream": func(e *est, p map[string]any) {
			if e.str(asMap(p["StreamModeDetails"]), "StreamMode", "PROVISIONED") == "ON_DEMAND" {
				e.hourly(1, "", "kinesis|OnDemand-StreamHour", "on-demand stream")
				return
			}
			n := e.mustNum(p, "ShardCount")
			e.hourly(n, "shards", "kinesis|Storage-ShardHour", "provisioned shards")
			if e.num(p, "RetentionPeriodHours", 24) > 24 { // billed per shard hour, stored data or not
				e.hourly(n, "shards", "kinesis|Extended-ShardHour", "extended retention (over 24 hours)")
			}
		},
		"AWS::KMS::Key": func(e *est, p map[string]any) {
			e.monthly(1, "", "kms|KMS-Keys", "customer managed key")
		},
		"AWS::KMS::ReplicaKey": func(e *est, p map[string]any) {
			e.monthly(1, "", "kms|KMS-Keys", "customer managed key (replica)")
		},
		"AWS::SecretsManager::Secret": func(e *est, p map[string]any) {
			n, why := 1+float64(max(0, e.length(p, "ReplicaRegions"))), "secret"
			if n > 1 { // each replica is billed as a secret
				why = "secret and its replicas"
			}
			e.monthly(n, "", "secrets|AWSSecretsManager-Secrets", why)
		},
		"AWS::Route53::HostedZone": func(e *est, p map[string]any) {
			e.monthly(1, "", "r53|HostedZone", "hosted zone")
		},
		"AWS::EFS::FileSystem": func(e *est, p map[string]any) {
			if e.str(p, "ThroughputMode", "bursting") == "provisioned" {
				e.monthly(e.mustNum(p, "ProvisionedThroughputInMibps"), "MiB/s", "efs|ProvisionedTP-MiBpsHrs", "provisioned throughput")
			}
		},
		"AWS::FSx::FileSystem": fsx,
		"AWS::ECS::Service":    ecsService,
	}
}

// localZone matches Local Zone and Wavelength names (us-east-1-bos-1a), whose
// prices differ from the region's.
var localZone = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]+-[a-z0-9-]+[a-z]$`)

// notLocalZone stops the estimate of an instance placed in a Local Zone, by its
// AvailabilityZone or its subnet's.
func (e *est) notLocalZone(p map[string]any) {
	// Only literal zone names can be Local Zones; Fn::GetAZs picks the region's own.
	az, _ := p["AvailabilityZone"].(string)
	if r, ok := p["SubnetId"].(map[string]any); ok && az == "" {
		if sub, ok := e.st.Resources[fmt.Sprint(r["Ref"])]; ok {
			az, _ = sub.Properties["AvailabilityZone"].(string)
		}
	}
	if localZone.MatchString(az) {
		panic(unresolved("Local Zone " + az + " prices are not supported yet"))
	}
}

// autoPublicIP reports whether instances get a public IPv4 at launch: by
// AssociatePublicIpAddress when set, else by MapPublicIpOnLaunch of every
// subnet. A subnet outside this template, or a value that doesn't resolve,
// counts as no, so the instance itself is still estimated.
func (e *est) autoPublicIP(assoc any, subnets []any) bool {
	return e.maybe(func() bool {
		if assoc != nil {
			return e.flag(map[string]any{"AssociatePublicIpAddress": assoc}, "AssociatePublicIpAddress")
		}
		for _, s := range subnets {
			_, sub := e.ref(map[string]any{"SubnetId": s}, "SubnetId")
			if sub.Type != "AWS::EC2::Subnet" || !e.flag(sub.Properties, "MapPublicIpOnLaunch") {
				return false
			}
		}
		return len(subnets) > 0
	})
}

// hasEIP reports whether an Elastic IP in this template is attached to the
// instance; it replaces the auto-assigned address and is counted on its own.
func (e *est) hasEIP() bool {
	for _, r := range e.st.Resources {
		if (r.Type == "AWS::EC2::EIP" || r.Type == "AWS::EC2::EIPAssociation") && asMap(r.Properties["InstanceId"])["Ref"] == e.id {
			return true
		}
	}
	return false
}

// maybe returns f's answer, or false when f reads a value that doesn't resolve.
func (e *est) maybe(f func() bool) (yes bool) {
	defer func() {
		if v := recover(); v != nil {
			if _, ok := v.(unresolved); !ok {
				panic(v)
			}
			yes = false
		}
	}()
	return f()
}

func firstMap(v any) map[string]any {
	if l, _ := v.([]any); len(l) > 0 {
		return asMap(l[0])
	}
	return nil
}

// launchTemplate returns the LaunchTemplateData a LaunchTemplate specification
// points to (same template only).
func (e *est) launchTemplate(spec map[string]any) map[string]any {
	key := "LaunchTemplateId"
	if _, ok := spec[key]; !ok {
		key = "LaunchTemplateName"
	}
	_, lt := e.ref(spec, key)
	return asMap(lt.Properties["LaunchTemplateData"])
}

func (e *est) blockDevices(v any, count float64) {
	if v == nil {
		return // the root volume comes from the AMI; not counted
	}
	l, ok := v.([]any)
	if !ok {
		e.resolve("BlockDeviceMappings", v)
	}
	for _, d := range l {
		ebs := asMap(asMap(d)["Ebs"])
		if ebs == nil || ebs["VolumeSize"] == nil {
			continue // instance store, or size from the AMI/snapshot
		}
		t := e.str(ebs, "VolumeType", "")
		if t == "" {
			panic(unresolved("BlockDeviceMappings VolumeType is not set (it comes from the AMI)"))
		}
		e.ebs(t, e.num(ebs, "VolumeSize", 0), e.num(ebs, "Iops", 0), e.num(ebs, "Throughput", 0), count)
	}
}

// ebs adds the storage, IOPS and throughput above the free baseline of n volumes.
func (e *est) ebs(t string, size, iops, tput, n float64) {
	usage := map[string]string{"gp2": "gp2", "gp3": "gp3", "io1": "piops", "io2": "io2", "st1": "st1", "sc1": "sc1", "standard": ""}
	u, ok := usage[t]
	if !ok {
		panic(unresolved("EBS volume type " + t + " is not supported"))
	}
	key := "ebs|EBS:VolumeUsage"
	if u != "" {
		key += "." + u
	}
	e.monthly(size*n, "GB "+t, key, "EBS "+t+" storage")
	switch t {
	case "gp3":
		e.monthly(math.Max(0, iops-3000)*n, "IOPS", "ebs|EBS:VolumeP-IOPS.gp3", "gp3 IOPS above 3000")
		e.monthly(math.Max(0, tput-125)*n, "MiB/s", "ebs|EBS:VolumeP-Throughput.gp3", "gp3 throughput above 125 MiB/s")
	case "io1":
		e.monthly(iops*n, "IOPS", "ebs|EBS:VolumeP-IOPS.piops", "io1 provisioned IOPS")
	case "io2": // tiered per volume
		if iops <= 0 {
			return
		}
		t1, t2, t3 := math.Min(iops, 32000), math.Max(0, math.Min(iops, 64000)-32000), math.Max(0, iops-64000)
		e.add(iops*n, "IOPS", n*(t1*e.price("ebs|EBS:VolumeP-IOPS.io2")+t2*e.price("ebs|EBS:VolumeP-IOPS.io2.tier2")+t3*e.price("ebs|EBS:VolumeP-IOPS.io2.tier3")), "io2 provisioned IOPS")
	}
}

var rdsEngineCode = map[string]string{"mysql": "2", "postgres": "14", "mariadb": "18", "aurora": "16", "aurora-mysql": "16", "aurora-postgresql": "21"}

func rdsInstance(e *est, p map[string]any) {
	class := e.mustStr(p, "DBInstanceClass")
	engine := e.str(p, "Engine", "")
	var cluster map[string]any
	if _, ok := p["DBClusterIdentifier"]; ok {
		_, c := e.ref(p, "DBClusterIdentifier")
		cluster = c.Properties
		if engine == "" {
			engine = e.mustStr(cluster, "Engine")
		}
	}
	if engine == "" {
		if _, ok := p["SourceDBInstanceIdentifier"]; ok {
			_, src := e.ref(p, "SourceDBInstanceIdentifier")
			engine = e.mustStr(src.Properties, "Engine")
		} else {
			panic(unresolved("Engine is not set"))
		}
	}
	code, ok := rdsEngineCode[strings.ToLower(engine)]
	if !ok {
		code, ok = e.licensedEngineCode(strings.ToLower(engine), p)
	}
	if !ok {
		panic(unresolved("engine " + engine + " is not supported yet"))
	}
	if cluster != nil { // Aurora: storage is usage-based, only the instance counts
		iopt := e.str(cluster, "StorageType", "aurora") == "aurora-iopt1"
		suffix := ""
		if iopt {
			suffix = ", I/O-Optimized"
		}
		if class == "db.serverless" {
			sc := asMap(cluster["ServerlessV2ScalingConfiguration"])
			key := "rds|Aurora:ServerlessV2Usage|" + code
			if iopt {
				key = "rds|Aurora:ServerlessV2IOOptimizedUsage|" + code
			}
			min := e.mustNum(sc, "MinCapacity")
			if min == 0 { // shown at $0 so it reads as checked, not missed
				e.items = append(e.items, Item{Quantity: "×0 ACU", Reason: "Serverless v2 minimum capacity (auto-pause)" + suffix})
				return
			}
			e.hourly(min, "ACU", key, "Serverless v2 minimum capacity"+suffix)
			return
		}
		usage := "InstanceUsage"
		if iopt {
			usage = "InstanceUsageIOOptimized"
		}
		e.hourly(1, "", "rds|"+usage+":"+class+"|"+code, class+" "+engine+suffix)
		return
	}
	multi := e.flag(p, "MultiAZ")
	usage, maz, az := "InstanceUsage", "", "Single-AZ"
	if multi {
		usage, maz, az = "Multi-AZUsage", "Multi-AZ-", "Multi-AZ"
		if strings.HasPrefix(strings.ToLower(engine), "sqlserver") { // SQL Server Multi-AZ is priced as mirroring
			usage, maz = "MirrorUsage", "Mirror-"
		}
	}
	e.hourly(1, "", "rds|"+usage+":"+class+"|"+code, class+" "+engine+" "+az)
	size := e.mustNum(p, "AllocatedStorage")
	iops, tput := e.num(p, "Iops", 0), e.num(p, "StorageThroughput", 0)
	def := "gp3" // CloudFormation's default
	if iops > 0 {
		def = "io1"
	}
	t := e.str(p, "StorageType", def)
	k := func(s string) string { return "rds|RDS:" + maz + s + "|" + code }
	switch t {
	case "gp2":
		e.monthly(size, "GB gp2", k("GP2-Storage"), "storage")
	case "gp3":
		e.monthly(size, "GB gp3", k("GP3-Storage"), "storage")
		// Striped volumes from 400 GiB (200 GiB on Oracle) get a higher baseline;
		// SQL Server is never striped.
		bi, bt, stripe := 3000.0, 125.0, 400.0
		switch le := strings.ToLower(engine); {
		case strings.HasPrefix(le, "oracle"):
			stripe = 200
		case strings.HasPrefix(le, "sqlserver"):
			stripe = math.Inf(1)
		}
		if size >= stripe {
			bi, bt = 12000, 500
		}
		e.monthly(math.Max(0, iops-bi), "IOPS", k("GP3-PIOPS"), fmt.Sprintf("gp3 IOPS above %.0f", bi))
		e.monthly(math.Max(0, tput-bt), "MiB/s", k("GP3-Throughput"), fmt.Sprintf("gp3 throughput above %.0f MiB/s", bt))
	case "io1":
		e.monthly(size, "GB io1", k("PIOPS-Storage"), "storage")
		e.monthly(iops, "IOPS", k("PIOPS"), "provisioned IOPS")
	case "io2":
		e.monthly(size, "GB io2", k("PIOPS-Storage-IO2"), "storage")
		e.monthly(iops, "IOPS", k("IO2-PIOPS"), "provisioned IOPS")
	case "standard":
		e.monthly(size, "GB magnetic", k("StorageUsage"), "storage")
	default:
		panic(unresolved("StorageType " + t + " is not supported"))
	}
}

// licensedEngineCode returns the Price List engineCode of Oracle and SQL
// Server, which the license model decides along with the edition.
func (e *est) licensedEngineCode(engine string, p map[string]any) (string, bool) {
	engine = strings.TrimSuffix(engine, "-cdb") // the CDB architecture is priced the same
	byo := map[string]string{"oracle-se2": "19", "oracle-ee": "5", "sqlserver-se": "52", "sqlserver-ee": "53", "sqlserver-dev-ee": "51"}
	li := map[string]string{"oracle-se2": "20", "sqlserver-ex": "10", "sqlserver-web": "11", "sqlserver-se": "12", "sqlserver-ee": "15"}
	def := "license-included"
	if strings.HasPrefix(engine, "oracle") {
		def = "bring-your-own-license"
		if engine == "oracle-se2" {
			def = e.mustStr(p, "LicenseModel") // either is possible, and there is no default to rely on
		}
	}
	switch lm := e.str(p, "LicenseModel", def); lm {
	case "license-included":
		code, ok := li[engine]
		return code, ok
	case "bring-your-own-license", "bring-your-own-media":
		code, ok := byo[engine]
		return code, ok
	default:
		panic(unresolved("LicenseModel " + lm + " is not supported"))
	}
}

func rdsCluster(e *est, p map[string]any) {
	// Aurora clusters bill through their instances and usage-based storage.
	engine := strings.ToLower(e.str(p, "Engine", "aurora"))
	if strings.HasPrefix(engine, "aurora") {
		if e.str(p, "EngineMode", "provisioned") == "serverless" {
			panic(unresolved("Aurora Serverless v1 is not supported")) // billed per ACU on the cluster, not its instances
		}
		return
	}
	// A Multi-AZ DB cluster: one writer and two readable standbys, priced per cluster.
	code, ok := rdsEngineCode[engine]
	if !ok {
		panic(unresolved("engine " + engine + " is not supported yet"))
	}
	class := e.mustStr(p, "DBClusterInstanceClass")
	e.hourly(1, "", "rds|Multi-AZClusterUsage:"+class+"|"+code, class+" "+engine+" Multi-AZ DB cluster (3 instances)")
	size := e.mustNum(p, "AllocatedStorage")
	k := func(s string) string { return "rds|RDS:Multi-AZCluster-" + s + "|" + code }
	switch t := e.mustStr(p, "StorageType"); t {
	case "gp3":
		if p["Iops"] != nil || p["StorageThroughput"] != nil {
			panic(unresolved("gp3 IOPS and throughput of Multi-AZ DB clusters are not supported yet"))
		}
		e.monthly(size, "GB gp3", k("GP3-Storage"), "storage")
	case "io1":
		e.monthly(size, "GB io1", k("PIOPS-Storage"), "storage")
		e.monthly(e.mustNum(p, "Iops"), "IOPS", k("PIOPS"), "provisioned IOPS")
	case "io2":
		e.monthly(size, "GB io2", k("PIOPS-Storage-IO2"), "storage")
		e.monthly(e.mustNum(p, "Iops"), "IOPS", k("IO2-PIOPS"), "provisioned IOPS")
	default:
		panic(unresolved("StorageType " + t + " is not supported"))
	}
}

func (e *est) searchDomain(cc map[string]any, ccName string, p map[string]any) {
	norm := func(t string) string { // m5.large.elasticsearch is priced as m5.large.search
		if s, ok := strings.CutSuffix(t, ".elasticsearch"); ok {
			return s + ".search"
		}
		return t
	}
	t := norm(e.mustStr(cc, "InstanceType"))
	n := e.mustNum(cc, "InstanceCount")
	e.hourly(n, "", "es|"+t, t+" data nodes")
	if e.flag(cc, "DedicatedMasterEnabled") {
		m := norm(e.mustStr(cc, "DedicatedMasterType"))
		e.hourly(e.mustNum(cc, "DedicatedMasterCount"), "", "es|"+m, m+" dedicated master nodes")
	}
	if e.flag(cc, "WarmEnabled") {
		w := norm(e.mustStr(cc, "WarmType"))
		e.hourly(e.mustNum(cc, "WarmCount"), "", "es|"+w, w+" UltraWarm nodes")
	}
	ebs := asMap(p["EBSOptions"])
	if !e.flag(ebs, "EBSEnabled") {
		return
	}
	size := e.mustNum(ebs, "VolumeSize")
	vt := e.mustStr(ebs, "VolumeType")
	switch vt {
	case "gp2":
		e.monthly(size*n, "GB gp2", "es|ES:GP2-Storage", "EBS storage")
	case "gp3":
		e.monthly(size*n, "GB gp3", "es|ES:GP3-Storage", "EBS storage")
		// OpenSearch includes 3000 IOPS (3 per GiB above 1024 GiB) and 125 MiB/s (250 above 170 GiB).
		// ponytail: the extra 250 MiB/s per 3 TiB of very large volumes is not included.
		bi, bt := math.Max(3000, 3*size), 125.0
		if size > 170 {
			bt = 250
		}
		e.monthly(math.Max(0, e.num(ebs, "Iops", 0)-bi)*n, "IOPS", "es|ES:GP3-PIOPS", fmt.Sprintf("gp3 IOPS above %.0f", bi))
		e.monthly(math.Max(0, e.num(ebs, "Throughput", 0)-bt)*n, "MiB/s", "es|ES:GP3-Provisioned-ThroughPut", fmt.Sprintf("gp3 throughput above %.0f MiB/s", bt))
	case "io1":
		e.monthly(size*n, "GB io1", "es|ES:PIOPS-Storage", "EBS storage")
		e.monthly(e.num(ebs, "Iops", 0)*n, "IOPS", "es|ES:PIOPS", "provisioned IOPS")
	case "standard":
		e.monthly(size*n, "GB magnetic", "es|ES:Magnetic-Storage", "EBS storage")
	default:
		panic(unresolved("VolumeType " + vt + " is not supported"))
	}
}

func cacheEngine(engine string) string {
	switch strings.ToLower(engine) {
	case "redis":
		return "Redis"
	case "valkey":
		return "Valkey"
	case "memcached":
		return "Memcached"
	}
	panic(unresolved("engine " + engine + " is not supported"))
}

func (e *est) cacheNodes(engine, nodeType string, n float64) {
	en := cacheEngine(engine)
	e.hourly(n, "nodes", "elasticache|"+en+"|"+nodeType, nodeType+" "+en)
}

func fsx(e *est, p map[string]any) {
	t := e.mustStr(p, "FileSystemType")
	size := e.mustNum(p, "StorageCapacity")
	storageType := e.str(p, "StorageType", "SSD")
	switch t {
	case "WINDOWS":
		c := asMap(p["WindowsConfiguration"])
		multi := e.str(c, "DeploymentType", "SINGLE_AZ_1") == "MULTI_AZ_1"
		sk, tk, ik := "Storage", "ThroughputCapacity", "ProvisionedSSDIOPS"
		if storageType == "HDD" {
			sk = "Storage:HDD"
		}
		if multi {
			sk, tk, ik = "Storage.MultiAZ:"+storageType, "ThroughputCapacity.MultiAZ", "ProvisionedSSDIOPS.MultiAZ"
		}
		e.monthly(size, "GB "+storageType, "fsx|Windows|"+sk, "storage")
		e.monthly(e.mustNum(c, "ThroughputCapacity"), "MB/s", "fsx|Windows|"+tk, "throughput capacity")
		e.userIOPS(asMap(c["DiskIopsConfiguration"]), size, "fsx|Windows|"+ik)
	case "LUSTRE":
		c := asMap(p["LustreConfiguration"])
		dt := e.str(c, "DeploymentType", "SCRATCH_1")
		if strings.HasPrefix(dt, "SCRATCH") {
			e.monthly(size, "GB", "fsx|Lustre|Storage", "scratch storage")
			return
		}
		per := e.mustStr(c, "PerUnitStorageThroughput")
		k := "Storage." + storageType + "." + per
		if storageType == "HDD" && e.str(c, "DriveCacheType", "NONE") == "READ" {
			k += ".RC20"
		}
		e.monthly(size, "GB "+storageType, "fsx|Lustre|"+k, fmt.Sprintf("persistent storage (%s MB/s/TiB)", per))
	case "OPENZFS":
		c := asMap(p["OpenZFSConfiguration"])
		dt := e.mustStr(c, "DeploymentType")
		sfx, ok := map[string]string{"SINGLE_AZ_1": "SAZ", "SINGLE_AZ_2": "SAZ2", "SINGLE_AZ_HA_1": "SAZ_HA", "SINGLE_AZ_HA_2": "SAZ_HA2", "MULTI_AZ_1": "MAZ"}[dt]
		if !ok {
			panic(unresolved("DeploymentType " + dt + " is not supported yet"))
		}
		e.monthly(size, "GB SSD", "fsx|OpenZFS|Storage."+sfx+":SSD", "storage")
		e.monthly(e.mustNum(c, "ThroughputCapacity"), "MB/s", "fsx|OpenZFS|ThroughputCapacity."+sfx, "throughput capacity")
		e.userIOPS(asMap(c["DiskIopsConfiguration"]), size, "fsx|OpenZFS|ProvisionedSSDIOPS."+sfx)
	case "ONTAP":
		c := asMap(p["OntapConfiguration"])
		dt := e.mustStr(c, "DeploymentType")
		sfx, ok := map[string]string{"SINGLE_AZ_1": "SAZ_2N", "SINGLE_AZ_2": "SAZ_2N2", "MULTI_AZ_1": "MAZ", "MULTI_AZ_2": "MAZ2"}[dt]
		if !ok {
			panic(unresolved("DeploymentType " + dt + " is not supported yet"))
		}
		tput := e.num(c, "ThroughputCapacity", 0)
		if tput == 0 {
			tput = e.mustNum(c, "ThroughputCapacityPerHAPair") * e.num(c, "HAPairs", 1)
		}
		e.monthly(size, "GB SSD", "fsx|ONTAP|Storage."+sfx+":SSD", "SSD storage (capacity pool is pay-per-use)")
		e.monthly(tput, "MB/s", "fsx|ONTAP|ThroughputCapacity."+sfx, "throughput capacity")
		e.userIOPS(asMap(c["DiskIopsConfiguration"]), size, "fsx|ONTAP|ProvisionedSSDIOPS."+sfx)
	default:
		panic(unresolved("FSx for " + t + " is not supported yet"))
	}
}

// userIOPS bills SSD IOPS provisioned above the included 3 IOPS per GB.
func (e *est) userIOPS(c map[string]any, size float64, key string) {
	if e.str(c, "Mode", "AUTOMATIC") == "USER_PROVISIONED" {
		e.monthly(math.Max(0, e.mustNum(c, "Iops")-3*size), "IOPS", key, "SSD IOPS above 3 per GB")
	}
}

// globalTable bills the provisioned capacity of the replica in the stack's
// region. Writes can only be set as an auto scaling range, so the minimum is
// used; with two or more replicas they are billed as replicated writes.
// ponytail: other regions' replicas are only counted in the reason; pricing
// them needs a region per Item.
func globalTable(e *est, p map[string]any) {
	if e.str(p, "BillingMode", "PROVISIONED") != "PROVISIONED" {
		return
	}
	replicas, _ := p["Replicas"].([]any)
	var here map[string]any
	for _, r := range replicas {
		if e.mustStr(asMap(r), "Region") == e.st.Region {
			here = asMap(r)
		}
	}
	if here == nil {
		panic(unresolved("no replica in " + e.st.Region))
	}
	minWrite := func(m map[string]any) float64 {
		as := asMap(asMap(m["WriteProvisionedThroughputSettings"])["WriteCapacityAutoScalingSettings"])
		if as == nil {
			panic(unresolved("WriteProvisionedThroughputSettings is not set"))
		}
		return e.mustNum(as, "MinCapacity")
	}
	read := func(m map[string]any) float64 {
		rp := asMap(m["ReadProvisionedThroughputSettings"])
		if rp["ReadCapacityUnits"] != nil {
			return e.num(rp, "ReadCapacityUnits", 0)
		}
		as := asMap(rp["ReadCapacityAutoScalingSettings"])
		if as == nil {
			panic(unresolved("ReadProvisionedThroughputSettings is not set"))
		}
		return e.mustNum(as, "MinCapacity")
	}
	r, w := read(here), minWrite(p)
	replicaGSIs, _ := here["GlobalSecondaryIndexes"].([]any)
	for _, g := range replicaGSIs {
		r += read(asMap(g))
	}
	gsis, _ := p["GlobalSecondaryIndexes"].([]any)
	for _, g := range gsis {
		w += minWrite(asMap(g))
	}
	ia := ""
	if e.str(here, "TableClass", "STANDARD") == "STANDARD_INFREQUENT_ACCESS" {
		ia = "IA-"
	}
	wkey, wunit, what, sfx := "WriteCapacityUnit-Hrs", "WCU", "write capacity", ""
	if len(replicas) > 1 {
		wkey, wunit, what = "ReplWriteCapacityUnit-Hrs", "rWCU", "replicated write capacity"
		sfx = fmt.Sprintf(", replica in %s (%d in other regions not counted)", e.st.Region, len(replicas)-1)
	}
	e.hourly(r, "RCU", "ddb|"+ia+"ReadCapacityUnit-Hrs", "provisioned read capacity"+sfx)
	e.hourly(w, wunit, "ddb|"+ia+wkey, what+", auto scaling minimum"+sfx)
}

func ecsService(e *est, p map[string]any) {
	lt := e.str(p, "LaunchType", "")
	if lt == "" {
		// Only an all-FARGATE strategy is priced: splitting tasks by base and
		// weight across Spot or other providers is not supported.
		cps, _ := p["CapacityProviderStrategy"].([]any)
		for _, c := range cps {
			switch cp := e.str(asMap(c), "CapacityProvider", ""); cp {
			case "FARGATE":
				lt = cp
			case "FARGATE_SPOT":
				panic(unresolved("Fargate Spot is not supported"))
			default:
				panic(unresolved("capacity provider " + cp + " is not supported"))
			}
		}
		if lt == "" {
			panic(unresolved("LaunchType is not set (cluster default capacity provider)"))
		}
	}
	switch lt {
	case "EC2", "EXTERNAL":
		return // the instances are counted on their own
	case "FARGATE_SPOT":
		panic(unresolved("Fargate Spot is not supported"))
	}
	n := e.num(p, "DesiredCount", 1)
	_, td := e.ref(p, "TaskDefinition")
	tp := td.Properties
	plat := asMap(tp["RuntimePlatform"])
	pre := "" // x86 Linux
	switch os := e.str(plat, "OperatingSystemFamily", "LINUX"); {
	case os == "LINUX":
		if e.str(plat, "CpuArchitecture", "X86_64") == "ARM64" {
			pre = "ARM-"
		}
	case strings.HasPrefix(os, "WINDOWS_SERVER_"):
		pre = "Windows-"
	default:
		panic(unresolved("Fargate on " + os + " is not supported yet"))
	}
	vcpu := e.mustNum(tp, "Cpu") / 1024
	gb := e.mustNum(tp, "Memory") / 1024
	e.hourly(n*vcpu, "vCPU", "fargate|Fargate-"+pre+"vCPU-Hours:perCPU", "Fargate vCPU ("+count(n, "task")+")")
	if pre == "Windows-" {
		e.hourly(n*vcpu, "vCPU", "fargate|Fargate-Windows-OS-Hours:perCPU", "Fargate Windows OS license ("+count(n, "task")+")")
	}
	e.hourly(n*gb, "GB", "fargate|Fargate-"+pre+"GB-Hours", "Fargate memory ("+count(n, "task")+")")
	if es := e.num(asMap(tp["EphemeralStorage"]), "SizeInGiB", 20); es > 20 && pre != "Windows-" {
		e.hourly(n*(es-20), "GB", "fargate|Fargate-EphemeralStorage-GB-Hours", "Fargate ephemeral storage above 20 GB")
	}
	vpc := asMap(asMap(p["NetworkConfiguration"])["AwsvpcConfiguration"])
	if e.maybe(func() bool { return e.str(vpc, "AssignPublicIp", "DISABLED") == "ENABLED" }) {
		e.hourly(n, "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 per task ("+count(n, "task")+")")
	}
}

// notCounted are types that cost money with zero traffic but have no rule yet.
// They are listed in the report instead of being skipped silently.
var notCounted = []string{
	"AWS::WorkSpaces::Workspace", "AWS::AppStream::Fleet", "AWS::EC2::Host",
	"AWS::ElasticBeanstalk::Environment", "AWS::Lightsail::Instance", "AWS::Lightsail::Database",
	"AWS::MediaLive::Channel", "AWS::Shield::Protection",
	"AWS::Connect::PhoneNumber", // not in the Price List
	"AWS::Bedrock::ImportedModel", "AWS::CloudFront::DistributionTenant",
	"AWS::Route53Resolver::OutpostResolver", // priced per model copy, plan or Outpost
	"AWS::CloudWatch::OTelEnrichment", "AWS::GuardDuty::Detector", "AWS::SecurityHub::Hub", "AWS::SecurityHub::HubV2",
	"AWS::Config::ConfigurationRecorder", "AWS::Logs::Integration", // priced by the rest of the account
	"AWS::RDS::ReservedDBInstance", "AWS::ElastiCache::ReservedCacheNode", "AWS::MemoryDB::ReservedNode", // a commitment
}

func init() {
	more := map[string]func(e *est, p map[string]any){
		"AWS::EKS::Cluster": eksCluster,
		"AWS::EKS::Nodegroup": func(e *est, p map[string]any) {
			if e.str(p, "CapacityType", "ON_DEMAND") != "ON_DEMAND" {
				panic(unresolved("Spot node groups are not supported"))
			}
			if at := e.str(p, "AmiType", ""); strings.HasPrefix(at, "WINDOWS_") {
				panic(unresolved("Windows node groups (" + at + ") are not supported yet"))
			}
			sc := asMap(p["ScalingConfig"])
			n, why := e.num(sc, "DesiredSize", -1), "DesiredSize"
			if n < 0 {
				n, why = e.mustNum(sc, "MinSize"), "MinSize"
			}
			var t string
			if l, ok := p["InstanceTypes"].([]any); ok && len(l) == 1 {
				t = e.str(map[string]any{"InstanceTypes": l[0]}, "InstanceTypes", "")
			} else if lt, ok := p["LaunchTemplate"]; ok && p["InstanceTypes"] == nil {
				t = e.mustStr(e.launchTemplate(asMap(lt)), "InstanceType")
			} else {
				panic(unresolved("InstanceTypes must name exactly one type"))
			}
			e.hourly(n, "", "ec2|"+t, fmt.Sprintf("%s on-demand (Linux), %s", t, why))
		},
		"AWS::NetworkFirewall::Firewall": func(e *est, p map[string]any) {
			n := e.length(p, "SubnetMappings")
			if n < 0 {
				n = e.length(p, "AvailabilityZoneMappings")
			}
			if n < 0 {
				panic(unresolved("SubnetMappings is not set"))
			}
			e.hourly(float64(n), "endpoints", "nfw|Endpoint-Hour", "firewall endpoint per AZ")
		},
		"AWS::WAFv2::WebACL": func(e *est, p map[string]any) {
			e.monthly(1, "", "waf|WebACLV2", "web ACL")
			e.monthly(float64(max(0, e.length(p, "Rules"))), "rules", "waf|RuleV2", "rules (managed rule group fees not counted)")
		},
		"AWS::CloudWatch::Alarm": func(e *est, p map[string]any) {
			n, high := 1.0, e.num(p, "Period", 60) < 60
			if l, ok := p["Metrics"].([]any); ok {
				n = 0
				for _, m := range l {
					mm := asMap(m)
					if strings.Contains(fmt.Sprint(mm["Expression"]), "ANOMALY_DETECTION_BAND") {
						panic(unresolved("anomaly detection alarms are not supported yet"))
					}
					if ms := asMap(mm["MetricStat"]); ms != nil {
						n++
						high = high || e.num(ms, "Period", 60) < 60
					}
				}
			} else if p["Metrics"] != nil {
				e.length(p, "Metrics") // panics with the reason
			}
			if high {
				e.monthly(n, "metrics", "cw|CW:HighResAlarmMonitorUsage", "high-resolution alarm per metric")
				return
			}
			e.monthly(n, "metrics", "cw|CW:AlarmMonitorUsage", "alarm per metric")
		},
		"AWS::CloudWatch::CompositeAlarm": func(e *est, p map[string]any) {
			e.monthly(1, "", "cw|CW:CompositeAlarmMonitorUsage", "composite alarm")
		},
		"AWS::CodePipeline::Pipeline": func(e *est, p map[string]any) {
			switch t := e.mustStr(p, "PipelineType"); t {
			case "V1":
				e.monthly(1, "", "codepipeline|activePipeline", "active V1 pipeline")
			case "V2": // billed per action execution minute
			default:
				panic(unresolved("PipelineType " + t + " is not supported"))
			}
		},
		"AWS::ApiGateway::Stage": func(e *est, p map[string]any) {
			if e.flag(p, "CacheClusterEnabled") {
				size := e.str(p, "CacheClusterSize", "0.5")
				e.hourly(1, "", "apigw|cache:"+size, size+" GB cache cluster")
			}
		},
		"AWS::Lambda::Version": lambdaProvisioned,
		"AWS::Lambda::Alias":   lambdaProvisioned,
		"AWS::GlobalAccelerator::Accelerator": func(e *est, p map[string]any) {
			if e.str(p, "Enabled", "true") != "false" {
				e.hourly(1, "", "ga|Global-Accelerator-fixed-fee", "accelerator fixed fee")
			}
		},
		"AWS::EC2::TransitGatewayAttachment": func(e *est, p map[string]any) {
			e.hourly(1, "", "tgw|TransitGatewayVPC", "Transit Gateway VPC attachment")
		},
		"AWS::EC2::TransitGatewayVpcAttachment": func(e *est, p map[string]any) {
			e.hourly(1, "", "tgw|TransitGatewayVPC", "Transit Gateway VPC attachment")
		},
		"AWS::EC2::TransitGatewayPeeringAttachment": func(e *est, p map[string]any) {
			e.hourly(1, "", "tgw|TransitGatewayPeering", "Transit Gateway peering attachment")
		},
		"AWS::EC2::TransitGatewayConnect": func(e *est, p map[string]any) {
			e.hourly(1, "", "tgw|TransitGatewayConnect", "Transit Gateway Connect attachment")
		},
		"AWS::EC2::VPNConnection": func(e *est, p map[string]any) {
			e.hourly(1, "", "vpc|VPN-Usage-Hours:ipsec.1", "Site-to-Site VPN connection")
			if _, ok := p["TransitGatewayId"]; ok {
				e.hourly(1, "", "tgw|TransitGatewayVPN", "Transit Gateway VPN attachment")
			}
		},
		"AWS::EC2::ClientVpnTargetNetworkAssociation": func(e *est, p map[string]any) {
			e.hourly(1, "", "vpc|ClientVPN-EndpointHours", "Client VPN endpoint association")
		},
		"AWS::Route53Resolver::ResolverEndpoint": func(e *est, p map[string]any) {
			n := e.length(p, "IpAddresses")
			if n < 0 {
				panic(unresolved("IpAddresses is not set"))
			}
			e.hourly(float64(n), "ENIs", "resolver|ResolverNetworkInterface", "Resolver endpoint")
		},
		"AWS::DocDB::DBInstance": func(e *est, p map[string]any) {
			e.clusterInstance(p, "docdb", "iopt1", "ServerlessV2ScalingConfiguration", "DocumentDB:ServerlessUsage", "DocumentDB:ServerlessIOOptimizedUsage", "DCU")
		},
		"AWS::Neptune::DBInstance": func(e *est, p map[string]any) {
			e.clusterInstance(p, "neptune", "iopt1", "ServerlessScalingConfiguration", "Neptune:ServerlessUsage", "Neptune:ServerlessIOOptimizedUsage", "NCU")
		},
		"AWS::MSK::Cluster": func(e *est, p map[string]any) {
			b := asMap(p["BrokerNodeGroupInfo"])
			t := e.mustStr(b, "InstanceType") // kafka.m5.large, express.m7g.large
			n := e.mustNum(p, "NumberOfBrokerNodes")
			e.hourly(n, "brokers", "msk|"+strings.ToUpper(t[:1])+t[1:], t+" brokers")
			if strings.HasPrefix(t, "express.") {
				return // Express storage is pay-per-use
			}
			ebs := asMap(asMap(b["StorageInfo"])["EBSStorageInfo"])
			e.monthly(n*e.mustNum(ebs, "VolumeSize"), "GB", "msk|Kafka.Storage.GP2", "broker storage")
			if pt := asMap(ebs["ProvisionedThroughput"]); e.flag(pt, "Enabled") {
				e.monthly(n*math.Max(0, e.mustNum(pt, "VolumeThroughput")-250), "MiB/s", "msk|Kafka.Throughput", "storage throughput above 250 MiB/s")
			}
		},
		"AWS::MSK::ServerlessCluster": func(e *est, p map[string]any) {
			e.hourly(1, "", "msk|KafkaServerless-ClusterHours", "serverless cluster (partitions not counted)")
		},
		"AWS::Redshift::Cluster": func(e *est, p map[string]any) {
			n := 1.0
			if e.mustStr(p, "ClusterType") == "multi-node" {
				n = e.mustNum(p, "NumberOfNodes")
			}
			t := e.mustStr(p, "NodeType")
			e.hourly(n, "nodes", "redshift|"+t, t+" nodes")
		},
		"AWS::DAX::Cluster": func(e *est, p map[string]any) {
			t := e.mustStr(p, "NodeType")
			e.hourly(e.mustNum(p, "ReplicationFactor"), "nodes", "dax|"+t, t+" nodes")
		},
		"AWS::AmazonMQ::Broker": func(e *est, p map[string]any) {
			engine := map[string]string{"ACTIVEMQ": "ActiveMQ", "RABBITMQ": "RabbitMQ"}[strings.ToUpper(e.mustStr(p, "EngineType"))]
			deploy := map[string]string{"SINGLE_INSTANCE": "Single-AZ", "ACTIVE_STANDBY_MULTI_AZ": "Multi-AZ", "CLUSTER_MULTI_AZ": "Multi-AZ"}[e.mustStr(p, "DeploymentMode")]
			if engine == "" || deploy == "" {
				panic(unresolved("EngineType or DeploymentMode is not supported"))
			}
			t := e.mustStr(p, "HostInstanceType")
			e.hourly(1, "", "mq|"+engine+"|"+deploy+"|"+strings.TrimPrefix(t, "mq."), t+" "+engine+" "+deploy)
		},
		"AWS::Transfer::Server": func(e *est, p map[string]any) {
			n := e.length(p, "Protocols")
			if n < 0 {
				n = 1 // SFTP
			}
			e.hourly(float64(n), "protocols", "transfer|ProtocolHours", "server")
		},
		"AWS::AppRunner::Service": func(e *est, p map[string]any) {
			mem := e.str(asMap(p["InstanceConfiguration"]), "Memory", "2 GB")
			gb, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(mem, "GB")), 64)
			if err != nil {
				panic(unresolved(fmt.Sprintf("Memory is %q", mem)))
			}
			if !strings.HasSuffix(mem, "GB") {
				gb /= 1024 // MB
			}
			min := 1.0 // the default auto scaling configuration
			if _, ok := p["AutoScalingConfigurationArn"]; ok {
				_, as := e.ref(p, "AutoScalingConfigurationArn")
				min = e.num(as.Properties, "MinSize", 1)
			}
			e.hourly(min*gb, "GB", "apprunner|AppRunner-Provisioned-GB-hours", "provisioned instance memory ("+count(min, "instance")+")")
		},
	}
	for k, v := range more {
		rules[k] = v
	}
}

func lambdaProvisioned(e *est, p map[string]any) {
	pc := e.num(asMap(p["ProvisionedConcurrencyConfig"]), "ProvisionedConcurrentExecutions", 0)
	if pc == 0 {
		return
	}
	_, fn := e.ref(p, "FunctionName")
	mb := e.num(fn.Properties, "MemorySize", 128)
	key := "lambda|Lambda-Provisioned-Concurrency"
	if l, _ := fn.Properties["Architectures"].([]any); len(l) == 1 && l[0] == "arm64" {
		key += "-ARM"
	}
	e.add(pc, "concurrency", pc*mb/1024*hoursPerMonth*3600*e.price(key), fmt.Sprintf("provisioned concurrency (%g MB)", mb))
}

// clusterInstance prices a DocumentDB or Neptune instance: its class, or the
// serverless minimum capacity of its cluster.
func (e *est) clusterInstance(p map[string]any, svc, ioptType, scalingProp, serverlessKey, serverlessIOKey, unit string) {
	class := e.mustStr(p, "DBInstanceClass")
	_, c := e.ref(p, "DBClusterIdentifier")
	iopt := e.str(c.Properties, "StorageType", "standard") == ioptType
	if class == "db.serverless" {
		key := serverlessKey
		if iopt {
			key = serverlessIOKey
		}
		e.hourly(e.mustNum(asMap(c.Properties[scalingProp]), "MinCapacity"), unit, svc+"|"+key, "serverless minimum capacity")
		return
	}
	usage, sfx := "InstanceUsage", ""
	if iopt {
		usage, sfx = "InstanceUsageIOOptimized", ", I/O-Optimized"
	}
	e.hourly(1, "", svc+"|"+usage+":"+class, class+" instance"+sfx)
}

func init() {
	more := map[string]func(e *est, p map[string]any){
		"AWS::RDS::DBProxy":                     rdsProxy,
		"AWS::OpenSearchServerless::Collection": aossCollection,
		"AWS::MemoryDB::Cluster": func(e *est, p map[string]any) {
			engine := "Redis"
			if strings.EqualFold(e.str(p, "Engine", "redis"), "valkey") {
				engine = "Valkey"
			}
			t := e.mustStr(p, "NodeType")
			n := e.num(p, "NumShards", 1) * (e.num(p, "NumReplicasPerShard", 1) + 1)
			e.hourly(n, "nodes", "memorydb|"+engine+"|"+t, t+" "+engine)
		},
		"AWS::DirectoryService::MicrosoftAD": func(e *est, p map[string]any) {
			key := map[string]string{"Enterprise": "ds|MicrosoftAD-DC-Usage", "Standard": "ds|Std-MicrosoftAD-DC-Usage"}
			ed := e.str(p, "Edition", "Enterprise")
			if key[ed] == "" {
				panic(unresolved("Edition " + ed + " is not supported"))
			}
			// More domain controllers are added by API call, not in the template.
			e.hourly(2, "DCs", key[ed], ed+" Edition, 2 domain controllers")
		},
		"AWS::DirectoryService::SimpleAD": func(e *est, p map[string]any) {
			size := e.mustStr(p, "Size")
			if !in(size, "Small", "Large") {
				panic(unresolved("Size " + size + " is not supported"))
			}
			e.hourly(1, "", "ds|"+size+"-SimpleAD-Usage", size+" Simple AD")
		},
		"AWS::SageMaker::Endpoint": func(e *est, p map[string]any) {
			_, cfg := e.ref(p, "EndpointConfigName")
			l, ok := cfg.Properties["ProductionVariants"].([]any)
			if !ok || len(l) == 0 {
				panic(unresolved("ProductionVariants is not a list"))
			}
			for _, v := range l {
				v := asMap(v)
				if sc, ok := v["ServerlessConfig"]; ok {
					if e.num(asMap(sc), "ProvisionedConcurrency", 0) > 0 {
						panic(unresolved("serverless provisioned concurrency is not supported yet"))
					}
					continue // billed per request
				}
				t := e.mustStr(v, "InstanceType")
				e.hourly(e.mustNum(v, "InitialInstanceCount"), "", "sagemaker|"+t, t+" hosting ("+e.str(v, "VariantName", "variant")+")")
			}
		},
	}
	for k, v := range more {
		rules[k] = v
	}
}

// rdsProxy bills per vCPU of the provisioned instances behind the proxy, or
// per ACU of the Serverless v2 ones (their minimum, idle).
func rdsProxy(e *est, p map[string]any) {
	var vcpu, acu float64
	found := false
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if r.Type != "AWS::RDS::DBProxyTargetGroup" {
			continue
		}
		tp := e.props(r)
		if asMap(tp["DBProxyName"])["Ref"] != e.id {
			continue
		}
		found = true
		for _, key := range []string{"DBInstanceIdentifiers", "DBClusterIdentifiers"} {
			l, _ := tp[key].([]any)
			for i := range l {
				tid, target := e.ref(map[string]any{key: l[i]}, key)
				instances := []Resource{target}
				var cluster map[string]any
				if key == "DBClusterIdentifiers" {
					cluster, instances = target.Properties, nil
					for _, iid := range sortedIDs(e.st.Resources) {
						inst := e.st.Resources[iid]
						if inst.Type == "AWS::RDS::DBInstance" && asMap(inst.Properties["DBClusterIdentifier"])["Ref"] == tid {
							instances = append(instances, inst)
						}
					}
					if len(instances) == 0 {
						panic(unresolved("the instances of the target cluster are not in this template"))
					}
				}
				for _, inst := range instances {
					ip := e.props(inst)
					class := e.mustStr(ip, "DBInstanceClass")
					if class != "db.serverless" {
						vcpu += dbVCPU(class)
						continue
					}
					if cluster == nil {
						_, c := e.ref(ip, "DBClusterIdentifier")
						cluster = e.props(c)
					}
					acu += e.mustNum(asMap(cluster["ServerlessV2ScalingConfiguration"]), "MinCapacity")
				}
			}
		}
	}
	if !found {
		panic(unresolved("no DBProxyTargetGroup of this proxy in this template"))
	}
	e.hourly(vcpu, "vCPU", "rdsproxy|RDS:ProxyUsage", "per vCPU of the target instances")
	e.hourly(acu, "ACU", "rdsproxy|RDS:Proxy-ASv2-Usage", "per ACU of the Serverless v2 targets (minimum)")
}

// dbVCPU reads the vCPU count off an instance class: large is 2, xlarge 4,
// Nxlarge 4N, and the burstable micro/small/medium are 2.
// ponytail: naming rule, not data; metal and odd legacy classes are unresolved.
func dbVCPU(class string) float64 {
	size := class[strings.LastIndex(class, ".")+1:]
	if strings.HasPrefix(class, "db.t2.") && (size == "micro" || size == "small") {
		return 1
	}
	switch size {
	case "micro", "small", "medium", "large":
		return 2
	case "xlarge":
		return 4
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(size, "xlarge")); err == nil && strings.HasSuffix(size, "xlarge") {
		return 4 * float64(n)
	}
	panic(unresolved("vCPU count of " + class + " is not known"))
}

// aossCollection bills the minimum OCUs of a Classic collection. Collections
// of the same type share them, so only the first one in the stack is counted.
func aossCollection(e *est, p map[string]any) {
	if g := e.str(p, "CollectionGroupName", ""); g != "" {
		panic(unresolved("capacity is set by collection group " + g))
	}
	typ := e.str(p, "Type", "SEARCH")
	for _, id := range sortedIDs(e.st.Resources) {
		r := e.st.Resources[id]
		if id == e.id {
			break
		}
		if r.Type == e.r.Type && r.Properties["CollectionGroupName"] == nil && e.str(e.props(r), "Type", "SEARCH") == typ {
			e.add(1, "", 0, "shares the OCUs of "+e.st.DisplayPath(id))
			return
		}
	}
	ocu, why := 1.0, "minimum OCUs, standby replicas"
	if e.str(p, "StandbyReplicas", "ENABLED") == "DISABLED" {
		ocu, why = 0.5, "minimum OCUs, no standby replicas"
	}
	e.hourly(ocu, "OCU", "aoss|IndexingOCU", "indexing "+why)
	e.hourly(ocu, "OCU", "aoss|SearchOCU", "search "+why)
}

func init() {
	more := map[string]func(e *est, p map[string]any){
		"AWS::ACMPCA::CertificateAuthority": func(e *est, p map[string]any) {
			if e.str(p, "UsageMode", "GENERAL_PURPOSE") == "SHORT_LIVED_CERTIFICATE" {
				e.monthly(1, "", "pca|ShortLivedCertificatePrivateCA", "private CA (short-lived certificate mode)")
				return
			}
			e.monthly(1, "", "pca|PaidPrivateCA", "private CA (general-purpose mode)")
		},
		"AWS::DMS::ReplicationInstance": func(e *est, p map[string]any) {
			class := e.mustStr(p, "ReplicationInstanceClass")
			usage, az := "InstanceUsg:", "Single-AZ"
			if e.flag(p, "MultiAZ") {
				usage, az = "Multi-AZUsg:", "Multi-AZ"
			}
			// ponytail: each class includes some storage (50 or 100 GB); storage above it is not counted.
			e.hourly(1, "", "dms|"+usage+class, class+" "+az+" (storage above the included amount not counted)")
		},
		"AWS::SageMaker::NotebookInstance": func(e *est, p map[string]any) {
			t := e.mustStr(p, "InstanceType")
			e.hourly(1, "", "notebook|"+t, t+" notebook instance")
			e.monthly(e.num(p, "VolumeSizeInGB", 5), "GB", "notebook|VolumeUsage.gp2", "notebook storage")
		},
		"AWS::OSIS::Pipeline": func(e *est, p map[string]any) {
			e.hourly(e.mustNum(p, "MinUnits"), "OCU", "osis|IngestionOCU", "minimum Ingestion OCUs")
		},
		"AWS::EC2::CapacityReservation": func(e *est, p map[string]any) {
			if pl := e.mustStr(p, "InstancePlatform"); pl != "Linux/UNIX" {
				panic(unresolved("InstancePlatform " + pl + " is not supported yet"))
			}
			if e.str(p, "Tenancy", "default") != "default" {
				panic(unresolved("dedicated tenancy is not supported yet"))
			}
			t := e.mustStr(p, "InstanceType")
			e.hourly(e.mustNum(p, "InstanceCount"), "", "ec2|"+t, t+" reserved capacity (Linux)")
		},
		"AWS::Synthetics::Canary": func(e *est, p map[string]any) {
			if !e.flag(p, "StartCanaryAfterCreation") && p["StartCanaryAfterCreation"] != nil {
				return // created stopped
			}
			sch := asMap(p["Schedule"])
			if e.num(sch, "DurationInSeconds", 0) > 0 {
				panic(unresolved("the canary stops after Schedule.DurationInSeconds"))
			}
			expr := e.mustStr(sch, "Expression")
			m := canaryRate.FindStringSubmatch(expr)
			if m == nil {
				panic(unresolved("Schedule " + expr + " is not a rate() expression"))
			}
			n, _ := strconv.ParseFloat(m[1], 64)
			if n == 0 {
				return // rate(0 minutes) runs once
			}
			if strings.HasPrefix(m[2], "hour") {
				n *= 60
			}
			e.monthly(hoursPerMonth*60/n, "runs", "cw|CW:Canary-runs", "canary runs ("+expr+")")
		},
		"AWS::Kinesis::StreamConsumer": func(e *est, p map[string]any) {
			_, s := e.ref(p, "StreamARN")
			if e.str(asMap(s.Properties["StreamModeDetails"]), "StreamMode", "PROVISIONED") == "ON_DEMAND" {
				panic(unresolved("enhanced fan-out on on-demand streams is not supported yet"))
			}
			e.hourly(e.mustNum(s.Properties, "ShardCount"), "shards", "kinesis|EnhancedFanoutHour", "enhanced fan-out consumer per shard")
		},
		"AWS::KafkaConnect::Connector": func(e *est, p map[string]any) {
			c := asMap(p["Capacity"])
			if pc := asMap(c["ProvisionedCapacity"]); pc != nil {
				n := e.num(pc, "McuCount", 1) * e.mustNum(pc, "WorkerCount")
				e.hourly(n, "MCU", "msk|Kafka.mcu.general", "connector MCUs")
				return
			}
			as := asMap(c["AutoScaling"])
			n := e.num(as, "McuCount", 1) * e.mustNum(as, "MinWorkerCount")
			e.hourly(n, "MCU", "msk|Kafka.mcu.general", "connector MCUs (auto scaling minimum)")
		},
		"AWS::MSK::Replicator": func(e *est, p map[string]any) {
			e.hourly(1, "", "msk|KafkaReplication-ReplicatorHours", "replicator (data replicated not counted)")
		},
		"AWS::SSM::Parameter": func(e *est, p map[string]any) {
			if e.str(p, "Tier", "Standard") == "Advanced" {
				e.hourly(1, "", "ssm|PS-Advanced-Param-Tier1", "advanced parameter")
			}
		},
		"AWS::Cassandra::Table": func(e *est, p map[string]any) {
			bm := asMap(p["BillingMode"])
			if e.str(bm, "Mode", "ON_DEMAND") != "PROVISIONED" {
				return
			}
			pt := asMap(bm["ProvisionedThroughput"])
			e.hourly(e.mustNum(pt, "ReadCapacityUnits"), "RCU", "mcs|ReadCapacityUnit-Hrs", "provisioned read capacity")
			e.hourly(e.mustNum(pt, "WriteCapacityUnits"), "WCU", "mcs|WriteCapacityUnit-Hrs", "provisioned write capacity")
		},
		"AWS::MWAA::Environment": mwaa,
		"AWS::AppSync::ApiCache": func(e *est, p map[string]any) {
			t := e.mustStr(p, "Type")
			gb, ok := appsyncCacheGB[t]
			if !ok {
				panic(unresolved("Type " + t + " is not supported"))
			}
			e.hourly(1, "", "appsync|cache:"+gb, t+" cache ("+gb+" GB)")
		},
		"AWS::HealthLake::FHIRDatastore": func(e *est, p map[string]any) {
			e.hourly(1, "", "healthlake|FHIRDataStore", "FHIR data store")
		},
		"AWS::Route53RecoveryControl::Cluster": func(e *est, p map[string]any) {
			e.hourly(1, "", "arc|Global-Cluster-Hours", "routing control cluster")
		},
		"AWS::Route53RecoveryReadiness::ReadinessCheck": func(e *est, p map[string]any) {
			e.hourly(1, "", "arc|Global-ReadinessCheck-Hours", "readiness check")
		},
		"AWS::VpcLattice::Service": func(e *est, p map[string]any) {
			e.hourly(1, "", "vpc|VPCLattice-Service-Hourly", "VPC Lattice service (requests and data not counted)")
		},
		"AWS::DocDBElastic::Cluster": func(e *est, p map[string]any) {
			vcpu := e.mustNum(p, "ShardCapacity") * e.mustNum(p, "ShardCount") * e.mustNum(p, "ShardInstanceCount")
			e.hourly(vcpu, "vCPU", "docdb|ElasticCPUUsage", "shard vCPUs × shards × instances per shard")
		},
		"AWS::Timestream::InfluxDBInstance": func(e *est, p map[string]any) {
			t := e.mustStr(p, "DbInstanceType") // db.influx.large
			usage, az, label := "InstanceUsage-", "SingleAZ-", "Single-AZ"
			if e.str(p, "DeploymentType", "SINGLE_AZ") == "WITH_MULTIAZ_STANDBY" {
				usage, az, label = "MultiAZUsage-", "MultiAZ-", "Multi-AZ"
			}
			e.hourly(1, "", "influx|"+usage+"D"+strings.TrimPrefix(t, "d"), t+" "+label)
			st := e.str(p, "DbStorageType", "InfluxIOIncludedT1")
			e.monthly(e.mustNum(p, "AllocatedStorage"), "GB", "influx|"+az+"InfluxDBStorage-"+st, st+" storage")
		},
	}
	for k, v := range more {
		rules[k] = v
	}
}

var canaryRate = regexp.MustCompile(`^rate\(\s*(\d+)\s+(minutes?|hours?)\s*\)$`)

// mwaa bills the environment class, which includes one worker, two schedulers
// and two web servers (one each on mw1.micro), plus the minimum above those.
func mwaa(e *est, p map[string]any) {
	class := e.str(p, "EnvironmentClass", "mw1.small")
	name, ok := map[string]string{"mw1.micro": "Micro", "mw1.small": "Small", "mw1.medium": "Medium", "mw1.large": "Large", "mw1.xlarge": "XLarge", "mw1.2xlarge": "2XLarge"}[class]
	if !ok {
		panic(unresolved("EnvironmentClass " + class + " is not supported"))
	}
	inc := 2.0
	if name == "Micro" {
		inc = 1
	}
	e.hourly(1, "", "mwaa|"+name+"Environment", class+" environment")
	e.hourly(e.num(p, "MinWorkers", 1)-1, "workers", "mwaa|"+name+"Worker", "workers above the included one (MinWorkers)")
	e.hourly(e.num(p, "Schedulers", inc)-inc, "schedulers", "mwaa|"+name+"Scheduler", "schedulers above the included ones")
	e.hourly(e.num(p, "MinWebservers", inc)-inc, "web servers", "mwaa|"+name+"WebServer", "web servers above the included ones (MinWebservers)")
}

// appsyncCacheGB maps an AppSync cache Type to the memory size the Price List keys it by.
var appsyncCacheGB = map[string]string{
	"SMALL": "1.55", "MEDIUM": "3.22", "LARGE": "12.3", "XLARGE": "25.05", "LARGE_2X": "50.47", "LARGE_4X": "101.38", "LARGE_8X": "203.26", "LARGE_12X": "317.77",
	"T2_SMALL": "1.55", "T2_MEDIUM": "3.22", "R4_LARGE": "12.3", "R4_XLARGE": "25.05", "R4_2XLARGE": "50.47", "R4_4XLARGE": "101.38", "R4_8XLARGE": "203.26",
}

func init() {
	rules["AWS::EMR::Cluster"] = func(e *est, p map[string]any) {
		if why := e.emrTransient(p); why != "" {
			e.items = append(e.items, Item{Quantity: "×0", Reason: why})
			return
		}
		in := asMap(p["Instances"])
		if in["MasterInstanceFleet"] != nil || in["CoreInstanceFleet"] != nil {
			panic(unresolved("instance fleets are not supported yet"))
		}
		e.emrGroup(asMap(in["MasterInstanceGroup"]), "primary")
		if g := asMap(in["CoreInstanceGroup"]); g != nil {
			e.emrGroup(g, "core")
		}
	}
	rules["AWS::EMR::InstanceGroupConfig"] = func(e *est, p map[string]any) {
		if id, ok := asMap(p["JobFlowId"])["Ref"].(string); ok {
			if c, ok := e.st.Resources[id]; ok && e.emrTransient(e.props(c)) != "" {
				return // shown on the cluster
			}
		}
		e.emrGroup(p, strings.ToLower(e.mustStr(p, "InstanceRole")))
	}
}

// emrTransient says why a cluster does not stay up while idle, or "".
func (e *est) emrTransient(p map[string]any) string {
	if p["AutoTerminationPolicy"] != nil {
		return "terminates after its AutoTerminationPolicy idle timeout"
	}
	if in := asMap(p["Instances"]); in["KeepJobFlowAliveWhenNoSteps"] != nil && !e.flag(in, "KeepJobFlowAliveWhenNoSteps") {
		return "terminates when its steps finish (KeepJobFlowAliveWhenNoSteps: false)"
	}
	return ""
}

// emrGroup prices the instances of an EMR instance group: EC2 (Linux), the EMR
// fee, and the EBS volumes attached to each. The root volume is not counted.
func (e *est) emrGroup(g map[string]any, role string) {
	if e.str(g, "Market", "ON_DEMAND") != "ON_DEMAND" {
		panic(unresolved("Spot instance groups are not supported"))
	}
	t := e.mustStr(g, "InstanceType")
	n := e.mustNum(g, "InstanceCount")
	e.hourly(n, "", "ec2|"+t, t+" "+role+" instances (Linux)")
	e.hourly(n, "", "emr|"+t, "EMR fee, "+role)
	l, _ := asMap(g["EbsConfiguration"])["EbsBlockDeviceConfigs"].([]any)
	for _, c := range l {
		c := asMap(c)
		vs := asMap(c["VolumeSpecification"])
		e.ebs(e.mustStr(vs, "VolumeType"), e.mustNum(vs, "SizeInGB"), e.num(vs, "Iops", 0), e.num(vs, "Throughput", 0), n*e.num(c, "VolumesPerInstance", 1))
	}
}

// eksSupport holds the end of standard and of extended support of each EKS
// Kubernetes version (UTC), from the Amazon EKS release calendar.
// ponytail: a table to update when EKS releases a version; versions newer than
// the table are taken to be in standard support.
var eksSupport = map[string][2]string{
	"1.31": {"2025-11-26", "2026-11-26"}, "1.32": {"2026-03-23", "2027-03-23"}, "1.33": {"2026-07-29", "2027-07-29"},
	"1.34": {"2026-12-02", "2027-12-02"}, "1.35": {"2027-03-27", "2028-03-27"}, "1.36": {"2027-08-02", "2028-08-02"},
	"1.37": {"2027-12-01", "2028-12-01"},
}

// eksCluster bills the cluster hour, plus the extended support fee when its
// version is past the end of standard support on the date of the prices (the
// snapshot date, or today with --live), so a version always gives the same total.
func eksCluster(e *est, p map[string]any) {
	e.hourly(1, "", "eks|AmazonEKS-Hours:perCluster", "EKS cluster")
	v := e.str(p, "Version", "")
	if v == "" {
		return // the latest version, in standard support
	}
	day := e.prices.AsOf
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	s, ok := eksSupport[v]
	if !ok {
		minor, err := strconv.Atoi(strings.TrimPrefix(v, "1."))
		if err != nil || minor < 31 {
			panic(unresolved("Kubernetes version " + v + " is not supported by EKS"))
		}
		return // newer than the table
	}
	switch {
	case day >= s[1]:
		panic(unresolved("Kubernetes " + v + " reached the end of extended support on " + s[1]))
	case day < s[0]:
		return
	case e.str(asMap(p["UpgradePolicy"]), "SupportType", "EXTENDED") == "STANDARD":
		panic(unresolved("Kubernetes " + v + " reached the end of standard support on " + s[0] + " and SupportType is STANDARD"))
	}
	e.hourly(1, "", "eks|AmazonEKS-Hours:extendedSupport", "extended support for Kubernetes "+v+" (since "+s[0]+")")
}

func init() {
	rules["AWS::KinesisAnalyticsV2::Application"] = flinkApp
	rules["AWS::KinesisAnalytics::Application"] = func(e *est, p map[string]any) {
		panic(unresolved("SQL applications are not supported"))
	}
	rules["AWS::NetworkManager::CoreNetwork"] = coreNetwork
	rules["AWS::EC2::EC2Fleet"] = func(e *est, p map[string]any) {
		tc := asMap(p["TargetCapacitySpecification"])
		if u := e.str(tc, "TargetCapacityUnitType", "units"); u != "units" {
			panic(unresolved("TargetCapacityUnitType " + u + " is not supported"))
		}
		total := e.mustNum(tc, "TotalTargetCapacity")
		od, spot := e.num(tc, "OnDemandTargetCapacity", 0), e.num(tc, "SpotTargetCapacity", 0)
		if rest := total - od - spot; rest > 0 { // the rest is of the default type
			switch d := e.mustStr(tc, "DefaultTargetCapacityType"); d {
			case "on-demand":
				od += rest
			case "spot":
				spot += rest
			default:
				panic(unresolved("DefaultTargetCapacityType " + d + " is not supported"))
			}
		}
		e.fleet(od, spot, e.fleetLaunches(p["LaunchTemplateConfigs"]))
	}
	rules["AWS::EC2::SpotFleet"] = func(e *est, p map[string]any) {
		c := asMap(p["SpotFleetRequestConfigData"])
		if u := e.str(c, "TargetCapacityUnitType", "units"); u != "units" {
			panic(unresolved("TargetCapacityUnitType " + u + " is not supported"))
		}
		od := e.num(c, "OnDemandTargetCapacity", 0)
		spot := e.mustNum(c, "TargetCapacity") - od
		if specs, ok := c["LaunchSpecifications"].([]any); ok {
			var ls []fleetLaunch
			for _, s := range specs {
				s := asMap(s)
				ls = append(ls, fleetLaunch{e.mustStr(s, "InstanceType"), e.num(s, "WeightedCapacity", 1), s["BlockDeviceMappings"]})
			}
			e.fleet(od, spot, ls)
			return
		}
		e.fleet(od, spot, e.fleetLaunches(c["LaunchTemplateConfigs"]))
	}
	rules["AWS::Route53::HealthCheck"] = healthCheck
}

// flinkApp bills a Managed Service for Apache Flink application while it runs.
// CloudFormation creates it stopped (READY), but it is deployed to be started.
// A running application uses its KPUs (parallelism / parallelism per KPU), one
// more for orchestration (two for a Studio notebook), and 50 GB of running
// storage per KPU (one KPU more for a Studio notebook).
func flinkApp(e *est, p map[string]any) {
	rt := e.mustStr(p, "RuntimeEnvironment")
	if strings.HasPrefix(rt, "SQL") {
		panic(unresolved("SQL applications are not supported"))
	}
	studio := e.str(p, "ApplicationMode", "STREAMING") == "INTERACTIVE"
	pc := asMap(asMap(asMap(p["ApplicationConfiguration"])["FlinkApplicationConfiguration"])["ParallelismConfiguration"])
	par, per := 1.0, 1.0 // DEFAULT
	if e.str(pc, "ConfigurationType", "DEFAULT") == "CUSTOM" {
		par, per = e.num(pc, "Parallelism", 1), e.num(pc, "ParallelismPerKPU", 1)
	} else if studio {
		panic(unresolved("the parallelism of the Studio notebook is not set"))
	}
	kpu := math.Ceil(par / per)
	if studio {
		e.hourly(kpu+2, "KPU", "kda|KPU-Hour-Interactive", fmt.Sprintf("Studio notebook: %g KPU + 2 for orchestration and the notebook, while running", kpu))
		e.monthly((kpu+1)*50, "GB", "kda|RunningApplicationStorage-Interactive", "running application storage, 50 GB per KPU")
		return
	}
	e.hourly(kpu+1, "KPU", "kda|KPU-Hour-Java", fmt.Sprintf("%g KPU + 1 for orchestration, while running (created stopped)", kpu))
	e.monthly(kpu*50, "GB", "kda|RunningApplicationStorage", "running application storage, 50 GB per KPU")
}

// coreNetwork bills the hourly fee of each edge location in the core network policy.
// ponytail: every edge is priced in the stack's region (all regions are $0.50
// today); pricing each in its own region needs a region per Item.
func coreNetwork(e *est, p map[string]any) {
	doc := p["PolicyDocument"]
	if doc == nil {
		panic(unresolved("PolicyDocument is not set (the edges come from the policy)"))
	}
	if m, ok := doc.(map[string]any); ok && len(m) == 1 {
		for k := range m {
			if k == "Ref" || strings.HasPrefix(k, "Fn::") {
				doc = e.resolve("PolicyDocument", doc)
			}
		}
	}
	if s, ok := doc.(string); ok {
		if err := json.Unmarshal([]byte(s), &doc); err != nil {
			panic(unresolved("PolicyDocument is not JSON"))
		}
	}
	n := e.length(asMap(asMap(doc)["core-network-configuration"]), "edge-locations")
	if n < 0 {
		panic(unresolved("the policy has no edge-locations"))
	}
	e.hourly(float64(n), "edges", "wan|CloudWAN-CoreNetworkEdge-Hours", "core network edge per location")
}

// fleetLaunch is one instance type a fleet can launch.
type fleetLaunch struct {
	typ    string
	weight float64 // capacity units per instance
	bdm    any
}

// fleetLaunches reads the instance types of LaunchTemplateConfigs: each
// override's, else the launch template's.
func (e *est) fleetLaunches(v any) []fleetLaunch {
	cfgs, ok := v.([]any)
	if !ok {
		panic(unresolved("LaunchTemplateConfigs is not set"))
	}
	var out []fleetLaunch
	for _, c := range cfgs {
		c := asMap(c)
		data := e.launchTemplate(asMap(c["LaunchTemplateSpecification"]))
		ovs, _ := c["Overrides"].([]any)
		if len(ovs) == 0 {
			out = append(out, fleetLaunch{e.mustStr(data, "InstanceType"), 1, data["BlockDeviceMappings"]})
		}
		for _, o := range ovs {
			o := asMap(o)
			if o["InstanceRequirements"] != nil {
				panic(unresolved("attribute-based instance type selection is not supported"))
			}
			t := e.str(o, "InstanceType", "")
			if t == "" {
				t = e.mustStr(data, "InstanceType")
			}
			out = append(out, fleetLaunch{t, e.num(o, "WeightedCapacity", 1), data["BlockDeviceMappings"]})
		}
	}
	return out
}

// fleet bills the on-demand instances of an EC2 Fleet or Spot Fleet, when they
// can be of only one instance type. Spot capacity has no fixed price.
func (e *est) fleet(od, spot float64, launches []fleetLaunch) {
	if od <= 0 {
		if spot > 0 {
			panic(unresolved("Spot capacity is not supported"))
		}
		return
	}
	if len(launches) == 0 {
		panic(unresolved("no instance type"))
	}
	l := launches[0]
	for _, x := range launches[1:] {
		if x.typ != l.typ || x.weight != l.weight {
			panic(unresolved("the fleet can launch several instance types"))
		}
	}
	n := math.Ceil(od / l.weight)
	why := "on-demand capacity"
	if spot > 0 {
		why += fmt.Sprintf(" (%g Spot not counted)", spot)
	}
	e.hourly(n, "", "ec2|"+l.typ, l.typ+" on-demand (Linux), "+why)
	e.blockDevices(l.bdm, n)
}

// healthCheck bills a Route 53 health check and its optional features. An
// endpoint counts as AWS when it is a resource in this template, else as
// non-AWS. Calculated and metric based checks are billed as AWS endpoints.
func healthCheck(e *est, p map[string]any) {
	c := asMap(p["HealthCheckConfig"])
	t := e.mustStr(c, "Type")
	if in(t, "CALCULATED", "CLOUDWATCH_METRIC", "RECOVERY_CONTROL") {
		e.monthly(1, "", "r53hc|Health-Check-AWS", t+" health check (first 50 AWS-endpoint checks are free)")
		return
	}
	sfx, where := "-Non-AWS", "non-AWS endpoint (not in this template)"
	for _, k := range []string{"IPAddress", "FullyQualifiedDomainName"} {
		m := asMap(c[k])
		ref, _ := m["Ref"].(string)
		if _, ok := e.st.Resources[ref]; ok || m["Fn::GetAtt"] != nil {
			sfx, where = "-AWS", "AWS endpoint (first 50 are free)"
		}
	}
	var opts []string
	if strings.HasPrefix(t, "HTTPS") {
		opts = append(opts, "HTTPS")
	}
	if strings.HasSuffix(t, "STR_MATCH") {
		opts = append(opts, "string matching")
	}
	if e.num(c, "RequestInterval", 30) == 10 {
		opts = append(opts, "fast interval")
	}
	if e.flag(c, "MeasureLatency") {
		opts = append(opts, "latency measurement")
	}
	e.monthly(1, "", "r53hc|Health-Check"+sfx, t+" health check, "+where)
	e.monthly(float64(len(opts)), "features", "r53hc|Health-Check-Option"+sfx, "optional features: "+strings.Join(opts, ", "))
}

func init() {
	rules["AWS::Kendra::Index"] = kendraIndex
	rules["AWS::EC2::VerifiedAccessEndpoint"] = func(e *est, p map[string]any) {
		t, proto := e.mustStr(p, "EndpointType"), "tcp" // rds and cidr endpoints are TCP
		switch t {
		case "load-balancer":
			proto = e.mustStr(asMap(p["LoadBalancerOptions"]), "Protocol")
		case "network-interface":
			proto = e.mustStr(asMap(p["NetworkInterfaceOptions"]), "Protocol")
		}
		if proto == "tcp" {
			e.hourly(1, "", "vpc|VerifiedAccess-non-HTTP-App-Hours", "Verified Access "+t+" endpoint, non-HTTP application")
			return
		}
		e.hourly(1, "", "vpc|VerifiedAccess-App-Hours", "Verified Access "+t+" endpoint, HTTP application")
	}
	rules["AWS::CloudWatch::InsightRule"] = func(e *est, p map[string]any) {
		if e.str(p, "RuleState", "ENABLED") == "ENABLED" {
			e.monthly(1, "", "cw|CW:ContributorInsightRules", "Contributor Insights rule (matched log events not counted)")
		}
	}
	rules["AWS::SageMaker::MlflowTrackingServer"] = func(e *est, p map[string]any) {
		size := e.str(p, "TrackingServerSize", "Small")
		e.hourly(1, "", "mlflow|"+size, size+" MLflow tracking server")
	}
	rules["AWS::NeptuneGraph::Graph"] = func(e *est, p map[string]any) {
		m, r := e.mustNum(p, "ProvisionedMemory"), e.num(p, "ReplicaCount", 1)
		e.hourly(1+r, "", "graph|"+strconv.FormatFloat(m, 'f', -1, 64), fmt.Sprintf("Neptune Analytics graph of %g m-NCU, primary and %g replicas", m, r))
	}
	rules["AWS::GameLift::Fleet"] = gameLiftFleet
	rules["AWS::Batch::ComputeEnvironment"] = func(e *est, p map[string]any) {
		c := asMap(p["ComputeResources"])
		if strings.EqualFold(e.str(p, "Type", "MANAGED"), "UNMANAGED") || strings.HasPrefix(e.str(c, "Type", ""), "FARGATE") {
			return // your own instances, or Fargate tasks billed while jobs run
		}
		if v := e.num(c, "MinvCpus", 0); v > 0 {
			panic(unresolved(fmt.Sprintf("MinvCpus %g keeps instances running, of types Batch chooses", v)))
		}
	}
}

// kendraIndex bills the base capacity of an index and its additional capacity units.
func kendraIndex(e *est, p map[string]any) {
	cu := asMap(p["CapacityUnits"])
	q, s := e.num(cu, "QueryCapacityUnits", 0), e.num(cu, "StorageCapacityUnits", 0)
	switch ed := e.mustStr(p, "Edition"); ed {
	case "DEVELOPER_EDITION":
		e.hourly(1, "", "kendra|KendraDeveloperEdition", "Developer Edition index")
	case "ENTERPRISE_EDITION":
		e.hourly(1, "", "kendra|Kendra-Enterprise-Edition", "Enterprise Edition index")
		e.hourly(q, "units", "kendra|Kendra-Query-Capacity-Unit", "additional query capacity")
		e.hourly(s, "units", "kendra|Kendra-Storage-Capacity-Unit", "additional storage capacity")
	case "GEN_AI_ENTERPRISE_EDITION":
		e.hourly(1, "", "kendra|Kendra-GenAI-Ent-Base-Capacity", "GenAI Enterprise Edition index")
		e.hourly(q, "units", "kendra|Kendra-GenAI-Ent-Addl-QueryCap", "additional query capacity")
		e.hourly(s, "units", "kendra|Kendra-GenAI-Ent-Addl-StorageCap", "additional storage capacity")
	default:
		panic(unresolved("Edition " + ed + " is not supported"))
	}
}

// gameLiftFleet bills the on-demand instances of a managed EC2 fleet in its
// home Region. The OS comes from the build (Realtime scripts run on Linux).
func gameLiftFleet(e *est, p map[string]any) {
	if e.str(p, "ComputeType", "EC2") == "ANYWHERE" {
		return // your own compute
	}
	if e.str(p, "FleetType", "ON_DEMAND") == "SPOT" {
		panic(unresolved("Spot fleets are not supported"))
	}
	t := e.mustStr(p, "EC2InstanceType")
	n := e.num(p, "DesiredEC2Instances", -1)
	locs, _ := p["Locations"].([]any)
	for _, l := range locs {
		l := asMap(l)
		if loc := e.mustStr(l, "Location"); loc != e.st.Region {
			panic(unresolved("remote location " + loc + " is not supported"))
		}
		if c := asMap(l["LocationCapacity"]); n < 0 && c["DesiredEC2Instances"] != nil {
			n = e.num(c, "DesiredEC2Instances", 1)
		}
	}
	if n < 0 {
		n = 1 // GameLift starts one instance
	}
	os := "Linux"
	if _, ok := p["ScriptId"]; !ok {
		_, b := e.ref(p, "BuildId")
		if strings.HasPrefix(e.mustStr(b.Properties, "OperatingSystem"), "WINDOWS") { // no default
			os = "Windows"
		}
	}
	e.hourly(n, "", "gamelift|"+os+"|"+t, t+" GameLift instance ("+os+")")
}

func init() {
	rules["AWS::Lambda::CapacityProvider"] = capacityProvider
	rules["AWS::Lambda::EventSourceMapping"] = func(e *est, p map[string]any) {
		if n := e.num(asMap(p["ProvisionedPollerConfig"]), "MinimumPollers", 0); n > 0 {
			panic(unresolved(fmt.Sprintf("%g provisioned event pollers are not supported yet", n)))
		}
	}
	rules["AWS::CloudWatch::Dashboard"] = func(e *est, p map[string]any) {
		// ponytail: dashboards are not in the Price List; update by hand if the $3 list price changes.
		e.add(1, "", 3, "dashboard ($3 list price, not in the Price List)")
	}
}

// capacityProvider bills the instances Lambda Managed Instances keeps running:
// enough for the minimum execution environments of the functions that use the
// provider, in any input stack of its region, and at least one per environment
// up to three (one per AZ). Each instance pays EC2 plus the management fee.
// ponytail: a lower bound; Lambda also keeps headroom for traffic to double,
// and packs environments onto instances in ways the template doesn't say.
func capacityProvider(e *est, p map[string]any) {
	l, _ := asMap(p["InstanceRequirements"])["AllowedInstanceTypes"].([]any)
	if len(l) != 1 {
		panic(unresolved("Lambda chooses the instance type; set AllowedInstanceTypes to one type to estimate it"))
	}
	t := e.str(map[string]any{"AllowedInstanceTypes": l[0]}, "AllowedInstanceTypes", "")
	var gibPerVCPU float64
	if t != "" {
		gibPerVCPU = map[byte]float64{'c': 2, 'm': 4, 'r': 8}[t[0]]
	}
	if gibPerVCPU == 0 || strings.Contains(t, "*") {
		panic(unresolved("instance type " + t + " is not supported yet"))
	}
	vcpu := dbVCPU(t)
	var envs, needVCPU, needGiB float64
	fns := 0
	for _, s := range e.all {
		if s.Region != e.st.Region {
			continue
		}
		fe := &est{st: s, all: e.all, prices: e.prices}
		for _, id := range sortedIDs(s.Resources) {
			r := s.Resources[id]
			if r.Type != "AWS::Lambda::Function" {
				continue
			}
			if r.Condition != "" {
				if on, ok := fe.tryCondition(r.Condition); ok && !on {
					continue
				}
			}
			fp := fe.props(r)
			cfg := asMap(asMap(fp["CapacityProviderConfig"])["LambdaManagedInstancesCapacityProviderConfig"])
			if !e.pointsHere(s, cfg["CapacityProviderArn"]) {
				continue
			}
			fns++
			n := fe.num(asMap(fp["FunctionScalingConfig"]), "MinExecutionEnvironments", 3)
			gib := fe.num(fp, "MemorySize", 2048) / 1024
			envs += n
			needGiB += n * gib
			needVCPU += n * gib / fe.num(cfg, "ExecutionEnvironmentMemoryGiBPerVCpu", 2)
		}
	}
	why := ", one per AZ until a function uses it"
	n := 3.0
	if fns > 0 {
		n = max(math.Ceil(needVCPU/vcpu), math.Ceil(needGiB/(vcpu*gibPerVCPU)), min(3, envs))
		why = fmt.Sprintf(" for %s of %s", count(envs, "execution environment"), count(float64(fns), "function"))
	}
	e.hourly(n, "", "ec2|"+t, fmt.Sprintf("%s Lambda Managed Instances%s (minimum; headroom not counted)", t, why))
	e.hourly(n, "", "lmi|"+t, "Lambda Managed Instances management fee")
}

// pointsHere says whether v, read in stack s, names this resource: with Ref or
// Fn::GetAtt in its own stack, Fn::ImportValue of its export (a strong CDK
// reference), Fn::GetStackOutput of its output (a weak one), or its ARN.
func (e *est) pointsHere(s *Stack, v any) bool {
	m := asMap(v)
	if s == e.st && refID(v) == e.id {
		return true
	}
	if name, ok := m["Fn::ImportValue"].(string); ok {
		return e.st.Exports[name] == e.id
	}
	if o := asMap(m["Fn::GetStackOutput"]); o != nil {
		region, _ := o["Region"].(string)
		return o["StackName"] == e.st.Name && (region == "" || region == e.st.Region) &&
			o["OutputName"] != nil && e.st.Outputs[fmt.Sprint(o["OutputName"])] == e.id
	}
	arn, _ := v.(string)
	name, _ := e.r.Properties["CapacityProviderName"].(string)
	return arn != "" && name != "" && strings.HasSuffix(arn, ":capacity-provider:"+name)
}
