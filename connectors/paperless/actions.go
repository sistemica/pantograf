package paperless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	stdhttp "net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// sess narrows the connector.Session to our concrete type.
func sess(s connector.Session) (*session, error) {
	cs, ok := s.(*session)
	if !ok {
		return nil, errors.New("paperless: wrong session type")
	}
	return cs, nil
}

// getList GETs a paginated endpoint and returns the standard envelope.
func (s *session) getList(ctx context.Context, path string, q url.Values) (any, error) {
	var page listPage
	if err := s.http.GetJSON(ctx, path, q, &page); err != nil {
		return nil, err
	}
	return page, nil
}

// toIntList parses a string list of ids into ints, rejecting non-numerics.
func toIntList(vals []string) ([]int, error) {
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("not an integer id: %q", v)
		}
		out = append(out, n)
	}
	return out, nil
}

// ── list-documents ──────────────────────────────────────────────────────────

type listDocumentsAction struct{}

func (listDocumentsAction) Name() string        { return "list-documents" }
func (listDocumentsAction) DisplayName() string { return "List / search documents" }
func (listDocumentsAction) Description() string {
	return "List documents with optional full-text search and filters. query runs Paperless's full-text index; title/correspondent/document_type/tags filter the result set."
}
func (listDocumentsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Label: "Full-text query", Kind: connector.FieldString, Description: "Searches OCR content + metadata via the Paperless index."},
		{Name: "title", Label: "Title contains", Kind: connector.FieldString},
		{Name: "correspondent", Label: "Correspondent id", Kind: connector.FieldInt},
		{Name: "document_type", Label: "Document type id", Kind: connector.FieldInt},
		{Name: "tags", Label: "Tag ids (all must match)", Kind: connector.FieldStringList},
		{Name: "ordering", Label: "Ordering", Kind: connector.FieldString, Default: "-created", Description: "Field to sort by; prefix - for descending. e.g. created, -added, title."},
		{Name: "page", Label: "Page", Kind: connector.FieldInt, Default: 1},
		{Name: "page_size", Label: "Page size", Kind: connector.FieldInt, Default: 25},
	}}
}
func (a listDocumentsAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	q := url.Values{}
	if v := strings.TrimSpace(p.String("query")); v != "" {
		q.Set("query", v)
	}
	if v := strings.TrimSpace(p.String("title")); v != "" {
		q.Set("title__icontains", v)
	}
	if v := p.Int("correspondent"); v > 0 {
		q.Set("correspondent__id", strconv.Itoa(v))
	}
	if v := p.Int("document_type"); v > 0 {
		q.Set("document_type__id", strconv.Itoa(v))
	}
	if tags := p.StringList("tags"); len(tags) > 0 {
		ids, err := toIntList(tags)
		if err != nil {
			return nil, err
		}
		strs := make([]string, len(ids))
		for i, id := range ids {
			strs[i] = strconv.Itoa(id)
		}
		q.Set("tags__id__all", strings.Join(strs, ","))
	}
	q.Set("ordering", p.String("ordering"))
	q.Set("page", strconv.Itoa(p.Int("page")))
	q.Set("page_size", strconv.Itoa(p.Int("page_size")))
	return cs.getList(ctx, "/api/documents/", q)
}

// ── get-document ────────────────────────────────────────────────────────────

type getDocumentAction struct{}

func (getDocumentAction) Name() string        { return "get-document" }
func (getDocumentAction) DisplayName() string { return "Get document" }
func (getDocumentAction) Description() string {
	return "Fetch one document by id. With metadata=true also returns the file metadata (media size, mime, archive checksum, original filename)."
}
func (getDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Label: "Document id", Kind: connector.FieldInt, Required: true},
		{Name: "metadata", Label: "Include file metadata", Kind: connector.FieldBool, Default: false},
	}}
}
func (a getDocumentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	id := p.Int("id")
	if id <= 0 {
		return nil, errors.New("id is required")
	}
	var doc map[string]any
	if err := cs.http.GetJSON(ctx, fmt.Sprintf("/api/documents/%d/", id), nil, &doc); err != nil {
		return nil, err
	}
	if p.Bool("metadata") {
		var meta any
		if err := cs.http.GetJSON(ctx, fmt.Sprintf("/api/documents/%d/metadata/", id), nil, &meta); err == nil {
			doc["metadata"] = meta
		}
	}
	return doc, nil
}

// ── download-document ───────────────────────────────────────────────────────

type downloadDocumentAction struct{}

func (downloadDocumentAction) Name() string        { return "download-document" }
func (downloadDocumentAction) DisplayName() string { return "Download document" }
func (downloadDocumentAction) Description() string {
	return "Download a document's file to disk. By default the archived PDF; original=true fetches the originally-consumed file (e.g. the source scan)."
}
func (downloadDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Label: "Document id", Kind: connector.FieldInt, Required: true},
		{Name: "out", Label: "Output file path", Kind: connector.FieldString, Required: true, IsPath: true},
		{Name: "original", Label: "Fetch original (not archived)", Kind: connector.FieldBool, Default: false},
	}}
}
func (a downloadDocumentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	id := p.Int("id")
	if id <= 0 {
		return nil, errors.New("id is required")
	}
	out := p.String("out")
	if out == "" {
		return nil, errors.New("out is required")
	}
	path := fmt.Sprintf("/api/documents/%d/download/", id)
	if p.Bool("original") {
		path += "?original=true"
	}
	resp, err := cs.http.Do(ctx, stdhttp.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("download: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"saved":        true,
		"out":          out,
		"bytes":        n,
		"content_type": resp.Header.Get("Content-Type"),
	}, nil
}

// ── upload-document ─────────────────────────────────────────────────────────

type uploadDocumentAction struct{}

func (uploadDocumentAction) Name() string        { return "upload-document" }
func (uploadDocumentAction) DisplayName() string { return "Upload document" }
func (uploadDocumentAction) Description() string {
	return "Upload a file for OCR + consumption. Asynchronous — returns a task UUID; poll task-status to get the resulting document id. Tags can't be set here (multipart limitation); set them via update-document after consumption."
}
func (uploadDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "file", Label: "File to upload", Kind: connector.FieldString, Required: true, IsPath: true},
		{Name: "title", Label: "Title", Kind: connector.FieldString},
		{Name: "created", Label: "Created date (YYYY-MM-DD)", Kind: connector.FieldString},
		{Name: "correspondent", Label: "Correspondent id", Kind: connector.FieldInt},
		{Name: "document_type", Label: "Document type id", Kind: connector.FieldInt},
		{Name: "archive_serial_number", Label: "Archive serial number", Kind: connector.FieldInt},
	}}
}
func (a uploadDocumentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	file := p.String("file")
	if file == "" {
		return nil, errors.New("file is required")
	}
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}
	fields := map[string]string{}
	if v := strings.TrimSpace(p.String("title")); v != "" {
		fields["title"] = v
	}
	if v := strings.TrimSpace(p.String("created")); v != "" {
		fields["created"] = v
	}
	if v := p.Int("correspondent"); v > 0 {
		fields["correspondent"] = strconv.Itoa(v)
	}
	if v := p.Int("document_type"); v > 0 {
		fields["document_type"] = strconv.Itoa(v)
	}
	if v := p.Int("archive_serial_number"); v > 0 {
		fields["archive_serial_number"] = strconv.Itoa(v)
	}
	// Build the multipart body in memory. Unlike the transport's streaming
	// PostMultipart helper, this sets a real per-part Content-Type on the
	// file — Paperless's parser rejects an application/octet-stream part as
	// "no file submitted". Documents are small enough that buffering is fine.
	body, contentType, err := buildUpload(file, fields)
	if err != nil {
		return nil, err
	}
	resp, err := cs.http.Do(ctx, stdhttp.MethodPost, "/api/documents/post_document/", body, contentType)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != stdhttp.StatusOK {
		return nil, fmt.Errorf("upload: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	// Paperless returns the consume task UUID as a bare JSON string.
	//
	// QUIRK: a 200 here means "queued", NOT "stored". Consumption is async and
	// can still fail — most commonly because Paperless dedupes on the file's
	// content hash, so re-uploading identical bytes lands the task in FAILURE
	// with a "duplicate of …" message (a changed title/filename does NOT make
	// it unique). Always poll task-status to confirm the document landed.
	var task string
	if err := json.Unmarshal(bytes.TrimSpace(raw), &task); err != nil {
		task = strings.Trim(strings.TrimSpace(string(raw)), `"`)
	}
	return map[string]any{"task_id": task, "status": "queued"}, nil
}

// buildUpload assembles the post_document multipart body: the file under
// the "document" field with a sniffed Content-Type, plus any string fields.
func buildUpload(path string, fields map[string]string) (io.Reader, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	// Sniff content type from the first 512 bytes, then rewind.
	head := make([]byte, 512)
	n, _ := f.Read(head)
	ctype := stdhttp.DetectContentType(head[:n])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="document"; filename=%q`, filepath.Base(path)))
	h.Set("Content-Type", ctype)
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", err
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}

// ── update-document ─────────────────────────────────────────────────────────

type updateDocumentAction struct{}

func (updateDocumentAction) Name() string        { return "update-document" }
func (updateDocumentAction) DisplayName() string { return "Update document" }
func (updateDocumentAction) Description() string {
	return "Patch a document's metadata. Only the fields you pass change. tags REPLACES the full tag set (pass all desired tag ids)."
}
func (updateDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Label: "Document id", Kind: connector.FieldInt, Required: true},
		{Name: "title", Label: "Title", Kind: connector.FieldString},
		{Name: "correspondent", Label: "Correspondent id", Kind: connector.FieldInt},
		{Name: "document_type", Label: "Document type id", Kind: connector.FieldInt},
		{Name: "tags", Label: "Tag ids (replaces all)", Kind: connector.FieldStringList},
		{Name: "archive_serial_number", Label: "Archive serial number", Kind: connector.FieldInt},
		{Name: "created", Label: "Created date (YYYY-MM-DD)", Kind: connector.FieldString},
	}}
}
func (a updateDocumentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	id := p.Int("id")
	if id <= 0 {
		return nil, errors.New("id is required")
	}
	body := map[string]any{}
	if p.Has("title") {
		body["title"] = p.String("title")
	}
	if p.Has("correspondent") {
		body["correspondent"] = p.Int("correspondent")
	}
	if p.Has("document_type") {
		body["document_type"] = p.Int("document_type")
	}
	if p.Has("archive_serial_number") {
		body["archive_serial_number"] = p.Int("archive_serial_number")
	}
	if p.Has("created") {
		body["created"] = p.String("created")
	}
	if p.Has("tags") {
		ids, err := toIntList(p.StringList("tags"))
		if err != nil {
			return nil, err
		}
		body["tags"] = ids
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to update — pass at least one field")
	}
	var doc map[string]any
	if err := cs.http.SendJSON(ctx, stdhttp.MethodPatch, fmt.Sprintf("/api/documents/%d/", id), body, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// ── delete-document ─────────────────────────────────────────────────────────

type deleteDocumentAction struct{}

func (deleteDocumentAction) Name() string        { return "delete-document" }
func (deleteDocumentAction) DisplayName() string { return "Delete document" }
func (deleteDocumentAction) Description() string {
	return "Permanently delete a document (moves to the trash on recent Paperless, then auto-purges). Irreversible from the API."
}
func (deleteDocumentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Label: "Document id", Kind: connector.FieldInt, Required: true},
	}}
}
func (a deleteDocumentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	id := p.Int("id")
	if id <= 0 {
		return nil, errors.New("id is required")
	}
	resp, err := cs.http.Do(ctx, stdhttp.MethodDelete, fmt.Sprintf("/api/documents/%d/", id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusNoContent && resp.StatusCode != stdhttp.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("delete: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return map[string]any{"deleted": true, "id": id}, nil
}

// ── taxonomy: list ──────────────────────────────────────────────────────────

// taxonomyListAction is one parameterised list endpoint (tags /
// correspondents / document_types). The Actions() slice registers one
// configured instance per endpoint — same shape, different path.
type taxonomyListAction struct {
	name  string
	label string // plural noun, e.g. "tags"
	path  string
}

func (a taxonomyListAction) Name() string        { return a.name }
func (a taxonomyListAction) DisplayName() string { return "List " + a.label }
func (a taxonomyListAction) Description() string {
	return "List " + a.label + " (name + id), sorted by name. Use the ids to filter list-documents or set on update-document."
}
func (a taxonomyListAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Label: "Name contains", Kind: connector.FieldString},
		{Name: "page_size", Label: "Page size", Kind: connector.FieldInt, Default: 200},
	}}
}
func (a taxonomyListAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	q := url.Values{"ordering": {"name"}, "page_size": {strconv.Itoa(p.Int("page_size"))}}
	if v := strings.TrimSpace(p.String("name")); v != "" {
		q.Set("name__icontains", v)
	}
	return cs.getList(ctx, a.path, q)
}

// ── taxonomy: create ────────────────────────────────────────────────────────

// createNamed POSTs {name:...} (+ extras) to a taxonomy endpoint.
func createNamed(ctx context.Context, cs *session, path string, body map[string]any) (any, error) {
	var out map[string]any
	if err := cs.http.SendJSON(ctx, stdhttp.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type createTagAction struct{}

func (createTagAction) Name() string        { return "create-tag" }
func (createTagAction) DisplayName() string { return "Create tag" }
func (createTagAction) Description() string {
	return "Create a tag. Optional color (hex, e.g. #a6cee3) and is_inbox_tag."
}
func (createTagAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Label: "Name", Kind: connector.FieldString, Required: true},
		{Name: "color", Label: "Color (hex)", Kind: connector.FieldString},
		{Name: "is_inbox_tag", Label: "Inbox tag", Kind: connector.FieldBool, Default: false},
	}}
}
func (a createTagAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	name := strings.TrimSpace(p.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{"name": name}
	if c := strings.TrimSpace(p.String("color")); c != "" {
		body["color"] = c
	}
	if p.Bool("is_inbox_tag") {
		body["is_inbox_tag"] = true
	}
	return createNamed(ctx, cs, "/api/tags/", body)
}

type createCorrespondentAction struct{}

func (createCorrespondentAction) Name() string        { return "create-correspondent" }
func (createCorrespondentAction) DisplayName() string { return "Create correspondent" }
func (createCorrespondentAction) Description() string { return "Create a correspondent by name." }
func (createCorrespondentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Label: "Name", Kind: connector.FieldString, Required: true},
	}}
}
func (a createCorrespondentAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	name := strings.TrimSpace(p.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	return createNamed(ctx, cs, "/api/correspondents/", map[string]any{"name": name})
}

type createDocumentTypeAction struct{}

func (createDocumentTypeAction) Name() string        { return "create-document-type" }
func (createDocumentTypeAction) DisplayName() string { return "Create document type" }
func (createDocumentTypeAction) Description() string { return "Create a document type by name." }
func (createDocumentTypeAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Label: "Name", Kind: connector.FieldString, Required: true},
	}}
}
func (a createDocumentTypeAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	name := strings.TrimSpace(p.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	return createNamed(ctx, cs, "/api/document_types/", map[string]any{"name": name})
}

// ── task-status ─────────────────────────────────────────────────────────────

type taskStatusAction struct{}

func (taskStatusAction) Name() string        { return "task-status" }
func (taskStatusAction) DisplayName() string { return "Task status" }
func (taskStatusAction) Description() string {
	return "Look up a consume task by the UUID returned from upload-document. Reports SUCCESS/FAILURE/PENDING and, on success, the resulting document id."
}
func (taskStatusAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "task_id", Label: "Task UUID", Kind: connector.FieldString, Required: true},
	}}
}
func (a taskStatusAction) Run(ctx context.Context, s connector.Session, p connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	p = p.WithDefaults(a.Schema())
	tid := strings.TrimSpace(p.String("task_id"))
	if tid == "" {
		return nil, errors.New("task_id is required")
	}
	var tasks []any
	if err := cs.http.GetJSON(ctx, "/api/tasks/", url.Values{"task_id": {tid}}, &tasks); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task found for %s (it may have expired)", tid)
	}
	return tasks[0], nil
}

// ── statistics ──────────────────────────────────────────────────────────────

type statisticsAction struct{}

func (statisticsAction) Name() string        { return "statistics" }
func (statisticsAction) DisplayName() string { return "Statistics" }
func (statisticsAction) Description() string {
	return "Instance-wide counts: total documents, documents in inbox, total characters, file-type breakdown."
}
func (statisticsAction) Schema() connector.Schema { return connector.Schema{} }
func (a statisticsAction) Run(ctx context.Context, s connector.Session, _ connector.Values) (any, error) {
	cs, err := sess(s)
	if err != nil {
		return nil, err
	}
	var stats any
	if err := cs.http.GetJSON(ctx, "/api/statistics/", nil, &stats); err != nil {
		return nil, err
	}
	return stats, nil
}
