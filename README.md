<p align="center">
  <img src="https://raw.githubusercontent.com/badmintoncryer/cfn-idle-cost/main/assets/logo.png" alt="cfn-idle-cost" width="104" height="104">
</p>

<h1 align="center">cfn-idle-cost</h1>

<p align="center">
  <strong>What your AWS CDK app costs with zero traffic.</strong>
</p>

<p align="center">
  <a href="https://github.com/badmintoncryer/cfn-idle-cost/actions/workflows/ci.yml"><img src="https://github.com/badmintoncryer/cfn-idle-cost/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://www.npmjs.com/package/cfn-idle-cost"><img src="https://img.shields.io/npm/v/cfn-idle-cost.svg" alt="npm version"></a>
  <a href="https://www.npmjs.com/package/cfn-idle-cost"><img src="https://img.shields.io/npm/dt/cfn-idle-cost.svg" alt="npm total downloads"></a>
</p>

<p align="center"><b>English</b> | <a href="https://github.com/badmintoncryer/cfn-idle-cost/blob/main/README.ja.md">日本語</a></p>

**For AWS CDK & CloudFormation.** Read `cdk.out` and print the monthly
**idle cost** of your app: what it costs even when nobody uses it. NAT
Gateways, load balancers, instances, databases, provisioned capacity and
storage, keys and secrets, grouped by stack and named by construct path.

```sh
cdk synth
npx cfn-idle-cost
```

```
ap-northeast-1 · 4 stacks · idle ≈ $382/mo

DataStack                                    $196/mo
  Aurora/writer                 ×0.5 ACU      $55  Serverless v2 minimum capacity
  Search                        ×1            $41  t3.small.search data nodes
  Search                        ×20 GB gp3  $2.93  EBS storage
  Db                            ×1            $36  db.t4g.micro postgres Multi-AZ
  Db                            ×20 GB gp3  $5.52  storage
  ...

NetworkStack                                  $69/mo
  Vpc/PublicSubnet1/NATGateway  ×1            $45  NAT Gateway
  Vpc/SsmEndpoint               ×2 AZ         $20  interface endpoint
  Vpc/PublicSubnet1/EIP         ×1          $3.65  public IPv4 address

? could not estimate: 1
  AppStack  ParamInstance  InstanceType is Parameter WorkerType

prices: bundled snapshot 2026-10-15 · on-demand list prices, 730 h/mo, USD, free tier ignored
```

It runs offline and needs no AWS credentials: prices come from a snapshot of
the public [AWS Bulk Price List](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-the-aws-price-list-bulk-api.html)
bundled in the package and refreshed weekly. The same `cdk.out` always gives
the same numbers for a given version.

## Usage

```sh
npx cfn-idle-cost [flags] [cdk.out | template ...]
```

Inputs are `cdk.out` directories (default: `./cdk.out`, including Stages and
nested stacks) or CloudFormation templates (JSON or YAML).

| Flag | What it does |
|---|---|
| `--base <path>` | Compare against another `cdk.out` (or template), e.g. one synthesized from `main`, and print only what changes. See [In pull requests](#in-pull-requests). |
| `--format <f>` | `text` (default), `md` (a pull request comment), `json`, or `html`: a page to open in a browser, with the cost by category and a table you can sort and filter (`npx cfn-idle-cost --format html > idle-cost.html`). `html` does not support `--base` yet. |
| `--max <usd>` | Exit `3` if the total idle cost is over `usd` per month. |
| `--max-increase <usd>` | With `--base`: exit `3` if the idle cost grows by more than `usd` per month. |
| `--region <r>` | Region for stacks that have none: env-agnostic CDK stacks and plain templates. Stacks with an `env` keep their own region. Without it, such stacks are an error. |
| `--parameter [Stack:]Key=Value` | Value of a template parameter, for every stack that has it or only `Stack`. Repeat it for more. Parameters without one use their `Default`. |
| `--live` | Fetch current prices from the [Price List Query API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-price-list-query-api.html) instead of the bundled snapshot. Needs AWS credentials with `pricing:GetProducts`. |
| `--version` | Print the version. |

Exit codes: `0` success (even when some resources could not be estimated), `1`
error, `2` usage error (including a stack without a region), `3` over `--max`
or `--max-increase`. When a budget flag is set and some resources could not be
estimated, a warning says the real idle cost may be higher. `--max` does not
fail on them, but `--max-increase` does when the pull request adds a resource
that can't be estimated or makes one unresolvable, since its increase can't be
checked. Resources under `? no rule` are in the warning but never fail a budget.

## In pull requests

`--base` shows what a change does to the idle cost:

```sh
npx cfn-idle-cost --base cdk.out.main cdk.out
```

```
ap-northeast-1 · idle ≈ $382 → $682/mo (+$300)

  ~ DataStack     Db                              $42 →  $322   +$280  db.t4g.micro postgres Multi-AZ + 1 more → db.t4g.large postgres Multi-AZ + 1 more
  + NetworkStack  Vpc/PublicSubnet2/NATGateway  $0.00 →   $45    +$45  NAT Gateway
  - DataStack     Stream                          $28 → $0.00    -$28  provisioned shards
  + NetworkStack  Vpc/PublicSubnet2/EIP         $0.00 → $3.65  +$3.65  public IPv4 address
```

Resources are matched by stack name and logical ID. Each line is the whole
resource, named by its most expensive charge; `+ 1 more` says the amount also
includes other charges, such as the storage of a database. A resource that becomes
(or stops being) unresolvable shows `?`, and is left out of both totals, so it
never reads as a saving; the heading then says how many changes could not be
estimated.

In GitHub Actions, synthesize both sides, post the Markdown as a comment, and
fail the check when the increase is over budget:

```yaml
name: idle-cost
on: pull_request
permissions: { contents: read, pull-requests: write }
jobs:
  idle-cost:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { path: head }
      - uses: actions/checkout@v7
        with: { path: base, ref: "${{ github.base_ref }}" }
      - uses: actions/setup-node@v7
        with: { node-version: 22 }
      - run: npm ci && npx cdk synth -q
        working-directory: head
      - run: npm ci && npx cdk synth -q
        working-directory: base
      - uses: badmintoncryer/cfn-idle-cost@v1
        with: { base: base/cdk.out, path: head/cdk.out, max-increase: 50 }
```

The action posts one comment and updates it on every push, writes the same
Markdown to the job summary, and fails the job when a budget is exceeded. On
pull requests from forks and Dependabot the token is read-only, so it only
warns that it could not comment. It needs a Linux or macOS runner: the npm
packages have no Windows binary yet.

| Input | What it does |
|---|---|
| `path` | `cdk.out` or templates (space-separated) of the pull request. Default `cdk.out`. |
| `base` | `cdk.out` or template of the base branch. Without it, the comment shows the total idle cost. |
| `max`, `max-increase` | Same as the flags. |
| `args` | More flags, e.g. `--region us-east-1 --parameter Env=prod`. |
| `comment` | `false` to skip the comment. Default `true`. |
| `github-token` | Token used to comment. Default `github.token`. |

`@v1` runs the latest 1.x release; `@v1.0.0` pins that version.

`cdk synth` reads context from `cdk.context.json`; commit it, or the synth
needs AWS credentials for lookups.

## What is counted

Only charges that run with zero traffic: hourly resources, fixed monthly fees,
and provisioned storage billed whether you use it or not.

| Resource | Counted |
|---|---|
| `AWS::EC2::NatGateway` | hourly |
| `AWS::EC2::EIP` | public IPv4 address |
| `AWS::EC2::VPCEndpoint` | Interface: hourly × subnets (AZs). Gateway Load Balancer: hourly. Gateway: free |
| `AWS::ElasticLoadBalancingV2::LoadBalancer` | ALB / NLB / GWLB hourly, plus one public IPv4 per subnet when internet-facing (none for `dualstack-without-public-ipv4`) |
| `AWS::ElasticLoadBalancing::LoadBalancer` | Classic Load Balancer hourly |
| `AWS::EC2::Instance` | instance hourly (Linux), EBS volumes in `BlockDeviceMappings`, the public IPv4 it gets at launch |
| `AWS::AutoScaling::AutoScalingGroup` | instance hourly × `DesiredCapacity` (or `MinSize`), EBS volumes and public IPv4 at launch, from its launch template or launch configuration |
| `AWS::EC2::Volume` | size, gp3 IOPS / throughput above 3000 / 125 MiB/s, io1 / io2 IOPS |
| `AWS::RDS::DBInstance` | instance hourly (Single-AZ / Multi-AZ; MySQL, PostgreSQL, MariaDB, Oracle and SQL Server by license model), allocated storage, provisioned IOPS and throughput above the gp3 baseline. Aurora: provisioned instances, or Serverless v2 minimum ACU (I/O-Optimized aware) |
| `AWS::RDS::DBCluster` (Multi-AZ DB cluster) | cluster hourly (writer and two readable standbys), allocated storage and provisioned IOPS (gp3 IOPS and throughput not supported yet) |
| `AWS::OpenSearchService::Domain`, `AWS::Elasticsearch::Domain` | data, dedicated master and UltraWarm nodes, EBS storage / IOPS / throughput |
| `AWS::ElastiCache::CacheCluster`, `ReplicationGroup` | node hourly × nodes |
| `AWS::ElastiCache::ServerlessCache` | minimum data storage (1 GB, 100 MB for Valkey, or your `CacheUsageLimits` minimum) |
| `AWS::DynamoDB::Table` | provisioned RCU / WCU, including GSIs (Standard and Standard-IA) |
| `AWS::DynamoDB::GlobalTable` | provisioned RCU and the auto scaling minimum WCU of the replica in the stack's region, including GSIs (replicated WCU with two or more replicas). Replicas in other regions are counted in the line, not priced |
| `AWS::Kinesis::Stream` | provisioned shard hours (and extended retention over 24 hours), or the on-demand stream hour |
| `AWS::ECS::Service` (Fargate) | vCPU, memory, ephemeral storage above 20 GB and public IPv4 (`AssignPublicIp: ENABLED`) × `DesiredCount` (x86 and ARM Linux, and Windows with its OS license per vCPU) |
| `AWS::EFS::FileSystem` | provisioned throughput |
| `AWS::FSx::FileSystem` | storage and throughput capacity for Windows, Lustre, OpenZFS and ONTAP, SSD IOPS above the included 3 per GB (the ONTAP capacity pool is pay-per-use) |
| `AWS::KMS::Key`, `AWS::KMS::ReplicaKey` | $1 per key |
| `AWS::SecretsManager::Secret` | per secret and per replica |
| `AWS::Route53::HostedZone` | per hosted zone |
| `AWS::EKS::Cluster` | cluster hourly, plus the extended support fee when `Version` is past the end of standard support on the date of the prices (the snapshot date, or today with `--live`) |
| `AWS::EKS::Nodegroup` | instance hourly × `DesiredSize` (or `MinSize`), on-demand, one instance type |
| `AWS::NetworkFirewall::Firewall` | endpoint hourly × subnets, plus Advanced Inspection hourly × subnets when its firewall policy (in the same template) has a TLS inspection configuration |
| `AWS::WAFv2::WebACL` | web ACL, its rules, and the rules of the rule groups in the same template it references (managed rule group fees not counted) |
| `AWS::CloudWatch::Alarm`, `CompositeAlarm` | per alarm metric (standard or high resolution), per composite alarm |
| `AWS::Lambda::Alias`, `AWS::Lambda::Version` | provisioned concurrency × function memory (x86 and ARM). Versions: the SnapStart cache per GB of memory, for a non-Java function in the same template |
| `AWS::Lambda::CapacityProvider` | Lambda Managed Instances: EC2 hourly (Linux) plus the management fee × the instances the functions that use it need at least. See [Assumptions](#assumptions). Needs `AllowedInstanceTypes` with one type |
| `AWS::CloudWatch::Dashboard` | $3 per dashboard (the list price; dashboards are not in the Price List) |
| `AWS::EC2::TransitGatewayAttachment`, `TransitGatewayVpcAttachment`, `TransitGatewayPeeringAttachment`, `TransitGatewayConnect` | attachment hourly |
| `AWS::EC2::VPNConnection` | connection hourly, plus the Transit Gateway VPN attachment |
| `AWS::EC2::ClientVpnTargetNetworkAssociation` | association hourly |
| `AWS::Route53Resolver::ResolverEndpoint` | hourly × IP addresses |
| `AWS::GlobalAccelerator::Accelerator` | fixed hourly fee |
| `AWS::DocDB::DBInstance`, `AWS::Neptune::DBInstance` | instance hourly (I/O-Optimized aware), or the cluster's serverless minimum capacity |
| `AWS::MSK::Cluster` | broker hourly, broker storage, storage throughput above 250 MiB/s (Standard and Express brokers) |
| `AWS::MSK::ServerlessCluster` | cluster hourly (partitions not counted) |
| `AWS::Redshift::Cluster` | node hourly × nodes |
| `AWS::DAX::Cluster` | node hourly × replication factor |
| `AWS::AmazonMQ::Broker` | broker hourly (ActiveMQ and RabbitMQ, single or multi-AZ) |
| `AWS::Transfer::Server` | hourly per protocol |
| `AWS::AppRunner::Service` | memory of the minimum provisioned instances |
| `AWS::RDS::DBProxy` | per vCPU of the target instances, or per ACU of Serverless v2 targets at their minimum (targets must be in the same template) |
| `AWS::OpenSearchServerless::Collection` | minimum indexing and search OCUs (1 + 1 with standby replicas, 0.5 + 0.5 without), once per collection type in a stack, since collections share OCUs (assuming they use the same KMS key). Collections in a collection group are not estimated |
| `AWS::MemoryDB::Cluster` | node hourly × shards × (1 + replicas) (Redis and Valkey) |
| `AWS::SageMaker::Endpoint` | instance hourly × `InitialInstanceCount` per production variant (serverless variants: $0 idle) |
| `AWS::CodePipeline::Pipeline` | per active V1 pipeline (V2 pipelines bill per minute: $0 idle) |
| `AWS::ApiGateway::Stage` | cache cluster hourly by `CacheClusterSize` when `CacheClusterEnabled` |
| `AWS::DirectoryService::MicrosoftAD` | 2 domain controllers hourly (Standard or Enterprise). Controllers added later are not in the template |
| `AWS::DirectoryService::SimpleAD` | directory hourly (Small or Large) |
| `AWS::ACMPCA::CertificateAuthority` | monthly per CA (general-purpose or short-lived certificate mode) |
| `AWS::DMS::ReplicationInstance` | instance hourly (Single-AZ / Multi-AZ). Storage above the amount included with the class is not counted |
| `AWS::SageMaker::NotebookInstance` | instance hourly and its storage |
| `AWS::OSIS::Pipeline` | `MinUnits` Ingestion OCUs |
| `AWS::EC2::CapacityReservation` | instance hourly × `InstanceCount` (Linux, shared tenancy) |
| `AWS::Synthetics::Canary` | runs per month from a `rate()` schedule, when it starts after creation |
| `AWS::Kinesis::StreamConsumer` | enhanced fan-out per shard of a provisioned stream |
| `AWS::KafkaConnect::Connector` | MCUs × workers (provisioned, or the auto scaling minimum) |
| `AWS::MSK::Replicator` | replicator hourly (data replicated not counted) |
| `AWS::SSM::Parameter` | per advanced parameter |
| `AWS::Cassandra::Table` | provisioned RCU / WCU |
| `AWS::MWAA::Environment` | environment class hourly, plus workers, schedulers and web servers above the included ones |
| `AWS::AppSync::ApiCache` | cache hourly by `Type` |
| `AWS::HealthLake::FHIRDatastore` | data store hourly |
| `AWS::Route53RecoveryControl::Cluster`, `AWS::Route53RecoveryReadiness::ReadinessCheck` | cluster hourly, readiness check hourly |
| `AWS::VpcLattice::Service` | service hourly (requests and data not counted) |
| `AWS::DocDBElastic::Cluster` | shard vCPUs × shards × instances per shard |
| `AWS::Timestream::InfluxDBInstance` | instance hourly (Single-AZ / Multi-AZ) and allocated storage |
| `AWS::EMR::Cluster`, `AWS::EMR::InstanceGroupConfig` | EC2 hourly (Linux), EMR fee and EBS volumes of on-demand instance groups. Clusters that auto-terminate or end with their steps: $0 idle |
| `AWS::KinesisAnalyticsV2::Application` | KPUs (parallelism / parallelism per KPU) plus one for orchestration, and 50 GB of running storage per KPU, as if it runs (CloudFormation creates it stopped). Studio notebooks: two extra KPUs and storage for one. SQL applications are not estimated |
| `AWS::NetworkManager::CoreNetwork` | hourly per edge location in `PolicyDocument`, priced in the stack's region |
| `AWS::EC2::EC2Fleet`, `AWS::EC2::SpotFleet` | instance hourly (Linux) × on-demand target capacity, and the EBS volumes of its launch template, when the fleet launches one instance type. Spot capacity is not counted |
| `AWS::Route53::HealthCheck` | monthly fee and optional features (HTTPS, string matching, fast interval, latency measurement). An endpoint that is a resource in the template is priced as an AWS endpoint, any other as non-AWS. Calculated and CloudWatch metric checks are priced as AWS endpoints |
| `AWS::Kendra::Index` | edition hourly (Developer, Enterprise, GenAI Enterprise) and additional query and storage capacity units |
| `AWS::EC2::VerifiedAccessEndpoint` | hourly per endpoint, at the HTTP or the non-HTTP (TCP, RDS, CIDR) application price |
| `AWS::CloudWatch::InsightRule` | monthly fee per enabled Contributor Insights rule (matched log events not counted) |
| `AWS::SageMaker::MlflowTrackingServer` | tracking server hourly by size (Small, Medium, Large) |
| `AWS::NeptuneGraph::Graph` | provisioned m-NCUs hourly × (1 + replicas) |
| `AWS::GameLift::Fleet` | instance hourly × desired instances (1 if not set) in the home Region, Linux or Windows from the build. Anywhere fleets: $0 idle |
| `AWS::Batch::ComputeEnvironment` | $0 idle for Fargate, unmanaged and `MinvCpus: 0` environments |
| `AWS::EC2::VPC` | VPC Encryption Controls hourly when `VpcEncryptionControl.Mode` is set |
| `AWS::EC2::TransitGateway` | with `EncryptionSupport: enable`, VPC Encryption Controls hourly × the VPCs attached to it in the same template (a VPC with its own encryption controls is counted on the VPC) |
| `AWS::NetworkFirewall::VpcEndpointAssociation` | secondary endpoint hourly |
| `AWS::RDS::DBProxyEndpoint` | interface endpoint hourly × subnets |
| `AWS::EC2::VPCEndpointService` | $0 idle, unless `SupportedRegions` lists another region (cross-Region PrivateLink, not estimated) |
| `AWS::Route53Resolver::FirewallRuleGroupAssociation` | DNS Firewall Advanced hourly when the rule group (in the same template) has a `DnsThreatProtection` rule |
| `AWS::Route53Resolver::FirewallDomainList` | monthly × `Domains` (lists imported with `DomainFileUrl` are not estimated) |
| `AWS::Route53::TrafficPolicyInstance` | monthly per Traffic Flow policy record |
| `AWS::ServiceDiscovery::Instance` | monthly per Cloud Map registered instance |
| `AWS::KinesisFirehose::DeliveryStream` | VPC delivery hourly × subnets, for OpenSearch Service, OpenSearch Serverless and Elasticsearch destinations in a VPC |
| `AWS::CloudFront::Distribution` | dedicated IP custom SSL monthly fee when `SslSupportMethod` is `vip` |
| `AWS::CloudFront::MonitoringSubscription` | 8 CloudWatch metrics when additional metrics are enabled |
| `AWS::CloudFront::AnycastIpList` | hourly × `IpCount` IPv4 addresses (BYOIP lists and IPv6: $0) |
| `AWS::WAFv2::WebACLAssociation` | Amplify Hosting firewall monthly fee when the resource is an Amplify app |
| `AWS::CertificateManager::Certificate` | $0 idle, unless `CertificateExport: ENABLED` (billed per name at issuance and renewal, not estimated) |
| `AWS::RDS::DBShardGroup` | Aurora Limitless minimum capacity (`MinACU`, by default 4 ACU per node of the `MaxACU` size) hourly, plus standby shards for `ComputeRedundancy` |
| `AWS::RDS::Deployment` | the green copy of a Blue/Green deployment, priced like its `Source` instance or cluster in the same template with the `Target*` settings |
| `AWS::AutoScaling::WarmPool` | the group's prepared instances (`MaxGroupPreparedCapacity`, by default `MaxSize`, minus `DesiredCapacity`, at least `MinSize`): EBS volumes while stopped or hibernated, instance hourly too while running |
| `AWS::GameLift::ContainerFleet` | Linux instance hourly × desired instances in the home Region (`MinSize` if not set, 1 with no locations). Scale-to-zero fleets: $0 idle |
| `AWS::GameLift::GameServerGroup` | `MinSize` × (EC2 hourly + FleetIQ fee) of the first instance type, for `ON_DEMAND_ONLY` groups (Linux assumed) |
| `AWS::ECS::ExpressGatewayService` | Fargate vCPU and memory (1 vCPU and 2 GB by default) and public IPv4 × `MinTaskCount`, plus the Application Load Balancer Express Mode services share, once per stack |
| `AWS::EKS::Capability` | hourly fee per Argo CD, ACK or kro capability (managed resources not counted) |
| `AWS::FSx::FileCache` | storage at $1.33/GB-month (the us-east-1 list price; File Cache is not in the Price List) |
| `AWS::Transfer::WebApp` | hourly per web app unit (1 if not set) |
| `AWS::AppConfig::ExperimentRun` | hourly while the experiment runs (creating it starts the run) |
| `AWS::Budgets::BudgetsAction` | daily fee of an action-enabled budget, once per budget in the stack |
| `AWS::CloudWatch::LogAlarm` | per alarm, as a standard alarm (scanned log data not counted) |
| `AWS::Glue::Trigger`, `AWS::Glue::Crawler` | $0 idle unless they run on a schedule or start a streaming job (see below) |

Usage-only charges are left out on purpose: S3, Lambda, Aurora storage and
I/O, CloudWatch Logs, data transfer, requests.

### Not counted yet

Common resource types that cost money with zero traffic but have no rule yet,
such as WorkSpaces, AppStream 2.0, Lightsail and Elastic Beanstalk, are listed
under `! not counted yet`. So are types whose cost the template can't size:
account-level services (GuardDuty, Security Hub, Config recorders, CloudWatch
OTel enrichment, CloudWatch Logs integrations), reservations (RDS, ElastiCache,
MemoryDB), Bedrock imported models, CloudFront distribution tenants and Route 53
Resolver on Outposts. Types known to cost nothing idle (IAM, S3, SQS, SNS,
CloudWatch Logs, Lambda functions, subnets and route tables, and so on) are
left out. That list names each type; only `Custom::` resources are matched by
prefix, so a new type in a service that is otherwise free still gets a line.
Every other type with no rule is listed under `? no rule` and assumed $0 idle,
so a resource that may cost money never disappears without a line.
Partly supported types (Fargate Spot and other capacity providers, Spot and
Windows node groups, EMR instance fleets, Aurora Serverless v1, instances in
Local Zones, canaries on a cron schedule, Spot-only fleets and fleets of several
instance types, Batch compute environments with `MinvCpus` above 0, and
GameLift fleets on Spot or in remote locations, FleetIQ game server groups
that use Spot, event source mappings with provisioned pollers, and Glue triggers
and crawlers that run on a schedule or start a streaming job) are listed under
`? could not estimate`.

## Assumptions

- On-demand list prices in USD, 730 hours a month. Savings Plans, Reserved
  Instances and the free tier are ignored.
- EC2 instances are priced as Linux with shared tenancy (the AMI is not read).
  A root volume that comes from the AMI (no `VolumeSize` in
  `BlockDeviceMappings`) is not counted.
- A public IPv4 that an instance, Auto Scaling group or Fargate task gets at
  launch is counted when `AssociatePublicIpAddress` (or `AssignPublicIp`) says
  so, or else when every subnet is in the template with `MapPublicIpOnLaunch:
  true`. When a subnet comes from elsewhere, no line is added and the rest of
  the resource is still estimated. An instance with an Elastic IP attached in
  the same template is not counted twice.
- A value that cannot be read from the template, such as a Parameter without a
  default or a reference to another stack, is never guessed: the resource is
  listed under `? could not estimate` with the reason. A Parameter with a
  `Default` is estimated with that default, and the line says so; pass
  `--parameter` to use another value.
- `Conditions` (`Fn::Equals`, `Fn::And`, `Fn::Or`, `Fn::Not`) and `Fn::If` are
  evaluated when they depend only on parameters and `AWS::Region`. A resource
  whose `Condition` is false is not created, so it is not listed. A condition
  on anything else, such as `AWS::AccountId`, makes the resource
  `? could not estimate`.
- An Auto Scaling group uses `DesiredCapacity` when set, otherwise `MinSize`.
  DynamoDB uses the table's provisioned capacity, not an auto scaling minimum.
  Global tables are the exception: their write capacity can only be set as
  an auto scaling range, so the minimum is used.
- A Lambda Managed Instances capacity provider is priced at the instances its
  functions need at least: each function's `MinExecutionEnvironments` (3 by
  default) × its memory and vCPUs (`MemorySize` /
  `ExecutionEnvironmentMemoryGiBPerVCpu`, 2 GiB per vCPU by default), packed
  onto the instance type, and at least one instance per environment up to three
  (one per AZ). The headroom Lambda keeps for traffic to double is not counted,
  so the real cost can be higher. A provider no function uses is priced at three
  instances. Functions are found in every input stack of the same region, by
  `Ref` / `Fn::GetAtt`, `Fn::ImportValue` of its export (strong CDK references),
  `Fn::GetStackOutput` (weak CDK references) or its ARN. The
  `FunctionScalingConfig` of an `AWS::Lambda::Version` is not read.
- CloudWatch dashboards are not in the Price List, so the $3 list price is
  built in and also used with `--live`.
- Plain templates have no construct paths, so lines show the logical ID.

## Install

```sh
npm install -D cfn-idle-cost   # or: npx cfn-idle-cost
go install github.com/badmintoncryer/cfn-idle-cost@latest
```

`cdk-idle-cost` is the same tool under another name. The npm packages cover
Linux and macOS (x64 and arm64); on Windows, use `go install` or the binary from
the GitHub Release.

## License

Apache-2.0
