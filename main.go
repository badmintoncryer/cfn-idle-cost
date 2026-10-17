// Command cfn-idle-cost prints what an AWS CDK app or CloudFormation template
// costs per month with zero traffic.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

var version = "dev"

const usage = `cfn-idle-cost — monthly idle cost of a CDK app or CloudFormation template

Usage:
  cfn-idle-cost [flags] [cdk.out | template ...]
  cfn-idle-cost --base <cdk.out | template> [flags] [cdk.out | template ...]

Inputs default to ./cdk.out (run ` + "`cdk synth`" + ` first). Templates may be JSON or YAML.
With --base, prints how the idle cost changes from the base to the inputs.

Flags:
  --base path          compare against this cdk.out (or template), e.g. one synthesized from main
  --format f           text (default), md (for a pull request comment) or json
  --max usd            exit 3 if the total idle cost is over usd per month
  --max-increase usd   with --base: exit 3 if the idle cost grows by more than usd per month
  --region r           region for stacks without one (env-agnostic stacks, plain templates)
  --parameter [Stack:]Key=Value
                       value of a template parameter, for every stack or only Stack (repeatable);
                       parameters without one use their Default
  --live               fetch current prices from the AWS Price List Query API
                       (needs credentials with pricing:GetProducts) instead of the bundled snapshot
  --version            print the version

Exit codes: 0 ok, 1 error, 2 usage error, 3 over --max or --max-increase.
`

// usageError makes main exit with 2, overBudget with 3.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

type overBudget struct{ msg string }

func (e overBudget) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:], os.Stdout)
	var ue usageError
	var ob overBudget
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	case errors.As(err, &ob):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	case err != nil:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "prices" { // maintainer: regenerate prices.json
		return runPrices(args[1:])
	}
	fs := flag.NewFlagSet("cfn-idle-cost", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	region := fs.String("region", "", "")
	base := fs.String("base", "", "")
	format := fs.String("format", "text", "")
	maxTotal := fs.Float64("max", -1, "")
	maxIncrease := fs.Float64("max-increase", -1, "")
	live := fs.Bool("live", false, "")
	var params paramFlag
	fs.Var(&params, "parameter", "")
	showVersion := fs.Bool("version", false, "")
	// Allow flags after the inputs too (npx cfn-idle-cost cdk.out --format md).
	var flags, inputs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			inputs = append(inputs, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if f := fs.Lookup(name); f != nil && !strings.Contains(a, "=") && i+1 < len(args) {
				if _, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool {
					i++
					flags = append(flags, args[i])
				}
			}
		default:
			inputs = append(inputs, a)
		}
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return nil
		}
		return usageError{err.Error() + " (see --help)"}
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return nil
	}
	if !in(*format, "text", "md", "json") {
		return usageError{"--format must be text, md or json"}
	}
	if *maxIncrease >= 0 && *base == "" {
		return usageError{"--max-increase needs --base"}
	}
	head, err := loadStacks(inputs, *region)
	if err != nil {
		return err
	}
	if unused := params.apply(head); len(unused) > 0 {
		return usageError{"no stack has parameter " + strings.Join(unused, ", ")}
	}
	var baseStacks []*Stack
	if *base != "" {
		if baseStacks, err = loadStacks([]string{*base}, *region); err != nil {
			return err
		}
		params.apply(baseStacks) // the base may not have a parameter added since
	}
	var prices *Prices
	if *live {
		prices, err = LivePrices(context.Background(), append(append([]*Stack{}, head...), baseStacks...))
	} else {
		prices, err = BundledPrices()
	}
	if err != nil {
		return err
	}
	headItems := Estimate(head, prices)
	rep := NewReport(head, headItems, prices)
	var d *Diff
	if *base != "" {
		d = NewDiff(NewReport(baseStacks, Estimate(baseStacks, prices), prices), rep)
	}
	switch {
	case d != nil && *format == "json":
		err = d.WriteJSON(stdout)
	case d != nil && *format == "md":
		d.WriteMarkdown(stdout)
	case d != nil:
		d.WriteText(stdout)
	case *format == "json":
		err = rep.WriteJSON(stdout)
	case *format == "md":
		rep.WriteMarkdown(stdout)
	default:
		rep.WriteText(stdout)
	}
	if err != nil {
		return err
	}
	return checkBudget(rep, d, *maxTotal, *maxIncrease)
}

// checkBudget returns overBudget when a --max or --max-increase gate fails.
func checkBudget(rep Report, d *Diff, maxTotal, maxIncrease float64) error {
	if maxTotal < 0 && maxIncrease < 0 {
		return nil
	}
	if n := rep.unknownCount(); n > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d resources could not be estimated or are not counted; the real idle cost may be higher\n", n)
	}
	if total := rep.Total(); maxTotal >= 0 && total > maxTotal {
		return overBudget{fmt.Sprintf("over budget: idle cost %s/mo is over --max %s", money(total), money(maxTotal))}
	}
	if d != nil && maxIncrease >= 0 && d.After-d.Before > maxIncrease {
		return overBudget{fmt.Sprintf("over budget: idle cost grows by %s/mo, over --max-increase %s", money(d.After-d.Before), money(maxIncrease))}
	}
	return nil
}

// paramFlag collects --parameter [Stack:]Key=Value.
type paramFlag []struct{ stack, key, value string }

func (p *paramFlag) String() string { return "" }

func (p *paramFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return fmt.Errorf("--parameter %q: want [Stack:]Key=Value", s)
	}
	st, key := "", k
	if i := strings.LastIndex(k, ":"); i >= 0 {
		st, key = k[:i], k[i+1:]
	}
	*p = append(*p, struct{ stack, key, value string }{st, key, v})
	return nil
}

// apply sets the given parameters and returns the ones no stack has.
func (p paramFlag) apply(stacks []*Stack) (unused []string) {
	for _, g := range p {
		used := false
		for _, s := range stacks {
			if par, ok := s.Parameters[g.key]; ok && (g.stack == "" || g.stack == s.Name) {
				par.Value, par.Given = g.value, true
				s.Parameters[g.key] = par
				used = true
			}
		}
		if !used {
			unused = append(unused, strings.TrimPrefix(g.stack+":"+g.key, ":"))
		}
	}
	return unused
}

// loadStacks loads inputs and fills the region of stacks that have none.
func loadStacks(inputs []string, region string) ([]*Stack, error) {
	stacks, err := LoadInputs(inputs)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, s := range stacks {
		if s.Region == "" {
			if region == "" {
				missing = append(missing, s.Name)
			}
			s.Region = region
		}
	}
	if len(missing) > 0 {
		return nil, usageError{fmt.Sprintf("no region for %s; pass --region", strings.Join(missing, ", "))}
	}
	return stacks, nil
}

func runPrices(args []string) error {
	fs := flag.NewFlagSet("prices", flag.ContinueOnError)
	out := fs.String("o", "prices.json", "output file")
	regions := fs.String("regions", "", "comma-separated regions (default: all commercial regions)")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	var rs []string
	if *regions != "" {
		rs = strings.Split(*regions, ",")
	}
	p, err := BuildPrices(rs, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) })
	if err != nil {
		return err
	}
	return WritePrices(*out, p)
}
