package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	BPQDir       string
	Listen       string
	MailConfig   string
	SevenPlusDir string
	SessionTTL   time.Duration
}

func Parse(args []string, getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	defaults := Config{
		BPQDir:     envOr(getenv, "LINBPQPAGES_BPQ_DIR", "/home/luis/linbpq"),
		Listen:     envOr(getenv, "LINBPQPAGES_LISTEN", ":8081"),
		SessionTTL: 8 * time.Hour,
	}
	if value := getenv("LINBPQPAGES_SESSION_TTL"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil {
			return Config{}, fmt.Errorf("invalid LINBPQPAGES_SESSION_TTL: %w", err)
		}
		defaults.SessionTTL = duration
	}

	flags := flag.NewFlagSet("linbpqpages", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bpqDir := flags.String("bpq-dir", defaults.BPQDir, "directorio de datos de LinBPQ")
	listen := flags.String("listen", defaults.Listen, "dirección HTTP de escucha")
	mailConfig := flags.String("mail-cfg", envOr(getenv, "LINBPQPAGES_MAIL_CFG", ""), "archivo linmail.cfg (por defecto: <bpq-dir>/linmail.cfg)")
	sevenPlusDir := flags.String("sevenplus-dir", envOr(getenv, "LINBPQPAGES_SEVENPLUS_DIR", ""), "directorio de archivos decodificados 7+ (por defecto: <bpq-dir>/HTML/7plus)")
	ttl := flags.Duration("session-ttl", defaults.SessionTTL, "duración de las sesiones")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("argumentos no reconocidos: %v", flags.Args())
	}
	if *bpqDir == "" {
		return Config{}, fmt.Errorf("--bpq-dir no puede estar vacío")
	}
	if *listen == "" {
		return Config{}, fmt.Errorf("--listen no puede estar vacío")
	}
	if *ttl <= 0 {
		return Config{}, fmt.Errorf("--session-ttl debe ser positivo")
	}
	if *mailConfig == "" {
		*mailConfig = filepath.Join(*bpqDir, "linmail.cfg")
	}
	if *sevenPlusDir == "" {
		*sevenPlusDir = filepath.Join(*bpqDir, "HTML", "7plus")
	}
	return Config{
		BPQDir:       *bpqDir,
		Listen:       *listen,
		MailConfig:   *mailConfig,
		SevenPlusDir: *sevenPlusDir,
		SessionTTL:   *ttl,
	}, nil
}

func envOr(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}
