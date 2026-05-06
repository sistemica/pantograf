package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strconv"
	"strings"
)

// AttachmentInfo is the metadata returned for each attached part on read.
// PartID is the dotted-IMAP path (e.g. "2", "2.1") suitable for handing to
// download_attachment in a future fetch.
type AttachmentInfo struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	PartID      string `json:"part_id"`
}

// ParsedMessage carries the structured form of an RFC 822 message after
// multipart walking. TextBody is the preferred text/plain content; HTMLBody
// the text/html alternative if present. Attachments lists every part with
// Content-Disposition: attachment OR a filename param.
type ParsedMessage struct {
	TextBody    string
	HTMLBody    string
	Attachments []AttachmentInfo
}

// parseMessage takes the raw bytes of an RFC 822 message (full BODY[]) and
// returns a structured view. Falls back gracefully on malformed input —
// returns whatever could be recovered, never errors out partway.
func parseMessage(raw []byte) *ParsedMessage {
	out := &ParsedMessage{}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		// Last-resort fallback: treat the whole blob as text.
		out.TextBody = strings.TrimSpace(string(raw))
		return out
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		body, _ := io.ReadAll(msg.Body)
		decoded := decodeBody(body, msg.Header.Get("Content-Transfer-Encoding"))
		if strings.HasPrefix(mediaType, "text/html") {
			out.HTMLBody = strings.TrimSpace(string(decoded))
		} else {
			out.TextBody = strings.TrimSpace(string(decoded))
		}
		return out
	}

	walkMultipart(msg.Body, params["boundary"], "", out)
	return out
}

// walkMultipart recurses through multipart parts. partPrefix is the IMAP
// section path of the *parent* multipart container, accumulating as "1.2.".
func walkMultipart(body io.Reader, boundary, partPrefix string, out *ParsedMessage) {
	if boundary == "" {
		return
	}
	mr := multipart.NewReader(body, boundary)
	idx := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		idx++
		partID := strconv.Itoa(idx)
		if partPrefix != "" {
			partID = partPrefix + "." + partID
		}
		processPart(part, partID, out)
		_ = part.Close()
	}
}

func processPart(part *multipart.Part, partID string, out *ParsedMessage) {
	header := part.Header
	mediaType, params, _ := mime.ParseMediaType(header.Get("Content-Type"))

	// Nested multipart: recurse without consuming bytes here.
	if strings.HasPrefix(mediaType, "multipart/") {
		walkMultipart(part, params["boundary"], partID, out)
		return
	}

	disposition, dispParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	isAttachment := disposition == "attachment" || filename != ""

	body, err := io.ReadAll(part)
	if err != nil {
		return
	}
	decoded := decodeBody(body, header.Get("Content-Transfer-Encoding"))

	if isAttachment {
		out.Attachments = append(out.Attachments, AttachmentInfo{
			Filename:    decodeWord(filename),
			ContentType: mediaType,
			Size:        len(decoded),
			PartID:      partID,
		})
		return
	}

	switch {
	case strings.HasPrefix(mediaType, "text/plain") && out.TextBody == "":
		out.TextBody = strings.TrimSpace(string(decoded))
	case strings.HasPrefix(mediaType, "text/html") && out.HTMLBody == "":
		out.HTMLBody = strings.TrimSpace(string(decoded))
	}
}

func decodeBody(body []byte, encoding string) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		out := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
		// Tolerate whitespace/newlines that some clients sprinkle into base64.
		clean := bytes.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, body)
		n, err := base64.StdEncoding.Decode(out, clean)
		if err != nil {
			return body
		}
		return out[:n]
	case "quoted-printable":
		dec, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return body
		}
		return dec
	default:
		return body
	}
}

// decodeWord handles RFC 2047 encoded-words in filenames ("=?UTF-8?Q?...?=").
func decodeWord(s string) string {
	if s == "" {
		return s
	}
	dec := mime.WordDecoder{}
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// PartIDValid reports whether s is a non-empty dotted-numeric IMAP section.
func PartIDValid(s string) bool {
	if s == "" {
		return false
	}
	for _, p := range strings.Split(s, ".") {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// imapPartSpec parses a part_id like "2.1" into the BodySection.Part []int.
// Returns nil for empty input — caller should treat that as "no specific
// part requested."
func imapPartSpec(partID string) []int {
	if partID == "" {
		return nil
	}
	parts := strings.Split(partID, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// extractAttachmentBytes re-walks the message and returns the decoded bytes
// of the part whose dotted ID matches partID. Returns nil if not found.
func extractAttachmentBytes(raw []byte, partID string) []byte {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil
	}
	target := strings.Split(partID, ".")
	return walkForPart(msg.Body, params["boundary"], nil, target)
}

func walkForPart(body io.Reader, boundary string, prefix, target []string) []byte {
	if boundary == "" {
		return nil
	}
	mr := multipart.NewReader(body, boundary)
	idx := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return nil
		}
		idx++
		current := append(prefix, strconv.Itoa(idx))
		mediaType, params, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if strings.HasPrefix(mediaType, "multipart/") {
			if found := walkForPart(part, params["boundary"], current, target); found != nil {
				return found
			}
			_ = part.Close()
			continue
		}
		if equalIDs(current, target) {
			b, _ := io.ReadAll(part)
			_ = part.Close()
			return decodeBody(b, part.Header.Get("Content-Transfer-Encoding"))
		}
		_ = part.Close()
	}
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = fmt.Sprint
