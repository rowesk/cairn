package cairn

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestMetadata(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Config{DataDir: dir, PublisherToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	p := savedPage{Title: "Report", Agent: "Agent", OriginalRequest: "Private original", RequestSummary: "Find my closest dentist.", RequestSource: "Hermes"}
	for _, owner := range []bool{true, false} {
		w := httptest.NewRecorder()
		s.renderReader(w, httptest.NewRequest("GET", "/abc", nil), p, "abc", owner, !owner, false, "/abc")
		body := w.Body.String()
		if strings.Contains(body, p.OriginalRequest) {
			t.Fatal("original shown instead of summary")
		}
		if strings.Contains(body, p.RequestSummary) != owner || strings.Contains(body, "via Hermes") != owner {
			t.Fatal("request privacy or rendering incorrect")
		}
	}
	object, err := s.writeObject(publication{HTML: "<p>Report</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO pages (id,slug,title,agent,original_request,created_at,object,request_summary,request_source) VALUES ('a','abc','Report','Agent','Original','',?,'Summary','Hermes')", object); err != nil {
		t.Fatal(err)
	}
	p, err = s.getPage("abc")
	if err != nil || p.RequestSummary != "Summary" || p.RequestSource != "Hermes" {
		t.Fatalf("read metadata: %+v %v", p, err)
	}
	s.Close()
	s, err = Open(Config{DataDir: dir, PublisherToken: strings.Repeat("x", 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = s.getPage("abc")
	if err != nil || p.RequestSummary != "Summary" {
		t.Fatal("metadata not retained")
	}
	in := publication{Title: "Title", Agent: "Agent", OriginalRequest: "Original", HTML: "<p>Body</p>", RequestSummary: strings.Repeat("word ", 40)}
	if err := validatePublication(in); err != nil {
		t.Fatal(err)
	}
	in.RequestSummary += "extra"
	if validatePublication(in) == nil {
		t.Fatal("accepted 41 words")
	}
}
