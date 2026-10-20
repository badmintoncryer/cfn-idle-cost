package main

import (
	"fmt"
	"strings"
)

func init() {
	rules["AWS::CodeBuild::Fleet"] = codeBuildFleet
	rules["AWS::AppStream::ImageBuilder"] = func(e *est, p map[string]any) {
		t := e.mustStr(p, "InstanceType")
		img := fmt.Sprint(p["ImageName"], p["ImageArn"])
		for _, w := range []string{"Linux", "RHEL", "Rocky"} {
			if strings.Contains(img, w) {
				panic(unresolved("Linux image builders are not supported yet"))
			}
		}
		// ponytail: an image whose name doesn't say Linux is priced as Windows
		// (license included); BYOL and custom Linux images cost less.
		e.hourly(1, "", "appstream|"+t, t+" image builder (runs from creation; Windows assumed)")
	}
	rules["AWS::WorkspacesInstances::WorkspaceInstance"] = func(e *est, p map[string]any) {
		mi := asMap(p["ManagedInstance"])
		if t := e.str(asMap(mi["Placement"]), "Tenancy", "default"); t != "default" {
			panic(unresolved("Tenancy " + t + " is not supported yet"))
		}
		t := e.mustStr(mi, "InstanceType")
		e.hourly(1, "", "wsi|"+t, t+" WorkSpaces Core managed instance (Linux or BYOL rate; license-included OSes cost more)")
		e.blockDevices(mi["BlockDeviceMappings"], 1)
	}
	rules["AWS::WorkspacesInstances::Volume"] = func(e *est, p map[string]any) {
		size := e.num(p, "SizeInGB", 0)
		if size == 0 {
			panic(unresolved("SizeInGB is not set (the size comes from SnapshotId)"))
		}
		e.ebs(e.str(p, "VolumeType", "gp2"), size, e.num(p, "Iops", 0), e.num(p, "Throughput", 0), 1)
	}
	rules["AWS::Cloud9::EnvironmentEC2"] = func(e *est, p map[string]any) {
		t := e.mustStr(p, "InstanceType")
		stop := e.num(p, "AutomaticStopTimeMinutes", -1)
		if stop < 0 {
			panic(unresolved("AutomaticStopTimeMinutes is not set; whether the instance then stops when idle is not documented"))
		}
		if stop == 0 {
			e.hourly(1, "", "ec2|"+t, t+" instance (AutomaticStopTimeMinutes 0: never stops)")
		}
		// ponytail: Cloud9 gives the instance a 10 GiB root volume; gp3 is assumed.
		e.ebs("gp3", 10, 0, 0, 1)
	}
	rules["AWS::Deadline::Fleet"] = func(e *est, p map[string]any) {
		if n := e.num(p, "MinWorkerCount", 0); n > 0 && asMap(p["Configuration"])["ServiceManagedEc2"] != nil {
			panic(unresolved(fmt.Sprintf("keeps %s running; Deadline picks their instance type from InstanceCapabilities", count(n, "worker"))))
		}
		// customer-managed fleets pay per worker-hour only while a host is connected
	}
	rules["AWS::PCS::Cluster"] = func(e *est, p map[string]any) {
		s := strings.ToLower(e.mustStr(p, "Size"))
		s = strings.ToUpper(s[:1]) + s[1:]
		e.hourly(1, "", "pcs|PCSController:Slurm:"+s+":RunningHours", "Slurm controller ("+s+")")
		if e.str(asMap(asMap(p["SlurmConfiguration"])["Accounting"]), "Mode", "NONE") == "STANDARD" {
			e.hourly(1, "", "pcs|PCSAccountingUsage:Slurm:"+s+":RunningHours", "Slurm accounting (accounting storage not counted)")
		}
	}
	rules["AWS::PCS::ComputeNodeGroup"] = pcsNodeGroup
	rules["AWS::EVS::Environment"] = evsEnvironment
	rules["AWS::M2::Environment"] = func(e *est, p map[string]any) {
		t := strings.TrimPrefix(e.mustStr(p, "InstanceType"), "M2.")
		engine := e.mustStr(p, "EngineType")
		usage, ok := map[string]string{"bluage": "BluAgeRuntime", "microfocus": "EnterpriseServer"}[engine]
		if !ok {
			panic(unresolved("EngineType " + engine + " is not supported"))
		}
		n := e.num(asMap(p["HighAvailabilityConfig"]), "DesiredCapacity", 1)
		e.hourly(n, "", "m2|"+t+usage, "M2."+t+" "+engine+" runtime")
	}
	rules["AWS::MediaLive::Input"] = func(e *est, p map[string]any) {
		switch t := e.mustStr(p, "Type"); {
		case in(t, "RTMP_PULL", "URL_PULL", "MP4_FILE", "TS_FILE", "SRT_CALLER"):
			// pull inputs have no idle charge
		case in(t, "UDP_PUSH", "RTP_PUSH", "RTMP_PUSH", "MEDIACONNECT", "INPUT_DEVICE", "AWS_CDI", "SRT_LISTENER"):
			e.hourly(1, "", "medialive|INACTIVE_INPUT", "idle push input (until a running channel uses it)")
		default:
			panic(unresolved("not known whether a " + t + " input has an idle charge"))
		}
	}
	rules["AWS::MediaLive::Multiplex"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "medialive|INACTIVE_MULTIPLEX", "idle multiplex (created idle; a running one costs more)")
	}
	rules["AWS::SSMIncidents::ResponsePlan"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "ssm|IM-ResponsePlan-Months-Tier1", "Incident Manager response plan")
	}
	rules["AWS::PaymentCryptography::Key"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "paycrypto|ActiveKeys", "active key")
	}
	rules["AWS::B2BI::Partnership"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "b2bi|PartnershipMonths", "partnership")
	}
	rules["AWS::BillingConductor::BillingGroup"] = func(e *est, p map[string]any) {
		ag := asMap(p["AccountGrouping"])
		if e.flag(ag, "AutoAssociate") {
			panic(unresolved("AutoAssociate adds accounts the template doesn't list"))
		}
		if _, ok := ag["ResponsibilityTransferArn"]; ok {
			panic(unresolved("billing transfer groups are not supported yet"))
		}
		e.monthly(float64(e.length(ag, "LinkedAccountIds")), "accounts", "billingconductor|Proforma-Accounts", "pro forma billing per account (first-500 rate)")
	}
	rules["AWS::PricingPlanManager::Subscription"] = func(e *est, p map[string]any) {
		if t := e.mustStr(p, "PlanTier"); !strings.EqualFold(t, "FREE") {
			panic(unresolved("the " + t + " plan's monthly fee starts once the subscription is approved; CloudFormation leaves it pending"))
		}
	}
	rules["AWS::SMSVOICE::PhoneNumber"] = smsPhoneNumber
	rules["AWS::SMSVOICE::Registration"] = func(e *est, p map[string]any) {
		if t := e.mustStr(p, "RegistrationType"); strings.Contains(t, "CAMPAIGN") {
			panic(unresolved(t + ": $10/month per campaign ($2 low-volume) once it is approved"))
		}
	}
	rules["AWS::Lightsail::LoadBalancer"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "lightsail|LoadBalancerUsage", "load balancer")
	}
	rules["AWS::Lightsail::Disk"] = func(e *est, p map[string]any) {
		e.monthly(e.mustNum(p, "SizeInGb"), "GB", "lightsail|DiskUsage", "block storage")
	}
	rules["AWS::Lightsail::StaticIp"] = func(e *est, p map[string]any) {
		if p["AttachedTo"] == nil {
			e.hourly(1, "", "lightsail|UnusedStaticIP", "static IP not attached to an instance")
		}
	}
	rules["AWS::Lightsail::Container"] = func(e *est, p map[string]any) {
		power := e.mustStr(p, "Power")
		u, ok := map[string]string{"nano": "Nano-0.25CPU-0.5GB", "micro": "Micro-0.25CPU-1GB", "small": "Small-0.5CPU-1GB",
			"medium": "Medium-1CPU-2GB", "large": "Large-2CPU-4GB", "xlarge": "XLarge-4CPU-8GB"}[power]
		if !ok {
			panic(unresolved("Power " + power + " is not supported"))
		}
		e.hourly(e.mustNum(p, "Scale"), "nodes", "lightsail|ContainerSvcUsage:"+u, power+" container service nodes (billed while disabled too)")
	}
	// ponytail: bundle IDs (small_1_0, ...) come from GetBucketBundles and
	// GetDistributionBundles, not the docs; an unknown size is unresolved.
	rules["AWS::Lightsail::Bucket"] = func(e *est, p map[string]any) {
		e.lightsailBundle(p, "Storage-BundleUsage:", map[string]string{"small": "5GB", "medium": "100GB", "large": "250GB"}, "object storage bundle")
	}
	rules["AWS::Lightsail::Distribution"] = func(e *est, p map[string]any) {
		e.lightsailBundle(p, "CDN-BundleUsage:", map[string]string{"small": "50GB", "medium": "200GB", "large": "500GB"}, "distribution plan")
	}
}

// codeBuildFleet bills the BaseCapacity instances of a reserved fleet, which are
// billed per minute while provisioned, used or not.
func codeBuildFleet(e *est, p map[string]any) {
	n := e.num(p, "BaseCapacity", 0)
	if n == 0 {
		return
	}
	ct := e.mustStr(p, "ComputeType")
	size, ok := strings.CutPrefix(ct, "BUILD_GENERAL1_")
	if !ok {
		panic(unresolved("ComputeType " + ct + " is not supported yet"))
	}
	env := e.mustStr(p, "EnvironmentType")
	family, ok := map[string]string{
		"LINUX_CONTAINER": "Linux:Reserved:x86-64.g1", "LINUX_EC2": "Linux:Reserved:x86-64.g1",
		"ARM_CONTAINER": "Linux:Reserved:arm.g1", "ARM_EC2": "Linux:Reserved:arm.g1", "LINUX_GPU_CONTAINER": "Linux:Reserved:gpu.g1",
		"WINDOWS_SERVER_2019_CONTAINER": "Windows:Reserved:x86-64.g1", "WINDOWS_SERVER_2022_CONTAINER": "Windows:Reserved:x86-64.g1",
		"WINDOWS_EC2": "Windows:Reserved:x86-64.g1", "MAC_ARM": "Mac:Reserved:arm.m2",
	}[env]
	if !ok {
		panic(unresolved("EnvironmentType " + env + " is not supported"))
	}
	perMin := e.price("codebuild|" + family + "." + strings.ToLower(size)) // the Price List unit is minutes
	e.add(n, "", n*perMin*60*hoursPerMonth, ct+" "+env+" reserved capacity (billed per minute, used or not)")
}

// pcsNodeGroup bills the static nodes of a PCS compute node group: EC2 plus the
// PCS node management fee, advanced tier for P and Trn instances.
func pcsNodeGroup(e *est, p map[string]any) {
	n := e.num(asMap(p["ScalingConfiguration"]), "MinInstanceCount", 0)
	if n == 0 {
		return
	}
	if po := e.str(p, "PurchaseOption", "ONDEMAND"); po != "ONDEMAND" {
		panic(unresolved("PurchaseOption " + po + " is not supported yet"))
	}
	if e.length(p, "InstanceConfigs") != 1 {
		panic(unresolved("PCS launches any of several InstanceConfigs; list one type to estimate it"))
	}
	t := e.mustStr(firstMap(p["InstanceConfigs"]), "InstanceType")
	tier := "StdNode"
	if strings.HasPrefix(t, "p") || strings.HasPrefix(t, "trn") {
		tier = "AdvNode"
	}
	e.hourly(n, "", "ec2|"+t, t+" static compute nodes (MinInstanceCount; root volumes not counted)")
	e.hourly(n, "", "pcs|PCSInstance:Slurm:"+tier+":RunningHours", "PCS node management fee")
}

// evsEnvironment bills the bare-metal hosts of an Amazon EVS environment and
// the EVS fee per host.
func evsEnvironment(e *est, p map[string]any) {
	if e.length(p, "Hosts") <= 0 {
		panic(unresolved("Hosts is not set"))
	}
	var types []string
	n := map[string]float64{}
	for _, h := range p["Hosts"].([]any) {
		hm := asMap(h)
		if hm["DedicatedHostId"] != nil {
			panic(unresolved("hosts on Dedicated Hosts are not supported yet"))
		}
		t := e.mustStr(hm, "InstanceType")
		if n[t] == 0 {
			types = append(types, t)
		}
		n[t]++
	}
	total := 0.0
	for _, t := range types {
		e.hourly(n[t], "hosts", "ec2|"+t, t+" hosts")
		total += n[t]
	}
	e.hourly(total, "hosts", "evs|EVSUsage", "EVS fee per host (VCF licenses and route servers not counted)")
}

// smsPhoneNumber bills the monthly lease of an origination number.
// ponytail: leases are not in the Price List; update by hand from the End User
// Messaging pricing page. They vary by country, not by AWS Region.
func smsPhoneNumber(e *est, p map[string]any) {
	t, c := e.mustStr(p, "NumberType"), e.mustStr(p, "IsoCountryCode")
	var usd float64
	switch {
	case t == "SIMULATOR", t == "TEN_DLC" && c == "US":
		usd = 1
	case t == "TOLL_FREE" && c == "US":
		usd = 2
	case t == "LONG_CODE":
		usd = map[string]float64{"AU": 22, "AT": 27, "CL": 21, "DK": 10, "FI": 15, "HK": 49, "IT": 30, "NL": 13,
			"NO": 30, "PL": 15, "ES": 20, "SE": 10, "GB": 2, "HU": 60, "PT": 25}[c]
	}
	if usd == 0 {
		panic(unresolved("no list price for a " + t + " number in " + c))
	}
	e.add(1, "", usd, fmt.Sprintf("%s %s number lease ($%g list price, not in the Price List)", c, t, usd))
}

// lightsailBundle bills the monthly bundle a Lightsail BundleId names.
func (e *est) lightsailBundle(p map[string]any, usage string, sizes map[string]string, what string) {
	id := e.mustStr(p, "BundleId")
	size, _, _ := strings.Cut(id, "_")
	gb, ok := sizes[size]
	if !ok {
		panic(unresolved("BundleId " + id + " is not known"))
	}
	e.monthly(1, "", "lightsail|"+usage+gb, gb+" "+what)
}
