package bpq

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
)

const maxMIMEParts = 128

type mimeParts struct {
	plainText []string
	htmlText  []string
	files     []Attachment
}

func (s *Store) ReadMessageBody(number int64) (MessageBody, error) {
	if number <= 0 || number > int64(^uint32(0)>>1) {
		return MessageBody{}, os.ErrNotExist
	}
	path := filepath.Join(s.root, "Mail", fmt.Sprintf("m_%06d.mes", number))
	info, err := os.Lstat(path)
	if err != nil {
		return MessageBody{}, fmt.Errorf("leer mensaje %d: %w", number, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return MessageBody{}, fmt.Errorf("mensaje %d no es un archivo regular", number)
	}
	data, err := readStableFile(path, maxMailSize)
	if err != nil {
		return MessageBody{}, fmt.Errorf("leer mensaje %d: %w", number, err)
	}
	return parseMessageBody(data)
}

func parseMessageBody(data []byte) (MessageBody, error) {
	normalized := normalizeMessageBytes(data)
	lines := bytes.Split(normalized, []byte{'\n'})
	routing := make([]string, 0)
	bodyStart := 0
	for bodyStart < len(lines) && bytes.HasPrefix(lines[bodyStart], []byte("R:")) {
		routing = append(routing, decodeTextBytes(lines[bodyStart], ""))
		bodyStart++
	}
	if bodyStart < len(lines) && len(lines[bodyStart]) == 0 {
		bodyStart++
	}
	bodyBytes := bytes.Join(lines[bodyStart:], []byte{'\n'})
	bodyBytes = bytes.TrimSuffix(bodyBytes, []byte{'\n'})

	result := MessageBody{
		Routing: routing,
		Path:    routingPath(routing),
		Body:    "",
	}
	if len(bytes.TrimSpace(bodyBytes)) == 0 {
		return result, nil
	}

	if !looksLikeMailMessage(bodyBytes) {
		result.Body = decodeTextBytes(bodyBytes, "")
		return result, nil
	}
	message, err := mail.ReadMessage(bufio.NewReader(bytes.NewReader(bodyBytes)))
	if err != nil {
		// Una cabecera RFC822 malformada no debe volver ilegible el mensaje;
		// mostrar el contenido como texto plano.
		result.Body = decodeTextBytes(bodyBytes, "")
		return result, nil
	}
	entityBody, err := io.ReadAll(io.LimitReader(message.Body, maxMailSize+1))
	if err != nil || int64(len(entityBody)) > maxMailSize {
		return MessageBody{}, errors.New("cuerpo MIME supera el límite permitido")
	}
	parts := mimeParts{}
	if err := walkMIME(textproto.MIMEHeader(message.Header), entityBody, &parts, 0); err != nil {
		return MessageBody{}, fmt.Errorf("parsear partes MIME: %w", err)
	}
	if len(parts.plainText) > 0 {
		result.Body = strings.Join(parts.plainText, "\n\n")
	} else if len(parts.htmlText) > 0 {
		// La interfaz lo asigna a textContent dentro de <pre>, nunca como HTML.
		result.Body = strings.Join(parts.htmlText, "\n\n")
	}
	result.Attachments = parts.files
	return result, nil
}

func normalizeMessageBytes(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
}

func looksLikeMailMessage(data []byte) bool {
	known := map[string]bool{
		"content-type": true, "mime-version": true, "from": true, "to": true,
		"subject": true, "date": true, "received": true, "message-id": true,
		"reply-to": true, "sender": true, "cc": true, "content-transfer-encoding": true,
		"mid": true, "body": true,
	}
	for _, line := range bytes.SplitN(data, []byte{'\n'}, 64) {
		if len(line) == 0 {
			break
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			// Las continuaciones solo son válidas después de una cabecera y por
			// sí solas no clasifican un mensaje de texto plano.
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(line[:colon])))
		if known[name] {
			return true
		}
	}
	return false
}

func walkMIME(header textproto.MIMEHeader, raw []byte, result *mimeParts, depth int) error {
	if depth > 8 {
		return errors.New("anidamiento MIME demasiado profundo")
	}
	contentType := header.Get("Content-Type")
	mediaType := "text/plain"
	params := map[string]string{}
	if contentType != "" {
		parsedType, parsedParams, err := mime.ParseMediaType(contentType)
		if err == nil {
			mediaType = strings.ToLower(parsedType)
			params = parsedParams
		} else {
			mediaType = "application/octet-stream"
		}
	}
	decoded, err := decodeTransferEncoding(raw, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return errors.New("multipart sin boundary")
		}
		reader := multipart.NewReader(bytes.NewReader(decoded), boundary)
		for count := 0; ; count++ {
			part, partErr := reader.NextRawPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				return partErr
			}
			if count >= maxMIMEParts {
				part.Close()
				return fmt.Errorf("mensaje supera el límite de %d partes MIME", maxMIMEParts)
			}
			partData, readErr := io.ReadAll(io.LimitReader(part, maxMailSize+1))
			part.Close()
			if readErr != nil {
				return readErr
			}
			if int64(len(partData)) > maxMailSize {
				return errors.New("parte MIME supera el límite permitido")
			}
			if err := walkMIME(part.Header, partData, result, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	disposition, dispositionParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	attachment := strings.EqualFold(disposition, "attachment") || filename != ""
	if strings.HasPrefix(mediaType, "text/") && !attachment {
		text := decodeTextBytes(decoded, params["charset"])
		if mediaType == "text/plain" {
			result.plainText = append(result.plainText, text)
		} else if mediaType == "text/html" {
			result.htmlText = append(result.htmlText, text)
		}
		return nil
	}
	if !attachment && (strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "application/")) {
		attachment = true
	}
	if !attachment {
		return nil
	}
	if filename == "" {
		filename = fmt.Sprintf("adjunto-%d", len(result.files)+1)
	}
	filename = safeAttachmentName(filename, len(result.files)+1)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	result.files = append(result.files, Attachment{
		Index:       len(result.files),
		Name:        filename,
		ContentType: mediaType,
		Size:        int64(len(decoded)),
		Data:        decoded,
	})
	return nil
}

func decodeTransferEncoding(data []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return append([]byte(nil), data...), nil
	case "base64":
		decoder := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(data))
		decoded, err := io.ReadAll(io.LimitReader(decoder, maxMailSize+1))
		if err != nil {
			return nil, fmt.Errorf("decodificar base64: %w", err)
		}
		if int64(len(decoded)) > maxMailSize {
			return nil, errors.New("contenido base64 supera el límite permitido")
		}
		return decoded, nil
	case "quoted-printable":
		decoded, err := io.ReadAll(io.LimitReader(quotedprintable.NewReader(bytes.NewReader(data)), maxMailSize+1))
		if err != nil {
			return nil, fmt.Errorf("decodificar quoted-printable: %w", err)
		}
		if int64(len(decoded)) > maxMailSize {
			return nil, errors.New("contenido quoted-printable supera el límite permitido")
		}
		return decoded, nil
	default:
		// Mantener la representación almacenada de una codificación desconocida
		// en vez de descartar el contenido.
		return append([]byte(nil), data...), nil
	}
}

// decodeTextBytes convierte texto a UTF-8. Un charset MIME declarado manda;
// sin declaración (o con una desconocida) se detecta el contenido con
// decodeAuto, igual que el webmail de LinBPQ.
func decodeTextBytes(data []byte, charset string) string {
	charset = strings.ToLower(strings.TrimSpace(strings.Trim(charset, "\"'")))
	switch charset {
	case "utf-8", "utf8":
		return validUTF8(data)
	case "us-ascii", "ascii", "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		return decodeWindows1252(data)
	case "cp437", "ibm437", "437":
		return decodeCP437(data)
	case "cp1251", "windows-1251":
		return decodeCP1251(data)
	default:
		return decodeAuto(data)
	}
}

func safeAttachmentName(name string, index int) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return -1
		}
		return character
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return fmt.Sprintf("adjunto-%d", index+1)
	}
	return name
}

func routingPath(routing []string) []string {
	path := make([]string, 0, len(routing))
	for _, line := range routing {
		at := strings.IndexByte(line, '@')
		if at < 0 {
			continue
		}
		address := strings.TrimPrefix(line[at+1:], ":")
		end := 0
		for end < len(address) {
			character := address[end]
			if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' {
				end++
				continue
			}
			break
		}
		if end > 0 {
			path = append(path, strings.ToUpper(address[:end]))
		}
	}
	return path
}

func (s *Store) ReadAttachment(number int64, index int) (Attachment, error) {
	if index < 0 {
		return Attachment{}, os.ErrNotExist
	}
	body, err := s.ReadMessageBody(number)
	if err != nil {
		return Attachment{}, err
	}
	if index >= len(body.Attachments) {
		return Attachment{}, os.ErrNotExist
	}
	return body.Attachments[index], nil
}
