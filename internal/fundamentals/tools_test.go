package fundamentals

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/joseph1009/market-watch/internal/mcp"
)

func toolNamed(t *testing.T, tools []mcp.Tool, name string) mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no tool %s", name)
	return mcp.Tool{}
}

func call(t *testing.T, tool mcp.Tool, args string) (string, error) {
	t.Helper()
	return tool.Call(context.Background(), json.RawMessage(args))
}

// The three tools find what the company reports, read it, and calculate,
// and each call is logged.
func TestTheAccountToolsFindReadAndCalculate(t *testing.T) {
	c := conceptServer(t, map[string]string{
		"AccountsReceivableNetCurrent": `{"units":{"USD":[{"end":"2025-12-31","val":4200000000,"form":"10-K","fp":"FY","fy":2025,"filed":"2026-02-01"}]}}`,
	})
	c.tags = map[int][]TagInfo{42: {
		{Taxonomy: "us-gaap", Tag: "AccountsReceivableNetCurrent", Label: "Accounts Receivable, Net, Current", Units: []string{"USD"}},
		{Taxonomy: "us-gaap", Tag: "Revenues", Label: "Revenues", Units: []string{"USD"}},
	}}
	var log bytes.Buffer
	tools := AccountTools(c, 42, &log)

	found, err := call(t, toolNamed(t, tools, "find_concepts"), `{"query":"receivable"}`)
	if err != nil || !strings.Contains(found, "- us-gaap/AccountsReceivableNetCurrent (Accounts Receivable, Net, Current) [USD]") {
		t.Errorf("find_concepts = %q, %v", found, err)
	}
	read, err := call(t, toolNamed(t, tools, "read_concept"), `{"taxonomy":"us-gaap","tag":"AccountsReceivableNetCurrent"}`)
	if err != nil || !strings.Contains(read, "at 2025-12-31 (10-K): 4,200,000,000 USD") {
		t.Errorf("read_concept = %q, %v", read, err)
	}
	if _, err := call(t, toolNamed(t, tools, "read_concept"), `{"taxonomy":"us-gaap","tag":"Inventory"}`); err == nil || !strings.Contains(err.Error(), "not reported by this company") {
		t.Errorf("an unreported tag = %v, want it said plainly", err)
	}
	sum, err := call(t, toolNamed(t, tools, "compute"), `{"expression":"4.2bn / 35bn * 365","of":"days to collect"}`)
	if err != nil || sum != "days to collect: 4.2bn / 35bn * 365 = 43.8" {
		t.Errorf("compute = %q, %v", sum, err)
	}
	for _, want := range []string{`find_concepts "receivable"`, "read_concept us-gaap/AccountsReceivableNetCurrent", "compute 4.2bn / 35bn * 365 = 43.8"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}

// The company's list of concepts arrives gzipped, as the SEC sends it, and is
// read.
func TestTheConceptsAreReadWhenTheyArriveGzipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Errorf("Accept-Encoding = %q, want gzip", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		_, _ = z.Write([]byte(`{"facts":{"us-gaap":{"InventoryNet":{"label":"Inventory, Net","units":{"USD":[]}}}}}`))
		_ = z.Close()
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), FactsURL: srv.URL + "/CIK%010d.json", Unpaced: true}

	found, err := call(t, toolNamed(t, AccountTools(c, 723125, nil), "find_concepts"), `{"query":"inventory"}`)
	if err != nil || !strings.Contains(found, "us-gaap/InventoryNet (Inventory, Net) [USD]") {
		t.Errorf("find_concepts = %q, %v", found, err)
	}
}

// The lookups run out, and say so; the calculations do not count against
// them.
func TestTheLookupsRunOut(t *testing.T) {
	c := conceptServer(t, nil)
	c.tags = map[int][]TagInfo{42: {{Taxonomy: "us-gaap", Tag: "Revenues", Label: "Revenues"}}}
	tools := AccountTools(c, 42, nil)
	find, sum := toolNamed(t, tools, "find_concepts"), toolNamed(t, tools, "compute")
	for i := 0; i < ToolLookups; i++ {
		if _, err := call(t, find, `{"query":"revenue"}`); err != nil {
			t.Fatalf("lookup %d: %v", i+1, err)
		}
		if _, err := call(t, sum, fmt.Sprintf(`{"expression":"%d + 1"}`, i)); err != nil {
			t.Fatalf("sum %d: %v", i+1, err)
		}
	}
	if _, err := call(t, find, `{"query":"revenue"}`); err == nil || !strings.Contains(err.Error(), "no lookups left") {
		t.Errorf("lookup past the limit = %v", err)
	}
	if _, err := call(t, sum, `{"expression":"2 * 2"}`); err != nil {
		t.Errorf("a calculation after the lookups ran out: %v", err)
	}
}
