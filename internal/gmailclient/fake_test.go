package gmailclient

import (
	"strings"
	"testing"
	"time"

	"gclean/internal/models"
)

func TestFakeClient_QueryAndSemantics(t *testing.T) {
	msgs := []*models.Message{
		{ID: "a", Subject: "alpha", Date: time.Now(), Sender: models.Sender{Email: "x@example.com"}, Labels: []string{"INBOX"}},
		{ID: "b", Subject: "alpha", Date: time.Now(), Sender: models.Sender{Email: "y@example.com"}, Labels: []string{"INBOX"}},
		{ID: "c", Subject: "beta", Date: time.Now(), Sender: models.Sender{Email: "x@example.com"}, Labels: []string{"INBOX"}},
	}
	c := NewFakeClientFromMessages(msgs)

	// Gmail ANDs search terms: only the message matching BOTH must come back.
	got, err := c.ListMessages("subject:alpha from:x@example.com", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("AND semantics broken, got %#v", got)
	}

	// has: matches a header-key substring, aligned with the engine DSL.
	msgs[0].Headers = map[string]string{"List-Unsubscribe": "<x>"}
	c = NewFakeClientFromMessages(msgs)
	got, err = c.ListMessages("has:unsubscribe", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("has: should match header-key substring, got %#v", got)
	}
}

func TestFakeClient_QueryUnsupportedTokenErrors(t *testing.T) {
	msgs := []*models.Message{{ID: "a", Subject: "alpha", Date: time.Now()}}
	c := NewFakeClientFromMessages(msgs)

	// A token the fake cannot faithfully honor must fail loudly, not
	// silently fall back to a subject substring.
	if _, err := c.ListMessages("older_than:30d", 0); err == nil {
		t.Fatal("unsupported query token must error")
	}
}

func TestFakeClient_InTrash_ReturnsTrashedSubset(t *testing.T) {
	c := NewFakeClientFromMessages([]*models.Message{
		{ID: "a", Subject: "a", Date: time.Now()},
		{ID: "b", Subject: "b", Date: time.Now()},
		{ID: "c", Subject: "c", Date: time.Now()},
	})
	if err := c.TrashMessages([]string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	in, err := c.InTrash([]string{"a", "b", "c", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(in, ","); got != "a,c" {
		t.Fatalf("InTrash = %q, want a,c", got)
	}
}

func TestFakeClient_ListAndTrash(t *testing.T) {
	msgs := []*models.Message{
		{ID: "a", Subject: "alpha", Date: time.Now()},
		{ID: "b", Subject: "beta", Date: time.Now()},
		{ID: "c", Subject: "gamma", Date: time.Now()},
	}
	c := NewFakeClientFromMessages(msgs)

	got, err := c.ListMessages("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}

	got, err = c.ListMessages("subject:alpha", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("query filter broken, got %#v", got)
	}

	if err := c.TrashMessages([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.ListMessages("", 0)
	if len(got) != 2 {
		t.Fatalf("trashed message should disappear from list, got %d", len(got))
	}

	ids := c.TrashedIDs()
	if len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("TrashedIDs off: %v", ids)
	}
	if err := c.RestoreFromTrash([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.ListMessages("", 0)
	if len(got) != 3 {
		t.Fatalf("restored message should reappear, got %d", len(got))
	}
}
