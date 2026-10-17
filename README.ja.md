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
  Vpc/PublicSubnet1/NATGateway  ×1            $45  NAT Gateway
  Vpc/SsmEndpoint               ×2 AZ         $20  interface endpoint
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
| `--base <path>` | 別の`cdk.out`（またはテンプレート）と比べて、変わった分だけを表示します。たとえば`main`ブランチからsynthした`cdk.out`を指定します。詳しくは「[プルリクエストで使う](#プルリクエストで使う)」を見てください。 |
| `--format <f>` | 出力形式です。`text`（既定）、`md`（プルリクエストのコメント向け）、`json`から選びます。 |
| `--max <usd>` | アイドルコストの合計が月`usd`ドルを超えたら、終了コード`3`で終わります。 |
| `--max-increase <usd>` | `--base`と一緒に使います。アイドルコストの増加が月`usd`ドルを超えたら、終了コード`3`で終わります。 |
| `--region <r>` | リージョンを持たないスタック（envを指定していないCDKスタックと、素のテンプレート）に使うリージョンです。`env`を指定したスタックは、そのリージョンのまま見積もります。リージョンのないスタックがあるのにこのフラグがなければ、エラーになります。 |
| `--parameter [Stack:]Key=Value` | テンプレートのパラメータの値を指定します。そのパラメータを持つすべてのスタックに効き、`Stack:`を付けるとそのスタックだけに効きます。複数指定できます。指定しなかったパラメータには`Default`を使います。 |
| `--live` | 同梱のスナップショットではなく、[Price List Query API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-price-list-query-api.html)から最新の価格を取得します。`pricing:GetProducts`を許可したAWSの認証情報が必要です。 |
| `--version` | バージョンを表示します。 |

終了コードは、成功が`0`、エラーが`1`、使い方の誤り（リージョンのないスタックを含みます）が`2`、`--max`か`--max-increase`の超過が`3`です。見積もれなかったリソースがあっても、成功なら`0`を返します。予算のフラグを付けたときに見積もれなかったリソースがあると、実際のアイドルコストはもっと高いかもしれないという警告を出します。ただし、この警告だけでは失敗にしません。

## プルリクエストで使う

`--base`を付けると、変更によってアイドルコストがどう変わるかを表示します。

```sh
npx cfn-idle-cost --base cdk.out.main cdk.out
```

```
ap-northeast-1 · idle ≈ $382 → $682/mo (+$300)

  ~ DataStack     Db                              $42 →  $322   +$280  db.t4g.micro postgres Multi-AZ → db.t4g.large postgres Multi-AZ
  + NetworkStack  Vpc/PublicSubnet2/NATGateway  $0.00 →   $45    +$45  NAT Gateway
  - DataStack     Stream                          $28 → $0.00    -$28  provisioned shards
  + NetworkStack  Vpc/PublicSubnet2/EIP         $0.00 → $3.65  +$3.65  public IPv4 address
```

リソースは、スタック名と論理IDで対応づけます。見積もれない状態になったリソースや、見積もれるようになったリソースでは、金額の欄が`?`です。

GitHub Actionsでは、変更前後の両方をsynthし、Markdownをコメントとして投稿します。増加額が予算を超えたら、チェックを失敗にします。

```yaml
name: idle-cost
on: pull_request
permissions: { contents: read, pull-requests: write }
jobs:
  idle-cost:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { path: head }
      - uses: actions/checkout@v4
        with: { path: base, ref: "${{ github.base_ref }}" }
      - uses: actions/setup-node@v4
        with: { node-version: 22 }
      - run: npm ci && npx cdk synth -q
        working-directory: head
      - run: npm ci && npx cdk synth -q
        working-directory: base
      - uses: badmintoncryer/cfn-idle-cost@v0
        with: { base: base/cdk.out, path: head/cdk.out, max-increase: 50 }
```

このActionはプルリクエストにコメントを1件投稿し、プッシュのたびにそのコメントを更新します。同じMarkdownをジョブのサマリーにも書き出し、予算を超えたときはジョブを失敗させます。

| 入力 | 内容 |
|---|---|
| `path` | プルリクエスト側の`cdk.out`またはテンプレート（空白区切りで複数可）。デフォルトは`cdk.out`です。 |
| `base` | ベースブランチ側の`cdk.out`またはテンプレート。指定しないと、コメントにはアイドルコストの合計を載せます。 |
| `max`、`max-increase` | 同名のフラグと同じです。 |
| `args` | そのほかのフラグ。たとえば`--region us-east-1 --parameter Env=prod`です。 |
| `comment` | `false`にするとコメントを投稿しません。デフォルトは`true`です。 |
| `github-token` | コメントに使うトークン。デフォルトは`github.token`です。 |

`@v0`は最新のリリースを使います。`@v0.0.5`のように書くと、そのバージョンに固定できます。

`cdk synth`は`cdk.context.json`からコンテキストを読みます。このファイルをコミットしておかないと、lookupのためにsynthでAWSの認証情報が必要になります。

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
| `AWS::EKS::Cluster` | クラスターの時間料金（標準サポート） |
| `AWS::EKS::Nodegroup` | インスタンスの時間料金×`DesiredSize`（なければ`MinSize`）。オンデマンドで、インスタンスタイプが1つの場合 |
| `AWS::NetworkFirewall::Firewall` | エンドポイントの時間料金×サブネット数 |
| `AWS::WAFv2::WebACL` | Web ACLとルール（マネージドルールグループの追加料金は含みません） |
| `AWS::CloudWatch::Alarm`、`CompositeAlarm` | アラームのメトリクスごとの料金（標準解像度・高解像度）、複合アラームごとの料金 |
| `AWS::Lambda::Alias`、`AWS::Lambda::Version` | プロビジョニング済み同時実行数×関数のメモリ（x86とARM） |
| `AWS::EC2::TransitGatewayAttachment`、`TransitGatewayVpcAttachment`、`TransitGatewayPeeringAttachment`、`TransitGatewayConnect` | アタッチメントの時間料金 |
| `AWS::EC2::VPNConnection` | 接続の時間料金。Transit Gatewayにつなぐ場合は、VPNアタッチメントの時間料金も加えます |
| `AWS::EC2::ClientVpnTargetNetworkAssociation` | 関連付けの時間料金 |
| `AWS::Route53Resolver::ResolverEndpoint` | 時間料金×IPアドレス数 |
| `AWS::GlobalAccelerator::Accelerator` | 固定の時間料金 |
| `AWS::DocDB::DBInstance`、`AWS::Neptune::DBInstance` | インスタンスの時間料金（I/O-Optimizedにも対応）、またはクラスターのサーバーレス最小容量 |
| `AWS::MSK::Cluster` | ブローカーの時間料金、ブローカーのストレージ、250 MiB/sを超えるストレージスループット（StandardとExpressのブローカー） |
| `AWS::MSK::ServerlessCluster` | クラスターの時間料金（パーティションの料金は含みません） |
| `AWS::Redshift::Cluster` | ノードの時間料金×ノード数 |
| `AWS::DAX::Cluster` | ノードの時間料金×レプリケーション係数 |
| `AWS::AmazonMQ::Broker` | ブローカーの時間料金（ActiveMQとRabbitMQ、シングル・マルチAZ） |
| `AWS::Transfer::Server` | プロトコルごとの時間料金 |
| `AWS::AppRunner::Service` | 最小数のプロビジョニング済みインスタンスのメモリ |
| `AWS::RDS::DBProxy` | ターゲットのインスタンスのvCPUごとの料金。Serverless v2のターゲットは最小ACUごとの料金（ターゲットが同じテンプレートにある場合だけ） |
| `AWS::OpenSearchServerless::Collection` | インデックス作成と検索の最小OCU（スタンバイレプリカありで1 + 1、なしで0.5 + 0.5）。コレクションはOCUを共有するので、スタック内でコレクションのタイプごとに1回だけ数えます。コレクショングループに属するコレクションは見積もりません |
| `AWS::MemoryDB::Cluster` | ノードの時間単価 × シャード数 ×（1 + レプリカ数）（RedisとValkey） |
| `AWS::SageMaker::Endpoint` | プロダクションバリアントごとに、インスタンスの時間単価 × `InitialInstanceCount`（サーバーレスのバリアントはアイドル時$0） |
| `AWS::CodePipeline::Pipeline` | アクティブなV1パイプラインごとの料金（V2は実行時間の分だけかかるので、アイドル時は$0） |

使った分だけかかる費用は、意図して対象から外しています。S3、Lambda、AuroraのストレージとI/O、CloudWatch Logs、データ転送、リクエスト料金がこれに当たります。

### まだ見積もらないもの

アクセスがゼロでも費用がかかるのに、まだルールのないリソースタイプ（EMR、WorkSpaces、Directory Service、CloudWatchダッシュボードなど）は、`! not counted yet`に載せます。そのため、こうした費用が黙って抜け落ちることはありません。一部だけ対応しているリソース（FSx for ONTAP、プロビジョンドモードのDynamoDBグローバルテーブル、RDSのOracleとSQL Server、マルチAZ DBクラスター、Fargate SpotとWindowsのFargate、Spotのノードグループ、Local Zonesのインスタンス、API Gatewayのキャッシュ）は、`? could not estimate`に載ります。

## 前提

- オンデマンドの定価（USD）で、1か月を730時間として計算します。Savings Plans、リザーブドインスタンス、無料利用枠は考慮しません。
- EC2インスタンスは、Linux・共有テナンシーの料金で見積もります（AMIは読みません）。AMIから決まるルートボリューム（`BlockDeviceMappings`に`VolumeSize`がないもの）と、インスタンスに自動で割り当てられるパブリックIPは数えません。
- テンプレートから読み取れない値は推測しません。たとえば、Defaultのないパラメータや、別スタックへの参照です。こうしたリソースは、理由と一緒に`? could not estimate`に載ります。Defaultのあるパラメータは、そのDefaultで見積もり、そのことを行に書きます。別の値を使うときは`--parameter`で渡します。
- `Conditions`（`Fn::Equals`、`Fn::And`、`Fn::Or`、`Fn::Not`）と`Fn::If`は、パラメータと`AWS::Region`だけで決まるときに評価します。`Condition`がfalseのリソースは作られないので、一覧に載せません。`AWS::AccountId`など、それ以外の値で決まる条件を持つリソースは`? could not estimate`に載ります。
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
