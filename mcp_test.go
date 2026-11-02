package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The server answers initialize and tools/list, maps a tool call to flags, and
// stays silent on notifications.
func TestMCP(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"idle_cost","arguments":{"inputs":["cdk.out"],"region":"us-east-1","suggest":true,"discount":["10"]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`,
	}, "\n")
	var got []string
	var out bytes.Buffer
	err := runMCP(strings.NewReader(in), &out, func(args []string) (string, bool) {
		got = args
		return `{"total":1}`, false
	})
	if err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		resps = append(resps, r)
	}
	if len(resps) != 5 {
		t.Fatalf("want 5 responses (none for the notification), got %d:\n%s", len(resps), out.String())
	}
	if v := resps[0]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("protocolVersion %v", v)
	}
	if !strings.Contains(out.String(), `"name":"idle_cost"`) {
		t.Errorf("tools/list: %s", out.String())
	}
	if strings.Join(got, " ") != "--format json --region us-east-1 --suggest --discount 10 -- cdk.out" {
		t.Errorf("args %q", got)
	}
	if !strings.Contains(out.String(), `{\"total\":1}`) {
		t.Errorf("tool result missing: %s", out.String())
	}
	if resps[3]["error"] == nil || resps[4]["error"] == nil {
		t.Errorf("unknown tool and method should be errors: %v %v", resps[3], resps[4])
	}
}

// The real binary path: the flags a tool call builds are ones run accepts.
func TestMCPArgsRun(t *testing.T) {
	args, _ := mcpTools[0].args(map[string]any{"inputs": []any{"testdata/suggest.yaml"}, "region": "eu-north-1", "suggest": true, "hours": 220.0, "discount": []any{"EC2=5"}})
	var out bytes.Buffer
	defer func() { clear(discounts); clear(discountNames); hoursPerMonth = 730 }()
	if err := run(args, &out); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	if !strings.Contains(out.String(), `"suggestion"`) {
		t.Errorf("no suggestions in:\n%s", out.String())
	}
}
