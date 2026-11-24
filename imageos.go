package main

import (
	"fmt"
	"strings"
)

// osOverrides holds --os: an AMI ID, an SSM parameter path, a logical ID or a
// construct path, mapped to linux or windows.
var osOverrides = map[string]string{}

// osFlag collects --os Key=linux|windows.
type osFlag struct{}

func (osFlag) String() string { return "" }

func (osFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	v = strings.ToLower(v)
	if !ok || k == "" || !in(v, "linux", "windows") {
		return fmt.Errorf("--os %q: want Key=linux or Key=windows, where Key is an AMI ID, an SSM parameter path, a logical ID or a construct path", s)
	}
	osOverrides[k] = v
	return nil
}

// linuxImagePaths are the public SSM parameters of Linux AMIs.
var linuxImagePaths = []string{
	"/aws/service/ami-amazon-linux-latest/", "/aws/service/canonical/", "/aws/service/debian/",
	"/aws/service/bottlerocket/", "/aws/service/eks/optimized-ami/", "/aws/service/ecs/optimized-ami/amazon-linux",
	"/aws/service/deeplearning/", "/aws/service/freebsd/",
}

// imageOS reads the operating system of an instance from its ImageId: the
// resource's Metadata (cfn-idle-cost: {os: windows}), --os, or the public SSM
// parameter path CDK writes for MachineImage.latestWindows() and the like.
// When none says, it returns linux and the assumption the line rests on.
func (e *est) imageOS(img any) (os string, note string, a *Assumption) {
	if v, ok := asMap(e.r.Metadata["cfn-idle-cost"])["os"].(string); ok {
		if v = strings.ToLower(v); in(v, "linux", "windows") {
			return v, "", nil
		}
	}
	key, path := e.imageKey(img)
	for _, k := range []string{e.id, e.st.DisplayPath(e.id), key} {
		if v, ok := osOverrides[k]; ok && k != "" {
			return v, "", nil
		}
	}
	if path != "" {
		l := strings.ToLower(path)
		switch {
		case strings.Contains(l, "windows"):
			if strings.Contains(l, "sql_") {
				return "windows", "", &Assumption{
					What:     "SQL Server license not counted: the Windows price of " + path,
					Override: "none yet; add the SQL Server license-included rate by hand",
				}
			}
			return "windows", "", nil
		case strings.HasPrefix(l, "/aws/service/suse/"):
			return "linux", "", &Assumption{What: "SUSE subscription not counted: the Linux price of " + path}
		}
		for _, p := range linuxImagePaths {
			if strings.HasPrefix(l, p) {
				return "linux", "", nil
			}
		}
	}
	what := "ImageId doesn't say the OS"
	if key != "" {
		what = "ImageId " + key + " doesn't say the OS"
	}
	ov := key
	if ov == "" {
		ov = e.id
	}
	return "linux", "Linux assumed", &Assumption{
		What:     "Linux assumed: " + what,
		Override: "--os " + ov + "=windows (or Metadata cfn-idle-cost: {os: windows})",
	}
}

// imageKey names an ImageId for --os (an AMI ID or SSM parameter path, else
// "") and returns the SSM parameter path it is read from, if any.
func (e *est) imageKey(img any) (key, path string) {
	switch x := img.(type) {
	case string:
		if p, ok := strings.CutPrefix(x, "{{resolve:ssm:"); ok {
			p = strings.TrimSuffix(p, "}}")
			if i := strings.LastIndex(p, ":"); i > 0 && !strings.Contains(p[i:], "/") {
				p = p[:i] // a version or label
			}
			return p, p
		}
		return x, ""
	case map[string]any:
		ref, _ := x["Ref"].(string)
		p, ok := e.st.Parameters[ref]
		if !ok {
			return "", ""
		}
		if p.Given {
			if strings.HasPrefix(p.Value, "/") {
				return p.Value, p.Value
			}
			return p.Value, ""
		}
		d, _ := p.Default.(string)
		if strings.HasPrefix(p.Type, "AWS::SSM::Parameter::Value") {
			return d, d
		}
		return d, ""
	}
	return "", ""
}

// instances bills n instances of type t that run the OS of the image img,
// Windows with the license included. what follows the type in the reason
// (", DesiredCapacity"). It returns whether they run Linux, for the Graviton
// suggestion.
func (e *est) instances(n float64, t string, img any, what string) (linux bool) {
	os, note, a := e.imageOS(img)
	key, label := "ec2|", "Linux"
	if os == "windows" {
		key, label = "ec2win|", "Windows, license included"
	}
	if note != "" {
		label = note
	}
	if note != "" && n > 0 { // what it costs if the guess is wrong
		lin, ok1 := e.prices.Get(e.st.Region, "ec2|"+t)
		win, ok2 := e.prices.Get(e.st.Region, "ec2win|"+t)
		if ok1 && ok2 && win > lin {
			a.IfWrong = (win - lin) * n * hoursPerMonth * (1 - discountFor(e.r.Type))
			a.IfWrongCase = "Windows"
		}
	}
	e.assuming = a
	defer func() { e.assuming = nil }()
	e.hourly(n, "", key+t, fmt.Sprintf("%s on-demand (%s)%s", t, label, what))
	return os == "linux"
}
