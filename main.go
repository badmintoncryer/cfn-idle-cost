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

Inputs default to ./cdk.out (run ` + "`cdk synth`" + ` first). Templates may be JSON or YAML.

Flags:
  --region r   region for stacks without one (env-agnostic stacks, plain templates)
  --live       fetch current prices from the AWS Price List Query API
               (needs credentials with pricing:GetProducts) instead of the bundled snapshot
  --json       print JSON
  --version    print the version
`

// usageError makes main exit with 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func main() {
	err := run(os.Args[1:])
	var ue usageError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "prices" { // maintainer: regenerate prices.json
		return runPrices(args[1:])
	}
	fs := flag.NewFlagSet("cfn-idle-cost", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	region := fs.String("region", "", "")
	live := fs.Bool("live", false, "")
	asJSON := fs.Bool("json", false, "")
	showVersion := fs.Bool("version", false, "")
	// Allow flags after the inputs too (npx cfn-idle-cost cdk.out --json).
	var flags, inputs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			inputs = append(inputs, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			flags = append(flags, a)
			if (a == "--region" || a == "-region") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			inputs = append(inputs, a)
		}
	}
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(usage)
			return nil
		}
		return usageError{err.Error() + " (see --help)"}
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	stacks, err := LoadInputs(inputs)
	if err != nil {
		return err
	}
	var missing []string
	for _, s := range stacks {
		if s.Region == "" {
			if *region == "" {
				missing = append(missing, s.Name)
			}
			s.Region = *region
		}
	}
	if len(missing) > 0 {
		return usageError{fmt.Sprintf("no region for %s; pass --region", strings.Join(missing, ", "))}
	}
	var prices *Prices
	if *live {
		prices, err = LivePrices(context.Background(), stacks)
	} else {
		prices, err = BundledPrices()
	}
	if err != nil {
		return err
	}
	rep := NewReport(stacks, Estimate(stacks, prices), prices)
	if *asJSON {
		return rep.WriteJSON(os.Stdout)
	}
	rep.WriteText(os.Stdout)
	return nil
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
