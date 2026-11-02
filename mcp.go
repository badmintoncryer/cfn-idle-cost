package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// `cfn-idle-cost mcp` is a Model Context Protocol server on stdin/stdout, so an
// AI agent writing CDK can price what it synthesized without parsing the CLI's
// text. Each tool call runs this same binary with the matching flags and
// returns what it printed.

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	args        func(in map[string]any) ([]string, error)
}

var mcpTools = []mcpTool{{
	Name: "idle_cost",
	Description: "Estimate the monthly idle cost (what it costs with zero traffic: NAT Gateways, load balancers, instances, " +
		"databases, provisioned capacity, storage) of a synthesized AWS CDK app or CloudFormation templates, offline, " +
		"from bundled AWS list prices. Returns JSON with one item per billed line (stack, construct path, quantity, " +
		"monthly USD, reason) and resources it could not estimate. Run `cdk synth` first. With base, returns the change " +
		"from the base cdk.out instead. With suggest, items carry a cheaper setting and its monthly saving.",
	InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"inputs":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "cdk.out directories or template files (default ./cdk.out)"},
		"base":       map[string]any{"type": "string", "description": "cdk.out or template to compare against, e.g. one synthesized from the main branch"},
		"region":     map[string]any{"type": "string", "description": "region for stacks without one (env-agnostic stacks, plain templates)"},
		"suggest":    map[string]any{"type": "boolean", "description": "add cheaper settings and what each saves (not with base)"},
		"hours":      map[string]any{"type": "number", "description": "hours a month hour-based charges run (default 730)"},
		"parameters": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "template parameter values, [Stack:]Key=Value"},
		"discount":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "discounts: pct for every price, or Service=pct, e.g. 10 or RDS=20"},
	}},
	args: func(in map[string]any) ([]string, error) {
		a := []string{"--format", "json"}
		if s, ok := in["base"].(string); ok && s != "" {
			a = append(a, "--base", s)
		}
		if s, ok := in["region"].(string); ok && s != "" {
			a = append(a, "--region", s)
		}
		if b, ok := in["suggest"].(bool); ok && b {
			a = append(a, "--suggest")
		}
		if h, ok := in["hours"].(float64); ok {
			a = append(a, "--hours", fmt.Sprint(h))
		}
		for _, p := range mcpStrings(in["parameters"]) {
			a = append(a, "--parameter", p)
		}
		for _, d := range mcpStrings(in["discount"]) {
			a = append(a, "--discount", d)
		}
		return append(append(a, "--"), mcpStrings(in["inputs"])...), nil
	},
}}

func mcpStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// runMCP serves MCP requests, one JSON-RPC message per line, until in closes.
// call runs one tool invocation (the binary itself, in production).
func runMCP(in io.Reader, out io.Writer, call func(args []string) (string, bool)) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
			continue
		}
		if len(req.ID) == 0 { // a notification (notifications/initialized, cancelled): no reply
			continue
		}
		result, rpcErr := mcpHandle(req, call)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func mcpHandle(req mcpRequest, call func(args []string) (string, bool)) (any, map[string]any) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := p.ProtocolVersion
		if v == "" {
			v = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "cfn-idle-cost", "version": version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, map[string]any{"code": -32602, "message": "invalid params"}
		}
		for _, t := range mcpTools {
			if t.Name != p.Name {
				continue
			}
			args, err := t.args(p.Arguments)
			if err != nil {
				return mcpText(err.Error(), true), nil
			}
			text, failed := call(args)
			return mcpText(text, failed), nil
		}
		return nil, map[string]any{"code": -32602, "message": "unknown tool " + p.Name}
	}
	return nil, map[string]any{"code": -32601, "message": "method not found: " + req.Method}
}

func mcpText(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

// mcpExec runs this binary with args. Exit code 3 (over a budget) is a result, not an error.
func mcpExec(args []string) (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return err.Error(), true
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	text := stdout.String()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		text += "\n" + s
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
		return text, false
	}
	return text, err != nil
}
