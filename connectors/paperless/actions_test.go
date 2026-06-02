package paperless

import (
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToIntList(t *testing.T) {
	got, err := toIntList([]string{"1", " 2 ", "", "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", got)
	}
	if _, err := toIntList([]string{"abc"}); err == nil {
		t.Error("expected error on non-integer id")
	}
}

// TestBuildUploadFilePart is the regression guard for the bug that broke
// the first live upload: Paperless rejects a file part sent as
// application/octet-stream ("no file was submitted"). buildUpload must emit
// the file under field "document" with a real, sniffed Content-Type.
func TestBuildUploadFilePart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	body, contentType, err := buildUpload(path, map[string]string{"title": "hello"})
	if err != nil {
		t.Fatalf("buildUpload: %v", err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type %q: %v", contentType, err)
	}

	mr := multipart.NewReader(body, params["boundary"])
	var sawDoc, sawTitle bool
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		switch part.FormName() {
		case "document":
			sawDoc = true
			if part.FileName() != "doc.pdf" {
				t.Errorf("filename = %q, want doc.pdf", part.FileName())
			}
			if ct := part.Header.Get("Content-Type"); ct == "" || ct == "application/octet-stream" {
				t.Errorf("file part Content-Type = %q; must be a sniffed real type, not octet-stream", ct)
			}
			data, _ := io.ReadAll(part)
			if !strings.HasPrefix(string(data), "%PDF") {
				t.Errorf("file content not preserved: %q", string(data))
			}
		case "title":
			sawTitle = true
		}
	}
	if !sawDoc {
		t.Error("multipart body missing the 'document' file part")
	}
	if !sawTitle {
		t.Error("multipart body missing the 'title' field")
	}
}
