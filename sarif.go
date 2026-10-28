package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The SARIF rules: one per kind of finding.
var sarifRules = []struct{ id, text, help string }{
	{"idle-cost", "Resource that costs money while idle", "Its idle cost is in the total. Delete it, size it down, or check that it is needed in this environment."},
	{"could-not-estimate", "Resource whose idle cost could not be estimated", "A value the template doesn't say (a Parameter, a reference to another stack) or a case cfn-idle-cost does not support yet. It is not in the total."},
	{"not-counted", "Resource that may cost money idle and has no rule yet", "cfn-idle-cost has no rule for this type yet. It is not in the total."},
}

// WriteSARIF writes the resources whose idle cost is at least minMonthly per
// month, and those that could not be estimated or are not counted, as SARIF
// 2.1.0 for GitHub code scanning. Locations are the templates, relative to
// the working directory, at the line of the logical ID.
func (rep Report) WriteSARIF(w io.Writer, minMonthly float64) error {
	type result = map[string]any
	lines := lineFinder{}
	results := []result{}
	add := func(rule, level, msg string, it Item, monthly float64) {
		loc := map[string]any{"artifactLocation": map[string]any{"uri": sarifURI(it.File)}}
		if n := lines.find(it.File, it.LogicalID); n > 0 {
			loc["region"] = map[string]any{"startLine": n}
		}
		r := result{
			"ruleId":    rule,
			"level":     level,
			"message":   map[string]any{"text": msg},
			"locations": []any{map[string]any{"physicalLocation": loc, "logicalLocations": []any{map[string]any{"name": it.LogicalID, "fullyQualifiedName": it.Stack + "/" + it.Path, "kind": "resource"}}}},
			// Same resource, same fingerprint across runs, so code scanning tracks it.
			"partialFingerprints": map[string]any{"resource/v1": it.Region + "/" + it.Stack + "/" + it.LogicalID},
		}
		if rule == "idle-cost" {
			r["properties"] = map[string]any{"monthlyUSD": monthly}
		}
		results = append(results, r)
	}
	for _, reg := range rep.Regions {
		for _, s := range reg.Billed {
			// One result per resource: its lines added up, the reasons listed.
			var order []string
			byID := map[string][]Item{}
			for _, it := range s.Items {
				if byID[it.LogicalID] == nil {
					order = append(order, it.LogicalID)
				}
				byID[it.LogicalID] = append(byID[it.LogicalID], it)
			}
			for _, id := range order {
				its := byID[id]
				total := 0.0
				var why []string
				for _, it := range its {
					total += it.Monthly
					why = append(why, fmt.Sprintf("%s %s (%s)", money(it.Monthly), it.Reason, it.Quantity))
				}
				if total < minMonthly || total < 0.005 {
					continue
				}
				cents(&total)
				add("idle-cost", "warning", fmt.Sprintf("%s (%s) idles at ≈ %s/mo in %s: %s", its[0].Path, its[0].Type, money(total), reg.Region, strings.Join(why, "; ")), its[0], total)
			}
		}
		for _, it := range reg.Unknown {
			add("could-not-estimate", "note", fmt.Sprintf("%s (%s): idle cost could not be estimated: %s", it.Path, it.Type, it.Reason), it, 0)
		}
		for _, it := range reg.Skipped {
			add("not-counted", "note", fmt.Sprintf("%s (%s) may cost money idle; cfn-idle-cost has no rule for it yet", it.Path, it.Type), it, 0)
		}
	}
	var rules []any
	for _, r := range sarifRules {
		rules = append(rules, map[string]any{
			"id": r.id, "shortDescription": map[string]any{"text": r.text}, "help": map[string]any{"text": r.help},
		})
	}
	return writeJSON(w, map[string]any{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name": "cfn-idle-cost", "version": version, "informationUri": "https://github.com/badmintoncryer/cfn-idle-cost", "rules": rules,
			}},
			"results":    results,
			"properties": map[string]any{"prices": rep.Prices, "hoursPerMonth": hoursPerMonth},
		}},
	})
}

// sarifURI is a template path as a relative, slash-separated URI.
func sarifURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		if wd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(wd, abs); err == nil && !strings.HasPrefix(rel, "..") {
				path = rel
			}
		}
	}
	return filepath.ToSlash(path)
}

// lineFinder finds the line of a logical ID in a template: the first line
// that has it as a key ("Id": in JSON, Id: in YAML).
type lineFinder map[string][]string

func (f lineFinder) find(file, id string) int {
	lines, ok := f[file]
	if !ok {
		if fh, err := os.Open(file); err == nil {
			sc := bufio.NewScanner(fh)
			sc.Buffer(nil, 16<<20)
			for sc.Scan() {
				lines = append(lines, sc.Text())
			}
			fh.Close()
		}
		f[file] = lines
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, `"`+id+`":`) || strings.HasPrefix(t, id+":") {
			return i + 1
		}
	}
	return 0
}
