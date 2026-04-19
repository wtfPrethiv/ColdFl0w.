package store

import (
	"errors"
	"testing"
	"time"

	"coldflow/internal/domain"
)

func TestDurableQueueAndSearch(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := domain.Contact{ID: "one", Name: "Ada", Email: "ada@example.edu"}
	if err = s.Put("contact", c.ID, c); err != nil {
		t.Fatal(err)
	}
	if err = s.Enqueue("research", "one", c, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.Enqueue("research", "one", c, time.Now()); err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if j.Attempts != 1 {
		t.Fatal(j)
	}
	if _, err = s.Claim(); err == nil {
		t.Fatal("leased job was claimed twice")
	}
	if err = s.Finish(j, errors.New("network unavailable")); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := Get[domain.Contact](s, "contact", "one")
	if err != nil || got.Name != "Ada" {
		t.Fatalf("%+v %v", got, err)
	}
	found, err := s.Search("Ada")
	if err != nil || len(found) != 1 {
		t.Fatalf("%v %v", found, err)
	}
	jobs, err := s.Jobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("%v %v", jobs, err)
	}
	if err = s.Delete("contact", "one"); err != nil {
		t.Fatal(err)
	}
	found, err = s.Search("Ada")
	if err != nil || len(found) != 0 {
		t.Fatalf("%v %v", found, err)
	}
}
