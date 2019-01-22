package reactionchain

import (
	"fmt"
)

type Resources interface {
	Dump() []string
	AllResources() []Resource
	Resource(coordinates ResourceCoordinates) Resource
	Roots() []Resource
}

type ResourceCoordinates struct {
	Group     string
	Resource  string
	Namespace string
	Name      string
}

func NewCoordinates(group, resource, namespace, name string) ResourceCoordinates {
	return ResourceCoordinates{
		Group:     group,
		Resource:  resource,
		Namespace: namespace,
		Name:      name,
	}
}

func (c ResourceCoordinates) String() string {
	resource := c.Resource
	if len(c.Group) > 0 {
		resource = resource + "." + c.Group
	}
	return resource + "/" + c.Name + "[" + c.Namespace + "]"
}

type Resource interface {
	From(Resource) Resource
	Note(note string) Resource

	fmt.Stringer
	Coordinates() ResourceCoordinates
	Sources() []Resource
	Dump(indentDepth int) []string
	DumpSources(indentDepth int) []string
}

type SimpleSource struct {
	coordinates ResourceCoordinates
	note        string
	nested      []Resource
	sources     []Resource
}

func NewResource(coordinates ResourceCoordinates) Resource {
	return &SimpleSource{coordinates: coordinates}
}

func NewSingleSource(coordinates ResourceCoordinates, source Resource) *SimpleSource {
	return &SimpleSource{coordinates: coordinates, sources: []Resource{source}}
}
func NewSource(coordinates ResourceCoordinates, sources []Resource) *SimpleSource {
	return &SimpleSource{coordinates: coordinates, sources: sources}
}

func (r *SimpleSource) Coordinates() ResourceCoordinates {
	return r.coordinates
}

func (s *SimpleSource) From(source Resource) Resource {
	s.sources = append(s.sources, source)
	return s
}

func (s *SimpleSource) Note(note string) Resource {
	s.note = note
	return s
}

func (s *SimpleSource) String() string {
	return fmt.Sprintf("%v%s", s.coordinates, s.note)
}

func (s *SimpleSource) Sources() []Resource {
	return s.sources
}

func (r *SimpleSource) Dump(indentDepth int) []string {
	lines := []string{}
	lines = append(lines, indent(indentDepth, r.String()))

	for _, nested := range r.nested {
		lines = append(lines, nested.Dump(indentDepth+1)...)
	}

	return lines
}

func (r *SimpleSource) DumpSources(indentDepth int) []string {
	lines := []string{}
	lines = append(lines, indent(indentDepth, r.String()))

	for _, source := range r.sources {
		lines = append(lines, source.DumpSources(indentDepth+1)...)
	}

	return lines
}

func indent(depth int, in string) string {
	indent := ""
	for i := 0; i < depth; i++ {
		indent = indent + "    "
	}
	return indent + in
}
