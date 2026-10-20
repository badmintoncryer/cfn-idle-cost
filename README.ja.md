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
| `--format <f>` | 出力形式です。`text`（既定）、`md`（プルリクエストのコメント向け）、`json`、`html`から選びます。`html`を選ぶと、ブラウザで開くページを出します。ページには、カテゴリ別の費用と、並べ替えと絞り込みのできる表が載ります（例: `npx cfn-idle-cost --format html > idle-cost.html`）。`html`はまだ`--base`と一緒に使えません。 |
| `--max <usd>` | アイドルコストの合計が月`usd`ドルを超えたら、終了コード`3`で終わります。 |
| `--max-increase <usd>` | `--base`と一緒に使います。アイドルコストの増加が月`usd`ドルを超えたら、終了コード`3`で終わります。 |
| `--region <r>` | リージョンを持たないスタック（envを指定していないCDKスタックと、素のテンプレート）に使うリージョンです。`env`を指定したスタックは、そのリージョンのまま見積もります。リージョンのないスタックがあるのにこのフラグがなければ、エラーになります。 |
| `--parameter [Stack:]Key=Value` | テンプレートのパラメータの値を指定します。そのパラメータを持つすべてのスタックに効き、`Stack:`を付けるとそのスタックだけに効きます。複数指定できます。指定しなかったパラメータには`Default`を使います。 |
| `--live` | 同梱のスナップショットではなく、[Price List Query API](https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/using-price-list-query-api.html)から最新の価格を取得します。`pricing:GetProducts`を許可したAWSの認証情報が必要です。 |
| `--version` | バージョンを表示します。 |

終了コードは、成功が`0`、エラーが`1`、使い方の誤り（リージョンのないスタックを含みます）が`2`、`--max`か`--max-increase`の超過が`3`です。見積もれなかったリソースがあっても、成功なら`0`を返します。予算のフラグを付けたときに見積もれなかったリソースがあると、実際のアイドルコストはもっと高いかもしれないという警告を出します。`--max`はこの場合も失敗にしません。一方、`--max-increase`は、見積もれないリソースが追加されたときや、見積もれていたリソースが見積もれなくなったときに失敗します。増加額を確かめられないためです。`? no rule`のリソースは警告の件数に含めますが、予算のチェックを失敗させることはありません。

## プルリクエストで使う

`--base`を付けると、変更によってアイドルコストがどう変わるかを表示します。

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

リソースは、スタック名と論理IDで対応づけます。1行は1リソース全体の金額で、説明にはいちばん高い料金を書きます。`+ 1 more`は、データベースのストレージのように、ほかの料金も金額に含まれていることを表します。見積もれない状態になったリソースや、見積もれるようになったリソースでは、金額の欄が`?`です。こうしたリソースは変更前と変更後の両方の合計から外すので、節約したように見えることはありません。そのときは、見積もれなかった変更の数を見出しに書きます。

GitHub Actionsでは、変更前後の両方をsynthし、Markdownをコメントとして投稿します。増加額が予算を超えたら、チェックを失敗にします。

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

このActionはプルリクエストにコメントを1件投稿し、プッシュのたびにそのコメントを更新します。同じMarkdownをジョブのサマリーにも書き出し、予算を超えたときはジョブを失敗させます。フォークやDependabotからのプルリクエストではトークンが読み取り専用なので、コメントできなかったことを警告するだけにします。実行にはLinuxかmacOSのランナーが必要です。npmのパッケージには、まだWindows用のバイナリがありません。

| 入力 | 内容 |
|---|---|
| `path` | プルリクエスト側の`cdk.out`またはテンプレート（空白区切りで複数可）。デフォルトは`cdk.out`です。 |
| `base` | ベースブランチ側の`cdk.out`またはテンプレート。指定しないと、コメントにはアイドルコストの合計を載せます。 |
| `max`、`max-increase` | 同名のフラグと同じです。 |
| `args` | そのほかのフラグ。たとえば`--region us-east-1 --parameter Env=prod`です。 |
| `comment` | `false`にするとコメントを投稿しません。デフォルトは`true`です。 |
| `github-token` | コメントに使うトークン。デフォルトは`github.token`です。 |

`@v1`は1.xの最新のリリースを使います。`@v1.0.0`のように書くと、そのバージョンに固定できます。

`cdk synth`は`cdk.context.json`からコンテキストを読みます。このファイルをコミットしておかないと、lookupのためにsynthでAWSの認証情報が必要になります。

## 見積もる費用

アクセスがゼロでもかかる費用だけを見積もります。時間単位で課金されるリソース、月額の固定料金、使っていなくても課金されるプロビジョニング済みのストレージの3種類です。

| リソース | 見積もる費用 |
|---|---|
| `AWS::EC2::NatGateway` | 時間料金 |
| `AWS::EC2::EIP` | パブリックIPv4アドレス |
| `AWS::EC2::VPCEndpoint` | Interface型は時間料金×サブネット数（AZ数）。Gateway Load Balancer型は時間料金。Gateway型は無料 |
| `AWS::ElasticLoadBalancingV2::LoadBalancer` | ALB、NLB、GWLBの時間料金。internet-facingなら、サブネットごとにパブリックIPv4を1つ（`dualstack-without-public-ipv4`なら数えません） |
| `AWS::ElasticLoadBalancing::LoadBalancer` | Classic Load Balancerの時間料金 |
| `AWS::EC2::Instance` | インスタンスの時間料金（Linux）、`BlockDeviceMappings`のEBSボリューム、起動時に割り当てられるパブリックIPv4 |
| `AWS::AutoScaling::AutoScalingGroup` | インスタンスの時間料金×`DesiredCapacity`（なければ`MinSize`）、EBSボリューム、起動時に割り当てられるパブリックIPv4。起動テンプレートか起動設定から読みます |
| `AWS::EC2::Volume` | 容量、gp3の3000 IOPS・125 MiB/sを超える分、io1・io2のIOPS |
| `AWS::RDS::DBInstance` | インスタンスの時間料金（シングルAZ・マルチAZ。MySQL、PostgreSQL、MariaDBと、ライセンスモデルごとのOracleとSQL Server）、割り当てたストレージ、プロビジョンドIOPS、gp3の基準値を超えるIOPSとスループット。Auroraは、プロビジョンドのインスタンスか、Serverless v2の最小ACU（I/O-Optimizedにも対応） |
| `AWS::RDS::DBCluster`（マルチAZ DBクラスター） | クラスターの時間料金（ライター1台と読み取り可能なスタンバイ2台）、割り当てたストレージ、プロビジョンドIOPS（gp3のIOPSとスループットにはまだ対応していません） |
| `AWS::OpenSearchService::Domain`、`AWS::Elasticsearch::Domain` | データノード、専用マスターノード、UltraWarmノード、EBSの容量・IOPS・スループット |
| `AWS::ElastiCache::CacheCluster`、`ReplicationGroup` | ノードの時間料金×ノード数 |
| `AWS::ElastiCache::ServerlessCache` | データストレージの最小量（1 GB。Valkeyは100 MB。`CacheUsageLimits`で最小量を指定していればその値） |
| `AWS::DynamoDB::Table` | プロビジョニングしたRCU・WCU（GSIを含みます。StandardとStandard-IAに対応） |
| `AWS::DynamoDB::GlobalTable` | スタックと同じリージョンにあるレプリカの、プロビジョニングしたRCUとAuto Scalingの最小WCU（GSIを含みます。レプリカが2つ以上なら、書き込みにはレプリケートされたWCUの料金を使います）。ほかのリージョンのレプリカは、数を行に書くだけで、料金には含めません |
| `AWS::Kinesis::Stream` | プロビジョンドのシャード時間（24時間を超える延長保持を含む）、またはオンデマンドのストリーム時間 |
| `AWS::ECS::Service`（Fargate） | vCPU、メモリ、20 GBを超えるエフェメラルストレージ、パブリックIPv4（`AssignPublicIp: ENABLED`のとき）×`DesiredCount`（x86とARMのLinux、Windows。WindowsはvCPUごとのOSライセンス料金も含みます） |
| `AWS::EFS::FileSystem` | プロビジョンドスループット |
| `AWS::FSx::FileSystem` | Windows、Lustre、OpenZFS、ONTAPのストレージ容量とスループット容量。容量1 GBあたり3 IOPSを超えるSSD IOPS（ONTAPの容量プールは使った分だけかかるので含みません） |
| `AWS::KMS::Key`、`AWS::KMS::ReplicaKey` | キー1つにつき$1 |
| `AWS::SecretsManager::Secret` | シークレットとレプリカごとの料金 |
| `AWS::Route53::HostedZone` | ホストゾーンごとの料金 |
| `AWS::EKS::Cluster` | クラスターの時間料金。`Version`が価格の基準日（同梱のスナップショットの日付、`--live`なら当日）に標準サポートを過ぎていれば、延長サポートの料金も加えます |
| `AWS::EKS::Nodegroup` | インスタンスの時間料金×`DesiredSize`（なければ`MinSize`）。オンデマンドで、インスタンスタイプが1つの場合 |
| `AWS::NetworkFirewall::Firewall` | エンドポイントの時間料金×サブネット数。同じテンプレートにあるファイアウォールポリシーがTLSインスペクションの設定を持つときは、Advanced Inspectionの時間料金×サブネット数も加える |
| `AWS::WAFv2::WebACL` | Web ACLとルール。同じテンプレートのルールグループを参照していれば、そのグループのルールも数える（マネージドルールグループの追加料金は含みません） |
| `AWS::CloudWatch::Alarm`、`CompositeAlarm` | アラームのメトリクスごとの料金（標準解像度・高解像度）、複合アラームごとの料金 |
| `AWS::Lambda::Alias`、`AWS::Lambda::Version` | プロビジョニング済み同時実行数×関数のメモリ（x86とARM） |
| `AWS::Lambda::CapacityProvider` | Lambda Managed Instances。EC2の時間料金（Linux）と管理料金×このプロバイダを使う関数に最低限必要なインスタンス数。[前提](#前提)を参照してください。`AllowedInstanceTypes`でタイプを1つに決めている必要があります |
| `AWS::CloudWatch::Dashboard` | ダッシュボードごとに$3（公開価格。ダッシュボードはPrice Listにありません） |
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
| `AWS::OpenSearchServerless::Collection` | インデックス作成と検索の最小OCU（スタンバイレプリカありで1 + 1、なしで0.5 + 0.5）。コレクションはOCUを共有するので、スタック内でコレクションのタイプごとに1回だけ数えます（同じKMSキーを使う前提です）。コレクショングループに属するコレクションは見積もりません |
| `AWS::MemoryDB::Cluster` | ノードの時間単価 × シャード数 ×（1 + レプリカ数）（RedisとValkey） |
| `AWS::SageMaker::Endpoint` | プロダクションバリアントごとに、インスタンスの時間単価 × `InitialInstanceCount`（サーバーレスのバリアントはアイドル時$0） |
| `AWS::CodePipeline::Pipeline` | アクティブなV1パイプラインごとの料金（V2は実行時間の分だけかかるので、アイドル時は$0） |
| `AWS::ApiGateway::Stage` | `CacheClusterEnabled`のとき、`CacheClusterSize`に応じたキャッシュクラスターの時間料金 |
| `AWS::DirectoryService::MicrosoftAD` | ドメインコントローラー2台の時間料金（StandardとEnterprise）。あとから追加したドメインコントローラーはテンプレートに現れないため含みません |
| `AWS::DirectoryService::SimpleAD` | ディレクトリの時間料金（SmallとLarge） |
| `AWS::ACMPCA::CertificateAuthority` | CAごとの月額料金（汎用モードと短期証明書モード） |
| `AWS::DMS::ReplicationInstance` | インスタンスの時間料金（シングルAZ・マルチAZ）。インスタンスクラスに含まれる量を超えたストレージは含みません |
| `AWS::SageMaker::NotebookInstance` | インスタンスの時間料金とストレージ |
| `AWS::OSIS::Pipeline` | `MinUnits`のIngestion OCU |
| `AWS::EC2::CapacityReservation` | インスタンスの時間料金×`InstanceCount`（Linux、共有テナンシー） |
| `AWS::Synthetics::Canary` | 作成後に開始するカナリアの、`rate()`のスケジュールから求めた月の実行回数 |
| `AWS::Kinesis::StreamConsumer` | プロビジョンドのストリームのシャードごとの拡張ファンアウト料金 |
| `AWS::KafkaConnect::Connector` | MCU×ワーカー数（プロビジョンド、または自動スケーリングの最小値） |
| `AWS::MSK::Replicator` | レプリケーターの時間料金（レプリケートしたデータの料金は含みません） |
| `AWS::SSM::Parameter` | アドバンストパラメータごとの料金 |
| `AWS::Cassandra::Table` | プロビジョンドのRCUとWCU |
| `AWS::MWAA::Environment` | 環境クラスの時間料金と、環境に含まれる数を超えたワーカー、スケジューラー、ウェブサーバー |
| `AWS::AppSync::ApiCache` | `Type`に応じたキャッシュの時間料金 |
| `AWS::HealthLake::FHIRDatastore` | データストアの時間料金 |
| `AWS::Route53RecoveryControl::Cluster`、`AWS::Route53RecoveryReadiness::ReadinessCheck` | クラスターの時間料金、準備状況チェックの時間料金 |
| `AWS::VpcLattice::Service` | サービスの時間料金（リクエストとデータの料金は含みません） |
| `AWS::DocDBElastic::Cluster` | シャードのvCPU×シャード数×シャードごとのインスタンス数 |
| `AWS::Timestream::InfluxDBInstance` | インスタンスの時間料金（シングルAZ・マルチAZ）と割り当てたストレージ |
| `AWS::EMR::Cluster`、`AWS::EMR::InstanceGroupConfig` | オンデマンドのインスタンスグループの、EC2の時間料金（Linux）、EMRの料金、EBSボリューム。自動で終了するクラスターと、ステップが終わると終了するクラスターはアイドル時$0 |
| `AWS::KinesisAnalyticsV2::Application` | KPU（並列度÷KPUあたりの並列度）とオーケストレーション用の1KPU、KPUごとに50GBの実行時ストレージ。CloudFormationは停止した状態で作成するが、起動している前提で数える。StudioノートブックはKPUが2つ、ストレージが1KPU分多い。SQLアプリケーションは見積もらない |
| `AWS::NetworkManager::CoreNetwork` | `PolicyDocument`にあるエッジロケーションごとの時間料金。スタックのリージョンの価格で計算する |
| `AWS::EC2::EC2Fleet`、`AWS::EC2::SpotFleet` | インスタンスタイプが1つのフリートの、インスタンスの時間料金（Linux）×オンデマンドの目標容量と、起動テンプレートのEBSボリューム。Spotの分は数えない |
| `AWS::Route53::HealthCheck` | 月額料金とオプション機能（HTTPS、文字列照合、高速間隔、レイテンシー測定）。テンプレート内のリソースを監視するものはAWSエンドポイント、それ以外はAWS以外のエンドポイントの価格で計算する。`CALCULATED`と`CLOUDWATCH_METRIC`のヘルスチェックは、AWSエンドポイントの価格で計算する |
| `AWS::Kendra::Index` | エディション（Developer、Enterprise、GenAI Enterprise）の時間料金と、追加のクエリ・ストレージのキャパシティユニット |
| `AWS::EC2::VerifiedAccessEndpoint` | エンドポイントごとの時間料金。HTTPのアプリケーションと、HTTP以外（TCP、RDS、CIDR）のアプリケーションで価格が違う |
| `AWS::CloudWatch::InsightRule` | 有効なContributor Insightsルールごとの月額料金。一致したログイベントの料金は数えない |
| `AWS::SageMaker::MlflowTrackingServer` | サイズ（Small、Medium、Large）ごとのトラッキングサーバーの時間料金 |
| `AWS::NeptuneGraph::Graph` | プロビジョニングしたm-NCUの時間料金×（1＋レプリカ数） |
| `AWS::GameLift::Fleet` | ホームリージョンのインスタンスの時間料金×希望するインスタンス数（指定がなければ1）。LinuxかWindowsかはビルドから読む。Anywhereフリートはアイドル時$0 |
| `AWS::Batch::ComputeEnvironment` | Fargate、アンマネージド、`MinvCpus: 0`のコンピューティング環境はアイドル時$0 |
| `AWS::EC2::VPC` | `VpcEncryptionControl.Mode`を指定したときの、VPC暗号化コントロールの時間料金 |
| `AWS::EC2::TransitGateway` | `EncryptionSupport: enable`のとき、VPC暗号化コントロールの時間料金×同じテンプレートでアタッチしているVPCの数。暗号化コントロールを自分で設定しているVPCは、VPCの行で数える |
| `AWS::NetworkFirewall::VpcEndpointAssociation` | セカンダリエンドポイントの時間料金 |
| `AWS::RDS::DBProxyEndpoint` | インターフェイスエンドポイントの時間料金×サブネット数 |
| `AWS::EC2::VPCEndpointService` | アイドル時$0。`SupportedRegions`にほかのリージョンがあるとき（クロスリージョンのPrivateLink）は見積もらない |
| `AWS::Route53Resolver::FirewallRuleGroupAssociation` | 同じテンプレートにあるルールグループが`DnsThreatProtection`のルールを持つときの、DNS Firewall Advancedの時間料金 |
| `AWS::Route53Resolver::FirewallDomainList` | 月額料金×`Domains`のドメイン数。`DomainFileUrl`で読み込むリストは見積もらない |
| `AWS::Route53::TrafficPolicyInstance` | トラフィックフローのポリシーレコードごとの月額料金 |
| `AWS::ServiceDiscovery::Instance` | Cloud Mapに登録したインスタンスごとの月額料金 |
| `AWS::KinesisFirehose::DeliveryStream` | 宛先がVPC内のOpenSearch Service、OpenSearch Serverless、Elasticsearchのときの、VPC配信の時間料金×サブネット数 |
| `AWS::CloudFront::Distribution` | `SslSupportMethod`が`vip`のときの、専用IPカスタムSSLの月額料金 |
| `AWS::CloudFront::MonitoringSubscription` | 追加メトリクスを有効にしたときの、CloudWatchメトリクス8つ分の料金 |
| `AWS::CloudFront::AnycastIpList` | 時間料金×`IpCount`のIPv4アドレス数（BYOIPのリストとIPv6は$0） |
| `AWS::WAFv2::WebACLAssociation` | 関連付ける先がAmplifyアプリのときの、Amplify Hostingのファイアウォールの月額料金 |
| `AWS::CertificateManager::Certificate` | アイドル時$0。`CertificateExport: ENABLED`のときは、発行と更新のたびにドメイン名ごとの料金がかかるので見積もらない |

使った分だけかかる費用は、意図して対象から外しています。S3、Lambda、AuroraのストレージとI/O、CloudWatch Logs、データ転送、リクエスト料金がこれに当たります。

### まだ見積もらないもの

アクセスがゼロでも費用がかかるのに、まだルールのないリソースタイプのうち、よく使われるもの（WorkSpaces、AppStream 2.0、Lightsail、Elastic Beanstalkなど）は、`! not counted yet`に載せます。テンプレートから費用を見積もれないタイプも、ここに載せます。該当するのはアカウント単位のサービス（GuardDuty、Security Hub、Configのレコーダー、CloudWatchのOTelエンリッチメント、CloudWatch Logsのインテグレーション）、RDSのリザーブドインスタンスとElastiCacheやMemoryDBのリザーブドノード、Bedrockにインポートしたモデル、CloudFrontのディストリビューションテナント、OutpostsのRoute 53 Resolverです。アイドル時に費用がかからないとわかっているタイプ（IAM、S3、SQS、SNS、CloudWatch Logs、Lambda関数、サブネットやルートテーブルなど）は載せません。費用がかからないタイプは1つずつ名前で挙げていて、プレフィックスで判定するのは`Custom::`のリソースだけです。そのため、ほかのタイプが無料のサービスに新しいタイプが加わっても、そのタイプは`? no rule`に載ります。それ以外のルールのないタイプは、アイドル時$0とみなして`? no rule`に載せます。費用がかかるかもしれないリソースが、行を出さずに消えることはありません。一部だけ対応しているリソース（Fargate Spotとほかのキャパシティプロバイダ、SpotとWindowsのノードグループ、EMRのインスタンスフリート、Aurora Serverless v1、Local Zonesのインスタンス、cronのスケジュールで動くカナリア、Spotだけのフリート、複数のインスタンスタイプを使うフリート、`MinvCpus`が0より大きいBatchのコンピューティング環境、SpotやリモートロケーションのGameLiftフリート、プロビジョニングしたイベントポーラーを使うイベントソースマッピング）は、`? could not estimate`に載ります。

## 前提

- オンデマンドの定価（USD）で、1か月を730時間として計算します。Savings Plans、リザーブドインスタンス、無料利用枠は考慮しません。
- EC2インスタンスは、Linux・共有テナンシーの料金で見積もります（AMIは読みません）。AMIから決まるルートボリューム（`BlockDeviceMappings`に`VolumeSize`がないもの）は数えません。
- インスタンス、Auto Scalingグループ、Fargateのタスクに起動時に割り当てられるパブリックIPv4は、`AssociatePublicIpAddress`（Fargateは`AssignPublicIp`）で割り当てると指定しているときに数えます。指定がなければ、すべてのサブネットが同じテンプレートにあり、`MapPublicIpOnLaunch`がtrueのときに数えます。サブネットが別のスタックなどにあって決まらないときは、IPの行を出さず、リソースのほかの費用だけを見積もります。同じテンプレートでElastic IPを関連付けたインスタンスは、IPを二重に数えません。
- Lambda Managed Instancesのキャパシティプロバイダは、関数が最低限必要とするインスタンス数で見積もります。関数ごとの実行環境は`MinExecutionEnvironments`（既定は3）個で、1つあたりのメモリは`MemorySize`、vCPUは`MemorySize`÷`ExecutionEnvironmentMemoryGiBPerVCpu`（既定はvCPUあたり2GiB）です。この実行環境をインスタンスタイプに詰め込んだ台数と、実行環境の数（上限3、AZごとに1台）の大きいほうを使います。トラフィックが倍になっても耐えられるようにLambdaが確保する余裕は数えないので、実際の費用はもっと高くなることがあります。どの関数も使っていないプロバイダは3台で見積もります。関数は同じリージョンのすべての入力スタックから探します。プロバイダを指す方法は`Ref`か`Fn::GetAtt`、エクスポートを読む`Fn::ImportValue`（CDKのstrong参照）、`Fn::GetStackOutput`（CDKのweak参照）、ARNのどれでもかまいません。`AWS::Lambda::Version`の`FunctionScalingConfig`は読みません。
- CloudWatchダッシュボードはPrice Listにないので、公開価格の$3を組み込んでいます。`--live`でもこの値を使います。
- テンプレートから読み取れない値は推測しません。たとえば、Defaultのないパラメータや、別スタックへの参照です。こうしたリソースは、理由と一緒に`? could not estimate`に載ります。Defaultのあるパラメータは、そのDefaultで見積もり、そのことを行に書きます。別の値を使うときは`--parameter`で渡します。
- `Conditions`（`Fn::Equals`、`Fn::And`、`Fn::Or`、`Fn::Not`）と`Fn::If`は、パラメータと`AWS::Region`だけで決まるときに評価します。`Condition`がfalseのリソースは作られないので、一覧に載せません。`AWS::AccountId`など、それ以外の値で決まる条件を持つリソースは`? could not estimate`に載ります。
- Auto Scalingグループは、`DesiredCapacity`があればその値を、なければ`MinSize`を使います。DynamoDBは、Auto Scalingの最小値ではなく、テーブルに設定したプロビジョニング容量を使います。ただしグローバルテーブルは、書き込み容量をAuto Scalingの範囲でしか指定できないため、その最小値を使います。
- 素のテンプレートにはコンストラクトのパスがないため、行には論理IDを表示します。

## インストール

```sh
npm install -D cfn-idle-cost   # または npx cfn-idle-cost
go install github.com/badmintoncryer/cfn-idle-cost@latest
```

`cdk-idle-cost`は、同じツールの別名です。npmのパッケージはLinuxとmacOS（x64とarm64）に対応しています。Windowsでは、`go install`か、GitHub Releaseのバイナリを使ってください。

## ライセンス

Apache-2.0
