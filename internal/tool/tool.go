// Package tool defines the Tool interface, a registry, and the built-in
// tools (filesystem + shell) the agent can call. Filesystem access is confined
// to a configurable root directory.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/defomok-max/Ties/internal/provider"
)

// Result is the outcome of running a tool.
type Result struct {
	Content string
	IsError bool
}

// Tool is a single capability exposed to the model.
type Tool interface {
	Name() string
	Description() string
	// Schema returns a JSON Schema object describing the tool's arguments.
	Schema() json.RawMessage
	// Run executes the tool with JSON-encoded arguments.
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// Registry is an ordered collection of tools addressable by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds (or replaces) a tool.
func (r *Registry) Register(t Tool) { r.tools[t.Name()] = t }

// Get returns the named tool.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Unregister removes a tool by name (no-op if absent).
func (r *Registry) Unregister(name string) { delete(r.tools, name) }

// Clone returns a shallow copy sharing the same Tool instances. Mutating the
// clone's membership (e.g. Unregister) does not affect the original — handy for
// giving a sub-agent a reduced tool set.
func (r *Registry) Clone() *Registry {
	c := NewRegistry()
	for n, t := range r.tools {
		c.tools[n] = t
	}
	return c
}

// Names returns the sorted tool names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Definitions converts every tool into a provider.ToolDefinition.
func (r *Registry) Definitions() []provider.ToolDefinition {
	defs := make([]provider.ToolDefinition, 0, len(r.tools))
	for _, name := range r.Names() {
		t := r.tools[name]
		defs = append(defs, provider.ToolDefinition{
			Name: t.Name(), Description: t.Description(), Parameters: t.Schema(),
		})
	}
	return defs
}

// DefaultRegistry returns a registry populated with the built-in tools, all
// confined to root.
func DefaultRegistry(root string) *Registry {
	r := NewRegistry()
	r.Register(newReadTool(root))
	r.Register(newWriteTool(root))
	r.Register(newEditTool(root))
	r.Register(newListTool(root))
	r.Register(newGlobTool(root))
	r.Register(newGrepTool(root))
	r.Register(newBashTool(root))
	r.Register(newMultieditTool(root))
	r.Register(newPatchTool(root))
	r.Register(newTreeTool(root))
	r.Register(newWebfetchTool())
	return r
}

// NewTodoTool returns the planning todo tool. onChange (may be nil) is called
// with the full list after every `set` so callers can render progress.
func NewTodoTool(onChange func([]TodoItem)) Tool { return newTodoTool(onChange) }

// decode unmarshals tool arguments into v, returning a friendly error.
func decode(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
