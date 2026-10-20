package main

import (
	"fmt"
	"math"
	"strings"
)

// Data, analytics and ML types.
func init() {
	rules["AWS::Grafana::Workspace"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "grafana|Grafana:EditorUser", "Editor license every workspace pays for (active users not counted)")
	}
	rules["AWS::LookoutEquipment::InferenceScheduler"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "l4e|Inference-Hours-L4E", "scheduled inference (created running)")
	}
	rules["AWS::ManagedBlockchain::Member"] = func(e *est, p map[string]any) {
		nc := asMap(p["NetworkConfiguration"])
		if nc == nil {
			panic(unresolved("joins an existing network, whose edition is not in the template"))
		}
		fabric := asMap(asMap(nc["NetworkFrameworkConfiguration"])["NetworkFabricConfiguration"])
		ed := e.mustStr(fabric, "Edition")
		op := map[string]string{"STANDARD": "Fabric:Standard", "STARTER": "Fabric:Starter"}[ed]
		if op == "" {
			panic(unresolved("Edition " + ed + " is not supported"))
		}
		e.hourly(1, "", "blockchain|Member|"+op, "Hyperledger Fabric member ("+ed+" edition)")
	}
	rules["AWS::ManagedBlockchain::Node"] = func(e *est, p map[string]any) {
		t := e.mustStr(asMap(p["NodeConfiguration"]), "InstanceType")
		e.hourly(1, "", "blockchain|Node:"+t, t+" peer node (storage not counted)")
	}
	rules["AWS::Personalize::Campaign"] = func(e *est, p map[string]any) {
		panic(unresolved(fmt.Sprintf("bills %g minimum TPS (MinProvisionedTPS) at a rate set by the recipe of its solution version", e.num(p, "MinProvisionedTPS", 1))))
	}
	rules["AWS::DataPipeline::Pipeline"] = func(e *est, p map[string]any) {
		if e.str(p, "Activate", "true") == "false" {
			e.monthly(1, "", "datapipeline|InactivePipelines", "inactive pipeline")
			return
		}
		panic(unresolved(fmt.Sprintf("an active pipeline bills each activity and precondition by how often it runs (%s)", count(float64(max(0, e.length(p, "PipelineObjects"))), "pipeline object"))))
	}
	rules["AWS::HealthImaging::Datastore"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "healthimaging|DataStore-Hours", "data store fee (image storage not counted)")
	}
	rules["AWS::OpenSearchServerless::CollectionGroup"] = func(e *est, p map[string]any) {
		cl := asMap(p["CapacityLimits"])
		for _, k := range []string{"Indexing", "Search"} {
			key := "Min" + k + "CapacityInOcu"
			if cl[key] == nil {
				panic(unresolved("CapacityLimits." + key + " is not set; the default minimum is not documented"))
			}
			e.hourly(e.num(cl, key, 0), "OCU", "aoss|"+k+"OCU", strings.ToLower(k)+" minimum OCUs of the group")
		}
	}
	rules["AWS::Comprehend::DocumentClassifierEndpoint"] = comprehendEndpoint
	rules["AWS::Comprehend::EntityRecognizerEndpoint"] = comprehendEndpoint
	rules["AWS::Comprehend::DocumentClassifier"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "comprehend|DocClassifier-Storage", "custom model management fee (training not counted)")
	}
	rules["AWS::Comprehend::EntityRecognizer"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "comprehend|EntityRecognizer-Storage", "custom model management fee (training not counted)")
	}
	rules["AWS::KendraRanking::ExecutionPlan"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "kendra|KendraIntelligentRanking-BaseCapacity", "Intelligent Ranking rescore execution plan")
		e.hourly(e.num(asMap(p["CapacityUnits"]), "RescoreCapacityUnits", 0), "units", "kendra|KendraIntelligentRanking-CapacityUnits", "additional rescore capacity")
	}
	rules["AWS::Kendra::DataSource"] = kendraDataSource
	rules["AWS::Athena::CapacityReservation"] = func(e *est, p map[string]any) {
		e.hourly(e.mustNum(p, "TargetDpus"), "DPU", "athena|ReservedCapacityInDPUHours", "provisioned capacity")
	}
	rules["AWS::QBusiness::Index"] = func(e *est, p map[string]any) {
		t := e.mustStr(p, "Type")
		ed := map[string]string{"STARTER": "Starter", "ENTERPRISE": "Enterprise"}[t]
		if ed == "" {
			panic(unresolved("Type " + t + " is not supported"))
		}
		e.hourly(e.num(asMap(p["CapacityConfiguration"]), "Units", 1), "units", "qbusiness|Q-Business-"+ed+"-Index", ed+" index units (user subscriptions not counted)")
	}
	rules["AWS::APS::Scraper"] = func(e *est, p map[string]any) {
		e.hourly(1, "", "amp|AMP:CollectorHours", "managed collector (samples collected not counted)")
	}
	rules["AWS::Glue::DevEndpoint"] = func(e *est, p map[string]any) {
		dpu, why := e.num(p, "NumberOfNodes", 5), "NumberOfNodes"
		if n := e.num(p, "NumberOfWorkers", 0); n > 0 {
			per := map[string]float64{"Standard": 1, "G.1X": 1, "G.2X": 2}[e.mustStr(p, "WorkerType")]
			if per == 0 {
				panic(unresolved("WorkerType " + e.str(p, "WorkerType", "") + " is not supported"))
			}
			dpu, why = n*per, "NumberOfWorkers × WorkerType"
		}
		e.hourly(dpu, "DPU", "glue|DEVED-DPU-Hour", "development endpoint DPUs ("+why+")")
	}
	rules["AWS::Glue::Session"] = func(e *est, p map[string]any) {
		panic(unresolved(fmt.Sprintf("bills DPU-hours from creation until it times out after %g minutes, then stops: a one-off cost", e.num(p, "Timeout", 2880))))
	}
	rules["AWS::EMR::InstanceFleetConfig"] = func(e *est, p map[string]any) {
		if c, ok := e.st.Resources[refID(p["ClusterId"])]; ok && e.emrTransient(e.props(c)) != "" {
			return // shown on the cluster
		}
		e.emrFleet(p, strings.ToLower(e.mustStr(p, "InstanceFleetType")))
	}
	rules["AWS::EMRServerless::Application"] = func(e *est, p map[string]any) {
		if e.length(p, "InitialCapacity") > 0 && e.str(asMap(p["AutoStopConfiguration"]), "Enabled", "true") == "false" {
			e.none("not started by CloudFormation; once a job starts it, the pre-initialized capacity bills until it is stopped (auto-stop is off)")
		}
	}
	rules["AWS::EMRContainers::Endpoint"] = func(e *est, p map[string]any) {
		if t := e.mustStr(p, "Type"); t != "JUPYTER_ENTERPRISE_GATEWAY" {
			panic(unresolved("Type " + t + " is not supported yet"))
		}
		e.hourly(1, "", "elb|Load Balancer-Application", "Application Load Balancer of the managed endpoint (the gateway pod's EMR on EKS fee not counted)")
	}
	rules["AWS::Timestream::InfluxDBCluster"] = func(e *est, p map[string]any) {
		if eng := e.str(p, "EngineType", "INFLUXDB_V2"); eng != "INFLUXDB_V2" {
			panic(unresolved("EngineType " + eng + " is not supported yet"))
		}
		t := e.mustStr(p, "DbInstanceType")
		st := e.str(p, "DbStorageType", "InfluxIOIncludedT1")
		// ponytail: the cluster has a primary and at least one read replica; more replicas are not a template property.
		e.hourly(2, "nodes", "influx|ClusterNodeUsage-D"+strings.TrimPrefix(t, "d"), t+" primary and read replica (InfluxData read replica license not counted)")
		e.monthly(2*e.mustNum(p, "AllocatedStorage"), "GB", "influx|ClusterNode-InfluxDBStorage-"+st, st+" storage of each node")
	}
	rules["AWS::DMS::ReplicationConfig"] = func(e *est, p map[string]any) {
		if e.mustStr(p, "ReplicationType") != "full-load" {
			e.none(fmt.Sprintf("not started by CloudFormation; once started, CDC keeps at least %g DCU running", e.num(asMap(p["ComputeConfig"]), "MinCapacityUnits", 1)))
		}
	}
	rules["AWS::DMS::DataMigration"] = func(e *est, p map[string]any) {
		if e.mustStr(p, "DataMigrationType") != "full-load" {
			e.none("not started by CloudFormation; once started, CDC bills hourly until it is stopped")
		}
	}
	rules["AWS::ResilienceHub::App"] = func(e *est, p map[string]any) {
		why := ""
		if e.str(p, "AppAssessmentSchedule", "Disabled") == "Daily" {
			why = "daily assessment"
		}
		for _, id := range sortedIDs(e.st.Resources) {
			if r := e.st.Resources[id]; why == "" && r.Type == "AWS::ResilienceHub::AppAssessment" && refID(r.Properties["AppArn"]) == e.id {
				why = "assessed by " + e.st.DisplayPath(id)
			}
		}
		if why != "" {
			e.monthly(1, "", "resiliencehub|PaidTierApplication", "assessed application ("+why+")")
		}
	}
	rules["AWS::ResilienceHub::AppAssessment"] = func(e *est, p map[string]any) {
		if r, ok := e.st.Resources[refID(p["AppArn"])]; ok && r.Type == "AWS::ResilienceHub::App" {
			return // billed on the app
		}
		e.monthly(1, "", "resiliencehub|PaidTierApplication", "the application it assesses (not in this template)")
	}
	rules["AWS::ResilienceHubV2::Service"] = func(e *est, p map[string]any) {
		e.none("billed monthly after its first failure mode assessment, which is not started by CloudFormation")
	}
	rules["AWS::ApplicationSignals::ServiceLevelObjective"] = func(e *est, p map[string]any) {
		if p["RequestBasedSli"] != nil {
			panic(unresolved("request-based SLOs: the signals they generate are not documented"))
		}
		sec := e.num(asMap(asMap(p["Sli"])["SliMetric"]), "PeriodSeconds", 60)
		n := 2 * hoursPerMonth * 3600 / sec
		e.add(n, "signals", n*e.price("appsignals|Application-Signals"), fmt.Sprintf("2 Application Signals per %gs SLI period", sec))
	}
	rules["AWS::AgentRegistry::RegistryRecord"] = func(e *est, p map[string]any) {
		e.monthly(1, "", "agentregistry|Registry:Consumption-based:Records", "registry record")
	}
	rules["AWS::BedrockAgentCore::GatewayTarget"] = gatewayTarget
	rules["AWS::FinSpace::Environment"] = func(e *est, p map[string]any) {
		if p["SuperuserParameters"] != nil {
			e.monthly(1, "", "finspace|StandardUser", "the superuser it creates (other users not counted)")
		}
	}
	rules["AWS::AccessAnalyzer::Analyzer"] = accessAnalyzer
	rules["AWS::SSMQuickSetup::ConfigurationManager"] = func(e *est, p map[string]any) {
		defs, _ := p["ConfigurationDefinitions"].([]any)
		for _, d := range defs {
			switch t := e.str(asMap(d), "Type", ""); t {
			case "AWSQuickSetupType-DevOpsGuru":
				panic(unresolved("enables DevOps Guru, billed per analyzed resource-hour"))
			case "AWSQuickSetupType-CFGRecording", "AWSQuickSetupType-ConformancePacks":
				panic(unresolved(t + " turns on AWS Config, billed per configuration item and rule evaluation"))
			}
		}
	}

	// Schedules: runs bill whether or not there is new data, but the work per run is not in the template.
	rules["AWS::AppFlow::Flow"] = func(e *est, p map[string]any) {
		tc := asMap(p["TriggerConfig"])
		if e.str(tc, "TriggerType", "") != "Scheduled" || in(e.str(p, "FlowStatus", "Active"), "Suspended", "Draft") {
			return
		}
		panic(unresolved("runs on " + e.str(asMap(tc["TriggerProperties"]), "ScheduleExpression", "a schedule") + "; each run is billed, even when it finds no new data"))
	}
	rules["AWS::DataBrew::Schedule"] = func(e *est, p map[string]any) {
		panic(unresolved("runs its jobs on " + e.mustStr(p, "CronExpression") + "; node-hours per run are not known"))
	}
	rules["AWS::DataSync::Task"] = func(e *est, p map[string]any) {
		s := asMap(p["Schedule"])
		if x := e.str(s, "ScheduleExpression", ""); x != "" && e.str(s, "Status", "ENABLED") == "ENABLED" && e.str(p, "TaskMode", "BASIC") == "ENHANCED" {
			panic(unresolved("runs on " + x + "; each Enhanced mode execution is billed"))
		}
	}
	rules["AWS::ImageBuilder::ImagePipeline"] = func(e *est, p map[string]any) {
		if x := e.str(asMap(p["Schedule"]), "ScheduleExpression", ""); x != "" && e.str(p, "Status", "ENABLED") == "ENABLED" {
			panic(unresolved("builds on " + x + "; build instance time per run is not known"))
		}
	}
	rules["AWS::Timestream::ScheduledQuery"] = func(e *est, p map[string]any) {
		panic(unresolved("runs on " + e.mustStr(asMap(p["ScheduleConfiguration"]), "ScheduleExpression") + "; query TCU time per run is not known"))
	}

	rules["AWS::SageMaker::App"] = sageMakerApp
	rules["AWS::SageMaker::Space"] = func(e *est, p map[string]any) {
		s := asMap(p["SpaceSettings"])
		if !in(e.str(s, "AppType", ""), "JupyterLab", "CodeEditor") {
			return // Studio Classic spaces use the domain's EFS
		}
		gb := e.num(asMap(asMap(s["SpaceStorageSettings"])["EbsStorageSettings"]), "EbsVolumeSizeInGb", 5)
		e.monthly(gb, "GB", "studio|Studio:VolumeUsage.gp3", "space storage (apps in the space not counted)")
	}
	rules["AWS::SageMaker::Cluster"] = hyperPod
	rules["AWS::SageMaker::FeatureGroup"] = func(e *est, p map[string]any) {
		os := asMap(p["OnlineStoreConfig"])
		if e.flag(os, "EnableOnlineStore") && e.str(os, "StorageType", "Standard") == "InMemory" {
			e.hourly(5.37, "GB", "featurestore|FeatureStore:InMemoryStorage", "in-memory online store minimum (5 GiB)")
		}
		tc := asMap(p["ThroughputConfig"])
		if e.str(tc, "ThroughputMode", "OnDemand") == "Provisioned" {
			e.hourly(e.mustNum(tc, "ProvisionedReadCapacityUnits"), "RCU", "featurestore|FeatureStore:ReadCapacityUnit-Hours", "provisioned read capacity")
			e.hourly(e.mustNum(tc, "ProvisionedWriteCapacityUnits"), "WCU", "featurestore|FeatureStore:WriteCapacityUnit-Hours", "provisioned write capacity")
		}
	}
	rules["AWS::SageMaker::InferenceExperiment"] = func(e *est, p map[string]any) {
		if in(e.str(p, "DesiredState", "Running"), "Completed", "Cancelled") {
			return
		}
		shadow := map[string]bool{}
		l, _ := asMap(p["ShadowModeConfig"])["ShadowModelVariants"].([]any)
		for _, v := range l {
			shadow[e.str(asMap(v), "ShadowModelVariantName", "")] = true
		}
		vs, _ := p["ModelVariants"].([]any)
		for _, v := range vs {
			v := asMap(v)
			name := e.str(v, "VariantName", "")
			if !shadow[name] {
				continue // the production variant is billed on the endpoint
			}
			c := asMap(asMap(v["InfrastructureConfig"])["RealTimeInferenceConfig"])
			t := e.mustStr(c, "InstanceType")
			e.hourly(e.mustNum(c, "InstanceCount"), "", "sagemaker|"+t, t+" hosting (shadow variant "+name+") while the experiment runs, up to 30 days")
		}
	}
}

// none adds a $0 line that says why a type that can cost money costs nothing here.
func (e *est) none(reason string) {
	e.items = append(e.items, Item{Quantity: "×0", Reason: reason})
}

// comprehendEndpoint bills the provisioned inference units, priced per IU-second.
func comprehendEndpoint(e *est, p map[string]any) {
	iu := e.mustNum(p, "DesiredInferenceUnits")
	e.add(iu, "IU", iu*3600*hoursPerMonth*e.price("comprehend|Endpoint-Time"), "provisioned inference units")
}

// kendraDataSource bills the connector fee of a GenAI Enterprise index, once
// per index; on other editions a scheduled sync bills its run time.
func kendraDataSource(e *est, p map[string]any) {
	idx, r := e.ref(p, "IndexId")
	if e.mustStr(r.Properties, "Edition") != "GEN_AI_ENTERPRISE_EDITION" {
		if s := e.str(p, "Schedule", ""); s != "" {
			panic(unresolved("syncs on " + s + "; sync hours per run are not known"))
		}
		return
	}
	for _, id := range sortedIDs(e.st.Resources) {
		if id == e.id {
			break
		}
		if o := e.st.Resources[id]; o.Type == e.r.Type && refID(o.Properties["IndexId"]) == idx {
			e.add(1, "", 0, "shares the connector fee of "+e.st.DisplayPath(id))
			return
		}
	}
	e.monthly(1, "", "kendra|Kendra-Connector-MonthlyFee", "connector fee of GenAI Enterprise index "+e.st.DisplayPath(idx)+" (once per index)")
}

// emrFleet prices an EMR instance fleet that launches one instance type on
// demand, like an instance group of ceil(TargetOnDemandCapacity / WeightedCapacity).
func (e *est) emrFleet(f map[string]any, role string) {
	if e.num(f, "TargetSpotCapacity", 0) > 0 {
		panic(unresolved("Spot capacity in instance fleets is not supported"))
	}
	od := e.num(f, "TargetOnDemandCapacity", 0)
	if od == 0 {
		return
	}
	l, _ := f["InstanceTypeConfigs"].([]any)
	if len(l) != 1 {
		panic(unresolved("EMR chooses among several InstanceTypeConfigs"))
	}
	c := asMap(l[0])
	n := math.Ceil(od / e.num(c, "WeightedCapacity", 1))
	e.emrGroup(map[string]any{"InstanceType": c["InstanceType"], "InstanceCount": n, "EbsConfiguration": c["EbsConfiguration"]}, role)
}

// gatewayTarget bills semantic search indexing of the tools of a target whose
// gateway, in the same template, has SearchType SEMANTIC.
func gatewayTarget(e *est, p map[string]any) {
	_, g := e.ref(p, "GatewayIdentifier")
	if e.str(asMap(asMap(g.Properties["ProtocolConfiguration"])["Mcp"]), "SearchType", "") != "SEMANTIC" {
		return
	}
	ts := asMap(asMap(asMap(asMap(p["TargetConfiguration"])["Mcp"])["Lambda"])["ToolSchema"])
	n := e.length(ts, "InlinePayload")
	if n < 0 {
		panic(unresolved("the tools are not listed inline; semantic search indexes each one"))
	}
	e.monthly(float64(n), "tools", "agentcore|Gateway:Consumption-based:Tool-Indexing", "tools indexed for semantic search")
}

// accessAnalyzer bills internal access analysis per monitored resource when the
// analyzer lists them. External access analyzers are free; unused access
// analyzers bill per IAM role and user in the account.
func accessAnalyzer(e *est, p map[string]any) {
	t := e.mustStr(p, "Type")
	switch {
	case in(t, "ACCOUNT", "ORGANIZATION"):
		return
	case strings.HasSuffix(t, "_UNUSED_ACCESS"):
		panic(unresolved("bills each IAM role and user in the account monthly"))
	case !strings.HasSuffix(t, "_INTERNAL_ACCESS"):
		panic(unresolved("Type " + t + " is not supported"))
	}
	rule := asMap(asMap(asMap(p["AnalyzerConfiguration"])["InternalAccessConfiguration"])["InternalAccessAnalysisRule"])
	l, _ := rule["Inclusions"].([]any)
	n := 0
	for _, inc := range l {
		inc := asMap(inc)
		if inc["ResourceTypes"] != nil || inc["AccountIds"] != nil || inc["ResourceArns"] == nil {
			n = -1
			break
		}
		n += e.length(inc, "ResourceArns")
	}
	if n <= 0 {
		panic(unresolved("bills each monitored resource monthly; the resources are not listed by ARN"))
	}
	e.monthly(float64(n), "resources", "accessanalyzer|AccessFinding-Monitored-AWS-Resources", "internal access analysis per monitored resource")
}

// sageMakerApp bills a Studio app while it runs. JupyterServer runs on a free system instance.
func sageMakerApp(e *est, p map[string]any) {
	at := e.mustStr(p, "AppType")
	if at == "Canvas" {
		e.hourly(1, "", "studio|Canvas:Session-Hrs", "Canvas workspace instance (billed until the app is deleted or logged out)")
		return
	}
	prefix := map[string]string{"KernelGateway": "Studio:KernelGateway-", "RSessionGateway": "RStudio:RSessionGateway-", "RStudioServerPro": "RStudio:RServer-", "JupyterServer": ""}
	pre, ok := prefix[at]
	if !ok {
		panic(unresolved("AppType " + at + " is not supported"))
	}
	t := e.str(asMap(p["ResourceSpec"]), "InstanceType", "")
	if pre == "" || t == "system" {
		return
	}
	if t == "" {
		panic(unresolved("ResourceSpec.InstanceType is not set; the domain's default applies"))
	}
	e.hourly(1, "", "studio|"+pre+t, t+" "+at+" app")
}

// hyperPod bills the instances of each instance group of a SageMaker HyperPod
// cluster (the minimum with auto scaling) and their extra EBS volumes.
func hyperPod(e *est, p map[string]any) {
	l, _ := p["InstanceGroups"].([]any)
	for _, g := range l {
		g := asMap(g)
		if g["InstanceRequirements"] != nil {
			panic(unresolved("HyperPod chooses among several instance types"))
		}
		if asMap(g["CapacityRequirements"])["Spot"] != nil {
			panic(unresolved("Spot instance groups are not supported"))
		}
		n, why := e.mustNum(g, "InstanceCount"), "InstanceCount"
		if p["AutoScaling"] != nil {
			n, why = e.mustNum(g, "MinInstanceCount"), "MinInstanceCount"
		}
		t := e.mustStr(g, "InstanceType")
		name := e.str(g, "InstanceGroupName", "")
		e.hourly(n, "", "hyperpod|"+t, fmt.Sprintf("%s HyperPod instances (%s), %s", t, name, why))
		cs, _ := g["InstanceStorageConfigs"].([]any)
		for _, c := range cs {
			if v := asMap(asMap(c)["EbsVolumeConfig"]); v != nil {
				e.monthly(n*e.mustNum(v, "VolumeSizeInGB"), "GB", "hyperpod|VolumeUsage.gp2", "extra EBS volume per instance ("+name+")")
			}
		}
	}
}
