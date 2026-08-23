package app

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"pentgo/internal/agent"
	"pentgo/internal/domain"
)

func TestStructuredProjectFactToolsExposeSchemasAndNoLegacyWriter(t *testing.T) {
	runtime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := toolsByName(tools)
	for _, name := range []string{
		"upsert_project_fact", "get_project_fact", "list_project_facts",
		"search_project_facts", "deprecate_project_fact", "restore_project_fact",
	} {
		tool, found := byName[name]
		if !found {
			t.Fatalf("missing structured fact tool %q", name)
		}
		provider, ok := tool.(agent.ToolSchemaProvider)
		if !ok || provider.InputSchema()["type"] != "object" {
			t.Fatalf("tool %q has no object schema", name)
		}
	}
	if _, found := byName["write_project_fact"]; found {
		t.Fatal("legacy write_project_fact remains exposed")
	}
}

func TestStructuredProjectFactToolsManageFactLifecycle(t *testing.T) {
	runtime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := toolsByName(tools)

	mustInvokeFactTool(t, byName, "upsert_project_fact", map[string]any{
		"key": "host", "category": "target", "summary": "API target", "body": "https://TARGET/api/v1", "confidence": "tentative", "pinned": true,
	})
	get := mustInvokeFactTool(t, byName, "get_project_fact", map[string]any{"key": "host"})
	if !strings.Contains(get, "body:\nhttps://TARGET/api/v1") || !strings.Contains(get, "pinned: true") {
		t.Fatalf("get output = %q", get)
	}
	listed := mustInvokeFactTool(t, byName, "list_project_facts", map[string]any{"limit": 1})
	if !strings.Contains(listed, "host [target/tentative]") {
		t.Fatalf("list output = %q", listed)
	}
	searched := mustInvokeFactTool(t, byName, "search_project_facts", map[string]any{"query": "api/v1", "limit": 1})
	if !strings.Contains(searched, "host [target/tentative]") {
		t.Fatalf("search output = %q", searched)
	}
	mustInvokeFactTool(t, byName, "deprecate_project_fact", map[string]any{"key": "host"})
	listed = mustInvokeFactTool(t, byName, "list_project_facts", map[string]any{})
	if strings.Contains(listed, "host [") {
		t.Fatalf("deprecated fact remained in default list: %q", listed)
	}
	mustInvokeFactTool(t, byName, "restore_project_fact", map[string]any{"key": "host", "confidence": "tentative"})
	fact, found, err := runtime.ProjectFacts().Get("host")
	if err != nil || !found || fact.Confidence != domain.FactConfidenceTentative {
		t.Fatalf("restored fact = %#v found=%v err=%v", fact, found, err)
	}
}

func TestUpsertProjectFactRequiresSuccessfulEvidenceAndWritesEdgesAtomically(t *testing.T) {
	runtime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := toolsByName(tools)

	if output, err := byName["upsert_project_fact"].Invoke(context.Background(), map[string]any{
		"key": "confirmed", "category": "finding", "summary": "missing evidence", "body": "missing evidence", "confidence": "confirmed", "evidence_refs": []any{1},
	}); err == nil || !strings.Contains(output, "requires successful evidence") {
		t.Fatalf("confirmed missing evidence output/error = %q/%v", output, err)
	}
	if _, found, err := runtime.ProjectFacts().Get("confirmed"); err != nil || found {
		t.Fatalf("confirmed fact persisted despite invalid Evidence: found=%v err=%v", found, err)
	}

	evidenceRef, err := runtime.Evidence().Record(context.Background(), "fixture_probe", map[string]any{"target": "TARGET"}, true, "probe succeeded")
	if err != nil {
		t.Fatal(err)
	}
	mustInvokeFactTool(t, byName, "upsert_project_fact", map[string]any{
		"key": "target", "category": "target", "summary": "target", "body": "https://TARGET", "confidence": "tentative",
	})
	mustInvokeFactTool(t, byName, "upsert_project_fact", map[string]any{
		"key": "finding", "category": "finding", "summary": "confirmed finding", "body": "reproducible finding details", "confidence": "confirmed", "evidence_refs": []any{float64(evidenceRef)},
		"edges": []any{map[string]any{"target_key": "target", "edge_type": "discovered_on", "confidence": "confirmed"}},
	})
	index, err := runtime.ProjectFacts().FactIndex(1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(index.Text, "finding -[discovered_on]-&gt; target") {
		t.Fatalf("atomic edge missing or unsafe in fact index: %q", index.Text)
	}
	if _, err := byName["upsert_project_fact"].Invoke(context.Background(), map[string]any{
		"key": "rollback", "category": "note", "summary": "rollback", "body": "rollback", "confidence": "tentative",
		"edges": []any{map[string]any{"target_key": "missing", "edge_type": "supports", "confidence": "tentative"}},
	}); err == nil {
		t.Fatal("upsert with missing edge target succeeded")
	}
	if _, found, err := runtime.ProjectFacts().Get("rollback"); err != nil || found {
		t.Fatalf("fact persisted after failed atomic edge mutation: found=%v err=%v", found, err)
	}
}

func TestProjectFactListRenderingIsBounded(t *testing.T) {
	facts := make([]domain.ProjectFact, 0, maxFactToolLimit)
	for index := 0; index < maxFactToolLimit; index++ {
		facts = append(facts, domain.ProjectFact{FactKey: strings.Repeat("k", 128), Category: domain.FactCategoryNote, Confidence: domain.FactConfidenceTentative, Summary: strings.Repeat("summary ", maxFactSummaryRunes/8)})
	}
	output := renderFactList(facts)
	if utf8.RuneCountInString(output) > maxFactToolOutputRunes || !strings.Contains(output, "additional facts omitted") {
		t.Fatalf("bounded discovery output = %d/%q", utf8.RuneCountInString(output), output)
	}
}

func TestProjectFactToolsBoundKeysAtHostBoundary(t *testing.T) {
	runtime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := toolsByName(tools)
	longKey := strings.Repeat("k", domain.MaxProjectFactKeyRunes+1)
	for _, name := range []string{"get_project_fact", "deprecate_project_fact", "restore_project_fact"} {
		arguments := map[string]any{"key": longKey}
		if name == "restore_project_fact" {
			arguments["confidence"] = "tentative"
		}
		if output, err := byName[name].Invoke(context.Background(), arguments); err == nil || !strings.Contains(output, "at most") {
			t.Fatalf("%s long key output/error = %q/%v", name, output, err)
		}
	}
}

func TestRejectedProjectFactToolIsRecordedAsFailedEvidence(t *testing.T) {
	runtime, session, _ := newApplicationFixture(t, scriptedStepper{})
	tools, err := newRuntimeToolProvider(runtime, session, nil, nil, false).Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := invokeToolCalls(context.Background(), runtime, tools, []agent.ToolCall{{
		ID: "invalid-fact", Name: "upsert_project_fact", Arguments: map[string]any{
			"key": "invalid", "category": "wrong", "summary": "invalid", "body": "invalid", "confidence": "tentative",
		},
	}})
	if len(result) != 1 || result[0].err != nil || !strings.Contains(result[0].message.Content, "工具调用失败") {
		t.Fatalf("tool result = %#v", result)
	}
	record, found := runtime.Evidence().Lookup(1)
	if !found || record.Success {
		t.Fatalf("rejected fact evidence = %#v found=%v", record, found)
	}
	_ = session
}

func toolsByName(tools []agent.Tool) map[string]agent.Tool {
	result := make(map[string]agent.Tool, len(tools))
	for _, tool := range tools {
		result[tool.Name()] = tool
	}
	return result
}

func mustInvokeFactTool(t *testing.T, tools map[string]agent.Tool, name string, arguments map[string]any) string {
	t.Helper()
	tool, found := tools[name]
	if !found {
		t.Fatalf("tool %q is missing", name)
	}
	output, err := tool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("%s failed: output=%q err=%v", name, output, err)
	}
	return output
}
