package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"oss.terrastruct.com/d2/d2graph"
	"oss.terrastruct.com/d2/d2layouts/d2elklayout"
	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	d2log "oss.terrastruct.com/d2/lib/log"
	"oss.terrastruct.com/d2/lib/textmeasure"
	"oss.terrastruct.com/util-go/go2"
)

var NAME_REGEX = "^([0-9\\.]+).*"                                                                            // match only number code
var IGNORE_WITH_PARENT_ID = []int{24200, 24225, 24532, 25061, 25083, 24413, 24374, 24738, 25211, 230, 23795} // IDs of archive folders to ignore

// main reads a data.json file, preprocesses its entries, resolves their dependencies
// and dependents, and then generates a SVG diagram for each component.
func main() {
	data, err := os.ReadFile("data.json")
	if err != nil {
		log.Fatal(err)
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Fatal(err)
	}

	// 1. Prepocessing
	preprocessed := Preprocess(entries)

	// 2. Resolve relations (dependencies and dependents)
	store := ResolveRelations(preprocessed)

	for _, comp := range store {
		log.Printf("Generating %s\n", comp.Name)
		comp.SaveAsSvg()
	}
}

type DirectReference struct {
	Id             int    `json:"id"`
	TypeName       string `json:"typeName"`
	DependencyType []int  `json:"dependencyType"`
}

type Entry struct {
	Uuid             string            `json:"uuid"`
	Id               int               `json:"id"`
	ParentId         int               `json:"parentId"`
	Name             string            `json:"name"`
	TypeName         string            `json:"typeName"`
	IsFolder         bool              `json:"isFolder"`
	DirectReferences []DirectReference `json:"directReferences"`
	SortOrder        int               `json:"SORT_ORDER"`
}

// AsComponent converts an Entry into a Component.
// It initializes the component with the entry's ID, name, and type name.
// The component's dependencies and dependents are initially empty.
func (e *Entry) AsComponent() *Component {
	return &Component{
		Id:           e.Id,
		Name:         e.Name,
		TypeName:     e.TypeName,
		Dependencies: []*Component{},
		Dependents:   []*Component{},
	}
}

type Component struct {
	Id           int
	Name         string
	TypeName     string
	Dependencies []*Component
	Dependents   []*Component
}

// AddDependency adds a dependency component reference to the component.
// This adds a incoming data flow to the component.
func (c *Component) AddDependency(dep *Component) {
	c.Dependencies = append(c.Dependencies, dep)
}

// AddDependent adds a dependent component reference to the component.
// This adds an outgoing data flow to the component.
func (c *Component) AddDependent(dep *Component) {
	c.Dependents = append(c.Dependents, dep)
}

// ShortName returns the short name of the component, using a regular expression.
// Currently this is the number of the component.
func (c *Component) ShortName() string {
	re := regexp.MustCompile(NAME_REGEX)
	matches := re.FindStringSubmatch(c.Name)

	if len(matches) >= 2 {
		return matches[1]
	}

	return c.Name
}

// SaveAsSvg generates a SVG diagram of the component's data flow and saves it to disk.
// The filename is the short name of the component, and the file is saved in the
// diagrams/ directory.
func (c *Component) SaveAsSvg() {
	svg := c.RenderSvg()

	err := os.WriteFile(filepath.Join("./diagrams/", fmt.Sprintf("%s.svg", c.ShortName())), svg, 0600)
	if err != nil {
		log.Fatal(err)
	}
}

// RenderSvg compiles the component's full diagram into a SVG representation.
// It uses D2 graph layout and rendering options to generate the visual output.
// The function returns the generated SVG as a byte slice.
func (c *Component) RenderSvg() []byte {
	contents := c.FullDiagram()

	ruler, _ := textmeasure.NewRuler()
	layoutResolver := func(engine string) (d2graph.LayoutGraph, error) {
		return d2elklayout.DefaultLayout, nil
	}
	renderOpts := &d2svg.RenderOpts{
		Pad:     go2.Pointer(int64(5)),
		ThemeID: &d2themescatalog.NeutralDefault.ID,
	}
	compileOpts := &d2lib.CompileOptions{
		LayoutResolver: layoutResolver,
		Ruler:          ruler,
	}
	ctx := d2log.WithDefault(context.Background())
	diagram, _, _ := d2lib.Compile(ctx, contents, compileOpts, renderOpts)
	out, _ := d2svg.Render(diagram, renderOpts)
	return out
}

// FullDiagram generates a D2 diagram string that represents all incoming and outgoing
// data flows of the component. The diagram is in the D2 graph format.
func (c *Component) FullDiagram() string {
	return fmt.Sprintf("%s\n%s", c.UpstreamDiagram(), c.DownstreamDiagram())
}

// UpstreamDiagram generates a D2 diagram string that represents all incoming data
// flows to the component. Each incoming data flow is represented as an arrow
// from another component to the current component. The arrow is labeled with
// the short name of the other component. The diagram is in the D2 graph
// format.
func (c *Component) UpstreamDiagram() string {
	relations := []string{}
	for _, dep := range c.Dependencies {
		if dep.TypeName != "MeasureSheet" {
			continue
		}

		if dep.Id == c.Id {
			continue
		}

		relations = append(relations, fmt.Sprintf("'%s' -> '%s'", dep.ShortName(), c.ShortName()))
	}

	return strings.Join(relations, "\n")
}

// DownstreamDiagram generates a D2 diagram string that represents all outgoing data
// flows from the component. Each outgoing data flow is represented as an arrow
// from the current component to another component. The arrow is labeled with
// the short name of the other component. The diagram is in the D2 graph format.
func (c *Component) DownstreamDiagram() string {
	relations := []string{}
	for _, dep := range c.Dependents {
		if dep.TypeName != "MeasureSheet" {
			continue
		}

		if dep.Id == c.Id {
			continue
		}

		relations = append(relations, fmt.Sprintf("'%s' -> '%s'", c.ShortName(), dep.ShortName()))
	}

	return strings.Join(relations, "\n")
}

// Preprocess filters the given entries based on specific criteria and returns a
// slice of entries that meet these criteria. The function ignores entries that
// are folders, have zero direct references, belong to ignored parent IDs, or
// are not of the "MeasureSheet" type.
func Preprocess(entries []Entry) []Entry {
	var preprocessed []Entry
	for _, entry := range entries {
		// ignore folders
		if entry.IsFolder {
			continue
		}

		// ignore entries with 0 references
		if len(entry.DirectReferences) == 0 {
			continue
		}

		// ignore entries part of ignored folders (archive)
		if slices.Contains(IGNORE_WITH_PARENT_ID, entry.ParentId) {
			continue
		}

		// only measure sheets
		if entry.TypeName != "MeasureSheet" {
			continue
		}

		preprocessed = append(preprocessed, entry)
	}

	return preprocessed
}

// ResolveRelations processes a slice of Entry objects to establish relationships
// between components by mapping their dependencies and dependents. It converts
// each entry into a Component and stores them in a map using their ID as the key.
// For each entry, it adds references to its dependencies and dependents. Entries
// with neither dependencies nor dependents are removed from the map. The function
// returns a map of components with established relationships.
func ResolveRelations(entries []Entry) map[int]*Component {
	store := make(map[int]*Component)
	for _, entry := range entries {
		store[entry.Id] = entry.AsComponent()
	}

	for _, entry := range entries {
		for _, ref := range entry.DirectReferences {
			if _, ok := store[ref.Id]; ok {
				store[entry.Id].AddDependency(store[ref.Id])
				store[ref.Id].AddDependent(store[entry.Id])
			}
		}
	}

	for _, comp := range store {
		if len(comp.Dependents) == 0 && len(comp.Dependencies) == 0 {
			delete(store, comp.Id)
		}
	}

	return store
}
