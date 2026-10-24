package web

import (
	"fmt"
	"strings"
)

const largeSeqScanRows = 1000

type planNode struct {
	Type      string
	Detail    string
	Cost      string
	Rows      string
	Time      string
	SeqScan   bool
	Costliest bool
	Children  []*planNode
	self      float64
}

type planView struct {
	Root    *planNode
	Summary string
}

func buildPlan(raw any) *planView {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	top, ok := list[0].(map[string]any)
	if !ok {
		return nil
	}
	p, ok := top["Plan"].(map[string]any)
	if !ok {
		return nil
	}
	root := planFrom(p)
	var all []*planNode
	collect(root, &all)
	costliest := all[0]
	for _, n := range all {
		if n.self > costliest.self {
			costliest = n
		}
	}
	costliest.Costliest = true

	var summary []string
	if v, ok := top["Planning Time"].(float64); ok {
		summary = append(summary, fmt.Sprintf("planning %.3f ms", v))
	}
	if v, ok := top["Execution Time"].(float64); ok {
		summary = append(summary, fmt.Sprintf("eksekusi %.3f ms", v))
	}
	return &planView{Root: root, Summary: strings.Join(summary, " · ")}
}

func planFrom(p map[string]any) *planNode {
	str := func(k string) string { s, _ := p[k].(string); return s }
	num := func(k string) (float64, bool) { f, ok := p[k].(float64); return f, ok }

	n := &planNode{Type: str("Node Type")}
	for _, k := range []string{"Operation", "Join Type", "Strategy"} {
		if v := str(k); v != "" {
			n.Type += " (" + v + ")"
		}
	}
	var detail []string
	if rel := str("Relation Name"); rel != "" {
		if schema := str("Schema"); schema != "" {
			rel = schema + "." + rel
		}
		detail = append(detail, "pada "+rel)
	}
	if idx := str("Index Name"); idx != "" {
		detail = append(detail, "index "+idx)
	}
	for _, k := range []string{"Index Cond", "Hash Cond", "Merge Cond", "Join Filter", "Filter"} {
		if v := str(k); v != "" {
			detail = append(detail, k+": "+v)
		}
	}
	n.Detail = strings.Join(detail, " · ")

	startup, _ := num("Startup Cost")
	total, _ := num("Total Cost")
	n.Cost = fmt.Sprintf("%.2f..%.2f", startup, total)
	planRows, _ := num("Plan Rows")
	n.Rows = fmt.Sprintf("estimasi %.0f baris", planRows)
	if actual, ok := num("Actual Rows"); ok {
		n.Rows += fmt.Sprintf(", aktual %.0f", actual)
	}
	if t, ok := num("Actual Total Time"); ok {
		loops, _ := num("Actual Loops")
		n.Time = fmt.Sprintf("%.3f ms × %.0f loop", t, loops)
	}
	n.SeqScan = str("Node Type") == "Seq Scan" && planRows >= largeSeqScanRows

	n.self = total
	if children, ok := p["Plans"].([]any); ok {
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				child := planFrom(cm)
				n.Children = append(n.Children, child)
				if ct, ok := cm["Total Cost"].(float64); ok {
					n.self -= ct
				}
			}
		}
	}
	return n
}

func collect(n *planNode, out *[]*planNode) {
	*out = append(*out, n)
	for _, c := range n.Children {
		collect(c, out)
	}
}
