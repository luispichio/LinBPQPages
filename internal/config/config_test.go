package config

import (
	"reflect"
	"testing"
	"time"
)

func TestParseDefaultsAndEnvironment(t *testing.T) {
	environment := map[string]string{
		"LINBPQPAGES_BPQ_DIR":       "/srv/bpq",
		"LINBPQPAGES_LISTEN":        "127.0.0.1:8081",
		"LINBPQPAGES_SESSION_TTL":   "2h",
		"LINBPQPAGES_MAIL_CFG":      "",
		"LINBPQPAGES_SEVENPLUS_DIR": "",
	}
	config, err := Parse(nil, func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		BPQDir:       "/srv/bpq",
		Listen:       "127.0.0.1:8081",
		MailConfig:   "/srv/bpq/linmail.cfg",
		SevenPlusDir: "/srv/bpq/HTML/7plus",
		SessionTTL:   2 * time.Hour,
	}
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("config=%+v, want %+v", config, want)
	}
}

func TestParseFlagsOverrideEnvironment(t *testing.T) {
	config, err := Parse([]string{
		"--bpq-dir", "/opt/node", "--listen", ":8099", "--mail-cfg", "conf/custom.cfg",
		"--sevenplus-dir", "/srv/decoded", "--session-ttl", "45m",
	}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		BPQDir:       "/opt/node",
		Listen:       ":8099",
		MailConfig:   "conf/custom.cfg",
		SevenPlusDir: "/srv/decoded",
		SessionTTL:   45 * time.Minute,
	}
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("config=%+v, want %+v", config, want)
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	for _, args := range [][]string{
		{"--session-ttl", "0"},
		{"--bpq-dir", ""},
		{"--listen", ""},
		{"--unknown"},
	} {
		if _, err := Parse(args, func(string) string { return "" }); err == nil {
			t.Fatalf("Parse(%v) unexpectedly succeeded", args)
		}
	}
	if _, err := Parse(nil, func(key string) string {
		if key == "LINBPQPAGES_SESSION_TTL" {
			return "forever"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid TTL in environment unexpectedly accepted")
	}
}
