package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

type stackReport struct {
	Name    string  `json:"name"`
	Monthly float64 `json:"monthly"`
	Items   []Item  `json:"items"`
}

type regionReport struct {
	Region  string  `json:"region"`
	Stacks  int     `json:"stackCount"`
	Monthly float64 `json:"monthly"`
	// Assumed is the part of Monthly from lines that rest on an assumption.
	Assumed float64 `json:"assumedMonthly"`
	// Elsewhere is the part of Monthly billed in other regions (replicas).
	Elsewhere map[string]float64 `json:"billedInOtherRegions,omitempty"`
	Billed    []stackReport      `json:"stacks"`
	Unknown   []Item             `json:"couldNotEstimate"`
	Skipped   []Item             `json:"notCounted"`
	NoRule    []Item             `json:"noRule"`
}

// stageReport is the idle cost of one CDK Stage, over all its regions.
type stageReport struct {
	Stage   string  `json:"stage"` // "" for the stacks directly in the app
	Monthly float64 `json:"monthly"`
}

type Report struct {
	Regions []regionReport `json:"regions"`
	Stages  []stageReport  `json:"stages,omitempty"` // only when some stack is in a Stage
	Prices  string         `json:"prices"`
}

// stageName labels the stacks outside any Stage.
func stageName(s string) string {
	if s == "" {
		return "(app)"
	}
	return s
}

// NewReport groups items by region and stack, most expensive first.
func NewReport(stacks []*Stack, items []Item, prices *Prices) Report {
	byRegion := map[string]*regionReport{}
	seen := map[string]bool{}
	stageOf := map[string]string{} // region, stack -> Stage
	staged := false
	for _, s := range stacks {
		stageOf[s.Region+"\x00"+s.Name] = s.Stage()
		staged = staged || s.Stage() != ""
		r := byRegion[s.Region]
		if r == nil {
			r = &regionReport{Region: s.Region, Unknown: []Item{}, Skipped: []Item{}, NoRule: []Item{}}
			byRegion[s.Region] = r
		}
		if !seen[s.Region+"\x00"+s.Name] { // a nested stack belongs to its parent
			seen[s.Region+"\x00"+s.Name] = true
			r.Stacks++
		}
	}
	for _, it := range items {
		r := byRegion[it.Region]
		if it.Unknown {
			r.Unknown = append(r.Unknown, it)
			continue
		}
		if it.NotCounted {
			r.Skipped = append(r.Skipped, it)
			continue
		}
		if it.NoRule {
			r.NoRule = append(r.NoRule, it)
			continue
		}
		r.Monthly += it.Monthly
		if it.Assumption != nil {
			r.Assumed += it.Monthly
		}
		if it.BilledIn != "" {
			if r.Elsewhere == nil {
				r.Elsewhere = map[string]float64{}
			}
			r.Elsewhere[it.BilledIn] += it.Monthly
		}
		i := 0
		for i < len(r.Billed) && r.Billed[i].Name != it.Stack {
			i++
		}
		if i == len(r.Billed) {
			r.Billed = append(r.Billed, stackReport{Name: it.Stack})
		}
		r.Billed[i].Monthly += it.Monthly
		r.Billed[i].Items = append(r.Billed[i].Items, it)
	}
	rep := Report{Prices: prices.Source}
	if staged {
		byStage := map[string]float64{}
		for _, st := range stageOf {
			byStage[st] += 0 // every Stage, even one at $0
		}
		for _, it := range items {
			if !it.Unknown && !it.NotCounted && !it.NoRule {
				byStage[stageOf[it.Region+"\x00"+it.Stack]] += it.Monthly
			}
		}
		for _, st := range sortedIDs(byStage) {
			rep.Stages = append(rep.Stages, stageReport{st, byStage[st]})
		}
	}
	for _, region := range sortedIDs(byRegion) {
		r := byRegion[region]
		sort.SliceStable(r.Billed, func(i, j int) bool { return r.Billed[i].Monthly > r.Billed[j].Monthly })
		for _, s := range r.Billed { // most expensive resource first, its lines together
			res := map[string]float64{}
			for _, it := range s.Items {
				res[it.LogicalID] += it.Monthly
			}
			sort.SliceStable(s.Items, func(i, j int) bool {
				a, b := s.Items[i], s.Items[j]
				if res[a.LogicalID] != res[b.LogicalID] {
					return res[a.LogicalID] > res[b.LogicalID]
				}
				if a.LogicalID != b.LogicalID {
					return a.LogicalID < b.LogicalID
				}
				return a.Monthly > b.Monthly
			})
		}
		if r.Billed == nil {
			r.Billed = []stackReport{}
		}
		rep.Regions = append(rep.Regions, *r)
	}
	return rep
}

// money: whole dollars from $10, cents below.
func money(v float64) string {
	if v >= 10 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func (rep Report) WriteText(w io.Writer) {
	for i, r := range rep.Regions {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s · %s · idle ≈ %s/mo%s\n", r.Region, count(float64(r.Stacks), "stack"), money(r.Monthly), r.assumedNote())
		pw, qw, mw := 0, 0, 0
		for _, s := range r.Billed {
			for _, it := range s.Items {
				pw = max(pw, len([]rune(it.Path)))
				qw = max(qw, len([]rune(it.Quantity)))
				mw = max(mw, len(money(it.Monthly)))
			}
		}
		for _, s := range r.Billed {
			tot := money(s.Monthly) // right-aligned with the item amounts
			fmt.Fprintf(w, "\n%s%s/mo\n", pad(s.Name+" ", 6+pw+qw+mw-len(tot)), tot)
			for _, it := range s.Items {
				fmt.Fprintf(w, "  %s  %s  %*s  %s\n", pad(it.Path, pw), pad(it.Quantity, qw), mw, money(it.Monthly), approx(it)+it.Reason)
				if it.Later != nil {
					fmt.Fprintf(w, "  %s  ↗ %s/mo after %s: %s\n", pad("", pw+qw+mw+2), money(it.Later.Monthly), it.Later.After, it.Later.Why)
				}
				if it.Suggestion != "" {
					fmt.Fprintf(w, "  %s  → -%s/mo: %s\n", pad("", pw+qw+mw+2), money(it.Saving), it.Suggestion)
				}
			}
		}
		if n, saving := r.suggestions(); n > 0 {
			fmt.Fprintf(w, "\n→ %s save %s/mo\n", count(float64(n), "suggestion"), money(saving))
		}
		if as := r.assumptions(); len(as) > 0 {
			fmt.Fprintf(w, "\n≈ assumptions: %d (in the total; set them to firm it up)\n", len(as))
			for _, it := range as {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.paths(), it.text())
			}
		}
		if len(r.Unknown) > 0 {
			fmt.Fprintf(w, "\n? could not estimate: %d\n", len(r.Unknown))
			for _, it := range r.Unknown {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.Path, it.Reason)
			}
		}
		if len(r.Skipped) > 0 {
			fmt.Fprintf(w, "\n! not counted yet: %d (these may cost money idle; cfn-idle-cost has no rule for them)\n", len(r.Skipped))
			for _, it := range r.Skipped {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.Path, it.Type)
			}
		}
		if len(r.NoRule) > 0 {
			fmt.Fprintf(w, "\n? no rule: %d (assumed $0 idle; cfn-idle-cost doesn't know whether these cost money)\n", len(r.NoRule))
			for _, it := range r.NoRule {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.Path, it.Type)
			}
		}
	}
	if len(rep.Stages) > 0 {
		nw, mw := 0, 0
		for _, st := range rep.Stages {
			nw, mw = max(nw, len([]rune(stageName(st.Stage)))), max(mw, len(money(st.Monthly)))
		}
		fmt.Fprintf(w, "\nby Stage\n")
		for _, st := range rep.Stages {
			fmt.Fprintf(w, "  %s  %*s/mo\n", pad(stageName(st.Stage), nw), mw, money(st.Monthly))
		}
	}
	fmt.Fprintf(w, "\nprices: %s · %s, %g h/mo, USD, free tier ignored\n", rep.Prices, priceBasis(), hoursPerMonth)
}

// laterNote: " (↗ $3.00/mo after 2 years, automatic key rotation)".
func laterNote(it Item) string {
	if it.Later == nil {
		return ""
	}
	return fmt.Sprintf(" (↗ %s/mo after %s, %s)", money(it.Later.Monthly), it.Later.After, it.Later.Why)
}

// approx marks a line that rests on an assumption.
func approx(it Item) string {
	if it.Assumption != nil {
		return "≈ "
	}
	return ""
}

// assumedNote is the header's "· $70 of it on assumptions", and the parts
// billed in other regions.
func (r regionReport) assumedNote() string {
	n := ""
	if r.Assumed >= 0.005 {
		n = " · " + money(r.Assumed) + " of it on assumptions"
	}
	for _, reg := range sortedIDs(r.Elsewhere) {
		n += " · " + money(r.Elsewhere[reg]) + " billed in " + reg
	}
	return n
}

// assumptionGroup is one assumption and the resources of a stack that rest on it.
type assumptionGroup struct {
	Stack string
	Paths []string
	First Item // for the location in SARIF
	A     Assumption
}

// assumptions groups the region's assumed lines by stack and assumption, so an
// AMI shared by ten instances is one entry with one way to set it.
func (r regionReport) assumptions() []assumptionGroup {
	var out []assumptionGroup
	at := map[string]int{}
	for _, s := range r.Billed {
		for _, it := range s.Items {
			a := it.Assumption
			if a == nil {
				continue
			}
			k := it.Stack + "\x00" + a.What + "\x00" + a.Override
			i, ok := at[k]
			if !ok {
				i = len(out)
				at[k] = i
				out = append(out, assumptionGroup{Stack: it.Stack, First: it, A: Assumption{What: a.What, Override: a.Override, IfWrongCase: a.IfWrongCase}})
			}
			g := &out[i]
			if !in(it.Path, g.Paths...) {
				g.Paths = append(g.Paths, it.Path)
			}
			g.A.IfWrong += a.IfWrong
		}
	}
	return out
}

// text: what was assumed, what it would cost if wrong, how to set it.
func (g assumptionGroup) text() string {
	a := g.A
	t := a.What + "."
	if a.IfWrong >= 0.005 {
		t += " +" + money(a.IfWrong) + "/mo if " + a.IfWrongCase + "."
	}
	if a.Override != "" {
		t += " Set it: " + a.Override
	}
	return t
}

// paths: "Web", or "Web, Worker, Batch" for a shared assumption.
func (g assumptionGroup) paths() string { return strings.Join(g.Paths, ", ") }

// count: "1 task", "2 tasks".
func count(n float64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%g %ss", n, noun)
}

func pad(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-len([]rune(s))))
}

func (rep Report) WriteJSON(w io.Writer) error {
	for i := range rep.Regions {
		r := &rep.Regions[i]
		cents(&r.Monthly)
		cents(&r.Assumed)
		for k, v := range r.Elsewhere {
			cents(&v)
			r.Elsewhere[k] = v
		}
		for j := range r.Billed {
			s := &r.Billed[j]
			cents(&s.Monthly)
			for k := range s.Items {
				cents(&s.Items[k].Monthly)
				cents(&s.Items[k].Saving)
				if l := s.Items[k].Later; l != nil {
					cents(&l.Monthly)
				}
			}
		}
	}
	for i := range rep.Stages {
		cents(&rep.Stages[i].Monthly)
	}
	return writeJSON(w, rep)
}

func cents(v *float64) { *v = math.Round(*v*100) / 100 }

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Total is the idle cost of all regions.
func (rep Report) Total() float64 {
	t := 0.0
	for _, r := range rep.Regions {
		t += r.Monthly
	}
	return t
}

func (rep Report) unknownCount() int {
	n := 0
	for _, r := range rep.Regions {
		n += len(r.Unknown) + len(r.Skipped) + len(r.NoRule)
	}
	return n
}

// mdCell escapes a table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func (rep Report) WriteMarkdown(w io.Writer) {
	fmt.Fprintf(w, "### Idle cost ≈ %s/mo\n\n", money(rep.Total()))
	if a := rep.assumed(); a >= 0.005 {
		fmt.Fprintf(w, "%s of it rests on assumptions (marked ≈, listed below).\n\n", money(a))
	}
	fmt.Fprintln(w, "| Region | Stacks | Idle cost |\n|---|---:|---:|")
	for _, r := range rep.Regions {
		fmt.Fprintf(w, "| %s | %d | %s/mo |\n", r.Region, r.Stacks, money(r.Monthly))
	}
	if len(rep.Stages) > 0 {
		fmt.Fprintln(w, "\n| Stage | Idle cost |\n|---|---:|")
		for _, st := range rep.Stages {
			fmt.Fprintf(w, "| %s | %s/mo |\n", mdCell(stageName(st.Stage)), money(st.Monthly))
		}
	}
	fmt.Fprint(w, "\n<details><summary>Breakdown by resource</summary>\n\n")
	for _, r := range rep.Regions {
		if len(rep.Regions) > 1 {
			fmt.Fprintf(w, "#### %s\n\n", r.Region)
		}
		fmt.Fprintln(w, "| Stack | Resource | Quantity | $/mo | |\n|---|---|---:|---:|---|")
		for _, s := range r.Billed {
			for _, it := range s.Items {
				fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s |\n", mdCell(s.Name), mdCell(it.Path), it.Quantity, money(it.Monthly), mdCell(approx(it)+it.Reason+laterNote(it)))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, "</details>\n\n")
	writeMarkdownSuggestions(w, rep)
	writeMarkdownAssumptions(w, rep)
	writeMarkdownUnknown(w, rep)
	fmt.Fprintf(w, "<sub>cfn-idle-cost · prices: %s · %s, %g h/mo, USD, free tier ignored</sub>\n", rep.Prices, priceBasis(), hoursPerMonth)
}

// suggestions counts the region's suggestions and what they save a month.
func (r regionReport) suggestions() (n int, saving float64) {
	for _, s := range r.Billed {
		for _, it := range s.Items {
			if it.Suggestion != "" {
				n++
				saving += it.Saving
			}
		}
	}
	return n, saving
}

func writeMarkdownSuggestions(w io.Writer, rep Report) {
	n, saving := 0, 0.0
	for _, r := range rep.Regions {
		rn, rs := r.suggestions()
		n, saving = n+rn, saving+rs
	}
	if n == 0 {
		return
	}
	fmt.Fprintf(w, "**Suggestions (%d, -%s/mo)**\n\n", n, money(saving))
	for _, r := range rep.Regions {
		for _, s := range r.Billed {
			for _, it := range s.Items {
				if it.Suggestion != "" {
					fmt.Fprintf(w, "- %s `%s`: -%s/mo, %s (now %s)\n", mdCell(s.Name), mdCell(it.Path), money(it.Saving), it.Suggestion, it.Reason)
				}
			}
		}
	}
	fmt.Fprintln(w)
}

func (rep Report) assumed() float64 {
	a := 0.0
	for _, r := range rep.Regions {
		a += r.Assumed
	}
	return a
}

func writeMarkdownAssumptions(w io.Writer, rep Report) {
	var as []assumptionGroup
	for _, r := range rep.Regions {
		as = append(as, r.assumptions()...)
	}
	if len(as) == 0 {
		return
	}
	fmt.Fprintf(w, "<details><summary>≈ Assumptions (%d, in the total)</summary>\n\n", len(as))
	for _, it := range as {
		fmt.Fprintf(w, "- %s `%s`: %s\n", it.Stack, strings.Join(it.Paths, "`, `"), it.text())
	}
	fmt.Fprint(w, "\n</details>\n\n")
}

func writeMarkdownUnknown(w io.Writer, rep Report) {
	var unknown, skipped, noRule []Item
	for _, r := range rep.Regions {
		unknown = append(unknown, r.Unknown...)
		skipped = append(skipped, r.Skipped...)
		noRule = append(noRule, r.NoRule...)
	}
	for _, g := range []struct {
		title string
		items []Item
		what  func(Item) string
	}{
		{"Could not estimate", unknown, func(it Item) string { return it.Reason }},
		{"Not counted yet", skipped, func(it Item) string { return it.Type }},
		{"No rule, assumed $0 idle", noRule, func(it Item) string { return it.Type }},
	} {
		if len(g.items) == 0 {
			continue
		}
		fmt.Fprintf(w, "**%s (%d)**\n\n", g.title, len(g.items))
		for _, it := range g.items {
			fmt.Fprintf(w, "- %s `%s`: %s\n", it.Stack, it.Path, g.what(it))
		}
		fmt.Fprintln(w)
	}
}

// assumedItems returns one line per resource of the group, for SARIF, which
// places each result at its resource.
func (r regionReport) assumedItems(g assumptionGroup) []Item {
	var out []Item
	seen := map[string]bool{}
	for _, s := range r.Billed {
		for _, it := range s.Items {
			if a := it.Assumption; a != nil && it.Stack == g.Stack && a.What == g.A.What && a.Override == g.A.Override && !seen[it.LogicalID] {
				seen[it.LogicalID] = true
				out = append(out, it)
			}
		}
	}
	return out
}
