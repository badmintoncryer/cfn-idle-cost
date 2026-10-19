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
	Region  string        `json:"region"`
	Stacks  int           `json:"stackCount"`
	Monthly float64       `json:"monthly"`
	Billed  []stackReport `json:"stacks"`
	Unknown []Item        `json:"couldNotEstimate"`
	Skipped []Item        `json:"notCounted"`
}

type Report struct {
	Regions []regionReport `json:"regions"`
	Prices  string         `json:"prices"`
}

// NewReport groups items by region and stack, most expensive first.
func NewReport(stacks []*Stack, items []Item, prices *Prices) Report {
	byRegion := map[string]*regionReport{}
	seen := map[string]bool{}
	for _, s := range stacks {
		r := byRegion[s.Region]
		if r == nil {
			r = &regionReport{Region: s.Region, Unknown: []Item{}, Skipped: []Item{}}
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
		r.Monthly += it.Monthly
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
		fmt.Fprintf(w, "%s · %s · idle ≈ %s/mo\n", r.Region, count(float64(r.Stacks), "stack"), money(r.Monthly))
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
				fmt.Fprintf(w, "  %s  %s  %*s  %s\n", pad(it.Path, pw), pad(it.Quantity, qw), mw, money(it.Monthly), it.Reason)
			}
		}
		if len(r.Unknown) > 0 {
			fmt.Fprintf(w, "\n? could not estimate: %d\n", len(r.Unknown))
			for _, it := range r.Unknown {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.Path, it.Reason)
			}
		}
		if len(r.Skipped) > 0 {
			fmt.Fprintf(w, "\n! not counted yet: %d (these cost money idle; cfn-idle-cost has no rule for them)\n", len(r.Skipped))
			for _, it := range r.Skipped {
				fmt.Fprintf(w, "  %s  %s  %s\n", it.Stack, it.Path, it.Type)
			}
		}
	}
	fmt.Fprintf(w, "\nprices: %s · on-demand list prices, %d h/mo, USD, free tier ignored\n", rep.Prices, hoursPerMonth)
}

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
		for j := range r.Billed {
			s := &r.Billed[j]
			cents(&s.Monthly)
			for k := range s.Items {
				cents(&s.Items[k].Monthly)
			}
		}
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
		n += len(r.Unknown) + len(r.Skipped)
	}
	return n
}

// mdCell escapes a table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func (rep Report) WriteMarkdown(w io.Writer) {
	fmt.Fprintf(w, "### Idle cost ≈ %s/mo\n\n", money(rep.Total()))
	fmt.Fprintln(w, "| Region | Stacks | Idle cost |\n|---|---:|---:|")
	for _, r := range rep.Regions {
		fmt.Fprintf(w, "| %s | %d | %s/mo |\n", r.Region, r.Stacks, money(r.Monthly))
	}
	fmt.Fprint(w, "\n<details><summary>Breakdown by resource</summary>\n\n")
	for _, r := range rep.Regions {
		if len(rep.Regions) > 1 {
			fmt.Fprintf(w, "#### %s\n\n", r.Region)
		}
		fmt.Fprintln(w, "| Stack | Resource | Quantity | $/mo | |\n|---|---|---:|---:|---|")
		for _, s := range r.Billed {
			for _, it := range s.Items {
				fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s |\n", mdCell(s.Name), mdCell(it.Path), it.Quantity, money(it.Monthly), mdCell(it.Reason))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, "</details>\n\n")
	writeMarkdownUnknown(w, rep)
	fmt.Fprintf(w, "<sub>cfn-idle-cost · prices: %s · on-demand list prices, %d h/mo, USD, free tier ignored</sub>\n", rep.Prices, hoursPerMonth)
}

func writeMarkdownUnknown(w io.Writer, rep Report) {
	var unknown, skipped []Item
	for _, r := range rep.Regions {
		unknown = append(unknown, r.Unknown...)
		skipped = append(skipped, r.Skipped...)
	}
	for _, g := range []struct {
		title string
		items []Item
		what  func(Item) string
	}{
		{"Could not estimate", unknown, func(it Item) string { return it.Reason }},
		{"Not counted yet", skipped, func(it Item) string { return it.Type }},
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
