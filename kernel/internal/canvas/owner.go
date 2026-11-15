package canvas

import "strings"

// Owner turns an author graph into the owner view of a process (contract 0060 §3, GET /processes/{name}?view=owner):
//
//   - placement: a flow joins the stage whose model fires its trigger (on: <model>.<event>), when the process has
//     a stage for that model; otherwise it stays where Build put it (the first stage it touches, 0014 §7);
//   - handoffs between stages are drawn again after the move;
//   - cuts: no CEL (conditions, rule checks, approval and stage when), no detail of plumbing steps (find, find_all,
//     for_each), no source paths, no editable values (the owner view is read-only) and no "inferred".
//
// Node ids, labels, kinds, descriptions and summaries are unchanged, so a client can still join nodes and edges.
func Owner(g *Graph) {
	home := map[string]string{}
	for _, gr := range g.Groups {
		if _, ok := home[gr.Model]; !ok {
			home[gr.Model] = gr.ID
		}
	}
	// a flow is one file: its trigger is the node at pointer "/", and every node of that file moves with it
	move := map[string]string{}
	for _, n := range g.Nodes {
		if n.Kind != KindTrigger || !strings.HasSuffix(n.ID, "#/") {
			continue
		}
		if m := eventRe.FindStringSubmatch(n.Detail); m != nil {
			if to, ok := home[m[1]]; ok && to != n.Group {
				move[strings.TrimSuffix(n.ID, "#/")] = to
			}
		}
	}
	if len(move) > 0 {
		// where each handoff left its stage before the move, with its label
		before := map[string]string{}
		for _, n := range g.Nodes {
			before[n.ID] = n.Group
		}
		leave := map[string]Edge{}
		edges := g.Edges[:0:0]
		for _, e := range g.Edges {
			if e.Kind == EdgeHandoff {
				leave[before[e.From]] = e
				continue
			}
			edges = append(edges, e)
		}
		moved := map[string][]string{}
		byID := map[string]*Node{}
		for i := range g.Nodes {
			n := &g.Nodes[i]
			byID[n.ID] = n
			if to, ok := move[fileOf(n.ID)]; ok {
				n.Group = to
			}
		}
		for i := range g.Groups {
			gr := &g.Groups[i]
			kept := []string{}
			for _, id := range gr.Nodes {
				if n := byID[id]; n != nil && n.Group != gr.ID {
					moved[n.Group] = append(moved[n.Group], id)
					continue
				}
				kept = append(kept, id)
			}
			gr.Nodes = kept
		}
		for i := range g.Groups {
			gr := &g.Groups[i]
			gr.Nodes = append(gr.Nodes, moved[gr.ID]...)
			for _, k := range Kinds {
				gr.Counts[k] = 0
			}
			for _, id := range gr.Nodes {
				gr.Counts[byID[id].Kind]++
			}
		}
		// nodes stay listed in group order, as Build lists them
		nodes := make([]Node, 0, len(g.Nodes))
		for _, gr := range g.Groups {
			for _, id := range gr.Nodes {
				nodes = append(nodes, *byID[id])
			}
		}
		g.Nodes = nodes
		// handoffs again between consecutive stages that have nodes: from the step it left before (when that step
		// is still in its stage), else the stage's last step, else its last node; into the next stage's first node
		var prev *Group
		for i := range g.Groups {
			gr := &g.Groups[i]
			if len(gr.Nodes) == 0 {
				continue
			}
			if prev != nil {
				e := Edge{Kind: EdgeHandoff, To: gr.Nodes[0]}
				old, ok := leave[prev.ID]
				if ok && byID[old.From] != nil && byID[old.From].Group == prev.ID {
					e.From, e.Label = old.From, old.Label
				} else {
					e.From, e.Label = lastOf(prev, byID), old.Label
				}
				edges = append(edges, e)
			}
			prev = gr
		}
		g.Edges = edges
	}
	g.Source, g.Inferred = Source{}, false
	for i := range g.Groups {
		g.Groups[i].Source, g.Groups[i].When = Source{}, ""
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		n.Source, n.Editable = Source{}, map[string]any{}
		switch {
		case n.Kind == KindCondition, n.Kind == KindRule, n.Kind == KindApproval:
			n.Detail = "" // CEL: a rule's check, an approval's when
		case n.Kind == KindStep && strings.HasSuffix(n.ID, "/when"):
			n.Detail = "" // a stage's when
		case n.Kind == KindAction && plumbing(n.Detail):
			n.Detail = ""
		}
	}
}

func fileOf(id string) string {
	if i := strings.Index(id, "#"); i >= 0 {
		return id[:i]
	}
	return id
}

// plumbing is a step that only reads (find, find_all) or loops (for_each): it does nothing an owner would name.
func plumbing(detail string) bool {
	verb, _, _ := strings.Cut(detail, " ")
	return verb == "find" || verb == "find_all" || verb == "for_each"
}

// lastOf is a stage's last lifecycle step, else its last node.
func lastOf(gr *Group, byID map[string]*Node) string {
	for i := len(gr.Nodes) - 1; i >= 0; i-- {
		if n := byID[gr.Nodes[i]]; n != nil && n.Kind == KindStep && !strings.HasSuffix(n.ID, "/when") {
			return n.ID
		}
	}
	return gr.Nodes[len(gr.Nodes)-1]
}
