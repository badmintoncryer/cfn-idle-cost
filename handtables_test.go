package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCheckHandTables(t *testing.T) {
	checked, _ := time.Parse("2006-01-02", handTables[0].Checked)
	var b bytes.Buffer
	if n := checkHandTables(&b, checked); n != 0 {
		t.Errorf("fresh tables: %d to look at:\n%s", n, b.String())
	}
	b.Reset()
	if n := checkHandTables(&b, checked.AddDate(0, 0, 200)); n != len(handTables) || !strings.Contains(b.String(), "last checked "+handTables[1].Checked) {
		t.Errorf("200 days on: %d to look at:\n%s", n, b.String())
	}
	until, _ := time.Parse("2006-01-02", eksUntil())
	b.Reset()
	checkHandTables(&b, until.AddDate(0, 0, -10))
	if !strings.Contains(b.String(), "EKS Kubernetes version support calendar: known until "+eksUntil()) {
		t.Errorf("10 days before the EKS table ends:\n%s", b.String())
	}
	if got := handTablesPast(eksUntil()); len(got) != 0 {
		t.Errorf("on its last day: %v", got)
	}
	if got := handTablesPast("9999-01-01"); len(got) != 1 || got[0].Name != handTables[0].Name {
		t.Errorf("after it: %v", got)
	}
}
