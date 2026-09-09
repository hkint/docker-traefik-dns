package models

type RecordType string

const (
	TypeA     RecordType = "A"
	TypeAAAA  RecordType = "AAAA"
	TypeCNAME RecordType = "CNAME"
	TypeTXT   RecordType = "TXT"
)

type Record struct {
	ID       string     `json:"id,omitempty"`
	Type     RecordType `json:"type"`
	Name     string     `json:"name"`
	Target   string     `json:"target"`
	TTL      int        `json:"ttl,omitempty"`
	Proxy    bool       `json:"proxy,omitempty"`
	Priority uint16     `json:"priority,omitempty"`
}
