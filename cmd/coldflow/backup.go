package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"coldflow/internal/config"
	"coldflow/internal/domain"
	"coldflow/internal/store"
)

func backup(c config.Config, destination string) error {
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(destination)
		}
	}()
	tmp, err := os.MkdirTemp("", "coldflow-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	s, err := store.Open(c.DataDir)
	if err != nil {
		return err
	}
	defer s.Close()
	snapshot := filepath.Join(tmp, "coldflow.db")
	if err = s.Backup(snapshot); err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	add := func(name, path string) error {
		r, err := os.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0600)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		return err
	}
	if err = add("coldflow.db", snapshot); err != nil {
		zw.Close()
		return err
	}
	for _, name := range []string{"config.json", "api-token", "gmail-token.json", "ssh_host_ed25519", "ssh_host_ed25519.pub"} {
		path := filepath.Join(c.DataDir, name)
		if _, statErr := os.Stat(path); statErr == nil {
			if err = add(name, path); err != nil {
				zw.Close()
				return err
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(c.DataDir, "attachments"))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err = add("attachments/"+entry.Name(), filepath.Join(c.DataDir, "attachments", entry.Name())); err != nil {
			zw.Close()
			return err
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	success = true
	fmt.Println("Backup created:", destination, "(contains credentials; keep private)")
	return nil
}
func restore(dir, source string) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return errors.New("restore destination must be empty")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	zr, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer zr.Close()
	total := uint64(0)
	allowed := map[string]bool{"coldflow.db": true, "config.json": true, "api-token": true, "gmail-token.json": true, "ssh_host_ed25519": true, "ssh_host_ed25519.pub": true}
	for _, f := range zr.File {
		clean := filepath.ToSlash(filepath.Clean(f.Name))
		if clean != f.Name || strings.Contains(clean, "..") || strings.ContainsAny(clean, ":\\") || strings.HasPrefix(clean, "/") || f.Mode()&os.ModeSymlink != 0 || (!allowed[clean] && !strings.HasPrefix(clean, "attachments/")) {
			return errors.New("backup contains an unexpected path")
		}
		total += f.UncompressedSize64
		if total > 2<<30 {
			return errors.New("backup exceeds 2 GiB restore limit")
		}
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, f := range zr.File {
		target := filepath.Join(dir, filepath.FromSlash(f.Name))
		resolved, _ := filepath.Abs(target)
		base, _ := filepath.Abs(dir)
		if !strings.HasPrefix(resolved, base+string(os.PathSeparator)) {
			return errors.New("restore path escapes destination")
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			r.Close()
			return err
		}
		_, copyErr := io.Copy(w, io.LimitReader(r, 2<<30))
		r.Close()
		closeErr := w.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	c, err := config.Load(dir)
	if err != nil {
		return err
	}
	if err = config.Save(c); err != nil {
		return err
	}
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	attachments, err := store.List[domain.Attachment](s, "attachment")
	if err != nil {
		return err
	}
	for _, a := range attachments {
		a.Path = filepath.Join(dir, "attachments", a.ID)
		if err = s.Put("attachment", a.ID, a); err != nil {
			return err
		}
	}
	fmt.Println("Restored into", dir)
	return nil
}
