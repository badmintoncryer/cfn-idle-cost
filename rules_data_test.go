package main

import (
	"bytes"
	"testing"
)

// dataPrices are the us-east-1 list prices of the data, analytics and ML
// rules, fixed so the golden file doesn't move with the snapshot.
var dataPrices = &Prices{Source: "test prices", AsOf: "2026-10-20", Regions: map[string]map[string]float64{
	"us-east-1": {
		"grafana|Grafana:EditorUser": 9, "l4e|Inference-Hours-L4E": 0.25, "blockchain|Member|Fabric:Starter": 0.3,
		"blockchain|Node:bc.t3.small": 0.034, "datapipeline|InactivePipelines": 1, "healthimaging|DataStore-Hours": 5,
		"aoss|IndexingOCU": 0.24, "aoss|SearchOCU": 0.24, "comprehend|Endpoint-Time": 0.0005,
		"comprehend|DocClassifier-Storage": 0.5, "comprehend|EntityRecognizer-Storage": 0.5,
		"kendra|KendraIntelligentRanking-BaseCapacity": 0.14, "kendra|KendraIntelligentRanking-CapacityUnits": 0.0035,
		"kendra|Kendra-GenAI-Ent-Base-Capacity": 0.32, "kendra|Kendra-Enterprise-Edition": 1.4, "kendra|Kendra-Connector-MonthlyFee": 30,
		"athena|ReservedCapacityInDPUHours": 0.3, "qbusiness|Q-Business-Starter-Index": 0.14, "amp|AMP:CollectorHours": 0.04,
		"glue|DEVED-DPU-Hour": 0.44, "ec2|m5.xlarge": 0.192, "emr|m5.xlarge": 0.048, "ebs|EBS:VolumeUsage.gp2": 0.1,
		"elb|Load Balancer-Application": 0.0225, "influx|ClusterNodeUsage-Db.influx.medium": 0.12,
		"influx|ClusterNode-InfluxDBStorage-InfluxIOIncludedT1": 0.1, "resiliencehub|PaidTierApplication": 15,
		"appsignals|Application-Signals": 0.0000015, "agentregistry|Registry:Consumption-based:Records": 0.0004,
		"agentcore|Gateway:Consumption-based:Tool-Indexing": 0.0002, "finspace|StandardUser": 150,
		"accessanalyzer|AccessFinding-Monitored-AWS-Resources": 9, "studio|Studio:KernelGateway-ml.t3.medium": 0.05,
		"studio|Canvas:Session-Hrs": 1.9, "studio|Studio:VolumeUsage.gp3": 0.112, "hyperpod|ml.m5.large": 0.115,
		"hyperpod|VolumeUsage.gp2": 0.14, "featurestore|FeatureStore:InMemoryStorage": 0.233060643,
		"featurestore|FeatureStore:ReadCapacityUnit-Hours": 0.00013, "featurestore|FeatureStore:WriteCapacityUnit-Hours": 0.00065,
		"sagemaker|ml.m5.large": 0.115,
	},
}}

// Data, analytics and ML types: priced from the template, $0 until started
// outside CloudFormation, schedules, account-level costs, inference components
// on SageMaker endpoints, EMR instance fleets and OpenSearch collection groups.
func TestDataRules(t *testing.T) {
	stacks, err := LoadInputs([]string{"testdata/data.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	stacks[0].Region = "us-east-1"
	var b bytes.Buffer
	NewReport(stacks, Estimate(stacks, dataPrices), dataPrices).WriteText(&b)
	golden(t, "data.yaml", b.String())
}
