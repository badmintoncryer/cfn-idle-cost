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
			r = &regionReport{Region: s.Region, Unknown: []Item{}}
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
		plural := "s"
		if r.Stacks == 1 {
			plural = ""
		}
		fmt.Fprintf(w, "%s · %d stack%s · idle ≈ %s/mo\n", r.Region, r.Stacks, plural, money(r.Monthly))
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
	}
	fmt.Fprintf(w, "\nprices: %s · on-demand list prices, %d h/mo, USD, free tier ignored\n", rep.Prices, hoursPerMonth)
}

func pad(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-len([]rune(s))))
}

func (rep Report) WriteJSON(w io.Writer) error {
	cents := func(v *float64) { *v = math.Round(*v*100) / 100 }
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
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
