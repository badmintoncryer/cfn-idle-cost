package main

import (
	"html/template"
	"io"
	"strings"
)

// categories group resource types for the HTML report: by type first (EC2
// holds network and storage resources too), then by service. A type in
// neither is grouped under its service name.
var typeCategory = map[string]string{
	"AWS::EC2::NatGateway": "Network", "AWS::EC2::EIP": "Network", "AWS::EC2::VPCEndpoint": "Network",
	"AWS::EC2::TransitGatewayAttachment": "Network", "AWS::EC2::TransitGatewayVpcAttachment": "Network",
	"AWS::EC2::TransitGatewayPeeringAttachment": "Network", "AWS::EC2::TransitGatewayConnect": "Network",
	"AWS::EC2::VPNConnection": "Network", "AWS::EC2::ClientVpnTargetNetworkAssociation": "Network",
	"AWS::EC2::VerifiedAccessEndpoint": "Network", "AWS::EC2::Volume": "Storage",
	"AWS::ApiGateway::Stage": "Network", "AWS::AppSync::ApiCache": "Database", "AWS::SSM::Parameter": "Security",
	"AWS::CodePipeline::Pipeline": "Developer tools", "AWS::Transfer::Server": "Storage",
}

var serviceCategory = map[string]string{
	"EC2": "Compute", "AutoScaling": "Compute", "Lambda": "Compute", "ECS": "Compute", "EKS": "Compute",
	"AppRunner": "Compute", "Batch": "Compute", "GameLift": "Compute", "EMR": "Compute",
	"RDS": "Database", "DocDB": "Database", "DocDBElastic": "Database", "Neptune": "Database", "NeptuneGraph": "Database",
	"DynamoDB": "Database", "ElastiCache": "Database", "MemoryDB": "Database", "DAX": "Database", "Redshift": "Database",
	"Cassandra": "Database", "Timestream": "Database", "DMS": "Database",
	"EFS": "Storage", "FSx": "Storage",
	"ElasticLoadBalancing": "Network", "ElasticLoadBalancingV2": "Network", "GlobalAccelerator": "Network",
	"Route53": "Network", "Route53Resolver": "Network", "Route53RecoveryControl": "Network",
	"Route53RecoveryReadiness": "Network", "NetworkFirewall": "Network", "NetworkManager": "Network", "VpcLattice": "Network",
	"Kinesis": "Analytics & streaming", "KinesisAnalyticsV2": "Analytics & streaming", "MSK": "Analytics & streaming",
	"KafkaConnect": "Analytics & streaming", "OpenSearchService": "Analytics & streaming", "Elasticsearch": "Analytics & streaming",
	"OpenSearchServerless": "Analytics & streaming", "OSIS": "Analytics & streaming", "MWAA": "Analytics & streaming",
	"AmazonMQ": "Analytics & streaming",
	"KMS":      "Security", "SecretsManager": "Security", "WAFv2": "Security", "ACMPCA": "Security",
	"DirectoryService": "Security", "Shield": "Security",
	"CloudWatch": "Monitoring", "Synthetics": "Monitoring",
	"SageMaker": "Machine learning", "Kendra": "Machine learning", "HealthLake": "Machine learning",
}

func category(t string) string {
	if c, ok := typeCategory[t]; ok {
		return c
	}
	parts := strings.Split(t, "::")
	if len(parts) < 2 {
		return t
	}
	if c, ok := serviceCategory[parts[1]]; ok {
		return c
	}
	return parts[1]
}

// htmlResource is one row of the table: a resource and its billed lines.
type htmlResource struct {
	Region    string     `json:"region"`
	Stack     string     `json:"stack"`
	Path      string     `json:"path"`
	LogicalID string     `json:"logicalId"`
	Type      string     `json:"type"`
	Category  string     `json:"category"`
	Monthly   float64    `json:"monthly"`
	Lines     []htmlLine `json:"lines"`
}

type htmlLine struct {
	Quantity string  `json:"quantity"`
	Monthly  float64 `json:"monthly"`
	Reason   string  `json:"reason"`
	// Suggestion and Saving are set with --suggest.
	Suggestion string  `json:"suggestion,omitempty"`
	Saving     float64 `json:"saving,omitempty"`
}

type htmlData struct {
	Total      float64         `json:"total"`
	Prices     string          `json:"prices"`
	Hours      int             `json:"hours"`
	Regions    []htmlRegion    `json:"regions"`
	Items      []*htmlResource `json:"items"`
	Unknown    []Item          `json:"unknown"`
	NotCounted []Item          `json:"notCounted"`
	NoRule     []Item          `json:"noRule"`
}

type htmlRegion struct {
	Region  string  `json:"region"`
	Stacks  int     `json:"stacks"`
	Monthly float64 `json:"monthly"`
}

// WriteHTML writes a self-contained page: totals, a bar per category, and a
// table to sort and filter. It needs no network.
func (rep Report) WriteHTML(w io.Writer) error {
	d := htmlData{Total: rep.Total(), Prices: rep.Prices, Hours: hoursPerMonth, Regions: []htmlRegion{},
		Items: []*htmlResource{}, Unknown: []Item{}, NotCounted: []Item{}, NoRule: []Item{}}
	for _, r := range rep.Regions {
		d.Regions = append(d.Regions, htmlRegion{r.Region, r.Stacks, r.Monthly})
		for _, s := range r.Billed {
			var res *htmlResource
			for _, it := range s.Items { // a resource's lines are together
				if res == nil || res.LogicalID != it.LogicalID {
					res = &htmlResource{Region: it.Region, Stack: it.Stack, Path: it.Path, LogicalID: it.LogicalID,
						Type: it.Type, Category: category(it.Type)}
					d.Items = append(d.Items, res)
				}
				res.Monthly += it.Monthly
				res.Lines = append(res.Lines, htmlLine{it.Quantity, it.Monthly, it.Reason, it.Suggestion, it.Saving})
			}
		}
		d.Unknown = append(d.Unknown, r.Unknown...)
		d.NotCounted = append(d.NotCounted, r.Skipped...)
		d.NoRule = append(d.NoRule, r.NoRule...)
	}
	return htmlPage.Execute(w, d)
}

var htmlPage = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Idle cost report</title>
<style>
:root {
  color-scheme: light;
  --bg: #f6f6f4; --surface: #fcfcfb; --border: #e4e3df; --track: #eeede9;
  --text: #0b0b0b; --text-2: #52514e; --muted: #7a7975;
  --bar: #2a78d6; --accent: #2a78d6;
  --warn-bg: #fdf3e1; --warn: #8a5a00; --err-bg: #fbe9e8; --err: #a32b2a; --info-bg: #ecf3fc; --info: #1d5aa3;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    color-scheme: dark;
    --bg: #121211; --surface: #1a1a19; --border: #2e2e2c; --track: #262624;
    --text: #f0efec; --text-2: #c3c2b7; --muted: #8f8e86;
    --bar: #3987e5; --accent: #6aa6ee;
    --warn-bg: #33270f; --warn: #f0c060; --err-bg: #3a1c1b; --err: #f29a99; --info-bg: #17263a; --info: #8fbdf3;
  }
}
:root[data-theme="dark"] {
  color-scheme: dark;
  --bg: #121211; --surface: #1a1a19; --border: #2e2e2c; --track: #262624;
  --text: #f0efec; --text-2: #c3c2b7; --muted: #8f8e86;
  --bar: #3987e5; --accent: #6aa6ee;
  --warn-bg: #33270f; --warn: #f0c060; --err-bg: #3a1c1b; --err: #f29a99; --info-bg: #17263a; --info: #8fbdf3;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font: 14px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 1120px; margin: 0 auto; padding: 32px 16px 48px; }
h1 { font-size: 28px; margin: 0; font-variant-numeric: tabular-nums; }
h2 { font-size: 16px; margin: 0 0 12px; }
.sub { color: var(--text-2); margin: 4px 0 0; }
.tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin: 24px 0; }
.tile, section { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; }
.tile { padding: 14px 16px; }
.tile .label { color: var(--text-2); font-size: 12px; }
.tile .value { font-size: 22px; font-weight: 600; font-variant-numeric: tabular-nums; }
section { padding: 20px; margin-bottom: 16px; }
.bars { display: grid; gap: 6px; }
.bar-row { display: grid; grid-template-columns: minmax(90px, 30%) 1fr auto; align-items: center; gap: 12px;
  padding: 4px 6px; border-radius: 6px; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
.bar-row:hover, .bar-row[aria-pressed="true"] { background: var(--track); }
.bar-row:focus-visible, th button:focus-visible, input:focus-visible, select:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.bar-track { height: 14px; }
.bar { height: 14px; min-width: 2px; background: var(--bar); border-radius: 0 4px 4px 0; }
.bar-value { text-align: right; font-variant-numeric: tabular-nums; }
.bar-name .n { color: var(--muted); font-size: 12px; margin-left: 4px; }
.controls { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
.controls input, .controls select { font: inherit; color: inherit; background: var(--bg); border: 1px solid var(--border); border-radius: 6px; padding: 6px 10px; }
.controls input { flex: 1 1 220px; }
.table-wrap { overflow-x: auto; }
table { width: 100%; min-width: 860px; border-collapse: collapse; }
th, td { padding: 7px 8px; border-bottom: 1px solid var(--border); text-align: left; vertical-align: top; }
th { font-size: 12px; color: var(--text-2); font-weight: 600; white-space: nowrap; }
th button { all: unset; cursor: pointer; }
th button::after { content: " ↕"; color: var(--muted); }
th[aria-sort="ascending"] button::after { content: " ↑"; color: var(--text); }
th[aria-sort="descending"] button::after { content: " ↓"; color: var(--text); }
td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
td.path { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; word-break: break-all; }
td.detail { color: var(--text-2); min-width: 340px; }
.lines { list-style: none; margin: 0; padding: 0; }
.lines .q { color: var(--text); font-variant-numeric: tabular-nums; }
.lines .m { color: var(--muted); font-variant-numeric: tabular-nums; }
tr.group td { background: var(--track); font-weight: 600; }
tr.group td.num { font-weight: 600; }
.notes h3 { font-size: 14px; margin: 16px 0 6px; display: flex; gap: 8px; align-items: center; }
.notes h3:first-child { margin-top: 0; }
.badge { font-size: 12px; font-weight: 600; padding: 1px 8px; border-radius: 999px; }
.badge.err { background: var(--err-bg); color: var(--err); }
.badge.warn { background: var(--warn-bg); color: var(--warn); }
.badge.info { background: var(--info-bg); color: var(--info); }
.notes p { margin: 0 0 6px; color: var(--text-2); }
.notes ul { margin: 0; padding-left: 18px; }
.notes code { font-size: 13px; }
.empty { color: var(--muted); }
footer { color: var(--muted); font-size: 12px; margin-top: 24px; }
</style>
</head>
<body>
<main>
<header>
  <h1 id="total"></h1>
  <p class="sub" id="regions"></p>
</header>
<div class="tiles" id="tiles"></div>
<section aria-labelledby="by-cat">
  <h2 id="by-cat">By category</h2>
  <div class="bars" id="bars"></div>
</section>
<section aria-labelledby="by-res">
  <h2 id="by-res">Resources</h2>
  <div class="controls">
    <input id="q" type="search" placeholder="Filter by resource, type, stack or detail" aria-label="Filter">
    <select id="cat" aria-label="Category"><option value="">All categories</option></select>
  </div>
  <div class="table-wrap"><table>
    <thead><tr id="head"></tr></thead>
    <tbody id="rows"></tbody>
  </table></div>
</section>
<section class="notes" id="notes" aria-label="Not in the total"></section>
<footer id="foot"></footer>
</main>
<script>
const D = {{.}};
const $ = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"]/g, c => ({"&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;"})[c]);
const money = v => v >= 10 ? "$" + Math.round(v).toLocaleString("en-US") : "$" + v.toFixed(2);
const multiRegion = D.regions.length > 1;

$("total").textContent = "Idle cost ≈ " + money(D.total) + "/mo";
$("regions").textContent = D.regions.map(r => r.region + " · " + r.stacks + (r.stacks === 1 ? " stack" : " stacks") + (multiRegion ? " · " + money(r.monthly) + "/mo" : "")).join("   |   ");
$("foot").textContent = "cfn-idle-cost · prices: " + D.prices + " · on-demand list prices, " + D.hours + " h/mo, USD, free tier ignored";

const cats = {};
for (const it of D.items) {
  const c = cats[it.category] ??= {name: it.category, monthly: 0, resources: new Set()};
  c.monthly += it.monthly;
  c.resources.add(it);
}
const catList = Object.values(cats).sort((a, b) => b.monthly - a.monthly || a.name.localeCompare(b.name));
const catRank = Object.fromEntries(catList.map((c, i) => [c.name, i]));
const resources = D.items.length;
const attention = D.unknown.length + D.notCounted.length + D.noRule.length;

$("tiles").innerHTML = [
  ["Idle cost per month", money(D.total)],
  ["Per year", money(D.total * 12)],
  ["Resources that cost money idle", resources],
  ["Not in the total", attention],
].map(([l, v]) => '<div class="tile"><div class="label">' + esc(l) + '</div><div class="value">' + esc(v) + "</div></div>").join("");

let filterCat = "";
const maxCat = catList.length ? catList[0].monthly : 0;
function renderBars() {
  $("bars").innerHTML = catList.length ? catList.map(c => {
    const pct = D.total ? (100 * c.monthly / D.total).toFixed(1) : "0";
    return '<button class="bar-row" type="button" data-cat="' + esc(c.name) + '" aria-pressed="' + (filterCat === c.name) + '"' +
      ' title="' + esc(c.name + ": " + money(c.monthly) + "/mo, " + pct + "% of the total, " + c.resources.size + " resources") + '">' +
      '<span class="bar-name">' + esc(c.name) + '<span class="n">' + c.resources.size + '</span></span>' +
      '<span class="bar-track"><span class="bar" style="display:block;width:' + (maxCat ? 100 * c.monthly / maxCat : 0) + '%"></span></span>' +
      '<span class="bar-value">' + money(c.monthly) + "</span></button>";
  }).join("") : '<p class="empty">Nothing costs money with zero traffic.</p>';
}
$("bars").addEventListener("click", e => {
  const b = e.target.closest(".bar-row");
  if (!b) return;
  filterCat = filterCat === b.dataset.cat ? "" : b.dataset.cat;
  $("cat").value = filterCat;
  renderBars(); renderRows();
});
for (const c of catList) $("cat").insertAdjacentHTML("beforeend", '<option value="' + esc(c.name) + '">' + esc(c.name) + "</option>");
$("cat").addEventListener("change", e => { filterCat = e.target.value; renderBars(); renderRows(); });
$("q").addEventListener("input", renderRows);

const cols = [
  {key: "category", label: "Category"},
  ...(multiRegion ? [{key: "region", label: "Region"}] : []),
  {key: "stack", label: "Stack"},
  {key: "path", label: "Resource"},
  {key: "type", label: "Type"},
  {key: "monthly", label: "$/mo", num: true},
  {key: "lines", label: "Charges"},
];
let sortKey = "category", sortDir = 1;
function renderHead() {
  $("head").innerHTML = cols.map(c => '<th' + (c.num ? ' class="num"' : "") + ' aria-sort="' +
    (sortKey === c.key ? (sortDir > 0 ? "ascending" : "descending") : "none") + '"><button type="button" data-key="' + c.key + '">' + esc(c.label) + "</button></th>").join("");
}
$("head").addEventListener("click", e => {
  const b = e.target.closest("button");
  if (!b) return;
  const k = b.dataset.key;
  if (sortKey === k) sortDir = -sortDir;
  else { sortKey = k; sortDir = k === "monthly" ? -1 : 1; }
  renderHead(); renderRows();
});
function compare(a, b) {
  let d;
  if (sortKey === "category") d = catRank[a.category] - catRank[b.category] || b.monthly - a.monthly;
  else if (sortKey === "monthly") d = a.monthly - b.monthly;
  else if (sortKey === "lines") d = a.lines.length - b.lines.length;
  else d = String(a[sortKey]).localeCompare(String(b[sortKey]));
  return sortDir * d || b.monthly - a.monthly;
}
function renderRows() {
  const q = $("q").value.trim().toLowerCase();
  const rows = D.items.filter(it => (!filterCat || it.category === filterCat) &&
    (!q || [it.stack, it.path, it.type, it.category, it.region, ...it.lines.map(l => l.reason)].join(" ").toLowerCase().includes(q))).sort(compare);
  const cell = (it, c) => {
    if (c.key === "lines") return '<td class="detail"><ul class="lines">' + it.lines.map(l =>
      '<li><span class="q">' + esc(l.quantity) + "</span> " + esc(l.reason) + (it.lines.length > 1 ? ' <span class="m">' + money(l.monthly) + "</span>" : "") +
      (l.suggestion ? '<div class="m">→ -' + money(l.saving) + "/mo: " + esc(l.suggestion) + "</div>" : "") + "</li>").join("") + "</ul></td>";
    const v = c.key === "monthly" ? money(it.monthly) : it[c.key];
    const cls = c.num ? "num" : c.key === "path" ? "path" : "";
    return "<td" + (cls ? ' class="' + cls + '"' : "") + ">" + esc(v) + "</td>";
  };
  let html = "", last = null;
  for (const it of rows) {
    if (sortKey === "category" && it.category !== last) {
      last = it.category;
      const sum = rows.filter(r => r.category === last).reduce((s, r) => s + r.monthly, 0);
      html += '<tr class="group"><td colspan="' + (cols.length - 2) + '">' + esc(last) + '</td><td class="num">' + money(sum) + "</td><td></td></tr>";
    }
    html += "<tr>" + cols.map(c => cell(it, c)).join("") + "</tr>";
  }
  $("rows").innerHTML = html || '<tr><td class="empty" colspan="' + cols.length + '">No resources match.</td></tr>';
}

const groups = [
  ["err", "Could not estimate", "Their idle cost is not in the total: a value the template doesn't say, or a case not supported yet.", D.unknown, it => it.reason],
  ["warn", "Not counted yet", "These types may cost money idle, but cfn-idle-cost has no rule for them.", D.notCounted, it => it.type],
  ["info", "No rule, assumed $0 idle", "cfn-idle-cost doesn't know whether these types cost money with zero traffic.", D.noRule, it => it.type],
].filter(g => g[3].length);
$("notes").innerHTML = groups.map(([cls, title, about, items, what]) =>
  '<h3>' + esc(title) + ' <span class="badge ' + cls + '">' + items.length + "</span></h3><p>" + esc(about) + "</p><ul>" +
  items.map(it => "<li>" + esc(it.stack) + " <code>" + esc(it.path) + "</code>: " + esc(what(it)) + "</li>").join("") + "</ul>").join("");
if (!groups.length) $("notes").remove();

renderBars(); renderHead(); renderRows();
</script>
</body>
</html>
`))
