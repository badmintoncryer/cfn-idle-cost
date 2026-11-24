package main

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// handTable is data kept by hand in the code, not from the Price List: a list
// price or a calendar. Checked is when it was last compared with its source.
// Until, when set, is the last date it knows: an estimate for a later date
// may miss what was announced since.
type handTable struct {
	Name    string
	Source  string
	Checked string
	Until   func() string
}

// handTables lists every hand-kept table, for --version, the warning when the
// prices' date is past one, and `cfn-idle-cost prices -check`.
// ponytail: when you update one of these tables, set its Checked date.
var handTables = []handTable{
	{"EKS Kubernetes version support calendar", "https://docs.aws.amazon.com/eks/latest/userguide/kubernetes-versions.html", "2026-11-24", eksUntil},
	{"CloudWatch dashboard list price ($3)", "https://aws.amazon.com/cloudwatch/pricing/", "2026-11-24", nil},
	{"FSx File Cache list price ($1.33/GB-month, us-east-1)", "https://aws.amazon.com/fsx/file-cache/pricing/", "2026-11-24", nil},
	{"End User Messaging number lease list prices", "https://aws.amazon.com/end-user-messaging/pricing/", "2026-11-24", nil},
}

// eksUntil is the newest end of standard support in eksSupport: by then EKS
// has surely released versions the table does not have.
func eksUntil() string {
	last := ""
	for _, d := range eksSupport {
		last = max(last, d[0])
	}
	return last
}

// handTablesPast returns the tables whose last known date is before day
// (YYYY-MM-DD), for the warning under the report.
func handTablesPast(day string) []handTable {
	var out []handTable
	for _, t := range handTables {
		if t.Until != nil && day > t.Until() {
			out = append(out, t)
		}
	}
	return out
}

// writeVersion prints the version, the snapshot date and the hand tables'.
func writeVersion(w io.Writer, p *Prices) {
	fmt.Fprintln(w, version)
	fmt.Fprintf(w, "prices: %s\n", p.Source)
	for _, t := range handTables {
		until := ""
		if t.Until != nil {
			until = ", known until " + t.Until()
		}
		fmt.Fprintf(w, "%s: checked %s%s\n", t.Name, t.Checked, until)
	}
}

// checkHandTables reports, for maintainers, the tables that need a look on
// today: past or within 30 days of their last known date, or not checked for
// 180 days. It returns how many.
func checkHandTables(w io.Writer, today time.Time) int {
	soon := today.AddDate(0, 0, 30).Format("2006-01-02")
	stale := today.AddDate(0, 0, -180).Format("2006-01-02")
	var lines []string
	for _, t := range handTables {
		if t.Until != nil && t.Until() <= soon {
			lines = append(lines, fmt.Sprintf("- [ ] %s: known until %s. Add what was announced since: %s", t.Name, t.Until(), t.Source))
		}
		if t.Checked < stale {
			lines = append(lines, fmt.Sprintf("- [ ] %s: last checked %s. Compare with %s and set its Checked date", t.Name, t.Checked, t.Source))
		}
	}
	sort.Strings(lines)
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	return len(lines)
}
