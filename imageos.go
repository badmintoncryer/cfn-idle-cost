package main

import (
	"fmt"
	"math"
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
				return "windows", "", &Assumption{What: "SQL Server license not counted: the Windows price of " + path}
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
		lin, ok1 := e.prices.Get(e.reg(), "ec2|"+t)
		win, ok2 := e.prices.Get(e.reg(), "ec2win|"+t)
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

// rootDevices are the root device names of Linux and Windows AMIs.
var rootDevices = []string{"/dev/xvda", "/dev/sda1"}

// rootVolume bills the root volumes of n instances when their size comes from
// the AMI: no mapping for the root device with a VolumeSize. The AMI's size is
// not in the template, so the line is marked as an assumption: 8 GB (the
// Amazon Linux default) or 30 GB for Windows, gp3 unless the mapping says.
func (e *est) rootVolume(bdm any, img any, n float64) {
	size, why := 8.0, "the Amazon Linux default"
	if os, _, _ := e.imageOS(img); os == "windows" {
		size, why = 30, "the Windows Server default"
	}
	e.rootVolumeOf(bdm, n, size, why)
}

// rootVolumeOf is rootVolume with the AMI's root volume size given.
func (e *est) rootVolumeOf(bdm any, n, size float64, why string) {
	if n <= 0 {
		return
	}
	t := ""
	if l, ok := bdm.([]any); ok {
		for _, d := range l {
			d := asMap(d)
			if !in(e.str(d, "DeviceName", ""), rootDevices...) {
				continue
			}
			ebs := asMap(d["Ebs"])
			if ebs == nil || ebs["VolumeSize"] != nil {
				return // counted with the other volumes, or instance store
			}
			t = e.str(ebs, "VolumeType", "")
		}
	}
	what := fmt.Sprintf("%g GB root volume per instance, %s", size, why)
	if t == "" {
		t = "gp3"
		what = fmt.Sprintf("%g GB gp3 root volume per instance, %s", size, why)
	}
	usage := map[string]string{"gp2": "gp2", "gp3": "gp3", "io1": "piops", "io2": "io2", "st1": "st1", "sc1": "sc1", "standard": ""}
	u, ok := usage[t]
	if !ok {
		panic(unresolved("EBS volume type " + t + " is not supported"))
	}
	key := "ebs|EBS:VolumeUsage"
	if u != "" {
		key += "." + u
	}
	e.assuming = &Assumption{What: what + " (the AMI's size is not in the template)",
		Override: "VolumeSize and VolumeType of the root device (" + rootDevices[0] + " or " + rootDevices[1] + ") in BlockDeviceMappings"}
	defer func() { e.assuming = nil }()
	e.monthly(size*n, "GB "+t, key, "root volume from the AMI")
}

// spot bills n Spot instances of type t running os (linux or windows) at the
// Spot Instance Advisor's average saving over on-demand in the region, marked
// as an assumption: Spot prices move, up to the on-demand price.
func (e *est) spot(n float64, t, os, what string) {
	if n <= 0 {
		return
	}
	key, label, adv := "ec2|", "Linux", "Linux"
	if os == "windows" {
		key, label, adv = "ec2win|", "Windows", "Windows"
	}
	od := e.price(key + t)
	s, ok := e.prices.Get(e.reg(), "spot|"+adv+":"+t)
	if !ok {
		panic(unresolved(fmt.Sprintf("the Spot Instance Advisor has no saving for %s %s in %s", label, t, e.reg())))
	}
	pct := math.Round(s * 100)
	d := 1 - discountFor(e.r.Type)
	e.assuming = &Assumption{
		What:        fmt.Sprintf("Spot at %g%% off on-demand, the Spot Instance Advisor's recent average for %s; Spot prices move", pct, t),
		IfWrong:     n * od * s * hoursPerMonth * d,
		IfWrongCase: "Spot rises to the on-demand price, the most it can cost",
	}
	defer func() { e.assuming = nil }()
	e.add(n, "", n*od*(1-s)*hoursPerMonth, fmt.Sprintf("%s Spot (%s, %g%% off on-demand)%s", t, label, pct, what))
}

// spotInstances is spot with the OS read from the image (see imageOS).
func (e *est) spotInstances(n float64, t string, img any, what string) {
	os, _, a := e.imageOS(img)
	if a != nil && n > 0 { // the OS guess is a second assumption on the same line
		e.assuming = a
		k := len(e.items)
		e.spot(n, t, os, what)
		if len(e.items) > k {
			sa := e.items[k].Assumption
			e.items[k].Assumption = &Assumption{What: a.What + "; " + sa.What, Override: a.Override, IfWrong: sa.IfWrong, IfWrongCase: sa.IfWrongCase}
			e.items[k].Reason = strings.Replace(e.items[k].Reason, "(Linux,", "(Linux assumed,", 1)
		}
		return
	}
	e.spot(n, t, os, what)
}
