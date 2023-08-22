package unused

import (
	"fmt"
	"go/token"
	"os"

	"golang.org/x/tools/go/types/objectpath"
)

type ObjectPath struct {
	PkgPath string
	ObjPath objectpath.Path
}

// XXX make sure that node 0 always exists and is always the root

type SerializedGraph struct {
	nodes       []Node
	nodesByPath map[ObjectPath]NodeID
	// XXX deduplicating on position is dubious for `switch x := foo.(type)`, where x will be declared many times for
	// the different types, but all at the same position. On the other hand, merging these nodes is probably fine.
	nodesByPosition map[token.Position]NodeID
}

func trace(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f, args...)
	fmt.Fprintln(os.Stderr)
}

func (g *SerializedGraph) Merge(nodes []Node) {
	if g.nodesByPath == nil {
		g.nodesByPath = map[ObjectPath]NodeID{}
	}
	if g.nodesByPosition == nil {
		g.nodesByPosition = map[token.Position]NodeID{}
	}
	if len(g.nodes) == 0 {
		// Seed nodes with a root node
		g.nodes = append(g.nodes, Node{})
	}
	// OPT(dh): reuse storage between calls to Merge
	remapping := make([]NodeID, len(nodes))

	// First pass: compute remapping of IDs of to-be-merged nodes
	for _, n := range nodes {
		// XXX Column is never 0. it's 1 if there is no column information in the export data. which sucks, because
		// objects can also genuinely be in column 1.
		if n.ID != 0 && n.Obj.Path == (ObjectPath{}) && n.Obj.Position.Column == 0 {
			// If the object has no path, then it couldn't have come from export data, which means it needs to have full
			// position information including a column.
			panic(fmt.Sprintf("object %q has no path but also no column information", n.Obj.Name))
		}

		if orig, ok := g.nodesByPath[n.Obj.Path]; ok {
			// We already have a node for this object
			trace("deduplicating %d -> %d based on path %s", n.ID, orig, n.Obj.Path)
			remapping[n.ID] = orig
		} else if orig, ok := g.nodesByPosition[n.Obj.Position]; ok && n.Obj.Position.Column != 0 {
			// We already have a node for this object
			trace("deduplicating %d -> %d based on position %s", n.ID, orig, n.Obj.Position)
			remapping[n.ID] = orig
		} else {
			// This object is new to us; change ID to avoid collision
			newID := NodeID(len(g.nodes))
			trace("new node, remapping %d -> %d", n.ID, newID)
			remapping[n.ID] = newID
			g.nodes = append(g.nodes, Node{
				ID:   newID,
				Obj:  n.Obj,
				Uses: make([]NodeID, 0, len(n.Uses)),
				Owns: make([]NodeID, 0, len(n.Owns)),
			})
			if n.ID == 0 {
				// Our root uses all the roots of the subgraphs
				g.nodes[0].Uses = append(g.nodes[0].Uses, newID)
			}
			if n.Obj.Path != (ObjectPath{}) {
				g.nodesByPath[n.Obj.Path] = newID
			}
			if n.Obj.Position.Column != 0 {
				g.nodesByPosition[n.Obj.Position] = newID
			}
		}
	}

	// Second step: apply remapping
	for _, n := range nodes {
		n.ID = remapping[n.ID]
		for i := range n.Uses {
			n.Uses[i] = remapping[n.Uses[i]]
		}
		for i := range n.Owns {
			n.Owns[i] = remapping[n.Owns[i]]
		}
		g.nodes[n.ID].Uses = append(g.nodes[n.ID].Uses, n.Uses...)
		g.nodes[n.ID].Owns = append(g.nodes[n.ID].Owns, n.Owns...)
	}
}
