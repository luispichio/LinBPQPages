package bpq

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

func (s *Store) Messages() ([]Message, error) {
	path := filepath.Join(s.root, "DIRMES.SYS")
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat DIRMES.SYS: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("DIRMES.SYS no es un archivo regular")
	}
	currentStamp := stamp(info)

	s.messagesMu.Lock()
	defer s.messagesMu.Unlock()
	if s.messagesCache.valid && s.messagesCache.stamp == currentStamp {
		return cloneMessages(s.messagesCache.messages), nil
	}
	data, err := readStableFile(path, maxIndexSize)
	if err != nil {
		return s.staleMessages(err)
	}
	latest, messages, err := parseDirmes(data, s.layout)
	if err != nil {
		return s.staleMessages(err)
	}
	after, err := os.Stat(path)
	if err != nil || stamp(after) != currentStamp {
		return s.staleMessages(errors.New("DIRMES.SYS cambió durante la lectura"))
	}
	s.messagesCache = messagesCache{
		stamp:        currentStamp,
		latestNumber: latest,
		messages:     messages,
		valid:        true,
	}
	return cloneMessages(messages), nil
}

func (s *Store) staleMessages(cause error) ([]Message, error) {
	if s.messagesCache.valid {
		log.Printf("DIRMES.SYS temporalmente ilegible; se sirve la última lectura válida: %v", cause)
		return cloneMessages(s.messagesCache.messages), nil
	}
	return nil, fmt.Errorf("leer DIRMES.SYS: %w", cause)
}

func parseDirmes(data []byte, layout Layout) (int64, []Message, error) {
	if layout.RecordSize <= 0 || len(data)%layout.RecordSize != 0 {
		return 0, nil, fmt.Errorf("DIRMES.SYS tiene %d bytes; el formato esperado usa registros de %d bytes", len(data), layout.RecordSize)
	}
	if len(data) < layout.RecordSize {
		return 0, nil, errors.New("DIRMES.SYS no contiene el registro de control")
	}
	control := data[:layout.RecordSize]
	if control[layout.Status.Offset] != 2 {
		return 0, nil, fmt.Errorf("registro de control DIRMES.SYS inválido (status=%d)", control[layout.Status.Offset])
	}
	messageCount := readSigned(control, layout.Number)
	latestNumber := readSigned(control, layout.Length)
	if messageCount < 0 || messageCount != int64(len(data)/layout.RecordSize-1) {
		return 0, nil, fmt.Errorf("registro de control indica %d mensajes, pero el archivo contiene %d registros", messageCount, len(data)/layout.RecordSize-1)
	}
	if latestNumber < 0 {
		return 0, nil, fmt.Errorf("último número de mensaje inválido: %d", latestNumber)
	}

	messages := make([]Message, 0, messageCount)
	for offset := layout.RecordSize; offset < len(data); offset += layout.RecordSize {
		record := data[offset : offset+layout.RecordSize]
		typeByte := record[layout.Type.Offset]
		statusByte := record[layout.Status.Offset]
		// La API JSON de LinBPQ también ignora registros con type o status NUL.
		if typeByte == 0 || statusByte == 0 || typeByte < 0x20 || typeByte > 0x7e || statusByte < 0x20 || statusByte > 0x7e {
			continue
		}
		utf8Text := record[layout.UTF8.Offset] != 0
		message := Message{
			Type:      string(typeByte),
			Status:    string(statusByte),
			Number:    readSigned(record, layout.Number),
			Size:      readSigned(record, layout.Length),
			Date:      readSigned(record, layout.DateReceived),
			BBSFrom:   readText(record, layout.BBSFrom),
			Route:     readText(record, layout.Via),
			From:      readText(record, layout.From),
			To:        readText(record, layout.To),
			BID:       readText(record, layout.BID),
			Title:     readText(record, layout.Title),
			B2Flags:   record[layout.B2Flags.Offset],
			UTF8:      utf8Text,
			CreatedAt: readSigned(record, layout.DateCreated),
			ChangedAt: readSigned(record, layout.DateChanged),
		}
		if message.Number <= 0 {
			continue
		}
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Number > messages[j].Number
	})
	return latestNumber, messages, nil
}

func readSigned(record []byte, field Field) int64 {
	value := record[field.Offset : field.Offset+field.Size]
	switch field.Size {
	case 1:
		return int64(value[0])
	case 2:
		return int64(binary.LittleEndian.Uint16(value))
	case 4:
		return int64(int32(binary.LittleEndian.Uint32(value)))
	case 8:
		return int64(binary.LittleEndian.Uint64(value))
	default:
		return 0
	}
}

// readText extrae un campo de texto del índice. El indicador UTF8 no decide la
// codificación: se detecta el contenido igual que en los cuerpos de mensaje,
// porque LinBPQ no lo marca al importar mensajes UTF-8.
func readText(record []byte, field Field) string {
	value := record[field.Offset : field.Offset+field.Size]
	for index, character := range value {
		if character == 0 {
			value = value[:index]
			break
		}
	}
	value = bytesTrimRight(value, " \t\r\n")
	return decodeAuto(value)
}

func cloneMessages(messages []Message) []Message {
	copyOfMessages := make([]Message, len(messages))
	copy(copyOfMessages, messages)
	return copyOfMessages
}
