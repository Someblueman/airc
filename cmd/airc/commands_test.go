package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/mcpserver"
)

func TestCommandTableIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range commandTable {
		if c.name == "" || c.run == nil {
			t.Errorf("entry %q has no name or no runner", c.name)
		}
		if seen[c.name] {
			t.Errorf("%s is listed twice", c.name)
		}
		seen[c.name] = true
		if c.mcp && !c.session {
			t.Errorf("%s is an MCP command but not a session command: the adapter leases it a connection it would ignore", c.name)
		}
		if c.signalsOnly && !c.session {
			t.Errorf("%s is signalsOnly but not a session command, so the mark does nothing", c.name)
		}
		for _, text := range []string{c.help, c.notes} {
			for line := range strings.SplitSeq(text, "\n") {
				if text != "" && !strings.HasPrefix(line, "  ") {
					t.Errorf("%s: help line %q is not indented like the rest of airc -h", c.name, line)
				}
			}
		}
		if c.help != "" && !strings.Contains(c.help, "airc ") {
			t.Errorf("%s: help %q does not show an airc invocation", c.name, c.help)
		}
		if got := commandsByName[c.name]; got == nil || got.name != c.name {
			t.Errorf("commandsByName does not index %s", c.name)
		}
	}
	if len(commands) != len(commandTable) {
		t.Errorf("commands names %d commands, the table has %d", len(commands), len(commandTable))
	}
	for _, name := range []string{"help", "--help", "-h"} {
		if _, ok := commandsByName[name]; ok {
			t.Errorf("%s is dispatch's own case, not a table entry", name)
		}
	}
}

// mcpToolAliases are the MCP tools whose name differs from the command they
// run: thread is a history query (mcp_connections.go), react is a send with
// --reaction and cancel is prepare --cancel (mcp_chat_tools.go).
var mcpToolAliases = map[string]string{"thread": "history", "react": "send", "cancel": "prepare"}

// The tool definitions carry schemas and stay hand-written, so this keeps them
// and the table's mcp marks from drifting apart.
func TestMCPToolsMatchCommandTable(t *testing.T) {
	session := (&mcpAdapter{}).server().NewSession()
	initialize, err := mcpserver.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"table","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	session.Handle(context.Background(), initialize, nil)
	m, err := mcpserver.ParseMessage([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(session.Handle(context.Background(), m, nil), &response); err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, tool := range response.Result.Tools {
		tools[tool.Name] = true
		name := tool.Name
		if alias, ok := mcpToolAliases[name]; ok {
			name = alias
		}
		if c, ok := commandsByName[name]; !ok || !c.mcp {
			t.Errorf("MCP tool %s runs %q, which the table does not mark mcp", tool.Name, name)
		}
	}
	for _, c := range commandTable {
		if c.mcp && !tools[c.name] {
			t.Errorf("%s is marked mcp but no MCP tool of that name exists", c.name)
		}
	}
	for alias, name := range mcpToolAliases {
		if !tools[alias] {
			t.Errorf("alias %s -> %s names no MCP tool", alias, name)
		}
	}
}
