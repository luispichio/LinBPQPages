package bpq

import "time"

const (
	UserFlagExcluded = 0x0001
	UserFlagSysop    = 0x0008
	MessageFlagB2    = 0x01
	MessageFlagFiles = 0x02
)

// User contiene los campos que necesita la interfaz. La contraseña y las
// banderas de acceso son privadas para no serializarlas en la API.
type User struct {
	Callsign string `json:"callsign"`
	Name     string `json:"name,omitempty"`
	Address  string `json:"address,omitempty"`
	HomeBBS  string `json:"home_bbs,omitempty"`
	QRA      string `json:"qra,omitempty"`
	ZIP      string `json:"zip,omitempty"`
	Sysop    bool   `json:"sysop"`

	password string
	excluded bool
}

// Message representa un registro público del índice DIRMES.SYS.
type Message struct {
	Type      string `json:"type"`
	Status    string `json:"status"`
	Number    int64  `json:"number"`
	Size      int64  `json:"size"`
	Date      int64  `json:"date"`
	BBSFrom   string `json:"bbs_from,omitempty"`
	Route     string `json:"route,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	BID       string `json:"bid,omitempty"`
	Title     string `json:"title,omitempty"`
	B2Flags   uint8  `json:"b2_flags,omitempty"`
	UTF8      bool   `json:"utf8"`
	CreatedAt int64  `json:"created_at,omitempty"`
	ChangedAt int64  `json:"changed_at,omitempty"`
}

type Attachment struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url,omitempty"`
	Data        []byte `json:"-"`
}

type MessageBody struct {
	Routing     []string     `json:"routing"`
	Path        []string     `json:"path"`
	Body        string       `json:"body"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type ComposeRequest struct {
	To    string
	Via   string
	Type  string
	Title string
	Body  string
}

type ImportInfo struct {
	Configured      bool  `json:"configured"`
	IntervalSeconds int64 `json:"interval_seconds,omitempty"`
}

type InstanceInfo struct {
	BBSName   string     `json:"bbs_name"`
	SYSOPCall string     `json:"sysop_call,omitempty"`
	Import    ImportInfo `json:"import"`
}

type Status struct {
	BBSName          string         `json:"bbs_name"`
	SYSOPCall        string         `json:"sysop_call,omitempty"`
	ImportConfigured bool           `json:"import_configured"`
	DelaySeconds     int64          `json:"delay_seconds,omitempty"`
	QueuePending     bool           `json:"queue_pending"`
	QueueBytes       int64          `json:"queue_bytes"`
	TotalMessages    int            `json:"total_messages"`
	LatestNumber     int64          `json:"latest_number"`
	ByType           map[string]int `json:"by_type"`
	ByStatus         map[string]int `json:"by_status"`
}

type FileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	MIME     string    `json:"mime"`
	Modified time.Time `json:"modified"`
}

type FileGroup struct {
	Name      string     `json:"name"`
	Primary   *FileInfo  `json:"primary,omitempty"`
	Auxiliary []FileInfo `json:"auxiliary,omitempty"`
}
