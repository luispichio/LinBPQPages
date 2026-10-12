package bpq

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func (r ComposeRequest) Validate() error {
	if r.Type != "P" && r.Type != "B" {
		return errors.New("el tipo de mensaje debe ser P o B")
	}
	if !validAddressToken(r.To) || len(r.To) > 6 {
		return errors.New("el destinatario debe ser una callsign/alias de hasta 6 caracteres")
	}
	if r.Via != "" && (!validAddressToken(r.Via) || len(r.Via) > 40) {
		return errors.New("la ruta debe ocupar como máximo 40 caracteres")
	}
	if strings.TrimSpace(r.Title) == "" || strings.ContainsAny(r.Title, "\r\n\x00") || len(r.Title) > 60 || strings.EqualFold(strings.TrimSpace(r.Title), "/EX") {
		return errors.New("el asunto debe tener entre 1 y 60 bytes y ocupar una sola línea")
	}
	for _, character := range r.Title {
		if character < 0x20 || character == 0x7f {
			return errors.New("el asunto contiene caracteres de control")
		}
	}
	if strings.ContainsRune(r.Body, '\x00') || len(r.Body) > 512*1024 {
		return errors.New("el cuerpo contiene NUL o supera el límite permitido")
	}
	for _, character := range r.Body {
		if character < 0x20 && character != '\r' && character != '\n' && character != '\t' || character == 0x7f {
			return errors.New("el cuerpo contiene caracteres de control no permitidos")
		}
	}
	for _, line := range strings.Split(normalizeNewlines(r.Body), "\n") {
		if len(line) > 64*1024 {
			return errors.New("una línea del cuerpo supera el límite de 64 KiB")
		}
		if strings.EqualFold(strings.TrimSpace(line), "/EX") {
			return errors.New("el cuerpo no puede contener una línea igual a /EX")
		}
	}
	return nil
}

func validAddressToken(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._#", character) {
			continue
		}
		return false
	}
	return true
}

// AppendMail agrega un registro completo a la cola Mail/Import/mail.in de
// LinBPQ. El lock se aplica al propio archivo para no crear archivos auxiliares
// en el directorio activo de la BBS.
func (s *Store) AppendMail(source string, request ComposeRequest) error {
	if !validAddressToken(source) || len(source) > 6 {
		return errors.New("callsign de origen inválido")
	}
	if err := request.Validate(); err != nil {
		return err
	}
	command := "SP"
	if request.Type == "B" {
		command = "SB"
	}
	var message strings.Builder
	message.Grow(len(request.To) + len(request.Via) + len(source) + len(request.Title) + len(request.Body) + 32)
	message.WriteString(command)
	message.WriteByte(' ')
	message.WriteString(request.To)
	if request.Via != "" {
		message.WriteString(" @ ")
		message.WriteString(request.Via)
	}
	message.WriteString(" < ")
	message.WriteString(source)
	message.WriteByte('\n')
	message.WriteString(request.Title)
	message.WriteByte('\n')
	message.WriteString(normalizeNewlines(request.Body))
	message.WriteString("\n/EX\n")

	mailDir := filepath.Join(s.root, "Mail")
	mailInfo, err := os.Lstat(mailDir)
	if err != nil {
		return fmt.Errorf("abrir Mail: %w", err)
	}
	if mailInfo.Mode()&os.ModeSymlink != 0 || !mailInfo.IsDir() {
		return errors.New("Mail no es un directorio regular")
	}
	queueDir := filepath.Join(mailDir, "Import")
	directoryInfo, err := os.Lstat(queueDir)
	if err != nil {
		return fmt.Errorf("abrir Mail/Import: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return errors.New("Mail/Import no es un directorio")
	}
	queuePath := filepath.Join(queueDir, "mail.in")
	s.mailMu.Lock()
	defer s.mailMu.Unlock()
	if existing, err := os.Lstat(queuePath); err == nil {
		if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() {
			return errors.New("Mail/Import/mail.in no es un archivo regular")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("verificar Mail/Import/mail.in: %w", err)
	}
	fd, err := syscall.Open(queuePath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_APPEND|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0660)
	if err != nil {
		return fmt.Errorf("abrir Mail/Import/mail.in: %w", err)
	}
	file := os.NewFile(uintptr(fd), queuePath)
	if file == nil {
		syscall.Close(fd)
		return errors.New("no se pudo crear el handle de Mail/Import/mail.in")
	}
	defer file.Close()
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() {
		return errors.New("Mail/Import/mail.in no es un archivo regular")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("bloquear Mail/Import/mail.in: %w", err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	data := []byte(message.String())
	for written := 0; written < len(data); {
		count, err := file.Write(data[written:])
		if err != nil {
			return fmt.Errorf("escribir Mail/Import/mail.in: %w", err)
		}
		if count == 0 {
			return fmt.Errorf("escribir Mail/Import/mail.in: %w", io.ErrShortWrite)
		}
		written += count
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sincronizar Mail/Import/mail.in: %w", err)
	}
	return nil
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
