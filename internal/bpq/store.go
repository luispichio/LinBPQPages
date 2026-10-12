package bpq

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	maxConfigSize = 8 << 20
	maxIndexSize  = 64 << 20
	maxMailSize   = 16 << 20
)

type fileStamp struct {
	size    int64
	modTime int64
}

func stamp(info os.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
}

type instanceConfig struct {
	info  InstanceInfo
	users map[string]User
}

type configCache struct {
	stamp fileStamp
	value instanceConfig
	valid bool
}

type messagesCache struct {
	stamp        fileStamp
	latestNumber int64
	messages     []Message
	valid        bool
}

// Store ofrece a la API una vista de solo lectura del directorio LinBPQ,
// salvo por el append de mensajes nuevos a Mail/Import/mail.in.
type Store struct {
	root       string
	configPath string
	filesPath  string
	layout     Layout

	configMu    sync.Mutex
	configCache configCache

	messagesMu    sync.Mutex
	messagesCache messagesCache

	mailMu sync.Mutex
}

func NewStore(root, configPath, filesPath string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("directorio LinBPQ vacío")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolver directorio LinBPQ: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("directorio LinBPQ: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s no es un directorio", absoluteRoot)
	}
	if configPath == "" {
		configPath = filepath.Join(absoluteRoot, "linmail.cfg")
	} else if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(absoluteRoot, configPath)
	}
	if filesPath == "" {
		filesPath = filepath.Join(absoluteRoot, "HTML", "7plus")
	} else if !filepath.IsAbs(filesPath) {
		filesPath = filepath.Join(absoluteRoot, filesPath)
	}
	absoluteConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolver linmail.cfg: %w", err)
	}
	absoluteFiles, err := filepath.Abs(filesPath)
	if err != nil {
		return nil, fmt.Errorf("resolver directorio 7+: %w", err)
	}
	store := &Store{
		root:       absoluteRoot,
		configPath: absoluteConfig,
		filesPath:  absoluteFiles,
		layout:     CurrentLayout(),
	}
	if _, err := store.loadInstanceConfig(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Layout() Layout {
	return s.layout
}

func (s *Store) loadInstanceConfig() (instanceConfig, error) {
	info, err := os.Stat(s.configPath)
	if err != nil {
		return instanceConfig{}, fmt.Errorf("stat linmail.cfg: %w", err)
	}
	if !info.Mode().IsRegular() {
		return instanceConfig{}, errors.New("linmail.cfg no es un archivo regular")
	}
	currentStamp := stamp(info)

	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.configCache.valid && s.configCache.stamp == currentStamp {
		return s.configCache.value, nil
	}

	data, err := readStableFile(s.configPath, maxConfigSize)
	if err != nil {
		if s.configCache.valid {
			log.Printf("linmail.cfg cambió durante la lectura; se conserva la configuración anterior: %v", err)
			return s.configCache.value, nil
		}
		return instanceConfig{}, fmt.Errorf("leer linmail.cfg: %w", err)
	}
	parsed, err := parseInstanceConfig(data, s.root)
	if err != nil {
		if s.configCache.valid {
			log.Printf("linmail.cfg temporalmente inválido; se conserva la configuración anterior: %v", err)
			return s.configCache.value, nil
		}
		return instanceConfig{}, fmt.Errorf("parsear linmail.cfg: %w", err)
	}
	after, err := os.Stat(s.configPath)
	if err != nil || stamp(after) != currentStamp {
		if s.configCache.valid {
			log.Printf("linmail.cfg cambió durante la lectura; se conserva la configuración anterior")
			return s.configCache.value, nil
		}
		return instanceConfig{}, errors.New("linmail.cfg cambió durante la lectura")
	}
	s.configCache = configCache{stamp: currentStamp, value: parsed, valid: true}
	return parsed, nil
}

func parseInstanceConfig(data []byte, root string) (instanceConfig, error) {
	rootObject, err := parseLibconfig(data)
	if err != nil {
		return instanceConfig{}, err
	}
	mainObject := configObject(rootObject["main"])
	usersObject, ok := rootObject["BBSUsers"]
	if !ok {
		usersObject, ok = mainObject["BBSUsers"]
	}
	if !ok {
		return instanceConfig{}, errors.New("linmail.cfg no contiene BBSUsers; BPQBBSUsers.dat (formato binario legacy) no está soportado")
	}
	userEntries := configObject(usersObject)
	if userEntries == nil {
		return instanceConfig{}, errors.New("BBSUsers en linmail.cfg no es un grupo")
	}
	users := make(map[string]User, len(userEntries))
	for configuredCall, rawValue := range userEntries {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		callsign := strings.TrimSpace(strings.TrimPrefix(configuredCall, "*"))
		if callsign == "" {
			continue
		}
		fields := strings.Split(value, "^")
		if len(fields) < 9 {
			return instanceConfig{}, fmt.Errorf("registro BBSUsers de %s tiene %d campos; se requieren al menos 9", callsign, len(fields))
		}
		flags, parseErr := parseDecimal(fields[8])
		if parseErr != nil {
			return instanceConfig{}, fmt.Errorf("flags inválidos para %s: %w", callsign, parseErr)
		}
		if flags < 0 {
			return instanceConfig{}, fmt.Errorf("flags negativos para %s", callsign)
		}
		user := User{
			Callsign: strings.ToUpper(callsign),
			Name:     fields[0],
			Address:  fields[1],
			HomeBBS:  fields[2],
			QRA:      fields[3],
			password: fields[4],
			ZIP:      fields[5],
			Sysop:    flags&UserFlagSysop != 0,
			excluded: flags&UserFlagExcluded != 0,
		}
		users[strings.ToUpper(callsign)] = user
	}

	bbsName := configString(rootObject, "BBSName")
	if bbsName == "" {
		bbsName = configString(mainObject, "BBSName")
	}
	sysopCall := configString(rootObject, "SYSOPCall")
	if sysopCall == "" {
		sysopCall = configString(mainObject, "SYSOPCall")
	}
	info := InstanceInfo{BBSName: strings.TrimSpace(bbsName), SYSOPCall: strings.TrimSpace(sysopCall)}
	settings := rootObject
	if _, ok := settings["BBSForwarding"]; !ok && mainObject != nil {
		settings = make(map[string]any, len(rootObject)+len(mainObject))
		for key, value := range rootObject {
			settings[key] = value
		}
		for key, value := range mainObject {
			if _, exists := settings[key]; !exists {
				settings[key] = value
			}
		}
	}
	info.Import = findImportConfig(settings, root)
	return instanceConfig{info: info, users: users}, nil
}

func parseDecimal(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
}

func findImportConfig(config map[string]any, root string) ImportInfo {
	forwarding := configObject(config["BBSForwarding"])
	result := ImportInfo{}
	for _, rawEntry := range forwarding {
		entry := configObject(rawEntry)
		if entry == nil || !configEnabled(entry["Enabled"]) {
			continue
		}
		if !scriptImportsMailIn(configString(entry, "ConnectScript"), root) {
			continue
		}
		result.Configured = true
		interval := configInt(entry, "FwdInterval")
		if interval > 0 && (result.IntervalSeconds == 0 || interval < result.IntervalSeconds) {
			result.IntervalSeconds = interval
		}
	}
	return result
}

func configEnabled(value any) bool {
	text, ok := value.(string)
	if !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func scriptImportsMailIn(script, root string) bool {
	for _, command := range strings.Split(script, "|") {
		fields := strings.Fields(strings.TrimSpace(command))
		if len(fields) < 2 || !strings.EqualFold(fields[0], "IMPORT") {
			continue
		}
		candidate := strings.Trim(fields[1], "\"'")
		if sameMailInPath(candidate, root) {
			return true
		}
	}
	return false
}

func sameMailInPath(candidate, root string) bool {
	candidate = strings.ReplaceAll(candidate, "\\", "/")
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(candidate)), "./")
	if strings.EqualFold(clean, "Mail/Import/mail.in") {
		return true
	}
	if filepath.IsAbs(candidate) {
		expected := filepath.Join(root, "Mail", "Import", "mail.in")
		left, errLeft := filepath.Abs(candidate)
		right, errRight := filepath.Abs(expected)
		return errLeft == nil && errRight == nil && strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return false
}

func (s *Store) Authenticate(callsign, password string) (User, bool, error) {
	config, err := s.loadInstanceConfig()
	if err != nil {
		return User{}, false, err
	}
	user, found := config.users[strings.ToUpper(callsign)]
	if !found || user.excluded || user.password == "" || user.password != password {
		return User{}, false, nil
	}
	return user, true, nil
}

func (s *Store) Instance() (InstanceInfo, error) {
	config, err := s.loadInstanceConfig()
	if err != nil {
		return InstanceInfo{}, err
	}
	return config.info, nil
}

func (s *Store) Status() (Status, error) {
	instance, err := s.Instance()
	if err != nil {
		return Status{}, err
	}
	messages, err := s.Messages()
	if err != nil {
		return Status{}, err
	}
	status := Status{
		BBSName:          instance.BBSName,
		SYSOPCall:        instance.SYSOPCall,
		ImportConfigured: instance.Import.Configured,
		DelaySeconds:     instance.Import.IntervalSeconds,
		TotalMessages:    len(messages),
		ByType:           make(map[string]int),
		ByStatus:         make(map[string]int),
	}
	for _, message := range messages {
		status.ByType[message.Type]++
		status.ByStatus[message.Status]++
	}
	s.messagesMu.Lock()
	status.LatestNumber = s.messagesCache.latestNumber
	s.messagesMu.Unlock()
	queuePath := filepath.Join(s.root, "Mail", "Import", "mail.in")
	if info, err := os.Lstat(queuePath); err == nil {
		if info.Mode().IsRegular() {
			status.QueueBytes = info.Size()
			status.QueuePending = info.Size() > 0
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("stat cola mail.in: %w", err)
	}
	return status, nil
}

func readStableFile(path string, maxSize int64) ([]byte, error) {
	file, before, err := openRegularNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if before.Size() > maxSize {
		return nil, fmt.Errorf("%s supera el límite de %d bytes", filepath.Base(path), maxSize)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("%s supera el límite de %d bytes", filepath.Base(path), maxSize)
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != after.Size() {
		return nil, errors.New("archivo cambió mientras se leía")
	}
	pathInfo, err := os.Stat(path)
	if err != nil || !os.SameFile(after, pathInfo) {
		return nil, errors.New("archivo fue reemplazado mientras se leía")
	}
	return data, nil
}
