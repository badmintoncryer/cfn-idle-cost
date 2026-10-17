package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stack is one CloudFormation template. A CDK nested stack is loaded as its own
// Stack that carries its parent's name and region.
type Stack struct {
	Name       string
	Region     string // from the cloud assembly env; empty when env-agnostic or a plain file
	Path       string
	Parameters map[string]Param
	Resources  map[string]Resource
	paths      map[string]string // logical ID -> construct path, from the cloud assembly
	cdkPath    string            // construct path of the stack (Stage/Stack), stripped from resource paths
}

type Param struct {
	Type    string
	Default any
}

type Resource struct {
	Type       string
	Condition  string
	Properties map[string]any
	Metadata   map[string]any
}

// ParseTemplate parses JSON or YAML. YAML short-form intrinsics (!Ref, !GetAtt …)
// are rewritten to their long form so they are never mistaken for literals.
func ParseTemplate(path string, raw []byte) (*Stack, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	doc, _ := toAny(&n).(map[string]any)
	if doc == nil {
		return nil, fmt.Errorf("%s: not a CloudFormation template", path)
	}
	s := &Stack{Path: path, Parameters: map[string]Param{}, Resources: map[string]Resource{}}
	for name, p := range asMap(doc["Parameters"]) {
		pm := asMap(p)
		t, _ := pm["Type"].(string)
		s.Parameters[name] = Param{Type: t, Default: pm["Default"]}
	}
	for id, r := range asMap(doc["Resources"]) {
		rm := asMap(r)
		t, _ := rm["Type"].(string)
		c, _ := rm["Condition"].(string)
		s.Resources[id] = Resource{Type: t, Condition: c, Properties: asMap(rm["Properties"]), Metadata: asMap(rm["Metadata"])}
	}
	return s, nil
}

func toAny(n *yaml.Node) any {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return toAny(n.Content[0])
	case yaml.AliasNode:
		return toAny(n.Alias)
	}
	var v any
	switch n.Kind {
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = toAny(n.Content[i+1])
		}
		v = m
	case yaml.SequenceNode:
		l := []any{}
		for _, c := range n.Content {
			l = append(l, toAny(c))
		}
		v = l
	default:
		if strings.HasPrefix(n.Tag, "!") && !strings.HasPrefix(n.Tag, "!!") {
			v = n.Value
		} else if err := n.Decode(&v); err != nil {
			v = n.Value
		}
	}
	if !strings.HasPrefix(n.Tag, "!") || strings.HasPrefix(n.Tag, "!!") {
		return v
	}
	name := n.Tag[1:]
	if name == "Ref" || name == "Condition" {
		return map[string]any{name: v}
	}
	if s, ok := v.(string); ok && name == "GetAtt" {
		a, b, _ := strings.Cut(s, ".")
		v = []any{a, b}
	}
	return map[string]any{"Fn::" + name: v}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// LoadInputs loads cdk.out directories (default ./cdk.out) and template files.
func LoadInputs(paths []string) ([]*Stack, error) {
	if len(paths) == 0 {
		paths = []string{"cdk.out"}
	}
	var out []*Stack
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			ss, err := loadAssembly(p)
			if err != nil {
				return nil, err
			}
			out = append(out, ss...)
			continue
		}
		s, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		s.Name = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		out = append(out, s)
	}
	return out, nil
}

func loadAssembly(dir string) ([]*Stack, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("%s is not a cloud assembly (run `cdk synth` first): %w", dir, err)
	}
	var m struct {
		Artifacts map[string]struct {
			Type         string                   `json:"type"`
			Environment  string                   `json:"environment"`
			Metadata     map[string][]cdkMetadata `json:"metadata"`
			MetadataFile string                   `json:"additionalMetadataFile"`
			DisplayName  string                   `json:"displayName"`
			Properties   struct {
				TemplateFile  string `json:"templateFile"`
				StackName     string `json:"stackName"`
				DirectoryName string `json:"directoryName"`
			} `json:"properties"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s/manifest.json: %w", dir, err)
	}
	ids := make([]string, 0, len(m.Artifacts))
	for id := range m.Artifacts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out, roots []*Stack
	var stackIDs []string
	for _, id := range ids {
		a := m.Artifacts[id]
		switch a.Type {
		case "aws:cloudformation:stack":
			s, err := loadFile(filepath.Join(dir, a.Properties.TemplateFile))
			if err != nil {
				return nil, err
			}
			s.Name = a.Properties.StackName
			if s.Name == "" {
				s.Name = id
			}
			s.cdkPath = a.DisplayName
			if r := a.Environment[strings.LastIndex(a.Environment, "/")+1:]; r != "unknown-region" {
				s.Region = r
			}
			// Templates carry aws:cdk:path only when the CDK CLI synthesizes; the
			// assembly metadata always maps construct paths to logical IDs.
			md := a.Metadata
			if a.MetadataFile != "" {
				if raw, err := os.ReadFile(filepath.Join(dir, a.MetadataFile)); err == nil {
					_ = json.Unmarshal(raw, &md)
				}
			}
			s.paths = map[string]string{}
			for path, entries := range md {
				for _, m := range entries {
					if id, ok := m.Data.(string); ok && m.Type == "aws:cdk:logicalId" {
						s.paths[id] = path
					}
				}
			}
			out = append(out, s)
			roots, stackIDs = append(roots, s), append(stackIDs, id)
		case "cdk:cloud-assembly": // Stage
			ss, err := loadAssembly(filepath.Join(dir, a.Properties.DirectoryName))
			if err != nil {
				return nil, err
			}
			out = append(out, ss...)
		}
	}
	// CDK writes nested stack templates as <id>.nested.template.json and lists
	// them, at any depth, in the root stack's <artifact>.assets.json.
	files, err := filepath.Glob(filepath.Join(dir, "*.nested.template.json"))
	if err != nil {
		return nil, err
	}
	nested := map[string]*Stack{}
	for _, p := range files {
		s, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		nested[filepath.Base(p)] = s
	}
	for i, id := range stackIDs {
		raw, err := os.ReadFile(filepath.Join(dir, id+".assets.json"))
		if err != nil {
			continue
		}
		var am struct {
			Files map[string]struct {
				Source struct {
					Path string `json:"path"`
				} `json:"source"`
			} `json:"files"`
		}
		if err := json.Unmarshal(raw, &am); err != nil {
			return nil, fmt.Errorf("%s.assets.json: %w", id, err)
		}
		for _, h := range sortedIDs(am.Files) {
			f := am.Files[h].Source.Path
			if s := nested[f]; s != nil {
				// ponytail: nested stacks share the root's path map; a logical ID used in
				// both shows the root's path. Key by template if that ever happens.
				s.Name, s.Region, s.paths, s.cdkPath = roots[i].Name, roots[i].Region, roots[i].paths, roots[i].cdkPath
				out = append(out, s)
				delete(nested, f)
			}
		}
	}
	for _, f := range sortedIDs(nested) {
		s := nested[f]
		s.Name = strings.TrimSuffix(f, ".nested.template.json")
		out = append(out, s)
	}
	return out, nil
}

func sortedIDs[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func loadFile(path string) (*Stack, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTemplate(path, raw)
}

type cdkMetadata struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// DisplayPath is the CDK construct path without the stack and the trailing
// "/Resource", or the logical ID for plain templates.
func (s *Stack) DisplayPath(logicalID string) string {
	p, _ := s.Resources[logicalID].Metadata["aws:cdk:path"].(string)
	if p == "" {
		p = s.paths[logicalID]
	}
	if p == "" {
		return logicalID
	}
	p = strings.TrimPrefix(p, "/")
	if rest, ok := strings.CutPrefix(p, s.cdkPath+"/"); ok && s.cdkPath != "" {
		p = rest // a stack inside a Stage: "Prod/Edge/Key" -> "Key"
	} else if _, rest, ok := strings.Cut(p, "/"); ok {
		p = rest
	}
	p = strings.TrimSuffix(p, "/Resource")
	p = strings.TrimSuffix(p, "/Default")
	return p
}
