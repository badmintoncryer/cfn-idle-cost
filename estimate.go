package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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
}

// unresolved aborts the estimate of one resource; the message says why.
type unresolved string

type est struct {
	st     *Stack
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
			out = append(out, estimateResource(st, prices, id)...)
		}
	}
	return out
}

func estimateResource(st *Stack, prices *Prices, id string) (items []Item) {
	e := &est{st: st, prices: prices, r: st.Resources[id], id: id}
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
	if rule == nil {
		return nil
	}
	if e.r.Condition != "" {
		panic(unresolved("depends on Condition " + e.r.Condition))
	}
	rule(e, e.r.Properties)
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
			e.hourly(1, "", "nat|NatGateway-Hours", "NAT Gateway hourly")
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
				e.hourly(float64(n), "AZ", "vpc|VpcEndpoint-Hours", "interface endpoint hourly per AZ")
			case "GatewayLoadBalancer":
				e.hourly(1, "", "vpc|VpcEndpoint-GWLBE-Hours", "Gateway Load Balancer endpoint hourly")
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
			e.hourly(1, "", "elb|Load Balancer-"+f, f+" Load Balancer hourly")
			if t == "gateway" || e.str(p, "Scheme", "internet-facing") != "internet-facing" {
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
			e.hourly(1, "", "elb|Load Balancer", "Classic Load Balancer hourly")
		},
		"AWS::EC2::Instance": func(e *est, p map[string]any) {
			t := e.str(p, "InstanceType", "")
			bdm := p["BlockDeviceMappings"]
			if lt, ok := p["LaunchTemplate"]; ok {
				data := e.launchTemplate(asMap(lt))
				if t == "" {
					t = e.str(data, "InstanceType", "")
				}
				if bdm == nil {
					bdm = data["BlockDeviceMappings"]
				}
			}
			if t == "" {
				panic(unresolved("InstanceType is not set"))
			}
			if e.str(p, "Tenancy", "default") != "default" {
				panic(unresolved("dedicated tenancy is not supported yet"))
			}
			e.hourly(1, "", "ec2|"+t, t+" on-demand (Linux)")
			e.blockDevices(bdm, 1)
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
			var bdm any
			if lt, ok := p["LaunchTemplate"]; ok {
				data := e.launchTemplate(asMap(lt))
				t, bdm = e.mustStr(data, "InstanceType"), data["BlockDeviceMappings"]
			} else if _, ok := p["LaunchConfigurationName"]; ok {
				_, lc := e.ref(p, "LaunchConfigurationName")
				t, bdm = e.mustStr(lc.Properties, "InstanceType"), lc.Properties["BlockDeviceMappings"]
			} else {
				panic(unresolved("no LaunchTemplate or LaunchConfigurationName"))
			}
			e.hourly(n, "", "ec2|"+t, fmt.Sprintf("%s on-demand (Linux), %s", t, why))
			e.blockDevices(bdm, n)
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
		"AWS::DynamoDB::GlobalTable": func(e *est, p map[string]any) {
			if e.str(p, "BillingMode", "PROVISIONED") == "PROVISIONED" {
				panic(unresolved("provisioned global tables are not supported yet"))
			}
		},
		"AWS::Kinesis::Stream": func(e *est, p map[string]any) {
			if e.str(asMap(p["StreamModeDetails"]), "StreamMode", "PROVISIONED") == "ON_DEMAND" {
				e.hourly(1, "", "kinesis|OnDemand-StreamHour", "on-demand stream hourly")
				return
			}
			e.hourly(e.mustNum(p, "ShardCount"), "shards", "kinesis|Storage-ShardHour", "provisioned shard hourly")
		},
		"AWS::KMS::Key": func(e *est, p map[string]any) {
			e.monthly(1, "", "kms|KMS-Keys", "customer managed key")
		},
		"AWS::KMS::ReplicaKey": func(e *est, p map[string]any) {
			e.monthly(1, "", "kms|KMS-Keys", "customer managed key (replica)")
		},
		"AWS::SecretsManager::Secret": func(e *est, p map[string]any) {
			e.monthly(1, "", "secrets|AWSSecretsManager-Secrets", "secret")
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
		panic(unresolved("engine " + engine + " is not supported yet"))
	}
	if cluster != nil { // Aurora: storage is usage-based, only the instance counts
		iopt := e.str(cluster, "StorageType", "aurora") == "aurora-iopt1"
		if class == "db.serverless" {
			sc := asMap(cluster["ServerlessV2ScalingConfiguration"])
			key := "rds|Aurora:ServerlessV2Usage|" + code
			if iopt {
				key = "rds|Aurora:ServerlessV2IOOptimizedUsage|" + code
			}
			e.hourly(e.mustNum(sc, "MinCapacity"), "ACU", key, "Serverless v2 minimum capacity")
			return
		}
		usage := "InstanceUsage"
		if iopt {
			usage = "InstanceUsageIOOptimized"
		}
		e.hourly(1, "", "rds|"+usage+":"+class+"|"+code, class+" "+engine)
		return
	}
	multi := e.flag(p, "MultiAZ")
	usage, maz, az := "InstanceUsage", "", "Single-AZ"
	if multi {
		usage, maz, az = "Multi-AZUsage", "Multi-AZ-", "Multi-AZ"
	}
	e.hourly(1, "", "rds|"+usage+":"+class+"|"+code, class+" "+engine+" "+az)
	size := e.mustNum(p, "AllocatedStorage")
	iops, tput := e.num(p, "Iops", 0), e.num(p, "StorageThroughput", 0)
	def := "gp2"
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
		bi, bt := 3000.0, 125.0
		if size >= 400 {
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

func rdsCluster(e *est, p map[string]any) {
	// Aurora clusters bill through their instances and usage-based storage.
	if !strings.HasPrefix(strings.ToLower(e.str(p, "Engine", "aurora")), "aurora") {
		panic(unresolved("Multi-AZ DB clusters are not supported yet"))
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
		e.monthly(math.Max(0, e.num(ebs, "Iops", 0)-3000)*n, "IOPS", "es|ES:GP3-PIOPS", "gp3 IOPS above 3000")
		e.monthly(math.Max(0, e.num(ebs, "Throughput", 0)-125)*n, "MiB/s", "es|ES:GP3-Provisioned-ThroughPut", "gp3 throughput above 125 MiB/s")
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

func ecsService(e *est, p map[string]any) {
	lt := e.str(p, "LaunchType", "")
	if lt == "" {
		cps, _ := p["CapacityProviderStrategy"].([]any)
		for _, c := range cps {
			if cp := e.str(asMap(c), "CapacityProvider", ""); cp == "FARGATE" || cp == "FARGATE_SPOT" {
				lt = cp
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
	if os := e.str(asMap(tp["RuntimePlatform"]), "OperatingSystemFamily", "LINUX"); os != "LINUX" {
		panic(unresolved("Fargate on " + os + " is not supported yet"))
	}
	arm := ""
	if e.str(asMap(tp["RuntimePlatform"]), "CpuArchitecture", "X86_64") == "ARM64" {
		arm = "ARM-"
	}
	vcpu := e.mustNum(tp, "Cpu") / 1024
	gb := e.mustNum(tp, "Memory") / 1024
	e.hourly(n*vcpu, "vCPU", "fargate|Fargate-"+arm+"vCPU-Hours:perCPU", fmt.Sprintf("Fargate vCPU (%g tasks)", n))
	e.hourly(n*gb, "GB", "fargate|Fargate-"+arm+"GB-Hours", fmt.Sprintf("Fargate memory (%g tasks)", n))
	if es := e.num(asMap(tp["EphemeralStorage"]), "SizeInGiB", 20); es > 20 {
		e.hourly(n*(es-20), "GB", "fargate|Fargate-EphemeralStorage-GB-Hours", "Fargate ephemeral storage above 20 GB")
	}
}
