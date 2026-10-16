<p align="center">
  <img src="https://raw.githubusercontent.com/badmintoncryer/cfn-idle-cost/main/assets/logo.png" alt="cfn-idle-cost" width="104" height="104">
</p>

<h1 align="center">cfn-idle-cost</h1>

<p align="center">
  <strong>AWS CDKアプリが、アクセスゼロでも毎月いくらかかるかを表示します。</strong>
</p>

<p align="center">
  <a href="https://github.com/badmintoncryer/cfn-idle-cost/actions/workflows/ci.yml"><img src="https://github.com/badmintoncryer/cfn-idle-cost/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://www.npmjs.com/package/cfn-idle-cost"><img src="https://img.shields.io/npm/v/cfn-idle-cost.svg" alt="npm version"></a>
  <a href="https://www.npmjs.com/package/cfn-idle-cost"><img src="https://img.shields.io/npm/dt/cfn-idle-cost.svg" alt="npm total downloads"></a>
</p>

<p align="center"><a href="https://github.com/badmintoncryer/cfn-idle-cost/blob/main/README.md">English</a> | <b>日本語</b></p>

**AWS CDKとCloudFormationのためのツールです**。`cdk.out`を読み、アプリの月額の**アイドルコスト**を表示します。アイドルコストとは、誰も使っていなくてもかかる費用のことです。NAT Gateway、ロードバランサー、インスタンス、データベース、プロビジョニングした容量やストレージ、KMSキー、シークレットを、スタックごとにまとめて、CDKのコンストラクトのパスで示します。

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

オフラインで動き、AWSの認証情報は要りません。価格は、公開されている[AWS Bulk Price List](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-the-aws-price-list-bulk-api.html)から作ったスナップショットをパッケージに同梱しており、毎週更新しています。同じバージョンなら、同じ`cdk.out`から常に同じ金額が出ます。

## 使い方

```sh
npx cfn-idle-cost [flags] [cdk.out | template ...]
```

入力は`cdk.out`ディレクトリ（既定は`./cdk.out`。StageとNested Stackも含みます）か、CloudFormationテンプレート（JSONまたはYAML）です。

| フラグ | 内容 |
|---|---|
| `--region <r>` | リージョンを持たないスタック（envを指定していないCDKスタックと、素のテンプレート）に使うリージョンです。`env`を指定したスタックは、そのリージョンのまま見積もります。リージョンのないスタックがあるのにこのフラグがなければ、エラーになります。 |
| `--live` | 同梱のスナップショットではなく、[Price List Query API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-price-list-query-api.html)から最新の価格を取得します。`pricing:GetProducts`を許可したAWSの認証情報が必要です。 |
| `--json` | JSONで出力します（リージョン、スタック、明細の順の入れ子に、見積もれなかったリソースを加えたもの）。 |
| `--version` | バージョンを表示します。 |

終了コードは、成功が`0`、エラーが`1`、使い方の誤り（リージョンのないスタックを含みます）が`2`です。見積もれなかったリソースがあっても、成功なら`0`を返します。

## 見積もる費用

アクセスがゼロでもかかる費用だけを見積もります。時間単位で課金されるリソース、月額の固定料金、使っていなくても課金されるプロビジョニング済みのストレージの3種類です。

| リソース | 見積もる費用 |
|---|---|
| `AWS::EC2::NatGateway` | 時間料金 |
| `AWS::EC2::EIP` | パブリックIPv4アドレス |
| `AWS::EC2::VPCEndpoint` | Interface型は時間料金×サブネット数（AZ数）。Gateway Load Balancer型は時間料金。Gateway型は無料 |
| `AWS::ElasticLoadBalancingV2::LoadBalancer` | ALB、NLB、GWLBの時間料金。internet-facingなら、サブネットごとにパブリックIPv4を1つ |
| `AWS::ElasticLoadBalancing::LoadBalancer` | Classic Load Balancerの時間料金 |
| `AWS::EC2::Instance` | インスタンスの時間料金（Linux）と、`BlockDeviceMappings`のEBSボリューム |
| `AWS::AutoScaling::AutoScalingGroup` | インスタンスの時間料金×`DesiredCapacity`（なければ`MinSize`）とEBSボリューム。起動テンプレートか起動設定から読みます |
| `AWS::EC2::Volume` | 容量、gp3の3000 IOPS・125 MiB/sを超える分、io1・io2のIOPS |
| `AWS::RDS::DBInstance` | インスタンスの時間料金（シングルAZ・マルチAZ）、割り当てたストレージ、プロビジョンドIOPS、gp3の基準値を超えるIOPSとスループット。Auroraは、プロビジョンドのインスタンスか、Serverless v2の最小ACU（I/O-Optimizedにも対応） |
| `AWS::OpenSearchService::Domain`、`AWS::Elasticsearch::Domain` | データノード、専用マスターノード、UltraWarmノード、EBSの容量・IOPS・スループット |
| `AWS::ElastiCache::CacheCluster`、`ReplicationGroup` | ノードの時間料金×ノード数 |
| `AWS::ElastiCache::ServerlessCache` | データストレージの最小量（1 GB。Valkeyは100 MB。`CacheUsageLimits`で最小量を指定していればその値） |
| `AWS::DynamoDB::Table` | プロビジョニングしたRCU・WCU（GSIを含みます。StandardとStandard-IAに対応） |
| `AWS::Kinesis::Stream` | プロビジョンドのシャード時間、またはオンデマンドのストリーム時間 |
| `AWS::ECS::Service`（Fargate） | vCPU、メモリ、20 GBを超えるエフェメラルストレージ×`DesiredCount`（x86とARM） |
| `AWS::EFS::FileSystem` | プロビジョンドスループット |
| `AWS::FSx::FileSystem` | Windows、Lustre、OpenZFSのストレージ容量とスループット容量。容量1 GBあたり3 IOPSを超えるSSD IOPS |
| `AWS::KMS::Key`、`AWS::KMS::ReplicaKey` | キー1つにつき$1 |
| `AWS::SecretsManager::Secret` | シークレットごとの料金 |
| `AWS::Route53::HostedZone` | ホストゾーンごとの料金 |

使った分だけかかる費用は、意図して対象から外しています。S3、Lambda、AuroraのストレージとI/O、CloudWatch Logs、データ転送、リクエスト料金がこれに当たります。

### まだ見積もらないもの

CloudWatchアラーム、WAFのWeb ACL、Lambdaのプロビジョニング済み同時実行、Redshift、MSK、DocumentDB、Neptune、Transit Gatewayのアタッチメント、Client VPN、RDS Proxy、FSx for ONTAP、プロビジョンドモードのDynamoDBグローバルテーブル、RDSのOracleとSQL Server、マルチAZ DBクラスター、Fargate SpotとWindowsのFargateです。このうち一部に対応しているリソースタイプは「could not estimate」に載り、それ以外は表示されません。

## 前提

- オンデマンドの定価（USD）で、1か月を730時間として計算します。Savings Plans、リザーブドインスタンス、無料利用枠は考慮しません。
- EC2インスタンスは、Linux・共有テナンシーの料金で見積もります（AMIは読みません）。AMIから決まるルートボリューム（`BlockDeviceMappings`に`VolumeSize`がないもの）と、インスタンスに自動で割り当てられるパブリックIPは数えません。
- テンプレートから読み取れない値は推測しません。たとえば、Defaultのないパラメータ、別スタックへの参照、`Fn::If`、`Condition`付きのリソースです。こうしたリソースは、理由と一緒に`? could not estimate`に載ります。Defaultのあるパラメータは、そのDefaultで見積もり、そのことを行に書きます。
- Auto Scalingグループは、`DesiredCapacity`があればその値を、なければ`MinSize`を使います。DynamoDBは、Auto Scalingの最小値ではなく、テーブルに設定したプロビジョニング容量を使います。
- 素のテンプレートにはコンストラクトのパスがないため、行には論理IDを表示します。

## インストール

```sh
npm install -D cfn-idle-cost   # または npx cfn-idle-cost
go install github.com/badmintoncryer/cfn-idle-cost@latest
```

`cdk-idle-cost`は、同じツールの別名です。

## ライセンス

Apache-2.0
