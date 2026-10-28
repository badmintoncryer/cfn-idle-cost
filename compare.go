package main

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// RegionCompare is the idle cost of the same stacks priced in each region.
type RegionCompare struct {
	Regions   []string
	Totals    []float64
	Unknown   []int          // resources that could not be estimated, per region
	Resources []compareRow   // most different first
	byKey     map[string]int // stack, path -> index in Resources
	Prices    string
}

type compareRow struct {
	Stack, Path string
	Monthly     []float64 // per region; NaN where it could not be estimated
}

// CompareRegions estimates the stacks once per region, as if every stack
// were deployed there.
func CompareRegions(stacks []*Stack, regions []string, prices *Prices) RegionCompare {
	c := RegionCompare{Regions: regions, Totals: make([]float64, len(regions)), Unknown: make([]int, len(regions)),
		byKey: map[string]int{}, Prices: prices.Source}
	saved := make([]string, len(stacks))
	for i, s := range stacks {
		saved[i] = s.Region
	}
	defer func() {
		for i, s := range stacks {
			s.Region = saved[i]
		}
	}()
	for ri, region := range regions {
		for _, s := range stacks {
			s.Region = region
		}
		for _, it := range Estimate(stacks, prices) {
			if it.NotCounted || it.NoRule {
				continue
			}
			row := c.row(it.Stack, it.Path)
			if it.Unknown {
				c.Unknown[ri]++
				row.Monthly[ri] = math.NaN()
				continue
			}
			c.Totals[ri] += it.Monthly
			if !math.IsNaN(row.Monthly[ri]) {
				row.Monthly[ri] += it.Monthly
			}
		}
	}
	sort.SliceStable(c.Resources, func(i, j int) bool { return c.Resources[i].spread() > c.Resources[j].spread() })
	return c
}

func (c *RegionCompare) row(stack, path string) *compareRow {
	k := stack + "\x00" + path
	i, ok := c.byKey[k]
	if !ok {
		i = len(c.Resources)
		c.byKey[k] = i
		c.Resources = append(c.Resources, compareRow{stack, path, make([]float64, len(c.Regions))})
	}
	return &c.Resources[i]
}

// spread is the gap between the cheapest and the dearest region. A resource
// that can be estimated in some regions only sorts first; one that can be
// estimated in none has no spread (-1).
func (r compareRow) spread() float64 {
	lo, hi, unknown := math.Inf(1), math.Inf(-1), 0
	for _, v := range r.Monthly {
		if math.IsNaN(v) {
			unknown++
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	switch unknown {
	case 0:
		return hi - lo
	case len(r.Monthly):
		return -1
	}
	return math.Inf(1)
}

// compareRows is how many resources the comparison lists.
const compareRows = 10

// rows returns the resources worth listing: those whose price differs.
func (c RegionCompare) rows() []compareRow {
	var out []compareRow
	for _, r := range c.Resources {
		if r.spread() >= 0.005 && len(out) < compareRows {
			out = append(out, r)
		}
	}
	return out
}

func (c RegionCompare) cell(v float64) string {
	if math.IsNaN(v) {
		return "?"
	}
	return money(v)
}

// delta is a total against the first region's: "+$10 (+7%)".
func (c RegionCompare) delta(i int) string {
	if i == 0 {
		return ""
	}
	d := c.Totals[i] - c.Totals[0]
	if math.Abs(d) < 0.005 {
		return "same"
	}
	s := "+"
	if d < 0 {
		s = "-"
	}
	out := s + money(math.Abs(d))
	if c.Totals[0] > 0 {
		out += fmt.Sprintf(" (%s%.0f%%)", s, math.Abs(d)/c.Totals[0]*100)
	}
	return out
}

func (c RegionCompare) WriteText(w io.Writer) {
	fmt.Fprintln(w, "idle cost of the same stacks in each region")
	fmt.Fprintln(w)
	rw, mw := 0, 0
	for i, r := range c.Regions {
		rw, mw = max(rw, len(r)), max(mw, len(money(c.Totals[i])))
	}
	for i, r := range c.Regions {
		line := fmt.Sprintf("  %s  %*s/mo", pad(r, rw), mw, money(c.Totals[i]))
		if d := c.delta(i); d != "" {
			line += "  " + d
		}
		if c.Unknown[i] > 0 {
			line += fmt.Sprintf("  (%d could not estimate)", c.Unknown[i])
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	if rows := c.rows(); len(rows) > 0 {
		fmt.Fprintln(w, "\nlargest differences ($/mo; ? = could not estimate there)")
		cols := make([]int, len(c.Regions))
		sw, pw := 0, 0
		for i, r := range c.Regions {
			cols[i] = len(r)
		}
		for _, r := range rows {
			sw, pw = max(sw, len([]rune(r.Stack))), max(pw, len([]rune(r.Path)))
			for i, v := range r.Monthly {
				cols[i] = max(cols[i], len(c.cell(v)))
			}
		}
		head := "  " + pad("", sw+2+pw)
		for i, r := range c.Regions {
			head += fmt.Sprintf("  %*s", cols[i], r)
		}
		fmt.Fprintln(w, head)
		for _, r := range rows {
			line := "  " + pad(r.Stack, sw) + "  " + pad(r.Path, pw)
			for i, v := range r.Monthly {
				line += fmt.Sprintf("  %*s", cols[i], c.cell(v))
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintf(w, "\nprices: %s · on-demand list prices, %d h/mo, USD, free tier ignored\n", c.Prices, hoursPerMonth)
}

func (c RegionCompare) WriteMarkdown(w io.Writer) {
	fmt.Fprint(w, "### Idle cost by region\n\n| Region | Idle cost | vs "+c.Regions[0]+" | Could not estimate |\n|---|---:|---:|---:|\n")
	for i, r := range c.Regions {
		fmt.Fprintf(w, "| %s | %s/mo | %s | %d |\n", r, money(c.Totals[i]), c.delta(i), c.Unknown[i])
	}
	if rows := c.rows(); len(rows) > 0 {
		fmt.Fprint(w, "\n<details><summary>Largest differences</summary>\n\n| Stack | Resource |")
		for _, r := range c.Regions {
			fmt.Fprintf(w, " %s |", r)
		}
		fmt.Fprint(w, "\n|---|---|"+strings.Repeat("---:|", len(c.Regions))+"\n")
		for _, r := range rows {
			fmt.Fprintf(w, "| %s | `%s` |", mdCell(r.Stack), mdCell(r.Path))
			for _, v := range r.Monthly {
				fmt.Fprintf(w, " %s |", c.cell(v))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprint(w, "\n</details>\n")
	}
	fmt.Fprintf(w, "\n<sub>cfn-idle-cost · prices: %s · on-demand list prices, %d h/mo, USD, free tier ignored</sub>\n", c.Prices, hoursPerMonth)
}
