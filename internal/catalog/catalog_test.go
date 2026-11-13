package catalog

import "testing"

func TestEmbeddedCatalogIsComplete(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Stats.RealTools != 87 {
		t.Fatalf("real tools = %d, want 87", c.Stats.RealTools)
	}
	if len(c.Tools) != 89 {
		t.Fatalf("tools = %d, want 87 real + 2 virtual", len(c.Tools))
	}
	if len(c.Resources) != 11 {
		t.Fatalf("resources = %d, want 11", len(c.Resources))
	}
	if len(c.Groups) != 13 {
		t.Fatalf("groups = %d, want 13", len(c.Groups))
	}
	if c.Stats.UnsafeTools != 25 {
		t.Fatalf("unsafe = %d, want 25", c.Stats.UnsafeTools)
	}
}

func TestToolLookup(t *testing.T) {
	c := MustLoad()

	tool, ok := c.Tool("decompile")
	if !ok {
		t.Fatal("decompile not found")
	}
	if tool.Group != "analysis" {
		t.Fatalf("group = %q, want analysis", tool.Group)
	}
	if len(tool.Params) != 2 {
		t.Fatalf("params = %d, want 2", len(tool.Params))
	}

	if _, ok := c.Tool("analysis.decompile"); !ok {
		t.Fatal("qualified lookup failed")
	}
	if _, ok := c.Tool("core.decompile"); ok {
		t.Fatal("qualified lookup should not cross groups")
	}
	if _, ok := c.Tool("cache.cache_status"); !ok {
		t.Fatal("virtual cache tool missing")
	}
}

func TestEveryGroupToolResolves(t *testing.T) {
	c := MustLoad()
	for _, group := range c.Groups {
		for _, name := range group.Tools {
			tool, ok := c.Tool(name)
			if !ok {
				t.Fatalf("group %s lists missing tool %s", group.Name, name)
			}
			if tool.Group != group.Name {
				t.Fatalf("tool %s group = %s, want %s", name, tool.Group, group.Name)
			}
		}
	}
}

func TestUnsafeFlagIsPresent(t *testing.T) {
	c := MustLoad()
	tool, ok := c.Tool("dbg_start")
	if !ok {
		t.Fatal("dbg_start not found")
	}
	if !tool.Unsafe {
		t.Fatal("dbg_start should be marked unsafe")
	}
	tool, ok = c.Tool("decompile")
	if !ok {
		t.Fatal("decompile not found")
	}
	if tool.Unsafe {
		t.Fatal("decompile should not be unsafe")
	}
}

func TestInputSchemaOmitsInstanceID(t *testing.T) {
	c := MustLoad()
	tool, _ := c.Tool("decompile")
	props, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected input schema: %#v", tool.InputSchema)
	}
	if _, exists := props["instance_id"]; exists {
		t.Fatal("input schema must not inject instance_id; --instance is global")
	}
}
