package web

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const planFixture = `[{"Plan": {"Node Type": "Hash Join", "Join Type": "Inner", "Startup Cost": 10, "Total Cost": 120.5,
  "Plan Rows": 50, "Actual Rows": 48, "Actual Total Time": 1.25, "Actual Loops": 1, "Hash Cond": "(b.author_id = a.id)",
  "Plans": [
    {"Node Type": "Seq Scan", "Schema": "app", "Relation Name": "books", "Startup Cost": 0, "Total Cost": 90, "Plan Rows": 5000},
    {"Node Type": "Hash", "Startup Cost": 5, "Total Cost": 8, "Plan Rows": 5,
     "Plans": [{"Node Type": "Index Scan", "Relation Name": "authors", "Index Name": "authors_pkey", "Startup Cost": 0, "Total Cost": 8, "Plan Rows": 5}]}
  ]}, "Planning Time": 0.2, "Execution Time": 1.4}]`

func TestBuildPlanTree(t *testing.T) {
	var raw any
	require.NoError(t, json.Unmarshal([]byte(planFixture), &raw))

	v := buildPlan(raw)
	require.NotNil(t, v)
	root := v.Root
	assert.Equal(t, "Hash Join (Inner)", root.Type)
	assert.Contains(t, root.Detail, "Hash Cond: (b.author_id = a.id)")
	assert.Equal(t, "10.00..120.50", root.Cost)
	assert.Equal(t, "estimasi 50 baris, aktual 48", root.Rows)
	assert.Equal(t, "1.250 ms × 1 loop", root.Time)
	assert.Equal(t, "planning 0.200 ms · eksekusi 1.400 ms", v.Summary)

	require.Len(t, root.Children, 2)
	seq := root.Children[0]
	assert.Equal(t, "pada app.books", seq.Detail)
	assert.True(t, seq.SeqScan)
	assert.True(t, seq.Costliest, "Seq Scan punya cost sendiri terbesar (90)")
	assert.False(t, root.Costliest)
	assert.Contains(t, root.Children[1].Children[0].Detail, "index authors_pkey")
}

func TestBuildPlanIgnoresNonPostgresShapes(t *testing.T) {
	assert.Nil(t, buildPlan(map[string]any{"query_block": map[string]any{}}))
	assert.Nil(t, buildPlan(nil))
}
