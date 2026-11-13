// Package catalog exposes the embedded tool snapshot generated from the
// Python API sources. It is read-only at runtime and requires no Python.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.json
var embedded []byte

// Param is one tool parameter.
type Param struct {
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Required    bool           `json:"required"`
	HasDefault  bool           `json:"has_default"`
	Default     any            `json:"default"`
	JSONSchema  map[string]any `json:"json_schema"`
}

// Tool is one MCP tool exposed by the IDA plugin / broker.
type Tool struct {
	Name        string         `json:"name"`
	Group       string         `json:"group"`
	Description string         `json:"description"`
	Summary     string         `json:"summary"`
	Unsafe      bool           `json:"unsafe"`
	SourceFile  string         `json:"source_file"`
	Params      []Param        `json:"params"`
	InputSchema map[string]any `json:"input_schema"`
}

// Resource is one ida:// MCP resource.
type Resource struct {
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Description string `json:"description"`
	Summary     string `json:"summary"`
	SourceFile  string `json:"source_file"`
}

// Group is a command group (one per api_*.py module plus cache).
type Group struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

// Stats is the generator's summary block.
type Stats struct {
	Tools        int `json:"tools"`
	RealTools    int `json:"real_tools"`
	VirtualTools int `json:"virtual_tools"`
	UnsafeTools  int `json:"unsafe_tools"`
	Resources    int `json:"resources"`
	Groups       int `json:"groups"`
}

// Catalog is the complete embedded snapshot.
type Catalog struct {
	SchemaVersion int        `json:"schema_version"`
	GeneratedFrom string     `json:"generated_from"`
	SourceCommit  string     `json:"source_commit"`
	GeneratedAt   string     `json:"generated_at"`
	Groups        []Group    `json:"groups"`
	Tools         []Tool     `json:"tools"`
	Resources     []Resource `json:"resources"`
	Stats         Stats      `json:"stats"`

	byName  map[string]*Tool
	byGroup map[string]*Group
}

var (
	once     sync.Once
	instance *Catalog
	loadErr  error
)

// Parse decodes and indexes a catalog document.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if len(c.Tools) == 0 {
		return nil, fmt.Errorf("catalog contains no tools")
	}
	c.byName = make(map[string]*Tool, len(c.Tools))
	for i := range c.Tools {
		c.byName[c.Tools[i].Name] = &c.Tools[i]
	}
	c.byGroup = make(map[string]*Group, len(c.Groups))
	for i := range c.Groups {
		c.byGroup[c.Groups[i].Name] = &c.Groups[i]
	}
	return &c, nil
}

// Load returns the embedded catalog, parsing it once.
func Load() (*Catalog, error) {
	once.Do(func() {
		instance, loadErr = Parse(embedded)
	})
	return instance, loadErr
}

// MustLoad returns the embedded catalog or panics. It is safe because the
// catalog is generated at build time and covered by tests.
func MustLoad() *Catalog {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// Tool looks up a tool by exact name, or by "group.tool".
func (c *Catalog) Tool(name string) (*Tool, bool) {
	if t, ok := c.byName[name]; ok {
		return t, true
	}
	if group, toolName, ok := strings.Cut(name, "."); ok {
		if t, ok := c.byName[toolName]; ok && t.Group == group {
			return t, true
		}
	}
	return nil, false
}

// Group looks up a group by name.
func (c *Catalog) Group(name string) (*Group, bool) {
	g, ok := c.byGroup[name]
	return g, ok
}

// GroupNames returns group names in catalog order.
func (c *Catalog) GroupNames() []string {
	out := make([]string, 0, len(c.Groups))
	for _, g := range c.Groups {
		out = append(out, g.Name)
	}
	return out
}

// ToolsInGroup returns the tools of a group in stable name order.
func (c *Catalog) ToolsInGroup(group string) []*Tool {
	g, ok := c.byGroup[group]
	if !ok {
		return nil
	}
	out := make([]*Tool, 0, len(g.Tools))
	for _, name := range g.Tools {
		if t, ok := c.byName[name]; ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resource looks up a resource by URI.
func (c *Catalog) Resource(uri string) (*Resource, bool) {
	for i := range c.Resources {
		if c.Resources[i].URI == uri {
			return &c.Resources[i], true
		}
	}
	return nil, false
}

// ToolNames returns all tool names sorted.
func (c *Catalog) ToolNames() []string {
	out := make([]string, 0, len(c.Tools))
	for _, t := range c.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
