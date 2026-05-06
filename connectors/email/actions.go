package email

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sistemica/pantograf/connector"
	smtptr "github.com/sistemica/pantograf/transport/smtp"
	"github.com/wneessen/go-mail"
)

// ── read_emails ───────────────────────────────────────────────────────────

type readEmailsAction struct{}

func (readEmailsAction) Name() string         { return "read_emails" }
func (readEmailsAction) DisplayName() string  { return "Read emails" }
func (readEmailsAction) Description() string  { return "Fetch the most recent N messages from a folder." }
func (readEmailsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "folder", Label: "Folder", Kind: connector.FieldString, Default: "INBOX"},
		{Name: "limit", Label: "Max messages", Kind: connector.FieldInt, Default: 10},
		{Name: "include_body", Label: "Include body", Kind: connector.FieldBool, Default: true},
	}}
}

type EmailMessage struct {
	UID         uint32           `json:"uid"`
	From        string           `json:"from"`
	To          []string         `json:"to"`
	Subject     string           `json:"subject"`
	Date        time.Time        `json:"date"`
	Body        string           `json:"body,omitempty"`      // text/plain
	HTMLBody    string           `json:"html_body,omitempty"` // text/html alternative
	Attachments []AttachmentInfo `json:"attachments,omitempty"`
}

func (a readEmailsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("read_emails: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	folder := params.String("folder")
	limit := params.Int("limit")
	includeBody := params.Bool("include_body")

	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	mbox, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", folder, err)
	}
	if mbox.NumMessages == 0 {
		return []EmailMessage{}, nil
	}

	// Fetch the last `limit` messages by sequence number.
	from := uint32(1)
	if int64(mbox.NumMessages) > int64(limit) {
		from = mbox.NumMessages - uint32(limit) + 1
	}
	seqset := imap.SeqSetNum()
	seqset.AddRange(from, mbox.NumMessages)

	fetchOpts := &imap.FetchOptions{
		UID:      true,
		Envelope: true,
	}
	if includeBody {
		// Fetch the whole RFC 822 message so the multipart parser has the
		// boundary param from the top-level Content-Type header.
		fetchOpts.BodySection = []*imap.FetchItemBodySection{{}}
	}

	cmd := cli.Fetch(seqset, fetchOpts)
	defer cmd.Close()

	var out []EmailMessage
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}
		em := EmailMessage{UID: uint32(buf.UID)}
		if buf.Envelope != nil {
			em.Subject = buf.Envelope.Subject
			em.Date = buf.Envelope.Date
			if len(buf.Envelope.From) > 0 {
				em.From = formatAddr(buf.Envelope.From[0])
			}
			for _, a := range buf.Envelope.To {
				em.To = append(em.To, formatAddr(a))
			}
		}
		if includeBody {
			for _, body := range buf.BodySection {
				parsed := parseMessage(body.Bytes)
				em.Body = parsed.TextBody
				em.HTMLBody = parsed.HTMLBody
				em.Attachments = parsed.Attachments
				break
			}
		}
		out = append(out, em)
	}
	if err := cmd.Close(); err != nil {
		return nil, err
	}
	// Reverse to newest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func formatAddr(a imap.Address) string {
	if a.Name != "" {
		return fmt.Sprintf("%s <%s@%s>", a.Name, a.Mailbox, a.Host)
	}
	return fmt.Sprintf("%s@%s", a.Mailbox, a.Host)
}

// ── list_folders ──────────────────────────────────────────────────────────

type listFoldersAction struct{}

func (listFoldersAction) Name() string         { return "list_folders" }
func (listFoldersAction) DisplayName() string  { return "List folders" }
func (listFoldersAction) Description() string  { return "List all IMAP folders (mailboxes)." }
func (listFoldersAction) Schema() connector.Schema {
	return connector.Schema{}
}

func (listFoldersAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("list_folders: wrong session type")
	}
	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	cmd := cli.List("", "*", nil)
	mbs, err := cmd.Collect()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(mbs))
	for _, mb := range mbs {
		names = append(names, mb.Mailbox)
	}
	return names, nil
}

// ── search_emails ─────────────────────────────────────────────────────────

type searchEmailsAction struct{}

func (searchEmailsAction) Name() string         { return "search_emails" }
func (searchEmailsAction) DisplayName() string  { return "Search emails" }
func (searchEmailsAction) Description() string  { return "Search a folder by subject substring." }
func (searchEmailsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Label: "Subject substring", Kind: connector.FieldString, Required: true},
		{Name: "folder", Label: "Folder", Kind: connector.FieldString, Default: "INBOX"},
		{Name: "limit", Label: "Max results", Kind: connector.FieldInt, Default: 20},
	}}
}

func (a searchEmailsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("search_emails: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	query := strings.TrimSpace(params.String("query"))
	if query == "" {
		return nil, errors.New("query is required")
	}
	folder := params.String("folder")
	limit := params.Int("limit")

	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	if _, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, err
	}

	criteria := &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: query}},
	}
	searchData, err := cli.Search(criteria, &imap.SearchOptions{}).Wait()
	if err != nil {
		return nil, err
	}
	uids := searchData.AllSeqNums()
	if len(uids) == 0 {
		return []EmailMessage{}, nil
	}
	if len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	return fetchByNums(cli, uids)
}

func fetchByNums(cli *imapclient.Client, nums []uint32) ([]EmailMessage, error) {
	seqset := imap.SeqSetNum(nums...)
	cmd := cli.Fetch(seqset, &imap.FetchOptions{UID: true, Envelope: true})
	defer cmd.Close()
	var out []EmailMessage
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			return nil, err
		}
		em := EmailMessage{UID: uint32(buf.UID)}
		if buf.Envelope != nil {
			em.Subject = buf.Envelope.Subject
			em.Date = buf.Envelope.Date
			if len(buf.Envelope.From) > 0 {
				em.From = formatAddr(buf.Envelope.From[0])
			}
		}
		out = append(out, em)
	}
	return out, cmd.Close()
}

// ── get_email ─────────────────────────────────────────────────────────────

type getEmailAction struct{}

func (getEmailAction) Name() string         { return "get_email" }
func (getEmailAction) DisplayName() string  { return "Get email" }
func (getEmailAction) Description() string  { return "Fetch a single message by UID, with body." }
func (getEmailAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "uid", Label: "Message UID", Kind: connector.FieldInt, Required: true},
		{Name: "folder", Label: "Folder", Kind: connector.FieldString, Default: "INBOX"},
	}}
}

func (a getEmailAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("get_email: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	uid := uint32(params.Int("uid"))
	if uid == 0 {
		return nil, errors.New("uid is required")
	}
	folder := params.String("folder")

	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	if _, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, err
	}

	uidset := imap.UIDSetNum(imap.UID(uid))
	cmd := cli.Fetch(uidset, &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}}, // whole message
	})
	defer cmd.Close()

	msg := cmd.Next()
	if msg == nil {
		return nil, fmt.Errorf("uid %d not found in %s", uid, folder)
	}
	buf, err := msg.Collect()
	if err != nil {
		return nil, err
	}
	em := EmailMessage{UID: uint32(buf.UID)}
	if buf.Envelope != nil {
		em.Subject = buf.Envelope.Subject
		em.Date = buf.Envelope.Date
		if len(buf.Envelope.From) > 0 {
			em.From = formatAddr(buf.Envelope.From[0])
		}
		for _, addr := range buf.Envelope.To {
			em.To = append(em.To, formatAddr(addr))
		}
	}
	for _, body := range buf.BodySection {
		parsed := parseMessage(body.Bytes)
		em.Body = parsed.TextBody
		em.HTMLBody = parsed.HTMLBody
		em.Attachments = parsed.Attachments
		break
	}
	return em, cmd.Close()
}

// composeFields returns the address/body/attachment fields shared by
// send_email and save_draft. Each action prepends/appends its own extras.
func composeFields() []connector.FieldSpec {
	return []connector.FieldSpec{
		{Name: "to", Label: "To", Kind: connector.FieldStringList, Required: true},
		{Name: "cc", Label: "CC", Kind: connector.FieldStringList},
		{Name: "bcc", Label: "BCC", Kind: connector.FieldStringList},
		{Name: "subject", Label: "Subject", Kind: connector.FieldString, Required: true},
		{Name: "body", Label: "Body", Kind: connector.FieldLongText, Required: true},
		{Name: "html", Label: "HTML body", Kind: connector.FieldBool, Default: false},
		{Name: "from", Label: "From override (must be a configured alias)", Kind: connector.FieldString},
		{Name: "attachments", Label: "File paths to attach", Kind: connector.FieldStringList},
	}
}

// buildMessage assembles a *mail.Msg from params. Returns the validated
// addresses so the caller can echo them back in its result.
func buildMessage(params connector.Values, defaultFrom string) (*mail.Msg, []string, error) {
	to := params.StringList("to")
	if len(to) == 0 {
		return nil, nil, errors.New("to is required")
	}
	subject := params.String("subject")
	body := params.String("body")
	if subject == "" || body == "" {
		return nil, nil, errors.New("subject and body are required")
	}
	from := params.String("from")
	if from == "" {
		from = defaultFrom
	}

	msg := mail.NewMsg()
	if err := msg.From(from); err != nil {
		return nil, nil, fmt.Errorf("from: %w", err)
	}
	if err := msg.To(to...); err != nil {
		return nil, nil, fmt.Errorf("to: %w", err)
	}
	if cc := params.StringList("cc"); len(cc) > 0 {
		if err := msg.Cc(cc...); err != nil {
			return nil, nil, fmt.Errorf("cc: %w", err)
		}
	}
	if bcc := params.StringList("bcc"); len(bcc) > 0 {
		if err := msg.Bcc(bcc...); err != nil {
			return nil, nil, fmt.Errorf("bcc: %w", err)
		}
	}
	msg.Subject(subject)
	if params.Bool("html") {
		msg.SetBodyString(mail.TypeTextHTML, body)
	} else {
		msg.SetBodyString(mail.TypeTextPlain, body)
	}
	for _, path := range params.StringList("attachments") {
		// AttachFile reads lazily on send — stat now so a typo fails fast.
		if _, err := os.Stat(path); err != nil {
			return nil, nil, fmt.Errorf("attach %s: %w", path, err)
		}
		msg.AttachFile(path)
	}
	return msg, to, nil
}

// ── download_attachment ───────────────────────────────────────────────────

type downloadAttachmentAction struct{}

func (downloadAttachmentAction) Name() string         { return "download_attachment" }
func (downloadAttachmentAction) DisplayName() string  { return "Download attachment" }
func (downloadAttachmentAction) Description() string  { return "Fetch one attachment part by UID + part_id and write decoded bytes to disk." }
func (downloadAttachmentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "uid", Label: "Message UID", Kind: connector.FieldInt, Required: true},
		{Name: "part_id", Label: "Part ID (e.g. 2 or 2.1)", Kind: connector.FieldString, Required: true},
		{Name: "out", Label: "Output file path", Kind: connector.FieldString, Required: true},
		{Name: "folder", Label: "Folder", Kind: connector.FieldString, Default: "INBOX"},
	}}
}

func (a downloadAttachmentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("download_attachment: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	uid := uint32(params.Int("uid"))
	if uid == 0 {
		return nil, errors.New("uid is required")
	}
	partID := params.String("part_id")
	if !PartIDValid(partID) {
		return nil, fmt.Errorf("invalid part_id %q", partID)
	}
	outPath := params.String("out")
	if outPath == "" {
		return nil, errors.New("out is required")
	}
	folder := params.String("folder")

	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	if _, err := cli.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, err
	}

	// Re-fetch the whole message and walk to the requested part. Cheaper
	// would be a partial BodySection fetch with Part:[]int — but the part
	// header (with Content-Transfer-Encoding) is needed to decode, and a
	// partial fetch returns only the part body. Whole-message fetch keeps
	// decoding correct without an extra round-trip.
	uidset := imap.UIDSetNum(imap.UID(uid))
	cmd := cli.Fetch(uidset, &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{}},
	})
	defer cmd.Close()

	msg := cmd.Next()
	if msg == nil {
		return nil, fmt.Errorf("uid %d not found in %s", uid, folder)
	}
	buf, err := msg.Collect()
	if err != nil {
		return nil, err
	}
	if len(buf.BodySection) == 0 {
		return nil, errors.New("no body returned")
	}
	parsed := parseMessage(buf.BodySection[0].Bytes)
	var match *AttachmentInfo
	for i := range parsed.Attachments {
		if parsed.Attachments[i].PartID == partID {
			match = &parsed.Attachments[i]
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("part_id %s not found among %d attachments", partID, len(parsed.Attachments))
	}

	// Re-walk to grab decoded bytes for the matched part. parseMessage
	// throws bytes away after sizing them; for now do the work twice.
	// Optimisation: keep bytes in AttachmentInfo when we know the caller
	// intends to download. Punt — small messages.
	body := extractAttachmentBytes(buf.BodySection[0].Bytes, partID)
	if body == nil {
		return nil, fmt.Errorf("failed to extract part %s bytes", partID)
	}

	if err := os.WriteFile(outPath, body, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", outPath, err)
	}
	return map[string]any{
		"saved":        true,
		"path":         outPath,
		"size":         len(body),
		"filename":     match.Filename,
		"content_type": match.ContentType,
	}, nil
}

// ── save_draft ────────────────────────────────────────────────────────────

type saveDraftAction struct{}

func (saveDraftAction) Name() string         { return "save_draft" }
func (saveDraftAction) DisplayName() string  { return "Save draft" }
func (saveDraftAction) Description() string  { return "Compose a message and APPEND it to the Drafts folder." }
func (saveDraftAction) Schema() connector.Schema {
	fields := composeFields()
	fields = append(fields, connector.FieldSpec{
		Name: "folder", Label: "Drafts folder", Kind: connector.FieldString, Default: "Drafts",
	})
	return connector.Schema{Fields: fields}
}

func (a saveDraftAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("save_draft: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	folder := params.String("folder")

	msg, _, err := buildMessage(params, s.cred.Values.String(fEmail))
	if err != nil {
		return nil, err
	}

	var rfc822 strings.Builder
	if _, err := msg.WriteTo(&rfc822); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	raw := rfc822.String()

	cli, err := s.imapClient()
	if err != nil {
		return nil, err
	}
	cmd := cli.Append(folder, int64(len(raw)), &imap.AppendOptions{
		Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen},
	})
	if _, err := cmd.Write([]byte(raw)); err != nil {
		_ = cmd.Close()
		return nil, fmt.Errorf("append write: %w", err)
	}
	if err := cmd.Close(); err != nil {
		return nil, fmt.Errorf("append close: %w", err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("append wait: %w", err)
	}
	return map[string]any{
		"saved":  true,
		"folder": folder,
		"uid":    uint32(data.UID),
	}, nil
}

// ── send_email ────────────────────────────────────────────────────────────

type sendEmailAction struct{}

func (sendEmailAction) Name() string         { return "send_email" }
func (sendEmailAction) DisplayName() string  { return "Send email" }
func (sendEmailAction) Description() string  { return "Send an email via SMTP." }
func (sendEmailAction) Schema() connector.Schema {
	return connector.Schema{Fields: composeFields()}
}

func (a sendEmailAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("send_email: wrong session type")
	}
	params = params.WithDefaults(a.Schema())

	msg, to, err := buildMessage(params, s.cred.Values.String(fEmail))
	if err != nil {
		return nil, err
	}
	if err := smtptr.Send(ctx, smtpConfigFromCred(s.cred), msg); err != nil {
		return nil, err
	}
	return map[string]any{
		"sent": true,
		"to":   to,
	}, nil
}

