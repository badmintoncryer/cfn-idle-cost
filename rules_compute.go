package main

import (
	"fmt"
	"math"
	"strings"
)

func init() {
	rules["AWS::RDS::DBShardGroup"] = shardGroup
	rules["AWS::RDS::Deployment"] = blueGreen
	rules["AWS::AutoScaling::WarmPool"] = warmPool
	rules["AWS::GameLift::ContainerFleet"] = containerFleet
	rules["AWS::GameLift::GameServerGroup"] = gameServerGroup
	rules["AWS::ECS::ExpressGatewayService"] = expressService
	rules["AWS::EKS::Capability"] = func(e *est, p map[string]any) {
		t := e.mustStr(p, "Type")
		x, ok := map[string]string{"ARGOCD": "ArgoCD", "ACK": "ACK", "KRO": "KRO"}[t]
		if !ok {
			panic(unresolved("Type " + t + " is not supported"))
		}
		e.hourly(1, "", "eks|AmazonEKSCapabilities-"+x+"-Hours:perCapability", x+" capability (managed resources not counted)")
	}
	rules["AWS::FSx::FileCache"] = func(e *est, p map[string]any) {
		// ponytail: File Cache is not in the Price List; update by hand if the $1.33/GB-month us-east-1 list price changes.
		if e.st.Region != "us-east-1" {
			panic(unresolved("File Cache prices are not in the Price List; only us-east-1's is known"))
		}
		size := e.mustNum(p, "StorageCapacity")
		e.add(size, "GB", size*1.33, "cache storage ($1.33/GB-month list price, not in the Price List)")
	}
	rules["AWS::Transfer::WebApp"] = func(e *est, p map[string]any) {
		e.hourly(e.num(asMap(p["WebAppUnits"]), "Provisioned", 1), "units", "transfer|WebAppHours", "web app units")
	}
	rules["AWS::AppConfig::ExperimentRun"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "ssm|AppConfig-ExperimentHours", "running experiment (creating it starts the run)")
	}
	rules["AWS::Glue::Trigger"] = glueTrigger
	rules["AWS::Glue::Crawler"] = func(e *est, p map[string]any) {
		if s := e.str(asMap(p["Schedule"]), "ScheduleExpression", ""); s != "" {
			panic(unresolved("runs on " + s + "; each run bills at least 10 minutes of DPUs Glue chooses"))
		}
	}
	rules["AWS::Budgets::BudgetsAction"] = func(e *est, p map[string]any) {
		name := fmt.Sprint(p["BudgetName"])
		for _, id := range sortedIDs(e.st.Resources) {
			r := e.st.Resources[id]
			if id == e.id {
				break
			}
			if r.Type == e.r.Type && fmt.Sprint(e.props(r)["BudgetName"]) == name {
				return // the budget is billed once, on its first action
			}
		}
		e.add(1, "", e.price("budgets|ActionEnabledBudgetsUsage")*hoursPerMonth/24, "action-enabled budget (one fee per budget)")
	}
	rules["AWS::CloudWatch::LogAlarm"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "cw|CW:AlarmMonitorUsage", "log alarm (query scanning not counted)")
	}
}

// shardGroup bills the minimum capacity of an Aurora PostgreSQL Limitless shard
// group. The default minimum is 4 ACUs per node, and the node count follows
// MaxACU (the table in the Aurora user guide).
// ponytail: compute standbys are billed as MinACU × the shards' share of the
// nodes, assuming the minimum is spread evenly; the docs don't give the split.
func shardGroup(e *est, p map[string]any) {
	maxACU := e.mustNum(p, "MaxACU")
	nodes := 4.0
	if maxACU > 400 {
		nodes = math.Min(24, 4+math.Ceil((maxACU-400)/100))
	}
	min := e.num(p, "MinACU", 4*nodes)
	shards := nodes - math.Ceil(nodes/3) // the rest are routers
	why := "Limitless shard group minimum capacity"
	if r := e.num(p, "ComputeRedundancy", 0); r > 0 {
		min *= 1 + r*shards/nodes
		why += fmt.Sprintf(", ComputeRedundancy %g (standby shards)", r)
	}
	e.hourly(min, "ACU", "rds|Aurora:IO-OptimizedLimitlessACUUsage-Limitless|21", why)
}

// blueGreen bills the green copy of a Blue/Green deployment like its source,
// with the Target* properties applied: an instance, or every instance of a
// cluster in this template.
func blueGreen(e *est, p map[string]any) {
	srcID, src := e.ref(p, "Source")
	target := func(m map[string]any, classKey string) map[string]any {
		c := map[string]any{}
		for k, v := range m {
			c[k] = v
		}
		for k, t := range map[string]string{classKey: "TargetDBInstanceClass", "AllocatedStorage": "TargetAllocatedStorage", "Iops": "TargetIops",
			"StorageType": "TargetStorageType", "StorageThroughput": "TargetStorageThroughput"} {
			if v, ok := p[t]; ok {
				c[k] = v
			}
		}
		return c
	}
	n := len(e.items)
	switch src.Type {
	case "AWS::RDS::DBInstance":
		rdsInstance(e, target(src.Properties, "DBInstanceClass"))
	case "AWS::RDS::DBCluster":
		rdsCluster(e, target(src.Properties, "DBClusterInstanceClass"))
		for _, id := range sortedIDs(e.st.Resources) {
			r := e.st.Resources[id]
			if r.Type == "AWS::RDS::DBInstance" && refID(r.Properties["DBClusterIdentifier"]) == srcID {
				rdsInstance(e, target(e.props(r), "DBInstanceClass"))
			}
		}
	default:
		panic(unresolved("Source is a " + src.Type))
	}
	for i := n; i < len(e.items); i++ {
		e.items[i].Reason = "green copy: " + e.items[i].Reason
	}
}

// asgLaunch returns the instance type, block devices and public IP setting of
// an Auto Scaling group's launch template or configuration. With a
// MixedInstancesPolicy, the type is the first override's (the On-Demand
// priority) and dist is its InstancesDistribution, non-nil.
func (e *est) asgLaunch(p map[string]any) (t string, bdm, assoc any, dist map[string]any) {
	if mp, ok := p["MixedInstancesPolicy"]; ok {
		mip := asMap(mp)
		lt := asMap(mip["LaunchTemplate"])
		data := e.launchTemplate(asMap(lt["LaunchTemplateSpecification"]))
		t = e.str(data, "InstanceType", "")
		if l, ok := lt["Overrides"].([]any); ok && len(l) > 0 {
			o := asMap(l[0])
			if o["InstanceRequirements"] != nil {
				panic(unresolved("MixedInstancesPolicy with InstanceRequirements: the instance type is chosen at launch"))
			}
			for _, x := range l {
				if w := e.num(asMap(x), "WeightedCapacity", 1); w != 1 {
					panic(unresolved("MixedInstancesPolicy with WeightedCapacity is not supported yet"))
				}
			}
			t = e.mustStr(o, "InstanceType")
		}
		if t == "" {
			panic(unresolved("MixedInstancesPolicy names no instance type"))
		}
		dist = asMap(mip["InstancesDistribution"])
		if dist == nil {
			dist = map[string]any{}
		}
		return t, data["BlockDeviceMappings"], firstMap(data["NetworkInterfaces"])["AssociatePublicIpAddress"], dist
	}
	if lt, ok := p["LaunchTemplate"]; ok {
		data := e.launchTemplate(asMap(lt))
		return e.mustStr(data, "InstanceType"), data["BlockDeviceMappings"], firstMap(data["NetworkInterfaces"])["AssociatePublicIpAddress"], nil
	}
	if _, ok := p["LaunchConfigurationName"]; ok {
		_, lc := e.ref(p, "LaunchConfigurationName")
		return e.mustStr(lc.Properties, "InstanceType"), lc.Properties["BlockDeviceMappings"], lc.Properties["AssociatePublicIpAddress"], nil
	}
	panic(unresolved("no LaunchTemplate or LaunchConfigurationName"))
}

// onDemand returns how many of n instances an InstancesDistribution launches
// On-Demand: OnDemandBaseCapacity, then OnDemandPercentageAboveBaseCapacity
// of the rest, rounded up (toward On-Demand).
func (e *est) onDemand(dist map[string]any, n float64) float64 {
	base := math.Min(n, e.num(dist, "OnDemandBaseCapacity", 0))
	pct := e.num(dist, "OnDemandPercentageAboveBaseCapacity", 100)
	return base + math.Ceil((n-base)*pct/100-1e-9)
}

// warmPool bills the instances an Auto Scaling group keeps prepared: their EBS
// volumes while stopped or hibernated, the whole instance while running.
func warmPool(e *est, p map[string]any) {
	_, asg := e.ref(p, "AutoScalingGroupName")
	ap := asg.Properties
	t, bdm, assoc, dist := e.asgLaunch(ap)
	if dist != nil {
		panic(unresolved("its Auto Scaling group has a MixedInstancesPolicy, which warm pools don't support"))
	}
	desired := e.num(ap, "DesiredCapacity", -1)
	if desired < 0 {
		desired = e.mustNum(ap, "MinSize")
	}
	prepared := e.num(p, "MaxGroupPreparedCapacity", -1)
	if prepared < 0 { // -1, the default, means the group's MaxSize
		prepared = e.mustNum(ap, "MaxSize")
	}
	n := math.Max(e.num(p, "MinSize", 0), prepared-desired)
	if n <= 0 {
		return
	}
	state := e.str(p, "PoolState", "Stopped")
	if state == "Running" {
		e.hourly(n, "", "ec2|"+t, t+" on-demand (Linux), warm pool Running")
		subnets, _ := ap["VPCZoneIdentifier"].([]any)
		if e.autoPublicIP(assoc, subnets) {
			e.hourly(n, "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 assigned at launch, warm pool")
		}
	}
	k := len(e.items)
	e.blockDevices(bdm, n)
	if state != "Running" && len(e.items) == k { // shown at $0 so it reads as checked, not missed
		e.items = append(e.items, Item{Quantity: fmt.Sprintf("×%g", n), Reason: "warm pool " + state + " (EBS volumes from the AMI not counted)"})
	}
}

// containerFleet bills the Linux instances of a GameLift container fleet in its
// home Region, like gameLiftFleet.
func containerFleet(e *est, p map[string]any) {
	if e.str(p, "BillingType", "ON_DEMAND") == "SPOT" {
		panic(unresolved("Spot fleets are not supported"))
	}
	t := e.mustStr(p, "InstanceType")
	n := 1.0 // GameLift starts one instance
	locs, _ := p["Locations"].([]any)
	for _, l := range locs {
		l := asMap(l)
		if loc := e.mustStr(l, "Location"); loc != e.st.Region {
			panic(unresolved("remote location " + loc + " is not supported"))
		}
		c := asMap(l["LocationCapacity"])
		if e.str(asMap(c["ManagedCapacityConfiguration"]), "ZeroCapacityStrategy", "MANUAL") == "SCALE_TO_AND_FROM_ZERO" {
			return // scales to zero with no game sessions
		}
		if c != nil {
			n = e.num(c, "DesiredEC2Instances", e.num(c, "MinSize", 0))
		}
	}
	e.hourly(n, "", "gamelift|Linux|"+t, t+" GameLift container fleet instance (Linux)")
}

// gameServerGroup bills the MinSize instances of a FleetIQ game server group:
// EC2 plus the FleetIQ fee. The OS comes from the launch template's AMI, so
// Linux is assumed, like the Auto Scaling group rule.
// ponytail: InstanceDefinitions lists at least two types and the group may
// launch any of them; the first one is priced, and WeightedCapacity is ignored.
func gameServerGroup(e *est, p map[string]any) {
	n := e.num(p, "MinSize", 0)
	if n == 0 {
		return
	}
	if s := e.str(p, "BalancingStrategy", "SPOT_PREFERRED"); s != "ON_DEMAND_ONLY" {
		panic(unresolved("BalancingStrategy " + s + " launches Spot instances, which are not supported"))
	}
	defs, _ := p["InstanceDefinitions"].([]any)
	if len(defs) == 0 {
		panic(unresolved("InstanceDefinitions is not set"))
	}
	t := e.mustStr(asMap(defs[0]), "InstanceType")
	why := fmt.Sprintf("first of %d instance types", len(defs))
	e.hourly(n, "", "ec2|"+t, t+" on-demand (Linux), MinSize, "+why)
	e.hourly(n, "", "gamelift|LinuxFleetIQ|"+t, "FleetIQ fee")
}

// expressService bills the Fargate tasks of an ECS Express Mode service at its
// minimum task count, and the Application Load Balancer Express Mode shares
// between up to 25 services of a VPC once per stack: a lone service still pays
// for a whole one.
func expressService(e *est, p map[string]any) {
	n := e.num(asMap(p["ScalingTarget"]), "MinTaskCount", 1)
	// Express Mode's defaults (the resource schema says 256 and 512, the ECS guide 1024 and 2048).
	td := map[string]any{"Cpu": p["Cpu"], "Memory": p["Memory"]}
	if _, ok := p["TaskDefinitionArn"]; ok {
		_, r := e.ref(p, "TaskDefinitionArn")
		td = r.Properties
	}
	if plat := asMap(td["RuntimePlatform"]); e.str(plat, "OperatingSystemFamily", "LINUX") != "LINUX" || e.str(plat, "CpuArchitecture", "X86_64") != "X86_64" {
		panic(unresolved("only x86 Linux task definitions are supported"))
	}
	tasks := count(n, "task")
	e.hourly(n*e.num(td, "Cpu", 1024)/1024, "vCPU", "fargate|Fargate-vCPU-Hours:perCPU", "Fargate vCPU ("+tasks+")")
	e.hourly(n*e.num(td, "Memory", 2048)/1024, "GB", "fargate|Fargate-GB-Hours", "Fargate memory ("+tasks+")")
	subnets, set := asMap(p["NetworkConfiguration"])["Subnets"].([]any)
	if !set || e.autoPublicIP(nil, subnets) { // the default VPC's public subnets
		e.hourly(n, "IPv4", "vpc|PublicIPv4:InUseAddress", "public IPv4 per task ("+tasks+")")
	}
	for _, id := range sortedIDs(e.st.Resources) {
		if e.st.Resources[id].Type == e.r.Type {
			if id == e.id {
				e.hourly(1, "", "elb|Load Balancer-Application", "Application Load Balancer shared by Express Mode services, counted once per stack")
			}
			return
		}
	}
}

// glueTrigger stops at triggers that start work with no traffic: a schedule,
// or a streaming job, which runs until stopped. Triggers not started on
// creation (and ON_DEMAND ones) fire nothing by themselves.
func glueTrigger(e *est, p map[string]any) {
	if !e.flag(p, "StartOnCreation") {
		return
	}
	if e.str(p, "Type", "") == "SCHEDULED" {
		panic(unresolved("runs its actions on " + e.str(p, "Schedule", "a schedule") + "; DPU time per run is not known"))
	}
	actions, _ := p["Actions"].([]any)
	for _, a := range actions {
		if job, ok := e.st.Resources[refID(asMap(a)["JobName"])]; ok && job.Type == "AWS::Glue::Job" &&
			e.str(asMap(e.props(job)["Command"]), "Name", "") == "gluestreaming" {
			panic(unresolved("starts streaming job " + refID(asMap(a)["JobName"]) + ", which runs until stopped"))
		}
	}
}

// snapStartCache bills the SnapStart snapshot a published version of a
// function in this template keeps cached. Java snapshots are free.
func (e *est) snapStartCache(p map[string]any) {
	fn, ok := e.st.Resources[refID(p["FunctionName"])]
	if !ok || fn.Type != "AWS::Lambda::Function" {
		return
	}
	fp := e.props(fn)
	if e.str(asMap(fp["SnapStart"]), "ApplyOn", "None") != "PublishedVersions" || strings.HasPrefix(e.str(fp, "Runtime", ""), "java") {
		return
	}
	mb := e.num(fp, "MemorySize", 128)
	e.add(mb/1024, "GB", mb/1024*hoursPerMonth*3600*e.price("lambda|Lambda-SnapStart-Cached-GB-S"), fmt.Sprintf("SnapStart cache (%g MB)", mb))
}
