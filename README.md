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
  Vpc/PublicSubnet1/NATGateway  ×1            $45  NAT Gateway hourly
  Vpc/SsmEndpoint               ×2 AZ         $20  interface endpoint hourly per AZ
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
| `--region <r>` | Region for stacks that have none: env-agnostic CDK stacks and plain templates. Stacks with an `env` keep their own region. Without it, such stacks are an error. |
| `--live` | Fetch current prices from the [Price List Query API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-price-list-query-api.html) instead of the bundled snapshot. Needs AWS credentials with `pricing:GetProducts`. |
| `--json` | Print JSON (regions → stacks → items, plus what could not be estimated). |
| `--version` | Print the version. |

Exit codes: `0` success (even when some resources could not be estimated), `1`
error, `2` usage error (including a stack without a region).

## What is counted

Only charges that run with zero traffic: hourly resources, fixed monthly fees,
and provisioned storage billed whether you use it or not.

| Resource | Counted |
|---|---|
| `AWS::EC2::NatGateway` | hourly |
| `AWS::EC2::EIP` | public IPv4 address |
| `AWS::EC2::VPCEndpoint` | Interface: hourly × subnets (AZs). Gateway Load Balancer: hourly. Gateway: free |
| `AWS::ElasticLoadBalancingV2::LoadBalancer` | ALB / NLB / GWLB hourly, plus one public IPv4 per subnet when internet-facing |
| `AWS::ElasticLoadBalancing::LoadBalancer` | Classic Load Balancer hourly |
| `AWS::EC2::Instance` | instance hourly (Linux), EBS volumes in `BlockDeviceMappings` |
| `AWS::AutoScaling::AutoScalingGroup` | instance hourly × `DesiredCapacity` (or `MinSize`), EBS volumes, from its launch template or launch configuration |
| `AWS::EC2::Volume` | size, gp3 IOPS / throughput above 3000 / 125 MiB/s, io1 / io2 IOPS |
| `AWS::RDS::DBInstance` | instance hourly (Single-AZ / Multi-AZ), allocated storage, provisioned IOPS and throughput above the gp3 baseline. Aurora: provisioned instances, or Serverless v2 minimum ACU (I/O-Optimized aware) |
| `AWS::OpenSearchService::Domain`, `AWS::Elasticsearch::Domain` | data, dedicated master and UltraWarm nodes, EBS storage / IOPS / throughput |
| `AWS::ElastiCache::CacheCluster`, `ReplicationGroup` | node hourly × nodes |
| `AWS::ElastiCache::ServerlessCache` | minimum data storage (1 GB, 100 MB for Valkey, or your `CacheUsageLimits` minimum) |
| `AWS::DynamoDB::Table` | provisioned RCU / WCU, including GSIs (Standard and Standard-IA) |
| `AWS::Kinesis::Stream` | provisioned shard hours, or the on-demand stream hour |
| `AWS::ECS::Service` (Fargate) | vCPU, memory and ephemeral storage above 20 GB × `DesiredCount` (x86 and ARM) |
| `AWS::EFS::FileSystem` | provisioned throughput |
| `AWS::FSx::FileSystem` | storage and throughput capacity for Windows, Lustre and OpenZFS, SSD IOPS above the included 3 per GB |
| `AWS::KMS::Key`, `AWS::KMS::ReplicaKey` | $1 per key |
| `AWS::SecretsManager::Secret` | per secret |
| `AWS::Route53::HostedZone` | per hosted zone |
| `AWS::EKS::Cluster` | cluster hourly (standard support) |
| `AWS::EKS::Nodegroup` | instance hourly × `DesiredSize` (or `MinSize`), on-demand, one instance type |
| `AWS::NetworkFirewall::Firewall` | endpoint hourly × subnets |
| `AWS::WAFv2::WebACL` | web ACL and its rules (managed rule group fees not counted) |
| `AWS::CloudWatch::Alarm`, `CompositeAlarm` | per alarm metric (standard or high resolution), per composite alarm |
| `AWS::Lambda::Alias`, `AWS::Lambda::Version` | provisioned concurrency × function memory (x86 and ARM) |
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

Usage-only charges are left out on purpose: S3, Lambda, Aurora storage and
I/O, CloudWatch Logs, data transfer, requests.

### Not counted yet

Resource types that cost money with zero traffic but have no rule yet, such as
RDS Proxy, OpenSearch Serverless, SageMaker endpoints, EMR, MemoryDB,
WorkSpaces and Directory Service, are listed under `! not counted yet`, so
their cost is never left out silently. Partly supported types (FSx for ONTAP,
provisioned DynamoDB global tables, Oracle and SQL Server on RDS, Multi-AZ DB
clusters, Fargate Spot and Windows, Spot node groups) are listed under
`? could not estimate`.

## Assumptions

- On-demand list prices in USD, 730 hours a month. Savings Plans, Reserved
  Instances and the free tier are ignored.
- EC2 instances are priced as Linux with shared tenancy (the AMI is not read).
  A root volume that comes from the AMI (no `VolumeSize` in
  `BlockDeviceMappings`) is not counted, and neither are public IPs that
  instances get automatically.
- A value that cannot be read from the template, such as a Parameter without a
  default, a reference to another stack, `Fn::If` or a resource with a
  `Condition`, is never guessed: the resource is listed under
  `? could not estimate` with the reason. A Parameter with a `Default` is
  estimated with that default, and the line says so.
- An Auto Scaling group uses `DesiredCapacity` when set, otherwise `MinSize`.
  DynamoDB uses the table's provisioned capacity, not an auto scaling minimum.
- Plain templates have no construct paths, so lines show the logical ID.

## Install

```sh
npm install -D cfn-idle-cost   # or: npx cfn-idle-cost
go install github.com/badmintoncryer/cfn-idle-cost@latest
```

`cdk-idle-cost` is the same tool under another name.

## License

Apache-2.0
