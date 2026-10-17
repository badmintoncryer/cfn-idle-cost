package main

import (
	"fmt"
	"io"
	"math"
	"sort"
)

// Change is one resource whose idle cost differs between base and head.
type Change struct {
	Kind          string  `json:"kind"` // added, removed, changed
	Region        string  `json:"region"`
	Stack         string  `json:"stack"`
	Path          string  `json:"path"`
	LogicalID     string  `json:"logicalId"`
	Type          string  `json:"type"`
	Before        float64 `json:"before"`
	After         float64 `json:"after"`
	BeforeReason  string  `json:"beforeReason,omitempty"`
	AfterReason   string  `json:"afterReason,omitempty"`
	BeforeUnknown bool    `json:"beforeUnknown,omitempty"` // base could not estimate it
	AfterUnknown  bool    `json:"afterUnknown,omitempty"`
}

type regionDiff struct {
	Region  string   `json:"region"`
	Before  float64  `json:"before"`
	After   float64  `json:"after"`
	Unknown int      `json:"unknownChanges"` // changes with "?" on either side
	Changes []Change `json:"changes"`
}

// Diff is how the idle cost changes from a base cdk.out to the inputs. Before
// and After leave out every resource that is "?" on either side, so one that
// becomes unresolvable doesn't read as a saving.
type Diff struct {
	Before  float64 `json:"before"`
	After   float64 `json:"after"`
	Unknown int     `json:"unknownChanges"`
	// NewUnknown counts resources added as, or changed to, "?": their increase
	// can't be checked, so --max-increase fails on them.
	NewUnknown int          `json:"newUnknown"`
	Regions    []regionDiff `json:"regions"`
	Prices     string       `json:"prices"`
}

// resourceState is a resource's idle cost in one report; reason is its most
// expensive line.
type resourceState struct {
	item    Item
	monthly float64
	reason  string
	top     float64
	unknown bool
}

func resourceStates(rep Report) (map[[3]string]*resourceState, map[string]float64) {
	states, totals := map[[3]string]*resourceState{}, map[string]float64{}
	add := func(it Item, unknown bool) {
		k := [3]string{it.Region, it.Stack, it.LogicalID}
		s := states[k]
		if s == nil {
			s = &resourceState{item: it, top: -1}
			states[k] = s
		}
		s.monthly += it.Monthly
		s.unknown = s.unknown || unknown
		if unknown || it.Monthly > s.top {
			s.reason, s.top = it.Reason, it.Monthly
		}
	}
	for _, r := range rep.Regions {
		totals[r.Region] = r.Monthly
		for _, s := range r.Billed {
			for _, it := range s.Items {
				add(it, false)
			}
		}
		for _, it := range append(append([]Item{}, r.Unknown...), r.Skipped...) {
			add(it, true)
		}
	}
	return states, totals
}

func NewDiff(base, head Report) *Diff {
	bs, bt := resourceStates(base)
	hs, ht := resourceStates(head)
	d := &Diff{Prices: head.Prices}
	byRegion := map[string]*regionDiff{}
	region := func(r string) *regionDiff {
		if byRegion[r] == nil {
			byRegion[r] = &regionDiff{Region: r, Before: bt[r], After: ht[r], Changes: []Change{}}
		}
		return byRegion[r]
	}
	for r := range bt {
		region(r)
	}
	for r := range ht {
		region(r)
	}
	keys := map[[3]string]bool{}
	for k := range bs {
		keys[k] = true
	}
	for k := range hs {
		keys[k] = true
	}
	for k := range keys {
		b, h := bs[k], hs[k]
		c := Change{Region: k[0], Stack: k[1], LogicalID: k[2]}
		switch {
		case b == nil:
			if h.monthly == 0 && !h.unknown {
				continue
			}
			c.Kind = "added"
		case h == nil:
			if b.monthly == 0 && !b.unknown {
				continue
			}
			c.Kind = "removed"
		case b.unknown && h.unknown && b.reason == h.reason,
			!b.unknown && !h.unknown && math.Abs(b.monthly-h.monthly) < 0.005:
			continue
		default:
			c.Kind = "changed"
		}
		rd := region(k[0])
		if b != nil && h != nil && b.unknown != h.unknown { // keep it out of both totals
			if h.unknown {
				rd.Before -= b.monthly
			} else {
				rd.After -= h.monthly
			}
		}
		if (b != nil && b.unknown) || (h != nil && h.unknown) {
			rd.Unknown++
			if h != nil && h.unknown && (b == nil || !b.unknown) {
				d.NewUnknown++
			}
		}
		src := h
		if h == nil {
			src = b
		}
		c.Path, c.Type = src.item.Path, src.item.Type
		if b != nil {
			c.Before, c.BeforeReason, c.BeforeUnknown = b.monthly, b.reason, b.unknown
		}
		if h != nil {
			c.After, c.AfterReason, c.AfterUnknown = h.monthly, h.reason, h.unknown
		}
		rd.Changes = append(rd.Changes, c)
	}
	for _, r := range sortedIDs(byRegion) {
		rd := byRegion[r]
		sort.Slice(rd.Changes, func(i, j int) bool {
			a, b := rd.Changes[i], rd.Changes[j]
			if da, db := math.Abs(a.After-a.Before), math.Abs(b.After-b.Before); da != db {
				return da > db
			}
			return a.Stack+a.Path < b.Stack+b.Path
		})
		d.Before += rd.Before
		d.After += rd.After
		d.Unknown += rd.Unknown
		d.Regions = append(d.Regions, *rd)
	}
	return d
}

func signed(v float64) string {
	if v < 0 {
		return "-" + money(-v)
	}
	return "+" + money(v)
}

func (c Change) amount(before bool) string {
	if (before && c.BeforeUnknown) || (!before && c.AfterUnknown) {
		return "?"
	}
	if before {
		return money(c.Before)
	}
	return money(c.After)
}

func (c Change) delta() string {
	if c.BeforeUnknown || c.AfterUnknown {
		return "?"
	}
	return signed(c.After - c.Before)
}

func (c Change) note() string {
	switch {
	case c.Kind == "removed":
		return c.BeforeReason
	case c.Kind == "changed" && c.BeforeReason != c.AfterReason:
		return c.BeforeReason + " → " + c.AfterReason
	}
	return c.AfterReason
}

var changeMark = map[string]string{"added": "+", "removed": "-", "changed": "~"}

func (d *Diff) summary(r string, before, after float64, unknown int) string {
	return fmt.Sprintf("%s · idle ≈ %s → %s/mo (%s)%s", r, money(before), money(after), signed(after-before), unknownNote(unknown))
}

// unknownNote flags a total that leaves out changes it could not price.
func unknownNote(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return " · 1 change could not be estimated"
	}
	return fmt.Sprintf(" · %d changes could not be estimated", n)
}

func (d *Diff) WriteText(w io.Writer) {
	if len(d.Regions) > 1 {
		fmt.Fprintln(w, d.summary("total", d.Before, d.After, d.Unknown))
		fmt.Fprintln(w)
	}
	printed := 0
	for _, r := range d.Regions {
		if len(r.Changes) == 0 && len(d.Regions) > 1 {
			continue // the total line already covers it
		}
		if printed++; printed > 1 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, d.summary(r.Region, r.Before, r.After, r.Unknown))
		if len(r.Changes) == 0 {
			fmt.Fprintln(w, "\n  no change")
			continue
		}
		sw, pw, bw, aw, dw := 0, 0, 0, 0, 0
		for _, c := range r.Changes {
			sw, pw = max(sw, len(c.Stack)), max(pw, len([]rune(c.Path)))
			bw, aw, dw = max(bw, len(c.amount(true))), max(aw, len(c.amount(false))), max(dw, len(c.delta()))
		}
		fmt.Fprintln(w)
		for _, c := range r.Changes {
			fmt.Fprintf(w, "  %s %s  %s  %*s → %*s  %*s  %s\n", changeMark[c.Kind], pad(c.Stack, sw), pad(c.Path, pw), bw, c.amount(true), aw, c.amount(false), dw, c.delta(), c.note())
		}
	}
	fmt.Fprintf(w, "\nprices: %s · on-demand list prices, %d h/mo, USD, free tier ignored\n", d.Prices, hoursPerMonth)
}

func (d *Diff) WriteMarkdown(w io.Writer) {
	fmt.Fprintf(w, "### Idle cost %s → %s/mo (%s)%s\n\n", money(d.Before), money(d.After), signed(d.After-d.Before), unknownNote(d.Unknown))
	var all []Change
	for _, r := range d.Regions {
		all = append(all, r.Changes...)
	}
	if len(all) == 0 {
		fmt.Fprint(w, "No change in idle cost.\n\n")
	} else {
		multi := len(d.Regions) > 1
		head, rule := "| | Stack | Resource | Before | After | Change | |", "|---|---|---|---:|---:|---:|---|"
		if multi {
			head, rule = "| | Region | Stack | Resource | Before | After | Change | |", "|---|---|---|---|---:|---:|---:|---|"
		}
		fmt.Fprintln(w, head+"\n"+rule)
		for _, c := range all {
			reg := ""
			if multi {
				reg = " " + c.Region + " |"
			}
			fmt.Fprintf(w, "| %s |%s %s | `%s` | %s | %s | %s | %s |\n", c.Kind, reg, mdCell(c.Stack), mdCell(c.Path), c.amount(true), c.amount(false), c.delta(), mdCell(c.note()))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "<sub>cfn-idle-cost · prices: %s · on-demand list prices, %d h/mo, USD, free tier ignored</sub>\n", d.Prices, hoursPerMonth)
}

func (d *Diff) WriteJSON(w io.Writer) error {
	cents(&d.Before)
	cents(&d.After)
	for i := range d.Regions {
		r := &d.Regions[i]
		cents(&r.Before)
		cents(&r.After)
		for j := range r.Changes {
			cents(&r.Changes[j].Before)
			cents(&r.Changes[j].After)
		}
	}
	return writeJSON(w, d)
}
