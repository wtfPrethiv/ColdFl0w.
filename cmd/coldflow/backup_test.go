package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"coldflow/internal/config"
	"coldflow/internal/domain"
	"coldflow/internal/engine"
	"coldflow/internal/store"
)

func TestBackupRestoreRelocatesAttachments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "source")
	if err := initialize(dir, true, true); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(c, s)
	f, err := os.CreateTemp(t.TempDir(), "cv-")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("a sample CV")
	f.Seek(0, 0)
	a, err := e.AddAttachment("cv.txt", f)
	f.Close()
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "backup.zip")
	if err = backup(c, archive); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if err = restore(dest, archive); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := store.Get[domain.Attachment](s, "attachment", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil || string(b) != "a sample CV" {
		t.Fatalf("%s %v", b, err)
	}
	if got.Path == a.Path {
		t.Fatal("attachment retained old machine path")
	}
}
func TestRestoreRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "bad.zip")
	f, _ := os.Create(archive)
	z := zip.NewWriter(f)
	w, _ := z.Create("../escape")
	w.Write([]byte("bad"))
	z.Close()
	f.Close()
	if err := restore(filepath.Join(t.TempDir(), "dest"), archive); err == nil {
		t.Fatal("archive traversal accepted")
	}
}
