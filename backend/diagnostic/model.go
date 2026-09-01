package diagnostic

import "time"

type Kind string

const (
	KindDNS        Kind = "dns"
	KindTCP        Kind = "tcp"
	KindHTTP       Kind = "http"
	KindHTTPS      Kind = "https"
	KindSSH        Kind = "ssh"
	KindSMTP       Kind = "smtp"
	KindSubmission Kind = "submission"
	KindSMTPS      Kind = "smtps"
	KindIMAP       Kind = "imap"
	KindIMAPS      Kind = "imaps"
	KindPOP3       Kind = "pop3"
	KindPOP3S      Kind = "pop3s"
	KindTraceroute Kind = "traceroute"
)

type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusDegraded    Status = "degraded"
	StatusUnreachable Status = "unreachable"
)

type Target struct {
	Kind           Kind   `json:"kind"`
	Address        string `json:"address"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
	Attempts       int    `json:"attempts,omitempty"`
}

type Request struct {
	Targets   []Target `json:"targets"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
}

type Result struct {
	Kind      Kind           `json:"kind"`
	Address   string         `json:"address"`
	Status    Status         `json:"status"`
	LatencyMS int64          `json:"latency_ms"`
	StartedAt time.Time      `json:"started_at"`
	ErrorCode string         `json:"error_code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type Report struct {
	ID         string    `json:"id"`
	Status     Status    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Summary    Summary   `json:"summary"`
	Results    []Result  `json:"results"`
}

type Summary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}
