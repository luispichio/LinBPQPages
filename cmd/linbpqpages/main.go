package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"linbpqpages/internal/api"
	"linbpqpages/internal/bpq"
	"linbpqpages/internal/config"
	"linbpqpages/web"
)

func main() {
	cfg, err := config.Parse(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatalf("configuración: %v", err)
	}
	logger := log.New(os.Stderr, "linbpqpages: ", log.LstdFlags)
	store, err := bpq.NewStore(cfg.BPQDir, cfg.MailConfig, cfg.SevenPlusDir)
	if err != nil {
		logger.Fatalf("LinBPQ: %v", err)
	}
	instance, err := store.Instance()
	if err != nil {
		logger.Fatalf("configuración LinBPQ: %v", err)
	}
	logger.Printf("BBS %q; DIRMES.SYS: layout packed de %d bytes", instance.BBSName, store.Layout().RecordSize)
	if instance.Import.Configured {
		if instance.Import.IntervalSeconds > 0 {
			logger.Printf("cola Mail/Import/mail.in configurada; FwdInterval: %d segundos", instance.Import.IntervalSeconds)
		} else {
			logger.Printf("cola Mail/Import/mail.in configurada; FwdInterval no definido")
		}
	} else {
		logger.Printf("ADVERTENCIA: linmail.cfg no contiene el ConnectScript IMPORT ./Mail/Import/mail.in; los mensajes enviados no se importarán automáticamente")
	}
	if messages, err := store.Messages(); err != nil {
		logger.Printf("ADVERTENCIA: no se pudo leer DIRMES.SYS al iniciar: %v", err)
	} else {
		logger.Printf("índice LinBPQ listo: %d mensajes", len(messages))
	}

	handler := api.NewServer(store, cfg.SessionTTL, web.Handler(), logger)
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	logger.Printf("escuchando en %s", cfg.Listen)
	if err := server.ListenAndServe(); err != nil {
		logger.Fatalf("servidor: %v", err)
	}
}
