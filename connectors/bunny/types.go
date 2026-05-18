package bunny

import (
	"fmt"
	"strconv"
	"strings"
)

// recordTypeNames is the canonical name table. Bunny encodes record
// types as integers on the wire; this table is the agent-facing dial.
// Order matches the integer values: A=0, AAAA=1, …, TLSA=15.
//
// Source: https://docs.bunny.net/reference/dnszonepublic_index
var recordTypeNames = []string{
	"A",        // 0
	"AAAA",     // 1
	"CNAME",    // 2
	"TXT",      // 3
	"MX",       // 4
	"Redirect", // 5
	"Flatten",  // 6
	"PullZone", // 7
	"SRV",      // 8
	"CAA",      // 9
	"PTR",      // 10
	"Script",   // 11
	"NS",       // 12
	"SVCB",     // 13
	"HTTPS",    // 14
	"TLSA",     // 15
}

// recordTypeFromName converts a user-supplied name (case-insensitive)
// or numeric string to the wire-level integer. Returns an error for
// names that don't match any known type.
func recordTypeFromName(s string) (int, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		if n >= 0 && n < len(recordTypeNames) {
			return n, nil
		}
		return 0, fmt.Errorf("record type %d out of range (0..%d)", n, len(recordTypeNames)-1)
	}
	upper := strings.ToUpper(s)
	for i, name := range recordTypeNames {
		if strings.ToUpper(name) == upper {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unknown record type %q (want one of %s)", s, strings.Join(recordTypeNames, ", "))
}

// recordTypeName is the reverse — used to surface human-readable type
// names in action responses, alongside the raw int Bunny returns.
func recordTypeName(n int) string {
	if n >= 0 && n < len(recordTypeNames) {
		return recordTypeNames[n]
	}
	return fmt.Sprintf("Type%d", n)
}

// ── wire types ────────────────────────────────────────────────────────────

// zonePage is what GET /dnszone returns.
type zonePage struct {
	Items        []zone `json:"Items"`
	CurrentPage  int    `json:"CurrentPage"`
	TotalItems   int    `json:"TotalItems"`
	HasMoreItems bool   `json:"HasMoreItems"`
}

// zone is the Bunny-shaped record. We only project the fields useful
// for the connector's actions; the API ships dozens more (statistics,
// nameserver checks, DNSSEC keys, ...) which we expose via raw=true.
type zone struct {
	ID                  int64    `json:"Id"`
	Domain              string   `json:"Domain"`
	Records             []record `json:"Records,omitempty"`
	DateModified        string   `json:"DateModified,omitempty"`
	DateCreated         string   `json:"DateCreated,omitempty"`
	NameserversDetected bool     `json:"NameserversDetected,omitempty"`
	NameserverCustom    bool     `json:"CustomNameserversEnabled,omitempty"`
	DnsSecEnabled       bool     `json:"DnsSecEnabled,omitempty"`
}

// record mirrors Bunny's DnsRecordModel — same field names so JSON
// unmarshal works directly.
type record struct {
	ID       int64  `json:"Id"`
	Type     int    `json:"Type"`
	Ttl      int    `json:"Ttl"`
	Value    string `json:"Value"`
	Name     string `json:"Name"`
	Weight   int    `json:"Weight,omitempty"`
	Priority int    `json:"Priority,omitempty"`
	Port     int    `json:"Port,omitempty"`
	Flags    int    `json:"Flags,omitempty"`
	Tag      string `json:"Tag,omitempty"`
	Disabled bool   `json:"Disabled,omitempty"`
	Comment  string `json:"Comment,omitempty"`
}

// recordView is the friendlier shape the connector returns. Includes
// both the integer type and the string name; agents pick whichever
// they want to work with.
func recordView(r record) map[string]any {
	out := map[string]any{
		"id":         r.ID,
		"type":       recordTypeName(r.Type),
		"type_code":  r.Type,
		"name":       r.Name,
		"value":      r.Value,
		"ttl":        r.Ttl,
	}
	if r.Weight != 0 {
		out["weight"] = r.Weight
	}
	if r.Priority != 0 {
		out["priority"] = r.Priority
	}
	if r.Port != 0 {
		out["port"] = r.Port
	}
	if r.Flags != 0 {
		out["flags"] = r.Flags
	}
	if r.Tag != "" {
		out["tag"] = r.Tag
	}
	if r.Comment != "" {
		out["comment"] = r.Comment
	}
	if r.Disabled {
		out["disabled"] = true
	}
	return out
}

func zoneView(z zone) map[string]any {
	out := map[string]any{
		"id":     z.ID,
		"domain": z.Domain,
	}
	if z.DateCreated != "" {
		out["date_created"] = z.DateCreated
	}
	if z.DateModified != "" {
		out["date_modified"] = z.DateModified
	}
	if z.NameserversDetected {
		out["nameservers_detected"] = true
	}
	if z.NameserverCustom {
		out["custom_nameservers"] = true
	}
	if z.DnsSecEnabled {
		out["dnssec"] = true
	}
	if len(z.Records) > 0 {
		records := make([]map[string]any, 0, len(z.Records))
		for _, r := range z.Records {
			records = append(records, recordView(r))
		}
		out["records"] = records
		out["record_count"] = len(z.Records)
	}
	return out
}
