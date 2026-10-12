package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"linbpqpages/internal/bpq"
)

const (
	maxJSONBody = 1 << 20
	maxPageSize = 200
)

type Server struct {
	store      *bpq.Store
	sessions   *sessions
	static     http.Handler
	logger     *log.Logger
	sessionTTL time.Duration
}

func NewServer(store *bpq.Store, sessionTTL time.Duration, static http.Handler, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		store:      store,
		sessions:   newSessions(sessionTTL),
		static:     static,
		logger:     logger,
		sessionTTL: sessionTTL,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		w.Header().Set("Cache-Control", "no-store")
		serveAPI(s, w, r)
		return
	}
	if s.static == nil {
		http.NotFound(w, r)
		return
	}
	s.static.ServeHTTP(w, r)
}

func serveAPI(s *Server, w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/login":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.login(w, r)
	case r.URL.Path == "/api/logout":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.logout(w, r)
	case r.URL.Path == "/api/me":
		if !s.requireUser(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.me(w, r)
	case r.URL.Path == "/api/status":
		if !s.requireUser(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.status(w)
	case r.URL.Path == "/api/messages":
		if !s.requireUser(w, r) {
			return
		}
		s.messages(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/messages/"):
		if !s.requireUser(w, r) {
			return
		}
		s.message(w, r)
	case r.URL.Path == "/api/files":
		if !s.requireUser(w, r) {
			return
		}
		s.files(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/files/"):
		if !s.requireUser(w, r) {
			return
		}
		s.file(w, r)
	default:
		http.NotFound(w, r)
	}
}

type loginRequest struct {
	Callsign string `json:"callsign"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(request.Callsign) > 32 || len(request.Password) > 128 || strings.TrimSpace(request.Callsign) == "" || strings.TrimSpace(request.Callsign) != request.Callsign {
		writeError(w, http.StatusBadRequest, "callsign o contraseña inválidos")
		return
	}
	user, authenticated, err := s.store.Authenticate(request.Callsign, request.Password)
	if err != nil {
		s.logger.Printf("login: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer la configuración de usuarios")
		return
	}
	if !authenticated {
		writeError(w, http.StatusUnauthorized, "callsign o contraseña incorrectos")
		return
	}
	token, err := s.sessions.create(user)
	if err != nil {
		s.logger.Printf("session: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo crear la sesión")
		return
	}
	setSessionCookie(w, token, s.sessionTTL)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.delete(cookie.Value)
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) status(w http.ResponseWriter) {
	status, err := s.store.Status()
	if err != nil {
		s.logger.Printf("status: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el estado de LinBPQ")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := currentUser(r); ok {
		return true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		if user, ok := s.sessions.get(cookie.Value); ok {
			*r = *r.WithContext(withUser(r.Context(), user))
			return true
		}
	}
	writeError(w, http.StatusUnauthorized, "sesión requerida")
	return false
}

type userContextKey struct{}

func withUser(ctx context.Context, user bpq.User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func currentUser(r *http.Request) (bpq.User, bool) {
	user, ok := r.Context().Value(userContextKey{}).(bpq.User)
	return user, ok
}

func messageVisibleTo(message bpq.Message, user bpq.User) bool {
	if user.Sysop {
		return true
	}
	if message.Status == "K" || message.Status == "H" {
		return false
	}
	switch strings.ToUpper(message.Type) {
	case "B", "T":
		return true
	case "P":
		return sameCallsign(message.From, user.Callsign) || sameCallsign(message.To, user.Callsign)
	default:
		return false
	}
}

func sameCallsign(left, right string) bool {
	return left != "" && strings.EqualFold(left, right)
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listMessages(w, r)
	case http.MethodPost:
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "origen de solicitud no permitido")
			return
		}
		s.composeMessage(w, r)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "sesión requerida")
		return
	}
	all, err := s.store.Messages()
	if err != nil {
		s.logger.Printf("messages: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el índice de mensajes")
		return
	}
	query := r.URL.Query()
	typeFilter := strings.ToUpper(strings.TrimSpace(query.Get("type")))
	textFilter := strings.ToLower(strings.TrimSpace(query.Get("q")))
	filtered := make([]bpq.Message, 0, len(all))
	for _, message := range all {
		if !messageVisibleTo(message, user) {
			continue
		}
		if typeFilter != "" && message.Type != typeFilter {
			continue
		}
		if textFilter != "" && !strings.Contains(strings.ToLower(message.Title), textFilter) &&
			!strings.Contains(strings.ToLower(message.From), textFilter) &&
			!strings.Contains(strings.ToLower(message.To), textFilter) &&
			!strings.Contains(strings.ToLower(message.Route), textFilter) &&
			!strings.Contains(strings.ToLower(message.BID), textFilter) {
			continue
		}
		filtered = append(filtered, message)
	}
	page, pageSize, err := pagination(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	start := len(filtered)
	totalPages := (len(filtered) + pageSize - 1) / pageSize
	if page <= totalPages {
		start = (page - 1) * pageSize
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	items := filtered[start:end]
	if items == nil {
		items = []bpq.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": items, "page": page, "page_size": pageSize,
		"total": len(filtered), "total_pages": totalPages,
	})
}

func pagination(query url.Values) (int, int, error) {
	page, pageSize := 1, 50
	var err error
	if value := query.Get("page"); value != "" {
		page, err = strconv.Atoi(value)
		if err != nil || page < 1 {
			return 0, 0, errors.New("page inválida")
		}
	}
	if value := query.Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil || pageSize < 1 || pageSize > maxPageSize {
			return 0, 0, fmt.Errorf("page_size debe estar entre 1 y %d", maxPageSize)
		}
	}
	return page, pageSize, nil
}

func (s *Server) message(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if number, attachmentIndex, isAttachment, ok := messageAttachmentPath(r.URL.Path); ok && isAttachment {
		s.attachment(w, r, number, attachmentIndex)
		return
	}
	number, ok := messageNumber(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "número de mensaje inválido")
		return
	}
	user, _ := currentUser(r)
	message, found, err := s.findMessage(number)
	if err != nil {
		s.logger.Printf("message index: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el índice de mensajes")
		return
	}
	if !found || !messageVisibleTo(message, user) {
		writeError(w, http.StatusNotFound, "mensaje no encontrado")
		return
	}
	body, err := s.store.ReadMessageBody(number, message.UTF8)
	bodyAvailable := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Printf("message %d body: %v", number, err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el cuerpo del mensaje")
		return
	}
	current, stillIndexed, err := s.findMessage(number)
	if err != nil {
		s.logger.Printf("message index after body read: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo validar el mensaje")
		return
	}
	if !stillIndexed || !sameMessageIdentity(message, current) || !messageVisibleTo(current, user) {
		writeError(w, http.StatusNotFound, "mensaje no encontrado")
		return
	}
	if !bodyAvailable {
		body = bpq.MessageBody{Routing: []string{}, Path: []string{}, Body: "", Attachments: []bpq.Attachment{}}
	}
	for index := range body.Attachments {
		body.Attachments[index].URL = fmt.Sprintf("/api/messages/%d/attachments/%d", number, body.Attachments[index].Index)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": message, "body": body, "body_available": bodyAvailable,
	})
}

func (s *Server) attachment(w http.ResponseWriter, r *http.Request, number int64, index int) {
	user, _ := currentUser(r)
	message, found, err := s.findMessage(number)
	if err != nil {
		s.logger.Printf("attachment index: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el índice de mensajes")
		return
	}
	if !found || !messageVisibleTo(message, user) {
		writeError(w, http.StatusNotFound, "mensaje no encontrado")
		return
	}
	attachment, err := s.store.ReadAttachment(number, index, message.UTF8)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "adjunto no encontrado")
			return
		}
		s.logger.Printf("attachment %d/%d: %v", number, index, err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer el adjunto")
		return
	}
	current, stillIndexed, err := s.findMessage(number)
	if err != nil {
		s.logger.Printf("attachment index after read: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo validar el mensaje")
		return
	}
	if !stillIndexed || !sameMessageIdentity(message, current) || !messageVisibleTo(current, user) {
		writeError(w, http.StatusNotFound, "mensaje no encontrado")
		return
	}
	disposition := "attachment"
	if safeInlineImage(attachment.ContentType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", attachment.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, attachment.Name, time.Time{}, bytes.NewReader(attachment.Data))
}

func (s *Server) findMessage(number int64) (bpq.Message, bool, error) {
	messages, err := s.store.Messages()
	if err != nil {
		return bpq.Message{}, false, err
	}
	for _, message := range messages {
		if message.Number == number {
			return message, true, nil
		}
	}
	return bpq.Message{}, false, nil
}

func sameMessageIdentity(left, right bpq.Message) bool {
	return left.Number == right.Number && left.Type == right.Type && left.Status == right.Status &&
		left.Date == right.Date && left.From == right.From && left.To == right.To &&
		left.BBSFrom == right.BBSFrom && left.Route == right.Route && left.BID == right.BID &&
		left.Title == right.Title && left.Size == right.Size && left.B2Flags == right.B2Flags &&
		left.UTF8 == right.UTF8 && left.CreatedAt == right.CreatedAt && left.ChangedAt == right.ChangedAt
}

func messageNumber(path string) (int64, bool) {
	part := strings.TrimPrefix(path, "/api/messages/")
	if strings.Contains(part, "/") {
		return 0, false
	}
	number, err := strconv.ParseInt(part, 10, 64)
	return number, err == nil && number > 0
}

func messageAttachmentPath(path string) (int64, int, bool, bool) {
	part := strings.TrimPrefix(path, "/api/messages/")
	segments := strings.Split(part, "/")
	if len(segments) != 3 || segments[1] != "attachments" {
		return 0, 0, false, false
	}
	number, numberErr := strconv.ParseInt(segments[0], 10, 64)
	index, indexErr := strconv.Atoi(segments[2])
	if numberErr != nil || indexErr != nil || number <= 0 || index < 0 {
		return 0, 0, true, false
	}
	return number, index, true, true
}

type composeRequest struct {
	To    string `json:"to"`
	Via   string `json:"via"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (s *Server) composeMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "sesión requerida")
		return
	}
	var request composeRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	compose := bpq.ComposeRequest{
		To: request.To, Via: request.Via, Type: strings.ToUpper(strings.TrimSpace(request.Type)),
		Title: request.Title, Body: request.Body,
	}
	if err := compose.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.AppendMail(user.Callsign, compose); err != nil {
		s.logger.Printf("append mail.in: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo encolar el mensaje")
		return
	}
	instance, err := s.store.Instance()
	if err != nil {
		s.logger.Printf("import status after send: %v", err)
	}
	delay := instance.Import.IntervalSeconds
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "delay_secs": delay, "import_configured": instance.Import.Configured,
	})
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host)
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	files, err := s.store.DecodedFiles()
	if err != nil {
		s.logger.Printf("files: %v", err)
		writeError(w, http.StatusInternalServerError, "no se pudo leer la carpeta de archivos 7+")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "" {
		writeError(w, http.StatusBadRequest, "nombre de archivo inválido")
		return
	}
	file, info, err := s.store.ReadDecodedFile(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, "archivo no encontrado")
			return
		}
		writeError(w, http.StatusBadRequest, "nombre de archivo inválido")
		return
	}
	defer file.Close()
	mimeType := detectServedMIME(file, info.Size())
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "no se pudo leer el archivo")
		return
	}
	w.Header().Set("Content-Type", mimeType)
	disposition := "attachment"
	if safeInlineImage(mimeType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func detectServedMIME(file *os.File, size int64) string {
	limit := int64(512)
	if size < limit {
		limit = size
	}
	buffer := make([]byte, limit)
	read, err := io.ReadFull(file, buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "application/octet-stream"
	}
	if read == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(buffer[:read])
}

func safeInlineImage(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	switch value {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/avif":
		return true
	default:
		return false
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("JSON inválido: se recibió más de un objeto")
		}
		return fmt.Errorf("JSON inválido: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "método no permitido")
}
